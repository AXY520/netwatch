package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"netwatch/internal/probe"
)

var downloadPayload = make([]byte, 1024*1024)

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
	mux.HandleFunc("/api/v1/settings/refresh-interval", h.handleRefreshInterval)
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
	mux.HandleFunc("/api/v1/auto-refresh", h.handleAutoRefresh)
	mux.HandleFunc("/api/v1/network/realtime", h.handleRealtimeNetStats)
	mux.HandleFunc("/api/v1/network/egress-lookups", h.handleEgressLookups)
	mux.HandleFunc("/metrics", h.handleMetrics)
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

func (h *Handler) handleRefreshInterval(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	seconds, err := strconv.Atoi(r.URL.Query().Get("seconds"))
	if err != nil || seconds <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid seconds"})
		return
	}

	writeJSON(w, http.StatusOK, h.service.UpdateRefreshInterval(seconds))
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
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
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
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if _, err := w.Write(downloadPayload); err != nil {
				return
			}
		}
		return
	}

	mb := parseMB(r.URL.Query().Get("mb"), 8)
	remaining := mb * 1024 * 1024
	for remaining > 0 {
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
	n, _ := io.Copy(io.Discard, r.Body)
	writeJSON(w, http.StatusOK, map[string]any{"received_bytes": n, "time": time.Now().Format(time.DateTime)})
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
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
			return
		}
		writeJSON(w, http.StatusOK, h.service.UpdateMutableSettings(in))
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (h *Handler) handleTrace(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Query().Get("host")
	if host == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "host required"})
		return
	}
	hops, _ := strconv.Atoi(r.URL.Query().Get("hops"))
	if r.Method == http.MethodPost {
		writeJSON(w, http.StatusOK, h.service.StartTraceTask(host, hops))
		return
	}
	writeJSON(w, http.StatusOK, h.service.GetTraceTask())
}

func (h *Handler) handleTraceTask(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GetTraceTask())
}

func (h *Handler) handleAutoRefresh(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": h.service.GetAutoRefresh()})
	case http.MethodPost:
		enabled := r.URL.Query().Get("enabled") == "true"
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": h.service.SetAutoRefresh(enabled)})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (h *Handler) handleRealtimeNetStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GetRealtimeNetStats())
}

func (h *Handler) handleEgressLookups(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		writeJSON(w, http.StatusOK, h.service.RefreshEgressLookups(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, h.service.GetEgressLookups(r.Context()))
}
