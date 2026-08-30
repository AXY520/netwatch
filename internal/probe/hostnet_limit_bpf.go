package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilium/ebpf"
	ebpflink "github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
	tcnetlink "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"netwatch/internal/logger"
)

const (
	hostTrafficLimitBPFPreference    = uint16(49160)
	hostTrafficLimitPolicePreference = uint16(49161)
	hostTrafficLimitMarkMask         = uint32(0x0fff0000)
	hostTrafficLimitMaxSlot          = uint32(4095)
)

type hostTrafficLimitTarget struct {
	device          string
	cgroupPaths     []string
	bridgeIfindexes []uint32
}

type hostTrafficLimitInstance struct {
	appID           string
	slot            uint32
	device          string
	limit           AppTrafficLimit
	generation      uint64
	cgroupPaths     []string
	bridgeIfindexes []uint32
	objects         hostlimitObjects
	cgroupLinks     map[string]ebpflink.Link
}

type hostTrafficLimiter struct {
	mu             sync.Mutex
	apps           map[string]*hostTrafficLimitInstance
	cleanedDevices map[string]bool
	nextGeneration uint64
	runtime        map[string]appTrafficLimitRuntime
}

func newHostTrafficLimiter() *hostTrafficLimiter {
	return &hostTrafficLimiter{
		apps:           make(map[string]*hostTrafficLimitInstance),
		cleanedDevices: make(map[string]bool),
		runtime:        make(map[string]appTrafficLimitRuntime),
	}
}

func hostTrafficLimitAvailable() bool {
	if systemCgroupV2Root() == "" {
		return false
	}
	_, err := defaultHostTrafficDevice()
	return err == nil
}

func defaultHostTrafficDevice() (string, error) {
	v4 := strings.TrimSpace(readDefaultIPv4Route().Interface)
	v6 := strings.TrimSpace(readDefaultIPv6Route().Interface)
	if v4 != "" && v6 != "" && v4 != v6 {
		return "", fmt.Errorf("IPv4/IPv6 default routes use different devices (%s/%s)", v4, v6)
	}
	device := v4
	if device == "" {
		device = v6
	}
	if device == "" {
		return "", errors.New("no default-route network device")
	}
	if _, err := net.InterfaceByName(device); err != nil {
		return "", fmt.Errorf("default-route device %s is unavailable: %w", device, err)
	}
	return device, nil
}

func resolveHostTrafficLimitTarget(appID string, targets []AppNetworkTarget) (hostTrafficLimitTarget, error) {
	device, err := defaultHostTrafficDevice()
	if err != nil {
		return hostTrafficLimitTarget{}, err
	}
	parent, err := hostAppCgroupPath(appID)
	if err != nil {
		return hostTrafficLimitTarget{}, err
	}
	root := systemCgroupV2Root()
	if root == "" {
		return hostTrafficLimitTarget{}, errors.New("cgroup v2 is unavailable")
	}
	cgroupPaths := make([]string, 0, 2)
	for _, relative := range hostFirewallCgroupPaths(parent) {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
			cgroupPaths = append(cgroupPaths, path)
		}
	}
	cgroupPaths = uniqueExistingCgroupPaths(cgroupPaths)
	if len(cgroupPaths) == 0 {
		return hostTrafficLimitTarget{}, fmt.Errorf("host application %s has no existing app cgroup", appID)
	}

	bridgeIfindexes := make([]uint32, 0)
	for _, target := range targets {
		if target.Kind != AppNetworkTargetBridge || strings.TrimSpace(target.Interface) == "" {
			continue
		}
		bridge, lookupErr := net.InterfaceByName(target.Interface)
		if lookupErr != nil {
			return hostTrafficLimitTarget{}, fmt.Errorf("application bridge %s is unavailable: %w", target.Interface, lookupErr)
		}
		bridgeIfindexes = append(bridgeIfindexes, uint32(bridge.Index))
	}
	sort.Slice(bridgeIfindexes, func(i, j int) bool { return bridgeIfindexes[i] < bridgeIfindexes[j] })
	bridgeIfindexes = slices.Compact(bridgeIfindexes)
	return hostTrafficLimitTarget{device: device, cgroupPaths: cgroupPaths, bridgeIfindexes: bridgeIfindexes}, nil
}

func uniqueExistingCgroupPaths(paths []string) []string {
	unique := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		unique[path] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for path := range unique {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func (m *hostTrafficLimiter) nextGenerationLocked() uint64 {
	m.nextGeneration++
	if m.nextGeneration == 0 {
		m.nextGeneration++
	}
	return m.nextGeneration
}

func (m *hostTrafficLimiter) allocateSlotLocked() (uint32, error) {
	used := make(map[uint32]bool, len(m.apps))
	for _, instance := range m.apps {
		used[instance.slot] = true
	}
	for slot := uint32(1); slot <= hostTrafficLimitMaxSlot; slot++ {
		if !used[slot] {
			return slot, nil
		}
	}
	return 0, errors.New("too many Host/Mixed application traffic limits")
}

func (m *hostTrafficLimiter) apply(_ context.Context, appID string, targets []AppNetworkTarget, limit AppTrafficLimit) error {
	if _, err := hostLimitConfig(limit, 1, 1); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit.UploadKbps == 0 && limit.DownloadKbps == 0 {
		err := m.closeAppLocked(appID)
		m.runtime[appID] = appTrafficLimitRuntime{Desired: limit, InSync: err == nil, Diagnostic: errorText(err), CheckedAt: time.Now()}
		return err
	}
	target, err := resolveHostTrafficLimitTarget(appID, targets)
	if err != nil {
		m.runtime[appID] = appTrafficLimitRuntime{Desired: limit, InSync: false, Diagnostic: err.Error(), CheckedAt: time.Now()}
		return err
	}
	instance := m.apps[appID]
	if instance == nil {
		slot, slotErr := m.allocateSlotLocked()
		if slotErr != nil {
			return slotErr
		}
		instance, err = m.newInstanceLocked(appID, slot, target, limit)
		if err == nil {
			m.apps[appID] = instance
		}
	} else {
		err = m.updateInstanceLocked(instance, target, limit)
	}
	status := appTrafficLimitRuntime{Desired: limit, Applied: instanceLimit(instance), InSync: err == nil, CheckedAt: time.Now()}
	if err != nil {
		status.Diagnostic = err.Error()
	}
	m.runtime[appID] = status
	return err
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func instanceLimit(instance *hostTrafficLimitInstance) AppTrafficLimit {
	if instance == nil {
		return AppTrafficLimit{}
	}
	return instance.limit
}

func hostLimitConfig(limit AppTrafficLimit, generation uint64, slot uint32) (hostlimitAppConfig, error) {
	if generation == 0 || slot == 0 || slot > hostTrafficLimitMaxSlot {
		return hostlimitAppConfig{}, errors.New("invalid Host/Mixed traffic limit generation or slot")
	}
	if limit.UploadKbps < 0 || limit.DownloadKbps < 0 || limit.UploadKbps > maxAppTrafficLimitKbps || limit.DownloadKbps > maxAppTrafficLimitKbps {
		return hostlimitAppConfig{}, fmt.Errorf("traffic limit must be between 0 and %d Kbit/s", maxAppTrafficLimitKbps)
	}
	config := hostlimitAppConfig{
		Generation:             generation,
		DownloadBytesPerSecond: uint64(limit.DownloadKbps * 1000 / 8),
		DownloadBurstBytes:     uint64(limitDirectionBurst(limit.DownloadKbps)),
		MarkMask:               hostTrafficLimitMarkMask,
	}
	if limit.UploadKbps > 0 {
		config.UploadMark = slot << 16
	}
	return config, nil
}

func limitDirectionBurst(kbps int64) int64 {
	if kbps <= 0 {
		return 0
	}
	burst := appTrafficBurstBytes(kbps)
	if burst < 128*1024 {
		burst = 128 * 1024
	}
	return burst
}

func (m *hostTrafficLimiter) newInstanceLocked(appID string, slot uint32, target hostTrafficLimitTarget, limit AppTrafficLimit) (_ *hostTrafficLimitInstance, err error) {
	_ = rlimit.RemoveMemlock()
	instance := &hostTrafficLimitInstance{appID: appID, slot: slot, device: target.device, cgroupLinks: make(map[string]ebpflink.Link)}
	if err := loadHostlimitObjects(&instance.objects, nil); err != nil {
		return nil, fmt.Errorf("load Host/Mixed TC eBPF: %w", err)
	}
	defer func() {
		if err != nil {
			_ = m.closeInstanceLocked(instance)
		}
	}()
	if err = m.prepareDeviceLocked(target.device); err != nil {
		return nil, err
	}
	if err = instance.reconcileCgroups(target.cgroupPaths); err != nil {
		return nil, err
	}
	instance.cgroupPaths = append([]string(nil), target.cgroupPaths...)
	if err = instance.replaceBridgeIfindexes(target.bridgeIfindexes); err != nil {
		return nil, err
	}
	instance.bridgeIfindexes = append([]uint32(nil), target.bridgeIfindexes...)
	if err = addHostTrafficBPFFilters(instance, target.device); err != nil {
		return nil, err
	}
	if err = replaceHostTrafficPolice(instance, target.device, limit.UploadKbps); err != nil {
		return nil, err
	}
	instance.generation = m.nextGenerationLocked()
	config, _ := hostLimitConfig(limit, instance.generation, instance.slot)
	if err = instance.objects.Config.Put(uint32(0), config); err != nil {
		return nil, fmt.Errorf("activate Host/Mixed traffic limit: %w", err)
	}
	instance.limit = limit
	return instance, nil
}

func (m *hostTrafficLimiter) updateInstanceLocked(instance *hostTrafficLimitInstance, target hostTrafficLimitTarget, limit AppTrafficLimit) error {
	if instance.device == target.device && slices.Equal(instance.cgroupPaths, target.cgroupPaths) &&
		slices.Equal(instance.bridgeIfindexes, target.bridgeIfindexes) && sameAppTrafficLimit(instance.limit, limit) {
		if inSync, err := hostTrafficFiltersInSync(instance); err == nil && inSync {
			return nil
		}
		if err := addHostTrafficBPFFilters(instance, instance.device); err != nil {
			return err
		}
		return replaceHostTrafficPolice(instance, instance.device, limit.UploadKbps)
	}
	if err := instance.reconcileCgroups(target.cgroupPaths); err != nil {
		return err
	}
	instance.cgroupPaths = append(instance.cgroupPaths[:0], target.cgroupPaths...)
	if err := instance.replaceBridgeIfindexes(target.bridgeIfindexes); err != nil {
		return err
	}
	instance.bridgeIfindexes = append(instance.bridgeIfindexes[:0], target.bridgeIfindexes...)
	if target.device != instance.device {
		if err := m.prepareDeviceLocked(target.device); err != nil {
			return err
		}
		if err := addHostTrafficBPFFilters(instance, target.device); err != nil {
			return err
		}
		if err := replaceHostTrafficPolice(instance, target.device, limit.UploadKbps); err != nil {
			_ = deleteHostTrafficBPFFilters(instance, target.device)
			return err
		}
		oldDevice := instance.device
		instance.device = target.device
		_ = deleteHostTrafficFilters(instance, oldDevice)
	} else if err := replaceHostTrafficPolice(instance, target.device, limit.UploadKbps); err != nil {
		return err
	}
	generation := m.nextGenerationLocked()
	config, _ := hostLimitConfig(limit, generation, instance.slot)
	if err := instance.objects.Config.Put(uint32(0), config); err != nil {
		_ = replaceHostTrafficPolice(instance, instance.device, instance.limit.UploadKbps)
		return fmt.Errorf("update Host/Mixed traffic limit: %w", err)
	}
	instance.generation = generation
	instance.limit = limit
	return nil
}

func (instance *hostTrafficLimitInstance) reconcileCgroups(paths []string) error {
	wanted := make(map[string]bool, len(paths))
	for _, path := range paths {
		wanted[path] = true
		if _, ok := instance.cgroupLinks[path]; ok {
			continue
		}
		attached, err := ebpflink.AttachCgroup(ebpflink.CgroupOptions{
			Path: path, Attach: ebpf.AttachCGroupInetSockCreate, Program: instance.objects.HostlimitTagSocket,
		})
		if err != nil {
			return fmt.Errorf("attach Host/Mixed socket tagger to %s: %w", path, err)
		}
		instance.cgroupLinks[path] = attached
	}
	for path, attached := range instance.cgroupLinks {
		if wanted[path] {
			continue
		}
		if err := attached.Close(); err != nil {
			return fmt.Errorf("detach stale Host/Mixed socket tagger from %s: %w", path, err)
		}
		delete(instance.cgroupLinks, path)
	}
	return nil
}

func (instance *hostTrafficLimitInstance) replaceBridgeIfindexes(ifindexes []uint32) error {
	iterator := instance.objects.BridgeIfindexes.Iterate()
	var key uint32
	var value uint8
	keys := make([]uint32, 0)
	for iterator.Next(&key, &value) {
		keys = append(keys, key)
	}
	if err := iterator.Err(); err != nil {
		return fmt.Errorf("read Host/Mixed bridge classifier map: %w", err)
	}
	for _, current := range keys {
		if err := instance.objects.BridgeIfindexes.Delete(current); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
			return fmt.Errorf("remove stale Host/Mixed bridge ifindex %d: %w", current, err)
		}
	}
	for _, ifindex := range ifindexes {
		if err := instance.objects.BridgeIfindexes.Put(ifindex, uint8(1)); err != nil {
			return fmt.Errorf("record Host/Mixed bridge ifindex %d: %w", ifindex, err)
		}
	}
	return nil
}

func (m *hostTrafficLimiter) prepareDeviceLocked(device string) error {
	if m.cleanedDevices[device] {
		return ensureHostTrafficClsact(device)
	}
	if err := ensureHostTrafficClsact(device); err != nil {
		return err
	}
	link, err := tcnetlink.LinkByName(device)
	if err != nil {
		return err
	}
	for _, parent := range []uint32{tcnetlink.HANDLE_MIN_INGRESS, tcnetlink.HANDLE_MIN_EGRESS} {
		filters, listErr := tcnetlink.FilterList(link, parent)
		if listErr != nil {
			return fmt.Errorf("list stale Host/Mixed filters on %s: %w", device, listErr)
		}
		for _, filter := range filters {
			priority := filter.Attrs().Priority
			if priority != hostTrafficLimitBPFPreference && priority != hostTrafficLimitPolicePreference {
				continue
			}
			if deleteErr := tcnetlink.FilterDel(filter); deleteErr != nil && !isNetlinkMissing(deleteErr) {
				return fmt.Errorf("delete stale Host/Mixed filter on %s: %w", device, deleteErr)
			}
		}
	}
	m.cleanedDevices[device] = true
	return nil
}

func ensureHostTrafficClsact(device string) error {
	link, err := tcnetlink.LinkByName(device)
	if err != nil {
		return fmt.Errorf("resolve Host/Mixed traffic device %s: %w", device, err)
	}
	qdiscs, err := tcnetlink.QdiscList(link)
	if err != nil {
		return fmt.Errorf("inspect Host/Mixed qdisc on %s: %w", device, err)
	}
	for _, qdisc := range qdiscs {
		if qdisc.Type() == "clsact" {
			return nil
		}
	}
	qdisc := &tcnetlink.Clsact{QdiscAttrs: tcnetlink.QdiscAttrs{
		LinkIndex: link.Attrs().Index, Handle: tcnetlink.MakeHandle(0xffff, 0), Parent: tcnetlink.HANDLE_CLSACT,
	}}
	if err := tcnetlink.QdiscAdd(qdisc); err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("create Host/Mixed clsact on %s: %w", device, err)
	}
	return nil
}

func hostTrafficBPFFilter(instance *hostTrafficLimitInstance, device string, ingress bool) (*tcnetlink.BpfFilter, error) {
	link, err := tcnetlink.LinkByName(device)
	if err != nil {
		return nil, err
	}
	parent := uint32(tcnetlink.HANDLE_MIN_EGRESS)
	program := instance.objects.HostlimitTcEgress
	if ingress {
		parent = tcnetlink.HANDLE_MIN_INGRESS
		program = instance.objects.HostlimitTcIngress
	}
	return &tcnetlink.BpfFilter{
		FilterAttrs: tcnetlink.FilterAttrs{
			LinkIndex: link.Attrs().Index, Parent: parent, Handle: instance.slot,
			Protocol: unix.ETH_P_ALL, Priority: hostTrafficLimitBPFPreference,
		},
		Fd: program.FD(), Name: fmt.Sprintf("netwatch_hostlimit_%d", instance.slot), DirectAction: true,
	}, nil
}

func addHostTrafficBPFFilters(instance *hostTrafficLimitInstance, device string) error {
	ingress, err := hostTrafficBPFFilter(instance, device, true)
	if err != nil {
		return err
	}
	if err := tcnetlink.FilterReplace(ingress); err != nil {
		return fmt.Errorf("attach Host/Mixed ingress classifier on %s: %w", device, err)
	}
	egress, err := hostTrafficBPFFilter(instance, device, false)
	if err != nil {
		_ = tcnetlink.FilterDel(ingress)
		return err
	}
	if err := tcnetlink.FilterReplace(egress); err != nil {
		_ = tcnetlink.FilterDel(ingress)
		return fmt.Errorf("attach Host/Mixed egress classifier on %s: %w", device, err)
	}
	return nil
}

func hostTrafficFiltersInSync(instance *hostTrafficLimitInstance) (bool, error) {
	link, err := tcnetlink.LinkByName(instance.device)
	if err != nil {
		return false, err
	}
	for _, parent := range []uint32{tcnetlink.HANDLE_MIN_INGRESS, tcnetlink.HANDLE_MIN_EGRESS} {
		filters, listErr := tcnetlink.FilterList(link, parent)
		if listErr != nil {
			return false, listErr
		}
		foundBPF := false
		foundPolice := instance.limit.UploadKbps == 0 || parent != tcnetlink.HANDLE_MIN_EGRESS
		for _, filter := range filters {
			attrs := filter.Attrs()
			if attrs.Handle == instance.slot && attrs.Priority == hostTrafficLimitBPFPreference && filter.Type() == "bpf" {
				foundBPF = true
			}
			fw, ok := filter.(*tcnetlink.FwFilter)
			if !ok || attrs.Handle != instance.slot<<16 || attrs.Priority != hostTrafficLimitPolicePreference {
				continue
			}
			for _, action := range fw.Actions {
				police, ok := action.(*tcnetlink.PoliceAction)
				if ok && police.Rate == uint32(instance.limit.UploadKbps*1000/8) &&
					police.ExceedAction == tcnetlink.TC_POLICE_SHOT {
					foundPolice = true
				}
			}
		}
		if !foundBPF || !foundPolice {
			return false, nil
		}
	}
	return true, nil
}

func hostTrafficPoliceFilter(instance *hostTrafficLimitInstance, device string, uploadKbps int64) (*tcnetlink.FwFilter, error) {
	link, err := tcnetlink.LinkByName(device)
	if err != nil {
		return nil, err
	}
	mark := instance.slot << 16
	police := tcnetlink.NewPoliceAction()
	police.Rate = uint32(uploadKbps * 1000 / 8)
	police.Burst = uint32(limitDirectionBurst(uploadKbps))
	police.Mtu = 65535
	police.ExceedAction = tcnetlink.TC_POLICE_SHOT
	police.NotExceedAction = tcnetlink.TC_POLICE_PIPE
	clearMark := tcnetlink.NewSkbEditAction()
	zero := uint32(0)
	mask := hostTrafficLimitMarkMask
	clearMark.Mark = &zero
	clearMark.Mask = &mask
	return &tcnetlink.FwFilter{
		FilterAttrs: tcnetlink.FilterAttrs{
			LinkIndex: link.Attrs().Index, Parent: tcnetlink.HANDLE_MIN_EGRESS,
			Handle: mark, Protocol: unix.ETH_P_ALL, Priority: hostTrafficLimitPolicePreference,
		},
		Mask: hostTrafficLimitMarkMask, Actions: []tcnetlink.Action{police, clearMark},
	}, nil
}

func replaceHostTrafficPolice(instance *hostTrafficLimitInstance, device string, uploadKbps int64) error {
	filter, err := hostTrafficPoliceFilter(instance, device, max(uploadKbps, 1))
	if err != nil {
		return err
	}
	if uploadKbps == 0 {
		if err := tcnetlink.FilterDel(filter); err != nil && !isNetlinkMissing(err) {
			return fmt.Errorf("remove Host/Mixed upload police on %s: %w", device, err)
		}
		return nil
	}
	if err := tcnetlink.FilterReplace(filter); err != nil {
		return fmt.Errorf("configure Host/Mixed upload police on %s: %w", device, err)
	}
	return nil
}

func deleteHostTrafficBPFFilters(instance *hostTrafficLimitInstance, device string) error {
	var cleanup []error
	for _, ingress := range []bool{true, false} {
		filter, err := hostTrafficBPFFilter(instance, device, ingress)
		if err != nil {
			cleanup = append(cleanup, err)
			continue
		}
		if err := tcnetlink.FilterDel(filter); err != nil && !isNetlinkMissing(err) {
			cleanup = append(cleanup, err)
		}
	}
	return errors.Join(cleanup...)
}

func deleteHostTrafficFilters(instance *hostTrafficLimitInstance, device string) error {
	var cleanup []error
	if err := replaceHostTrafficPolice(instance, device, 0); err != nil {
		cleanup = append(cleanup, err)
	}
	if err := deleteHostTrafficBPFFilters(instance, device); err != nil {
		cleanup = append(cleanup, err)
	}
	return errors.Join(cleanup...)
}

func isNetlinkMissing(err error) bool {
	return errors.Is(err, unix.ENOENT) || errors.Is(err, unix.EINVAL)
}

func (m *hostTrafficLimiter) closeAppLocked(appID string) error {
	instance := m.apps[appID]
	if instance == nil {
		return nil
	}
	delete(m.apps, appID)
	return m.closeInstanceLocked(instance)
}

func (m *hostTrafficLimiter) closeInstanceLocked(instance *hostTrafficLimitInstance) error {
	var cleanup []error
	if instance.objects.Config != nil {
		if err := instance.objects.Config.Put(uint32(0), hostlimitAppConfig{}); err != nil {
			cleanup = append(cleanup, fmt.Errorf("disable Host/Mixed classifier: %w", err))
		}
	}
	for path, attached := range instance.cgroupLinks {
		if err := attached.Close(); err != nil {
			cleanup = append(cleanup, fmt.Errorf("detach Host/Mixed socket tagger from %s: %w", path, err))
		}
		delete(instance.cgroupLinks, path)
	}
	if instance.device != "" {
		if err := deleteHostTrafficFilters(instance, instance.device); err != nil {
			cleanup = append(cleanup, err)
		}
	}
	if err := instance.objects.Close(); err != nil {
		cleanup = append(cleanup, fmt.Errorf("close Host/Mixed eBPF objects: %w", err))
	}
	return errors.Join(cleanup...)
}

func (m *hostTrafficLimiter) runtimeStatus(appID string, desired AppTrafficLimit) appTrafficLimitRuntime {
	m.mu.Lock()
	defer m.mu.Unlock()
	status, ok := m.runtime[appID]
	if !ok || !sameAppTrafficLimit(status.Desired, desired) {
		return appTrafficLimitRuntime{Desired: desired, InSync: desired.UploadKbps == 0 && desired.DownloadKbps == 0, Diagnostic: "等待核验 Host/Mixed TC 规则"}
	}
	return status
}

func (m *hostTrafficLimiter) reconcile(ctx context.Context, items []AppBridgeStats, limits map[string]AppTrafficLimit, enabled bool) map[string]bool {
	hostApps := make(map[string]bool)
	for _, item := range items {
		if item.AppID != "" && (item.NetworkMode == "host" || strings.HasPrefix(item.Bridge, hostAppTargetPrefix)) {
			hostApps[item.AppID] = true
		}
	}
	cleared := make(map[string]bool)
	for appID := range hostApps {
		limit := limits[appID]
		if !enabled || limit.UploadKbps == 0 && limit.DownloadKbps == 0 {
			m.mu.Lock()
			err := m.closeAppLocked(appID)
			m.mu.Unlock()
			if err != nil {
				logger.Warn("clear Host/Mixed traffic limit for %s: %v", appID, err)
			}
			if !enabled && (limit.UploadKbps != 0 || limit.DownloadKbps != 0) {
				cleared[appID] = err == nil
			}
			continue
		}
		targets := appNetworkTargetsForApp(items, appID)
		if err := m.apply(ctx, appID, targets, limit); err != nil {
			logger.Warn("restore Host/Mixed traffic limit for %s: %v", appID, err)
		}
	}
	m.mu.Lock()
	for appID := range m.apps {
		if !hostApps[appID] {
			if err := m.closeAppLocked(appID); err != nil {
				logger.Warn("remove stale Host/Mixed traffic limit for %s: %v", appID, err)
			}
		}
	}
	m.mu.Unlock()
	return cleared
}

func (m *hostTrafficLimiter) close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	appIDs := make([]string, 0, len(m.apps))
	for appID := range m.apps {
		appIDs = append(appIDs, appID)
	}
	sort.Strings(appIDs)
	var cleanup []error
	for _, appID := range appIDs {
		if err := m.closeAppLocked(appID); err != nil {
			cleanup = append(cleanup, fmt.Errorf("close Host/Mixed traffic limit for %s: %w", appID, err))
		}
	}
	return errors.Join(cleanup...)
}
