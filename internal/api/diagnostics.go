package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"netwatch/internal/probe"
)

func (h *Handler) handleTimeseries(w http.ResponseWriter, r *http.Request) {
	limit := clampQueryLimit(r.URL.Query().Get("limit"), 300, 2000)
	writeJSON(w, http.StatusOK, h.service.GetTimeseries(limit))
}

func (h *Handler) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.service.GetMutableSettings())
	case http.MethodPost, http.MethodPut:
		// Partial update: decode onto the current settings so omitted JSON keys
		// keep their existing values (Go encoding/json leaves missing fields alone).
		// This prevents main-page saves from zeroing LAN-only knobs like auto-remove.
		in := h.service.GetMutableSettings()
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

func (h *Handler) handleTraceCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.CancelTraceTask())
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
