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

// RecordClientBroadbandResult stores a browser-to-public-network result. The
// test traffic itself never traverses this service; this endpoint only keeps
// the local history together with server-exit test results.
func (s *Service) RecordClientBroadbandResult(result BroadbandSpeedResult) BroadbandSpeedResult {
	result.TestMode = "client"
	result.DownloadMbps = sanitizeSpeedMetric(result.DownloadMbps, 0, 100000)
	result.UploadMbps = sanitizeSpeedMetric(result.UploadMbps, 0, 100000)
	if result.LatencyMS < 0 || result.LatencyMS > 600000 {
		result.LatencyMS = 0
	}
	if result.JitterMS < 0 || result.JitterMS > 600000 {
		result.JitterMS = 0
	}
	if result.Timestamp == "" {
		result.Timestamp = localTimestamp()
	}
	s.pushBroadbandHistory(result)
	return result
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

func (s *Service) startAppLifecycleObserver() {
	go func() {
		defer close(s.appLifecycleDone)
		ctx, cancel := context.WithCancel(s.backgroundCtx())
		defer cancel()
		dockerChanges := make(chan struct{}, 1)
		go watchDockerLifecycleEvents(ctx, dockerChanges)

		calibration := time.NewTicker(appLifecycleCalibrationPeriod)
		defer calibration.Stop()
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
