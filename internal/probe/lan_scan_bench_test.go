package probe

import (
	"context"
	"testing"
	"time"
)

func TestScanLANDevicesFinishesQuickly(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	s := NewService(cfg)
	t.Cleanup(s.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	start := time.Now()
	snap := s.ScanLANDevices(ctx)
	elapsed := time.Since(start)
	t.Logf("sync scan elapsed=%s devices=%d online=%d networks=%d", elapsed, len(snap.Devices), snap.Online, len(snap.Networks))
	if elapsed > 10*time.Second {
		t.Fatalf("scan too slow: %v", elapsed)
	}
}

func TestStartLANScanReturnsImmediately(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	s := NewService(cfg)
	t.Cleanup(s.Close)

	start := time.Now()
	snap := s.StartLANScan()
	if time.Since(start) > 2*time.Second {
		t.Fatalf("StartLANScan should return immediately, took %v", time.Since(start))
	}
	if !snap.Scanning && snap.GeneratedAt == "" {
		// Either scanning or already has a snapshot is fine; just ensure no hang.
		t.Logf("snapshot: scanning=%v devices=%d", snap.Scanning, len(snap.Devices))
	}
	// Wait background scan to settle.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		cur := s.GetLANDevices()
		if !cur.Scanning {
			t.Logf("background scan finished devices=%d online=%d", len(cur.Devices), cur.Online)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("background scan still running after timeout")
}
