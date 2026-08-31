package probe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateSpeedHistoryNotePersists(t *testing.T) {
	dir := t.TempDir()
	service := &Service{
		cfg:                  Config{DataDir: dir},
		broadbandHistory:     []BroadbandSpeedResult{{ID: "b-1", Timestamp: "2026-08-05 12:00:00"}},
		localTransferHistory: []LocalTransferResult{{ID: "l-1", Timestamp: "2026-08-05 12:01:00"}},
	}
	if !service.UpdateSpeedHistoryNote("broadband", "b-1", "办公室有线") {
		t.Fatal("broadband note was not updated")
	}
	if !service.UpdateSpeedHistoryNote("local", "l-1", "Wi-Fi 对照") {
		t.Fatal("local note was not updated")
	}
	if service.UpdateSpeedHistoryNote("local", "missing", "ignored") {
		t.Fatal("missing history item unexpectedly updated")
	}

	var broadband []BroadbandSpeedResult
	readSpeedHistory(t, filepath.Join(dir, "broadband_history.json"), &broadband)
	if len(broadband) != 1 || broadband[0].Note != "办公室有线" {
		t.Fatalf("broadband history = %+v", broadband)
	}
	var local []LocalTransferResult
	readSpeedHistory(t, filepath.Join(dir, "local_transfer_history.json"), &local)
	if len(local) != 1 || local[0].Note != "Wi-Fi 对照" {
		t.Fatalf("local history = %+v", local)
	}
}

func TestClearSpeedHistoryPersistsEmptyLists(t *testing.T) {
	dir := t.TempDir()
	service := &Service{
		cfg:                  Config{DataDir: dir},
		broadbandHistory:     []BroadbandSpeedResult{{ID: "b-1"}},
		localTransferHistory: []LocalTransferResult{{ID: "l-1"}},
	}
	if !service.ClearSpeedHistory("broadband") || !service.ClearSpeedHistory("local") {
		t.Fatal("history clear failed")
	}
	if len(service.GetBroadbandHistory()) != 0 || len(service.GetLocalTransferHistory()) != 0 {
		t.Fatal("history was not cleared")
	}
	var broadband []BroadbandSpeedResult
	readSpeedHistory(t, filepath.Join(dir, "broadband_history.json"), &broadband)
	var local []LocalTransferResult
	readSpeedHistory(t, filepath.Join(dir, "local_transfer_history.json"), &local)
	if broadband == nil || local == nil {
		t.Fatalf("cleared history should persist as JSON arrays: broadband=%#v local=%#v", broadband, local)
	}
}

func TestLoadHistoryBackfillsLegacyBroadbandMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broadband_history.json")
	if err := os.WriteFile(path, []byte(`[{"timestamp":"2026-08-05 12:00:00","download_mbps":100}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	service := &Service{cfg: Config{DataDir: dir}}
	service.loadHistory()
	history := service.GetBroadbandHistory()
	if len(history) != 1 || history[0].TestMode != "server" || history[0].ID == "" {
		t.Fatalf("legacy broadband history = %+v", history)
	}

	var persisted []BroadbandSpeedResult
	readSpeedHistory(t, path, &persisted)
	if len(persisted) != 1 || persisted[0].TestMode != "server" || persisted[0].ID == "" {
		t.Fatalf("persisted broadband history = %+v", persisted)
	}
}

func readSpeedHistory(t *testing.T, path string, target any) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, target); err != nil {
		t.Fatal(err)
	}
}
