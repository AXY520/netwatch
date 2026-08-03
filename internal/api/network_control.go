package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"netwatch/internal/probe"
)

func (h *Handler) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.Capabilities())
}

func (h *Handler) handleNetworkMutationAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": h.service.NetworkMutationAudit(limit)})
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
	ctx, cancel := context.WithTimeout(h.service.LifecycleContext(), 75*time.Second)
	defer cancel()
	result := h.service.ApplyNetworkConfig(ctx, req)
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
	ctx, cancel := context.WithTimeout(h.service.LifecycleContext(), 45*time.Second)
	defer cancel()
	result := h.service.RollbackNetworkConfig(ctx, req.ID)
	status := http.StatusOK
	if !result.OK {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, result)
}

func (h *Handler) handleHostDNS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	device := strings.TrimSpace(r.URL.Query().Get("device"))
	writeJSON(w, http.StatusOK, h.service.GetHostDNS(r.Context(), device))
}

func (h *Handler) handleHostDNSPending(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.GetHostDNSPending())
}

func (h *Handler) handleHostDNSApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req probe.HostDNSApplyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	ctx, cancel := context.WithTimeout(h.service.LifecycleContext(), 60*time.Second)
	defer cancel()
	result := h.service.ApplyHostDNS(ctx, req)
	if !result.OK {
		writeJSON(w, http.StatusBadRequest, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleHostDNSConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&req)
	result := h.service.ConfirmHostDNS(req.ID)
	if !result.OK {
		writeJSON(w, http.StatusBadRequest, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleHostDNSRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&req)
	ctx, cancel := context.WithTimeout(h.service.LifecycleContext(), 45*time.Second)
	defer cancel()
	result := h.service.RollbackHostDNS(ctx, req.ID)
	if !result.OK {
		writeJSON(w, http.StatusBadRequest, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
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
