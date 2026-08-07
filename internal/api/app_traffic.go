package api

import (
	"net/http"

	"netwatch/internal/probe"
)

// handleAppTraffic serves the lightweight current bridge counters used by the
// dashboard. Historical analysis endpoints are intentionally not exposed.
func (h *Handler) handleAppTraffic(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, probe.CollectAppTraffic())
}
