package api

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"netwatch/internal/probe"
)

func TestHandleTraceGetDoesNotRequireHost(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/trace", nil)
	rec := httptest.NewRecorder()

	handler.handleTrace(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestHandleTracePostRequiresHost(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/trace", nil)
	rec := httptest.NewRecorder()

	handler.handleTrace(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleTraceRejectsUnsupportedMethods(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/diagnostics/trace?host=example.com", nil)
	rec := httptest.NewRecorder()

	handler.handleTrace(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusMethodNotAllowed, rec.Body.String())
	}
}

func TestHandleLocalUploadReportsReceivedBytes(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speed/local/upload", strings.NewReader("abc"))
	rec := httptest.NewRecorder()

	handler.handleLocalUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got struct {
		ReceivedBytes int64 `json:"received_bytes"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ReceivedBytes != 3 {
		t.Fatalf("received_bytes = %d, want 3", got.ReceivedBytes)
	}
}

func TestHandleLocalUploadRejectsOversizedBodies(t *testing.T) {
	oldLimit := maxLocalUploadBytes
	maxLocalUploadBytes = 4
	defer func() { maxLocalUploadBytes = oldLimit }()

	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speed/local/upload", strings.NewReader("12345"))
	rec := httptest.NewRecorder()

	handler.handleLocalUpload(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
}

func TestHandleHealthReportsStartingUntilFirstProbe(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.handleHealth(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleNetworkMutationAudit(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/network/mutations/audit?limit=10", nil)
	rec := httptest.NewRecorder()

	handler.handleNetworkMutationAudit(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got struct {
		Events []probe.NetworkMutationAuditEvent `json:"events"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Events) != 0 {
		t.Fatalf("events = %+v, want empty", got.Events)
	}
}

func TestHandleNetworkMutationAuditRejectsUnsupportedMethods(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/network/mutations/audit", nil)
	rec := httptest.NewRecorder()
	handler.handleNetworkMutationAudit(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestMetricsExposeRawAndSemanticAppTrafficCounters(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.handleMetrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	for _, metric := range []string{
		"netwatch_app_traffic_rx_bytes",
		"netwatch_app_traffic_tx_bytes",
		"netwatch_app_traffic_upload_bytes",
		"netwatch_app_traffic_download_bytes",
	} {
		if !strings.Contains(body, "# HELP "+metric+" ") {
			t.Fatalf("metrics missing %s", metric)
		}
	}
}

func TestSpeedStreamLimitReturnsTooManyRequests(t *testing.T) {
	handler := newTestHandler(t)
	for i := 0; i < maxConcurrentSpeedStreams; i++ {
		handler.speedSlots <- struct{}{}
	}
	defer func() {
		for i := 0; i < maxConcurrentSpeedStreams; i++ {
			<-handler.speedSlots
		}
	}()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/speed/local/upload", strings.NewReader("abc"))
	rec := httptest.NewRecorder()
	handler.handleLocalUpload(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
}

func TestHandleLocalResultRejectsInvalidNumbers(t *testing.T) {
	handler := newTestHandler(t)
	payload := strings.NewReader(`{"download_mbps":-1,"upload_mbps":10}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speed/local/result", payload)
	rec := httptest.NewRecorder()

	handler.handleLocalResult(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestRecordLocalTransferResultClampsOutliers(t *testing.T) {
	service := probe.NewService(testConfig(t))
	t.Cleanup(service.Close)

	got := service.RecordLocalTransferResult(probe.LocalTransferResult{
		DownloadMbps:       math.Inf(1),
		UploadMbps:         200000,
		PayloadMB:          2048,
		RoundTripLatencyMS: 700000,
		JitterMS:           700000,
	})

	if got.DownloadMbps != 0 {
		t.Fatalf("download_mbps = %v, want 0", got.DownloadMbps)
	}
	if got.UploadMbps != 100000 {
		t.Fatalf("upload_mbps = %v, want 100000", got.UploadMbps)
	}
	if got.PayloadMB != 1024 {
		t.Fatalf("payload_mb = %v, want 1024", got.PayloadMB)
	}
	if got.RoundTripLatencyMS != 0 || got.JitterMS != 0 {
		t.Fatalf("latency/jitter = %d/%d, want 0/0", got.RoundTripLatencyMS, got.JitterMS)
	}
}

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	service := probe.NewService(testConfig(t))
	t.Cleanup(service.Close)
	return NewHandler(service)
}

func testConfig(t *testing.T) probe.Config {
	t.Helper()
	cfg := probe.DefaultConfig()
	cfg.DataDir = t.TempDir()
	return cfg
}
