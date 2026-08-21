package probe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefaultMutableSettingsStableCore(t *testing.T) {
	def := DefaultMutableSettings()
	if def.BackgroundMonitorIntervalSec != 60 {
		t.Fatalf("BackgroundMonitorIntervalSec=%d", def.BackgroundMonitorIntervalSec)
	}
	if def.BarkServerURL == "" || def.BarkGroup == "" {
		t.Fatal("bark defaults missing")
	}
}

func TestLoadMutableSettingsFillsMissingKeys(t *testing.T) {
	dir := t.TempDir()
	// Intentionally omit many fields; only set background_monitor_enabled.
	raw := map[string]any{
		"background_monitor_enabled": true,
	}
	body, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := loadMutableSettings(dir)
	if !ok {
		t.Fatal("expected load ok")
	}
	if !got.BackgroundMonitorEnabled {
		t.Fatal("expected background monitor enabled")
	}
	// Missing notifications_enabled follows historical rule: mirrors background monitor.
	if !got.NotificationsEnabled {
		t.Fatal("expected notifications_enabled to follow background monitor when unset")
	}
	def := DefaultMutableSettings()
	if got.BarkServerURL != def.BarkServerURL {
		t.Fatalf("BarkServerURL=%q want %q", got.BarkServerURL, def.BarkServerURL)
	}
	if got.LANDeviceOfflineAfterSec != def.LANDeviceOfflineAfterSec {
		t.Fatalf("LANDeviceOfflineAfterSec=%d", got.LANDeviceOfflineAfterSec)
	}
}

func TestLoadMutableSettingsMissingFile(t *testing.T) {
	got, ok := loadMutableSettings(t.TempDir())
	if ok {
		t.Fatal("expected missing file to return false")
	}
	def := DefaultMutableSettings()
	if got.BarkGroup != def.BarkGroup {
		t.Fatalf("got BarkGroup %q", got.BarkGroup)
	}
}

func TestNormalizeDashboardCollapsedSections(t *testing.T) {
	got := normalizeDashboardCollapsedSections([]string{"app_traffic", "unknown", "app_traffic", "host_ports"})
	want := []string{"app_traffic", "host_ports"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
