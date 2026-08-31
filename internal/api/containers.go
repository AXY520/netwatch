package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"netwatch/internal/logger"
)

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
		Bridge     string `json:"bridge"`
		AppID      string `json:"app_id"`
		InstanceID string `json:"instance_id"`
		Mode       string `json:"mode"` // currently only "internet" is supported
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&body); err != nil || (body.Bridge == "" && body.AppID == "") {
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
	if strings.TrimSpace(body.AppID) != "" {
		allowed := false
		if err := h.service.UpdateAppInstanceNetworkPolicy(r.Context(), body.AppID, body.InstanceID, nil, nil, &allowed, nil, nil); err != nil {
			logger.Error("block app %s: %v", body.AppID, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
		Bridge     string `json:"bridge"`
		AppID      string `json:"app_id"`
		InstanceID string `json:"instance_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&body); err != nil || (body.Bridge == "" && body.AppID == "") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	if strings.TrimSpace(body.AppID) != "" {
		allowed := true
		if err := h.service.UpdateAppInstanceNetworkPolicy(r.Context(), body.AppID, body.InstanceID, nil, nil, &allowed, nil, nil); err != nil {
			logger.Error("unblock app %s: %v", body.AppID, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if err := h.service.UnblockApp(r.Context(), body.Bridge); err != nil {
		logger.Error("unblock app %s: %v", body.Bridge, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
