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
	if err := json.Unmarshal(body, &s); err != nil {
		return s, false
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
