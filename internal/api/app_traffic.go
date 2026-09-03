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
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if appID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "app_id is required"})
		return
	}
	samples, err := h.service.AppInstanceTrafficHistory(appID, instanceID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"app_id": appID, "instance_id": instanceID, "samples": samples})
}

func (h *Handler) handleAppTrafficConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	appID := strings.TrimSpace(r.URL.Query().Get("app_id"))
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if appID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "app_id is required"})
		return
	}
	snapshot, err := h.service.AppInstanceConnections(r.Context(), appID, instanceID, clampQueryLimit(r.URL.Query().Get("limit"), 300, 300))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (h *Handler) handleAppTrafficLimit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var request struct {
		AppID        string `json:"app_id"`
		InstanceID   string `json:"instance_id"`
		UploadKbps   int64  `json:"upload_kbps"`
		DownloadKbps int64  `json:"download_kbps"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	if err := h.service.SetAppInstanceTrafficLimit(r.Context(), request.AppID, request.InstanceID, probe.AppTrafficLimit{UploadKbps: request.UploadKbps, DownloadKbps: request.DownloadKbps}); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "app_id": strings.TrimSpace(request.AppID), "instance_id": strings.TrimSpace(request.InstanceID), "upload_kbps": request.UploadKbps, "download_kbps": request.DownloadKbps})
}

func (h *Handler) handleAppNetworkPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var request struct {
		AppID           string                  `json:"app_id"`
		InstanceID      string                  `json:"instance_id"`
		UploadKbps      *int64                  `json:"upload_kbps"`
		DownloadKbps    *int64                  `json:"download_kbps"`
		InternetAllowed *bool                   `json:"internet_allowed"`
		ProxyEnabled    *bool                   `json:"proxy_enabled"`
		ProxySettings   *probe.AppProxySettings `json:"proxy_settings"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	if strings.TrimSpace(request.AppID) == "" || (request.UploadKbps == nil && request.DownloadKbps == nil && request.InternetAllowed == nil && request.ProxyEnabled == nil && request.ProxySettings == nil) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "app_id and at least one policy field are required"})
		return
	}
	if err := h.service.UpdateAppInstanceNetworkPolicy(r.Context(), request.AppID, request.InstanceID, request.UploadKbps, request.DownloadKbps, request.InternetAllowed, request.ProxyEnabled, request.ProxySettings); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "app_id": strings.TrimSpace(request.AppID), "instance_id": strings.TrimSpace(request.InstanceID)})
}

func (h *Handler) handleAppProxySettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.service.GetAppProxySettings())
	case http.MethodPost:
		var request probe.AppProxySettings
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
			return
		}
		settings, err := h.service.SetAppProxySettings(r.Context(), request)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, settings)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}
