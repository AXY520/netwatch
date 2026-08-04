package probe

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"netwatch/internal/dockerlzc"
)

const maxAppConnections = 200

func CollectAppConnectionSnapshot(ctx context.Context, bridge, appID, project string, limit int, reveal bool) AppConnectionSnapshot {
	if limit <= 0 || limit > maxAppConnections {
		limit = maxAppConnections
	}
	snapshot := AppConnectionSnapshot{GeneratedAt: localTimestamp(), Revealed: reveal, Limit: limit, Connections: []AppConnectionEntry{}}
	containers, err := dockerlzc.ListContainerRuntime(ctx)
	if err != nil {
		snapshot.Note = "无法读取容器运行时信息，连接快照不可用"
		return snapshot
	}
	if bridge != "" {
		if info, mapErr := dockerlzc.BuildBridgeMap(ctx); mapErr == nil {
			if owner, ok := info[bridge]; ok {
				appID, project = owner.AppID, owner.Project
			}
		}
	}
	targets := filterAppRuntime(containers, strings.TrimSpace(appID), strings.TrimSpace(project))
	if len(targets) == 0 {
		snapshot.Note = "未找到应用容器"
		return snapshot
	}
	seen := map[string]bool{}
	readable := false
	for _, container := range targets {
		if container.PID <= 0 || !container.Running {
			continue
		}
		for _, spec := range []struct{ file, protocol, version string }{{"tcp", "tcp", "ipv4"}, {"tcp6", "tcp", "ipv6"}, {"udp", "udp", "ipv4"}, {"udp6", "udp", "ipv6"}} {
			path := filepath.Join(hostProcRoot(), strconv.Itoa(container.PID), "net", spec.file)
			if firstReadableFile(path) != "" {
				readable = true
			}
			for _, entry := range readConnectionFile(path, spec.protocol, spec.version) {
				entry.ContainerID, entry.ContainerName = shortContainerID(container.ID), container.Name
				entry.AppID, entry.Project, entry.NetworkMode = container.AppID, container.Project, container.NetworkMode
				entry.AttributionReliable = container.NetworkMode != "host"
				key := fmt.Sprintf("%s|%s|%d|%s|%d|%s", entry.Protocol, entry.LocalAddress, entry.LocalPort, entry.RemoteAddress, entry.RemotePort, entry.State)
				if seen[key] {
					continue
				}
				seen[key] = true
				if !reveal {
					entry.RemoteAddress = maskPublicAddress(entry.RemoteAddress)
				}
				snapshot.Connections = append(snapshot.Connections, entry)
			}
		}
	}
	if !readable {
		snapshot.Note = "宿主进程网络表不可读，连接快照不可用"
		return snapshot
	}
	snapshot.Supported = true
	sort.Slice(snapshot.Connections, func(i, j int) bool {
		if snapshot.Connections[i].State != snapshot.Connections[j].State {
			return snapshot.Connections[i].State < snapshot.Connections[j].State
		}
		if snapshot.Connections[i].RemoteAddress != snapshot.Connections[j].RemoteAddress {
			return snapshot.Connections[i].RemoteAddress < snapshot.Connections[j].RemoteAddress
		}
		return snapshot.Connections[i].RemotePort < snapshot.Connections[j].RemotePort
	})
	if len(snapshot.Connections) > limit {
		snapshot.Connections = snapshot.Connections[:limit]
		snapshot.Truncated = true
	}
	if hasHostNetworkContainer(targets) {
		snapshot.Note = "host network 容器共享宿主网络命名空间，连接可见但无法可靠归属到单个应用"
	}
	return snapshot
}

func readConnectionFile(path, protocol, version string) []AppConnectionEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	out := []AppConnectionEntry{}
	scanner := bufio.NewScanner(f)
	first := true
	for scanner.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		localIP, localPort, ok1 := parseConnectionAddress(fields[1], version)
		remoteIP, remotePort, ok2 := parseConnectionAddress(fields[2], version)
		if !ok1 || !ok2 || remotePort == 0 || remoteIP.IsUnspecified() {
			continue
		}
		state := tcpState(fields[3])
		if protocol == "tcp" && state == "LISTEN" {
			continue
		}
		if protocol == "udp" {
			state = "ACTIVE"
		}
		out = append(out, AppConnectionEntry{Protocol: protocol, IPVersion: version, LocalAddress: localIP.String(), LocalPort: localPort, RemoteAddress: remoteIP.String(), RemotePort: remotePort, State: state, Direction: "outbound"})
	}
	return out
}

func parseConnectionAddress(value, version string) (netip.Addr, int, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return netip.Addr{}, 0, false
	}
	port, err := strconv.ParseUint(parts[1], 16, 16)
	if err != nil {
		return netip.Addr{}, 0, false
	}
	b, err := hex.DecodeString(parts[0])
	if err != nil {
		return netip.Addr{}, 0, false
	}
	if version == "ipv4" && len(b) == 4 {
		return netip.AddrFrom4([4]byte{b[3], b[2], b[1], b[0]}), int(port), true
	}
	if version == "ipv6" && len(b) == 16 {
		for i := 0; i < 16; i += 4 {
			b[i], b[i+3] = b[i+3], b[i]
			b[i+1], b[i+2] = b[i+2], b[i+1]
		}
		var raw [16]byte
		copy(raw[:], b)
		return netip.AddrFrom16(raw), int(port), true
	}
	return netip.Addr{}, 0, false
}

func maskPublicAddress(value string) string {
	addr, err := netip.ParseAddr(value)
	if err != nil || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
		return value
	}
	if addr.Is4() {
		b := addr.As4()
		return net.IPv4(b[0], b[1], b[2], 0).String() + "/24"
	}
	return netip.PrefixFrom(addr, 48).Masked().String()
}
