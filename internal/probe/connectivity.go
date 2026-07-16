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

// Max body we will discard after headers — favicons are tiny; never wait on a full page.
const websiteProbeDrainLimit = 64 << 10

func (s *Service) ProbeWebsiteConnectivity(ctx context.Context) WebsiteConnectivity {
	timeout := s.cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = websiteProbeTimeoutFallback
	}
	batchCtx, batchCancel := context.WithTimeout(ctx, timeout+300*time.Millisecond)
	defer batchCancel()

	var domestic, global []TargetResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		domestic = probeTargets(batchCtx, s.cfg.DomesticSites, timeout)
	}()
	go func() {
		defer wg.Done()
		global = probeTargets(batchCtx, s.cfg.GlobalSites, timeout)
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
	results := make([]TargetResult, len(targets))
	if len(targets) == 0 {
		return results
	}
	var wg sync.WaitGroup
	wg.Add(len(targets))
	for i, target := range targets {
		go func(index int, target SiteTarget) {
			defer wg.Done()
			results[index] = probeHTTPTarget(ctx, target, timeout)
		}(i, target)
	}
	wg.Wait()
	return results
}

// probeHTTPTarget is one GET of a small URL. Mirrors zashboard's img.onload / onerror:
//   got headers → ok + latency_ms
//   error/timeout → down + latency_ms=0
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
