package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"netwatch/internal/dockerlzc"
	"netwatch/internal/logger"
)

const (
	maxNetworkEvents  = 1000
	eventDedupeWindow = 2 * time.Minute
)

type NetworkEvent struct {
	ID        string         `json:"id"`
	Timestamp string         `json:"timestamp"`
	FirstSeen string         `json:"first_seen"`
	LastSeen  string         `json:"last_seen"`
	Kind      string         `json:"kind"`
	Severity  string         `json:"severity"`
	Source    string         `json:"source"`
	Title     string         `json:"title"`
	Summary   string         `json:"summary"`
	Details   map[string]any `json:"details,omitempty"`
	DedupeKey string         `json:"dedupe_key,omitempty"`
	Count     int            `json:"count"`
}

type NetworkEventQuery struct {
	Kind     string
	Severity string
	Since    time.Time
	Limit    int
}

type networkEventStore struct {
	mu                   sync.RWMutex
	path                 string
	events               []NetworkEvent
	seq                  uint64
	trafficBaselineReady bool
	trafficSampledAt     time.Time
	trafficCounters      map[string]AppBridgeStats
	trafficAppRuntime    map[string]AppBridgeStats
	trafficMetadataAt    time.Time
	trafficMetadata      map[string]AppBridgeStats
}

func newNetworkEventStore(dataDir string) *networkEventStore {
	store := &networkEventStore{
		path:              filepath.Join(dataDir, "network_events.json"),
		trafficCounters:   make(map[string]AppBridgeStats),
		trafficAppRuntime: make(map[string]AppBridgeStats),
		trafficMetadata:   make(map[string]AppBridgeStats),
	}
	if body, err := os.ReadFile(store.path); err == nil {
		_ = json.Unmarshal(body, &store.events)
	}
	if len(store.events) > maxNetworkEvents {
		store.events = store.events[len(store.events)-maxNetworkEvents:]
	}
	store.seq = uint64(len(store.events))
	return store
}

func (s *networkEventStore) append(event NetworkEvent) NetworkEvent {
	now := time.Now()
	if event.Timestamp == "" {
		event.Timestamp = now.Format(time.DateTime)
	}
	if event.FirstSeen == "" {
		event.FirstSeen = event.Timestamp
	}
	event.LastSeen = event.Timestamp
	if event.Count <= 0 {
		event.Count = 1
	}
	if event.Severity == "" {
		event.Severity = "info"
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if event.DedupeKey != "" {
		for i := len(s.events) - 1; i >= 0; i-- {
			candidate := s.events[i]
			candidateAt, ok := parseEventTime(candidate.LastSeen)
			if !ok || now.Sub(candidateAt) > eventDedupeWindow {
				break
			}
			if candidate.Kind != event.Kind || candidate.DedupeKey != event.DedupeKey {
				continue
			}
			candidate.LastSeen = event.Timestamp
			candidate.Timestamp = event.Timestamp
			candidate.Count++
			candidate.Summary = event.Summary
			candidate.Details = event.Details
			s.events = append(s.events[:i], s.events[i+1:]...)
			s.events = append(s.events, candidate)
			if err := writeJSONFile(s.path, s.events, true); err != nil {
				logger.Warn("network event persist: %v", err)
			}
			return candidate
		}
	}
	s.seq++
	if event.ID == "" {
		event.ID = fmt.Sprintf("evt-%d-%d", now.UnixMilli(), s.seq)
	}
	s.events = append(s.events, event)
	if len(s.events) > maxNetworkEvents {
		s.events = s.events[len(s.events)-maxNetworkEvents:]
	}
	if err := writeJSONFile(s.path, s.events, true); err != nil {
		logger.Warn("network event persist: %v", err)
	}
	return event
}

func (s *networkEventStore) query(query NetworkEventQuery) []NetworkEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	out := make([]NetworkEvent, 0, limit)
	for i := len(s.events) - 1; i >= 0 && len(out) < limit; i-- {
		event := s.events[i]
		if !networkEventDisplayable(event) {
			continue
		}
		if query.Kind != "" && event.Kind != query.Kind {
			continue
		}
		if query.Severity != "" && event.Severity != query.Severity {
			continue
		}
		if !query.Since.IsZero() {
			at, ok := parseEventTime(event.Timestamp)
			if !ok || at.Before(query.Since) {
				continue
			}
		}
		out = append(out, event)
	}
	return out
}

func networkEventDisplayable(event NetworkEvent) bool {
	if event.Kind == "app_bridge_appeared" || event.Kind == "app_bridge_disappeared" {
		return false
	}
	if event.Kind != "app_enabled" && event.Kind != "app_disabled" {
		return true
	}
	if event.Details == nil {
		return false
	}
	source := event.Details["lifecycle_source"]
	return source == "container_runtime_v2" || source == "container_runtime_v3"
}

func parseEventTime(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, time.DateTime} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func (s *Service) NetworkEvents(query NetworkEventQuery) []NetworkEvent {
	if s.events == nil {
		return []NetworkEvent{}
	}
	return s.events.query(query)
}

func (s *Service) appendNetworkEvent(event NetworkEvent) {
	if s.events != nil {
		s.events.append(event)
	}
}

func (s *Service) recordSummaryEvents(previous, current Summary) {
	if !previous.Ready || !current.Ready {
		return
	}
	recordChange := func(kind, severity, title, before, after string) {
		if before == after {
			return
		}
		s.appendNetworkEvent(NetworkEvent{
			Kind: kind, Severity: severity, Source: "network_observer", Title: title,
			Summary: fmt.Sprintf("%s -> %s", displayEventValue(before), displayEventValue(after)),
			Details: map[string]any{"before": before, "after": after}, DedupeKey: kind,
		})
	}
	recordChange("egress_ipv4_changed", "info", "出口 IPv4 已变化", previous.NetworkInfo.EgressIPv4, current.NetworkInfo.EgressIPv4)
	recordChange("egress_ipv6_changed", "info", "出口 IPv6 已变化", previous.NetworkInfo.EgressIPv6, current.NetworkInfo.EgressIPv6)
	recordChange("default_ipv4_gateway_changed", "warning", "IPv4 默认网关已变化", previous.NetworkInfo.DefaultIPv4.Gateway, current.NetworkInfo.DefaultIPv4.Gateway)
	recordChange("default_ipv6_gateway_changed", "warning", "IPv6 默认网关已变化", previous.NetworkInfo.DefaultIPv6.Gateway, current.NetworkInfo.DefaultIPv6.Gateway)
	recordChange("domestic_connectivity_changed", statusEventSeverity(current.WebsiteConnectivity.DomesticStatus), "国内网站连通性已变化", string(previous.WebsiteConnectivity.DomesticStatus), string(current.WebsiteConnectivity.DomesticStatus))
	recordChange("global_connectivity_changed", statusEventSeverity(current.WebsiteConnectivity.GlobalStatus), "国际网站连通性已变化", string(previous.WebsiteConnectivity.GlobalStatus), string(current.WebsiteConnectivity.GlobalStatus))
	recordChange("nat_type_changed", "warning", "NAT 类型已变化", previous.NetworkInfo.NAT.Type, current.NetworkInfo.NAT.Type)
	recordChange("nat_reachability_changed", boolEventSeverity(current.NetworkInfo.NAT.Reachable), "NAT 可达性已变化", fmt.Sprint(previous.NetworkInfo.NAT.Reachable), fmt.Sprint(current.NetworkInfo.NAT.Reachable))

	previousInterfaces := make(map[string]string, len(previous.NetworkInfo.Interfaces))
	for _, item := range previous.NetworkInfo.Interfaces {
		previousInterfaces[item.Name] = item.OperState
	}
	for _, item := range current.NetworkInfo.Interfaces {
		if before, ok := previousInterfaces[item.Name]; ok && before != item.OperState {
			s.appendNetworkEvent(NetworkEvent{
				Kind: "interface_state_changed", Severity: boolEventSeverity(item.OperState == "up"),
				Source: "network_observer", Title: "网卡状态已变化",
				Summary:   fmt.Sprintf("%s: %s -> %s", item.Name, displayEventValue(before), displayEventValue(item.OperState)),
				Details:   map[string]any{"interface": item.Name, "before": before, "after": item.OperState},
				DedupeKey: "interface_state_changed:" + item.Name,
			})
		}
	}
	previousPrefixes := interfaceIPv6Prefixes(previous.NetworkInfo.Interfaces)
	currentPrefixes := interfaceIPv6Prefixes(current.NetworkInfo.Interfaces)
	interfaceNames := make(map[string]bool, len(previousPrefixes)+len(currentPrefixes))
	for name := range previousPrefixes {
		interfaceNames[name] = true
	}
	for name := range currentPrefixes {
		interfaceNames[name] = true
	}
	for name := range interfaceNames {
		before := strings.Join(previousPrefixes[name], ", ")
		after := strings.Join(currentPrefixes[name], ", ")
		if before == after {
			continue
		}
		s.appendNetworkEvent(NetworkEvent{
			Kind: "ipv6_prefix_changed", Severity: "info", Source: "network_observer", Title: "IPv6 前缀已变化",
			Summary:   fmt.Sprintf("%s: %s -> %s", name, displayEventValue(before), displayEventValue(after)),
			Details:   map[string]any{"interface": name, "before": previousPrefixes[name], "after": currentPrefixes[name]},
			DedupeKey: "ipv6_prefix_changed:" + name,
		})
	}
}

func interfaceIPv6Prefixes(interfaces []InterfaceInfo) map[string][]string {
	out := make(map[string][]string)
	for _, item := range interfaces {
		seen := make(map[string]bool)
		for _, raw := range item.IPv6 {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil || !prefix.Addr().IsGlobalUnicast() || prefix.Addr().IsLinkLocalUnicast() {
				continue
			}
			value := prefix.Masked().String()
			if !seen[value] {
				seen[value] = true
				out[item.Name] = append(out[item.Name], value)
			}
		}
		sort.Strings(out[item.Name])
	}
	return out
}

func (s *Service) recordAppTrafficEvents() {
	if s.events == nil {
		return
	}
	threshold := s.notify.snapshotConfig().AbnormalTrafficThreshold
	s.events.observeAppTraffic(s.appTrafficEventStats(), time.Now(), threshold)
}

func (s *Service) appTrafficEventStats() []AppBridgeStats {
	counters := CollectAppTrafficCounters()
	runtime := make(map[string]dockerlzc.BridgeAppInfo)
	if dockerlzc.Available() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if current, err := dockerlzc.BuildBridgeMap(ctx); err == nil {
			runtime = current
		}
		cancel()
	}
	s.events.mu.RLock()
	metadataAt := s.events.trafficMetadataAt
	metadata := make(map[string]AppBridgeStats, len(s.events.trafficMetadata))
	for bridge, item := range s.events.trafficMetadata {
		metadata[bridge] = item
	}
	s.events.mu.RUnlock()
	refreshMetadata := time.Since(metadataAt) >= time.Minute
	if !refreshMetadata {
		for _, item := range counters {
			if _, ok := metadata[item.Bridge]; !ok {
				refreshMetadata = true
				break
			}
		}
	}
	if refreshMetadata {
		metadata = make(map[string]AppBridgeStats)
		for _, item := range CollectAppTraffic().Bridges {
			metadata[item.Bridge] = item
		}
		s.events.mu.Lock()
		s.events.trafficMetadataAt = time.Now()
		s.events.trafficMetadata = metadata
		s.events.mu.Unlock()
	}
	for index := range counters {
		if item, ok := metadata[counters[index].Bridge]; ok {
			counters[index].AppID = item.AppID
			counters[index].AppTitle = item.AppTitle
			counters[index].Project = item.Project
			counters[index].Icon = item.Icon
		}
		if item, ok := runtime[counters[index].Bridge]; ok {
			if counters[index].AppID == "" {
				counters[index].AppID = item.AppID
			}
			if counters[index].Project == "" {
				counters[index].Project = item.Project
			}
			if counters[index].AppTitle == "" && item.Title != item.AppID {
				counters[index].AppTitle = item.Title
			}
			counters[index].ContainerCount = item.ContainerCount
			counters[index].RunningCount = item.RunningCount
			counters[index].CreatedAt = item.CreatedAt
		}
	}
	return counters
}

func appTrafficEventName(item AppBridgeStats) string {
	if strings.TrimSpace(item.AppTitle) != "" {
		return strings.TrimSpace(item.AppTitle)
	}
	if strings.TrimSpace(item.AppID) != "" {
		return strings.TrimSpace(item.AppID)
	}
	if strings.TrimSpace(item.Project) != "" {
		return strings.TrimSpace(item.Project)
	}
	return "未知应用"
}

func appRuntimeKnown(item AppBridgeStats) bool {
	return item.ContainerCount > 0 || item.RunningCount > 0
}

func aggregateAppTrafficRuntime(stats []AppBridgeStats) map[string]AppBridgeStats {
	apps := make(map[string]AppBridgeStats)
	for _, item := range stats {
		key := appTrafficIdentityKey(item)
		current, exists := apps[key]
		if !exists {
			apps[key] = item
			continue
		}
		if current.AppID == "" {
			current.AppID = item.AppID
		}
		if current.AppTitle == "" {
			current.AppTitle = item.AppTitle
		}
		if current.Project == "" {
			current.Project = item.Project
		}
		if current.Icon == "" {
			current.Icon = item.Icon
		}
		if item.ContainerCount > current.ContainerCount {
			current.ContainerCount = item.ContainerCount
		}
		if item.RunningCount > current.RunningCount {
			current.RunningCount = item.RunningCount
			current.Bridge = item.Bridge
		}
		if item.CreatedAt > 0 && (current.CreatedAt == 0 || item.CreatedAt < current.CreatedAt) {
			current.CreatedAt = item.CreatedAt
		}
		apps[key] = current
	}
	return apps
}

func appEnabledEventTimestamp(item AppBridgeStats, previousAt, observedAt time.Time) string {
	if item.CreatedAt <= 0 {
		return observedAt.Format(time.DateTime)
	}
	startedAt := time.Unix(item.CreatedAt, 0)
	if startedAt.After(previousAt) && !startedAt.After(observedAt.Add(time.Minute)) {
		return startedAt.Format(time.DateTime)
	}
	return observedAt.Format(time.DateTime)
}

func (s *networkEventStore) observeAppTraffic(stats []AppBridgeStats, now time.Time, thresholdMbps int) {
	current := make(map[string]AppBridgeStats, len(stats))
	for _, item := range stats {
		current[item.Bridge] = item
	}
	currentApps := aggregateAppTrafficRuntime(stats)
	s.mu.Lock()
	if !s.trafficBaselineReady {
		s.trafficBaselineReady = true
		s.trafficSampledAt = now
		s.trafficCounters = current
		s.trafficAppRuntime = currentApps
		s.mu.Unlock()
		return
	}
	previous := s.trafficCounters
	previousApps := s.trafficAppRuntime
	previousAt := s.trafficSampledAt
	s.trafficCounters = current
	s.trafficAppRuntime = currentApps
	s.trafficSampledAt = now
	s.mu.Unlock()

	for key, item := range currentApps {
		before, existed := previousApps[key]
		if appRuntimeKnown(item) && item.RunningCount > 0 && (!existed || (appRuntimeKnown(before) && before.RunningCount == 0)) {
			appName := appTrafficEventName(item)
			s.append(NetworkEvent{
				Timestamp: appEnabledEventTimestamp(item, previousAt, now), Kind: "app_enabled", Severity: "info", Source: "app_traffic_observer", Title: "应用已启用",
				Summary: appName, Details: map[string]any{"bridge": item.Bridge, "app_id": item.AppID, "app_title": item.AppTitle, "project": item.Project, "icon": item.Icon, "lifecycle_source": "container_runtime_v3"}, DedupeKey: "app_lifecycle:" + key,
			})
		}
	}
	for key, item := range previousApps {
		after, exists := currentApps[key]
		if appRuntimeKnown(item) && item.RunningCount > 0 && (!exists || (appRuntimeKnown(after) && after.RunningCount == 0)) {
			appName := appTrafficEventName(item)
			s.append(NetworkEvent{
				Timestamp: now.Format(time.DateTime), Kind: "app_disabled", Severity: "warning", Source: "app_traffic_observer", Title: "应用已停用",
				Summary: appName, Details: map[string]any{"bridge": item.Bridge, "app_id": item.AppID, "app_title": item.AppTitle, "project": item.Project, "icon": item.Icon, "lifecycle_source": "container_runtime_v3"}, DedupeKey: "app_lifecycle:" + key,
			})
		}
	}
	seconds := now.Sub(previousAt).Seconds()
	if thresholdMbps <= 0 || seconds <= 0 {
		return
	}
	for bridge, item := range current {
		before, ok := previous[bridge]
		if !ok || item.RxBytes < before.RxBytes || item.TxBytes < before.TxBytes {
			continue
		}
		bytesDelta := item.RxBytes - before.RxBytes + item.TxBytes - before.TxBytes
		mbps := float64(bytesDelta) * 8 / seconds / 1_000_000
		if mbps < float64(thresholdMbps) {
			continue
		}
		s.append(NetworkEvent{
			Timestamp: now.Format(time.DateTime), Kind: "app_traffic_high", Severity: "warning", Source: "app_traffic_observer", Title: "应用流量超过阈值",
			Summary:   fmt.Sprintf("%s: %.1f Mbps", appTrafficEventName(item), mbps),
			Details:   map[string]any{"bridge": bridge, "app_id": item.AppID, "app_title": item.AppTitle, "project": item.Project, "icon": item.Icon, "mbps": mbps, "threshold_mbps": thresholdMbps, "interval_seconds": seconds},
			DedupeKey: "app_traffic_high:" + bridge,
		})
	}
}

func displayEventValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "未设置"
	}
	return value
}

func statusEventSeverity(status ProbeStatus) string {
	if status == StatusOK {
		return "info"
	}
	return "warning"
}

func boolEventSeverity(ok bool) string {
	if ok {
		return "info"
	}
	return "warning"
}

func NetworkEventKinds(events []NetworkEvent) []string {
	seen := make(map[string]bool)
	for _, event := range events {
		seen[event.Kind] = true
	}
	out := make([]string, 0, len(seen))
	for kind := range seen {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}
