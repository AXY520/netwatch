package probe

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"netwatch/internal/logger"
)

func (s *Service) loadHistory() {
	_ = os.MkdirAll(s.cfg.DataDir, 0o755)
	s.loadJSON(filepath.Join(s.cfg.DataDir, "broadband_history.json"), &s.broadbandHistory)
	s.loadJSON(filepath.Join(s.cfg.DataDir, "local_transfer_history.json"), &s.localTransferHistory)
	changed := false
	for i := range s.broadbandHistory {
		if s.broadbandHistory[i].ID == "" {
			s.broadbandHistory[i].ID = fmt.Sprintf("broadband-legacy-%d", i)
			changed = true
		}
	}
	for i := range s.localTransferHistory {
		if s.localTransferHistory[i].ID == "" {
			s.localTransferHistory[i].ID = fmt.Sprintf("local-legacy-%d", i)
			changed = true
		}
	}
	if changed {
		s.saveBroadbandHistory()
		s.saveLocalTransferHistory()
	}
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
	if err := json.Unmarshal(body, target); err != nil {
		logger.Warn("loadJSON %s: %v", path, err)
	}
}

func (s *Service) saveJSON(path string, payload any) {
	if err := writeJSONFile(path, payload, true); err != nil {
		logger.Warn("saveJSON %s: %v", path, err)
	}
}
