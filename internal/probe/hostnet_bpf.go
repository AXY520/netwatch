package probe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
	"golang.org/x/sys/unix"
)

const hostBPFMapPinDirName = "netwatch"

// hostCgroupBPFManager owns the eBPF programs used for host-network container
// accounting. Programs and cgroup links are process-owned; the two counter
// maps are pinned in host bpffs so a Netwatch restart does not erase history.
type hostCgroupBPFManager struct {
	mu          sync.Mutex
	loaded      bool
	loadErr     error
	rxMap       *ebpf.Map
	txMap       *ebpf.Map
	ingressProg *ebpf.Program
	egressProg  *ebpf.Program
	links       map[string]hostCgroupBPFAttachment
	mapPinPath  string
	pinNote     string
}

type hostCgroupBPFAttachment struct {
	cgroupID uint64
	links    []link.Link
}

type hostCgroupBPFRead struct {
	Available  bool
	Attached   bool
	RxBytes    uint64
	TxBytes    uint64
	RxPackets  uint64
	TxPackets  uint64
	CgroupID   uint64
	Path       string
	Note       string
	Diagnostic string
}

type bpfTrafficValue struct {
	Bytes   uint64
	Packets uint64
}

var hostBPF = &hostCgroupBPFManager{}

// resetHostCgroupBPF detaches all process-owned host accounting programs when
// the service exits.
func resetHostCgroupBPF() {
	hostBPF.mu.Lock()
	defer hostBPF.mu.Unlock()
	for path, attachment := range hostBPF.links {
		for _, attached := range attachment.links {
			_ = attached.Close()
		}
		delete(hostBPF.links, path)
	}
	if hostBPF.ingressProg != nil {
		_ = hostBPF.ingressProg.Close()
	}
	if hostBPF.egressProg != nil {
		_ = hostBPF.egressProg.Close()
	}
	if hostBPF.rxMap != nil {
		_ = hostBPF.rxMap.Close()
	}
	if hostBPF.txMap != nil {
		_ = hostBPF.txMap.Close()
	}
	hostBPF.links = nil
	hostBPF.ingressProg = nil
	hostBPF.egressProg = nil
	hostBPF.rxMap = nil
	hostBPF.txMap = nil
	hostBPF.mapPinPath = ""
	hostBPF.pinNote = ""
	hostBPF.loaded = false
	hostBPF.loadErr = nil
}

func hostCgroupBPFState() (bool, error) {
	hostBPF.mu.Lock()
	defer hostBPF.mu.Unlock()
	return hostBPF.loaded, hostBPF.loadErr
}

func readHostCgroupBPFStats(cgroupPath string) hostCgroupBPFRead {
	if cgroupPath == "" {
		return hostCgroupBPFRead{Note: "未找到容器 cgroup 路径"}
	}
	if _, err := os.Stat(cgroupPath); err != nil {
		return hostCgroupBPFRead{Path: cgroupPath, Note: err.Error()}
	}
	return hostBPF.read(cgroupPath)
}

func (m *hostCgroupBPFManager) read(cgroupPath string) hostCgroupBPFRead {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureLoaded(); err != nil {
		return hostCgroupBPFRead{Path: cgroupPath, Note: err.Error()}
	}
	cgroupID, err := cgroupIDFromPath(cgroupPath)
	if err != nil {
		return hostCgroupBPFRead{Path: cgroupPath, Note: err.Error()}
	}
	if err := m.ensureAttached(cgroupPath, cgroupID); err != nil {
		return hostCgroupBPFRead{Path: cgroupPath, CgroupID: cgroupID, Note: err.Error()}
	}

	var rx, tx bpfTrafficValue
	rxErr := m.rxMap.Lookup(cgroupID, &rx)
	txErr := m.txMap.Lookup(cgroupID, &tx)
	if rxErr != nil && !errors.Is(rxErr, ebpf.ErrKeyNotExist) {
		return hostCgroupBPFRead{Path: cgroupPath, CgroupID: cgroupID, Attached: true, Note: "读取 ingress BPF map 失败: " + rxErr.Error()}
	}
	if txErr != nil && !errors.Is(txErr, ebpf.ErrKeyNotExist) {
		return hostCgroupBPFRead{Path: cgroupPath, CgroupID: cgroupID, Attached: true, Note: "读取 egress BPF map 失败: " + txErr.Error()}
	}
	note := "eBPF 已附着到容器 cgroup；计数从 netwatch 本次启动/首次附着开始累计。"
	if m.mapPinPath != "" {
		note = "eBPF 已附着到容器 cgroup；计数 map 持久化在 " + m.mapPinPath + "。"
	} else if m.pinNote != "" {
		note += " " + m.pinNote
	}
	if errors.Is(rxErr, ebpf.ErrKeyNotExist) && errors.Is(txErr, ebpf.ErrKeyNotExist) {
		note = "eBPF 已附着，等待该 cgroup 产生网络包。"
		if m.mapPinPath != "" {
			note += "计数 map 持久化在 " + m.mapPinPath + "。"
		} else if m.pinNote != "" {
			note += " " + m.pinNote
		}
	}
	return hostCgroupBPFRead{
		Available:  true,
		Attached:   true,
		RxBytes:    rx.Bytes,
		TxBytes:    tx.Bytes,
		RxPackets:  rx.Packets,
		TxPackets:  tx.Packets,
		CgroupID:   cgroupID,
		Path:       cgroupPath,
		Note:       note,
		Diagnostic: strings.TrimSpace(m.pinNote),
	}
}

func (m *hostCgroupBPFManager) ensureLoaded() error {
	if m.loaded {
		return nil
	}
	if m.loadErr != nil {
		return m.loadErr
	}
	if systemCgroupV2Root() == "" {
		m.loadErr = errors.New("未检测到 cgroup v2")
		return m.loadErr
	}
	// Newer kernels account BPF memory via memcg, while older ones still need a
	// higher memlock limit. This best-effort call avoids blocking modern boxes
	// where the rlimit operation itself is denied inside the container.
	_ = rlimit.RemoveMemlock()

	rxMap, pinPath, pinNote, err := newHostCgroupMap("netwatch_cgrp_rx")
	if err != nil {
		m.loadErr = fmt.Errorf("创建 ingress BPF map 失败: %w", err)
		return m.loadErr
	}
	txMap, txPinPath, txPinNote, err := newHostCgroupMap("netwatch_cgrp_tx")
	if err != nil {
		_ = rxMap.Close()
		m.loadErr = fmt.Errorf("创建 egress BPF map 失败: %w", err)
		return m.loadErr
	}
	ingressProg, err := ebpf.NewProgram(hostCgroupProgramSpec("netwatch_cgrp_in", ebpf.AttachCGroupInetIngress, rxMap))
	if err != nil {
		_ = rxMap.Close()
		_ = txMap.Close()
		m.loadErr = fmt.Errorf("加载 ingress cgroup eBPF 失败: %w", err)
		return m.loadErr
	}
	egressProg, err := ebpf.NewProgram(hostCgroupProgramSpec("netwatch_cgrp_out", ebpf.AttachCGroupInetEgress, txMap))
	if err != nil {
		_ = ingressProg.Close()
		_ = rxMap.Close()
		_ = txMap.Close()
		m.loadErr = fmt.Errorf("加载 egress cgroup eBPF 失败: %w", err)
		return m.loadErr
	}

	m.rxMap = rxMap
	m.txMap = txMap
	m.ingressProg = ingressProg
	m.egressProg = egressProg
	m.links = map[string]hostCgroupBPFAttachment{}
	if pinPath != "" && pinPath == txPinPath {
		m.mapPinPath = pinPath
	} else if pinPath != "" && txPinPath != "" {
		m.mapPinPath = filepath.Dir(pinPath)
	}
	m.pinNote = joinHostBPFPinNotes(pinNote, txPinNote)
	m.loaded = true
	return nil
}

func joinHostBPFPinNotes(notes ...string) string {
	unique := make([]string, 0, len(notes))
	seen := make(map[string]struct{}, len(notes))
	for _, note := range notes {
		note = strings.TrimSpace(note)
		if note == "" {
			continue
		}
		if _, ok := seen[note]; ok {
			continue
		}
		seen[note] = struct{}{}
		unique = append(unique, note)
	}
	return strings.Join(unique, "；")
}

func newHostCgroupMap(name string) (*ebpf.Map, string, string, error) {
	spec := hostCgroupMapSpec(name)
	if root, ok := hostBPFMapPinRoot(); ok {
		mapPath := filepath.Join(root, name)
		spec.Pinning = ebpf.PinByName
		mapped, err := ebpf.NewMapWithOptions(spec, ebpf.MapOptions{PinPath: root})
		if err == nil {
			return mapped, mapPath, "", nil
		}
		// A visible bpffs mount does not guarantee that this container can pin
		// objects there. Keep Host accounting usable with an ephemeral map and
		// expose the persistence loss in the diagnostic instead of disabling the
		// whole read-only feature.
		fallback, fallbackErr := ebpf.NewMap(hostCgroupMapSpec(name))
		if fallbackErr != nil {
			return nil, "", "", fmt.Errorf("创建临时 BPF map 失败（pin 失败: %v）: %w", err, fallbackErr)
		}
		return fallback, "", "bpffs map 持久化失败，Host 统计将在 Netwatch 重启时重置：" + err.Error(), nil
	}
	mapObj, err := ebpf.NewMap(spec)
	if err != nil {
		return nil, "", "", err
	}
	return mapObj, "", "未找到可写 bpffs，Host 统计 map 将在 Netwatch 重启时重置", nil
}

func hostBPFMapPinRoot() (string, bool) {
	return hostBPFMapPinRootAt(
		[]string{"/sys/fs/bpf", "/host/sys/fs/bpf"},
		os.MkdirAll,
		unix.Statfs,
	)
}

// hostBPFMapPinRootAt is kept small and injectable for tests; production uses
// hostBPFMapPinRoot so the root filesystem selection remains centralized.
func hostBPFMapPinRootAt(candidates []string, mkdir func(string, os.FileMode) error, statfs func(string, *unix.Statfs_t) error) (string, bool) {
	for _, candidate := range candidates {
		var stat unix.Statfs_t
		if err := statfs(candidate, &stat); err != nil || uint64(stat.Type) != 0xcafe4a11 {
			continue
		}
		root := filepath.Join(candidate, hostBPFMapPinDirName)
		if err := mkdir(root, 0o700); err != nil {
			continue
		}
		return root, true
	}
	return "", false
}

func (m *hostCgroupBPFManager) ensureAttached(cgroupPath string, cgroupID uint64) error {
	if current, ok := m.links[cgroupPath]; ok {
		if current.cgroupID == cgroupID && len(current.links) > 0 {
			return nil
		}
		remaining, err := closeHostCgroupLinks(current.links)
		if err != nil {
			current.links = remaining
			m.links[cgroupPath] = current
			return fmt.Errorf("detach replaced host cgroup %s: %w", cgroupPath, err)
		}
		delete(m.links, cgroupPath)
	}
	in, err := link.AttachCgroup(link.CgroupOptions{
		Path:    cgroupPath,
		Attach:  ebpf.AttachCGroupInetIngress,
		Program: m.ingressProg,
	})
	if err != nil {
		return fmt.Errorf("附着 ingress cgroup eBPF 失败: %w", err)
	}
	out, err := link.AttachCgroup(link.CgroupOptions{
		Path:    cgroupPath,
		Attach:  ebpf.AttachCGroupInetEgress,
		Program: m.egressProg,
	})
	if err != nil {
		_ = in.Close()
		return fmt.Errorf("附着 egress cgroup eBPF 失败: %w", err)
	}
	m.links[cgroupPath] = hostCgroupBPFAttachment{cgroupID: cgroupID, links: []link.Link{in, out}}
	return nil
}

func pruneHostCgroupBPF(active map[string]uint64) error {
	return hostBPF.prune(active)
}

func (m *hostCgroupBPFManager) prune(active map[string]uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.loaded {
		return nil
	}

	mapKeys := make([]uint64, 0)
	seenKeys := make(map[uint64]struct{})
	for _, counterMap := range []*ebpf.Map{m.rxMap, m.txMap} {
		keys, err := hostCgroupBPFMapKeys(counterMap)
		if err != nil {
			return err
		}
		for _, key := range keys {
			if _, seen := seenKeys[key]; seen {
				continue
			}
			seenKeys[key] = struct{}{}
			mapKeys = append(mapKeys, key)
		}
	}

	stalePaths, staleIDs := hostCgroupBPFPrunePlan(m.links, mapKeys, active)
	var cleanupErrors []error
	for _, path := range stalePaths {
		attachment := m.links[path]
		remaining, err := closeHostCgroupLinks(attachment.links)
		if err != nil {
			attachment.links = remaining
			m.links[path] = attachment
			cleanupErrors = append(cleanupErrors, fmt.Errorf("detach stale host cgroup %s: %w", path, err))
			continue
		}
		delete(m.links, path)
	}
	for _, cgroupID := range staleIDs {
		for _, counterMap := range []*ebpf.Map{m.rxMap, m.txMap} {
			if counterMap == nil {
				continue
			}
			if err := counterMap.Delete(cgroupID); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("delete stale host cgroup map key %d: %w", cgroupID, err))
			}
		}
	}
	return errors.Join(cleanupErrors...)
}

func closeHostCgroupLinks(links []link.Link) ([]link.Link, error) {
	remaining := make([]link.Link, 0)
	var closeErrors []error
	for _, attached := range links {
		if err := attached.Close(); err != nil {
			remaining = append(remaining, attached)
			closeErrors = append(closeErrors, err)
		}
	}
	return remaining, errors.Join(closeErrors...)
}

func hostCgroupBPFMapKeys(counterMap *ebpf.Map) ([]uint64, error) {
	if counterMap == nil {
		return nil, nil
	}
	iterator := counterMap.Iterate()
	keys := make([]uint64, 0)
	var key uint64
	var value bpfTrafficValue
	for iterator.Next(&key, &value) {
		keys = append(keys, key)
	}
	if err := iterator.Err(); err != nil {
		return nil, fmt.Errorf("iterate host cgroup counter map: %w", err)
	}
	return keys, nil
}

func hostCgroupBPFPrunePlan(attachments map[string]hostCgroupBPFAttachment, mapKeys []uint64, active map[string]uint64) ([]string, []uint64) {
	stalePaths := make([]string, 0)
	for path, attachment := range attachments {
		activeID, ok := active[path]
		if !ok || activeID == 0 || activeID != attachment.cgroupID {
			stalePaths = append(stalePaths, path)
		}
	}
	activeIDs := make(map[uint64]struct{}, len(active))
	for _, cgroupID := range active {
		if cgroupID != 0 {
			activeIDs[cgroupID] = struct{}{}
		}
	}
	staleIDs := make([]uint64, 0)
	for _, cgroupID := range mapKeys {
		if _, ok := activeIDs[cgroupID]; !ok {
			staleIDs = append(staleIDs, cgroupID)
		}
	}
	sort.Strings(stalePaths)
	sort.Slice(staleIDs, func(i, j int) bool { return staleIDs[i] < staleIDs[j] })
	return stalePaths, staleIDs
}

func hostCgroupMapSpec(name string) *ebpf.MapSpec {
	return &ebpf.MapSpec{
		Name:       name,
		Type:       ebpf.Hash,
		KeySize:    8,
		ValueSize:  16,
		MaxEntries: 4096,
	}
}

func hostCgroupProgramSpec(name string, attach ebpf.AttachType, statsMap *ebpf.Map) *ebpf.ProgramSpec {
	return &ebpf.ProgramSpec{
		Name:         name,
		Type:         ebpf.CGroupSKB,
		AttachType:   attach,
		License:      "GPL",
		Instructions: hostCgroupProgramInstructions(attach, statsMap.FD()),
	}
}

func hostCgroupProgramInstructions(attach ebpf.AttachType, statsMapFD int) asm.Instructions {
	instructions := asm.Instructions{
		// R6 keeps the __sk_buff context across helper calls.
		asm.Mov.Reg(asm.R6, asm.R1),
	}
	instructions = append(instructions, cgroupIDInstructions(attach)...)
	instructions = append(instructions,
		asm.StoreMem(asm.RFP, -8, asm.R0, asm.DWord),

		// value = stats_map.Lookup(&key)
		asm.LoadMapPtr(asm.R1, statsMapFD),
		asm.Mov.Reg(asm.R2, asm.RFP),
		asm.Add.Imm(asm.R2, -8),
		asm.FnMapLookupElem.Call(),
		asm.JEq.Imm(asm.R0, 0, "init"),

		// value->bytes += skb->len
		asm.Mov.Reg(asm.R7, asm.R0),
		asm.LoadMem(asm.R1, asm.R6, 0, asm.Word),
		asm.StoreXAdd(asm.R7, asm.R1, asm.DWord),

		// value->packets += 1
		asm.Add.Imm(asm.R7, 8),
		asm.Mov.Imm(asm.R1, 1),
		asm.StoreXAdd(asm.R7, asm.R1, asm.DWord),
		asm.Ja.Label("allow"),

		// First packet for this cgroup: insert {bytes: skb->len, packets: 1}.
		asm.LoadMem(asm.R1, asm.R6, 0, asm.Word).WithSymbol("init"),
		asm.StoreMem(asm.RFP, -24, asm.R1, asm.DWord),
		asm.Mov.Imm(asm.R2, 1),
		asm.StoreMem(asm.RFP, -16, asm.R2, asm.DWord),
		asm.LoadMapPtr(asm.R1, statsMapFD),
		asm.Mov.Reg(asm.R2, asm.RFP),
		asm.Add.Imm(asm.R2, -8),
		asm.Mov.Reg(asm.R3, asm.RFP),
		asm.Add.Imm(asm.R3, -24),
		asm.Mov.Imm(asm.R4, 0),
		asm.FnMapUpdateElem.Call(),

		// CGroupSKB programs must return 1 to allow traffic through.
		asm.Mov.Imm(asm.R0, 1).WithSymbol("allow"),
		asm.Return(),
	)
	return instructions
}

func cgroupIDInstructions(attach ebpf.AttachType) asm.Instructions {
	if attach == ebpf.AttachCGroupInetIngress {
		return asm.Instructions{asm.FnGetCurrentCgroupId.Call()}
	}
	return asm.Instructions{asm.Mov.Reg(asm.R1, asm.R6), asm.FnSkbCgroupId.Call()}
}

func hostCgroupPath(procRoot string, cgroup string) string {
	root := systemCgroupV2Root()
	if root == "" {
		return ""
	}
	if procRoot == "/proc" && strings.HasPrefix(root, "/host/") {
		root = "/sys/fs/cgroup"
	}
	cgroup = strings.TrimSpace(cgroup)
	if cgroup == "" {
		return ""
	}
	cgroup = strings.TrimPrefix(cgroup, "/")
	path := filepath.Join(root, cgroup)
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		return path
	}
	return ""
}

func cgroupIDFromPath(path string) (uint64, error) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return 0, err
	}
	return st.Ino, nil
}
