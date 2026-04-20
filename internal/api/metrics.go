package api

import (
	"fmt"
	"net/http"
	"strings"

	"netwatch/internal/probe"
)

func (h *Handler) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	summary := h.service.GetSummary()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	var b strings.Builder
	writeHelp := func(name, help, typ string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
	}

	writeHelp("netwatch_ready", "Whether the service has completed first probe", "gauge")
	fmt.Fprintf(&b, "netwatch_ready %d\n", boolInt(summary.Ready))

	writeHelp("netwatch_target_latency_ms", "Latency per target in ms", "gauge")
	writeHelp("netwatch_target_up", "Whether a target is up (1) or down (0)", "gauge")
	writeHelp("netwatch_target_loss_pct", "Packet loss percentage per target", "gauge")
	writeHelp("netwatch_tls_days_remaining", "Days remaining on TLS certificate", "gauge")
	for _, t := range summary.WebsiteConnectivity.Domestic {
		writeTargetMetrics(&b, "domestic", t)
	}
	for _, t := range summary.WebsiteConnectivity.Global {
		writeTargetMetrics(&b, "global", t)
	}

	writeHelp("netwatch_nat_reachable", "STUN reachability", "gauge")
	fmt.Fprintf(&b, "netwatch_nat_reachable %d\n", boolInt(summary.NetworkInfo.NAT.Reachable))

	_, _ = w.Write([]byte(b.String()))
}

func writeTargetMetrics(b *strings.Builder, scope string, t probe.TargetResult) {
	label := fmt.Sprintf(`scope="%s",target="%s"`, scope, escapeLabel(t.Name))
	fmt.Fprintf(b, "netwatch_target_latency_ms{%s} %d\n", label, t.LatencyMS)
	fmt.Fprintf(b, "netwatch_target_up{%s} %d\n", label, boolInt(t.Status == probe.StatusOK))
	fmt.Fprintf(b, "netwatch_target_loss_pct{%s} %.2f\n", label, t.PacketLossPct)
	if t.TLSDaysLeft > 0 {
		fmt.Fprintf(b, "netwatch_tls_days_remaining{%s} %d\n", label, t.TLSDaysLeft)
	}
}

func escapeLabel(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return v
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
