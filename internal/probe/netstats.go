package probe

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type NICThroughput struct {
	Name      string `json:"name"`
	Present   bool   `json:"present"`
	RxBps     int64  `json:"rx_bps"`
	TxBps     int64  `json:"tx_bps"`
	RxTotal   int64  `json:"rx_total"`
	TxTotal   int64  `json:"tx_total"`
	Timestamp string `json:"timestamp"`
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
	mu       sync.RWMutex
	nics     []string
	last     map[string]nicCounters
	current  map[string]NICThroughput
	active   bool
	interval time.Duration
}

func newNICStatsTracker(nics []string) *nicStatsTracker {
	return &nicStatsTracker{
		nics:     append([]string(nil), nics...),
		last:     make(map[string]nicCounters),
		current:  make(map[string]NICThroughput),
		active:   true,
		interval: time.Second,
	}
}

func (t *nicStatsTracker) start(stop <-chan struct{}) {
	go func() {
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

	t.mu.Lock()
	defer t.mu.Unlock()

	for _, name := range t.nics {
		cur, ok := snapshot[name]
		out := NICThroughput{
			Name:      name,
			Present:   ok,
			Timestamp: now.Format(time.DateTime),
		}
		if !ok {
			t.current[name] = out
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
		t.current[name] = out
	}
}

func (t *nicStatsTracker) snapshot() RealtimeNetStats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	out := RealtimeNetStats{Timestamp: localTimestamp()}
	for _, name := range t.nics {
		if v, ok := t.current[name]; ok {
			out.NICs = append(out.NICs, v)
		} else {
			out.NICs = append(out.NICs, NICThroughput{Name: name, Present: false})
		}
	}
	return out
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
