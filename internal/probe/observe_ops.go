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
		GeneratedAt:      localTimestamp(),
		Hostname:         hostname,
		Interfaces:       interfaces,
		DefaultIPv4:      readDefaultIPv4Route(),
		DefaultIPv6:      readDefaultIPv6Route(),
		ProxyEnvironment: detectProxyEnvironment(),
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
	// 手动刷新时清除公网 IP 缓存，强制重新查询
	s.publicIPMu.Lock()
	s.publicIPCache = publicIPCacheData{}
	s.publicIPMu.Unlock()
	// 同时清除出口 IP 查询缓存
	s.RefreshEgressLookups(ctx)
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
	if timeout <= 0 {
		timeout = websiteProbeTimeoutFallback
	}
	// Slightly above per-target budget so we never cancel a still-valid last hop.
	ctx, cancel := context.WithTimeout(ctx, timeout+500*time.Millisecond)
	defer cancel()

	website := s.ProbeWebsiteConnectivity(ctx)

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
	return s.nicStats.sampleAndSnapshot()
}

// ForceGetRealtimeNetStats always double-samples so manual refresh gets fresh bps.
func (s *Service) ForceGetRealtimeNetStats() RealtimeNetStats {
	return s.nicStats.forceSampleAndSnapshot()
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

// ClearPublicIPCache 清除公网 IP 缓存，下次 ProbeNetworkInfo 会重新查询。
func (s *Service) ClearPublicIPCache() {
	s.publicIPMu.Lock()
	s.publicIPCache = publicIPCacheData{}
	s.publicIPMu.Unlock()
}

// RefreshEgressLookups 强制立刻刷新并更新缓存。
func (s *Service) RefreshEgressLookups(ctx context.Context) EgressLookupResult {
	s.egressMu.Lock()
	if s.egressInflight {
		cache := s.egressCache
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

	result := LookupEgressIPs(ctx)
	result.DomesticIP = lookupDomesticIPs(ctx, cfg)

	s.egressMu.Lock()
	s.egressCache = result
	s.egressInflight = false
	s.egressCond.Broadcast()
	s.egressMu.Unlock()
	return result
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

func (s *Service) refreshNAT(ctx context.Context) {
	s.mu.RLock()
	timeout := s.cfg.NATTimeout
	s.mu.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, timeout*2)
	defer cancel()

	nat := s.ProbeNAT(ctx)
	s.mu.Lock()
	previousSummary := s.summary
	s.summary.NetworkInfo.NAT = nat
	snap := s.summary
	s.mu.Unlock()
	s.recordSummaryEvents(previousSummary, snap)
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

func (s *Service) collectFastSummaryConditional(ctx context.Context, runConnectivity bool) (Summary, error) {
	if runConnectivity {
		return s.collectFastSummary(ctx)
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
