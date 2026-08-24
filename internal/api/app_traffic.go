package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"netwatch/internal/probe"
)

func (h *Handler) handleAppTraffic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.AppTrafficSnapshot())
}

func (h *Handler) handleAppTrafficHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	appID := strings.TrimSpace(r.URL.Query().Get("app_id"))
	if appID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "app_id is required"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"app_id": appID, "samples": h.service.AppTrafficHistory(appID)})
}

func (h *Handler) handleAppTrafficLimit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var request struct {
		AppID        string `json:"app_id"`
		UploadKbps   int64  `json:"upload_kbps"`
		DownloadKbps int64  `json:"download_kbps"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	if err := h.service.SetAppTrafficLimit(r.Context(), request.AppID, probe.AppTrafficLimit{UploadKbps: request.UploadKbps, DownloadKbps: request.DownloadKbps}); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "app_id": strings.TrimSpace(request.AppID), "upload_kbps": request.UploadKbps, "download_kbps": request.DownloadKbps})
}
