package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

type observationFreshness struct {
	SampledAt  string `json:"sampled_at"`
	AgeSeconds int64  `json:"age_seconds"`
	Stale      bool   `json:"stale"`
}

func writeObservationJSON(w http.ResponseWriter, status int, payload any, sampledAt string, staleAfter time.Duration) {
	merged, err := marshalObservationJSON(payload, sampledAt, staleAfter)
	if err != nil {
		writeJSON(w, status, payload)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(merged, '\n'))
}

func marshalObservationJSON(payload any, sampledAt string, staleAfter time.Duration) ([]byte, error) {
	freshness := observationFreshness{SampledAt: sampledAt, Stale: sampledAt == ""}
	if sampled, ok := parseObservationTime(sampledAt); ok {
		age := time.Since(sampled)
		if age < 0 {
			age = 0
		}
		freshness.AgeSeconds = int64(age / time.Second)
		freshness.Stale = staleAfter > 0 && age > staleAfter
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(body) < 2 || body[0] != '{' || body[len(body)-1] != '}' {
		return nil, errors.New("observation freshness requires a JSON object")
	}
	extra, err := json.Marshal(freshness)
	if err != nil {
		return nil, err
	}
	merged := make([]byte, 0, len(body)+len(extra))
	merged = append(merged, body[:len(body)-1]...)
	if len(body) > 2 {
		merged = append(merged, ',')
	}
	merged = append(merged, extra[1:]...)
	return merged, nil
}

func parseObservationTime(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, time.DateTime} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func observationStaleAfter(refreshSeconds int64) time.Duration {
	if refreshSeconds <= 0 {
		return 2 * time.Minute
	}
	threshold := 2 * time.Duration(refreshSeconds) * time.Second
	if threshold < time.Minute {
		return time.Minute
	}
	return threshold
}

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
	summary := h.service.GetSummary()
	writeObservationJSON(w, http.StatusOK, summary, summary.GeneratedAt, observationStaleAfter(summary.RefreshIntervalSec))
}

func (h *Handler) handleWebsiteConnectivity(w http.ResponseWriter, _ *http.Request) {
	summary := h.service.GetSummary()
	website := summary.WebsiteConnectivity
	writeObservationJSON(w, http.StatusOK, website, website.GeneratedAt, observationStaleAfter(summary.RefreshIntervalSec))
}

func (h *Handler) handleWebsiteRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	result := h.service.RefreshWebsiteConnectivity(h.service.LifecycleContext())
	writeObservationJSON(w, http.StatusOK, result, result.GeneratedAt, observationStaleAfter(h.service.GetSummary().RefreshIntervalSec))
}

func (h *Handler) handleNetworkInfo(w http.ResponseWriter, _ *http.Request) {
	summary := h.service.GetSummary()
	info := summary.NetworkInfo
	writeObservationJSON(w, http.StatusOK, info, info.GeneratedAt, observationStaleAfter(summary.RefreshIntervalSec))
}

func (h *Handler) handleNetworkInterfacesRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	result := h.service.RefreshNetworkInfo(r.Context())
	writeObservationJSON(w, http.StatusOK, result, result.GeneratedAt, observationStaleAfter(h.service.GetSummary().RefreshIntervalSec))
}

func (h *Handler) handleNATRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	result := h.service.RefreshNAT(h.service.LifecycleContext())
	writeObservationJSON(w, http.StatusOK, result, result.GeneratedAt, observationStaleAfter(h.service.GetSummary().RefreshIntervalSec))
}

func (h *Handler) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	result := h.service.Refresh(h.service.LifecycleContext())
	writeObservationJSON(w, http.StatusOK, result, result.GeneratedAt, observationStaleAfter(result.RefreshIntervalSec))
}
