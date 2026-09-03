package probe

import (
	"testing"
	"time"
)

func TestSampleAndSnapshotPrimesOnColdStart(t *testing.T) {
	tracker := newNICStatsTracker()
	if tracker.primed() {
		t.Fatal("new tracker should not be primed")
	}
	start := time.Now()
	snap := tracker.sampleAndSnapshot()
	elapsed := time.Since(start)
	if !tracker.primed() {
		t.Fatal("sampleAndSnapshot should prime tracker")
	}
	if elapsed < 250*time.Millisecond {
		t.Fatalf("expected cold-start double sample delay, got %v", elapsed)
	}
	// Second call should be fast (no forced second sleep when already primed).
	start = time.Now()
	_ = tracker.sampleAndSnapshot()
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("warm sampleAndSnapshot should not sleep long, took %v", time.Since(start))
	}
	if len(snap.NICs) == 0 {
		// Environment may have no monitored NICs; still valid as long as we primed.
		t.Log("no monitored nics in test env")
	}
}

func TestForceSampleAndSnapshotAlwaysDoubleSamples(t *testing.T) {
	tracker := newNICStatsTracker()
	// Prime first.
	_ = tracker.sampleAndSnapshot()
	start := time.Now()
	_ = tracker.forceSampleAndSnapshot()
	if time.Since(start) < 250*time.Millisecond {
		t.Fatalf("forceSampleAndSnapshot should always double-sample, took %v", time.Since(start))
	}
}

func TestGetRealtimeNetStatsOnlyReadsBackgroundSnapshot(t *testing.T) {
	tracker := newNICStatsTracker()
	tracker.sample()
	before := tracker.sampleCount
	service := &Service{nicStats: tracker}

	_ = service.GetRealtimeNetStats()
	if tracker.sampleCount != before {
		t.Fatalf("GET sampled counters: count=%d, want %d", tracker.sampleCount, before)
	}
}
