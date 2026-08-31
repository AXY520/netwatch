package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	portPolicyProvider       = "ispeedtest"
	portPolicyProtocol       = "http"
	portPolicyPingCount      = 5
	portPolicyUploadSize     = int64(100 * 1024 * 1024)
	portPolicyProgressPeriod = 200 * time.Millisecond
	portPolicyRequestTimeout = 8 * time.Second
)

var (
	broadbandPortPolicyControlBaseURL = "http://ispeedtest.com.cn/api/start-test"
	broadbandPortPolicyTargetHost     = "ispeedtest.com.cn"
)

type portPolicyRemoteAllocation struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

type portPolicyTarget struct {
	ID       string
	Label    string
	Host     string
	Port     int
	Protocol string
}

func (t portPolicyTarget) endpoint(path string) string {
	return fmt.Sprintf("%s://%s:%d%s", t.Protocol, t.Host, t.Port, path)
}

func portPolicyControlBaseURL() string {
	if value := strings.TrimSpace(os.Getenv("BROADBAND_PORT_POLICY_CONTROL_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	return strings.TrimRight(broadbandPortPolicyControlBaseURL, "/")
}

func portPolicyTargetHost() string {
	if value := strings.TrimSpace(os.Getenv("BROADBAND_PORT_POLICY_TARGET_HOST")); value != "" {
		return value
	}
	return broadbandPortPolicyTargetHost
}

func allocatePortPolicyTargets(ctx context.Context) ([]portPolicyTarget, error) {
	type allocationResult struct {
		id         string
		allocation portPolicyRemoteAllocation
		err        error
	}
	results := make(chan allocationResult, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"low", "high"} {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			allocation, err := requestPortPolicyAllocation(ctx, portPolicyControlBaseURL()+"/"+id+"_"+portPolicyProtocol)
			results <- allocationResult{id: id, allocation: allocation, err: err}
		}()
	}
	wg.Wait()
	close(results)

	allocations := make(map[string]portPolicyRemoteAllocation, 2)
	for result := range results {
		if result.err != nil {
			return nil, fmt.Errorf("申请%s端口失败: %w", portPolicyTargetLabel(result.id), result.err)
		}
		allocations[result.id] = result.allocation
	}
	host := portPolicyTargetHost()
	return []portPolicyTarget{
		{ID: "low", Label: "低位端口", Host: host, Port: allocations["low"].Port, Protocol: portPolicyProtocol},
		{ID: "high", Label: "高位端口", Host: host, Port: allocations["high"].Port, Protocol: portPolicyProtocol},
	}, nil
}

func requestPortPolicyAllocation(ctx context.Context, endpoint string) (portPolicyRemoteAllocation, error) {
	requestCtx, cancel := context.WithTimeout(ctx, portPolicyRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, nil)
	if err != nil {
		return portPolicyRemoteAllocation{}, err
	}
	resp, err := (&http.Client{Timeout: portPolicyRequestTimeout}).Do(req)
	if err != nil {
		return portPolicyRemoteAllocation{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return portPolicyRemoteAllocation{}, fmt.Errorf("远端返回 %s: %s", resp.Status, message)
	}
	var allocation portPolicyRemoteAllocation
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&allocation); err != nil {
		return portPolicyRemoteAllocation{}, fmt.Errorf("响应格式无效: %w", err)
	}
	if allocation.Port < 1024 || allocation.Port > 65535 {
		return portPolicyRemoteAllocation{}, fmt.Errorf("端口号无效: %d", allocation.Port)
	}
	return allocation, nil
}

func portPolicyHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.MaxIdleConns = 8
	transport.MaxIdleConnsPerHost = 4
	return &http.Client{Transport: transport}
}

func portPolicyPing(ctx context.Context, client *http.Client, target portPolicyTarget) (int64, int64, error) {
	samples := make([]time.Duration, 0, portPolicyPingCount-1)
	for i := 0; i < portPolicyPingCount; i++ {
		started := time.Now()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, noCacheURL(target.endpoint("/ping")), nil)
		if err != nil {
			return 0, 0, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, 0, err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return 0, 0, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		if i > 0 {
			samples = append(samples, time.Since(started))
		}
	}
	if len(samples) == 0 {
		return 0, 0, errors.New("没有有效延迟样本")
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
	var jitter time.Duration
	if len(samples) > 1 {
		jitter = jitterTotal / time.Duration(len(samples)-1)
	}
	return average.Milliseconds(), jitter.Milliseconds(), nil
}

func portPolicyDownload(ctx context.Context, client *http.Client, target portPolicyTarget, duration time.Duration, update func(float64)) (float64, error) {
	testCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	started := time.Now()
	req, err := http.NewRequestWithContext(testCtx, http.MethodGet, noCacheURL(target.endpoint("/download")), nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var bytesRead atomic.Int64
	ticker := time.NewTicker(portPolicyProgressPeriod)
	defer ticker.Stop()
	done := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(io.Discard, &measuredReadCloser{r: resp.Body, measure: func(n int) { bytesRead.Add(int64(n)) }})
		done <- copyErr
	}()
	for {
		select {
		case copyErr := <-done:
			elapsed := time.Since(started)
			if bytesRead.Load() == 0 {
				if copyErr != nil {
					return 0, copyErr
				}
				return 0, errors.New("下载端点未返回有效数据")
			}
			return float64(bytesRead.Load()) * 8 / elapsed.Seconds() / 1e6, nil
		case <-ticker.C:
			elapsed := time.Since(started)
			if elapsed > 0 && update != nil {
				update(float64(bytesRead.Load()) * 8 / elapsed.Seconds() / 1e6)
			}
		case <-testCtx.Done():
			resp.Body.Close()
			<-done
			elapsed := time.Since(started)
			if bytesRead.Load() == 0 {
				return 0, errors.New("下载端点未返回有效数据")
			}
			return float64(bytesRead.Load()) * 8 / elapsed.Seconds() / 1e6, nil
		}
	}
}

type repeatingReader struct {
	data   []byte
	offset int
}

func (r *repeatingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	for i := range p {
		p[i] = r.data[r.offset]
		r.offset++
		if r.offset == len(r.data) {
			r.offset = 0
		}
	}
	return len(p), nil
}

type countingReader struct {
	r     io.Reader
	count atomic.Int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.count.Add(int64(n))
	}
	return n, err
}

func portPolicyUpload(ctx context.Context, client *http.Client, target portPolicyTarget, duration time.Duration, update func(float64)) (float64, error) {
	testCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	payload := serverUploadPayload()
	body := &countingReader{r: io.LimitReader(&repeatingReader{data: payload}, portPolicyUploadSize)}
	req, err := http.NewRequestWithContext(testCtx, http.MethodPost, noCacheURL(target.endpoint("/upload")), io.NopCloser(body))
	if err != nil {
		return 0, err
	}
	req.ContentLength = portPolicyUploadSize
	req.Header.Set("Content-Type", "application/octet-stream")
	started := time.Now()
	done := make(chan error, 1)
	go func() {
		resp, requestErr := client.Do(req)
		if resp != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if requestErr == nil && (resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices) {
				requestErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			}
		}
		done <- requestErr
	}()
	ticker := time.NewTicker(portPolicyProgressPeriod)
	defer ticker.Stop()
	for {
		select {
		case requestErr := <-done:
			elapsed := time.Since(started)
			if body.count.Load() == 0 {
				if requestErr != nil {
					return 0, requestErr
				}
				return 0, errors.New("上传端点未接收有效数据")
			}
			return float64(body.count.Load()) * 8 / elapsed.Seconds() / 1e6, nil
		case <-ticker.C:
			elapsed := time.Since(started)
			if elapsed > 0 && update != nil {
				update(float64(body.count.Load()) * 8 / elapsed.Seconds() / 1e6)
			}
		case <-testCtx.Done():
			requestErr := <-done
			elapsed := time.Since(started)
			if body.count.Load() == 0 {
				if requestErr != nil && !errors.Is(requestErr, context.DeadlineExceeded) {
					return 0, requestErr
				}
				return 0, errors.New("上传端点未接收有效数据")
			}
			return float64(body.count.Load()) * 8 / elapsed.Seconds() / 1e6, nil
		}
	}
}

func executeBroadbandPortPolicyTest(ctx context.Context, duration time.Duration, progress func(BroadbandPortPolicyTaskStatus)) (BroadbandPortPolicyTaskStatus, bool) {
	duration = min(max(duration, 5*time.Second), 40*time.Second)
	status := BroadbandPortPolicyTaskStatus{
		Stage:     "allocating",
		Running:   true,
		Message:   "正在申请公网高低端口",
		UpdatedAt: localTimestamp(),
		Provider:  portPolicyProvider,
		Host:      portPolicyTargetHost(),
		Protocol:  portPolicyProtocol,
	}
	report := func() {
		status.UpdatedAt = localTimestamp()
		if progress != nil {
			progress(status)
		}
	}
	report()
	targets, err := allocatePortPolicyTargets(ctx)
	if err != nil {
		status.Error = err.Error()
		status.Message = status.Error
		return status, false
	}
	status.Targets = make([]BroadbandPortPolicyTargetResult, len(targets))
	for i, target := range targets {
		status.Targets[i] = BroadbandPortPolicyTargetResult{ID: target.ID, Label: target.Label, Host: target.Host, Port: target.Port, Protocol: target.Protocol}
	}
	report()
	client := portPolicyHTTPClient()
	for i, target := range targets {
		result := &status.Targets[i]
		baseProgress := i * 50
		status.Stage = target.ID + "_latency"
		status.ProgressPercent = baseProgress + 2
		status.Message = target.Label + "延迟测试"
		report()
		latency, jitter, err := portPolicyPing(ctx, client, target)
		if err != nil {
			result.Error = "延迟测试失败: " + err.Error()
			status.Message = target.Label + " · " + result.Error
			report()
			continue
		}
		result.LatencyMS, result.JitterMS = latency, jitter
		status.Stage = target.ID + "_download"
		status.ProgressPercent = baseProgress + 10
		status.Message = target.Label + "下载测速"
		report()
		download, err := portPolicyDownload(ctx, client, target, duration, func(mbps float64) {
			result.DownloadMbps = sanitizeServerSpeed(mbps)
			status.ProgressPercent = baseProgress + 25
			status.Message = fmt.Sprintf("%s下载 %.2f Mbps", target.Label, result.DownloadMbps)
			report()
		})
		if err != nil {
			if ctx.Err() != nil {
				return status, false
			}
			result.Error = "下载测试失败: " + err.Error()
			status.Message = target.Label + " · " + result.Error
			report()
			continue
		}
		result.DownloadMbps = sanitizeServerSpeed(download)
		status.Stage = target.ID + "_upload"
		status.ProgressPercent = baseProgress + 35
		status.Message = target.Label + "上传测速"
		report()
		upload, err := portPolicyUpload(ctx, client, target, duration, func(mbps float64) {
			result.UploadMbps = sanitizeServerSpeed(mbps)
			status.ProgressPercent = baseProgress + 45
			status.Message = fmt.Sprintf("%s上传 %.2f Mbps", target.Label, result.UploadMbps)
			report()
		})
		if err != nil {
			if ctx.Err() != nil {
				return status, false
			}
			result.Error = "上传测试失败: " + err.Error()
			status.Message = target.Label + " · " + result.Error
			report()
			continue
		}
		result.UploadMbps = sanitizeServerSpeed(upload)
		result.OK = true
		status.ProgressPercent = baseProgress + 50
		report()
	}
	completed := len(status.Targets) > 0
	for _, target := range status.Targets {
		completed = completed && target.OK
	}
	if !completed {
		status.Error = "高低端口均无法完成测速"
		status.Message = status.Error
	}
	return status, completed
}

func portPolicyTargetLabel(id string) string {
	if id == "high" {
		return "高位"
	}
	return "低位"
}
