package probe

import (
	"context"
	"fmt"
	"strings"
	"time"

	"netwatch/internal/dockerlzc"
	"netwatch/internal/logger"
)

const (
	appLifecycleEventDebounce     = 2 * time.Second
	appLifecycleCalibrationPeriod = time.Minute
	appTrafficCounterPeriod       = 5 * time.Second
)

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
	if result.ID == "" {
		result.ID = fmt.Sprintf("broadband-%d", time.Now().UnixNano())
	}
	s.broadbandHistory = append([]BroadbandSpeedResult{result}, s.broadbandHistory...)
	if len(s.broadbandHistory) > maxHistoryItems {
		s.broadbandHistory = s.broadbandHistory[:maxHistoryItems]
	}
	s.saveBroadbandHistory()
}

func (s *Service) pushLocalTransferHistory(result LocalTransferResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if result.ID == "" {
		result.ID = fmt.Sprintf("local-%d", time.Now().UnixNano())
	}
	s.localTransferHistory = append([]LocalTransferResult{result}, s.localTransferHistory...)
	if len(s.localTransferHistory) > maxHistoryItems {
		s.localTransferHistory = s.localTransferHistory[:maxHistoryItems]
	}
	s.saveLocalTransferHistory()
}

func (s *Service) UpdateSpeedHistoryNote(kind, id, note string) bool {
	id = strings.TrimSpace(id)
	note = strings.TrimSpace(note)
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch kind {
	case "broadband":
		for i := range s.broadbandHistory {
			if s.broadbandHistory[i].ID == id {
				s.broadbandHistory[i].Note = note
				s.saveBroadbandHistory()
				return true
			}
		}
	case "local":
		for i := range s.localTransferHistory {
			if s.localTransferHistory[i].ID == id {
				s.localTransferHistory[i].Note = note
				s.saveLocalTransferHistory()
				return true
			}
		}
	}
	return false
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
		ctx, cancel := context.WithCancel(s.backgroundCtx())
		defer cancel()
		dockerChanges := make(chan struct{}, 1)
		go watchDockerLifecycleEvents(ctx, dockerChanges)

		calibration := time.NewTicker(appLifecycleCalibrationPeriod)
		defer calibration.Stop()
		trafficCounters := time.NewTicker(appTrafficCounterPeriod)
		defer trafficCounters.Stop()
		var debounce *time.Timer
		var debounceC <-chan time.Time
		scheduleObservation := func() {
			if debounce == nil {
				debounce = time.NewTimer(appLifecycleEventDebounce)
			} else {
				if !debounce.Stop() {
					select {
					case <-debounce.C:
					default:
					}
				}
				debounce.Reset(appLifecycleEventDebounce)
			}
			debounceC = debounce.C
		}

		dockerlzc.InvalidateBridgeMapCache()
		s.recordAppTrafficEvents()
		for {
			select {
			case <-s.appLifecycleStop:
				if debounce != nil {
					debounce.Stop()
				}
				return
			case <-dockerChanges:
				scheduleObservation()
			case <-debounceC:
				debounceC = nil
				dockerlzc.InvalidateBridgeMapCache()
				s.recordAppTrafficEvents()
			case <-calibration.C:
				if debounceC == nil {
					dockerlzc.InvalidateBridgeMapCache()
					s.recordAppTrafficEvents()
				}
			case <-trafficCounters.C:
				s.recordAppTrafficEvents()
			}
		}
	}()
}

func watchDockerLifecycleEvents(ctx context.Context, changes chan<- struct{}) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := dockerlzc.WatchEvents(ctx, func(event dockerlzc.Event) {
			if !dockerLifecycleEventRelevant(event) {
				return
			}
			select {
			case changes <- struct{}{}:
			default:
			}
		})
		if ctx.Err() != nil {
			return
		}
		logger.Warn("docker event stream disconnected: %v", err)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func dockerLifecycleEventRelevant(event dockerlzc.Event) bool {
	switch event.Type {
	case "network":
		switch event.Action {
		case "create", "connect", "disconnect", "destroy":
			return true
		}
	case "container":
		switch event.Action {
		case "create", "start", "stop", "die", "destroy", "restart", "pause", "unpause", "rename":
			return true
		}
	}
	return false
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
