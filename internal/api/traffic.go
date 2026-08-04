package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"netwatch/internal/probe"
)

func (h *Handler) handleAppNetworkDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	d, ok := parseTrafficRange(r.URL.Query().Get("range"))
	if !ok {
		d = 24 * time.Hour
	}
	limit := clampQueryLimit(r.URL.Query().Get("limit"), 240, 500)
	result, err := h.service.GetAppNetworkDetail(r.Context(), r.URL.Query().Get("bridge"), r.URL.Query().Get("app_id"), r.URL.Query().Get("project"), time.Now().Add(-d), limit)
	if errors.Is(err, probe.ErrAppNetworkNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleAppConnectionsSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	bridge, appID, project := r.URL.Query().Get("bridge"), r.URL.Query().Get("app_id"), r.URL.Query().Get("project")
	if bridge == "" && appID == "" && project == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "application identifier required"})
		return
	}
	if bridge != "" && !strings.HasPrefix(bridge, "lzc-br-") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid bridge"})
		return
	}
	limit := clampQueryLimit(r.URL.Query().Get("limit"), 200, 200)
	reveal := r.URL.Query().Get("reveal") == "true" || r.URL.Query().Get("reveal") == "1"
	writeJSON(w, http.StatusOK, probe.CollectAppConnectionSnapshot(r.Context(), bridge, appID, project, limit, reveal))
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
	limit := clampQueryLimit(r.URL.Query().Get("limit"), 1440, 1440)
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
	limit := clampQueryLimit(r.URL.Query().Get("limit"), 15, 30)
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
	limit := clampQueryLimit(r.URL.Query().Get("limit"), 1440, 1440)
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
