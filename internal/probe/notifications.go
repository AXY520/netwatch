package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"netwatch/internal/logger"
	"netwatch/internal/lzcsdk"
)

func (s *Service) SubscribeNotifications() (<-chan NotificationEvent, func()) {
	ch := make(chan NotificationEvent, 8)
	s.notificationSubsMu.Lock()
	s.notificationSubs = append(s.notificationSubs, ch)
	s.notificationSubsMu.Unlock()

	unsub := func() {
		s.notificationSubsMu.Lock()
		defer s.notificationSubsMu.Unlock()
		for i, c := range s.notificationSubs {
			if c == ch {
				s.notificationSubs = append(s.notificationSubs[:i], s.notificationSubs[i+1:]...)
				close(ch)
				return
			}
		}
	}
	return ch, unsub
}

func (s *Service) GetNotificationEvents(sinceID int64) []NotificationEvent {
	s.notificationMu.Lock()
	defer s.notificationMu.Unlock()
	out := make([]NotificationEvent, 0, len(s.notificationEvents))
	for _, ev := range s.notificationEvents {
		if ev.ID > sinceID {
			out = append(out, ev)
		}
	}
	return out
}

func (s *Service) pushNotification(kind, severity, title, body string) {
	s.mu.RLock()
	enabled := s.notificationsEnabled
	dndEnabled := s.dndEnabled
	dndStart := s.dndStart
	dndEnd := s.dndEnd
	tmplTitle := s.notifyTemplateTitle
	tmplBody := s.notifyTemplateBody
	s.mu.RUnlock()
	if !enabled {
		return
	}
	if dndEnabled && isWithinDNDPeriod(dndStart, dndEnd) {
		return
	}

	// Apply custom templates if configured.
	if tmplTitle != "" {
		if rendered, err := renderNotifyTemplate(tmplTitle, kind, severity, title, body); err == nil {
			title = rendered
		}
	}
	if tmplBody != "" {
		if rendered, err := renderNotifyTemplate(tmplBody, kind, severity, title, body); err == nil {
			body = rendered
		}
	}

	ev := NotificationEvent{
		Kind:      kind,
		Severity:  severity,
		Title:     title,
		Body:      body,
		CreatedAt: localTimestamp(),
	}
	s.notificationMu.Lock()
	s.notificationNextID++
	ev.ID = s.notificationNextID
	s.notificationEvents = append(s.notificationEvents, ev)
	if len(s.notificationEvents) > maxNotificationEvents {
		s.notificationEvents = s.notificationEvents[len(s.notificationEvents)-maxNotificationEvents:]
	}
	s.notificationMu.Unlock()

	s.notificationSubsMu.Lock()
	subs := append([]chan NotificationEvent(nil), s.notificationSubs...)
	s.notificationSubsMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
		}
	}
	s.sendExternalNotification(ev)
}

func (s *Service) sendExternalNotification(ev NotificationEvent) {
	s.mu.RLock()
	barkEnabled := s.barkEnabled
	serverURL := s.barkServerURL
	deviceKey := s.barkDeviceKey
	group := s.barkGroup
	clientEnabled := s.clientNotificationEnabled
	s.mu.RUnlock()

	if barkEnabled && serverURL != "" && deviceKey != "" {
		go func() {
			if err := sendBarkNotification(context.Background(), serverURL, deviceKey, group, ev); err != nil {
				logger.Warn("bark push failed: %v", err)
			}
		}()
	}

	if clientEnabled {
		go func() {
			if err := s.sendLazycatNotification(ev); err != nil {
				logger.Debug("lazycat client notify: %v", err)
			}
		}()
	}
}

func (s *Service) sendLazycatNotification(ev NotificationEvent) error {
	if !lzcsdk.GatewayAvailable() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	devices, err := lzcsdk.ListDevices(ctx)
	if err != nil {
		return fmt.Errorf("list devices: %w", err)
	}
	var lastErr error
	for _, dev := range devices {
		if !dev.IsOnline || dev.DeviceAPIURL == "" {
			continue
		}
		if !s.ShouldNotifyDevice(dev.ID) {
			continue
		}
		if err := lzcsdk.NotifyDevice(ctx, dev.DeviceAPIURL, ev.Title, ev.Body, ev.DeeplinkURL); err != nil {
			logger.Debug("notify device %s failed: %v", dev.ID, err)
			lastErr = err
		}
	}
	return lastErr
}

func (s *Service) TestBarkNotification() error {
	s.mu.RLock()
	serverURL := s.barkServerURL
	deviceKey := s.barkDeviceKey
	group := s.barkGroup
	s.mu.RUnlock()
	if strings.TrimSpace(deviceKey) == "" {
		return fmt.Errorf("bark device key is empty")
	}
	return sendBarkNotification(context.Background(), serverURL, deviceKey, group, NotificationEvent{
		Severity:  "info",
		Title:     "Netwatch 测试通知",
		Body:      fmt.Sprintf("Bark 推送通道测试成功，通知功能正常工作。\n\n服务地址：%s\n分组名称：%s\n测试时间：%s", serverURL, firstNonEmpty(group, "Netwatch"), localTimestamp()),
		CreatedAt: localTimestamp(),
	})
}

func sendBarkNotification(ctx context.Context, serverURL, deviceKey, group string, ev NotificationEvent) error {
	endpoint, err := buildBarkEndpoint(serverURL, deviceKey)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"title": ev.Title,
		"body":  ev.Body,
		"group": firstNonEmpty(group, "Netwatch"),
		"level": barkLevel(ev.Severity),
		"sound": barkSound(ev.Severity),
		"icon":  "https://img.icons8.com/fluency/96/network.png",
	}
	if ev.CreatedAt != "" {
		payload["timestamp"] = ev.CreatedAt
	}
	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		message := strings.TrimSpace(string(respBody))
		if message == "" {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, message)
	}
	return nil
}

func buildBarkEndpoint(raw, deviceKey string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty server url")
	}
	deviceKey = strings.TrimSpace(deviceKey)
	if deviceKey == "" {
		return "", fmt.Errorf("bark device key is empty")
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	basePath := strings.TrimRight(u.Path, "/")
	if strings.HasSuffix(basePath, "/push") {
		basePath = strings.TrimRight(strings.TrimSuffix(basePath, "/push"), "/")
	}
	u.Path = basePath + "/" + url.PathEscape(deviceKey)
	u.RawPath = ""
	return u.String(), nil
}

func normalizeHHMM(value, fallback string) string {
	v := strings.TrimSpace(value)
	if len(v) == 5 && v[2] == ':' {
		h, herr := strconv.Atoi(v[:2])
		m, merr := strconv.Atoi(v[3:])
		if herr == nil && merr == nil && h >= 0 && h <= 23 && m >= 0 && m <= 59 {
			return fmt.Sprintf("%02d:%02d", h, m)
		}
	}
	return fallback
}

func isWithinDNDPeriod(start, end string) bool {
	now := time.Now()
	hhmm := fmt.Sprintf("%02d:%02d", now.Hour(), now.Minute())
	if start <= end {
		return hhmm >= start && hhmm < end
	}
	return hhmm >= start || hhmm < end
}

type notifyTemplateData struct {
	Kind     string
	Severity string
	Title    string
	Body     string
}

func renderNotifyTemplate(tmplStr, kind, severity, title, body string) (string, error) {
	t, err := template.New("").Parse(tmplStr)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, notifyTemplateData{Kind: kind, Severity: severity, Title: title, Body: body}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func barkLevel(severity string) string {
	switch severity {
	case "warn", "error":
		return "timeSensitive"
	default:
		return "active"
	}
}

func barkSound(severity string) string {
	switch severity {
	case "warn", "error":
		return "alarm"
	default:
		return "chime"
	}
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
				s.mu.RLock()
				enabled := s.scheduledNotifyEnabled
				targetTime := s.scheduledNotifyTime
				notifEnabled := s.notificationsEnabled
				s.mu.RUnlock()
				if !enabled || !notifEnabled || targetTime == "" {
					continue
				}
				now := time.Now()
				hhmm := fmt.Sprintf("%02d:%02d", now.Hour(), now.Minute())
				today := now.Format("2006-01-02")
				if hhmm == targetTime && lastSentDate != today {
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

	// Connectivity
	if sum.Ready {
		body += fmt.Sprintf("🌐 全球互联：%s\n", statusText(sum.WebsiteConnectivity.GlobalStatus))
	}

	// Egress
	if sum.NetworkInfo.EgressIPv4 != "" {
		body += fmt.Sprintf("🔗 出口 IPv4：%s\n", sum.NetworkInfo.EgressIPv4)
	}
	if sum.NetworkInfo.EgressIPv6 != "" {
		body += fmt.Sprintf("🔗 出口 IPv6：%s\n", sum.NetworkInfo.EgressIPv6)
	}

	// LAN devices
	body += fmt.Sprintf("\n📱 局域网设备：%d 在线 / %d 离线", snap.Online, snap.Offline)

	s.pushNotification("scheduled_summary", "info", title, body)
}

func (s *Service) startBackgroundMonitor() {
	go func() {
		defer close(s.backgroundMonitorDone)
		for {
			s.mu.RLock()
			enabled := s.backgroundMonitorEnabled
			intervalSec := s.backgroundMonitorInterval
			s.mu.RUnlock()

			wait := time.Second
			if enabled {
				if intervalSec < 10 {
					intervalSec = 60
				}
				s.runBackgroundMonitorTick()
				wait = time.Duration(intervalSec) * time.Second
			}

			timer := time.NewTimer(wait)
			select {
			case <-s.backgroundMonitorStop:
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

const connectivityProbeInterval = 300 * time.Second // 出口IP/互联检测固定5分钟

func (s *Service) runBackgroundMonitorTick() {
	s.mu.RLock()
	interval := time.Duration(s.backgroundMonitorInterval) * time.Second
	s.mu.RUnlock()
	if interval < 10*time.Second {
		interval = 60 * time.Second
	}

	ctx, cancel := context.WithTimeout(s.backgroundCtx(), interval)
	defer cancel()

	now := time.Now()
	s.mu.RLock()
	notifyEgress := s.notifyEgressChange
	notifyLAN := s.notifyLANDeviceChange
	runConnectivity := now.Sub(s.lastConnectivityProbe) >= connectivityProbeInterval
	s.mu.RUnlock()

	if runConnectivity {
		s.mu.Lock()
		s.lastConnectivityProbe = now
		s.mu.Unlock()
		if notifyEgress {
			s.ClearPublicIPCache()
		}
	}

	summary, err := s.collectFastSummaryConditional(ctx, runConnectivity)
	if err != nil {
		s.pushNotification("background_probe_failed", "warn", "Netwatch 后台检测失败",
			fmt.Sprintf("后台定时检测出错，请检查服务状态。\n错误信息：%s", err.Error()))
		return
	}

	s.mu.Lock()
	summary.NetworkInfo.NAT = s.summary.NetworkInfo.NAT
	s.summary = summary
	s.summary.Ready = true
	s.summary.LastError = ""
	s.summary.RefreshIntervalSec = int64(s.cfg.RefreshInterval / time.Second)
	finalSummary := s.summary
	s.mu.Unlock()

	s.recordTimeseries(finalSummary)
	s.evaluateNotificationRules(finalSummary)
	if notifyLAN {
		_ = s.scanLANDevices(ctx, true)
	}
	s.alert.check(finalSummary)
	s.broadcast(finalSummary)
}

func (s *Service) evaluateNotificationRules(cur Summary) {
	s.mu.Lock()
	prevReady := s.monitorBaselineReady
	prev := s.monitorLastSummary
	notifyTraffic := s.notifyAbnormalTraffic
	notifyEgress := s.notifyEgressChange
	notifyConnectivity := s.notifyConnectivityChange
	thresholdMbps := s.abnormalTrafficThreshold
	s.monitorBaselineReady = true
	s.monitorLastSummary = cur
	s.mu.Unlock()

	if !prevReady {
		return
	}

	if notifyEgress {
		if prev.NetworkInfo.EgressIPv4 != "" && cur.NetworkInfo.EgressIPv4 != "" && prev.NetworkInfo.EgressIPv4 != cur.NetworkInfo.EgressIPv4 {
			s.pushNotification("egress_ipv4_changed", "warn", "出口 IPv4 发生变化",
				fmt.Sprintf("公网出口 IPv4 地址已变更，可能存在网络切换或运营商分配变化。\n\n原地址：%s\n新地址：%s\n检测时间：%s",
					prev.NetworkInfo.EgressIPv4, cur.NetworkInfo.EgressIPv4, localTimestamp()))
		}
		if prev.NetworkInfo.EgressIPv6 != "" && cur.NetworkInfo.EgressIPv6 != "" && prev.NetworkInfo.EgressIPv6 != cur.NetworkInfo.EgressIPv6 {
			s.pushNotification("egress_ipv6_changed", "warn", "出口 IPv6 发生变化",
				fmt.Sprintf("公网出口 IPv6 地址已变更。\n\n原地址：%s\n新地址：%s\n检测时间：%s",
					prev.NetworkInfo.EgressIPv6, cur.NetworkInfo.EgressIPv6, localTimestamp()))
		}
	}

	if notifyConnectivity {
		if prev.WebsiteConnectivity.GlobalStatus != "" && cur.WebsiteConnectivity.GlobalStatus != "" && prev.WebsiteConnectivity.GlobalStatus != cur.WebsiteConnectivity.GlobalStatus {
			title := "全球互联状态更新"
			severity := "info"
			detail := ""
			if cur.WebsiteConnectivity.GlobalStatus == StatusDown {
				severity = "warn"
				title = "全球互联已断开"
				detail = "国际互联连通性检测失败，可能存在网络故障或 DNS 问题。"
			} else if prev.WebsiteConnectivity.GlobalStatus == StatusDown && cur.WebsiteConnectivity.GlobalStatus == StatusOK {
				title = "全球互联已恢复"
				detail = "国际互联连通性已恢复正常。"
			} else {
				detail = fmt.Sprintf("互联状态从「%s」变为「%s」。", statusText(prev.WebsiteConnectivity.GlobalStatus), statusText(cur.WebsiteConnectivity.GlobalStatus))
			}
			body := fmt.Sprintf("%s\n\n状态变化：%s → %s\n检测时间：%s",
				detail,
				statusText(prev.WebsiteConnectivity.GlobalStatus),
				statusText(cur.WebsiteConnectivity.GlobalStatus),
				localTimestamp())
			s.pushNotification("global_connectivity_changed", severity, title, body)
		}
	}

	if notifyTraffic {
		s.evaluateTrafficNotification(thresholdMbps)
	}
}

func (s *Service) evaluateTrafficNotification(thresholdMbps int) {
	if thresholdMbps <= 0 {
		thresholdMbps = 100
	}
	stats := s.nicStats.sampleAndSnapshot()
	var peak NICThroughput
	for _, nic := range stats.NICs {
		if nic.OperState != "up" {
			continue
		}
		if nic.RxBps+nic.TxBps > peak.RxBps+peak.TxBps {
			peak = nic
		}
	}
	thresholdBytes := int64(thresholdMbps) * 1000 * 1000 / 8
	high := peak.RxBps+peak.TxBps >= thresholdBytes

	s.mu.Lock()
	wasHigh := s.monitorTrafficHigh
	s.monitorTrafficHigh = high
	s.mu.Unlock()

	if high && !wasHigh {
		mbps := float64(peak.RxBps+peak.TxBps) * 8 / 1000 / 1000
		rxMbps := float64(peak.RxBps) * 8 / 1000 / 1000
		txMbps := float64(peak.TxBps) * 8 / 1000 / 1000
		s.pushNotification("abnormal_traffic", "warn", "检测到异常流量",
			fmt.Sprintf("网卡「%s」流量超过设定阈值，可能存在大文件传输、备份或异常占用。\n\n当前速率：%.2f Mbps（↓ %.2f / ↑ %.2f）\n告警阈值：%d Mbps\n检测时间：%s",
				peak.Name, mbps, rxMbps, txMbps, thresholdMbps, localTimestamp()))
	}
}

func statusText(status ProbeStatus) string {
	switch status {
	case StatusOK:
		return "正常"
	case StatusDown:
		return "故障"
	case StatusDegraded:
		return "降级"
	case StatusUnknown:
		return "未知"
	default:
		return string(status)
	}
}

// --- Device registration for client notifications ---

func registeredDevicesPath(dataDir string) string {
	return filepath.Join(dataDir, "registered_devices.json")
}

type registeredDevicesStore struct {
	Devices map[string]RegisteredDevice `json:"devices"`
}

func loadRegisteredDevices(dataDir string) map[string]RegisteredDevice {
	out := map[string]RegisteredDevice{}
	if dataDir == "" {
		return out
	}
	body, err := os.ReadFile(registeredDevicesPath(dataDir))
	if err != nil {
		return out
	}
	var store registeredDevicesStore
	if err := json.Unmarshal(body, &store); err != nil {
		return out
	}
	return store.Devices
}

func (s *Service) saveRegisteredDevices() error {
	if s.cfg.DataDir == "" {
		return nil
	}
	return writeJSONFile(registeredDevicesPath(s.cfg.DataDir), registeredDevicesStore{Devices: s.registeredDevices}, true)
}

func (s *Service) RegisterDevice(id, name, platform string) []RegisteredDevice {
	if id == "" {
		return s.GetRegisteredDevices()
	}
	s.registeredDevicesMu.Lock()
	defer s.registeredDevicesMu.Unlock()
	now := localTimestamp()
	if dev, ok := s.registeredDevices[id]; ok {
		dev.LastSeen = now
		dev.Name = firstNonEmpty(name, dev.Name)
		dev.Platform = firstNonEmpty(platform, dev.Platform)
		s.registeredDevices[id] = dev
	} else {
		s.registeredDevices[id] = RegisteredDevice{
			ID:        id,
			Name:      name,
			Platform:  platform,
			FirstSeen: now,
			LastSeen:  now,
			Notify:    true,
		}
	}
	_ = s.saveRegisteredDevices()
	return s.getRegisteredDevicesLocked()
}

func (s *Service) GetRegisteredDevices() []RegisteredDevice {
	s.registeredDevicesMu.Lock()
	defer s.registeredDevicesMu.Unlock()
	return s.getRegisteredDevicesLocked()
}

func (s *Service) getRegisteredDevicesLocked() []RegisteredDevice {
	out := make([]RegisteredDevice, 0, len(s.registeredDevices))
	for _, dev := range s.registeredDevices {
		out = append(out, dev)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastSeen > out[j].LastSeen
	})
	return out
}

func (s *Service) SetNotificationDeviceIDs(ids []string) {
	s.mu.Lock()
	s.notificationDeviceIDs = ids
	s.mu.Unlock()
}

func (s *Service) GetNotificationDeviceIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.notificationDeviceIDs
}

func (s *Service) ShouldNotifyDevice(deviceID string) bool {
	s.mu.RLock()
	ids := s.notificationDeviceIDs
	s.mu.RUnlock()
	if len(ids) == 0 {
		return true
	}
	for _, id := range ids {
		if id == deviceID {
			return true
		}
	}
	return false
}
