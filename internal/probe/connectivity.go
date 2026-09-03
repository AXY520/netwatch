package probe

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// Website latency follows the zashboard model, adapted for host-side probes:
//   hit a tiny asset (favicon / generate_204), time until response headers,
//   success → latency_ms, failure → 0 + down.
// No HEAD→GET fallback, no 5xx "degraded", no response-header-timeout maze.
//
// Must stay server-side: netwatch diagnoses the *host* egress path. Browser
// <img> timing would measure the user's phone/PC instead of the Lazycat box.

const websiteProbeTimeoutFallback = 5 * time.Second

const (
	websiteProbeBatchGrace  = 300 * time.Millisecond
	websiteProbeCallerGrace = 200 * time.Millisecond
)

// Max body we will discard after headers — favicons are tiny; never wait on a full page.
const websiteProbeDrainLimit = 64 << 10

// probeWebsiteConnectivity merges concurrent startup/manual/background calls
// and applies the short manual-refresh cooldown supplied by the caller.
func (s *Service) probeWebsiteConnectivity(ctx context.Context, minAge time.Duration) WebsiteConnectivity {
	s.websiteProbeMu.Lock()
	cache := s.websiteProbeCache
	if cache.GeneratedAt != "" && egressCacheWithin(s.websiteProbeUpdatedAt, time.Now(), minAge) {
		s.websiteProbeMu.Unlock()
		return cache
	}
	if done := s.websiteProbeDone; done != nil {
		s.websiteProbeMu.Unlock()
		select {
		case <-ctx.Done():
		case <-done:
		}
		s.websiteProbeMu.Lock()
		cache = s.websiteProbeCache
		s.websiteProbeMu.Unlock()
		return cache
	}
	done := make(chan struct{})
	s.websiteProbeDone = done
	s.websiteProbeMu.Unlock()

	result := s.ProbeWebsiteConnectivity(ctx)
	s.websiteProbeMu.Lock()
	s.websiteProbeCache = result
	s.websiteProbeUpdatedAt = time.Now()
	if s.websiteProbeDone == done {
		s.websiteProbeDone = nil
		close(done)
	}
	s.websiteProbeMu.Unlock()
	return result
}

func (s *Service) ProbeWebsiteConnectivity(ctx context.Context) WebsiteConnectivity {
	timeout, domesticSites, globalSites := s.websiteProbeConfigSnapshot()
	// Domestic and global probes run as two bounded lanes. Sites inside each
	// lane are independent UI entries and are checked sequentially, avoiding a
	// burst of four simultaneous public requests while still reporting all of
	// the configured sites.
	batchCtx, batchCancel := context.WithTimeout(ctx, websiteProbeBudget(timeout, len(domesticSites), len(globalSites)))
	defer batchCancel()

	var domestic, global []TargetResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		domestic = probeTargets(batchCtx, domesticSites, timeout)
	}()
	go func() {
		defer wg.Done()
		global = probeTargets(batchCtx, globalSites, timeout)
	}()
	wg.Wait()

	return WebsiteConnectivity{
		GeneratedAt:    localTimestamp(),
		DomesticStatus: summarizeStatus(domestic),
		GlobalStatus:   summarizeStatus(global),
		Domestic:       domestic,
		Global:         global,
	}
}

func (s *Service) websiteProbeConfigSnapshot() (time.Duration, []SiteTarget, []SiteTarget) {
	s.mu.RLock()
	timeout := s.cfg.HTTPTimeout
	domestic := append([]SiteTarget(nil), s.cfg.DomesticSites...)
	global := append([]SiteTarget(nil), s.cfg.GlobalSites...)
	s.mu.RUnlock()
	if timeout <= 0 {
		timeout = websiteProbeTimeoutFallback
	}
	return timeout, domestic, global
}

func websiteProbeBudget(timeout time.Duration, domesticTargets, globalTargets int) time.Duration {
	if timeout <= 0 {
		timeout = websiteProbeTimeoutFallback
	}
	maxTargets := max(domesticTargets, globalTargets)
	if maxTargets <= 0 {
		return websiteProbeBatchGrace
	}
	return timeout*time.Duration(maxTargets) + websiteProbeBatchGrace
}

// Shared keep-alive client; per-request deadline comes only from context.
var probeClient = &http.Client{
	Timeout: 0,
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   2 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   3 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 0,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	},
	// Favicon CDNs sometimes redirect once; a couple hops is enough.
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

func probeTargets(ctx context.Context, targets []SiteTarget, timeout time.Duration) []TargetResult {
	if timeout <= 0 {
		timeout = websiteProbeTimeoutFallback
	}
	results := make([]TargetResult, 0, len(targets))
	for _, target := range targets {
		results = append(results, probeHTTPTarget(ctx, target, timeout))
	}
	return results
}

// probeHTTPTarget is one GET of a small URL. Mirrors zashboard's img.onload / onerror:
//
//	got headers → ok + latency_ms
//	error/timeout → down + latency_ms=0
func probeHTTPTarget(ctx context.Context, target SiteTarget, timeout time.Duration) TargetResult {
	result := TargetResult{
		Name:      target.Name,
		URL:       target.URL,
		Status:    StatusUnknown,
		CheckedAt: localTimestamp(),
	}
	if timeout <= 0 {
		timeout = websiteProbeTimeoutFallback
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target.URL, nil)
	if err != nil {
		result.Status = StatusDown
		result.Error = err.Error()
		return result
	}
	req.Header.Set("User-Agent", "netwatch/0.9")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Cache-Control", "no-cache")

	start := time.Now()
	resp, err := probeClient.Do(req)
	if err != nil {
		result.Status = StatusDown
		result.LatencyMS = 0
		result.Error = err.Error()
		return result
	}
	// Latency = time-to-headers (Do returns after headers). Body drain is free of the clock.
	ms := latencyMS(time.Since(start))
	code := resp.StatusCode
	_, _ = io.CopyN(io.Discard, resp.Body, websiteProbeDrainLimit)
	_ = resp.Body.Close()

	result.LatencyMS = ms
	result.TTFBMs = ms
	// Any HTTP answer means the path is up — same spirit as img.onload.
	if code > 0 {
		result.Status = StatusOK
	} else {
		result.Status = StatusUnknown
	}
	return result
}

func latencyMS(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	ms := d.Milliseconds()
	if ms <= 0 {
		return 1
	}
	return ms
}
