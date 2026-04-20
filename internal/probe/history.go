package probe

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func (s *Service) loadHistory() {
	_ = os.MkdirAll(s.cfg.DataDir, 0o755)
	s.loadJSON(filepath.Join(s.cfg.DataDir, "broadband_history.json"), &s.broadbandHistory)
	s.loadJSON(filepath.Join(s.cfg.DataDir, "local_transfer_history.json"), &s.localTransferHistory)
}

func (s *Service) saveBroadbandHistory() {
	s.saveJSON(filepath.Join(s.cfg.DataDir, "broadband_history.json"), s.broadbandHistory)
}

func (s *Service) saveLocalTransferHistory() {
	s.saveJSON(filepath.Join(s.cfg.DataDir, "local_transfer_history.json"), s.localTransferHistory)
}

func (s *Service) loadJSON(path string, target any) {
	body, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(body, target)
}

func (s *Service) saveJSON(path string, payload any) {
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, body, 0o644)
}
