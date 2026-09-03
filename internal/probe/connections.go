package probe

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"netwatch/internal/dockerlzc"
)

const maxAppConnections = 300

const (
	appConnectionRuntimeCacheTTL       = 15 * time.Second
	appConnectionBridgeOwnerCacheTTL   = 9 * time.Second
	maxAppConnectionBridgeOwnerEntries = 32
)

var appConnectionRuntimeCache = struct {
	sync.Mutex
	containers []dockerlzc.ContainerRuntimeInfo
	expiresAt  time.Time
	loading    chan struct{}
	loadErr    error
}{}

type appConnectionBridgeOwnerCacheEntry struct {
	owners    map[string]appConnectionSocketOwner
	expiresAt time.Time
}

var appConnectionBridgeOwnerCache = struct {
	sync.Mutex
	entries map[string]appConnectionBridgeOwnerCacheEntry
}{entries: make(map[string]appConnectionBridgeOwnerCacheEntry)}

type appConnectionSocketRow struct {
	entry AppConnectionEntry
	inode string
}

type appConnectionSocketOwner struct {
	container dockerlzc.ContainerRuntimeInfo
	pid       int
	process   string
}

type appConnectionNetNSGroup struct {
	key        string
	containers []dockerlzc.ContainerRuntimeInfo
	host       bool
	shared     bool
}

// AppInstanceConnections returns an on-demand snapshot. Connections are never
// persisted and no background conntrack scan is started when the dialog is
// closed. Bridge containers are isolated by network namespace; Host containers
// are attributed through the socket inode owned by their process tree.
func (s *Service) AppInstanceConnections(ctx context.Context, appID, instanceID string, limit int) (AppConnectionSnapshot, error) {
	appID = strings.TrimSpace(appID)
	instanceID = strings.TrimSpace(instanceID)
	if appID == "" {
		return AppConnectionSnapshot{}, fmt.Errorf("application id is required")
	}
	if limit <= 0 || limit > maxAppConnections {
		limit = maxAppConnections
	}
	containers, err := appConnectionRuntimeContainers(ctx, false)
	if err != nil {
		return AppConnectionSnapshot{
			GeneratedAt: localTimestamp(), AppID: appID, InstanceID: instanceID,
			Limit: limit, Connections: []AppConnectionEntry{},
			Note: "无法读取容器运行时信息，连接明细不可用",
		}, nil
	}
	policyID, err := resolveAppConnectionPolicyID(containers, appID, instanceID)
	if err != nil && appConnectionRuntimeRefreshMayHelp(containers, appID, instanceID) {
		if refreshed, refreshErr := appConnectionRuntimeContainers(ctx, true); refreshErr == nil {
			containers = refreshed
			policyID, err = resolveAppConnectionPolicyID(containers, appID, instanceID)
		}
	}
	if err != nil {
		return AppConnectionSnapshot{}, err
	}
	targets := filterAppConnectionContainers(containers, appID, policyID)
	if appConnectionTargetsStale(targets) {
		if refreshed, refreshErr := appConnectionRuntimeContainers(ctx, true); refreshErr == nil {
			containers = refreshed
			if refreshedPolicyID, resolveErr := resolveAppConnectionPolicyID(containers, appID, instanceID); resolveErr == nil {
				policyID = refreshedPolicyID
				targets = filterAppConnectionContainers(containers, appID, policyID)
			}
		}
	}
	hostnames := appConnectionRuntimeHostnames(containers)
	for address, hostname := range appConnectionLANHostnames(s.GetLANDevices()) {
		// LAN discovery is authoritative for local devices and should win if a
		// host happens to share an address with stale container metadata.
		hostnames[address] = hostname
	}
	return collectAppConnectionSnapshot(ctx, targets, appID, policyID, limit, hostnames), nil
}

func appConnectionRuntimeContainers(ctx context.Context, force bool) ([]dockerlzc.ContainerRuntimeInfo, error) {
	now := time.Now()
	appConnectionRuntimeCache.Lock()
	if !force && now.Before(appConnectionRuntimeCache.expiresAt) && appConnectionRuntimeCache.containers != nil {
		containers := append([]dockerlzc.ContainerRuntimeInfo(nil), appConnectionRuntimeCache.containers...)
		appConnectionRuntimeCache.Unlock()
		return containers, nil
	}
	if loading := appConnectionRuntimeCache.loading; loading != nil {
		appConnectionRuntimeCache.Unlock()
		select {
		case <-loading:
			appConnectionRuntimeCache.Lock()
			containers := append([]dockerlzc.ContainerRuntimeInfo(nil), appConnectionRuntimeCache.containers...)
			err := appConnectionRuntimeCache.loadErr
			appConnectionRuntimeCache.Unlock()
			if err != nil {
				return nil, err
			}
			return containers, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	loading := make(chan struct{})
	appConnectionRuntimeCache.loading = loading
	appConnectionRuntimeCache.Unlock()

	containers, err := dockerlzc.ListContainerRuntime(ctx)
	appConnectionRuntimeCache.Lock()
	if err == nil {
		appConnectionRuntimeCache.containers = append([]dockerlzc.ContainerRuntimeInfo(nil), containers...)
		appConnectionRuntimeCache.expiresAt = time.Now().Add(appConnectionRuntimeCacheTTL)
	}
	appConnectionRuntimeCache.loadErr = err
	appConnectionRuntimeCache.loading = nil
	close(loading)
	appConnectionRuntimeCache.Unlock()
	if err != nil {
		return nil, err
	}
	return containers, nil
}

func appConnectionRuntimeRefreshMayHelp(containers []dockerlzc.ContainerRuntimeInfo, appID, requestedInstanceID string) bool {
	foundApp := false
	for _, container := range containers {
		if !container.Running || strings.TrimSpace(container.AppID) != appID {
			continue
		}
		foundApp = true
		if requestedInstanceID != "" && appConnectionPolicyID(container) == requestedInstanceID {
			return false
		}
	}
	return !foundApp || requestedInstanceID != ""
}

func appConnectionTargetsStale(containers []dockerlzc.ContainerRuntimeInfo) bool {
	if len(containers) == 0 {
		return true
	}
	procRoot := hostProcRoot()
	for _, container := range containers {
		if _, err := os.Stat(filepath.Join(procRoot, strconv.Itoa(container.PID))); err != nil {
			return true
		}
	}
	return false
}

func appConnectionPolicyID(container dockerlzc.ContainerRuntimeInfo) string {
	if instanceID := strings.TrimSpace(container.InstanceID); instanceID != "" {
		return instanceID
	}
	return strings.TrimSpace(container.AppID)
}

func resolveAppConnectionPolicyID(containers []dockerlzc.ContainerRuntimeInfo, appID, requestedInstanceID string) (string, error) {
	instances := make(map[string]bool)
	for _, container := range containers {
		if !container.Running || strings.TrimSpace(container.AppID) != appID {
			continue
		}
		if policyID := appConnectionPolicyID(container); policyID != "" {
			instances[policyID] = true
		}
	}
	if requestedInstanceID != "" {
		if instances[requestedInstanceID] {
			return requestedInstanceID, nil
		}
		return "", fmt.Errorf("application instance %s was not found", requestedInstanceID)
	}
	if len(instances) == 0 {
		return "", fmt.Errorf("application %s has no running container", appID)
	}
	if len(instances) > 1 {
		return "", fmt.Errorf("应用 %s 当前有多个实例，请指定 instance_id", appID)
	}
	for policyID := range instances {
		return policyID, nil
	}
	return "", fmt.Errorf("application %s has no running container", appID)
}

func filterAppConnectionContainers(containers []dockerlzc.ContainerRuntimeInfo, appID, policyID string) []dockerlzc.ContainerRuntimeInfo {
	out := make([]dockerlzc.ContainerRuntimeInfo, 0)
	for _, container := range containers {
		if !container.Running || container.PID <= 0 || strings.TrimSpace(container.AppID) != appID || appConnectionPolicyID(container) != policyID {
			continue
		}
		out = append(out, container)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NetworkMode != out[j].NetworkMode {
			return out[i].NetworkMode < out[j].NetworkMode
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func groupAppConnectionNetworkNamespaces(procRoot string, containers []dockerlzc.ContainerRuntimeInfo) []appConnectionNetNSGroup {
	groups := make([]appConnectionNetNSGroup, 0, len(containers))
	indexes := make(map[string]int, len(containers))
	for _, container := range containers {
		key := ""
		if container.PID > 0 {
			key, _ = os.Readlink(filepath.Join(procRoot, strconv.Itoa(container.PID), "ns", "net"))
		}
		if key == "" {
			if container.NetworkMode == "host" {
				key = "host"
			} else {
				key = "container:" + container.ID + ":" + strconv.Itoa(container.PID)
			}
		}
		index, ok := indexes[key]
		if !ok {
			index = len(groups)
			indexes[key] = index
			groups = append(groups, appConnectionNetNSGroup{key: key})
		}
		group := &groups[index]
		group.containers = append(group.containers, container)
		group.host = group.host || container.NetworkMode == "host"
		group.shared = group.shared || strings.HasPrefix(container.NetworkMode, "container:")
	}
	for index := range groups {
		if len(groups[index].containers) > 1 {
			groups[index].shared = true
		}
	}
	return groups
}

func collectAppConnectionSnapshot(ctx context.Context, containers []dockerlzc.ContainerRuntimeInfo, appID, policyID string, limit int, lanHostnames map[string]string) AppConnectionSnapshot {
	snapshot := AppConnectionSnapshot{
		GeneratedAt: localTimestamp(), AppID: appID, InstanceID: policyID,
		Limit: limit, Connections: []AppConnectionEntry{},
	}
	if len(containers) == 0 {
		snapshot.Note = "未找到正在运行的应用容器"
		return snapshot
	}

	procRoot := hostProcRoot()
	groups := groupAppConnectionNetworkNamespaces(procRoot, containers)
	seen := make(map[string]bool)
	readable := false
	hasHost := false
	hostOwnershipReadable := false

	appendEntry := func(row appConnectionSocketRow, container dockerlzc.ContainerRuntimeInfo, owner appConnectionSocketOwner, reliable bool) {
		entry := row.entry
		entry.ContainerID = shortContainerID(container.ID)
		entry.ContainerName = strings.TrimPrefix(container.Name, "/")
		entry.AppID = appID
		entry.InstanceID = policyID
		entry.Project = container.Project
		entry.NetworkMode = "bridge"
		if container.NetworkMode == "host" {
			entry.NetworkMode = "host"
		} else if strings.HasPrefix(container.NetworkMode, "container:") {
			entry.NetworkMode = "shared"
		}
		entry.AttributionReliable = reliable
		if owner.pid > 0 {
			entry.ProcessPID = owner.pid
			entry.ProcessName = owner.process
		}
		key := strings.Join([]string{
			entry.ContainerID, entry.Protocol, entry.LocalAddress, strconv.Itoa(entry.LocalPort),
			entry.RemoteAddress, strconv.Itoa(entry.RemotePort), entry.State,
		}, "|")
		if seen[key] {
			return
		}
		seen[key] = true
		snapshot.Connections = append(snapshot.Connections, entry)
	}

	// Each network namespace is read once. Isolated Bridge namespaces already
	// provide reliable container attribution, so their expensive process/FD map
	// is cached briefly and is used only for the optional process label. Host and
	// shared namespaces still rebuild ownership on every snapshot because socket
	// ownership is required to filter unrelated processes safely.
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			break
		}
		if len(group.containers) == 0 {
			continue
		}
		container := group.containers[0]
		if group.host {
			hasHost = true
		}

		var owners map[string]appConnectionSocketOwner
		var filterOwners map[string]appConnectionSocketOwner
		if group.host || group.shared {
			var ownershipReadable bool
			owners, ownershipReadable = mapAppConnectionSocketOwnersAt(procRoot, group.containers)
			filterOwners = owners
			if group.host && ownershipReadable {
				hostOwnershipReadable = true
			}
		} else {
			owners = cachedAppConnectionBridgeSocketOwners(procRoot, container)
		}

		netRoot := filepath.Join(procRoot, strconv.Itoa(container.PID), "net")
		if group.host {
			netRoot = filepath.Join(procRoot, "net")
		}
		for _, spec := range appConnectionSocketSpecs() {
			path := filepath.Join(netRoot, spec.file)
			rows, ok := readAppConnectionSocketFileForOwners(path, spec.protocol, spec.version, filterOwners)
			readable = readable || ok
			for _, row := range rows {
				owner, owned := owners[row.inode]
				if group.host || group.shared {
					if !owned {
						continue
					}
					appendEntry(row, owner.container, owner, true)
					continue
				}
				// Cached ownership in an isolated namespace is optional metadata.
				// Never let a stale/reused inode override the namespace's container.
				if !owned || owner.container.ID != container.ID {
					owner = appConnectionSocketOwner{}
				}
				appendEntry(row, container, owner, true)
			}
		}
	}

	snapshot.Supported = readable
	if !readable {
		snapshot.Note = "宿主进程网络表不可读，连接明细不可用"
		return snapshot
	}
	if hasHost && !hostOwnershipReadable {
		snapshot.Note = "Host 容器的套接字所有者不可读，Host 连接无法安全归属"
	}

	sort.SliceStable(snapshot.Connections, func(i, j int) bool {
		a, b := snapshot.Connections[i], snapshot.Connections[j]
		if connectionStateRank(a.State) != connectionStateRank(b.State) {
			return connectionStateRank(a.State) < connectionStateRank(b.State)
		}
		if a.ContainerName != b.ContainerName {
			return a.ContainerName < b.ContainerName
		}
		if a.RemoteAddress != b.RemoteAddress {
			return a.RemoteAddress < b.RemoteAddress
		}
		return a.RemotePort < b.RemotePort
	})
	if len(snapshot.Connections) > limit {
		snapshot.Connections = snapshot.Connections[:limit]
		snapshot.Truncated = true
	}
	populateAppConnectionRemoteHosts(snapshot.Connections, lanHostnames)
	return snapshot
}

func populateAppConnectionRemoteHosts(connections []AppConnectionEntry, hostnames map[string]string) {
	for index := range connections {
		connections[index].RemoteHost = cachedAppConnectionRemoteHost(connections[index].RemoteAddress, hostnames)
	}
}

func appConnectionSocketSpecs() []struct {
	file, protocol, version string
} {
	return []struct {
		file, protocol, version string
	}{
		{"tcp", "tcp", "ipv4"}, {"tcp6", "tcp", "ipv6"},
		{"udp", "udp", "ipv4"}, {"udp6", "udp", "ipv6"},
	}
}

func readAppConnectionSocketFile(path, protocol, version string) ([]appConnectionSocketRow, bool) {
	return readAppConnectionSocketFileForOwners(path, protocol, version, nil)
}

func readAppConnectionSocketFileForOwners(path, protocol, version string, allowedOwners map[string]appConnectionSocketOwner) ([]appConnectionSocketRow, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()

	type rawRow struct {
		localIP, remoteIP     netip.Addr
		localPort, remotePort int
		state, inode          string
	}
	raw := make([]rawRow, 0)
	listeners := make(map[int]bool)
	scanner := bufio.NewScanner(file)
	first := true
	for scanner.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}
		if allowedOwners != nil {
			if _, owned := allowedOwners[fields[9]]; !owned {
				continue
			}
		}
		localIP, localPort, localOK := parseAppConnectionAddress(fields[1], version)
		remoteIP, remotePort, remoteOK := parseAppConnectionAddress(fields[2], version)
		if !localOK || !remoteOK {
			continue
		}
		state := tcpState(fields[3])
		if protocol == "tcp" && state == "LISTEN" {
			listeners[localPort] = true
			continue
		}
		if remotePort == 0 || remoteIP.IsUnspecified() {
			continue
		}
		if protocol == "udp" {
			state = "ACTIVE"
		}
		raw = append(raw, rawRow{
			localIP: localIP, localPort: localPort, remoteIP: remoteIP, remotePort: remotePort,
			state: state, inode: fields[9],
		})
	}

	out := make([]appConnectionSocketRow, 0, len(raw))
	for _, row := range raw {
		direction := "unknown"
		if protocol == "tcp" {
			direction = "outbound"
			if listeners[row.localPort] {
				direction = "inbound"
			}
		}
		out = append(out, appConnectionSocketRow{
			entry: AppConnectionEntry{
				Protocol: protocol, IPVersion: version,
				LocalAddress: row.localIP.String(), LocalPort: row.localPort,
				RemoteAddress: row.remoteIP.String(), RemotePort: row.remotePort,
				State: row.state, Direction: direction,
			},
			inode: row.inode,
		})
	}
	return out, true
}

func parseAppConnectionAddress(value, version string) (netip.Addr, int, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return netip.Addr{}, 0, false
	}
	port, err := strconv.ParseUint(parts[1], 16, 16)
	if err != nil {
		return netip.Addr{}, 0, false
	}
	raw, err := hex.DecodeString(parts[0])
	if err != nil {
		return netip.Addr{}, 0, false
	}
	if version == "ipv4" && len(raw) == 4 {
		return netip.AddrFrom4([4]byte{raw[3], raw[2], raw[1], raw[0]}), int(port), true
	}
	if version == "ipv6" && len(raw) == 16 {
		for index := 0; index < 16; index += 4 {
			raw[index], raw[index+3] = raw[index+3], raw[index]
			raw[index+1], raw[index+2] = raw[index+2], raw[index+1]
		}
		var address [16]byte
		copy(address[:], raw)
		return netip.AddrFrom16(address), int(port), true
	}
	return netip.Addr{}, 0, false
}

func mapAppConnectionSocketOwners(containers []dockerlzc.ContainerRuntimeInfo) (map[string]appConnectionSocketOwner, bool) {
	return mapAppConnectionSocketOwnersAt(hostProcRoot(), containers)
}

func mapAppConnectionSocketOwnersAt(procRoot string, containers []dockerlzc.ContainerRuntimeInfo) (map[string]appConnectionSocketOwner, bool) {
	owners := make(map[string]appConnectionSocketOwner)
	readable := false
	for _, container := range containers {
		if container.PID <= 0 {
			continue
		}
		for _, pid := range appConnectionProcessTreeAt(procRoot, container.PID) {
			fds, err := os.ReadDir(filepath.Join(procRoot, strconv.Itoa(pid), "fd"))
			if err != nil {
				continue
			}
			readable = true
			process := ""
			for _, fd := range fds {
				target, err := os.Readlink(filepath.Join(procRoot, strconv.Itoa(pid), "fd", fd.Name()))
				if err != nil {
					continue
				}
				inode, ok := socketInode(target)
				if !ok {
					continue
				}
				if _, exists := owners[inode]; exists {
					continue
				}
				if process == "" {
					process = appConnectionProcessNameAt(procRoot, pid)
				}
				owners[inode] = appConnectionSocketOwner{
					container: container, pid: pid, process: process,
				}
			}
		}
	}
	return owners, readable
}

func cachedAppConnectionBridgeSocketOwners(procRoot string, container dockerlzc.ContainerRuntimeInfo) map[string]appConnectionSocketOwner {
	key := container.ID + ":" + strconv.Itoa(container.PID)
	now := time.Now()
	appConnectionBridgeOwnerCache.Lock()
	if entry, ok := appConnectionBridgeOwnerCache.entries[key]; ok && now.Before(entry.expiresAt) {
		appConnectionBridgeOwnerCache.Unlock()
		return entry.owners
	}
	appConnectionBridgeOwnerCache.Unlock()

	owners, _ := mapAppConnectionSocketOwnersAt(procRoot, []dockerlzc.ContainerRuntimeInfo{container})
	appConnectionBridgeOwnerCache.Lock()
	for cacheKey, entry := range appConnectionBridgeOwnerCache.entries {
		if !now.Before(entry.expiresAt) {
			delete(appConnectionBridgeOwnerCache.entries, cacheKey)
		}
	}
	for len(appConnectionBridgeOwnerCache.entries) >= maxAppConnectionBridgeOwnerEntries {
		for cacheKey := range appConnectionBridgeOwnerCache.entries {
			delete(appConnectionBridgeOwnerCache.entries, cacheKey)
			break
		}
	}
	appConnectionBridgeOwnerCache.entries[key] = appConnectionBridgeOwnerCacheEntry{
		owners: owners, expiresAt: time.Now().Add(appConnectionBridgeOwnerCacheTTL),
	}
	appConnectionBridgeOwnerCache.Unlock()
	return owners
}

// appConnectionProcessTree walks only the selected container's descendants.
// Reading every host PID would make an open dialog unnecessarily expensive on
// boxes with many applications. Children are collected across all threads so
// processes forked by a non-main thread are not missed.
func appConnectionProcessTree(rootPID int) []int {
	return appConnectionProcessTreeAt(hostProcRoot(), rootPID)
}

func appConnectionProcessTreeAt(procRoot string, rootPID int) []int {
	if rootPID <= 0 {
		return nil
	}
	seen := map[int]bool{rootPID: true}
	queue := []int{rootPID}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		tasks, err := os.ReadDir(filepath.Join(procRoot, strconv.Itoa(pid), "task"))
		if err != nil {
			continue
		}
		for _, task := range tasks {
			if _, err := strconv.Atoi(task.Name()); err != nil {
				continue
			}
			body, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "task", task.Name(), "children"))
			if err != nil {
				continue
			}
			for _, field := range strings.Fields(string(body)) {
				child, err := strconv.Atoi(field)
				if err != nil || child <= 0 || seen[child] {
					continue
				}
				seen[child] = true
				queue = append(queue, child)
			}
		}
	}
	out := make([]int, 0, len(seen))
	for pid := range seen {
		out = append(out, pid)
	}
	sort.Ints(out)
	return out
}

func appConnectionProcessName(pid int) string {
	return appConnectionProcessNameAt(hostProcRoot(), pid)
}

func appConnectionProcessNameAt(procRoot string, pid int) string {
	body, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

func connectionStateRank(state string) int {
	switch strings.ToUpper(state) {
	case "ESTABLISHED", "ACTIVE":
		return 0
	case "SYN_SENT", "SYN_RECV":
		return 1
	case "CLOSE_WAIT":
		return 2
	case "FIN_WAIT1", "FIN_WAIT2", "LAST_ACK", "CLOSING":
		return 3
	case "TIME_WAIT":
		return 4
	default:
		return 5
	}
}

// cachedAppConnectionRemoteHost only reads names already learned from Docker
// runtime metadata or LAN discovery. Opening the connection dialog must never
// start its own DNS/PTR lookup loop.
func cachedAppConnectionRemoteHost(address string, hostnames map[string]string) string {
	parsed, err := netip.ParseAddr(strings.TrimSpace(address))
	if err != nil || !isInternalAppConnectionAddress(parsed) {
		return ""
	}
	return strings.TrimSpace(hostnames[parsed.Unmap().String()])
}

func appConnectionRuntimeHostnames(containers []dockerlzc.ContainerRuntimeInfo) map[string]string {
	hostnames := make(map[string]string)
	ambiguous := make(map[string]bool)
	for _, container := range containers {
		if !container.Running {
			continue
		}
		for _, endpoint := range container.NetworkEndpoints {
			hostname := preferredAppConnectionContainerHostname(container, endpoint)
			if hostname == "" {
				continue
			}
			for _, raw := range []string{endpoint.IPv4, endpoint.IPv6} {
				address, err := netip.ParseAddr(strings.TrimSpace(raw))
				if err != nil || !isInternalAppConnectionAddress(address) {
					continue
				}
				key := address.Unmap().String()
				if ambiguous[key] {
					continue
				}
				if existing, exists := hostnames[key]; exists && existing != hostname {
					delete(hostnames, key)
					ambiguous[key] = true
				} else if !exists {
					hostnames[key] = hostname
				}
			}
		}
	}
	return hostnames
}

func preferredAppConnectionContainerHostname(container dockerlzc.ContainerRuntimeInfo, endpoint dockerlzc.ContainerNetworkEndpoint) string {
	for _, names := range [][]string{endpoint.DNSNames, endpoint.Aliases} {
		for _, name := range names {
			if hostname := normalizeAppConnectionHostname(name, ".lzcapp"); hostname != "" {
				return hostname
			}
		}
	}
	service := strings.TrimSpace(container.Labels["com.docker.compose.service"])
	appID := strings.TrimSpace(container.AppID)
	if service == "" || appID == "" {
		return ""
	}
	return normalizeAppConnectionHostname(service+"."+appID+".lzcapp", ".lzcapp")

}

func normalizeAppConnectionHostname(raw, suffix string) string {
	hostname := strings.TrimSuffix(strings.TrimSpace(raw), ".")
	if hostname == "" || strings.ContainsAny(hostname, " /\\\t\r\n") || !strings.HasSuffix(strings.ToLower(hostname), suffix) {
		return ""
	}
	return hostname
}

func appConnectionLANHostnames(snapshot LANDeviceSnapshot) map[string]string {
	hostnames := make(map[string]string)
	addDevices := func(devices []LANDevice) {
		for _, device := range devices {
			if strings.EqualFold(strings.TrimSpace(device.Status), "offline") {
				continue
			}
			host := normalizeAppConnectionHostname(device.Hostname, ".lan")
			if host == "" {
				continue
			}
			addAddress := func(raw string) {
				address, err := netip.ParseAddr(strings.TrimSpace(raw))
				if err != nil || !isInternalAppConnectionAddress(address) {
					return
				}
				key := address.Unmap().String()
				if _, exists := hostnames[key]; !exists {
					hostnames[key] = host
				}
			}
			addAddress(device.IP)
			for _, address := range device.IPv6 {
				addAddress(address)
			}
		}
	}
	addDevices(snapshot.Devices)
	addDevices(snapshot.IgnoredDevices)
	addDevices(snapshot.PinnedDevices)
	return hostnames
}

func isInternalAppConnectionAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() {
		return false
	}
	return address.IsPrivate() || address.IsLinkLocalUnicast()
}
