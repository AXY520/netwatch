package probe

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"math"
	mrand "math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const (
	serverBroadbandDownloadStreams = 15
	serverBroadbandUploadStreams   = 5
	serverBroadbandGrace           = 2 * time.Second
	serverBroadbandUploadBytes     = 20 * 1024 * 1024
)

type BroadbandServerRequest struct {
	NodeID string `json:"node_id"`
}

var (
	serverBroadbandUploadPayload     []byte
	serverBroadbandUploadPayloadOnce sync.Once
)

func serverUploadPayload() []byte {
	serverBroadbandUploadPayloadOnce.Do(func() {
		serverBroadbandUploadPayload = make([]byte, serverBroadbandUploadBytes)
		if _, err := crand.Read(serverBroadbandUploadPayload); err != nil {
			// The payload only needs to resist transport compression. A failed
			// entropy read should not make a bandwidth test unavailable.
			for i := range serverBroadbandUploadPayload {
				serverBroadbandUploadPayload[i] = byte(i*31 + 17)
			}
		}
	})
	return serverBroadbandUploadPayload
}

func publicBroadbandHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.MaxIdleConns = serverBroadbandDownloadStreams + serverBroadbandUploadStreams + 8
	transport.MaxIdleConnsPerHost = serverBroadbandDownloadStreams + serverBroadbandUploadStreams
	return &http.Client{Transport: transport}
}

func choosePublicBroadbandNode(request BroadbandServerRequest) (publicBroadbandNode, error) {
	if request.NodeID == "" {
		request.NodeID = "1"
	}
	node, ok := publicBroadbandNodeByID(request.NodeID)
	if !ok {
		return publicBroadbandNode{}, fmt.Errorf("未知测速节点: %s", request.NodeID)
	}
	return node, nil
}

func nodeURL(urls []string) (string, error) {
	if len(urls) == 0 {
		return "", errors.New("测速节点没有对应的端点")
	}
	return urls[mrand.IntN(len(urls))], nil
}

func noCacheURL(raw string) string {
	separator := "?"
	for _, r := range raw {
		if r == '?' {
			separator = "&"
			break
		}
	}
	return raw + separator + "nocache=" + fmt.Sprintf("%d", time.Now().UnixNano())
}

func serverPing(ctx context.Context, client *http.Client, node publicBroadbandNode) (int64, int64, error) {
	var samples []time.Duration
	for i := 0; i < 5; i++ {
		url, err := nodeURL(node.PingURLs)
		if err != nil {
			return 0, 0, err
		}
		start := time.Now()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, noCacheURL(url), nil)
		if err != nil {
			return 0, 0, err
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return 0, 0, ctx.Err()
			}
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		// A completed HTTP exchange proves the endpoint is reachable. Several
		// public CDN ping URLs intentionally return 404 for their root path.
		if i > 0 {
			samples = append(samples, time.Since(start))
		}
	}
	if len(samples) == 0 {
		return 0, 0, errors.New("延迟端点不可用")
	}
	var total time.Duration
	for _, sample := range samples {
		total += sample
	}
	average := total / time.Duration(len(samples))
	var jitterTotal time.Duration
	for i := 1; i < len(samples); i++ {
		delta := samples[i] - samples[i-1]
		if delta < 0 {
			delta = -delta
		}
		jitterTotal += delta
	}
	jitter := time.Duration(0)
	if len(samples) > 1 {
		jitter = jitterTotal / time.Duration(len(samples)-1)
	}
	return average.Milliseconds(), jitter.Milliseconds(), nil
}

type measuredReadCloser struct {
	r       io.ReadCloser
	measure func(int)
}

func (r *measuredReadCloser) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.measure(n)
	}
	return n, err
}

func (r *measuredReadCloser) Close() error { return r.r.Close() }

func runServerDownload(ctx context.Context, client *http.Client, node publicBroadbandNode, duration time.Duration, update func(float64)) (float64, error) {
	if duration <= serverBroadbandGrace {
		duration = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	started := time.Now()
	measureAt := started.Add(serverBroadbandGrace)
	var bytesRead atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < serverBroadbandDownloadStreams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				url, err := nodeURL(node.DownloadURLs)
				if err != nil {
					return
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, noCacheURL(url), nil)
				if err != nil {
					return
				}
				resp, err := client.Do(req)
				if err != nil {
					continue
				}
				if resp.StatusCode < 200 || resp.StatusCode >= 400 {
					resp.Body.Close()
					continue
				}
				_, _ = io.Copy(io.Discard, &measuredReadCloser{r: resp.Body, measure: func(n int) {
					if time.Now().After(measureAt) {
						bytesRead.Add(int64(n))
					}
				}})
				resp.Body.Close()
			}
		}()
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			elapsed := time.Since(measureAt)
			if elapsed < time.Millisecond {
				elapsed = time.Millisecond
			}
			mbps := float64(bytesRead.Load()) * 8 * 1.06 / elapsed.Seconds() / 1e6
			if bytesRead.Load() == 0 {
				return 0, errors.New("下载端点未返回有效数据")
			}
			return mbps, nil
		case <-ticker.C:
			elapsed := time.Since(measureAt)
			if elapsed > 0 {
				update(float64(bytesRead.Load()) * 8 * 1.06 / elapsed.Seconds() / 1e6)
			}
		}
	}
}

func runServerUpload(ctx context.Context, client *http.Client, node publicBroadbandNode, duration time.Duration, update func(float64)) (float64, error) {
	if duration <= serverBroadbandGrace {
		duration = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	started := time.Now()
	measureAt := started.Add(serverBroadbandGrace)
	payload := serverUploadPayload()
	var bytesSent atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < serverBroadbandUploadStreams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				url, err := nodeURL(node.UploadURLs)
				if err != nil {
					return
				}
				var counted int64
				body := &measuredReadCloser{r: io.NopCloser(bytes.NewReader(payload)), measure: func(n int) {
					counted += int64(n)
					if time.Now().After(measureAt) {
						bytesSent.Add(int64(n))
					}
				}}
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, noCacheURL(url), body)
				if err != nil {
					body.Close()
					return
				}
				req.ContentLength = int64(len(payload))
				req.Header.Set("Content-Type", "application/octet-stream")
				resp, err := client.Do(req)
				if err == nil && resp != nil {
					_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
					resp.Body.Close()
				}
				_ = counted
			}
		}()
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			elapsed := time.Since(measureAt)
			if elapsed < time.Millisecond {
				elapsed = time.Millisecond
			}
			mbps := float64(bytesSent.Load()) * 8 * 1.06 / elapsed.Seconds() / 1e6
			if bytesSent.Load() == 0 {
				return 0, errors.New("上传端点未接收有效数据")
			}
			return mbps, nil
		case <-ticker.C:
			elapsed := time.Since(measureAt)
			if elapsed > 0 {
				update(float64(bytesSent.Load()) * 8 * 1.06 / elapsed.Seconds() / 1e6)
			}
		}
	}
}

func executeServerBroadbandSpeedTest(ctx context.Context, duration time.Duration, request BroadbandServerRequest, progress func(stage string, progress int, message string, partial BroadbandSpeedResult)) (BroadbandSpeedResult, bool) {
	duration = min(max(duration, 5*time.Second), 40*time.Second)
	node, err := choosePublicBroadbandNode(request)
	result := BroadbandSpeedResult{Timestamp: localTimestamp(), TestMode: "server", NodeID: node.ID, NodeName: node.Label, NodeCategory: node.Category, NodeSource: node.Category, Provider: node.Label}
	if err != nil {
		result.Error = err.Error()
		return result, false
	}
	client := publicBroadbandHTTPClient()
	report := func(stage string, pct int, message string) {
		if progress != nil {
			progress(stage, pct, message, result)
		}
	}
	report("starting", 2, "服务器正在连接 "+node.Label)
	latency, jitter, err := serverPing(ctx, client, node)
	if err != nil {
		result.Error = "延迟测试失败: " + err.Error()
		return result, false
	}
	result.LatencyMS, result.JitterMS = latency, jitter
	report("latency", 15, fmt.Sprintf("服务器延迟 %d ms", latency))
	download, err := runServerDownload(ctx, client, node, duration, func(mbps float64) {
		result.DownloadMbps = sanitizeServerSpeed(mbps)
		report("download", 20, fmt.Sprintf("服务器下载测速 %.2f Mbps", result.DownloadMbps))
	})
	if err != nil {
		result.Error = "下载测试失败: " + err.Error()
		return result, false
	}
	result.DownloadMbps = sanitizeServerSpeed(download)
	report("download", 60, fmt.Sprintf("服务器下载 %.2f Mbps", result.DownloadMbps))
	upload, err := runServerUpload(ctx, client, node, duration, func(mbps float64) {
		result.UploadMbps = sanitizeServerSpeed(mbps)
		report("upload", 65, fmt.Sprintf("服务器上传测速 %.2f Mbps", result.UploadMbps))
	})
	if err != nil {
		result.Error = "上传测试失败: " + err.Error()
		return result, false
	}
	result.UploadMbps = sanitizeServerSpeed(upload)
	report("complete", 100, "服务器宽带测速完成")
	return result, true
}

func sanitizeServerSpeed(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	return math.Min(value, 100000)
}
