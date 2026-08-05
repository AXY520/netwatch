package probe

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"netwatch/internal/dockerlzc"
)

func TestSanitizeIconURLUsesBoxDomain(t *testing.T) {
	got := sanitizeIconURL("https://$boxdomain/sys/icons/com.example.app.png", "box.example.com")
	want := "https://box.example.com/sys/icons/com.example.app.png"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEnrichAppTrafficPointKeepsRawAndAddsSemanticCounters(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 10, 0, time.Local)
	got := enrichAppTrafficPoint(AppTrafficPoint{
		Timestamp: now.Add(-10 * time.Second).Format(time.DateTime),
		RxBytes:   123,
		TxBytes:   456,
	}, now)
	if got.UploadBytes != 123 || got.DownloadBytes != 456 {
		t.Fatalf("semantic counters = upload %d download %d", got.UploadBytes, got.DownloadBytes)
	}
	if got.CounterPerspective != appTrafficCounterPerspective || got.Source != appTrafficSource {
		t.Fatalf("metadata = perspective %q source %q", got.CounterPerspective, got.Source)
	}
	if got.AgeSeconds != 10 || got.Stale {
		t.Fatalf("freshness = age %d stale %v", got.AgeSeconds, got.Stale)
	}
}

func TestTopSinceSkipsDiscontinuity(t *testing.T) {
	store := &appTrafficHistoryStore{
		history: map[string][]AppTrafficPoint{
			"lzc-br-test": {
				{Timestamp: "2026-08-04 12:00:00", RxBytes: 100, TxBytes: 100},
				{Timestamp: "2026-08-04 12:00:10", RxBytes: 200, TxBytes: 300},
				{Timestamp: "2026-08-04 12:00:20", RxBytes: 5000, TxBytes: 6000, Discontinuity: true, DiscontinuityReason: "service_restart"},
				{Timestamp: "2026-08-04 12:00:30", RxBytes: 5100, TxBytes: 6200},
			},
		},
		path: filepath.Join(t.TempDir(), "history.json"),
	}
	items := store.topSince(time.Time{}, 10)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].UploadDelta != 200 || items[0].DownloadDelta != 400 || items[0].TotalDelta != 600 {
		t.Fatalf("deltas = %+v, discontinuity span must be skipped", items[0])
	}
}

func TestHistoryStoreLoadsLegacyPointsAndKeepsPersistencePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app_traffic_history.json")
	legacy := `{"lzc-br-test":[{"timestamp":"2026-08-04 12:00:00","rx_bytes":10,"tx_bytes":20}]}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	store := newAppTrafficHistoryStore(dir)
	if store.path != path || !store.loadedHistory {
		t.Fatalf("store path/history state = %q/%v", store.path, store.loadedHistory)
	}
	points := store.snapshot("lzc-br-test", 10)
	if len(points) != 1 || points[0].UploadBytes != 10 || points[0].DownloadBytes != 20 {
		t.Fatalf("legacy point was not enriched: %+v", points)
	}
}

func TestSanitizeIconURLKeepsAbsoluteURL(t *testing.T) {
	got := sanitizeIconURL("https://cdn.example.com/icon.png", "box.example.com")
	if got != "https://cdn.example.com/icon.png" {
		t.Fatalf("got %q", got)
	}
}

func TestSanitizeIconURLFallsBackToRelativePath(t *testing.T) {
	got := sanitizeIconURL("https://$boxdomain/sys/icons/com.example.app.png", "")
	if got != "/sys/icons/com.example.app.png" {
		t.Fatalf("got %q", got)
	}
}

func TestIsNetwatchApp(t *testing.T) {
	if !isNetwatchApp(dockerlzc.BridgeAppInfo{AppID: "cloud.lazycat.app.netwatch"}) {
		t.Fatal("app id should match")
	}
	if !isNetwatchApp(dockerlzc.BridgeAppInfo{Project: "cloudlazycatappnetwatch"}) {
		t.Fatal("compose project should match")
	}
	if isNetwatchApp(dockerlzc.BridgeAppInfo{Project: "cloudlazycatappphoto"}) {
		t.Fatal("unrelated project must not match")
	}
	if isNetwatchApp(dockerlzc.BridgeAppInfo{Project: "something-netwatch-ish"}) {
		t.Fatal("loose substring must not match")
	}
}

func TestFilterSupersededAppBridges(t *testing.T) {
	items := []AppBridgeStats{
		{Bridge: "lzc-br-old", AppID: "app.a", ContainerCount: 0},
		{Bridge: "lzc-br-current", AppID: "app.a", ContainerCount: 2},
		{Bridge: "lzc-br-b1", AppID: "app.b", ContainerCount: 1},
		{Bridge: "lzc-br-b2", AppID: "app.b", ContainerCount: 1},
		{Bridge: "lzc-br-unknown", ContainerCount: 0},
	}
	got := filterSupersededAppBridges(items)
	if len(got) != 4 {
		t.Fatalf("filtered bridges = %+v", got)
	}
	for _, item := range got {
		if item.Bridge == "lzc-br-old" {
			t.Fatalf("superseded bridge was retained: %+v", got)
		}
	}
}
