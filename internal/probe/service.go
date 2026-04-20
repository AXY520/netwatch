package probe

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const maxHistoryItems = 30

type Service struct {
	cfg                  Config
	mu                   sync.RWMutex
	summary              Summary
	lastError            string
	nextRefresh          time.Time
	broadbandHistory     []BroadbandSpeedResult
	localTransferHistory []LocalTransferResult
	broadbandTask        BroadbandTaskStatus
	broadbandTaskCancel  context.CancelFunc
	timeseries           *timeseriesStore
	alert                *alertState
	subs                 []chan Summary
	subsMu               sync.Mutex
	alertWebhookURL      string
	autoRefresh          bool
	nicStats             *nicStatsTracker
	nicStop              chan struct{}
	egressCache          EgressLookupResult
	egressMu             sync.Mutex
	egressInflight       bool
	traceMu              sync.Mutex
	traceTask            TraceResult
	traceCancel          context.CancelFunc
}

func NewService(cfg Config) *Service {
	s := &Service{
		cfg:        cfg,
		timeseries: newTimeseriesStore(cfg.DataDir),
		alert:      newAlertState(),
		nicStats:   newNICStatsTracker(cfg.MonitoredNICs),
		nicStop:    make(chan struct{}),
		autoRefresh: true,
	}
	s.loadHistory()
	if saved, ok := loadMutableSettings(cfg.DataDir); ok {
		s.applyMutableSettings(saved, false)
	}
	s.nicStats.start(s.nicStop)
	return s
}

func (s *Service) Start(baseCtx context.Context) {
	startCtx, cancelStart := context.WithCancel(baseCtx)
	// 首轮探测异步执行，不阻塞 HTTP server 启动
	go s.refreshFast(startCtx)
	go s.refreshNAT(startCtx)

	go func() {
		for {
			s.mu.RLock()
			interval := s.cfg.RefreshInterval
			auto := s.autoRefresh
			s.mu.RUnlock()

			timer := time.NewTimer(interval)
			select {
			case <-startCtx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
				if auto {
					s.refreshFast(startCtx)
				}
			}
		}
	}()

	go func() {
		<-baseCtx.Done()
		cancelStart()
	}()
}

func (s *Service) GetAutoRefresh() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.autoRefresh
}

func (s *Service) SetAutoRefresh(enabled bool) bool {
	s.mu.Lock()
	s.autoRefresh = enabled
	s.mu.Unlock()
	return enabled
}

func (s *Service) Refresh(ctx context.Context) Summary {
	s.refreshFast(ctx)
	return s.GetSummary()
}

func (s *Service) RefreshNAT(ctx context.Context) NATInfo {
	s.refreshNAT(ctx)
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.summary.NetworkInfo.NAT
}

func (s *Service) RefreshWebsiteConnectivity(ctx context.Context) WebsiteConnectivity {
	s.mu.RLock()
	timeout := s.cfg.HTTPTimeout
	s.mu.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, timeout*2)
	defer cancel()

	website := s.ProbeWebsiteConnectivity(ctx)

	s.mu.Lock()
	s.summary.WebsiteConnectivity = website
	s.summary.GeneratedAt = localTimestamp()
	s.mu.Unlock()

	return website
}

func (s *Service) RunBroadbandSpeedTest(ctx context.Context) BroadbandSpeedResult {
	s.mu.RLock()
	duration := s.cfg.BroadbandDuration
	s.mu.RUnlock()

	result, completed := executeBroadbandSpeedTest(ctx, duration, nil)
	if completed {
		s.pushBroadbandHistory(result)
	}
	return result
}

func (s *Service) StartBroadbandTask() BroadbandTaskStatus {
	s.mu.Lock()
	if s.broadbandTask.Running {
		task := s.broadbandTask
		s.mu.Unlock()
		return task
	}

	duration := s.cfg.BroadbandDuration
	ctx, cancel := context.WithCancel(context.Background())
	task := BroadbandTaskStatus{
		ID:              fmt.Sprintf("broadband-%d", time.Now().UnixNano()),
		Stage:           "starting",
		ProgressPercent: 0,
		Running:         true,
		Message:         "准备开始宽带测速",
		UpdatedAt:       localTimestamp(),
		Result: BroadbandSpeedResult{
			Timestamp:    localTimestamp(),
			Provider:     "Speedtest China",
			ServerRegion: "中国测速节点",
		},
	}
	s.broadbandTask = task
	s.broadbandTaskCancel = cancel
	s.mu.Unlock()

	go s.runBroadbandTask(ctx, duration)
	return task
}

func (s *Service) GetBroadbandTask() BroadbandTaskStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.broadbandTask
}

func (s *Service) CancelBroadbandTask() BroadbandTaskStatus {
	s.mu.Lock()
	cancel := s.broadbandTaskCancel
	if s.broadbandTask.Running {
		s.broadbandTask.Message = "正在取消测速"
		s.broadbandTask.UpdatedAt = localTimestamp()
	}
	task := s.broadbandTask
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return task
}

func (s *Service) runBroadbandTask(ctx context.Context, duration time.Duration) {
	result, completed := executeBroadbandSpeedTest(ctx, duration, func(stage string, progress int, message string, partial BroadbandSpeedResult) {
		s.mu.Lock()
		s.broadbandTask.Stage = stage
		s.broadbandTask.ProgressPercent = progress
		s.broadbandTask.Message = message
		s.broadbandTask.Result = partial
		s.broadbandTask.UpdatedAt = localTimestamp()
		s.mu.Unlock()
	})

	s.mu.Lock()
	defer s.mu.Unlock()

	s.broadbandTask.Result = result
	s.broadbandTask.UpdatedAt = localTimestamp()
	s.broadbandTask.Running = false
	s.broadbandTaskCancel = nil

	switch {
	case ctx.Err() != nil:
		s.broadbandTask.Stage = "canceled"
		s.broadbandTask.Canceled = true
		s.broadbandTask.Finished = false
		s.broadbandTask.Message = "宽带测速已取消"
	case !completed:
		s.broadbandTask.Stage = "error"
		s.broadbandTask.Finished = false
		if result.Error == "" {
			result.Error = "测速未完成"
		}
		s.broadbandTask.Result = result
		s.broadbandTask.Message = result.Error
	default:
		s.broadbandTask.Stage = "complete"
		s.broadbandTask.ProgressPercent = 100
		s.broadbandTask.Finished = true
		s.broadbandTask.Canceled = false
		s.broadbandTask.Message = "宽带测速完成"
		go s.pushBroadbandHistory(result)
	}
}

func (s *Service) RecordLocalTransferResult(result LocalTransferResult) LocalTransferResult {
	if result.Timestamp == "" {
		result.Timestamp = localTimestamp()
	}
	s.pushLocalTransferHistory(result)
	return result
}

func (s *Service) GetBroadbandHistory() []BroadbandSpeedResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]BroadbandSpeedResult(nil), s.broadbandHistory...)
}

func (s *Service) GetLocalTransferHistory() []LocalTransferResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]LocalTransferResult(nil), s.localTransferHistory...)
}

func (s *Service) GetSpeedConfig() SpeedConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return SpeedConfig{
		BroadbandDurationSec:     int64(s.cfg.BroadbandDuration / time.Second),
		LocalTransferDurationSec: int64(s.cfg.LocalTransferDuration / time.Second),
		LocalTransferPayloadMB:   s.cfg.LocalTransferPayloadMB,
	}
}

func (s *Service) UpdateRefreshInterval(seconds int) Summary {
	if seconds > 0 {
		s.mu.Lock()
		s.cfg.RefreshInterval = time.Duration(seconds) * time.Second
		s.nextRefresh = time.Now().Add(s.cfg.RefreshInterval)
		if s.summary.GeneratedAt != "" {
			s.summary.RefreshIntervalSec = int64(s.cfg.RefreshInterval / time.Second)
			s.summary.NextRefreshAt = s.nextRefresh.Format(time.DateTime)
		}
		s.mu.Unlock()
	}
	return s.GetSummary()
}

func (s *Service) GetSummary() Summary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.summary
}

func (s *Service) Subscribe() (<-chan Summary, func()) {
	ch := make(chan Summary, 4)
	s.subsMu.Lock()
	s.subs = append(s.subs, ch)
	s.subsMu.Unlock()

	unsub := func() {
		s.subsMu.Lock()
		defer s.subsMu.Unlock()
		for i, c := range s.subs {
			if c == ch {
				s.subs = append(s.subs[:i], s.subs[i+1:]...)
				close(ch)
				return
			}
		}
	}
	return ch, unsub
}

func (s *Service) broadcast(summary Summary) {
	s.subsMu.Lock()
	subs := append([]chan Summary(nil), s.subs...)
	s.subsMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- summary:
		default:
		}
	}
}

func (s *Service) GetTimeseries(limit int) []TimeseriesPoint {
	return s.timeseries.snapshot(limit)
}

func (s *Service) GetMutableSettings() MutableSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return MutableSettings{
		RefreshIntervalSec:     int(s.cfg.RefreshInterval / time.Second),
		AutoRefreshEnabled:     s.autoRefresh,
		NICRealtimeEnabled:     s.nicStats.enabled(),
		NICRealtimeIntervalSec: s.nicStats.intervalSeconds(),
		DomesticSites:          append([]SiteTarget(nil), s.cfg.DomesticSites...),
		GlobalSites:            append([]SiteTarget(nil), s.cfg.GlobalSites...),
		AlertWebhookURL:        s.alertWebhookURL,
	}
}

func (s *Service) UpdateMutableSettings(in MutableSettings) MutableSettings {
	s.applyMutableSettings(in, true)
	return s.GetMutableSettings()
}

func (s *Service) applyMutableSettings(in MutableSettings, persist bool) {
	s.mu.Lock()
	if in.RefreshIntervalSec > 0 {
		s.cfg.RefreshInterval = time.Duration(in.RefreshIntervalSec) * time.Second
	}
	s.autoRefresh = in.AutoRefreshEnabled
	if len(in.DomesticSites) > 0 {
		s.cfg.DomesticSites = in.DomesticSites
	}
	if len(in.GlobalSites) > 0 {
		s.cfg.GlobalSites = in.GlobalSites
	}
	s.alertWebhookURL = in.AlertWebhookURL
	dataDir := s.cfg.DataDir
	s.mu.Unlock()

	s.nicStats.configure(in.NICRealtimeEnabled, in.NICRealtimeIntervalSec)
	s.alert.setWebhook(in.AlertWebhookURL)
	if persist {
		_ = saveMutableSettings(dataDir, s.GetMutableSettings())
	}
}

func (s *Service) GetRealtimeNetStats() RealtimeNetStats {
	return s.nicStats.snapshot()
}

// GetEgressLookups 返回缓存的多源查询结果，若没缓存则同步触发一次（冷启动场景）。
func (s *Service) GetEgressLookups(ctx context.Context) EgressLookupResult {
	s.egressMu.Lock()
	cache := s.egressCache
	empty := cache.GeneratedAt == ""
	s.egressMu.Unlock()
	if !empty {
		return cache
	}
	return s.RefreshEgressLookups(ctx)
}

// RefreshEgressLookups 强制立刻刷新并更新缓存。
func (s *Service) RefreshEgressLookups(ctx context.Context) EgressLookupResult {
	s.egressMu.Lock()
	if s.egressInflight {
		cache := s.egressCache
		s.egressMu.Unlock()
		return cache
	}
	s.egressInflight = true
	cfg := s.cfg
	s.egressMu.Unlock()

	result := LookupEgressIPs(ctx)
	result.DomesticIP = lookupDomesticIPs(ctx, cfg)

	s.egressMu.Lock()
	s.egressCache = result
	s.egressInflight = false
	s.egressMu.Unlock()
	return result
}

func (s *Service) StartTraceTask(host string, maxHops int) TraceResult {
	s.traceMu.Lock()
	if s.traceCancel != nil {
		s.traceCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	task := TraceResult{
		Target:    host,
		Timestamp: localTimestamp(),
		Tool:      "mtr",
		Running:   true,
	}
	s.traceTask = task
	s.traceCancel = cancel
	s.traceMu.Unlock()

	go func() {
		result := RunTrace(ctx, host, maxHops, func(update TraceResult) {
			s.traceMu.Lock()
			s.traceTask = update
			s.traceMu.Unlock()
		})
		s.traceMu.Lock()
		result.Running = false
		result.Finished = true
		s.traceTask = result
		s.traceCancel = nil
		s.traceMu.Unlock()
	}()

	return task
}

func (s *Service) GetTraceTask() TraceResult {
	s.traceMu.Lock()
	defer s.traceMu.Unlock()
	return s.traceTask
}

func (s *Service) refreshFast(ctx context.Context) {
	s.mu.RLock()
	interval := s.cfg.RefreshInterval
	currentNAT := s.summary.NetworkInfo.NAT
	s.mu.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, interval)
	defer cancel()

	summary, err := s.collectFastSummary(ctx)

	s.mu.Lock()

	if err != nil {
		s.lastError = err.Error()
		if s.summary.GeneratedAt == "" {
			s.summary = emptySummary(interval, s.lastError)
		}
		s.summary.LastError = s.lastError
		s.summary.NextRefreshAt = time.Now().Add(interval).Format(time.DateTime)
		s.mu.Unlock()
		return
	}

	summary.NetworkInfo.NAT = currentNAT
	s.lastError = ""
	s.nextRefresh = time.Now().Add(interval)
	s.summary = summary
	s.summary.Ready = true
	s.summary.LastError = ""
	s.summary.RefreshIntervalSec = int64(interval / time.Second)
	s.summary.NextRefreshAt = s.nextRefresh.Format(time.DateTime)
	finalSummary := s.summary
	s.mu.Unlock()

	s.recordTimeseries(finalSummary)
	s.alert.check(finalSummary)
	s.broadcast(finalSummary)
}

func (s *Service) recordTimeseries(summary Summary) {
	point := TimeseriesPoint{
		Timestamp:      summary.GeneratedAt,
		UnixMS:         time.Now().UnixMilli(),
		DomesticStatus: summary.WebsiteConnectivity.DomesticStatus,
		GlobalStatus:   summary.WebsiteConnectivity.GlobalStatus,
		TargetLatency:  map[string]int64{},
		TargetLoss:     map[string]float64{},
		EgressIPv4:     summary.NetworkInfo.EgressIPv4,
		EgressIPv6:     summary.NetworkInfo.EgressIPv6,
		NATType:        summary.NetworkInfo.NAT.Type,
	}
	for _, t := range summary.WebsiteConnectivity.Domestic {
		point.TargetLatency[t.Name] = t.LatencyMS
		point.TargetLoss[t.Name] = t.PacketLossPct
	}
	for _, t := range summary.WebsiteConnectivity.Global {
		point.TargetLatency[t.Name] = t.LatencyMS
		point.TargetLoss[t.Name] = t.PacketLossPct
	}
	s.timeseries.append(point)
}

func (s *Service) refreshNAT(ctx context.Context) {
	s.mu.RLock()
	timeout := s.cfg.NATTimeout
	s.mu.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, timeout*2)
	defer cancel()

	nat := s.ProbeNAT(ctx)
	s.mu.Lock()
	s.summary.NetworkInfo.NAT = nat
	snap := s.summary
	s.mu.Unlock()
	s.alert.check(snap)
	s.broadcast(snap)
}

func (s *Service) collectFastSummary(ctx context.Context) (Summary, error) {
	var website WebsiteConnectivity
	var networkInfo NetworkInfo

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		website = s.ProbeWebsiteConnectivity(ctx)
	}()

	go func() {
		defer wg.Done()
		networkInfo = s.ProbeNetworkInfo(ctx)
		networkInfo.NAT = NATInfo{}
	}()

	wg.Wait()

	return Summary{
		GeneratedAt:         localTimestamp(),
		RefreshIntervalSec:  int64(s.cfg.RefreshInterval / time.Second),
		Ready:               true,
		WebsiteConnectivity: website,
		NetworkInfo:         networkInfo,
	}, nil
}

func emptySummary(interval time.Duration, lastError string) Summary {
	return Summary{
		GeneratedAt:        localTimestamp(),
		RefreshIntervalSec: int64(interval / time.Second),
		Ready:              false,
		LastError:          lastError,
	}
}

func summarizeStatus(results []TargetResult) ProbeStatus {
	if len(results) == 0 {
		return StatusUnknown
	}

	hasOK := false
	hasDegraded := false
	for _, result := range results {
		switch result.Status {
		case StatusOK:
			hasOK = true
		case StatusDegraded:
			hasDegraded = true
		}
	}

	switch {
	case hasOK:
		return StatusOK
	case hasDegraded:
		return StatusDegraded
	default:
		return StatusDown
	}
}

func localTimestamp() string {
	return time.Now().Format(time.DateTime)
}

var speedClient = &http.Client{
	Timeout: 60 * time.Second,
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        50,
		MaxIdleConnsPerHost:  20,
		DisableCompression: true,
		IdleConnTimeout:    30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "speed.cloudflare.com"},
	},
}

func measureLatencyAndJitterProgress(ctx context.Context, count int, progress func(done, total int, latency, jitter int64)) (int64, int64) {
	var samples []int64
	for i := 0; i < count; i++ {
		if ctx.Err() != nil {
			break
		}

		start := time.Now()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://speed.cloudflare.com/__down?bytes=4096", nil)
		if err != nil {
			continue
		}
		resp, err := speedClient.Do(req)
		if err != nil {
			continue
		}
		if resp.Body != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
		}

		samples = append(samples, time.Since(start).Milliseconds())
		if progress != nil {
			progress(len(samples), count, averageInt64(samples), calculateJitter(samples))
		}
	}
	if len(samples) == 0 {
		return 0, 0
	}
	return averageInt64(samples), calculateJitter(samples)
}

func sustainedDownloadMbpsProgress(ctx context.Context, duration time.Duration, workers int, progress func(mbps float64, elapsed, total time.Duration)) float64 {
	if workers <= 0 {
		workers = 1
	}
	if duration <= 0 {
		duration = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	var totalBytes int64
	var wg sync.WaitGroup
	done := make(chan struct{})
	startedAt := time.Now()
	sampler := newThroughputSampler(startedAt)

	go reportThroughputProgress(done, &totalBytes, startedAt, duration, sampler, progress)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://speed.cloudflare.com/__down?bytes=20000000", nil)
				if err != nil {
					return
				}
				resp, err := speedClient.Do(req)
				if err != nil {
					return
				}
				if resp.Body != nil {
					n, _ := io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					if n > 0 {
						atomic.AddInt64(&totalBytes, n)
					}
				}
			}
		}()
	}

	wg.Wait()
	close(done)

	finalBytes := atomic.LoadInt64(&totalBytes)
	sampler.observe(finalBytes, time.Now())
	stable := sampler.stableMbps()
	seconds := time.Since(startedAt).Seconds()
	if stable > 0 {
		return stable
	}
	if seconds <= 0 || finalBytes <= 0 {
		return 0
	}
	return float64(finalBytes*8) / seconds / 1_000_000
}

func sustainedUploadMbpsProgress(ctx context.Context, duration time.Duration, workers int, payloadBytes int, progress func(mbps float64, elapsed, total time.Duration)) float64 {
	if workers <= 0 {
		workers = 1
	}
	if duration <= 0 {
		duration = 10 * time.Second
	}
	if payloadBytes <= 0 {
		payloadBytes = 4 * 1024 * 1024
	}

	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	payload := bytes.Repeat([]byte("u"), payloadBytes)
	var totalBytes int64
	var wg sync.WaitGroup
	done := make(chan struct{})
	startedAt := time.Now()
	sampler := newThroughputSampler(startedAt)

	go reportThroughputProgress(done, &totalBytes, startedAt, duration, sampler, progress)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://speed.cloudflare.com/__up", bytes.NewReader(payload))
				if err != nil {
					return
				}
				req.Header.Set("Content-Type", "application/octet-stream")
				resp, err := speedClient.Do(req)
				if err != nil {
					return
				}
				if resp.Body != nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
				atomic.AddInt64(&totalBytes, int64(payloadBytes))
			}
		}()
	}

	wg.Wait()
	close(done)

	finalBytes := atomic.LoadInt64(&totalBytes)
	sampler.observe(finalBytes, time.Now())
	stable := sampler.stableMbps()
	seconds := time.Since(startedAt).Seconds()
	if stable > 0 {
		return stable
	}
	if seconds <= 0 || finalBytes <= 0 {
		return 0
	}
	return float64(finalBytes*8) / seconds / 1_000_000
}

func reportThroughputProgress(done <-chan struct{}, totalBytes *int64, startedAt time.Time, duration time.Duration, sampler *throughputSampler, progress func(mbps float64, elapsed, total time.Duration)) {
	if progress == nil {
		return
	}

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			elapsed := time.Since(startedAt)
			bytes := atomic.LoadInt64(totalBytes)
			current := sampler.observe(bytes, time.Now())
			progress(current, elapsed, duration)
			return
		case <-ticker.C:
			elapsed := time.Since(startedAt)
			bytes := atomic.LoadInt64(totalBytes)
			current := sampler.observe(bytes, time.Now())
			progress(current, elapsed, duration)
		}
	}
}

func computeMbps(totalBytes int64, elapsed time.Duration) float64 {
	if elapsed <= 0 || totalBytes <= 0 {
		return 0
	}
	return float64(totalBytes*8) / elapsed.Seconds() / 1_000_000
}

type throughputSampler struct {
	startedAt time.Time
	lastAt    time.Time
	lastBytes int64
	samples   []float64
}

func newThroughputSampler(startedAt time.Time) *throughputSampler {
	return &throughputSampler{
		startedAt: startedAt,
		lastAt:    startedAt,
	}
}

func (s *throughputSampler) observe(totalBytes int64, now time.Time) float64 {
	if now.Before(s.lastAt) {
		now = s.lastAt
	}

	elapsed := now.Sub(s.lastAt)
	if elapsed > 0 {
		deltaBytes := totalBytes - s.lastBytes
		if deltaBytes < 0 {
			deltaBytes = 0
		}
		instMbps := computeMbps(deltaBytes, elapsed)
		if now.Sub(s.startedAt) >= 1200*time.Millisecond && instMbps > 0 {
			s.samples = append(s.samples, instMbps)
			if len(s.samples) > 80 {
				s.samples = s.samples[len(s.samples)-80:]
			}
		}
	}

	s.lastBytes = totalBytes
	s.lastAt = now
	return s.displayMbps(totalBytes, now)
}

func (s *throughputSampler) displayMbps(totalBytes int64, now time.Time) float64 {
	window := 6
	if len(s.samples) >= window {
		return averageFloat64(s.samples[len(s.samples)-window:])
	}
	return computeMbps(totalBytes, now.Sub(s.startedAt))
}

func (s *throughputSampler) stableMbps() float64 {
	if len(s.samples) == 0 {
		return 0
	}

	samples := append([]float64(nil), s.samples...)
	sortFloat64(samples)

	cut := int(math.Floor(float64(len(samples)) * 0.1))
	if cut*2 >= len(samples) {
		return averageFloat64(samples)
	}
	return averageFloat64(samples[cut : len(samples)-cut])
}

func averageFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var total float64
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func sortFloat64(values []float64) {
	sort.Float64s(values)
}

func calculateJitter(samples []int64) int64 {
	if len(samples) < 2 {
		return 0
	}
	var diffs []float64
	for i := 1; i < len(samples); i++ {
		diffs = append(diffs, math.Abs(float64(samples[i]-samples[i-1])))
	}
	var sum float64
	for _, diff := range diffs {
		sum += diff
	}
	return int64(math.Round(sum / float64(len(diffs))))
}

func averageInt64(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	var total int64
	for _, value := range values {
		total += value
	}
	return total / int64(len(values))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampProgress(progress int) int {
	switch {
	case progress < 0:
		return 0
	case progress > 100:
		return 100
	default:
		return progress
	}
}

func progressRange(elapsed, total time.Duration, width int) int {
	if total <= 0 || width <= 0 {
		return 0
	}
	ratio := float64(elapsed) / float64(total)
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	return int(math.Round(ratio * float64(width)))
}

func (s *Service) pushBroadbandHistory(result BroadbandSpeedResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadbandHistory = append([]BroadbandSpeedResult{result}, s.broadbandHistory...)
	if len(s.broadbandHistory) > maxHistoryItems {
		s.broadbandHistory = s.broadbandHistory[:maxHistoryItems]
	}
	s.saveBroadbandHistory()
}

func (s *Service) pushLocalTransferHistory(result LocalTransferResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localTransferHistory = append([]LocalTransferResult{result}, s.localTransferHistory...)
	if len(s.localTransferHistory) > maxHistoryItems {
		s.localTransferHistory = s.localTransferHistory[:maxHistoryItems]
	}
	s.saveLocalTransferHistory()
}
