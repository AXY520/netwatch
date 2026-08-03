package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"netwatch/internal/probe"
)

func TestSettingsPartialUpdatePreservesLANAutoRemove(t *testing.T) {
	handler := newTestHandler(t)

	// Seed LAN auto-remove to 30 days via full-ish payload.
	seed := map[string]any{
		"lan_device_auto_remove_days": 30,
		"lan_max_check_attempts":      5,
		"background_monitor_enabled":  true,
	}
	body, _ := json.Marshal(seed)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.handleSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Main-page style payload: no LAN auto-remove field.
	mainPage := map[string]any{
		"refresh_interval_sec":            15,
		"background_monitor_enabled":      true,
		"background_monitor_interval_sec": 60,
		"notifications_enabled":           false,
		"nic_realtime_enabled":            true,
		"nic_realtime_interval_sec":       1,
	}
	body, _ = json.Marshal(mainPage)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/settings", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	handler.handleSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("main-page save status = %d body=%s", rec.Code, rec.Body.String())
	}

	var got probe.MutableSettings
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.LANDeviceAutoRemoveDays != 30 {
		t.Fatalf("lan_device_auto_remove_days = %d, want 30 (must not be zeroed by partial main-page save)", got.LANDeviceAutoRemoveDays)
	}
	if got.LANMaxCheckAttempts != 5 {
		t.Fatalf("lan_max_check_attempts = %d, want 5", got.LANMaxCheckAttempts)
	}
	if got.RefreshIntervalSec != 15 {
		t.Fatalf("refresh_interval_sec = %d, want 15", got.RefreshIntervalSec)
	}
}

func TestSettingsCanExplicitlyDisableAutoRemove(t *testing.T) {
	handler := newTestHandler(t)

	body, _ := json.Marshal(map[string]any{"lan_device_auto_remove_days": 30})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.handleSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed status = %d", rec.Code)
	}

	body, _ = json.Marshal(map[string]any{"lan_device_auto_remove_days": 0})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/settings", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	handler.handleSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable status = %d", rec.Code)
	}
	var got probe.MutableSettings
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.LANDeviceAutoRemoveDays != 0 {
		t.Fatalf("lan_device_auto_remove_days = %d, want 0 (explicit disable)", got.LANDeviceAutoRemoveDays)
	}
}

func TestSettingsPersistsDashboardCollapsedSectionsAcrossPartialUpdates(t *testing.T) {
	handler := newTestHandler(t)

	body, _ := json.Marshal(map[string]any{
		"dashboard_collapsed_sections": []string{"app_traffic", "host_ports"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.handleSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed status = %d body=%s", rec.Code, rec.Body.String())
	}

	body, _ = json.Marshal(map[string]any{"refresh_interval_sec": 15})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/settings", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	handler.handleSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("partial update status = %d body=%s", rec.Code, rec.Body.String())
	}

	var got probe.MutableSettings
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{"app_traffic", "host_ports"}
	if !reflect.DeepEqual(got.DashboardCollapsedSections, want) {
		t.Fatalf("collapsed sections = %#v, want %#v", got.DashboardCollapsedSections, want)
	}
}

func TestClampQueryLimit(t *testing.T) {
	if got := clampQueryLimit("", 300, 2000); got != 300 {
		t.Fatalf("default empty = %d", got)
	}
	if got := clampQueryLimit("0", 300, 2000); got != 300 {
		t.Fatalf("zero = %d", got)
	}
	if got := clampQueryLimit("-1", 300, 2000); got != 300 {
		t.Fatalf("neg = %d", got)
	}
	if got := clampQueryLimit("50", 300, 2000); got != 50 {
		t.Fatalf("ok = %d", got)
	}
	if got := clampQueryLimit("9999", 300, 2000); got != 2000 {
		t.Fatalf("cap = %d", got)
	}
	if got := clampQueryLimit("abc", 15, 30); got != 15 {
		t.Fatalf("bad = %d", got)
	}
}
