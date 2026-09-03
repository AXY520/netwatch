package probe

import (
	"context"
	"testing"
	"time"
)

func TestGetHostPortsSnapshotDoesNotCollectOnPageLoad(t *testing.T) {
	hostPortsCache.Lock()
	previousSnapshot := hostPortsCache.snapshot
	previousExpires := hostPortsCache.expires
	previousOK := hostPortsCache.ok
	previousLoading := hostPortsCache.loading
	hostPortsCache.snapshot = HostPortsSnapshot{GeneratedAt: "2026-09-01 12:00:00"}
	hostPortsCache.expires = time.Now().Add(-time.Hour)
	hostPortsCache.ok = true
	hostPortsCache.loading = nil
	hostPortsCache.Unlock()
	t.Cleanup(func() {
		hostPortsCache.Lock()
		hostPortsCache.snapshot = previousSnapshot
		hostPortsCache.expires = previousExpires
		hostPortsCache.ok = previousOK
		hostPortsCache.loading = previousLoading
		hostPortsCache.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := GetHostPortsSnapshot(ctx)
	if got.GeneratedAt != "2026-09-01 12:00:00" {
		t.Fatalf("page-load snapshot = %q", got.GeneratedAt)
	}
}

func TestParseProcAddressIPv4(t *testing.T) {
	addr, port, ok := parseProcAddress("0100007F:1F90", "ipv4")
	if !ok {
		t.Fatal("parse failed")
	}
	if addr != "127.0.0.1" || port != 8080 {
		t.Fatalf("got %s:%d", addr, port)
	}
}

func TestParseProcAddressIPv6(t *testing.T) {
	addr, port, ok := parseProcAddress("00000000000000000000000000000000:0050", "ipv6")
	if !ok {
		t.Fatal("parse failed")
	}
	if addr != "::" || port != 80 {
		t.Fatalf("got %s:%d", addr, port)
	}
}

func TestTCPStateListen(t *testing.T) {
	if got := tcpState("0A"); got != "LISTEN" {
		t.Fatalf("got %q", got)
	}
}
