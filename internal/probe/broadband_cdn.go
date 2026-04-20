package probe

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type cdnEndpoint struct {
	Name   string
	Region string
	ISP    string
	URL    string
}

// 国内大带宽公共镜像，挂的都是 Ubuntu 24.04.3 live-server ISO（~2.5GB，稳定到 2025 年 Q4）。
// 排序无意义，启动时会做 TTFB 探测再按速度重新排列。
var chinaCDNEndpoints = []cdnEndpoint{
	{Name: "清华大学开源镜像", Region: "北京", ISP: "教育网", URL: "https://mirrors.tuna.tsinghua.edu.cn/ubuntu-releases/24.04.3/ubuntu-24.04.3-live-server-amd64.iso"},
	{Name: "中科大开源镜像", Region: "合肥", ISP: "教育网", URL: "https://mirrors.ustc.edu.cn/ubuntu-releases/24.04.3/ubuntu-24.04.3-live-server-amd64.iso"},
	{Name: "华为云开源镜像", Region: "贵阳", ISP: "华为云", URL: "https://mirrors.huaweicloud.com/ubuntu-releases/24.04.3/ubuntu-24.04.3-live-server-amd64.iso"},
	{Name: "南京大学开源镜像", Region: "南京", ISP: "教育网", URL: "https://mirror.nju.edu.cn/ubuntu-releases/24.04.3/ubuntu-24.04.3-live-server-amd64.iso"},
	{Name: "北京外国语大学镜像", Region: "北京", ISP: "教育网", URL: "https://mirrors.bfsu.edu.cn/ubuntu-releases/24.04.3/ubuntu-24.04.3-live-server-amd64.iso"},
	{Name: "网易开源镜像", Region: "杭州", ISP: "网易", URL: "https://mirrors.163.com/ubuntu-releases/24.04.3/ubuntu-24.04.3-live-server-amd64.iso"},
	{Name: "上海交大开源镜像", Region: "上海", ISP: "教育网", URL: "https://mirrors.sjtug.sjtu.edu.cn/ubuntu-releases/24.04.3/ubuntu-24.04.3-live-server-amd64.iso"},
	{Name: "重庆大学开源镜像", Region: "重庆", ISP: "教育网", URL: "https://mirrors.cqu.edu.cn/ubuntu-releases/24.04.3/ubuntu-24.04.3-live-server-amd64.iso"},
}

const cloudflareUpEndpoint = "https://speed.cloudflare.com/__up"

var cdnSpeedClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   16,
		DisableCompression:    true,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		IdleConnTimeout:       60 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	},
}

type rankedEndpoint struct {
	cdnEndpoint
	ttfbMS int64
}

// 并发探测所有节点，返回按响应时间排序的可用节点。
func probeCDNEndpoints(ctx context.Context) []rankedEndpoint {
	type item struct {
		idx  int
		ttfb int64
	}
	results := make([]item, len(chinaCDNEndpoints))
	var wg sync.WaitGroup
	for i, ep := range chinaCDNEndpoints {
		wg.Add(1)
		go func(i int, ep cdnEndpoint) {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			start := time.Now()
			req, err := http.NewRequestWithContext(c, http.MethodHead, ep.URL, nil)
			if err != nil {
				results[i] = item{i, -1}
				return
			}
			req.Header.Set("User-Agent", "netwatch/0.5")
			resp, err := cdnSpeedClient.Do(req)
			if err != nil || resp == nil {
				results[i] = item{i, -1}
				return
			}
			_ = resp.Body.Close()
			if resp.StatusCode >= 400 {
				results[i] = item{i, -1}
				return
			}
			results[i] = item{i, time.Since(start).Milliseconds()}
		}(i, ep)
	}
	wg.Wait()

	var out []rankedEndpoint
	for _, r := range results {
		if r.ttfb > 0 {
			out = append(out, rankedEndpoint{chinaCDNEndpoints[r.idx], r.ttfb})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ttfbMS < out[j].ttfbMS })
	return out
}

// 延迟 / 抖动：对目标主机做 N 次 TCP 握手，测纯 RTT，避开 DNS/TLS/应用层噪音。
func measureCDNLatency(ctx context.Context, targetURL string, samples int) (int64, int64) {
	if samples <= 0 {
		samples = 8
	}
	u, err := url.Parse(targetURL)
	if err != nil || u.Host == "" {
		return 0, 0
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	dialer := &net.Dialer{Timeout: 3 * time.Second}
	var ms []int64
	for i := 0; i < samples; i++ {
		if ctx.Err() != nil {
			break
		}
		start := time.Now()
		conn, err := dialer.DialContext(ctx, "tcp", host)
		if err != nil {
			continue
		}
		ms = append(ms, time.Since(start).Milliseconds())
		_ = conn.Close()
		// 样本间留 80ms 间隔，避免背靠背命中同一发包窗口。
		select {
		case <-ctx.Done():
			return averageInt64(ms), calculateJitter(ms)
		case <-time.After(80 * time.Millisecond):
		}
	}
	if len(ms) == 0 {
		return 0, 0
	}
	return averageInt64(ms), calculateJitter(ms)
}

// 多 worker 并发下载；走 Range 从 0 开始拉 ISO，链路打满后循环下一次。
func runCDNDownload(ctx context.Context, endpoints []rankedEndpoint, duration time.Duration, workers int, progress func(mbps float64, elapsed time.Duration)) float64 {
	if len(endpoints) == 0 {
		return 0
	}
	if workers <= 0 {
		workers = 4
	}
	if duration <= 0 {
		duration = 15 * time.Second
	}
	dlCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	var totalBytes int64
	var wg sync.WaitGroup
	startedAt := time.Now()
	sampler := newThroughputSampler(startedAt)
	done := make(chan struct{})

	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				cur := sampler.observe(atomic.LoadInt64(&totalBytes), time.Now())
				if progress != nil {
					progress(cur, time.Since(startedAt))
				}
			}
		}
	}()

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ep := endpoints[idx%len(endpoints)]
			buf := make([]byte, 64*1024)
			for dlCtx.Err() == nil {
				req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, ep.URL, nil)
				if err != nil {
					continue
				}
				req.Header.Set("User-Agent", "netwatch/0.5")
				req.Header.Set("Cache-Control", "no-cache")
				req.Header.Set("Accept-Encoding", "identity")
				resp, err := cdnSpeedClient.Do(req)
				if err != nil {
					continue
				}
				for dlCtx.Err() == nil {
					n, rerr := resp.Body.Read(buf)
					if n > 0 {
						atomic.AddInt64(&totalBytes, int64(n))
					}
					if rerr != nil {
						break
					}
				}
				_ = resp.Body.Close()
			}
		}(i)
	}

	wg.Wait()
	close(done)

	final := atomic.LoadInt64(&totalBytes)
	sampler.observe(final, time.Now())
	stable := sampler.stableMbps()
	if stable > 0 {
		return stable
	}
	sec := time.Since(startedAt).Seconds()
	if sec <= 0 || final <= 0 {
		return 0
	}
	return float64(final*8) / sec / 1_000_000
}

// 上传：Cloudflare speed 的 /__up（国内回程通常走 HK/SG，可能偏低；UI 里会标注）。
func runCloudflareUpload(ctx context.Context, duration time.Duration, workers int, progress func(mbps float64, elapsed time.Duration)) float64 {
	if workers <= 0 {
		workers = 4
	}
	if duration <= 0 {
		duration = 15 * time.Second
	}
	upCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	payloadBytes := 1 * 1024 * 1024
	payload := make([]byte, payloadBytes)

	var totalBytes int64
	var wg sync.WaitGroup
	startedAt := time.Now()
	sampler := newThroughputSampler(startedAt)
	done := make(chan struct{})

	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				cur := sampler.observe(atomic.LoadInt64(&totalBytes), time.Now())
				if progress != nil {
					progress(cur, time.Since(startedAt))
				}
			}
		}
	}()

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for upCtx.Err() == nil {
				reader := &countingReader{
					inner:   bytes.NewReader(payload),
					counter: &totalBytes,
				}
				req, err := http.NewRequestWithContext(upCtx, http.MethodPost, cloudflareUpEndpoint, reader)
				if err != nil {
					continue
				}
				req.Header.Set("Content-Type", "application/octet-stream")
				req.Header.Set("User-Agent", "netwatch/0.5")
				req.ContentLength = int64(payloadBytes)
				resp, err := cdnSpeedClient.Do(req)
				if err != nil {
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}()
	}

	wg.Wait()
	close(done)

	final := atomic.LoadInt64(&totalBytes)
	sampler.observe(final, time.Now())
	stable := sampler.stableMbps()
	if stable > 0 {
		return stable
	}
	sec := time.Since(startedAt).Seconds()
	if sec <= 0 || final <= 0 {
		return 0
	}
	return float64(final*8) / sec / 1_000_000
}

type countingReader struct {
	inner   *bytes.Reader
	counter *int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if n > 0 {
		atomic.AddInt64(r.counter, int64(n))
	}
	return n, err
}
