package probe

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"netwatch/internal/appmeta"
	"netwatch/internal/dockerlzc"
	"netwatch/internal/lzcsdk"
)

// AppBridgeStats describes traffic counters for a single lzc application
// docker network bridge as seen from the host network namespace.
//
// Each lzcapp gets its own bridge named "lzc-br-XXXXXXXX" and a /26 IPv4 subnet
// (and a fd03:1136:3800::/122 IPv6 subnet). All container egress traffic for
// that app crosses this bridge, so its rx/tx counters approximate
// "this application's traffic" — though host-network-mode app services bypass
// the bridge entirely and won't be counted here.
type AppBridgeStats struct {
	Bridge   string `json:"bridge"`
	AppID    string `json:"app_id,omitempty"`
	AppTitle string `json:"app_title,omitempty"`
	Project  string `json:"project,omitempty"`
	SubnetV4 string `json:"subnet_v4,omitempty"`
	SubnetV6 string `json:"subnet_v6,omitempty"`
	RxBytes  uint64 `json:"rx_bytes"`
	TxBytes  uint64 `json:"tx_bytes"`
}

type AppTrafficSnapshot struct {
	GeneratedAt string           `json:"generated_at"`
	Bridges     []AppBridgeStats `json:"bridges"`
	Note        string           `json:"note,omitempty"`
}

const (
	sysClassNetDir  = "/sys/class/net"
	lzcBridgePrefix = "lzc-br-"
)

// CollectAppTraffic enumerates all `lzc-br-*` bridges in the current network
// namespace and reports their cumulative byte counters.
//
// When the lzc-docker socket is mounted (see lzc-build.yml compose_override),
// each bridge is enriched with its owning appid and docker compose project.
//
// Requires the calling process to share the host's network namespace
// (`network_mode: host` in lzc-manifest.yml) — otherwise the bridges aren't
// visible. NET_ADMIN is not strictly required just for /sys reads.
func CollectAppTraffic() AppTrafficSnapshot {
	snap := AppTrafficSnapshot{GeneratedAt: localTimestamp()}
	entries, err := os.ReadDir(sysClassNetDir)
	if err != nil {
		snap.Note = "无法访问 /sys/class/net (容器需要 host 网络模式)"
		return snap
	}

	addrByName := bridgeAddresses()

	// Best-effort: when the socket isn't mounted or the call fails, we still
	// return bridge-level stats — just without the app name column.
	var bridgeMap map[string]dockerlzc.BridgeAppInfo
	if dockerlzc.Available() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if m, err := dockerlzc.BuildBridgeMap(ctx); err == nil {
			bridgeMap = m
		}
		cancel()
	}

	// 通过 SDK PackageManager.QueryApplication 拿应用中文 title。失败时回落到
	// 直接使用 appid（不影响主流程）。
	var appMap map[string]lzcsdk.AppInfo
	if lzcsdk.Available() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if m, err := lzcsdk.ListApps(ctx); err == nil {
			appMap = m
		}
		cancel()
	}

	// 兜底：从 /lzcapp/run/pkgm/<appid>/pkg/package.yml 读 name 字段。
	// 测试机上 SDK 返回的 Title 经常为空，本地扫描更稳。
	var localTitles map[string]string
	if appmeta.Available() {
		if m, err := appmeta.LoadTitles(); err == nil {
			localTitles = m
		}
	}

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, lzcBridgePrefix) {
			continue
		}
		stats := AppBridgeStats{Bridge: name}
		stats.RxBytes = readSysCounter(filepath.Join(sysClassNetDir, name, "statistics", "rx_bytes"))
		stats.TxBytes = readSysCounter(filepath.Join(sysClassNetDir, name, "statistics", "tx_bytes"))
		if addrs, ok := addrByName[name]; ok {
			stats.SubnetV4 = addrs.v4
			stats.SubnetV6 = addrs.v6
		}
		if info, ok := bridgeMap[name]; ok {
			stats.AppID = info.AppID
			stats.Project = info.Project
			// 优先使用本地 package.yml 读到的中文 name；SDK 兜底
			if title, ok := localTitles[info.AppID]; ok && title != "" {
				stats.AppTitle = title
			} else if app, ok := appMap[info.AppID]; ok && app.Title != "" && app.Title != info.AppID {
				stats.AppTitle = app.Title
			}
		}
		snap.Bridges = append(snap.Bridges, stats)
	}

	// 按总流量降序，便于前端排序展示
	sort.Slice(snap.Bridges, func(i, j int) bool {
		return snap.Bridges[i].RxBytes+snap.Bridges[i].TxBytes >
			snap.Bridges[j].RxBytes+snap.Bridges[j].TxBytes
	})

	if len(snap.Bridges) == 0 {
		snap.Note = "未发现 lzc-br-* 网桥"
	} else if bridgeMap == nil {
		snap.Note = "未挂载 lzc-docker socket，仅展示网桥级流量。请检查 lzc-build.yml 的 docker.sock bind"
	}
	return snap
}

type bridgeAddrs struct {
	v4 string
	v6 string
}

// bridgeAddresses scans net.Interfaces once and returns the first
// non-link-local IPv4/IPv6 subnet for each lzc-br-* bridge.
func bridgeAddresses() map[string]bridgeAddrs {
	out := map[string]bridgeAddrs{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range ifaces {
		if !strings.HasPrefix(iface.Name, lzcBridgePrefix) {
			continue
		}
		addrs, _ := iface.Addrs()
		var v4, v6 string
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ipNet.IP.To4() != nil {
				if v4 == "" {
					v4 = ipNet.String()
				}
				continue
			}
			if ipNet.IP.IsLinkLocalUnicast() {
				continue
			}
			if v6 == "" {
				v6 = ipNet.String()
			}
		}
		out[iface.Name] = bridgeAddrs{v4: v4, v6: v6}
	}
	return out
}

func readSysCounter(path string) uint64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	v, _ := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	return v
}
