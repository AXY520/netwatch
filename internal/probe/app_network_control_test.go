package probe

import (
	"context"
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
