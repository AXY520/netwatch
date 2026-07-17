package probe

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (s *Service) RunBroadbandSpeedTest(ctx context.Context) BroadbandSpeedResult {
	s.mu.RLock()
	duration := s.cfg.BroadbandDuration
	s.mu.RUnlock()

	result, completed := executeBroadbandSpeedTest(ctx, s, duration, nil, nil)
	if completed {
		s.pushBroadbandHistory(result)
	}
	return result
}

func (s *Service) StartBroadbandTask() BroadbandTaskStatus {
	s.mu.Lock()
	if s.tasks.broadbandTask.Running {
		task := s.tasks.broadbandTask
		s.mu.Unlock()
		return task
	}

	duration := s.cfg.BroadbandDuration
	ctx, cancel := context.WithTimeout(s.backgroundCtx(), broadbandTaskTimeout(duration))
	task := BroadbandTaskStatus{
		ID:              fmt.Sprintf("broadband-%d", time.Now().UnixNano()),
		Stage:           "starting",
		ProgressPercent: 0,
		Running:         true,
		Message:         "准备开始宽带测速",
		UpdatedAt:       localTimestamp(),
		Result: BroadbandSpeedResult{
			Timestamp:    localTimestamp(),
			Provider:     "Speedtest China",
			ServerRegion: "中国测速节点",
		},
	}
	s.tasks.broadbandTask = task
	s.tasks.broadbandCancel = cancel
	s.mu.Unlock()

	go s.runBroadbandTask(ctx, duration)
	return task
}

func (s *Service) GetBroadbandTask() BroadbandTaskStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tasks.broadbandTask
}

func (s *Service) CancelBroadbandTask() BroadbandTaskStatus {
	s.mu.Lock()
	cancel := s.tasks.broadbandCancel
	if s.tasks.broadbandTask.Running {
		s.tasks.broadbandTask.Message = "正在取消测速"
		s.tasks.broadbandTask.UpdatedAt = localTimestamp()
	}
	task := s.tasks.broadbandTask
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return task
}

// appendBroadbandStep 追加一条测速过程步骤(供前端实时展示),并限制最多保留 maxBroadbandSteps 条。
func (s *Service) appendBroadbandStep(stage, status, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq := 1
	if n := len(s.tasks.broadbandTask.Steps); n > 0 {
		seq = s.tasks.broadbandTask.Steps[n-1].Seq + 1
	}
	s.tasks.broadbandTask.Steps = append(s.tasks.broadbandTask.Steps, BroadbandTaskStep{
		Seq:     seq,
		Time:    localTimestamp(),
		Stage:   stage,
		Status:  status,
		Message: message,
	})
	if len(s.tasks.broadbandTask.Steps) > maxBroadbandSteps {
		s.tasks.broadbandTask.Steps = s.tasks.broadbandTask.Steps[len(s.tasks.broadbandTask.Steps)-maxBroadbandSteps:]
	}
	s.tasks.broadbandTask.UpdatedAt = localTimestamp()
}

func (s *Service) runBroadbandTask(ctx context.Context, duration time.Duration) {
	result, completed := executeBroadbandSpeedTest(ctx, s, duration, func(stage string, progress int, message string, partial BroadbandSpeedResult) {
		s.mu.Lock()
		s.tasks.broadbandTask.Stage = stage
		s.tasks.broadbandTask.ProgressPercent = progress
		s.tasks.broadbandTask.Message = message
		s.tasks.broadbandTask.Result = partial
		s.tasks.broadbandTask.UpdatedAt = localTimestamp()
		s.mu.Unlock()
	}, func(stage, status, message string) {
		s.appendBroadbandStep(stage, status, message)
	})

	s.mu.Lock()
	defer s.mu.Unlock()

	s.tasks.broadbandTask.Result = result
	s.tasks.broadbandTask.UpdatedAt = localTimestamp()
	s.tasks.broadbandTask.Running = false
	s.tasks.broadbandCancel = nil

	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		s.tasks.broadbandTask.Stage = "canceled"
		s.tasks.broadbandTask.Canceled = true
		s.tasks.broadbandTask.Finished = false
		s.tasks.broadbandTask.Message = "宽带测速已取消"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		s.tasks.broadbandTask.Stage = "error"
		s.tasks.broadbandTask.Finished = false
		s.tasks.broadbandTask.Canceled = false
		if result.Error == "" {
			result.Error = "宽带测速超时"
		}
		result.FailureStage = "timeout"
		result.FailureReason = result.Error
		s.tasks.broadbandTask.Result = result
		s.tasks.broadbandTask.Message = result.Error
	case !completed:
		s.tasks.broadbandTask.Stage = "error"
		s.tasks.broadbandTask.Finished = false
		if result.Error == "" {
			result.Error = "测速未完成"
		}
		if result.FailureReason == "" {
			result.FailureReason = result.Error
		}
		s.tasks.broadbandTask.Result = result
		s.tasks.broadbandTask.Message = result.Error
	default:
		s.tasks.broadbandTask.Stage = "complete"
		s.tasks.broadbandTask.ProgressPercent = 100
		s.tasks.broadbandTask.Finished = true
		s.tasks.broadbandTask.Canceled = false
		s.tasks.broadbandTask.Message = "宽带测速完成"
		go s.pushBroadbandHistory(result)
	}
}

func (s *Service) RecordLocalTransferResult(result LocalTransferResult) LocalTransferResult {
	result.DownloadMbps = sanitizeSpeedMetric(result.DownloadMbps, 0, 100000)
	result.UploadMbps = sanitizeSpeedMetric(result.UploadMbps, 0, 100000)
	result.PayloadMB = sanitizeSpeedMetric(result.PayloadMB, 0, 1024)
	result.DownloadMB = sanitizeSpeedMetric(result.DownloadMB, 0, 1024*1024)
	result.UploadMB = sanitizeSpeedMetric(result.UploadMB, 0, 1024*1024)
	if result.PayloadMB == 0 {
		result.PayloadMB = result.DownloadMB + result.UploadMB
	}
	if result.DurationMS < 0 || result.DurationMS > int64((10*time.Minute)/time.Millisecond) {
		result.DurationMS = 0
	}
	if result.RoundTripLatencyMS < 0 || result.RoundTripLatencyMS > 600000 {
		result.RoundTripLatencyMS = 0
	}
	if result.RTTMinMS < 0 || result.RTTMinMS > 600000 {
		result.RTTMinMS = 0
	}
	if result.RTTAvgMS < 0 || result.RTTAvgMS > 600000 {
		result.RTTAvgMS = 0
	}
	if result.RTTMaxMS < 0 || result.RTTMaxMS > 600000 {
		result.RTTMaxMS = 0
	}
	if result.RTTAvgMS == 0 {
		result.RTTAvgMS = result.RoundTripLatencyMS
	}
	if result.JitterMS < 0 || result.JitterMS > 600000 {
		result.JitterMS = 0
	}
	if result.Timestamp == "" {
		result.Timestamp = localTimestamp()
	}
	s.pushLocalTransferHistory(result)
	return result
}

func (s *Service) StartTraceTask(host string, maxHops int) TraceResult {
	s.tasks.traceMu.Lock()
	if s.tasks.traceCancel != nil {
		s.tasks.traceCancel()
	}
	ctx, cancel := context.WithCancel(s.traceCtx())
	task := TraceResult{
		Target:    host,
		Timestamp: localTimestamp(),
		Tool:      "mtr",
		Running:   true,
	}
	s.tasks.traceTask = task
	s.tasks.traceCancel = cancel
	s.tasks.traceMu.Unlock()

	go func() {
		result := RunTrace(ctx, host, maxHops, func(update TraceResult) {
			s.tasks.traceMu.Lock()
			// Do not clobber an already-cancelled snapshot with intermediate updates.
			if s.tasks.traceTask.Error == "cancelled" && !s.tasks.traceTask.Running {
				s.tasks.traceMu.Unlock()
				return
			}
			s.tasks.traceTask = update
			s.tasks.traceMu.Unlock()
		})
		s.tasks.traceMu.Lock()
		if ctx.Err() != nil || s.tasks.traceTask.Error == "cancelled" {
			// Keep partial hops from the last progress snapshot when available.
			if len(result.Hops) == 0 && len(s.tasks.traceTask.Hops) > 0 {
				result.Hops = s.tasks.traceTask.Hops
			}
			result.Error = "cancelled"
		}
		result.Running = false
		result.Finished = true
		s.tasks.traceTask = result
		s.tasks.traceCancel = nil
		s.tasks.traceMu.Unlock()
	}()

	return task
}

func (s *Service) GetTraceTask() TraceResult {
	s.tasks.traceMu.Lock()
	defer s.tasks.traceMu.Unlock()
	return s.tasks.traceTask
}

// CancelTraceTask aborts an in-flight traceroute and marks the task finished.
func (s *Service) CancelTraceTask() TraceResult {
	s.tasks.traceMu.Lock()
	defer s.tasks.traceMu.Unlock()
	if s.tasks.traceCancel != nil {
		s.tasks.traceCancel()
		s.tasks.traceCancel = nil
	}
	task := s.tasks.traceTask
	if task.Running || !task.Finished {
		task.Running = false
		task.Finished = true
		if task.Error == "" {
			task.Error = "cancelled"
		}
		s.tasks.traceTask = task
	}
	return s.tasks.traceTask
}
