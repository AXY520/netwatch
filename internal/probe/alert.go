package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"netwatch/internal/logger"
)

type alertState struct {
	mu         sync.Mutex
	egressV4   string
	egressV6   string
	natType    string
	webhookURL string
	client     *http.Client
}

func newAlertState() *alertState {
	return &alertState{
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (a *alertState) setWebhook(url string) {
	a.mu.Lock()
	a.webhookURL = url
	a.mu.Unlock()
}

func (a *alertState) check(summary Summary) {
	a.mu.Lock()
	url := a.webhookURL
	prevV4, prevV6, prevNAT := a.egressV4, a.egressV6, a.natType
	curV4 := summary.NetworkInfo.EgressIPv4
	curV6 := summary.NetworkInfo.EgressIPv6
	curNAT := summary.NetworkInfo.NAT.Type

	var events []map[string]string
	if prevV4 != "" && curV4 != "" && prevV4 != curV4 {
		events = append(events, map[string]string{"kind": "egress_ipv4_changed", "from": prevV4, "to": curV4})
	}
	if prevV6 != "" && curV6 != "" && prevV6 != curV6 {
		events = append(events, map[string]string{"kind": "egress_ipv6_changed", "from": prevV6, "to": curV6})
	}
	if prevNAT != "" && curNAT != "" && prevNAT != curNAT {
		events = append(events, map[string]string{"kind": "nat_type_changed", "from": prevNAT, "to": curNAT})
	}

	if curV4 != "" {
		a.egressV4 = curV4
	}
	if curV6 != "" {
		a.egressV6 = curV6
	}
	if curNAT != "" {
		a.natType = curNAT
	}
	a.mu.Unlock()

	if url == "" || len(events) == 0 {
		return
	}
	for _, ev := range events {
		ev["timestamp"] = localTimestamp()
		go a.post(url, ev)
	}
}

func (a *alertState) post(url string, payload map[string]string) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		logger.Warn("alert webhook failed: %v", err)
		return
	}
	resp.Body.Close()
}
