package probe

import (
	"context"
	"fmt"
	"time"
)

func (s *Service) SubscribeNotifications() (<-chan NotificationEvent, func()) {
	return s.notify.subscribe()
}

func (s *Service) GetNotificationEvents(sinceID int64) []NotificationEvent {
	return s.notify.eventsSince(sinceID)
}

func (s *Service) pushNotification(kind, severity, title, body string) {
	s.notify.push(kind, severity, title, body)
}

func (s *Service) TestBarkNotification() error {
	return s.notify.testBark()
}

func (s *Service) TestPushPlusNotification() error {
	return s.notify.testPushPlus()
}

func (s *Service) startScheduledNotifier() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		var lastSentDate string
		for {
			select {
			case <-s.backgroundMonitorStop:
				return
			case <-ticker.C:
				cfg := s.notify.snapshotConfig()
				if !cfg.ScheduledEnabled || !cfg.Enabled || cfg.ScheduledTime == "" {
					continue
				}
				now := time.Now()
				hhmm := fmt.Sprintf("%02d:%02d", now.Hour(), now.Minute())
				today := now.Format("2006-01-02")
				if hhmm == cfg.ScheduledTime && lastSentDate != today {
					lastSentDate = today
					s.sendScheduledSummary()
				}
			}
		}
	}()
}

func (s *Service) sendScheduledSummary() {
	sum := s.GetSummary()
	snap := s.GetLANDevices()

	title := "Netwatch 每日网络摘要"
	body := fmt.Sprintf("📅 %s\n\n", localTimestamp())
	if sum.Ready {
		body += fmt.Sprintf("🌐 全球互联：%s\n", statusText(sum.WebsiteConnectivity.GlobalStatus))
	}
	if sum.NetworkInfo.EgressIPv4 != "" {
		body += fmt.Sprintf("🔗 出口 IPv4：%s\n", sum.NetworkInfo.EgressIPv4)
	}
	if sum.NetworkInfo.EgressIPv6 != "" {
		body += fmt.Sprintf("🔗 出口 IPv6：%s\n", sum.NetworkInfo.EgressIPv6)
	}
	body += fmt.Sprintf("\n📱 局域网设备：%d 在线 / %d 离线", snap.Online, snap.Offline)
	s.pushNotification("scheduled_summary", "info", title, body)
}

func (s *Service) startBackgroundMonitor() {
	go func() {
		defer close(s.backgroundMonitorDone)
		for {
			enabled, intervalSec := s.settings.backgroundConfig()

			wait := time.Second
			if enabled {
				if intervalSec < 10 {
					intervalSec = 60
				}
				wait = time.Duration(intervalSec) * time.Second
			}

			timer := time.NewTimer(wait)
			select {
			case <-s.backgroundMonitorStop:
				timer.Stop()
				return
			case <-timer.C:
				if enabled {
					s.runBackgroundMonitorTick()
				}
			}
		}
	}()
}

const connectivityProbeInterval = 300 * time.Second // 出口IP/互联检测固定5分钟

func (s *Service) runBackgroundMonitorTick() {
	_, intervalSec := s.settings.backgroundConfig()
	interval := time.Duration(intervalSec) * time.Second
	if interval < 10*time.Second {
		interval = 60 * time.Second
	}

	ctx, cancel := context.WithTimeout(s.backgroundCtx(), interval)
	defer cancel()

	now := time.Now()
	cfg := s.notify.snapshotConfig()
	s.mu.RLock()
	runConnectivity := now.Sub(s.lastConnectivityProbe) >= connectivityProbeInterval
	s.mu.RUnlock()

	if runConnectivity {
		s.mu.Lock()
		s.lastConnectivityProbe = now
		s.mu.Unlock()
		if cfg.NotifyEgressChange {
			s.ClearPublicIPCache()
		}
	}

	// Egress is refreshed in the background only when the user explicitly
	// enabled egress-change notifications; ordinary page/background refreshes
	// preserve the existing public identity.
	summary, err := s.collectFastSummaryConditional(ctx, runConnectivity, runConnectivity && cfg.NotifyEgressChange)
	if err != nil {
		s.pushNotification("background_probe_failed", "warn", "Netwatch 后台检测失败",
			fmt.Sprintf("后台定时检测出错，请检查服务状态。\n错误信息：%s", err.Error()))
		return
	}

	s.mu.Lock()
	previousSummary := s.summary
	summary.NetworkInfo.NAT = s.summary.NetworkInfo.NAT
	s.summary = summary
	s.summary.Ready = true
	s.summary.LastError = ""
	s.summary.RefreshIntervalSec = int64(s.cfg.RefreshInterval / time.Second)
	finalSummary := s.summary
	s.mu.Unlock()

	s.recordSummaryEvents(previousSummary, finalSummary)
	s.recordTimeseries(finalSummary)
	s.notify.evaluateRules(finalSummary, func() RealtimeNetStats {
		return s.nicStats.sampleAndSnapshot()
	})
	if cfg.NotifyLANDeviceChange {
		_ = s.scanLANDevices(ctx, true)
	}
	s.alert.check(finalSummary)
	s.broadcast(finalSummary)
}

func (s *Service) evaluateNotificationRules(cur Summary) {
	s.notify.evaluateRules(cur, func() RealtimeNetStats {
		return s.nicStats.sampleAndSnapshot()
	})
}

func (s *Service) RegisterDevice(id, name, platform string) []RegisteredDevice {
	return s.notify.registerDevice(id, name, platform)
}

func (s *Service) GetRegisteredDevices() []RegisteredDevice {
	return s.notify.listDevices()
}

func (s *Service) SetNotificationDeviceIDs(ids []string) {
	s.notify.setDeviceIDs(ids)
}

func (s *Service) GetNotificationDeviceIDs() []string {
	return s.notify.deviceIDs()
}

func (s *Service) ShouldNotifyDevice(deviceID string) bool {
	return s.notify.shouldNotifyDevice(s.notify.snapshotConfig(), deviceID)
}
