package probe

import (
	"bufio"
	"context"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"netwatch/internal/appmeta"
	"netwatch/internal/dockerlzc"
	"netwatch/internal/lzcsdk"
)

type procSocketRow struct {
	protocol  string
	ipVersion string
	address   string
	port      int
	state     string
	inode     string
}

const hostPortsCacheTTL = 3 * time.Second

var hostPortsCache = struct {
	sync.Mutex
	snapshot HostPortsSnapshot
	expires  time.Time
	ok       bool
}{}

func hostProcRoot() string {
	if firstReadableFile("/host/proc/net/tcp") != "" {
		return "/host/proc"
	}
	return "/proc"
}

func CollectHostPorts(ctx context.Context) HostPortsSnapshot {
	now := time.Now()
	hostPortsCache.Lock()
	if hostPortsCache.ok && now.Before(hostPortsCache.expires) {
		snap := cloneHostPortsSnapshot(hostPortsCache.snapshot)
		hostPortsCache.Unlock()
		return snap
	}
	hostPortsCache.Unlock()

	snap := collectHostPorts(ctx)

	hostPortsCache.Lock()
	hostPortsCache.snapshot = cloneHostPortsSnapshot(snap)
	hostPortsCache.expires = time.Now().Add(hostPortsCacheTTL)
	hostPortsCache.ok = true
	hostPortsCache.Unlock()

	return snap
}

func collectHostPorts(ctx context.Context) HostPortsSnapshot {
	snap := HostPortsSnapshot{GeneratedAt: localTimestamp()}
	rows := readProcSockets()
	if len(rows) == 0 {
		snap.Note = "未读取到监听端口。"
		return snap
	}

	inodes := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.inode != "" {
			inodes[row.inode] = struct{}{}
		}
	}
	procByInode := mapSocketInodesToProcesses(inodes)
	containers := loadPortContainerIndex(ctx)

	ports := make([]HostPortEntry, 0, len(rows))
	for _, row := range rows {
		proc := procByInode[row.inode]
		entry := HostPortEntry{
			Protocol:  row.protocol,
			IPVersion: row.ipVersion,
			Address:   row.address,
			Port:      row.port,
			State:     row.state,
			Inode:     row.inode,
			Process:   proc,
		}
		if c := containers.lookup(proc.PID); c != nil {
			entry.Container = c
		}
		ports = append(ports, entry)
	}

	sort.SliceStable(ports, func(i, j int) bool {
		if ports[i].Port != ports[j].Port {
			return ports[i].Port < ports[j].Port
		}
		if ports[i].Protocol != ports[j].Protocol {
			return ports[i].Protocol < ports[j].Protocol
		}
		return ports[i].Address < ports[j].Address
	})

	snap.Ports = ports
	if len(procByInode) < len(inodes) {
		snap.Note = "部分端口未能映射到进程，通常是权限不足或进程已退出。"
	}
	if !dockerlzc.Available() {
		if snap.Note != "" {
			snap.Note += " "
		}
		snap.Note += "未挂载 Docker socket，容器归属可能为空。"
	}
	return snap
}

func cloneHostPortsSnapshot(in HostPortsSnapshot) HostPortsSnapshot {
	out := in
	if in.Ports != nil {
		out.Ports = append([]HostPortEntry(nil), in.Ports...)
	}
	return out
}

func readProcSockets() []procSocketRow {
	var out []procSocketRow
	for _, item := range []struct {
		path      string
		protocol  string
		ipVersion string
	}{
		{filepath.Join(hostProcRoot(), "net/tcp"), "tcp", "ipv4"},
		{filepath.Join(hostProcRoot(), "net/tcp6"), "tcp", "ipv6"},
		{filepath.Join(hostProcRoot(), "net/udp"), "udp", "ipv4"},
		{filepath.Join(hostProcRoot(), "net/udp6"), "udp", "ipv6"},
	} {
		out = append(out, readProcSocketFile(item.path, item.protocol, item.ipVersion)...)
	}
	return out
}

func readProcSocketFile(path, protocol, ipVersion string) []procSocketRow {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var rows []procSocketRow
	scanner := bufio.NewScanner(f)
	first := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if first {
			first = false
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		state := tcpState(fields[3])
		if protocol == "tcp" && state != "LISTEN" {
			continue
		}
		if protocol == "udp" {
			state = "BOUND"
		}
		addr, port, ok := parseProcAddress(fields[1], ipVersion)
		if !ok || port <= 0 {
			continue
		}
		rows = append(rows, procSocketRow{
			protocol:  protocol,
			ipVersion: ipVersion,
			address:   addr,
			port:      port,
			state:     state,
			inode:     fields[9],
		})
	}
	return rows
}

func parseProcAddress(value, ipVersion string) (string, int, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return "", 0, false
	}
	port64, err := strconv.ParseInt(parts[1], 16, 32)
	if err != nil {
		return "", 0, false
	}
	ipHex := parts[0]
	if ipVersion == "ipv4" {
		if len(ipHex) != 8 {
			return "", 0, false
		}
		b, err := hex.DecodeString(ipHex)
		if err != nil || len(b) != 4 {
			return "", 0, false
		}
		return net.IPv4(b[3], b[2], b[1], b[0]).String(), int(port64), true
	}
	if len(ipHex) != 32 {
		return "", 0, false
	}
	b, err := hex.DecodeString(ipHex)
	if err != nil || len(b) != 16 {
		return "", 0, false
	}
	for i := 0; i < 16; i += 4 {
		b[i], b[i+3] = b[i+3], b[i]
		b[i+1], b[i+2] = b[i+2], b[i+1]
	}
	return net.IP(b).String(), int(port64), true
}

func tcpState(hexState string) string {
	switch strings.ToUpper(hexState) {
	case "01":
		return "ESTABLISHED"
	case "02":
		return "SYN_SENT"
	case "03":
		return "SYN_RECV"
	case "04":
		return "FIN_WAIT1"
	case "05":
		return "FIN_WAIT2"
	case "06":
		return "TIME_WAIT"
	case "07":
		return "CLOSE"
	case "08":
		return "CLOSE_WAIT"
	case "09":
		return "LAST_ACK"
	case "0A":
		return "LISTEN"
	case "0B":
		return "CLOSING"
	default:
		return strings.ToUpper(hexState)
	}
}

func mapSocketInodesToProcesses(inodes map[string]struct{}) map[string]HostPortProcess {
	if len(inodes) == 0 {
		return nil
	}
	out := map[string]HostPortProcess{}
	entries, err := os.ReadDir(hostProcRoot())
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		fdDir := filepath.Join(hostProcRoot(), entry.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		var matched []string
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			inode, ok := socketInode(target)
			if !ok {
				continue
			}
			if _, wanted := inodes[inode]; wanted {
				matched = append(matched, inode)
			}
		}
		if len(matched) == 0 {
			continue
		}
		proc := readProcessInfo(pid)
		for _, inode := range matched {
			if _, exists := out[inode]; !exists {
				out[inode] = proc
			}
		}
	}
	return out
}

func socketInode(target string) (string, bool) {
	if !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
		return "", false
	}
	inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
	return inode, inode != ""
}

func readProcessInfo(pid int) HostPortProcess {
	proc := HostPortProcess{PID: pid}
	statusPath := filepath.Join(hostProcRoot(), strconv.Itoa(pid), "status")
	if body, err := os.ReadFile(statusPath); err == nil {
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(line, "Name:") {
				proc.Name = strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
			} else if strings.HasPrefix(line, "PPid:") {
				proc.PPID, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PPid:")))
			} else if strings.HasPrefix(line, "Uid:") {
				fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "Uid:")))
				if len(fields) > 0 {
					proc.User = uidToName(fields[0])
				}
			}
		}
	}
	if body, err := os.ReadFile(filepath.Join(hostProcRoot(), strconv.Itoa(pid), "cmdline")); err == nil {
		proc.Cmdline = strings.TrimSpace(strings.ReplaceAll(string(body), "\x00", " "))
	}
	if proc.Cmdline == "" && proc.Name != "" {
		proc.Cmdline = proc.Name
	}
	return proc
}

func uidToName(uid string) string {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return uid
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ":")
		if len(fields) >= 3 && fields[2] == uid {
			return fields[0]
		}
	}
	return uid
}

type portContainerIndex struct {
	byPID      map[int]HostPortContainer
	ancestorOf map[int]int
}

func loadPortContainerIndex(ctx context.Context) portContainerIndex {
	idx := portContainerIndex{byPID: map[int]HostPortContainer{}, ancestorOf: map[int]int{}}
	containers, err := dockerlzc.ListContainerRuntime(ctx)
	if err != nil {
		return idx
	}
	localTitles := map[string]string{}
	localAppIDs := []string{}
	if appmeta.Available() {
		if titles, err := appmeta.LoadTitles(); err == nil {
			localTitles = titles
		}
		if ids, err := appmeta.LoadAppIDs(); err == nil {
			localAppIDs = ids
		}
	}
	appMap := map[string]lzcsdk.AppInfo{}
	boxDomain := ""
	if lzcsdk.Available() {
		appCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		if apps, err := lzcsdk.ListApps(appCtx); err == nil {
			appMap = apps
		}
		boxDomain = lzcsdk.BoxDomain(appCtx)
		cancel()
	} else {
	}
	projectAppIDs := map[string]string{}
	for appID := range appMap {
		projectAppIDs[normalizeAppProject(appID)] = appID
	}
	for appID := range localTitles {
		key := normalizeAppProject(appID)
		if _, exists := projectAppIDs[key]; !exists {
			projectAppIDs[key] = appID
		}
	}
	for _, appID := range localAppIDs {
		key := normalizeAppProject(appID)
		if _, exists := projectAppIDs[key]; !exists {
			projectAppIDs[key] = appID
		}
	}
	titleByProject := map[string]string{}
	if bridgeMap, err := dockerlzc.BuildBridgeMap(ctx); err == nil {
		for _, info := range bridgeMap {
			if info.Project != "" && info.Title != "" {
				titleByProject[info.Project] = resolveAppTitle(info.AppID, info.Title, localTitles, appMap)
			}
		}
	}
	for _, c := range containers {
		if c.PID <= 0 || !c.Running {
			continue
		}
		info := HostPortContainer{
			ID:          shortContainerID(c.ID),
			Name:        c.Name,
			Image:       c.Image,
			AppID:       firstPortValue(c.AppID, projectAppIDs[normalizeAppProject(c.Project)]),
			AppTitle:    resolveAppTitle(firstPortValue(c.AppID, projectAppIDs[normalizeAppProject(c.Project)]), titleByProject[c.Project], localTitles, appMap),
			Icon:        appIcon(firstPortValue(c.AppID, projectAppIDs[normalizeAppProject(c.Project)]), appMap, boxDomain),
			Project:     c.Project,
			NetworkMode: c.NetworkMode,
			PID:         c.PID,
		}
		idx.byPID[c.PID] = info
	}
	idx.buildAncestors()
	return idx
}

func normalizeAppProject(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, ".", "")
	value = strings.ReplaceAll(value, "-", "")
	value = strings.ReplaceAll(value, "_", "")
	return value
}

func firstPortValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func appIcon(appID string, appMap map[string]lzcsdk.AppInfo, boxDomain string) string {
	if app, ok := appMap[appID]; ok {
		if icon := sanitizeIconURL(app.Icon, boxDomain); icon != "" {
			return icon
		}
	}
	if appID != "" && boxDomain != "" {
		return "https://" + strings.TrimSuffix(boxDomain, "/") + "/sys/icons/" + appID + ".png"
	}
	return ""
}

func resolveAppTitle(appID, fallback string, localTitles map[string]string, appMap map[string]lzcsdk.AppInfo) string {
	if appID != "" {
		if title := strings.TrimSpace(localTitles[appID]); title != "" {
			return title
		}
		if app, ok := appMap[appID]; ok {
			if title := strings.TrimSpace(app.Title); title != "" && title != appID {
				return title
			}
		}
	}
	fallback = strings.TrimSpace(fallback)
	if fallback != "" && fallback != appID {
		return fallback
	}
	return shortAppID(appID)
}

func shortAppID(appID string) string {
	parts := strings.Split(strings.TrimSpace(appID), ".")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return ""
}

func (idx portContainerIndex) lookup(pid int) *HostPortContainer {
	if pid <= 0 {
		return nil
	}
	if c, ok := idx.byPID[pid]; ok {
		return &c
	}
	if rootPID, ok := idx.ancestorOf[pid]; ok {
		if c, exists := idx.byPID[rootPID]; exists {
			return &c
		}
	}
	return nil
}

func (idx portContainerIndex) buildAncestors() {
	if len(idx.byPID) == 0 {
		return
	}
	rootSet := map[int]struct{}{}
	for pid := range idx.byPID {
		rootSet[pid] = struct{}{}
	}
	entries, err := os.ReadDir(hostProcRoot())
	if err != nil {
		return
	}
	ppid := map[int]int{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		ppid[pid] = readPPID(pid)
	}
	for pid := range ppid {
		seen := map[int]struct{}{}
		for cur := pid; cur > 0; cur = ppid[cur] {
			if _, ok := seen[cur]; ok {
				break
			}
			seen[cur] = struct{}{}
			if _, ok := rootSet[cur]; ok {
				idx.ancestorOf[pid] = cur
				break
			}
		}
	}
}

func readPPID(pid int) int {
	body, err := os.ReadFile(filepath.Join(hostProcRoot(), strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	text := string(body)
	end := strings.LastIndex(text, ")")
	if end < 0 || end+2 >= len(text) {
		return 0
	}
	fields := strings.Fields(text[end+2:])
	if len(fields) < 2 {
		return 0
	}
	ppid, _ := strconv.Atoi(fields[1])
	return ppid
}

func shortContainerID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
