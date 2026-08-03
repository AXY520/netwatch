package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"netwatch/internal/probe"
)

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
