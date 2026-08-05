package probe

import (
	"strings"
	"time"

	"netwatch/internal/logger"
)

const appLifecycleObservationInterval = 5 * time.Second

func (s *Service) GetBroadbandHistory() []BroadbandSpeedResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]BroadbandSpeedResult(nil), s.broadbandHistory...)
}

func (s *Service) GetLocalTransferHistory() []LocalTransferResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]LocalTransferResult(nil), s.localTransferHistory...)
}

func (s *Service) GetSpeedConfig() SpeedConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return SpeedConfig{
		BroadbandDurationSec:     int64(s.cfg.BroadbandDuration / time.Second),
		LocalTransferDurationSec: int64(s.cfg.LocalTransferDuration / time.Second),
		LocalTransferPayloadMB:   s.cfg.LocalTransferPayloadMB,
	}
}

func (s *Service) pushBroadbandHistory(result BroadbandSpeedResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadbandHistory = append([]BroadbandSpeedResult{result}, s.broadbandHistory...)
	if len(s.broadbandHistory) > maxHistoryItems {
		s.broadbandHistory = s.broadbandHistory[:maxHistoryItems]
	}
	s.saveBroadbandHistory()
}

func (s *Service) pushLocalTransferHistory(result LocalTransferResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localTransferHistory = append([]LocalTransferResult{result}, s.localTransferHistory...)
	if len(s.localTransferHistory) > maxHistoryItems {
		s.localTransferHistory = s.localTransferHistory[:maxHistoryItems]
	}
	s.saveLocalTransferHistory()
}

func (s *Service) startAppTrafficSampling() {
	go func() {
		defer close(s.appTrafficDone)
		lastSampled := make(map[string]time.Time)
		for {
			enabled, interval, perApp, persistent := s.getTrafficSamplingConfig()
			if !enabled {
				// Wait and re-check
				select {
				case <-s.appTrafficStop:
					return
				case <-time.After(5 * time.Second):
					continue
				}
			}

			now := time.Now()
			s.appTrafficHistory.sampleWithTiming(persistent, lastSampled, now, interval, perApp)

			// Sleep until the next bridge needs sampling
			sleep := time.Duration(interval) * time.Second
			for _, iv := range perApp {
				if iv > 0 {
					d := time.Duration(iv) * time.Second
					if d < sleep {
						sleep = d
					}
				}
			}
			if sleep < 1*time.Second {
				sleep = 1 * time.Second
			}

			select {
			case <-s.appTrafficStop:
				return
			case <-time.After(sleep):
			}
		}
	}()
}

func (s *Service) startAppLifecycleObserver() {
	go func() {
		defer close(s.appLifecycleDone)
		ticker := time.NewTicker(appLifecycleObservationInterval)
		defer ticker.Stop()
		s.recordAppTrafficEvents()
		for {
			select {
			case <-s.appLifecycleStop:
				return
			case <-ticker.C:
				s.recordAppTrafficEvents()
			}
		}
	}()
}

func (s *Service) getTrafficSamplingConfig() (enabled bool, interval int, perApp map[string]int, persistent []string) {
	return s.settings.snapshotTraffic()
}

func (s *Service) getPersistentTrafficBridges() []string {
	_, _, _, persistent := s.settings.snapshotTraffic()
	return persistent
}

func (s *Service) TogglePersistentTrafficBridge(bridge string, enabled bool) MutableSettings {
	bridge = strings.TrimSpace(bridge)
	if bridge == "" {
		return s.GetMutableSettings()
	}
	_, _, _, persistent := s.settings.snapshotTraffic()
	next := make([]string, 0, len(persistent)+1)
	found := false
	for _, b := range persistent {
		if b == bridge {
			found = true
			if enabled {
				next = append(next, b)
			}
			continue
		}
		next = append(next, b)
	}
	if enabled && !found {
		next = append(next, bridge)
	}
	s.settings.setPersistentBridges(next)
	settings := s.GetMutableSettings()
	if err := saveMutableSettings(s.cfg.DataDir, settings); err != nil {
		logger.Warn("save persistent bridges: %v", err)
	}
	return settings
}

func (s *Service) GetAppTrafficHistory(bridge string, limit int) []AppTrafficPoint {
	return s.appTrafficHistory.snapshot(bridge, limit)
}

func (s *Service) GetAppTrafficHistorySince(bridge string, since time.Time, limit int) []AppTrafficPoint {
	return s.appTrafficHistory.snapshotSince(bridge, since, limit)
}

func (s *Service) GetAppTrafficTop(since time.Time, limit int) []AppTrafficTopItem {
	return s.appTrafficHistory.topSince(since, limit)
}

func (s *Service) SampleAppTrafficBridge(bridge string, since time.Time, limit int) (AppTrafficLiveResult, bool) {
	if ok := s.appTrafficHistory.sampleOne(bridge, time.Now()); !ok {
		return AppTrafficLiveResult{}, false
	}
	stats, ok := CollectBridgeTraffic(bridge)
	if !ok {
		return AppTrafficLiveResult{}, false
	}
	return AppTrafficLiveResult{
		GeneratedAt:        localTimestamp(),
		Bridge:             stats,
		History:            s.appTrafficHistory.snapshotSince(bridge, since, limit),
		CounterPerspective: appTrafficCounterPerspective,
		Source:             appTrafficSource,
	}, true
}
