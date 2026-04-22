package probe

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func settingsPath(dataDir string) string {
	return filepath.Join(dataDir, "settings.json")
}

func loadMutableSettings(dataDir string) (MutableSettings, bool) {
	var s MutableSettings
	body, err := os.ReadFile(settingsPath(dataDir))
	if err != nil {
		return s, false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return s, false
	}
	if err := json.Unmarshal(body, &s); err != nil {
		return s, false
	}
	if _, ok := raw["broadband_domestic_only"]; !ok {
		s.BroadbandDomesticOnly = true
	}
	return s, true
}

func saveMutableSettings(dataDir string, s MutableSettings) error {
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath(dataDir), body, 0o644)
}
