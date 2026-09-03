package probe

import (
	"context"
	"os"
	"sync"
	"time"

	"netwatch/internal/lzcsdk"
)

// RefreshNetworkInfo re-collects NIC detail (interfaces/routes) without website
// probes and without re-querying public egress IP. Used after host bridge
// create/dissolve so the NIC detail card updates immediately — must stay fast
// while the host route is still settling (public IP lookups can hang).
func (s *Service) RefreshNetworkInfo(ctx context.Context) NetworkInfo {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	// Local-only collection: interfaces + default routes + SDK status.
	// Do NOT call getPublicIPWithCache here.
	hostname, _ := os.Hostname()
	hostname = sanitizeHostname(hostname)

	var (
		sdkStatus lzcsdk.NetStatus
		sdkOK     bool
	)
	if lzcsdk.Available() {
		if ns, err := lzcsdk.FetchNetworkStatus(ctx); err == nil {
			sdkStatus = ns
			sdkOK = true
		}
	}
	interfaces := collectInterfaces(sdkStatus, sdkOK)

	s.mu.Lock()
	prev := s.summary.NetworkInfo
	info := NetworkInfo{
		GeneratedAt: localTimestamp(),
		Hostname:    hostname,
		Interfaces:  interfaces,
		DefaultIPv4: readDefaultIPv4Route(),
		DefaultIPv6: readDefaultIPv6Route(),
		// Keep last known egress snapshot; full probe/run refreshes these later.
		EgressIPv4:           prev.EgressIPv4,
		EgressIPv6:           prev.EgressIPv6,
		EgressIPv4Region:     prev.EgressIPv4Region,
		EgressIPv6Region:     prev.EgressIPv6Region,
		NAT:                  prev.NAT,
		DetectionNotes:       prev.DetectionNotes,
		PlatformConnectivity: prev.PlatformConnectivity,
		HasInternet:          prev.HasInternet,
		WifiSSID:             prev.WifiSSID,
		WifiSignal:           prev.WifiSignal,
	}
	if sdkOK {
		info.PlatformConnectivity = sdkStatus.Connectivity
		info.HasInternet = sdkStatus.HasInternet
		info.WifiSSID = sdkStatus.Wifi.SSID
		info.WifiSignal = sdkStatus.Wifi.Signal
	}
	s.summary.NetworkInfo = info
	s.summary.GeneratedAt = info.GeneratedAt
	snap := s.summary
	s.mu.Unlock()
	s.broadcast(snap)
	return info
}

func (s *Service) Refresh(ctx context.Context) Summary {
	// Routine page refresh updates website and local interface state only.
	// Public identity is handled by initial load and the explicit egress button.
	s.refreshFast(ctx, false, manualObservationRefreshCooldown)
	return s.GetSummary()
}

func (s *Service) RefreshNAT(ctx context.Context) NATInfo {
	s.refreshNAT(ctx, manualObservationRefreshCooldown)
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.summary.NetworkInfo.NAT
}

func (s *Service) RefreshWebsiteConnectivity(ctx context.Context) WebsiteConnectivity {
	timeout, domesticSites, globalSites := s.websiteProbeConfigSnapshot()
	// Each lane checks its sites sequentially. Keep the outer request alive for
	// the full longest-lane budget so a slow first site cannot cancel the next.
	budget := websiteProbeBudget(timeout, len(domesticSites), len(globalSites)) + websiteProbeCallerGrace
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	website := s.probeWebsiteConnectivity(ctx, manualObservationRefreshCooldown)

	s.mu.Lock()
	s.summary.WebsiteConnectivity = website
	s.summary.GeneratedAt = localTimestamp()
	s.mu.Unlock()

	return website
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

func (s *Service) SubscribeNICRealtime() (<-chan RealtimeNetStats, func()) {
	ch := make(chan RealtimeNetStats, 2)
	s.nicSubsMu.Lock()
	s.nicSubs = append(s.nicSubs, ch)
	s.nicSubsMu.Unlock()
	return ch, func() {
		s.nicSubsMu.Lock()
		defer s.nicSubsMu.Unlock()
		for i, c := range s.nicSubs {
			if c == ch {
				s.nicSubs = append(s.nicSubs[:i], s.nicSubs[i+1:]...)
				close(ch)
				return
			}
		}
	}
}

func (s *Service) broadcastNICRealtime(snap RealtimeNetStats) {
	s.nicSubsMu.Lock()
	subs := append([]chan RealtimeNetStats(nil), s.nicSubs...)
	s.nicSubsMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- snap:
		default:
		}
	}
}

func (s *Service) SubscribeLANDevices() (<-chan LANDeviceSnapshot, func()) {
	ch := make(chan LANDeviceSnapshot, 2)
	s.lanDeviceSubsMu.Lock()
	s.lanDeviceSubs = append(s.lanDeviceSubs, ch)
	s.lanDeviceSubsMu.Unlock()
	return ch, func() {
		s.lanDeviceSubsMu.Lock()
		defer s.lanDeviceSubsMu.Unlock()
		for i, c := range s.lanDeviceSubs {
			if c == ch {
				s.lanDeviceSubs = append(s.lanDeviceSubs[:i], s.lanDeviceSubs[i+1:]...)
				close(ch)
				return
			}
		}
	}
}

func (s *Service) broadcastLANDevices(devices LANDeviceSnapshot) {
	s.lanDeviceSubsMu.Lock()
	subs := append([]chan LANDeviceSnapshot(nil), s.lanDeviceSubs...)
	s.lanDeviceSubsMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- devices:
		default:
		}
	}
}

func (s *Service) GetTimeseries(limit int) []TimeseriesPoint {
	return s.timeseries.snapshot(limit)
}

func (s *Service) GetRealtimeNetStats() RealtimeNetStats {
	// GET is a snapshot read. The lifecycle sampler owns automatic collection;
	// only the explicit refresh endpoint may force new counter samples.
	return s.nicStats.snapshot()
}

// ForceGetRealtimeNetStats always double-samples so manual refresh gets fresh bps.
func (s *Service) ForceGetRealtimeNetStats() RealtimeNetStats {
	return s.nicStats.forceSampleAndSnapshot()
}

const (
	egressLookupCacheTTL        = 2 * time.Minute
	egressLookupRefreshCooldown = 15 * time.Second
)

// GetEgressLookups is the page-load path. It never starts a public probe.
// Startup owns the initial collection and explicit refreshes use
// RefreshEgressLookups. If startup is still collecting, wait for that same
// in-flight result rather than issuing a duplicate request batch.
func (s *Service) GetEgressLookups(ctx context.Context) EgressLookupResult {
	s.egressMu.Lock()
	cache := s.egressCache
	inflight := s.egressInflight
	s.egressMu.Unlock()
	if cache.GeneratedAt != "" {
		return cache
	}
	if inflight {
		return s.waitForEgressLookup(ctx)
	}
	return cache
}

// ClearPublicIPCache 清除公网 IP 缓存，下次 ProbeNetworkInfo 会重新查询。
func (s *Service) ClearPublicIPCache() {
	s.publicIPMu.Lock()
	s.publicIPCache = publicIPCacheData{}
	s.publicIPMu.Unlock()
}

// RefreshEgressLookups 请求刷新并更新缓存。连续手动触发会命中短时冷却，
// 防止重复点击在短时间内产生多批公网请求。
func (s *Service) RefreshEgressLookups(ctx context.Context) EgressLookupResult {
	return s.refreshEgressLookups(ctx, true)
}

func (s *Service) refreshEgressLookups(ctx context.Context, force bool) EgressLookupResult {
	s.egressMu.Lock()
	cache := s.egressCache
	now := time.Now()
	maxAge := egressLookupCacheTTL
	if force {
		maxAge = egressLookupRefreshCooldown
	}
	if cache.GeneratedAt != "" && egressCacheWithin(s.egressUpdatedAt, now, maxAge) {
		s.egressMu.Unlock()
		return cache
	}
	if s.egressInflight {
		empty := cache.GeneratedAt == ""
		s.egressMu.Unlock()
		if empty {
			return s.waitForEgressLookup(ctx)
		}
		return cache
	}
	s.egressInflight = true
	cfg := s.cfg
	s.egressMu.Unlock()
	return s.collectEgressLookups(ctx, cfg)
}

// reserveInitialEgressLookup marks the startup probe as in flight before the
// HTTP server can serve a page-load read. The actual public work can then run
// asynchronously without a cold GET racing it and starting a second batch.
func (s *Service) reserveInitialEgressLookup() (Config, bool) {
	s.egressMu.Lock()
	defer s.egressMu.Unlock()
	if s.egressCache.GeneratedAt != "" || s.egressInflight {
		return Config{}, false
	}
	s.egressInflight = true
	return s.cfg, true
}

func (s *Service) collectEgressLookups(ctx context.Context, cfg Config) EgressLookupResult {
	result := LookupEgressIPs(ctx)
	result.DomesticIP = lookupDomesticIPs(ctx, cfg)

	s.egressMu.Lock()
	s.egressCache = result
	s.egressUpdatedAt = time.Now()
	s.egressInflight = false
	s.egressCond.Broadcast()
	s.egressMu.Unlock()
	return result
}

func egressCacheWithin(updatedAt, now time.Time, maxAge time.Duration) bool {
	if updatedAt.IsZero() || maxAge <= 0 || now.Before(updatedAt) {
		return false
	}
	return now.Sub(updatedAt) < maxAge
}

func (s *Service) waitForEgressLookup(ctx context.Context) EgressLookupResult {
	done := make(chan struct{})
	go func() {
		s.egressMu.Lock()
		for s.egressInflight && s.egressCache.GeneratedAt == "" {
			s.egressCond.Wait()
			select {
			case <-ctx.Done():
				s.egressMu.Unlock()
				close(done)
				return
			default:
			}
		}
		s.egressMu.Unlock()
		close(done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			s.egressCond.Broadcast()
		case <-done:
		}
	}()
	select {
	case <-ctx.Done():
	case <-done:
	}
	s.egressMu.Lock()
	cache := s.egressCache
	s.egressMu.Unlock()
	return cache
}

const manualObservationRefreshCooldown = 15 * time.Second

func (s *Service) refreshFast(ctx context.Context, refreshPublicIdentity bool, websiteMinAge time.Duration) {
	s.mu.RLock()
	interval := s.cfg.RefreshInterval
	currentNAT := s.summary.NetworkInfo.NAT
	s.mu.RUnlock()
	websiteTimeout, domesticSites, globalSites := s.websiteProbeConfigSnapshot()
	budget := websiteProbeBudget(websiteTimeout, len(domesticSites), len(globalSites)) + websiteProbeCallerGrace
	if interval > budget {
		budget = interval
	}

	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	summary, err := s.collectFastSummary(ctx, refreshPublicIdentity, websiteMinAge)

	s.mu.Lock()

	if err != nil {
		s.lastError = err.Error()
		if s.summary.GeneratedAt == "" {
			s.summary = emptySummary(interval, s.lastError)
		}
		s.summary.LastError = s.lastError
		s.mu.Unlock()
		return
	}

	summary.NetworkInfo.NAT = currentNAT
	s.lastError = ""
	previousSummary := s.summary
	s.summary = summary
	s.summary.Ready = true
	s.summary.LastError = ""
	s.summary.RefreshIntervalSec = int64(interval / time.Second)
	finalSummary := s.summary
	s.mu.Unlock()

	s.recordSummaryEvents(previousSummary, finalSummary)
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

func (s *Service) refreshNAT(ctx context.Context, minAge time.Duration) {
	s.mu.RLock()
	timeout := s.cfg.NATTimeout
	s.mu.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, timeout*2)
	defer cancel()

	nat := s.probeNAT(ctx, minAge)
	s.mu.Lock()
	previousSummary := s.summary
	s.summary.NetworkInfo.NAT = nat
	snap := s.summary
	s.mu.Unlock()
	s.recordSummaryEvents(previousSummary, snap)
	s.alert.check(snap)
	s.broadcast(snap)
}

func (s *Service) collectFastSummary(ctx context.Context, refreshPublicIdentity bool, websiteMinAge time.Duration) (Summary, error) {
	var website WebsiteConnectivity
	var networkInfo NetworkInfo

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		website = s.probeWebsiteConnectivity(ctx, websiteMinAge)
	}()

	go func() {
		defer wg.Done()
		networkInfo = s.probeNetworkInfo(ctx, refreshPublicIdentity)
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

func (s *Service) collectFastSummaryConditional(ctx context.Context, runConnectivity, refreshPublicIdentity bool) (Summary, error) {
	if runConnectivity {
		return s.collectFastSummary(ctx, refreshPublicIdentity, 0)
	}
	s.mu.RLock()
	prev := s.summary
	s.mu.RUnlock()
	return Summary{
		GeneratedAt:         localTimestamp(),
		RefreshIntervalSec:  int64(s.cfg.RefreshInterval / time.Second),
		Ready:               true,
		WebsiteConnectivity: prev.WebsiteConnectivity,
		NetworkInfo:         prev.NetworkInfo,
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
