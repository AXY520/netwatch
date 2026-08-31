package probe

import (
	"strings"
	"testing"
	"time"
)

func downloaderInstances() []AppBridgeStats {
	const appID = "cloud.lazycat.app.downloader"
	return []AppBridgeStats{
		{Bridge: "lzc-br-one", AppID: appID, InstanceID: appID + "@user:axy", UserID: "axy", MultiInstance: true, Project: "downloader", NetworkMode: "bridge", ContainerCount: 1, RunningCount: 1, UploadBytes: 100, DownloadBytes: 200},
		{Bridge: "lzc-br-two", AppID: appID, InstanceID: appID + "@user:damn", UserID: "damn", MultiInstance: true, Project: "downloader1", NetworkMode: "bridge", ContainerCount: 1, RunningCount: 1, UploadBytes: 300, DownloadBytes: 400},
	}
}

func TestResolveAppInstancePolicyIDRejectsAmbiguousApp(t *testing.T) {
	items := downloaderInstances()
	if _, err := resolveAppInstancePolicyID(items, items[0].AppID, ""); err == nil || !strings.Contains(err.Error(), "多个实例") {
		t.Fatalf("ambiguous app error = %v", err)
	}
	got, err := resolveAppInstancePolicyID(items, items[0].AppID, items[1].InstanceID)
	if err != nil || got != items[1].InstanceID {
		t.Fatalf("explicit instance = %q, %v", got, err)
	}
	targets := appNetworkTargetsForApp(items, got)
	if len(targets) != 1 || targets[0].ID != "lzc-br-two" || targets[0].InstanceID != got {
		t.Fatalf("instance targets = %+v", targets)
	}
}

func TestAppTrafficStateKeepsMultiInstanceUsageSeparate(t *testing.T) {
	state := newAppTrafficState(t.TempDir())
	items := downloaderInstances()
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.Local)
	state.sample(items, now)
	active := activeAppTrafficIDs(items)
	overview := state.overviewForActiveApps(true, active)
	if len(overview.Apps) != 2 {
		t.Fatalf("apps = %+v", overview.Apps)
	}
	byID := make(map[string]AppTrafficUsage)
	for _, app := range overview.Apps {
		byID[app.InstanceID] = app
	}
	if byID[items[0].InstanceID].UserID != "axy" || byID[items[0].InstanceID].TotalUpload != 100 {
		t.Fatalf("axy usage = %+v", byID[items[0].InstanceID])
	}
	if byID[items[1].InstanceID].UserID != "damn" || byID[items[1].InstanceID].TotalUpload != 300 {
		t.Fatalf("damn usage = %+v", byID[items[1].InstanceID])
	}

	items[0].UploadBytes += 20
	items[1].UploadBytes += 70
	state.sample(items, now.Add(2*time.Second))
	overview = state.overviewForActiveApps(true, active)
	for _, app := range overview.Apps {
		byID[app.InstanceID] = app
	}
	if byID[items[0].InstanceID].TodayUpload != 20 || byID[items[1].InstanceID].TodayUpload != 70 {
		t.Fatalf("instance deltas were merged: axy=%+v damn=%+v", byID[items[0].InstanceID], byID[items[1].InstanceID])
	}
}

func TestMigrateLegacyInstancePoliciesCopiesThenRemovesBaseKey(t *testing.T) {
	const appID = "cloud.lazycat.app.downloader"
	instances := map[string][]string{appID: {appID + "@user:axy", appID + "@user:damn"}}
	state := newAppTrafficState(t.TempDir())
	state.limits[appID] = AppTrafficLimit{UploadKbps: 10000, DownloadKbps: 10000}
	changed, err := state.migrateLegacyLimits(instances)
	if err != nil || !changed {
		t.Fatalf("limit migration = %v, %v", changed, err)
	}
	limits := state.limitsSnapshot()
	if _, exists := limits[appID]; exists || limits[instances[appID][0]].UploadKbps != 10000 || limits[instances[appID][1]].DownloadKbps != 10000 {
		t.Fatalf("migrated limits = %+v", limits)
	}

	blocked := map[string]string{appID: "internet"}
	proxyApps := map[string]bool{appID: true}
	proxyConfig := AppProxySettings{Protocol: "socks5", Host: "192.168.3.174", Port: 7890}
	proxyConfigs := map[string]AppProxySettings{appID: proxyConfig}
	if !migrateLegacyAppInstanceControlMaps(instances, blocked, proxyApps, proxyConfigs) {
		t.Fatal("control migration reported no change")
	}
	if blocked[appID] != "" || proxyApps[appID] {
		t.Fatalf("legacy controls remain: blocked=%+v proxy=%+v", blocked, proxyApps)
	}
	for _, instanceID := range instances[appID] {
		if blocked[instanceID] != "internet" || !proxyApps[instanceID] || proxyConfigs[instanceID] != proxyConfig {
			t.Fatalf("instance %s controls were not copied", instanceID)
		}
	}
}

func TestLimitMigrationDoesNotCommitWhenPersistenceFails(t *testing.T) {
	const appID = "cloud.lazycat.app.downloader"
	state := newAppTrafficState(t.TempDir())
	state.limits[appID] = AppTrafficLimit{DownloadKbps: 10000}
	state.path = t.TempDir() // Renaming a JSON file over this directory must fail.
	changed, err := state.migrateLegacyLimits(map[string][]string{appID: {appID + "@user:axy"}})
	if err == nil || changed {
		t.Fatalf("migration failure = changed %v, err %v", changed, err)
	}
	limits := state.limitsSnapshot()
	if limits[appID].DownloadKbps != 10000 || len(limits) != 1 {
		t.Fatalf("failed migration changed memory: %+v", limits)
	}
}

func TestSharedHostParentDisablesMultiInstanceControls(t *testing.T) {
	const appID = "cloud.lazycat.app.hostmulti"
	items := []AppBridgeStats{
		{AppID: appID, InstanceID: appID + "@user:axy", UserID: "axy", MultiInstance: true, NetworkMode: "host", Bridge: hostAppTarget(appID + "@user:axy"), CgroupPath: "system.slice/lzcapp.slice/demo.slice/docker-one.scope", Target: AppNetworkTarget{ID: hostAppTarget(appID + "@user:axy"), Kind: AppNetworkTargetCgroup, AppID: appID, InstanceID: appID + "@user:axy"}},
		{AppID: appID, InstanceID: appID + "@user:damn", UserID: "damn", MultiInstance: true, NetworkMode: "host", Bridge: hostAppTarget(appID + "@user:damn"), CgroupPath: "system.slice/lzcapp.slice/demo.slice/docker-two.scope", Target: AppNetworkTarget{ID: hostAppTarget(appID + "@user:damn"), Kind: AppNetworkTargetCgroup, AppID: appID, InstanceID: appID + "@user:damn"}},
	}
	annotateHostInstanceControlIsolation(items)
	for _, item := range items {
		if item.Target.ControlDiagnostic == "" {
			t.Fatalf("shared parent was not rejected: %+v", items)
		}
	}
	if instances := activeAppInstances(items); len(instances) != 0 {
		t.Fatalf("unsafe Host instances must not receive legacy policy migration: %+v", instances)
	}
}

func TestDistinctHostParentsKeepMultiInstanceControlsAvailable(t *testing.T) {
	const appID = "cloud.lazycat.app.hostmulti"
	items := []AppBridgeStats{
		{AppID: appID, InstanceID: appID + "@user:axy", NetworkMode: "host", CgroupPath: "system.slice/axy.slice/docker-one.scope", Target: AppNetworkTarget{InstanceID: appID + "@user:axy"}},
		{AppID: appID, InstanceID: appID + "@user:damn", NetworkMode: "host", CgroupPath: "system.slice/damn.slice/docker-two.scope", Target: AppNetworkTarget{InstanceID: appID + "@user:damn"}},
	}
	annotateHostInstanceControlIsolation(items)
	for _, item := range items {
		if item.Target.ControlDiagnostic != "" {
			t.Fatalf("distinct parent was rejected: %+v", items)
		}
	}
}

func TestMultiInstanceProxyRulesUseInstanceConfiguration(t *testing.T) {
	const appID = "cloud.lazycat.app.downloader"
	axy := appID + "@user:axy"
	damn := appID + "@user:damn"
	targets := []AppNetworkTarget{
		{ID: "lzc-br-one", Kind: AppNetworkTargetBridge, AppID: appID, InstanceID: axy, Interface: "lzc-br-one"},
		{ID: "lzc-br-two", Kind: AppNetworkTargetBridge, AppID: appID, InstanceID: damn, Interface: "lzc-br-two"},
	}
	_, _, v4, _, err := buildAppProxyRules(targets,
		map[string]AppProxySettings{axy: {Protocol: "http", Host: "192.168.3.192", Port: 7890}},
		nil, map[string]int{axy: 23001})
	if err != nil {
		t.Fatal(err)
	}
	rules := ""
	for _, rule := range append(v4.natPre, v4.filterForward...) {
		rules += strings.Join(rule, " ") + "\n"
	}
	if !strings.Contains(rules, "lzc-br-one") || !strings.Contains(rules, "23001") {
		t.Fatalf("selected instance rules missing:\n%s", rules)
	}
	if strings.Contains(rules, "lzc-br-two") {
		t.Fatalf("other user instance was proxied:\n%s", rules)
	}
}

func TestMultiInstanceLifecycleAggregationUsesInstanceIdentity(t *testing.T) {
	items := downloaderInstances()
	apps := aggregateAppTrafficRuntime(items)
	if len(apps) != 2 {
		t.Fatalf("lifecycle runtime merged user instances: %+v", apps)
	}
	for _, item := range items {
		if _, ok := apps["app:"+item.InstanceID]; !ok {
			t.Fatalf("missing lifecycle identity %s: %+v", item.InstanceID, apps)
		}
	}
}
