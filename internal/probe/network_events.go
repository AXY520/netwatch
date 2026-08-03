package probe

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

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
	mu     sync.RWMutex
	path   string
	events []NetworkEvent
	seq    uint64
}

func newNetworkEventStore(dataDir string) *networkEventStore {
	store := &networkEventStore{path: filepath.Join(dataDir, "network_events.json")}
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
