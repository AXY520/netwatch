package api

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"netwatch/internal/probe"
)

var downloadPayload = make([]byte, 1024*1024)

var maxLocalUploadBytes int64 = 128 * 1024 * 1024

type Handler struct {
	service *probe.Service
}

func NewHandler(service *probe.Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", h.handleHealth)
	mux.HandleFunc("/api/v1/summary", h.handleSummary)
	mux.HandleFunc("/api/v1/connectivity/websites", h.handleWebsiteConnectivity)
	mux.HandleFunc("/api/v1/connectivity/websites/run", h.handleWebsiteRefresh)
	mux.HandleFunc("/api/v1/network", h.handleNetworkInfo)
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
	mux.HandleFunc("/metrics", h.handleMetrics)
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
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   time.Now().Format(time.DateTime),
		"ready":  h.service.GetSummary().Ready,
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
	writeJSON(w, http.StatusOK, h.service.RefreshWebsiteConnectivity(r.Context()))
}

func (h *Handler) handleNetworkInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GetSummary().NetworkInfo)
}

func (h *Handler) handleNATRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.RefreshNAT(r.Context()))
}

func (h *Handler) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.Refresh(r.Context()))
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
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")

	secStr := r.URL.Query().Get("sec")
	if secStr != "" {
		sec, err := strconv.ParseFloat(secStr, 64)
		if err != nil || sec <= 0 {
			sec = 10
		}
		if sec > 60 {
			sec = 60
		}
		deadline := time.Now().Add(time.Duration(sec * float64(time.Second)))
		ctx := r.Context()
		flusher, canFlush := w.(http.Flusher)
		nextFlush := time.Now().Add(200 * time.Millisecond)
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if _, err := w.Write(downloadPayload); err != nil {
				return
			}
			if canFlush && time.Now().After(nextFlush) {
				flusher.Flush()
				nextFlush = time.Now().Add(200 * time.Millisecond)
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
	for remaining > 0 {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n := len(downloadPayload)
		if remaining < n {
			n = remaining
		}
		if _, err := w.Write(downloadPayload[:n]); err != nil {
			return
		}
		remaining -= n
	}
}

func (h *Handler) handleLocalUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
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
	if mb > 64 {
		return 64
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

func (h *Handler) handleRealtimeNetStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GetRealtimeNetStats())
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
		writeJSON(w, http.StatusOK, h.service.ScanLANDevices(r.Context()))
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
	limit := 5
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
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
