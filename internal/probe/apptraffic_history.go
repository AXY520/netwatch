package probe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxAppTrafficPoints = 1440
const appTrafficHistoryWriteMinInterval = 10 * time.Second

// AppTrafficPoint stores raw host-bridge counters. For lzc app bridges, RX maps
// to application upload and TX maps to application download.
type AppTrafficPoint struct {
	Timestamp string `json:"timestamp"`
	RxBytes   uint64 `json:"rx_bytes"`
	TxBytes   uint64 `json:"tx_bytes"`
}

type AppTrafficTopItem struct {
	Bridge     string  `json:"bridge"`
	RxDelta    uint64  `json:"rx_delta"`
	TxDelta    uint64  `json:"tx_delta"`
	TotalDelta uint64  `json:"total_delta"`
	PeakBps    float64 `json:"peak_bps"`
}

type appTrafficHistoryStore struct {
	mu        sync.RWMutex
	history   map[string][]AppTrafficPoint // bridge name → points
	maxPoints int
	path      string
	lastWrite time.Time
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
	fullSnap := s.persistSnapshotLocked(now, true)
	s.mu.Unlock()

	if fullSnap != nil {
		_ = writeJSONFile(s.path, fullSnap, false)
	}
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
	fullSnap := s.persistSnapshotLocked(now, true)
	s.mu.Unlock()

	if fullSnap != nil {
		_ = writeJSONFile(s.path, fullSnap, false)
	}
}

func (s *appTrafficHistoryStore) sampleOne(name string, now time.Time) bool {
	if !strings.HasPrefix(name, lzcBridgePrefix) {
		return false
	}
	if _, err := os.Stat(filepath.Join(sysClassNetDir, name, "statistics")); err != nil {
		return false
	}

	s.mu.Lock()
	s.sampleBridge(name, now)
	fullSnap := s.persistSnapshotLocked(now, false)
	s.mu.Unlock()

	if fullSnap != nil {
		_ = writeJSONFile(s.path, fullSnap, false)
	}
	return true
}

func (s *appTrafficHistoryStore) persistSnapshotLocked(now time.Time, force bool) map[string][]AppTrafficPoint {
	if !force && !s.lastWrite.IsZero() && now.Sub(s.lastWrite) < appTrafficHistoryWriteMinInterval {
		return nil
	}
	fullSnap := make(map[string][]AppTrafficPoint, len(s.history))
	for k, v := range s.history {
		fullSnap[k] = append([]AppTrafficPoint(nil), v...)
	}
	s.lastWrite = now
	return fullSnap
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

// snapshotSince returns history points newer than since. It includes the
// previous point when available so rate calculations can cover the first span.
func (s *appTrafficHistoryStore) snapshotSince(bridge string, since time.Time, limit int) []AppTrafficPoint {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pts := s.history[bridge]
	if since.IsZero() {
		return limitPoints(pts, limit)
	}

	start := -1
	for i, p := range pts {
		ts, err := time.ParseInLocation(time.DateTime, p.Timestamp, time.Local)
		if err != nil {
			continue
		}
		if !ts.Before(since) {
			start = i
			if start > 0 {
				start--
			}
			break
		}
	}
	if start < 0 {
		return nil
	}
	return limitPoints(pts[start:], limit)
}

func limitPoints(pts []AppTrafficPoint, limit int) []AppTrafficPoint {
	if limit <= 0 || limit >= len(pts) {
		return append([]AppTrafficPoint(nil), pts...)
	}
	return append([]AppTrafficPoint(nil), pts[len(pts)-limit:]...)
}

func (s *appTrafficHistoryStore) topSince(since time.Time, limit int) []AppTrafficTopItem {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]AppTrafficTopItem, 0, len(s.history))
	for bridge, pts := range s.history {
		scoped := scopePointsSince(pts, since)
		if len(scoped) < 2 {
			continue
		}
		var rxDelta, txDelta uint64
		var peak float64
		for i := 1; i < len(scoped); i++ {
			prev := scoped[i-1]
			curr := scoped[i]
			if curr.RxBytes < prev.RxBytes || curr.TxBytes < prev.TxBytes {
				continue
			}
			rx := curr.RxBytes - prev.RxBytes
			tx := curr.TxBytes - prev.TxBytes
			rxDelta += rx
			txDelta += tx
			t1, err1 := time.ParseInLocation(time.DateTime, prev.Timestamp, time.Local)
			t2, err2 := time.ParseInLocation(time.DateTime, curr.Timestamp, time.Local)
			if err1 == nil && err2 == nil {
				seconds := t2.Sub(t1).Seconds()
				if seconds > 0 {
					if bps := float64(rx+tx) / seconds; bps > peak {
						peak = bps
					}
				}
			}
		}
		total := rxDelta + txDelta
		if total == 0 && peak == 0 {
			continue
		}
		out = append(out, AppTrafficTopItem{
			Bridge:     bridge,
			RxDelta:    rxDelta,
			TxDelta:    txDelta,
			TotalDelta: total,
			PeakBps:    peak,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].TotalDelta > out[j].TotalDelta
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func scopePointsSince(pts []AppTrafficPoint, since time.Time) []AppTrafficPoint {
	if since.IsZero() {
		return pts
	}
	start := -1
	for i, p := range pts {
		ts, err := time.ParseInLocation(time.DateTime, p.Timestamp, time.Local)
		if err != nil {
			continue
		}
		if !ts.Before(since) {
			start = i
			if start > 0 {
				start--
			}
			break
		}
	}
	if start < 0 {
		return nil
	}
	return pts[start:]
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
