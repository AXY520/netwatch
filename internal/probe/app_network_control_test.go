package probe

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type recordingAppNetworkDriver struct {
	capabilities AppNetworkCapabilities
	limits       []AppTrafficLimit
}

func (d *recordingAppNetworkDriver) Capabilities() AppNetworkCapabilities {
	return d.capabilities
}

func (d *recordingAppNetworkDriver) ApplyLimit(_ context.Context, _ AppNetworkTarget, limit AppTrafficLimit) error {
	d.limits = append(d.limits, limit)
	return nil
}

func TestAppNetworkTargetFromStats(t *testing.T) {
	bridge, ok := appNetworkTargetFromStats(AppBridgeStats{AppID: "app.example", Bridge: "lzc-br-abc", NetworkMode: "bridge", Source: "linux_bridge_sysfs"})
	if !ok || bridge.Kind != AppNetworkTargetBridge || bridge.Interface != "lzc-br-abc" {
		t.Fatalf("bridge target=%#v ok=%v", bridge, ok)
	}
	host, ok := appNetworkTargetFromStats(AppBridgeStats{AppID: "app.example", Bridge: "host-app:app.example", NetworkMode: "host", ControlTarget: "host-app:app.example", CgroupPath: "demo.slice", Diagnostic: "attach failed"})
	if !ok || host.Kind != AppNetworkTargetCgroup || host.ID != "host-app:app.example" || host.CgroupPath != "demo.slice" || host.Diagnostic != "attach failed" {
		t.Fatalf("host target=%#v ok=%v", host, ok)
	}
}

func TestAggregateAppNetworkCapabilitiesRequiresEveryTarget(t *testing.T) {
	got := aggregateAppNetworkCapabilities([]AppNetworkTargetStatus{
		{Capabilities: AppNetworkCapabilities{Accounting: true, UploadLimit: true, DownloadLimit: true, InternetControl: true}},
		{Capabilities: AppNetworkCapabilities{Accounting: true, InternetControl: true}},
	})
	if !got.Accounting || got.UploadLimit || got.DownloadLimit || !got.InternetControl {
		t.Fatalf("capabilities=%#v", got)
	}
}

func TestAppNetworkTargetsForAppDeduplicatesTargets(t *testing.T) {
	targets := appNetworkTargetsForApp([]AppBridgeStats{
		{AppID: "app.example", Bridge: "lzc-br-abc", NetworkMode: "bridge"},
		{AppID: "app.example", Bridge: "lzc-br-abc", NetworkMode: "bridge"},
		{AppID: "other", Bridge: "lzc-br-other", NetworkMode: "bridge"},
	}, "app.example")
	if len(targets) != 1 || targets[0].ID != "lzc-br-abc" {
		t.Fatalf("targets=%#v", targets)
	}
}

func TestAppNetworkPolicyStatusReportsUnavailableHostAccounting(t *testing.T) {
	service := &Service{}
	service.appNetworkController = newAppNetworkController(service, nil)
	app := AppTrafficUsage{
		AppID: "app.example",
		NetworkTargets: []AppNetworkTarget{{
			ID: "host-app:app.example", Kind: AppNetworkTargetCgroup, AppID: "app.example",
			NetworkMode: "host", AccountingSource: "cgroup_skb_ebpf_unavailable", Diagnostic: "attach failed",
		}},
	}
	status := service.appNetworkPolicyStatus(app, nil)
	if status.Capabilities.Accounting || len(status.Targets) != 1 || status.Targets[0].Diagnostic != "attach failed" {
		t.Fatalf("status=%#v", status)
	}
}

func TestAppNetworkPolicyStatusHandlesMissingController(t *testing.T) {
	service := &Service{}
	status := service.appNetworkPolicyStatus(AppTrafficUsage{
		AppID: "app.example",
		NetworkTargets: []AppNetworkTarget{{
			ID: "lzc-br-example", Kind: AppNetworkTargetBridge, AppID: "app.example", Interface: "lzc-br-example",
		}},
	}, nil)
	if len(status.Targets) != 1 || status.Targets[0].Diagnostic == "" || status.Capabilities.Accounting {
		t.Fatalf("status=%#v", status)
	}
}

func TestAppNetworkPolicyStatusReportsTrafficLimitDrift(t *testing.T) {
	limiter := newAppTrafficLimiter()
	limiter.runtime["lzc-br-example"] = appTrafficLimitRuntime{
		Desired: AppTrafficLimit{UploadKbps: 1000}, InSync: false,
		Diagnostic: "上传 police filter 缺失", CheckedAt: time.Now(),
	}
	service := &Service{appTrafficLimiter: limiter}
	service.appNetworkController = newAppNetworkController(service, limiter)
	app := AppTrafficUsage{
		AppID: "app.example", Limit: AppTrafficLimit{UploadKbps: 1000},
		NetworkTargets: []AppNetworkTarget{{ID: "lzc-br-example", Kind: AppNetworkTargetBridge, AppID: "app.example", Interface: "lzc-br-example"}},
	}
	status := service.appNetworkPolicyStatus(app, nil)
	if status.LimitInSync || status.LimitState != "unsupported" || len(status.Targets) != 1 || status.Targets[0].LimitInSync {
		t.Fatalf("status=%#v", status)
	}
	if status.Diagnostic != "上传 police filter 缺失" {
		t.Fatalf("diagnostic=%q", status.Diagnostic)
	}
}

func TestLimitOnlyUpdatePreservesPartialInternetState(t *testing.T) {
	driver := &recordingAppNetworkDriver{capabilities: AppNetworkCapabilities{
		Accounting: true, UploadLimit: true, DownloadLimit: true, InternetControl: true,
	}}
	service := &Service{
		cfg:               Config{DataDir: t.TempDir()},
		settings:          newSettingsStore(DefaultMutableSettings()),
		containers:        newContainerControlState(),
		appTraffic:        newAppTrafficState(t.TempDir()),
		appTrafficLimiter: newAppTrafficLimiter(),
	}
	service.appNetworkController = &appNetworkController{drivers: map[AppNetworkTargetKind]appNetworkTargetDriver{
		AppNetworkTargetBridge: driver,
	}}
	service.containers.setBlocked("lzc-br-one", "internet")
	targets := []AppNetworkTarget{
		{ID: "lzc-br-one", Kind: AppNetworkTargetBridge, AppID: "app.example", Interface: "lzc-br-one"},
		{ID: "lzc-br-two", Kind: AppNetworkTargetBridge, AppID: "app.example", Interface: "lzc-br-two"},
	}
	upload := int64(1000)
	if err := service.applyAppNetworkPolicyUpdate(context.Background(), "app.example", targets, appNetworkPolicyUpdate{UploadKbps: &upload}); err != nil {
		t.Fatal(err)
	}
	if len(driver.limits) != 2 {
		t.Fatalf("limit calls=%d, want 2", len(driver.limits))
	}
	blocked := service.containers.snapshotBlocked()
	if blocked["lzc-br-one"] != "internet" || blocked["lzc-br-two"] != "" {
		t.Fatalf("blocked state changed: %#v", blocked)
	}
}

func TestMixedLimitUsesOneSharedHostDriver(t *testing.T) {
	bridgeDriver := &recordingAppNetworkDriver{capabilities: AppNetworkCapabilities{UploadLimit: true, DownloadLimit: true}}
	hostDriver := &recordingAppNetworkDriver{capabilities: AppNetworkCapabilities{UploadLimit: true, DownloadLimit: true}}
	settings := newSettingsStore(DefaultMutableSettings())
	settings.apply(MutableSettings{HostNetworkExperimentalEnabled: true})
	service := &Service{
		settings:          settings,
		containers:        newContainerControlState(),
		appTraffic:        newAppTrafficState(t.TempDir()),
		appTrafficLimiter: newAppTrafficLimiter(),
	}
	service.appNetworkController = &appNetworkController{drivers: map[AppNetworkTargetKind]appNetworkTargetDriver{
		AppNetworkTargetBridge: bridgeDriver,
		AppNetworkTargetCgroup: hostDriver,
	}}
	targets := []AppNetworkTarget{
		{ID: "lzc-br-mixed", Kind: AppNetworkTargetBridge, AppID: "app.example", Interface: "lzc-br-mixed"},
		{ID: "host-app:app.example", Kind: AppNetworkTargetCgroup, AppID: "app.example"},
	}
	upload := int64(1000)
	if err := service.applyAppNetworkPolicyUpdate(context.Background(), "app.example", targets, appNetworkPolicyUpdate{UploadKbps: &upload}); err != nil {
		t.Fatal(err)
	}
	if len(bridgeDriver.limits) != 0 || len(hostDriver.limits) != 1 {
		t.Fatalf("bridge calls=%v host calls=%v", bridgeDriver.limits, hostDriver.limits)
	}
}

func TestCombinedPolicyRollsBackLimitWhenInternetPersistenceFails(t *testing.T) {
	driver := &recordingAppNetworkDriver{capabilities: AppNetworkCapabilities{
		Accounting: true, UploadLimit: true, DownloadLimit: true, InternetControl: true,
	}}
	trafficDir := t.TempDir()
	trafficState := newAppTrafficState(trafficDir)
	previousLimit := AppTrafficLimit{UploadKbps: 256, DownloadKbps: 512}
	if err := trafficState.setLimit("app.example", previousLimit); err != nil {
		t.Fatal(err)
	}
	service := &Service{
		cfg:               Config{DataDir: t.TempDir()},
		settings:          newSettingsStore(DefaultMutableSettings()),
		containers:        newContainerControlState(),
		appTraffic:        trafficState,
		appTrafficLimiter: newAppTrafficLimiter(),
	}
	var reconciled []map[string]string
	service.appNetworkController = &appNetworkController{
		drivers:           map[AppNetworkTargetKind]appNetworkTargetDriver{AppNetworkTargetBridge: driver},
		internetAvailable: func() bool { return true },
		reconcileInternet: func(_ context.Context, _ []AppBridgeStats, desired map[string]string) error {
			copyDesired := make(map[string]string, len(desired))
			for appID, mode := range desired {
				copyDesired[appID] = mode
			}
			reconciled = append(reconciled, copyDesired)
			return nil
		},
		persistInternetPolicy: func() error { return errors.New("settings disk unavailable") },
	}

	targets := []AppNetworkTarget{{
		ID: "lzc-br-one", Kind: AppNetworkTargetBridge, AppID: "app.example", Interface: "lzc-br-one",
	}}
	upload, download, internetAllowed := int64(1024), int64(2048), false
	err := service.applyAppNetworkPolicyUpdate(context.Background(), "app.example", targets, appNetworkPolicyUpdate{
		UploadKbps: &upload, DownloadKbps: &download, InternetAllowed: &internetAllowed,
	})
	if err == nil || !strings.Contains(err.Error(), "persist application internet policy") {
		t.Fatalf("error=%v", err)
	}
	if len(driver.limits) != 2 {
		t.Fatalf("limit calls=%v, want apply + rollback", driver.limits)
	}
	if got := driver.limits[0]; got.UploadKbps != upload || got.DownloadKbps != download {
		t.Fatalf("applied limit=%#v", got)
	}
	if got := driver.limits[1]; got.UploadKbps != previousLimit.UploadKbps || got.DownloadKbps != previousLimit.DownloadKbps {
		t.Fatalf("rollback limit=%#v want=%#v", got, previousLimit)
	}
	if got := trafficState.limitForApp("app.example"); got.UploadKbps != previousLimit.UploadKbps || got.DownloadKbps != previousLimit.DownloadKbps {
		t.Fatalf("persisted limit=%#v want=%#v", got, previousLimit)
	}
	if got := newAppTrafficState(trafficDir).limitForApp("app.example"); got.UploadKbps != previousLimit.UploadKbps || got.DownloadKbps != previousLimit.DownloadKbps {
		t.Fatalf("reloaded limit=%#v want=%#v", got, previousLimit)
	}
	if blocked := service.containers.snapshotBlockedApps(); len(blocked) != 0 {
		t.Fatalf("blocked apps not restored: %#v", blocked)
	}
	if len(reconciled) != 2 || reconciled[0]["app.example"] != "internet" || len(reconciled[1]) != 0 {
		t.Fatalf("internet reconcile sequence=%#v", reconciled)
	}
}
