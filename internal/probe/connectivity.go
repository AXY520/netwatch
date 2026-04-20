package probe

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"sync"
	"time"

	"netwatch/internal/logger"
)

func (s *Service) ProbeWebsiteConnectivity(ctx context.Context) WebsiteConnectivity {
	domestic := probeTargets(ctx, s.cfg.DomesticSites, s.cfg.HTTPTimeout)
	global := probeTargets(ctx, s.cfg.GlobalSites, s.cfg.HTTPTimeout)

	return WebsiteConnectivity{
		GeneratedAt:    localTimestamp(),
		DomesticStatus: summarizeStatus(domestic),
		GlobalStatus:   summarizeStatus(global),
		Domestic:       domestic,
		Global:         global,
	}
}

// probeClient 共享 Transport 开启 keep-alive，后续探测复用连接，
// 这样我们测到的 HEAD 耗时约等于 1 个真实往返 RTT（TLS 握手只在第一次付代价）。
var probeClient = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       120 * time.Second,
		TLSHandshakeTimeout:   4 * time.Second,
		ResponseHeaderTimeout: 4 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	},
}

func probeTargets(ctx context.Context, targets []SiteTarget, timeout time.Duration) []TargetResult {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	results := make([]TargetResult, len(targets))
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

// probeHTTPTarget 真实端到端延迟：HEAD 请求走 HTTP_PROXY，测量从请求发出到收到首字节的总时间。
// - 首次探测：含 DNS + TCP + TLS 握手，数字偏高（100-300ms 正常）
// - 后续探测：复用 keep-alive 连接，只含 1 个 RTT（20-50ms）
// 不再用 TCP 纯握手 —— 在透明代理/TUN 环境下 TCP 会被本地代理瞬间接受，数字假。
func probeHTTPTarget(ctx context.Context, target SiteTarget, timeout time.Duration) TargetResult {
	result := TargetResult{
		Name:      target.Name,
		URL:       target.URL,
		Status:    StatusUnknown,
		CheckedAt: localTimestamp(),
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodHead, target.URL, nil)
	if err != nil {
		result.Status = StatusDown
		result.Error = err.Error()
		return result
	}
	req.Header.Set("User-Agent", "netwatch/0.5")

	resp, err := probeClient.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		result.LatencyMS = elapsed.Milliseconds()
		result.Status = StatusDown
		result.Error = err.Error()
		logger.Warn("probe %s: %v", target.Name, err)
		return result
	}
	defer resp.Body.Close()

	// 某些站点禁用 HEAD，返回 405：回退到 GET 并丢弃 body
	if resp.StatusCode == http.StatusMethodNotAllowed {
		_, _ = io.Copy(io.Discard, resp.Body)
		getStart := time.Now()
		req2, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target.URL, nil)
		if err == nil {
			req2.Header.Set("User-Agent", "netwatch/0.5")
			resp2, err := probeClient.Do(req2)
			if err == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp2.Body, 512))
				resp2.Body.Close()
				elapsed = time.Since(getStart)
				result.Status = statusFromHTTP(resp2.StatusCode)
				result.LatencyMS = elapsed.Milliseconds()
				return result
			}
		}
	}

	result.LatencyMS = elapsed.Milliseconds()
	result.Status = statusFromHTTP(resp.StatusCode)
	return result
}

func statusFromHTTP(code int) ProbeStatus {
	switch {
	case code >= 200 && code < 600:
		return StatusOK
	default:
		return StatusUnknown
	}
}
