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
	t.Logf("scan elapsed=%s devices=%d online=%d networks=%d", elapsed, len(snap.Devices), snap.Online, len(snap.Networks))
	if elapsed > 10*time.Second {
		t.Fatalf("scan too slow: %v", elapsed)
	}
}
