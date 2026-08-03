package api

import (
	"net/http"
	"time"
)

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
