package probe

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"netwatch/internal/logger"
)

const networkMutationAuditMaxRead = 200

func networkMutationAuditPath(dataDir string) string {
	return filepath.Join(dataDir, "network_mutation_audit.jsonl")
}

func (s *Service) auditNetworkMutation(event NetworkMutationAuditEvent, mutation *networkMutation) {
	if event.Timestamp == "" {
		event.Timestamp = localTimestamp()
	}
	if mutation != nil {
		if event.ID == "" {
			event.ID = mutation.ID
		}
		if event.Kind == "" {
			event.Kind = string(mutation.Kind)
		}
		if event.Target == "" {
			event.Target = mutation.Target
		}
		if event.State == "" {
			event.State = string(mutation.Status)
		}
		if event.DurationMS == 0 && !mutation.StartedAt.IsZero() {
			event.DurationMS = time.Since(mutation.StartedAt).Milliseconds()
		}
		event.Requested, event.Previous = networkMutationAuditPayloads(mutation)
	}

	path := networkMutationAuditPath(s.cfg.DataDir)
	s.network.auditMu.Lock()
	defer s.network.auditMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logger.Warn("network mutation audit mkdir: %v", err)
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		logger.Warn("network mutation audit open: %v", err)
		return
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(event); err != nil {
		logger.Warn("network mutation audit write: %v", err)
	}
	severity := "info"
	if event.State == "failed" || event.State == "rolled_back" || event.Error != "" {
		severity = "warning"
	}
	s.appendNetworkEvent(NetworkEvent{
		Timestamp: event.Timestamp,
		Kind:      "network_mutation_" + event.State,
		Severity:  severity,
		Source:    "network_control",
		Title:     "网络配置操作：" + event.Kind,
		Summary:   displayEventValue(event.Target) + " / " + displayEventValue(event.State),
		Details: map[string]any{
			"mutation_id":   event.ID,
			"mutation_kind": event.Kind,
			"target":        event.Target,
			"state":         event.State,
			"error":         event.Error,
			"duration_ms":   event.DurationMS,
		},
		DedupeKey: "",
	})
}

func networkMutationAuditPayloads(m *networkMutation) (requested, previous json.RawMessage) {
	if m == nil {
		return nil, nil
	}
	var req, prev any
	switch m.Kind {
	case networkMutationIP:
		if m.IP != nil {
			req, prev = m.IP.Request, m.IP.Snapshot
		}
	case networkMutationBridge:
		if m.Bridge != nil {
			req, prev = m.Bridge.Record, m.Bridge.Original
		}
	case networkMutationDNS:
		if m.DNS != nil {
			req, prev = m.DNS.Request, m.DNS.Snapshot
		}
	}
	requested, _ = json.Marshal(req)
	previous, _ = json.Marshal(prev)
	return requested, previous
}

func (s *Service) NetworkMutationAudit(limit int) []NetworkMutationAuditEvent {
	if limit <= 0 {
		limit = 50
	}
	if limit > networkMutationAuditMaxRead {
		limit = networkMutationAuditMaxRead
	}
	path := networkMutationAuditPath(s.cfg.DataDir)
	s.network.auditMu.Lock()
	defer s.network.auditMu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		return []NetworkMutationAuditEvent{}
	}
	defer f.Close()
	var events []NetworkMutationAuditEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var event NetworkMutationAuditEvent
		if json.Unmarshal(scanner.Bytes(), &event) == nil {
			events = append(events, event)
			if len(events) > limit {
				events = events[len(events)-limit:]
			}
		}
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events
}
