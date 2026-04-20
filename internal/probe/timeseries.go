package probe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const maxTimeseriesPoints = 2000

type timeseriesStore struct {
	mu     sync.RWMutex
	points []TimeseriesPoint
	path   string
}

func newTimeseriesStore(dataDir string) *timeseriesStore {
	path := filepath.Join(dataDir, "timeseries.json")
	store := &timeseriesStore{path: path}
	if body, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(body, &store.points)
	}
	return store
}

func (t *timeseriesStore) append(point TimeseriesPoint) {
	t.mu.Lock()
	t.points = append(t.points, point)
	if len(t.points) > maxTimeseriesPoints {
		t.points = t.points[len(t.points)-maxTimeseriesPoints:]
	}
	snapshot := append([]TimeseriesPoint(nil), t.points...)
	t.mu.Unlock()

	go func() {
		body, err := json.Marshal(snapshot)
		if err != nil {
			return
		}
		_ = os.WriteFile(t.path, body, 0o644)
	}()
}

func (t *timeseriesStore) snapshot(limit int) []TimeseriesPoint {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if limit <= 0 || limit >= len(t.points) {
		return append([]TimeseriesPoint(nil), t.points...)
	}
	return append([]TimeseriesPoint(nil), t.points[len(t.points)-limit:]...)
}
