package probe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func trafficBridge(appID, bridge string, upload, download uint64) AppBridgeStats {
	return AppBridgeStats{AppID: appID, AppTitle: "测试应用", Bridge: bridge, UploadBytes: upload, DownloadBytes: download, ContainerCount: 1, RunningCount: 1}
}

func trafficHostCgroup(appID, cgroup string, upload, download uint64) AppBridgeStats {
	return AppBridgeStats{
		AppID: appID, AppTitle: "Host 测试应用", Bridge: hostAppTarget(appID),
		NetworkMode: "host", CgroupPath: cgroup, UploadBytes: upload,
		DownloadBytes: download, ContainerCount: 1, RunningCount: 1,
	}
}

func TestAppTrafficStateUsesIndependentHostCgroupBaselines(t *testing.T) {
	state := newAppTrafficState(t.TempDir())
	start := time.Date(2026, 8, 28, 9, 0, 0, 0, time.Local)
	appID := "cloud.lazycat.app.host-multi"

	state.sample([]AppBridgeStats{
		trafficHostCgroup(appID, "system.slice/app-a.scope", 100, 200),
		trafficHostCgroup(appID, "system.slice/app-b.scope", 300, 500),
	}, start)
	// Container A restarted and its cgroup counter reset. Container B kept
	// running and advanced; its delta must not be discarded with A's reset.
	state.sample([]AppBridgeStats{
		trafficHostCgroup(appID, "system.slice/app-a.scope", 10, 20),
		trafficHostCgroup(appID, "system.slice/app-b.scope", 360, 580),
	}, start.Add(2*time.Second))

	app := state.overview(true).Apps[0]
	if app.TotalUpload != 460 || app.TotalDownload != 780 {
		t.Fatalf("host totals = upload %d download %d, want 460/780", app.TotalUpload, app.TotalDownload)
	}
	if app.TodayUpload != 60 || app.TodayDownload != 80 {
		t.Fatalf("host period totals = upload %d download %d, want 60/80", app.TodayUpload, app.TodayDownload)
	}
	if app.UploadBPS != 30 || app.DownloadBPS != 40 {
		t.Fatalf("host rates = upload %v download %v, want 30/40", app.UploadBPS, app.DownloadBPS)
	}
	if len(state.baselines) != 2 {
		t.Fatalf("baselines = %#v, want one baseline per cgroup", state.baselines)
	}
	if app.ContainerCount != 2 || app.RunningCount != 2 {
		t.Fatalf("container counts = %d/%d, want 2/2", app.ContainerCount, app.RunningCount)
	}
}

func TestAppTrafficBaselineKeyKeepsHostCgroupStableAcrossAppMetadataGaps(t *testing.T) {
	first := trafficHostCgroup("cloud.lazycat.app.host", "system.slice/app.scope", 100, 200)
	missing := first
	missing.AppID = ""
	if got := appTrafficBaselineKey(first); got != appTrafficBaselineKey(missing) {
		t.Fatalf("host baseline key changed across metadata gap: %q vs %q", appTrafficBaselineKey(first), appTrafficBaselineKey(missing))
	}
}

func TestAppTrafficBaselineKeyPreservesBridgePersistenceFormat(t *testing.T) {
	item := trafficBridge("cloud.lazycat.app.bridge", "lzc-br-legacy", 1, 2)
	if got := appTrafficBaselineKey(item); got != "lzc-br-legacy" {
		t.Fatalf("bridge baseline key=%q, want legacy bridge key", got)
	}
}

func TestAppTrafficStatePersistsIndependentHostCgroupBaselines(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 8, 28, 10, 0, 0, 0, time.Local)
	appID := "cloud.lazycat.app.host-persist"
	state := newAppTrafficState(dir)
	state.sample([]AppBridgeStats{
		trafficHostCgroup(appID, "system.slice/one.scope", 100, 200),
		trafficHostCgroup(appID, "system.slice/two.scope", 300, 400),
	}, start)
	state.flush()

	reloaded := newAppTrafficState(dir)
	reloaded.sample([]AppBridgeStats{
		trafficHostCgroup(appID, "system.slice/one.scope", 130, 250),
		trafficHostCgroup(appID, "system.slice/two.scope", 370, 490),
	}, start.Add(2*time.Second))
	app := reloaded.overview(true).Apps[0]
	if app.TotalUpload != 500 || app.TotalDownload != 740 {
		t.Fatalf("persisted host totals = %d/%d, want 500/740", app.TotalUpload, app.TotalDownload)
	}
	if app.TodayUpload != 100 || app.TodayDownload != 140 {
		t.Fatalf("persisted host period totals = %d/%d, want 100/140", app.TodayUpload, app.TodayDownload)
	}
}

func TestAppTrafficStateAggregatesBridgeAndHostDeltasIndependently(t *testing.T) {
	state := newAppTrafficState(t.TempDir())
	start := time.Date(2026, 8, 28, 11, 0, 0, 0, time.Local)
	appID := "cloud.lazycat.app.mixed"
	state.sample([]AppBridgeStats{
		trafficBridge(appID, "lzc-br-mixed", 1000, 2000),
		trafficHostCgroup(appID, "system.slice/mixed.scope", 100, 200),
	}, start)
	state.sample([]AppBridgeStats{
		trafficBridge(appID, "lzc-br-mixed", 1060, 2080),
		trafficHostCgroup(appID, "system.slice/mixed.scope", 130, 250),
	}, start.Add(2*time.Second))

	app := state.overview(true).Apps[0]
	if app.TodayUpload != 90 || app.TodayDownload != 130 {
		t.Fatalf("mixed deltas = %d/%d, want 90/130", app.TodayUpload, app.TodayDownload)
	}
	if len(state.baselines) != 2 {
		t.Fatalf("mixed baselines=%#v", state.baselines)
	}
}

func TestAppTrafficStateAccumulatesLifetimeTotalsAndKeepsBaselines(t *testing.T) {
	state := newAppTrafficState(t.TempDir())
	start := time.Date(2026, 8, 21, 9, 0, 0, 0, time.Local)
	appID := "cloud.lazycat.app.example"

	state.sample([]AppBridgeStats{trafficBridge(appID, "lzc-br-one", 100, 200)}, start)
	state.sample([]AppBridgeStats{
		trafficBridge(appID, "lzc-br-one", 160, 260),
		trafficBridge(appID, "lzc-br-two", 500, 700),
	}, start.Add(2*time.Second))
	state.sample([]AppBridgeStats{
		trafficBridge(appID, "lzc-br-one", 200, 300),
		trafficBridge(appID, "lzc-br-two", 550, 800),
	}, start.Add(4*time.Second))

	overview := state.overview(true)
	if len(overview.Apps) != 1 {
		t.Fatalf("apps = %#v", overview.Apps)
	}
	app := overview.Apps[0]
	// The first bridge seeds the lifetime total with its existing counters.
	// Later samples add only observed deltas; the newly attached bridge's
	// pre-existing 500/700 bytes are not attributed to the observer.
	if app.TotalUpload != 250 || app.TotalDownload != 400 {
		t.Fatalf("totals = upload %d download %d", app.TotalUpload, app.TotalDownload)
	}
	if app.TodayUpload != 150 || app.TodayDownload != 200 || app.MonthUpload != 150 || app.MonthDownload != 200 {
		t.Fatalf("period totals = %#v", app)
	}
	if app.UploadBPS != 45 || app.DownloadBPS != 70 {
		t.Fatalf("rates = upload %v download %v", app.UploadBPS, app.DownloadBPS)
	}
	if len(app.Bridges) != 2 || app.Bridges[0] != "lzc-br-one" || app.Bridges[1] != "lzc-br-two" {
		t.Fatalf("bridges = %#v", app.Bridges)
	}
}

func TestAppTrafficStateKeepsLifetimeTotalAfterBridgeReset(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 8, 21, 9, 0, 0, 0, time.Local)
	appID := "cloud.lazycat.app.example"
	state := newAppTrafficState(dir)
	state.sample([]AppBridgeStats{trafficBridge(appID, "lzc-br-one", 100, 200)}, start)
	state.sample([]AppBridgeStats{trafficBridge(appID, "lzc-br-one", 180, 260)}, start.Add(2*time.Second))
	state.flush()

	reloaded := newAppTrafficState(dir)
	// A reset after an app recreation must preserve the lifetime total and only
	// count the post-reset delta.
	reloaded.sample([]AppBridgeStats{trafficBridge(appID, "lzc-br-one", 10, 20)}, start.Add(4*time.Second))
	reloaded.sample([]AppBridgeStats{trafficBridge(appID, "lzc-br-one", 30, 50)}, start.Add(6*time.Second))
	app := reloaded.overview(true).Apps[0]
	if app.TotalUpload != 200 || app.TotalDownload != 290 {
		t.Fatalf("lifetime total after reset = upload %d download %d", app.TotalUpload, app.TotalDownload)
	}
	if app.TodayUpload != 100 || app.TodayDownload != 90 {
		t.Fatalf("counter reset changed period usage: upload=%d download=%d", app.TodayUpload, app.TodayDownload)
	}
}

func TestAppTrafficStateKeepsBaselineDuringMetadataGap(t *testing.T) {
	state := newAppTrafficState(t.TempDir())
	start := time.Date(2026, 8, 21, 9, 0, 0, 0, time.Local)
	appID := "cloud.lazycat.app.example"
	bridge := "lzc-br-one"
	state.sample([]AppBridgeStats{trafficBridge(appID, bridge, 100, 200)}, start)
	state.sample([]AppBridgeStats{trafficBridge(appID, bridge, 160, 260)}, start.Add(2*time.Second))
	// The bridge is still visible, but its app metadata was temporarily absent.
	state.sample([]AppBridgeStats{{Bridge: bridge, UploadBytes: 220, DownloadBytes: 320}}, start.Add(4*time.Second))
	state.sample([]AppBridgeStats{trafficBridge(appID, bridge, 280, 380)}, start.Add(6*time.Second))

	app := state.overview(true).Apps[0]
	if app.TotalUpload != 280 || app.TotalDownload != 380 {
		t.Fatalf("native cumulative totals = upload %d download %d", app.TotalUpload, app.TotalDownload)
	}
	if app.TodayUpload != 180 || app.TodayDownload != 180 {
		t.Fatalf("metadata gap lost period traffic: upload=%d download=%d", app.TodayUpload, app.TodayDownload)
	}
}

func TestAppTrafficOverviewFiltersInactiveAppsWithoutDroppingHistory(t *testing.T) {
	state := newAppTrafficState(t.TempDir())
	start := time.Date(2026, 8, 21, 9, 0, 0, 0, time.Local)
	activeID := "cloud.lazycat.app.active"
	stoppedID := "cloud.lazycat.app.stopped"

	state.sample([]AppBridgeStats{
		trafficBridge(activeID, "lzc-br-active", 100, 200),
		trafficBridge(stoppedID, "lzc-br-stopped", 300, 400),
	}, start)
	state.sample([]AppBridgeStats{
		trafficBridge(activeID, "lzc-br-active", 160, 260),
		{AppID: stoppedID, AppTitle: "已停止应用", Bridge: "lzc-br-stopped", ContainerCount: 1, RunningCount: 0, UploadBytes: 300, DownloadBytes: 400},
	}, start.Add(2*time.Second))

	allHistory := state.overview(true).Apps
	if len(allHistory) != 2 {
		t.Fatalf("persisted history apps = %d, want 2", len(allHistory))
	}
	visible := state.overviewForActiveApps(true, map[string]bool{activeID: true}).Apps
	if len(visible) != 1 || visible[0].AppID != activeID {
		t.Fatalf("visible apps = %#v, want only %q", visible, activeID)
	}
}

func TestAppTrafficStateTracksDailyAndMonthlyTotals(t *testing.T) {
	state := newAppTrafficState(t.TempDir())
	appID := "cloud.lazycat.app.example"
	dayOne := time.Date(2026, 8, 31, 23, 59, 55, 0, time.Local)
	state.sample([]AppBridgeStats{trafficBridge(appID, "lzc-br-one", 0, 0)}, dayOne)
	state.sample([]AppBridgeStats{trafficBridge(appID, "lzc-br-one", 100, 200)}, dayOne.Add(2*time.Second))
	dayTwo := dayOne.AddDate(0, 0, 1)
	state.sample([]AppBridgeStats{trafficBridge(appID, "lzc-br-one", 150, 260)}, dayTwo)
	app := state.overview(true).Apps[0]
	if app.TodayUpload != 50 || app.TodayDownload != 60 {
		t.Fatalf("today = %d/%d", app.TodayUpload, app.TodayDownload)
	}
	if app.MonthUpload != 50 || app.MonthDownload != 60 {
		t.Fatalf("month = %d/%d", app.MonthUpload, app.MonthDownload)
	}
	if app.TotalUpload != 150 || app.TotalDownload != 260 {
		t.Fatalf("total = %d/%d", app.TotalUpload, app.TotalDownload)
	}
}

func TestAppTrafficStateMigratesLegacyBridgeHistoryWhenAppIDIsKnown(t *testing.T) {
	dir := t.TempDir()
	bridge := "lzc-br-legacy"
	appID := "cloud.lazycat.app.example"
	points := map[string][]legacyAppTrafficPoint{
		bridge: {
			{Timestamp: "2026-08-20 23:59:00", RxBytes: 100, TxBytes: 200},
			{Timestamp: "2026-08-20 23:59:30", RxBytes: 160, TxBytes: 260},
			{Timestamp: "2026-08-21 00:00:30", RxBytes: 200, TxBytes: 340},
			// A reset must not turn the old absolute counter into new traffic.
			{Timestamp: "2026-08-21 00:01:00", RxBytes: 10, TxBytes: 20, Discontinuity: true},
			{Timestamp: "2026-08-21 00:02:00", RxBytes: 40, TxBytes: 70},
		},
	}
	body, err := json.Marshal(points)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app_traffic_history.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}

	state := newAppTrafficState(dir)
	if len(state.legacyBridges) != 1 {
		t.Fatalf("legacy bridges = %#v", state.legacyBridges)
	}
	now := time.Date(2026, 8, 21, 0, 3, 0, 0, time.Local)
	state.sample([]AppBridgeStats{trafficBridge(appID, bridge, 50, 80)}, now)

	app := state.overview(true).Apps[0]
	// 60/60, 40/80, and 30/50 after the reset.
	if app.TotalUpload != 200 || app.TotalDownload != 340 {
		t.Fatalf("migration totals = %d/%d", app.TotalUpload, app.TotalDownload)
	}
	if app.TodayUpload != 70 || app.TodayDownload != 130 {
		t.Fatalf("migration today = %d/%d", app.TodayUpload, app.TodayDownload)
	}
	// Five retained legacy timestamps plus the live sample that establishes the
	// post-migration bridge baseline.
	if len(state.history(appID)) != 6 {
		t.Fatalf("migration samples = %#v", state.history(appID))
	}
	if len(state.legacyBridges) != 0 {
		t.Fatalf("unmigrated legacy bridges = %#v", state.legacyBridges)
	}

	state.flush()
	reloaded := newAppTrafficState(dir)
	if len(reloaded.legacyBridges) != 0 || len(reloaded.history(appID)) != 6 {
		t.Fatalf("migration did not persist: legacy=%#v samples=%#v", reloaded.legacyBridges, reloaded.history(appID))
	}
}

func TestAppTrafficStateMergesDelayedLegacyMigrationBeforeLiveHistory(t *testing.T) {
	dir := t.TempDir()
	bridge := "lzc-br-legacy"
	appID := "cloud.lazycat.app.example"
	points := map[string][]legacyAppTrafficPoint{
		bridge: {
			{Timestamp: "2026-08-20 23:58:00", RxBytes: 100, TxBytes: 200},
			{Timestamp: "2026-08-20 23:59:00", RxBytes: 160, TxBytes: 260},
		},
	}
	body, err := json.Marshal(points)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app_traffic_history.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}

	state := newAppTrafficState(dir)
	// Metadata is absent at first, so live state starts before old history is
	// resolved. The second sample is deliberately after all legacy samples.
	state.sample([]AppBridgeStats{{Bridge: bridge, UploadBytes: 500, DownloadBytes: 700}}, time.Date(2026, 8, 21, 0, 1, 0, 0, time.Local))
	state.sample([]AppBridgeStats{trafficBridge(appID, bridge, 560, 780)}, time.Date(2026, 8, 21, 0, 2, 0, 0, time.Local))
	state.sample([]AppBridgeStats{trafficBridge(appID, bridge, 620, 860)}, time.Date(2026, 8, 21, 0, 3, 0, 0, time.Local))

	app := state.overview(true).Apps[0]
	if app.TotalUpload != 220 || app.TotalDownload != 340 {
		t.Fatalf("totals = %d/%d", app.TotalUpload, app.TotalDownload)
	}
	samples := state.history(appID)
	if len(samples) != 4 {
		t.Fatalf("samples = %#v", samples)
	}
	for index := 1; index < len(samples); index++ {
		if !trafficTimestampBefore(samples[index-1].Timestamp, samples[index].Timestamp) {
			t.Fatalf("samples out of order: %#v", samples)
		}
		if samples[index].UploadTotal < samples[index-1].UploadTotal || samples[index].DownloadTotal < samples[index-1].DownloadTotal {
			t.Fatalf("samples are not monotonic: %#v", samples)
		}
	}
	if samples[len(samples)-1].UploadTotal != app.TotalUpload || samples[len(samples)-1].DownloadTotal != app.TotalDownload {
		t.Fatalf("history tail = %#v, app = %#v", samples[len(samples)-1], app)
	}
}

func TestAppTrafficStateKeepsPeriodTotalsWithinLifetimeTotals(t *testing.T) {
	dir := t.TempDir()
	appID := "cloud.lazycat.app.example"
	body := []byte(`{"apps":{"` + appID + `":{"app_id":"` + appID + `","total_upload":8940000000,"total_download":597100000,"daily":[{"date":"2026-08-23","upload_bytes":14000000000,"download_bytes":744300000},{"date":"2026-08-22","upload_bytes":7600000000,"download_bytes":2587000000}]}}}`)
	if err := os.WriteFile(filepath.Join(dir, "app_traffic_history.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}

	state := newAppTrafficState(dir)
	app := state.apps[appID].AppTrafficUsage
	if app.TotalUpload != 21600000000 || app.TotalDownload != 3331300000 {
		t.Fatalf("normalized lifetime total = %d/%d", app.TotalUpload, app.TotalDownload)
	}
}

func TestAppTrafficStateShowsExistingBridgeCountersOnFirstObservation(t *testing.T) {
	state := newAppTrafficState(t.TempDir())
	appID := "cloud.lazycat.app.example"
	state.sample([]AppBridgeStats{
		trafficBridge(appID, "lzc-br-one", 4_000_000, 8_000_000),
		trafficBridge(appID, "lzc-br-two", 1_500_000, 2_500_000),
	}, time.Date(2026, 8, 21, 9, 0, 0, 0, time.Local))

	app := state.overview(true).Apps[0]
	if app.TotalUpload != 5_500_000 || app.TotalDownload != 10_500_000 {
		t.Fatalf("existing bridge counters were not exposed: upload=%d download=%d", app.TotalUpload, app.TotalDownload)
	}
	if app.TodayUpload != 0 || app.TodayDownload != 0 {
		t.Fatalf("first observation must not invent period usage: upload=%d download=%d", app.TodayUpload, app.TodayDownload)
	}
}

func TestAppTrafficStateUsesPrimaryApplicationStartTime(t *testing.T) {
	state := newAppTrafficState(t.TempDir())
	start := time.Date(2026, 8, 21, 9, 0, 0, 0, time.Local)
	appID := "cloud.lazycat.app.example"
	state.sample([]AppBridgeStats{
		{
			AppID:          appID,
			AppTitle:       "测试应用",
			Bridge:         "lzc-br-one",
			StatusText:     "今天 08:30",
			CreatedAt:      1_755_744_600,
			UploadBytes:    100,
			DownloadBytes:  200,
			ContainerCount: 1,
			RunningCount:   1,
		},
		{
			AppID:          appID,
			Bridge:         "lzc-br-two",
			StatusText:     "今天 09:00",
			CreatedAt:      1_755_746_400,
			UploadBytes:    300,
			DownloadBytes:  400,
			ContainerCount: 1,
			RunningCount:   1,
		},
	}, start)

	app := state.overview(true).Apps[0]
	if app.StatusText != "今天 09:00" {
		t.Fatalf("status text = %q, want primary app container start time", app.StatusText)
	}
	if app.CreatedAt != 1_755_746_400 {
		t.Fatalf("created at = %d, want primary app container start timestamp", app.CreatedAt)
	}
}

func TestAppTrafficStatePreservesUnresolvedLegacyBridgeHistory(t *testing.T) {
	dir := t.TempDir()
	points := map[string][]legacyAppTrafficPoint{
		"lzc-br-unresolved": {
			{Timestamp: "2026-08-20 23:58:00", RxBytes: 100, TxBytes: 200},
			{Timestamp: "2026-08-20 23:59:00", RxBytes: 160, TxBytes: 260},
		},
	}
	body, err := json.Marshal(points)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app_traffic_history.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}

	state := newAppTrafficState(dir)
	state.sample([]AppBridgeStats{trafficBridge("cloud.lazycat.app.other", "lzc-br-other", 10, 20)}, time.Date(2026, 8, 21, 0, 0, 0, 0, time.Local))
	state.flush()

	reloaded := newAppTrafficState(dir)
	got := reloaded.legacyBridges["lzc-br-unresolved"]
	if len(got) != len(points["lzc-br-unresolved"]) {
		t.Fatalf("unresolved legacy history was lost: %#v", reloaded.legacyBridges)
	}
}
