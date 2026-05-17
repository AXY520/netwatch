package probe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxAppTrafficPoints = 300

// AppTrafficPoint is a single sample of cumulative rx/tx bytes for one bridge.
type AppTrafficPoint struct {
	Timestamp string `json:"timestamp"`
	RxBytes   uint64 `json:"rx_bytes"`
	TxBytes   uint64 `json:"tx_bytes"`
}

type appTrafficHistoryStore struct {
	mu        sync.RWMutex
	history   map[string][]AppTrafficPoint // bridge name → points
	maxPoints int
	path      string
}

func newAppTrafficHistoryStore(dataDir string) *appTrafficHistoryStore {
	path := filepath.Join(dataDir, "app_traffic_history.json")
	store := &appTrafficHistoryStore{
		history:   make(map[string][]AppTrafficPoint),
		maxPoints: maxAppTrafficPoints,
		path:      path,
	}
	if body, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(body, &store.history)
	}
	return store
}

// sample reads all lzc-br-* bridge counters and appends a point for each.
// extraBridges lists additional bridge names to always sample (persistent mode).
func (s *appTrafficHistoryStore) sample(extraBridges []string) {
	entries, err := os.ReadDir(sysClassNetDir)
	now := time.Now()

	s.mu.Lock()
	seen := make(map[string]bool)
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, lzcBridgePrefix) {
				continue
			}
			seen[name] = true
			s.sampleBridge(name, now)
		}
	}
	for _, name := range extraBridges {
		if seen[name] {
			continue
		}
		s.sampleBridge(name, now)
	}
	// Also snapshot for persistence
	fullSnap := make(map[string][]AppTrafficPoint, len(s.history))
	for k, v := range s.history {
		fullSnap[k] = append([]AppTrafficPoint(nil), v...)
	}
	s.mu.Unlock()

	go func() {
		body, _ := json.Marshal(fullSnap)
		if body != nil {
			_ = os.WriteFile(s.path, body, 0o644)
		}
	}()
}

// sampleWithTiming samples bridges whose interval has elapsed since lastSampled.
// globalInterval is the default seconds; perApp overrides for specific bridges.
func (s *appTrafficHistoryStore) sampleWithTiming(extraBridges []string, lastSampled map[string]time.Time, now time.Time, globalInterval int, perApp map[string]int) {
	entries, err := os.ReadDir(sysClassNetDir)

	s.mu.Lock()
	seen := make(map[string]bool)
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, lzcBridgePrefix) {
				continue
			}
			seen[name] = true
			iv := globalInterval
			if v, ok := perApp[name]; ok && v > 0 {
				iv = v
			}
			if last, ok := lastSampled[name]; ok && now.Sub(last) < time.Duration(iv)*time.Second {
				continue
			}
			s.sampleBridge(name, now)
			lastSampled[name] = now
		}
	}
	for _, name := range extraBridges {
		if seen[name] {
			continue
		}
		iv := globalInterval
		if v, ok := perApp[name]; ok && v > 0 {
			iv = v
		}
		if last, ok := lastSampled[name]; ok && now.Sub(last) < time.Duration(iv)*time.Second {
			continue
		}
		s.sampleBridge(name, now)
		lastSampled[name] = now
	}
	fullSnap := make(map[string][]AppTrafficPoint, len(s.history))
	for k, v := range s.history {
		fullSnap[k] = append([]AppTrafficPoint(nil), v...)
	}
	s.mu.Unlock()

	go func() {
		body, _ := json.Marshal(fullSnap)
		if body != nil {
			_ = os.WriteFile(s.path, body, 0o644)
		}
	}()
}

// sampleBridge reads counters for a single bridge and appends a point.
func (s *appTrafficHistoryStore) sampleBridge(name string, now time.Time) {
	rx := readSysCounter(filepath.Join(sysClassNetDir, name, "statistics", "rx_bytes"))
	tx := readSysCounter(filepath.Join(sysClassNetDir, name, "statistics", "tx_bytes"))
	pts := s.history[name]
	pts = append(pts, AppTrafficPoint{Timestamp: now.Format(time.DateTime), RxBytes: rx, TxBytes: tx})
	if len(pts) > s.maxPoints {
		pts = pts[len(pts)-s.maxPoints:]
	}
	s.history[name] = pts
}

// snapshot returns the history for a specific bridge, limited to the last `limit` points.
func (s *appTrafficHistoryStore) snapshot(bridge string, limit int) []AppTrafficPoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pts := s.history[bridge]
	if limit <= 0 || limit >= len(pts) {
		return append([]AppTrafficPoint(nil), pts...)
	}
	return append([]AppTrafficPoint(nil), pts[len(pts)-limit:]...)
}

// snapshotAll returns the latest point for every bridge (for overview).
func (s *appTrafficHistoryStore) snapshotAll() map[string]AppTrafficPoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]AppTrafficPoint, len(s.history))
	for name, pts := range s.history {
		if len(pts) > 0 {
			out[name] = pts[len(pts)-1]
		}
	}
	return out
}
