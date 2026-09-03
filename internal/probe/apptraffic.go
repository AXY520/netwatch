package probe

import (
	"context"
	"errors"
	"fmt"
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

// sanitizeIconURL converts SDK icon URLs like "https://$boxdomain/sys/icons/X.png"
// to relative paths "/sys/icons/X.png" so they work from any access domain.
func sanitizeIconURL(raw, boxDomain string) string {
	if raw == "" {
		return raw
	}
	s := strings.TrimSpace(raw)
	if strings.Contains(s, "$boxdomain") && boxDomain != "" {
		s = strings.ReplaceAll(s, "$boxdomain", strings.TrimSpace(boxDomain))
	} else if strings.Contains(s, "$boxdomain") {
		if idx := strings.Index(s, "/sys/icons/"); idx >= 0 {
			s = s[idx:]
		} else {
			s = strings.ReplaceAll(s, "$boxdomain", "")
		}
	}
	// Strip scheme + host if present
	if idx := strings.Index(s, "/sys/icons/"); idx >= 0 {
		if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
			return s
		}
		if boxDomain != "" {
			return "https://" + strings.TrimSuffix(boxDomain, "/") + s[idx:]
		}
		return s[idx:]
	}
	if strings.HasPrefix(s, "/") {
		return s
	}
	return raw
}

// AppBridgeStats describes traffic counters for a single lzc application
// docker network bridge as seen from the host network namespace.
//
// Each lzcapp gets its own bridge named "lzc-br-XXXXXXXX" and a /26 IPv4 subnet
// (and a fd03:1136:3800::/122 IPv6 subnet). All container egress traffic for
// that app crosses this bridge, so its rx/tx counters approximate
// "this application's traffic". These are host-bridge directions: RX is traffic
// entering the host bridge from app containers (application upload), while TX
// is traffic sent from the host bridge to app containers (application download).
// Host-network-mode app services bypass the bridge entirely. They are counted
// separately through the cgroup eBPF collector.
type AppBridgeStats struct {
	Bridge             string           `json:"bridge"`
	AppID              string           `json:"app_id,omitempty"`
	InstanceID         string           `json:"instance_id,omitempty"`
	UserID             string           `json:"user_id,omitempty"`
	MultiInstance      bool             `json:"multi_instance,omitempty"`
	AppTitle           string           `json:"app_title,omitempty"`
	Project            string           `json:"project,omitempty"`
	SubnetV4           string           `json:"subnet_v4,omitempty"`
	SubnetV6           string           `json:"subnet_v6,omitempty"`
	RxBytes            uint64           `json:"rx_bytes"`
	TxBytes            uint64           `json:"tx_bytes"`
	UploadBytes        uint64           `json:"upload_bytes"`
	DownloadBytes      uint64           `json:"download_bytes"`
	RxPackets          uint64           `json:"rx_packets"`
	TxPackets          uint64           `json:"tx_packets"`
	RxErrors           uint64           `json:"rx_errors"`
	TxErrors           uint64           `json:"tx_errors"`
	RxDropped          uint64           `json:"rx_dropped"`
	TxDropped          uint64           `json:"tx_dropped"`
	ContainerCount     int              `json:"container_count,omitempty"`
	RunningCount       int              `json:"running_count,omitempty"`
	Domain             string           `json:"domain,omitempty"`
	Icon               string           `json:"icon,omitempty"`
	StatusText         string           `json:"status_text,omitempty"`
	CreatedAt          int64            `json:"created_at,omitempty"`
	SampledAt          string           `json:"sampled_at"`
	AgeSeconds         int64            `json:"age_seconds"`
	Stale              bool             `json:"stale"`
	CounterPerspective string           `json:"counter_perspective"`
	Source             string           `json:"source"`
	NetworkMode        string           `json:"network_mode,omitempty"`
	CgroupPath         string           `json:"cgroup_path,omitempty"`
	Experimental       bool             `json:"experimental,omitempty"`
	ControlTarget      string           `json:"control_target,omitempty"`
	Diagnostic         string           `json:"diagnostic,omitempty"`
	Target             AppNetworkTarget `json:"target"`
}

type AppTrafficSnapshot struct {
	GeneratedAt        string            `json:"generated_at"`
	Bridges            []AppBridgeStats  `json:"bridges"`
	Apps               []AppTrafficUsage `json:"apps,omitempty"`
	LimitSupport       bool              `json:"limit_support"`
	Note               string            `json:"note,omitempty"`
	CounterPerspective string            `json:"counter_perspective"`
	Source             string            `json:"source"`
}

type appTrafficMetadata struct {
	bridgeMap        map[string]dockerlzc.BridgeAppInfo
	appMap           map[string]lzcsdk.AppInfo
	boxDomain        string
	localTitles      map[string]string
	localAppIDs      []string
	hostAppIDs       map[string]bool
	hostProjects     map[string]bool
	dockerDiagnostic string
	dockerStale      bool
}

const (
	appTrafficMetadataTTL        = time.Minute
	appTrafficMetadataRetryDelay = 5 * time.Second
	appTrafficDockerReadTimeout  = 5 * time.Second
)

var appTrafficMetadataCache struct {
	sync.RWMutex
	at         time.Time
	retryAfter time.Time
	refreshing bool
	generation uint64
	data       appTrafficMetadata
}

func InvalidateAppTrafficMetadataCache() {
	appTrafficMetadataCache.Lock()
	appTrafficMetadataCache.at = time.Time{}
	appTrafficMetadataCache.generation++
	appTrafficMetadataCache.Unlock()
}

func cachedAppTrafficMetadata() appTrafficMetadata {
	now := time.Now()
	appTrafficMetadataCache.Lock()
	if !appTrafficMetadataCache.at.IsZero() && now.Sub(appTrafficMetadataCache.at) < appTrafficMetadataTTL {
		metadata := appTrafficMetadataCache.data
		appTrafficMetadataCache.Unlock()
		return metadata
	}
	if now.Before(appTrafficMetadataCache.retryAfter) {
		metadata := appTrafficMetadataCache.data
		appTrafficMetadataCache.Unlock()
		return metadata
	}
	if appTrafficMetadataCache.refreshing {
		metadata := appTrafficMetadataCache.data
		if metadata.bridgeMap == nil && metadata.dockerDiagnostic == "" {
			metadata.dockerDiagnostic = "正在读取 lzc-docker 应用信息"
		}
		appTrafficMetadataCache.Unlock()
		return metadata
	}
	appTrafficMetadataCache.refreshing = true
	generation := appTrafficMetadataCache.generation
	metadata := appTrafficMetadataCache.data
	appTrafficMetadataCache.Unlock()

	var dockerErr error
	if dockerlzc.Available() {
		ctx, cancel := context.WithTimeout(context.Background(), appTrafficDockerReadTimeout)
		bridgeMap, containers, err := dockerlzc.BuildBridgeMapWithRuntime(ctx)
		cancel()
		dockerErr = err
		metadata = mergeAppTrafficDockerMetadata(metadata, bridgeMap, containers, err, true)
	} else {
		dockerErr = errors.New("docker socket not mounted")
		metadata = mergeAppTrafficDockerMetadata(metadata, nil, nil, dockerErr, false)
	}
	if lzcsdk.Available() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		metadata.appMap, _ = lzcsdk.ListApps(ctx)
		metadata.boxDomain = lzcsdk.BoxDomain(ctx)
		cancel()
	}
	if appmeta.Available() {
		metadata.localTitles, _ = appmeta.LoadTitles()
		metadata.localAppIDs, _ = appmeta.LoadAppIDs()
	}
	appTrafficMetadataCache.Lock()
	if dockerErr != nil {
		appTrafficMetadataCache.at = time.Time{}
		appTrafficMetadataCache.retryAfter = time.Now().Add(appTrafficMetadataRetryDelay)
	} else {
		appTrafficMetadataCache.retryAfter = time.Time{}
		if generation == appTrafficMetadataCache.generation {
			appTrafficMetadataCache.at = time.Now()
		} else {
			appTrafficMetadataCache.at = time.Time{}
		}
	}
	appTrafficMetadataCache.data = metadata
	appTrafficMetadataCache.refreshing = false
	appTrafficMetadataCache.Unlock()
	return metadata
}

func mergeAppTrafficDockerMetadata(previous appTrafficMetadata, bridgeMap map[string]dockerlzc.BridgeAppInfo, containers []dockerlzc.ContainerRuntimeInfo, err error, socketMounted bool) appTrafficMetadata {
	if err != nil {
		previous.dockerStale = previous.bridgeMap != nil
		if !socketMounted {
			previous.dockerDiagnostic = "未检测到 lzc-docker socket 挂载"
		} else if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "context deadline exceeded") {
			previous.dockerDiagnostic = "读取 lzc-docker 应用拓扑超时"
		} else {
			previous.dockerDiagnostic = "lzc-docker API 暂时不可用：" + strings.TrimSpace(err.Error())
		}
		return previous
	}

	previous.bridgeMap = bridgeMap
	previous.hostAppIDs = make(map[string]bool)
	previous.hostProjects = make(map[string]bool)
	for _, container := range containers {
		if !container.Running || container.NetworkMode != "host" {
			continue
		}
		if appID := strings.TrimSpace(container.AppID); appID != "" {
			previous.hostAppIDs[appID] = true
		}
		if project := strings.TrimSpace(container.Project); project != "" {
			previous.hostProjects[project] = true
		}
	}
	previous.dockerDiagnostic = ""
	previous.dockerStale = false
	return previous
}

const (
	sysClassNetDir               = "/sys/class/net"
	lzcBridgePrefix              = "lzc-br-"
	appTrafficCounterPerspective = "host_bridge"
	appTrafficSource             = "linux_bridge_sysfs"
)

func finalizeAppBridgeStats(stats *AppBridgeStats, sampledAt string) {
	if stats.NetworkMode == "" {
		stats.NetworkMode = "bridge"
	}
	stats.UploadBytes = stats.RxBytes
	stats.DownloadBytes = stats.TxBytes
	stats.SampledAt = sampledAt
	stats.AgeSeconds = 0
	stats.Stale = false
	stats.CounterPerspective = appTrafficCounterPerspective
	stats.Source = appTrafficSource
}

func CollectBridgeTraffic(bridge string) (AppBridgeStats, bool) {
	if !strings.HasPrefix(bridge, lzcBridgePrefix) {
		return AppBridgeStats{}, false
	}
	statsPath := filepath.Join(sysClassNetDir, bridge, "statistics")
	if _, err := os.Stat(statsPath); err != nil {
		return AppBridgeStats{}, false
	}
	stats := AppBridgeStats{
		Bridge:    bridge,
		RxBytes:   readSysCounter(filepath.Join(statsPath, "rx_bytes")),
		TxBytes:   readSysCounter(filepath.Join(statsPath, "tx_bytes")),
		RxPackets: readSysCounter(filepath.Join(statsPath, "rx_packets")),
		TxPackets: readSysCounter(filepath.Join(statsPath, "tx_packets")),
		RxErrors:  readSysCounter(filepath.Join(statsPath, "rx_errors")),
		TxErrors:  readSysCounter(filepath.Join(statsPath, "tx_errors")),
		RxDropped: readSysCounter(filepath.Join(statsPath, "rx_dropped")),
		TxDropped: readSysCounter(filepath.Join(statsPath, "tx_dropped")),
	}
	finalizeAppBridgeStats(&stats, localTimestamp())
	if addrs, ok := bridgeAddresses()[bridge]; ok {
		stats.SubnetV4 = addrs.v4
		stats.SubnetV6 = addrs.v6
	}
	return stats, true
}

// CollectAppTrafficCounters returns lightweight bridge counters without
// querying Docker or the Lazycat SDK. It is intended for frequent metrics
// scrapes where application metadata enrichment would be unnecessarily costly.
func CollectAppTrafficCounters() []AppBridgeStats {
	entries, err := os.ReadDir(sysClassNetDir)
	if err != nil {
		return nil
	}
	sampledAt := localTimestamp()
	stats := make([]AppBridgeStats, 0)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, lzcBridgePrefix) {
			continue
		}
		statsPath := filepath.Join(sysClassNetDir, name, "statistics")
		item := AppBridgeStats{
			Bridge:  name,
			RxBytes: readSysCounter(filepath.Join(statsPath, "rx_bytes")),
			TxBytes: readSysCounter(filepath.Join(statsPath, "tx_bytes")),
		}
		finalizeAppBridgeStats(&item, sampledAt)
		stats = append(stats, item)
	}
	return stats
}

// CollectAppTraffic enumerates all `lzc-br-*` bridges in the current network
// namespace and reports their cumulative byte counters.
//
// When the lzc-docker socket is mounted (see lzc-build.yml compose_override),
// each bridge is enriched with its owning appid and docker compose project.
//
// Requires the calling process to share the host's network namespace
// (`network_mode: host` in lzc-manifest.yml) — otherwise the bridges aren't
// visible. NET_ADMIN is not strictly required just for /sys reads.
// isNetwatchApp returns true if the bridge belongs to the netwatch app itself.
// Keep matching strict: a loose "contains netwatch" would hide unrelated apps.
func isNetwatchApp(info dockerlzc.BridgeAppInfo) bool {
	id := strings.ToLower(strings.TrimSpace(info.AppID))
	if id == "cloud.lazycat.app.netwatch" || id == "netwatch" {
		return true
	}
	proj := strings.ToLower(strings.TrimSpace(info.Project))
	// compose project drops dots: cloudlazycatappnetwatch
	compact := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, proj)
	return compact == "cloudlazycatappnetwatch" || compact == "netwatch"
}

func appTrafficIdentityKey(item AppBridgeStats) string {
	if value := appTrafficPolicyID(item); value != "" {
		return "app:" + value
	}
	if value := strings.TrimSpace(item.Project); value != "" {
		return "project:" + value
	}
	return "bridge:" + strings.TrimSpace(item.Bridge)
}

func filterSupersededAppBridges(items []AppBridgeStats) []AppBridgeStats {
	hasAttachedBridge := make(map[string]bool)
	for _, item := range items {
		key := appTrafficIdentityKey(item)
		if !strings.HasPrefix(key, "bridge:") && item.ContainerCount > 0 {
			hasAttachedBridge[key] = true
		}
	}
	filtered := make([]AppBridgeStats, 0, len(items))
	for _, item := range items {
		key := appTrafficIdentityKey(item)
		if hasAttachedBridge[key] && item.ContainerCount == 0 {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func CollectAppTraffic() AppTrafficSnapshot {
	return collectAppTraffic()
}

func collectAppTraffic() AppTrafficSnapshot {
	sampledAt := localTimestamp()
	snap := AppTrafficSnapshot{
		GeneratedAt:        sampledAt,
		CounterPerspective: appTrafficCounterPerspective,
		Source:             appTrafficSource,
	}
	entries, err := os.ReadDir(sysClassNetDir)
	if err != nil {
		snap.Note = "无法访问 /sys/class/net (容器需要 host 网络模式)"
		return snap
	}

	addrByName := bridgeAddresses()

	metadata := cachedAppTrafficMetadata()
	bridgeMap := metadata.bridgeMap
	appMap := metadata.appMap
	boxDomain := metadata.boxDomain
	localTitles := metadata.localTitles
	localAppIDs := metadata.localAppIDs
	projectAppIDs := map[string]string{}
	for appID := range appMap {
		projectAppIDs[normalizeAppProject(appID)] = appID
	}
	for appID := range localTitles {
		projectAppIDs[normalizeAppProject(appID)] = appID
	}
	for _, appID := range localAppIDs {
		projectAppIDs[normalizeAppProject(appID)] = appID
	}

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, lzcBridgePrefix) {
			continue
		}
		statsPath := filepath.Join(sysClassNetDir, name, "statistics")
		stats := AppBridgeStats{
			Bridge:    name,
			RxBytes:   readSysCounter(filepath.Join(statsPath, "rx_bytes")),
			TxBytes:   readSysCounter(filepath.Join(statsPath, "tx_bytes")),
			RxPackets: readSysCounter(filepath.Join(statsPath, "rx_packets")),
			TxPackets: readSysCounter(filepath.Join(statsPath, "tx_packets")),
			RxErrors:  readSysCounter(filepath.Join(statsPath, "rx_errors")),
			TxErrors:  readSysCounter(filepath.Join(statsPath, "tx_errors")),
			RxDropped: readSysCounter(filepath.Join(statsPath, "rx_dropped")),
			TxDropped: readSysCounter(filepath.Join(statsPath, "tx_dropped")),
		}
		finalizeAppBridgeStats(&stats, sampledAt)
		if addrs, ok := addrByName[name]; ok {
			stats.SubnetV4 = addrs.v4
			stats.SubnetV6 = addrs.v6
		}
		if info, ok := bridgeMap[name]; ok {
			if isNetwatchApp(info) || isExcludedApp(info.AppID, info.Title) {
				continue
			}
			stats.AppID = info.AppID
			if stats.AppID == "" {
				stats.AppID = projectAppIDs[normalizeAppProject(info.Project)]
			}
			stats.Project = info.Project
			stats.InstanceID = info.InstanceID
			stats.UserID = info.UserID
			stats.MultiInstance = info.MultiInstance
			if stats.InstanceID == "" {
				stats.InstanceID = stats.AppID
			}
			stats.ContainerCount = info.ContainerCount
			stats.RunningCount = info.RunningCount
			stats.StatusText = info.StatusText
			stats.CreatedAt = info.CreatedAt
			// 优先使用本地 package.yml 读到的中文 name；SDK 兜底
			if title, ok := localTitles[stats.AppID]; ok && title != "" {
				stats.AppTitle = title
			} else if app, ok := appMap[stats.AppID]; ok && app.Title != "" && app.Title != stats.AppID {
				stats.AppTitle = app.Title
			}
			if app, ok := appMap[stats.AppID]; ok {
				if app.Domain != "" {
					stats.Domain = app.Domain
				}
				if app.Icon != "" {
					stats.Icon = sanitizeIconURL(app.Icon, boxDomain)
				}
			}
			if stats.Icon == "" && stats.AppID != "" && boxDomain != "" {
				stats.Icon = "https://" + strings.TrimSuffix(boxDomain, "/") + "/sys/icons/" + stats.AppID + ".png"
			}
		}
		if stats.AppID != "" {
			stats.Target = AppNetworkTarget{
				ID: stats.Bridge, Kind: AppNetworkTargetBridge, AppID: stats.AppID, InstanceID: stats.InstanceID,
				Interface: stats.Bridge, NetworkMode: "bridge", AccountingSource: stats.Source,
			}
		}
		snap.Bridges = append(snap.Bridges, stats)
	}
	snap.Bridges = filterSupersededAppBridges(snap.Bridges)
	hostStats := collectHostNetworkTraffic(metadata)
	snap.Bridges = append(snap.Bridges, hostStats...)
	annotateHostInstanceControlIsolation(snap.Bridges)
	hostStatsAvailable := false
	hostStatsUnavailable := false
	for _, item := range hostStats {
		if item.Source == "cgroup_skb_ebpf" {
			hostStatsAvailable = true
		}
		if item.Source == "cgroup_skb_ebpf_unavailable" {
			hostStatsUnavailable = true
		}
	}
	if len(hostStats) > 0 {
		snap.CounterPerspective = "mixed"
		if hostStatsAvailable {
			snap.Source = "linux_bridge_sysfs+cgroup_skb_ebpf"
		} else {
			snap.Source = "linux_bridge_sysfs+cgroup_skb_ebpf_unavailable"
		}
	}
	if hostStatsUnavailable {
		snap.Note = hostTrafficAvailabilityNote(hostStats)
	}

	// 按总流量降序，便于前端排序展示
	sort.Slice(snap.Bridges, func(i, j int) bool {
		return snap.Bridges[i].RxBytes+snap.Bridges[i].TxBytes >
			snap.Bridges[j].RxBytes+snap.Bridges[j].TxBytes
	})

	if dockerNote := appTrafficDockerMetadataNote(metadata); dockerNote != "" {
		snap.Note = appendAppTrafficNote(snap.Note, dockerNote)
	}
	if len(snap.Bridges) == 0 && snap.Note == "" {
		snap.Note = "未发现 lzc-br-* 网桥"
	}
	return snap
}

func appTrafficDockerMetadataNote(metadata appTrafficMetadata) string {
	diagnostic := strings.TrimSpace(metadata.dockerDiagnostic)
	if diagnostic == "" {
		return ""
	}
	if metadata.dockerStale {
		return "lzc-docker 暂时不可用，正在展示最近一次成功读取的应用信息（" + diagnostic + "）"
	}
	return diagnostic + "；暂时仅展示网桥级流量"
}

func appendAppTrafficNote(current, next string) string {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if current == "" {
		return next
	}
	if next == "" || strings.Contains(current, next) {
		return current
	}
	return strings.TrimRight(current, "。；") + "；" + next
}

func hostTrafficDiagnostic(items []AppBridgeStats) string {
	for _, item := range items {
		if item.Source == "cgroup_skb_ebpf_unavailable" && strings.TrimSpace(item.Diagnostic) != "" {
			return strings.TrimSpace(item.Diagnostic)
		}
	}
	return ""
}

func hostTrafficAvailabilityNote(items []AppBridgeStats) string {
	unavailable := 0
	for _, item := range items {
		if item.Source == "cgroup_skb_ebpf_unavailable" {
			unavailable++
		}
	}
	if unavailable == 0 {
		return ""
	}
	diagnostic := hostTrafficDiagnostic(items)
	if diagnostic == "" {
		diagnostic = "未获得 eBPF 诊断信息，Host 容器 cgroup 路径解析或初始化未完成"
	}
	if unavailable < len(items) {
		return fmt.Sprintf(
			"Host 模式流量统计部分不可用：%d/%d 个 Host 容器不可用（%s）；其余 Host 与 Bridge 流量统计不受影响。",
			unavailable, len(items), diagnostic,
		)
	}
	return "Host 模式流量统计不可用：" + diagnostic + "；Bridge 流量统计不受影响。"
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
