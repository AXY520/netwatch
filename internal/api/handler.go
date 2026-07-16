package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"netwatch/internal/logger"
	"netwatch/internal/probe"
)

// downloadPayload is incompressible pseudo-random data so proxies/browsers
// cannot silently compress the stream and inflate measured throughput.
var (
	downloadPayload     []byte
	downloadPayloadOnce sync.Once
)

func localDownloadPayload() []byte {
	downloadPayloadOnce.Do(func() {
		// 2 MiB chunks reduce syscall overhead on multi-gig paths.
		buf := make([]byte, 2*1024*1024)
		// math/rand is fine here: we only need entropy against compression, not crypto.
		r := rand.New(rand.NewSource(0x4e455457)) // NETW
		for i := range buf {
			buf[i] = byte(r.Intn(256))
		}
		downloadPayload = buf
	})
	return downloadPayload
}

// maxLocalUploadBytes caps a single upload request body. Multi-stream tests
// restart streams, so this only bounds one blob (not total test volume).
var maxLocalUploadBytes int64 = 512 * 1024 * 1024

const maxConcurrentSpeedStreams = 8

type Handler struct {
	service    *probe.Service
	speedSlots chan struct{}
}

func NewHandler(service *probe.Service) *Handler {
	return &Handler{service: service, speedSlots: make(chan struct{}, maxConcurrentSpeedStreams)}
}

func (h *Handler) acquireSpeedSlot(w http.ResponseWriter) bool {
	select {
	case h.speedSlots <- struct{}{}:
		return true
	default:
		w.Header().Set("Retry-After", "2")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many concurrent speed tests"})
		return false
	}
}

func (h *Handler) releaseSpeedSlot() {
	<-h.speedSlots
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", h.handleHealth)
	mux.HandleFunc("/api/v1/capabilities", h.handleCapabilities)
	mux.HandleFunc("/api/v1/summary", h.handleSummary)
	mux.HandleFunc("/api/v1/connectivity/websites", h.handleWebsiteConnectivity)
	mux.HandleFunc("/api/v1/connectivity/websites/run", h.handleWebsiteRefresh)
	mux.HandleFunc("/api/v1/network", h.handleNetworkInfo)
	mux.HandleFunc("/api/v1/network/interfaces/refresh", h.handleNetworkInterfacesRefresh)
	mux.HandleFunc("/api/v1/network/nat/run", h.handleNATRefresh)
	mux.HandleFunc("/api/v1/probe/run", h.handleRefresh)
	mux.HandleFunc("/api/v1/speed/config", h.handleSpeedConfig)
	mux.HandleFunc("/api/v1/speed/broadband/start", h.handleBroadbandStart)
	mux.HandleFunc("/api/v1/speed/broadband/task", h.handleBroadbandTask)
	mux.HandleFunc("/api/v1/speed/broadband/cancel", h.handleBroadbandCancel)
	mux.HandleFunc("/api/v1/speed/broadband/run", h.handleBroadbandRun)
	mux.HandleFunc("/api/v1/speed/broadband/history", h.handleBroadbandHistory)
	mux.HandleFunc("/api/v1/speed/local/history", h.handleLocalHistory)
	mux.HandleFunc("/api/v1/speed/local/result", h.handleLocalResult)
	mux.HandleFunc("/api/v1/speed/local/ping", h.handleLocalPing)
	mux.HandleFunc("/api/v1/speed/local/download", h.handleLocalDownload)
	mux.HandleFunc("/api/v1/speed/local/upload", h.handleLocalUpload)
	mux.HandleFunc("/api/v1/timeseries", h.handleTimeseries)
	mux.HandleFunc("/api/v1/settings", h.handleSettings)
	mux.HandleFunc("/api/v1/diagnostics/trace", h.handleTrace)
	mux.HandleFunc("/api/v1/diagnostics/trace/task", h.handleTraceTask)
	mux.HandleFunc("/api/v1/events", h.handleSSE)
	mux.HandleFunc("/api/v1/network/realtime", h.handleRealtimeNetStats)
	mux.HandleFunc("/api/v1/network/ports", h.handleHostPorts)

	mux.HandleFunc("/api/v1/network/egress-lookups", h.handleEgressLookups)
	mux.HandleFunc("/api/v1/notifications/events", h.handleNotificationEvents)
	mux.HandleFunc("/api/v1/notifications/bark/test", h.handleBarkNotificationTest)
	mux.HandleFunc("/api/v1/notifications/pushplus/test", h.handlePushPlusNotificationTest)
	mux.HandleFunc("/api/v1/lan/devices", h.handleLANDevices)
	mux.HandleFunc("/api/v1/lan/devices/meta", h.handleLANDeviceMeta)
	mux.HandleFunc("/api/v1/network/app-traffic", h.handleAppTraffic)
	mux.HandleFunc("/api/v1/network/app-traffic/history", h.handleAppTrafficHistory)
	mux.HandleFunc("/api/v1/network/app-traffic/live", h.handleAppTrafficLive)
	mux.HandleFunc("/api/v1/network/app-traffic/top", h.handleAppTrafficTop)
	mux.HandleFunc("/api/v1/settings/persistent-traffic-bridges", h.handlePersistentTrafficBridges)
	mux.HandleFunc("/api/v1/network/ipv6/renew-nics", h.handleIPv6RenewNICs)
	mux.HandleFunc("/api/v1/network/ipv6/renew", h.handleIPv6Renew)
	mux.HandleFunc("/api/v1/network/config/devices", h.handleNetworkConfigDevices)
	mux.HandleFunc("/api/v1/network/config/pending", h.handleNetworkConfigPending)
	mux.HandleFunc("/api/v1/network/config/check-ip", h.handleNetworkConfigCheckIP)
	mux.HandleFunc("/api/v1/network/config/apply", h.handleNetworkConfigApply)
	mux.HandleFunc("/api/v1/network/config/confirm", h.handleNetworkConfigConfirm)
	mux.HandleFunc("/api/v1/network/config/rollback", h.handleNetworkConfigRollback)
	mux.HandleFunc("/api/v1/network/bridges", h.handleHostBridges)
	mux.HandleFunc("/api/v1/network/bridges/create", h.handleHostBridgeCreate)
	mux.HandleFunc("/api/v1/network/bridges/confirm", h.handleHostBridgeConfirm)
	mux.HandleFunc("/api/v1/network/bridges/rollback", h.handleHostBridgeRollback)
	mux.HandleFunc("/api/v1/network/bridges/dissolve", h.handleHostBridgeDissolve)
	mux.HandleFunc("/api/v1/network/bridges/pending", h.handleHostBridgePending)
	mux.HandleFunc("/api/v1/containers", h.handleContainers)
	mux.HandleFunc("/api/v1/containers/block", h.handleContainerBlock)
	mux.HandleFunc("/api/v1/containers/unblock", h.handleContainerUnblock)
	mux.HandleFunc("/metrics", h.handleMetrics)
}

func (h *Handler) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.Capabilities())
}

func (h *Handler) handleNetworkConfigDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	resp := h.service.ListNetworkConfigDevices(r.Context())
	status := http.StatusOK
	if resp.Enabled && resp.Error != "" {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, resp)
}

func (h *Handler) handleNetworkConfigPending(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.GetNetworkConfigPending())
}

func (h *Handler) handleNetworkConfigCheckIP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req probe.NetworkConfigIPCheckRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	result := h.service.CheckNetworkConfigIP(r.Context(), req)
	status := http.StatusOK
	if !result.OK {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, result)
}

func (h *Handler) handleNetworkConfigApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req probe.NetworkConfigApplyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	result := h.service.ApplyNetworkConfig(r.Context(), req)
	status := http.StatusOK
	if !result.OK {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, result)
}

func (h *Handler) handleNetworkConfigConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	result := h.service.ConfirmNetworkConfig(req.ID)
	status := http.StatusOK
	if !result.OK {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, result)
}

func (h *Handler) handleNetworkConfigRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	result := h.service.RollbackNetworkConfig(r.Context(), req.ID)
	status := http.StatusOK
	if !result.OK {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, result)
}

func (h *Handler) handleHostBridges(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	resp := h.service.ListHostBridges(r.Context())
	status := http.StatusOK
	if !resp.Enabled && resp.Error != "" {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, resp)
}

func (h *Handler) handleHostBridgePending(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.GetHostBridgePending())
}

func (h *Handler) handleHostBridgeCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req probe.HostBridgeCreateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	// Detach from request context: creating a bridge rewires the host NIC and can
	// briefly drop the browser's TCP connection mid-flight. Cancelling the op leaves
	// a half-applied bridge and can break host routing / app-traffic visibility.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result := h.service.CreateHostBridge(ctx, req)
	status := http.StatusOK
	if !result.OK {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, result)
}

func (h *Handler) handleHostBridgeConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req probe.HostBridgeActionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	result := h.service.ConfirmHostBridge(req.ID)
	status := http.StatusOK
	if !result.OK {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, result)
}

func (h *Handler) handleHostBridgeRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req probe.HostBridgeActionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result := h.service.RollbackHostBridge(ctx, req.ID)
	status := http.StatusOK
	if !result.OK {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, result)
}

func (h *Handler) handleHostBridgeDissolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req probe.HostBridgeActionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result := h.service.DissolveHostBridge(ctx, req.Bridge)
	status := http.StatusOK
	if !result.OK {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, result)
}

func (h *Handler) handleIPv6RenewNICs(w http.ResponseWriter, r *http.Request) {
	nics, err := h.service.ListIPv6RenewNICs(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nics": nics})
}

func (h *Handler) handleIPv6Renew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Device string `json:"device"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	if req.Device == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device required"})
		return
	}
	result := h.service.RenewIPv6(r.Context(), req.Device)
	status := http.StatusOK
	if !result.OK {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, result)
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	ready := h.service.GetSummary().Ready
	status := http.StatusOK
	state := "ok"
	if !ready {
		status = http.StatusServiceUnavailable
		state = "starting"
	}
	writeJSON(w, status, map[string]any{
		"status": state,
		"time":   time.Now().Format(time.DateTime),
		"ready":  ready,
	})
}

func (h *Handler) handleSummary(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GetSummary())
}

func (h *Handler) handleWebsiteConnectivity(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GetSummary().WebsiteConnectivity)
}

func (h *Handler) handleWebsiteRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.RefreshWebsiteConnectivity(h.service.LifecycleContext()))
}

func (h *Handler) handleNetworkInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GetSummary().NetworkInfo)
}

func (h *Handler) handleNetworkInterfacesRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.RefreshNetworkInfo(r.Context()))
}

func (h *Handler) handleNATRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.RefreshNAT(h.service.LifecycleContext()))
}

func (h *Handler) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.Refresh(h.service.LifecycleContext()))
}

func (h *Handler) handleSpeedConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GetSpeedConfig())
}

func (h *Handler) handleBroadbandStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.StartBroadbandTask())
}

func (h *Handler) handleBroadbandTask(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GetBroadbandTask())
}

func (h *Handler) handleBroadbandCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.CancelBroadbandTask())
}

func (h *Handler) handleBroadbandRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.acquireSpeedSlot(w) {
		return
	}
	defer h.releaseSpeedSlot()
	writeJSON(w, http.StatusOK, h.service.RunBroadbandSpeedTest(r.Context()))
}

func (h *Handler) handleBroadbandHistory(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GetBroadbandHistory())
}

func (h *Handler) handleLocalHistory(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GetLocalTransferHistory())
}

func (h *Handler) handleLocalResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var result probe.LocalTransferResult
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	if err := decoder.Decode(&result); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	if !validLocalTransferResult(result) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid speed result"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.RecordLocalTransferResult(result))
}

func (h *Handler) handleLocalPing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"time": time.Now().Format(time.DateTime)})
}

func (h *Handler) handleLocalDownload(w http.ResponseWriter, r *http.Request) {
	if !h.acquireSpeedSlot(w) {
		return
	}
	defer h.releaseSpeedSlot()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	// Discourage intermediate compression / transformation.
	w.Header().Set("Content-Encoding", "identity")

	payload := localDownloadPayload()
	secStr := r.URL.Query().Get("sec")
	if secStr != "" {
		sec, err := strconv.ParseFloat(secStr, 64)
		if err != nil || sec <= 0 {
			sec = 10
		}
		if sec > 60 {
			sec = 60
		}
		// Client multi-stream tests may keep a connection slightly longer than
		// the nominal window; allow a small overrun so streams do not starve.
		deadline := time.Now().Add(time.Duration((sec + 1.5) * float64(time.Second)))
		ctx := r.Context()
		flusher, canFlush := w.(http.Flusher)
		nextFlush := time.Now()
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if _, err := w.Write(payload); err != nil {
				return
			}
			// Flush aggressively so browser onprogress sees real-time bytes
			// instead of large buffered bursts that under-report mid-test rate.
			if canFlush && !time.Now().Before(nextFlush) {
				flusher.Flush()
				nextFlush = time.Now().Add(50 * time.Millisecond)
			}
		}
		if canFlush {
			flusher.Flush()
		}
		return
	}

	mb := parseMB(r.URL.Query().Get("mb"), 8)
	remaining := mb * 1024 * 1024
	ctx := r.Context()
	flusher, canFlush := w.(http.Flusher)
	for remaining > 0 {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n := len(payload)
		if remaining < n {
			n = remaining
		}
		if _, err := w.Write(payload[:n]); err != nil {
			return
		}
		remaining -= n
		if canFlush {
			flusher.Flush()
		}
	}
}

func (h *Handler) handleLocalUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.acquireSpeedSlot(w) {
		return
	}
	defer h.releaseSpeedSlot()
	// Read body as-is without decompression/transform so measured size matches wire intent.
	w.Header().Set("Cache-Control", "no-store")
	n, err := io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, maxLocalUploadBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "payload too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "upload failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"received_bytes": n, "time": time.Now().Format(time.DateTime)})
}

func validLocalTransferResult(result probe.LocalTransferResult) bool {
	for _, value := range []float64{
		result.DownloadMbps,
		result.UploadMbps,
		result.PayloadMB,
		result.DownloadMB,
		result.UploadMB,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return false
		}
	}
	return result.RoundTripLatencyMS >= 0 &&
		result.RTTMinMS >= 0 &&
		result.RTTAvgMS >= 0 &&
		result.RTTMaxMS >= 0 &&
		result.JitterMS >= 0 &&
		result.DurationMS >= 0
}

func parseMB(value string, fallback int) int {
	mb, err := strconv.Atoi(value)
	if err != nil || mb <= 0 {
		return fallback
	}
	if mb > 256 {
		return 256
	}
	return mb
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *Handler) handleTimeseries(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 300
	}
	writeJSON(w, http.StatusOK, h.service.GetTimeseries(limit))
}

func (h *Handler) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.service.GetMutableSettings())
	case http.MethodPost, http.MethodPut:
		var in probe.MutableSettings
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
			return
		}
		writeJSON(w, http.StatusOK, h.service.UpdateMutableSettings(in))
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (h *Handler) handleTrace(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.service.GetTraceTask())
	case http.MethodPost:
		host := r.URL.Query().Get("host")
		if host == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "host required"})
			return
		}
		hops, _ := strconv.Atoi(r.URL.Query().Get("hops"))
		writeJSON(w, http.StatusOK, h.service.StartTraceTask(host, hops))
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (h *Handler) handleTraceTask(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GetTraceTask())
}

func (h *Handler) handleRealtimeNetStats(w http.ResponseWriter, r *http.Request) {
	// Manual refresh: POST or ?force=1 always double-samples for usable bps.
	force := r.Method == http.MethodPost || r.URL.Query().Get("force") == "1" || r.URL.Query().Get("force") == "true"
	if force {
		writeJSON(w, http.StatusOK, h.service.ForceGetRealtimeNetStats())
		return
	}
	writeJSON(w, http.StatusOK, h.service.GetRealtimeNetStats())
}

func (h *Handler) handleHostPorts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, probe.CollectHostPorts(r.Context()))
}

func (h *Handler) handleEgressLookups(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		h.service.ClearPublicIPCache()
		writeJSON(w, http.StatusOK, h.service.RefreshEgressLookups(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, h.service.GetEgressLookups(r.Context()))
}

func (h *Handler) handleNotificationEvents(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	writeJSON(w, http.StatusOK, map[string]any{
		"events": h.service.GetNotificationEvents(since),
	})
}

func (h *Handler) handleBarkNotificationTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if err := h.service.TestBarkNotification(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

func (h *Handler) handlePushPlusNotificationTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if err := h.service.TestPushPlusNotification(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

func (h *Handler) handleLANDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// Async: return cached snapshot + scanning=true immediately.
		// Full discovery continues in background so reverse proxies cannot
		// cancel the scan via request context (hostproxy: context canceled).
		writeJSON(w, http.StatusOK, h.service.StartLANScan())
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.GetLANDevices())
}

func (h *Handler) handleLANDeviceMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var in probe.LANDeviceMetaUpdate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.UpdateLANDeviceMeta(in))
}

func (h *Handler) handleAppTraffic(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, probe.CollectAppTraffic())
}

func (h *Handler) handleAppTrafficHistory(w http.ResponseWriter, r *http.Request) {
	bridge := r.URL.Query().Get("bridge")
	if bridge == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bridge parameter required"})
		return
	}
	limit := 1440
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if d, ok := parseTrafficRange(r.URL.Query().Get("range")); ok {
		writeJSON(w, http.StatusOK, h.service.GetAppTrafficHistorySince(bridge, time.Now().Add(-d), limit))
		return
	}
	writeJSON(w, http.StatusOK, h.service.GetAppTrafficHistory(bridge, limit))
}

func parseTrafficRange(value string) (time.Duration, bool) {
	switch value {
	case "15m":
		return 15 * time.Minute, true
	case "1m":
		return time.Minute, true
	case "5m":
		return 5 * time.Minute, true
	case "1h":
		return time.Hour, true
	case "6h":
		return 6 * time.Hour, true
	case "24h":
		return 24 * time.Hour, true
	default:
		return 0, false
	}
}

func (h *Handler) handleAppTrafficTop(w http.ResponseWriter, r *http.Request) {
	limit := 15
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 30 {
		limit = 30
	}
	var since time.Time
	if d, ok := parseTrafficRange(r.URL.Query().Get("range")); ok {
		since = time.Now().Add(-d)
	}
	writeJSON(w, http.StatusOK, h.service.GetAppTrafficTop(since, limit))
}

func (h *Handler) handleAppTrafficLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	bridge := r.URL.Query().Get("bridge")
	if bridge == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bridge parameter required"})
		return
	}
	limit := 1440
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	var since time.Time
	if d, ok := parseTrafficRange(r.URL.Query().Get("range")); ok {
		since = time.Now().Add(-d)
	}
	result, ok := h.service.SampleAppTrafficBridge(bridge, since, limit)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "bridge not found"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handlePersistentTrafficBridges(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var body struct {
		Bridge  string `json:"bridge"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil || body.Bridge == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	settings := h.service.TogglePersistentTrafficBridge(body.Bridge, body.Enabled)
	writeJSON(w, http.StatusOK, settings)
}

func (h *Handler) handleContainers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	resp := h.service.ListContainers(r.Context())
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleContainerBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var body struct {
		Bridge string `json:"bridge"`
		Mode   string `json:"mode"` // currently only "internet" is supported
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&body); err != nil || body.Bridge == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	mode := strings.TrimSpace(body.Mode)
	if mode == "" {
		mode = "internet"
	}
	if mode != "internet" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "only mode=internet is supported"})
		return
	}
	if err := h.service.BlockApp(r.Context(), body.Bridge, mode); err != nil {
		logger.Error("block app %s mode %s: %v", body.Bridge, body.Mode, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleContainerUnblock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var body struct {
		Bridge string `json:"bridge"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&body); err != nil || body.Bridge == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	if err := h.service.UnblockApp(r.Context(), body.Bridge); err != nil {
		logger.Error("unblock app %s: %v", body.Bridge, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
