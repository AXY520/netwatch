package probe

import "testing"

func TestAppTrafficRulesKeepStoreVisibleButProtected(t *testing.T) {
	const appID = "cloud.lazycat.shell.appstore"
	if !isWhitelistedApp(appID, "懒猫商店") {
		t.Fatal("app store must remain protected from network controls")
	}
	if isExcludedApp(appID, "懒猫商店") {
		t.Fatal("app store must be visible in traffic trends")
	}
	if isExcludedApp(appID, "App Store") {
		t.Fatal("localized app store title must not hide traffic trends")
	}
}

func TestAppTrafficRulesProtectCameraWithoutHidingTraffic(t *testing.T) {
	const appID = "cloud.lazycat.app.camera"
	if !isWhitelistedApp(appID, "懒猫摄像头") {
		t.Fatal("camera must be protected from network controls")
	}
	if isExcludedApp(appID, "懒猫摄像头") {
		t.Fatal("camera must remain visible in traffic trends")
	}
}

func TestAppTrafficRulesStillExcludeCoreSettingsAndBackup(t *testing.T) {
	for _, appID := range []string{"cloud.lazycat.shell.settings", "cloud.lazycat.shell.backup"} {
		if !isExcludedApp(appID, "") {
			t.Fatalf("core application %s must remain excluded", appID)
		}
	}
}
