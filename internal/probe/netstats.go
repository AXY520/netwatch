package probe

import (
	"bufio"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type NICThroughput struct {
	Name      string `json:"name"`
	Present   bool   `json:"present"`
	OperState string `json:"oper_state"` // "up", "down", "unknown", etc. from /sys/class/net/<name>/operstate
	RxBps     int64  `json:"rx_bps"`
	TxBps     int64  `json:"tx_bps"`
	RxTotal   int64  `json:"rx_total"`
	TxTotal   int64  `json:"tx_total"`
	Timestamp string `json:"timestamp"`
}

func sortedNICNames(items map[string]NICThroughput) []string {
	names := make([]string, 0, len(items))
	for name := range items {
		names = append(names, name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		left := inferLinkType(names[i])
		right := inferLinkType(names[j])
		if left != right {
			return left == "wired"
		}
		return names[i] < names[j]
	})
	return names
}

type RealtimeNetStats struct {
	Timestamp string          `json:"timestamp"`
	NICs      []NICThroughput `json:"nics"`
}

type nicCounters struct {
	rx, tx int64
	at     time.Time
}

type nicStatsTracker struct {
	mu        sync.RWMutex
	last      map[string]nicCounters
	current   map[string]NICThroughput
	active    bool
	interval  time.Duration
	onSampled func(RealtimeNetStats)
}

func newNICStatsTracker() *nicStatsTracker {
	return &nicStatsTracker{
		last:     make(map[string]nicCounters),
		current:  make(map[string]NICThroughput),
		active:   true,
		interval: time.Second,
	}
}

func (t *nicStatsTracker) start(stop <-chan struct{}, done chan<- struct{}) {
	go func() {
		defer close(done)
		t.sample()
		for {
			t.mu.RLock()
			enabled := t.active
			interval := t.interval
			t.mu.RUnlock()
			if interval <= 0 {
				interval = time.Second
			}

			ticker := time.NewTimer(interval)
			select {
			case <-stop:
				if !ticker.Stop() {
					<-ticker.C
				}
				return
			case <-ticker.C:
				if enabled {
					t.sample()
				}
			}
		}
	}()
}

func (t *nicStatsTracker) configure(enabled bool, intervalSec int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.active = enabled
	if intervalSec > 0 {
		t.interval = time.Duration(intervalSec) * time.Second
	}
}

func (t *nicStatsTracker) enabled() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.active
}

func (t *nicStatsTracker) intervalSeconds() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.interval <= 0 {
		return 1
	}
	return int(t.interval / time.Second)
}

func (t *nicStatsTracker) sample() {
	snapshot, err := readProcNetDev()
	if err != nil {
		return
	}
	now := time.Now()
	nics := autoMonitoredNICNames()

	t.mu.Lock()

	next := make(map[string]NICThroughput, len(nics))
	for _, name := range nics {
		cur, ok := snapshot[name]
		out := NICThroughput{
			Name:      name,
			Present:   ok,
			OperState: readOperState(name),
			Timestamp: now.Format(time.DateTime),
		}
		if !ok {
			next[name] = out
			continue
		}
		out.RxTotal = cur.rx
		out.TxTotal = cur.tx
		if prev, had := t.last[name]; had {
			dt := now.Sub(prev.at).Seconds()
			if dt > 0 {
				out.RxBps = int64(float64(cur.rx-prev.rx) / dt)
				out.TxBps = int64(float64(cur.tx-prev.tx) / dt)
				if out.RxBps < 0 {
					out.RxBps = 0
				}
				if out.TxBps < 0 {
					out.TxBps = 0
				}
			}
		}
		t.last[name] = nicCounters{rx: cur.rx, tx: cur.tx, at: now}
		next[name] = out
	}
	t.current = next
	out := RealtimeNetStats{Timestamp: localTimestamp()}
	for _, name := range sortedNICNames(next) {
		out.NICs = append(out.NICs, next[name])
	}
	callback := t.onSampled
	t.mu.Unlock()

	if callback != nil {
		callback(out)
	}
}

func (t *nicStatsTracker) snapshot() RealtimeNetStats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	out := RealtimeNetStats{Timestamp: localTimestamp()}
	for _, name := range sortedNICNames(t.current) {
		out.NICs = append(out.NICs, t.current[name])
	}
	return out
}

func (t *nicStatsTracker) sampleAndSnapshot() RealtimeNetStats {
	t.sample()
	return t.snapshot()
}

// readOperState reads /sys/class/net/<name>/operstate and returns the lowercase value.
// Returns "unknown" if the file doesn't exist or can't be read.
func readOperState(name string) string {
	data, err := os.ReadFile("/sys/class/net/" + name + "/operstate")
	if err != nil {
		return "unknown"
	}
	return strings.ToLower(strings.TrimSpace(string(data)))
}

func readProcNetDev() (map[string]nicCounters, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := make(map[string]nicCounters)
	scanner := bufio.NewScanner(f)
	// 前两行是表头
	for i := 0; i < 2 && scanner.Scan(); i++ {
	}
	for scanner.Scan() {
		line := scanner.Text()
		colon := strings.Index(line, ":")
		if colon <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 16 {
			continue
		}
		rx, err1 := strconv.ParseInt(fields[0], 10, 64)
		tx, err2 := strconv.ParseInt(fields[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out[name] = nicCounters{rx: rx, tx: tx, at: time.Now()}
	}
	return out, scanner.Err()
}
