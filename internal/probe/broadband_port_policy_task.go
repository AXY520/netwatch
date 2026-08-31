package probe

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (s *Service) StartBroadbandPortPolicyTask() BroadbandPortPolicyTaskStatus {
	s.tasks.portPolicyMu.Lock()
	if s.tasks.portPolicyTask.Running {
		task := cloneBroadbandPortPolicyTask(s.tasks.portPolicyTask)
		s.tasks.portPolicyMu.Unlock()
		return task
	}
	s.mu.RLock()
	duration := s.cfg.BroadbandDuration
	s.mu.RUnlock()
	ctx, cancel := context.WithTimeout(s.backgroundCtx(), broadbandTaskTimeout(duration))
	task := BroadbandPortPolicyTaskStatus{
		ID:              fmt.Sprintf("port-policy-%d", time.Now().UnixNano()),
		Stage:           "allocating",
		ProgressPercent: 0,
		Running:         true,
		Message:         "正在申请公网高低端口",
		UpdatedAt:       localTimestamp(),
		Provider:        portPolicyProvider,
		Host:            portPolicyTargetHost(),
		Protocol:        portPolicyProtocol,
	}
	s.tasks.portPolicyTask = task
	s.tasks.portPolicyCancel = cancel
	s.tasks.portPolicyMu.Unlock()

	go s.runBroadbandPortPolicyTask(ctx, duration, task.ID)
	return cloneBroadbandPortPolicyTask(task)
}

func (s *Service) GetBroadbandPortPolicyTask() BroadbandPortPolicyTaskStatus {
	s.tasks.portPolicyMu.Lock()
	defer s.tasks.portPolicyMu.Unlock()
	return cloneBroadbandPortPolicyTask(s.tasks.portPolicyTask)
}

func (s *Service) CancelBroadbandPortPolicyTask() BroadbandPortPolicyTaskStatus {
	s.tasks.portPolicyMu.Lock()
	cancel := s.tasks.portPolicyCancel
	if s.tasks.portPolicyTask.Running {
		s.tasks.portPolicyTask.Message = "正在取消端口策略测速"
		s.tasks.portPolicyTask.UpdatedAt = localTimestamp()
	}
	task := cloneBroadbandPortPolicyTask(s.tasks.portPolicyTask)
	s.tasks.portPolicyMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return task
}

func (s *Service) runBroadbandPortPolicyTask(ctx context.Context, duration time.Duration, taskID string) {
	result, completed := executeBroadbandPortPolicyTest(ctx, duration, func(update BroadbandPortPolicyTaskStatus) {
		s.tasks.portPolicyMu.Lock()
		defer s.tasks.portPolicyMu.Unlock()
		if s.tasks.portPolicyTask.ID != taskID || !s.tasks.portPolicyTask.Running {
			return
		}
		update.ID = taskID
		update.Running = true
		s.tasks.portPolicyTask = cloneBroadbandPortPolicyTask(update)
	})

	s.tasks.portPolicyMu.Lock()
	defer s.tasks.portPolicyMu.Unlock()
	if s.tasks.portPolicyTask.ID != taskID {
		return
	}
	result.ID = taskID
	result.Running = false
	result.UpdatedAt = localTimestamp()
	s.tasks.portPolicyCancel = nil
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		result.Stage = "canceled"
		result.Canceled = true
		result.Finished = false
		result.Message = "端口策略测速已取消"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		result.Stage = "error"
		result.Finished = false
		result.Error = "端口策略测速超时"
		result.Message = result.Error
	case !completed:
		result.Stage = "error"
		result.Finished = false
		if result.Error == "" {
			result.Error = "端口策略测速未完成"
		}
		result.Message = result.Error
	default:
		result.Stage = "complete"
		result.ProgressPercent = 100
		result.Finished = true
		result.Message = "端口策略测速完成"
	}
	s.tasks.portPolicyTask = cloneBroadbandPortPolicyTask(result)
}

func cloneBroadbandPortPolicyTask(task BroadbandPortPolicyTaskStatus) BroadbandPortPolicyTaskStatus {
	task.Targets = append([]BroadbandPortPolicyTargetResult(nil), task.Targets...)
	return task
}
