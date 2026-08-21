package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestServerPingAcceptsHTTPErrorResponse(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	node := publicBroadbandNode{PingURLs: []string{server.URL}}
	latency, jitter, err := serverPing(context.Background(), server.Client(), node)
	if err != nil {
		t.Fatalf("serverPing returned error for reachable 404 endpoint: %v", err)
	}
	if requests.Load() != 5 {
		t.Fatalf("requests = %d, want 5", requests.Load())
	}
	if latency < 0 || jitter < 0 {
		t.Fatalf("latency = %d, jitter = %d", latency, jitter)
	}
}

func TestChoosePublicBroadbandNodeRejectsRemovedNode(t *testing.T) {
	if _, err := choosePublicBroadbandNode(BroadbandServerRequest{NodeID: "3"}); err == nil {
		t.Fatal("removed broadband node 3 was accepted")
	}
}
