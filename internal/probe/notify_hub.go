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
	"sync"
	"time"

	"netwatch/internal/logger"
	"netwatch/internal/lzcsdk"
)

// notifyConfig is the mutable notification policy snapshot.
type notifyConfig struct {
	Enabled                  bool
	ClientEnabled            bool
	NotifyAbnormalTraffic    bool
	NotifyEgressChange       bool
	NotifyConnectivityChange bool
	NotifyLANDeviceChange    bool
	AbnormalTrafficThreshold int
	BarkEnabled              bool
	BarkServerURL            string
	BarkDeviceKey            string
	BarkGroup                string
	PushPlusEnabled          bool
	PushPlusToken            string
	PushPlusTopic            string
	DNDEnabled               bool
	DNDStart                 string
	DNDEnd                   string
	ScheduledEnabled         bool
	ScheduledTime            string
	DeviceIDs                []string
}

// notifyHub owns notification event fan-out, external channels and device registry.
// Service keeps a pointer and thin wrappers so HTTP handlers stay stable.
type notifyHub struct {
	mu            sync.RWMutex
	cfg           notifyConfig
	eventsMu      sync.Mutex
	events        []NotificationEvent
	nextID        int64
	subsMu        sync.Mutex
	subs          []chan NotificationEvent
	devicesMu     sync.Mutex
	devices       map[string]RegisteredDevice
	dataDir       string
	baselineMu    sync.Mutex
	baselineReady bool
	lastSummary   Summary
	trafficHigh   bool
}

func newNotifyHub(dataDir string, def MutableSettings) *notifyHub {
	h := &notifyHub{
		dataDir: dataDir,
		devices: loadRegisteredDevices(dataDir),
		cfg: notifyConfig{
			Enabled:                  def.NotificationsEnabled,
			ClientEnabled:            def.ClientNotificationEnabled,
			NotifyAbnormalTraffic:    def.NotifyAbnormalTraffic,
			NotifyEgressChange:       def.NotifyEgressChange,
			NotifyConnectivityChange: def.NotifyConnectivityChange,
			NotifyLANDeviceChange:    def.NotifyLANDeviceChange,
			AbnormalTrafficThreshold: def.AbnormalTrafficThresholdMbps,
			BarkEnabled:              def.BarkEnabled,
			BarkServerURL:            def.BarkServerURL,
			BarkDeviceKey:            def.BarkDeviceKey,
			BarkGroup:                def.BarkGroup,
			PushPlusEnabled:          def.PushPlusEnabled,
			PushPlusToken:            def.PushPlusToken,
			PushPlusTopic:            def.PushPlusTopic,
			DNDEnabled:               def.DNDEnabled,
			DNDStart:                 def.DNDStart,
			DNDEnd:                   def.DNDEnd,
			ScheduledEnabled:         def.ScheduledNotifyEnabled,
			ScheduledTime:            def.ScheduledNotifyTime,
			DeviceIDs:                append([]string(nil), def.NotificationDeviceIDs...),
		},
	}
	return h
}

func (h *notifyHub) snapshotConfig() notifyConfig {
	h.mu.RLock()
	defer h.mu.RUnlock()
	cfg := h.cfg
	cfg.DeviceIDs = append([]string(nil), h.cfg.DeviceIDs...)
	return cfg
}

func (h *notifyHub) applyFromSettings(in MutableSettings) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cfg.Enabled = in.NotificationsEnabled
	h.cfg.ClientEnabled = in.ClientNotificationEnabled
	h.cfg.NotifyAbnormalTraffic = in.NotifyAbnormalTraffic
	h.cfg.NotifyEgressChange = in.NotifyEgressChange
	h.cfg.NotifyConnectivityChange = in.NotifyConnectivityChange
	h.cfg.NotifyLANDeviceChange = in.NotifyLANDeviceChange
	if in.AbnormalTrafficThresholdMbps > 0 {
		h.cfg.AbnormalTrafficThreshold = in.AbnormalTrafficThresholdMbps
	}
	h.cfg.BarkEnabled = in.BarkEnabled
	h.cfg.BarkServerURL = strings.TrimSpace(in.BarkServerURL)
	h.cfg.BarkDeviceKey = strings.TrimSpace(in.BarkDeviceKey)
	h.cfg.BarkGroup = strings.TrimSpace(in.BarkGroup)
	h.cfg.PushPlusEnabled = in.PushPlusEnabled
	h.cfg.PushPlusToken = strings.TrimSpace(in.PushPlusToken)
	h.cfg.PushPlusTopic = strings.TrimSpace(in.PushPlusTopic)
	h.cfg.DNDEnabled = in.DNDEnabled
	h.cfg.DNDStart = normalizeHHMM(in.DNDStart, "22:00")
	h.cfg.DNDEnd = normalizeHHMM(in.DNDEnd, "08:00")
	h.cfg.ScheduledEnabled = in.ScheduledNotifyEnabled
	h.cfg.ScheduledTime = normalizeHHMM(in.ScheduledNotifyTime, "09:00")
	h.cfg.DeviceIDs = append([]string(nil), in.NotificationDeviceIDs...)
	if h.cfg.Enabled && !h.cfg.BarkEnabled && !h.cfg.ClientEnabled {
		h.cfg.ClientEnabled = true
	}
}

func (h *notifyHub) writeToSettings(out *MutableSettings) {
	cfg := h.snapshotConfig()
	out.NotificationsEnabled = cfg.Enabled
	out.ClientNotificationEnabled = cfg.ClientEnabled
	out.NotifyAbnormalTraffic = cfg.NotifyAbnormalTraffic
	out.NotifyEgressChange = cfg.NotifyEgressChange
	out.NotifyConnectivityChange = cfg.NotifyConnectivityChange
	out.NotifyLANDeviceChange = cfg.NotifyLANDeviceChange
	out.AbnormalTrafficThresholdMbps = cfg.AbnormalTrafficThreshold
	out.BarkEnabled = cfg.BarkEnabled
	out.BarkServerURL = cfg.BarkServerURL
	out.BarkDeviceKey = cfg.BarkDeviceKey
	out.BarkGroup = cfg.BarkGroup
	out.PushPlusEnabled = cfg.PushPlusEnabled
	out.PushPlusToken = cfg.PushPlusToken
	out.PushPlusTopic = cfg.PushPlusTopic
	out.DNDEnabled = cfg.DNDEnabled
	out.DNDStart = cfg.DNDStart
	out.DNDEnd = cfg.DNDEnd
	out.ScheduledNotifyEnabled = cfg.ScheduledEnabled
	out.ScheduledNotifyTime = cfg.ScheduledTime
	out.NotificationDeviceIDs = append([]string(nil), cfg.DeviceIDs...)
}

func (h *notifyHub) subscribe() (<-chan NotificationEvent, func()) {
	ch := make(chan NotificationEvent, 8)
	h.subsMu.Lock()
	h.subs = append(h.subs, ch)
	h.subsMu.Unlock()
	return ch, func() {
		h.subsMu.Lock()
		defer h.subsMu.Unlock()
		for i, c := range h.subs {
			if c == ch {
				h.subs = append(h.subs[:i], h.subs[i+1:]...)
				close(ch)
				return
			}
		}
	}
}

func (h *notifyHub) eventsSince(sinceID int64) []NotificationEvent {
	h.eventsMu.Lock()
	defer h.eventsMu.Unlock()
	out := make([]NotificationEvent, 0, len(h.events))
	for _, ev := range h.events {
		if ev.ID > sinceID {
			out = append(out, ev)
		}
	}
	return out
}

func (h *notifyHub) push(kind, severity, title, body string) {
	cfg := h.snapshotConfig()
	if !cfg.Enabled {
		return
	}
	if cfg.DNDEnabled && isWithinDNDPeriod(cfg.DNDStart, cfg.DNDEnd) {
		if severity != "error" {
			return
		}
	}
	ev := NotificationEvent{
		Kind:      kind,
		Severity:  severity,
		Title:     title,
		Body:      body,
		CreatedAt: localTimestamp(),
	}
	h.eventsMu.Lock()
	h.nextID++
	ev.ID = h.nextID
	h.events = append(h.events, ev)
	if len(h.events) > maxNotificationEvents {
		h.events = h.events[len(h.events)-maxNotificationEvents:]
	}
	h.eventsMu.Unlock()

	h.subsMu.Lock()
	subs := append([]chan NotificationEvent(nil), h.subs...)
	h.subsMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
		}
	}

	h.dispatchExternal(cfg, ev)
}

func (h *notifyHub) dispatchExternal(cfg notifyConfig, ev NotificationEvent) {
	if cfg.BarkEnabled && cfg.BarkServerURL != "" && cfg.BarkDeviceKey != "" {
		go func() {
			if err := sendBarkNotification(context.Background(), cfg.BarkServerURL, cfg.BarkDeviceKey, cfg.BarkGroup, ev); err != nil {
				logger.Warn("bark push failed: %v", err)
			}
		}()
	}
	if cfg.PushPlusEnabled && cfg.PushPlusToken != "" {
		go func() {
			if err := sendPushPlusNotification(context.Background(), cfg.PushPlusToken, cfg.PushPlusTopic, ev); err != nil {
				logger.Warn("pushplus push failed: %v", err)
			}
		}()
	}
	if cfg.ClientEnabled {
		go func() {
			if err := h.sendLazycatNotification(cfg, ev); err != nil {
				logger.Debug("lazycat client notify: %v", err)
			}
		}()
	}
}

func (h *notifyHub) sendLazycatNotification(cfg notifyConfig, ev NotificationEvent) error {
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
		if !h.shouldNotifyDevice(cfg, dev.ID) {
			continue
		}
		if err := lzcsdk.NotifyDevice(ctx, dev.DeviceAPIURL, ev.Title, ev.Body, ev.DeeplinkURL); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func (h *notifyHub) shouldNotifyDevice(cfg notifyConfig, deviceID string) bool {
	if len(cfg.DeviceIDs) == 0 {
		return true
	}
	for _, id := range cfg.DeviceIDs {
		if id == deviceID {
			return true
		}
	}
	return false
}

func (h *notifyHub) testBark() error {
	cfg := h.snapshotConfig()
	if strings.TrimSpace(cfg.BarkDeviceKey) == "" {
		return fmt.Errorf("bark device key is empty")
	}
	return sendBarkNotification(context.Background(), cfg.BarkServerURL, cfg.BarkDeviceKey, cfg.BarkGroup, NotificationEvent{
		Severity:  "info",
		Title:     "Netwatch 测试通知",
		Body:      fmt.Sprintf("Bark 推送通道测试成功，通知功能正常工作。\n\n服务地址：%s\n分组名称：%s\n测试时间：%s", cfg.BarkServerURL, firstNonEmpty(cfg.BarkGroup, "Netwatch"), localTimestamp()),
		CreatedAt: localTimestamp(),
	})
}

func (h *notifyHub) testPushPlus() error {
	cfg := h.snapshotConfig()
	if strings.TrimSpace(cfg.PushPlusToken) == "" {
		return fmt.Errorf("pushplus token is empty")
	}
	return sendPushPlusNotification(context.Background(), cfg.PushPlusToken, cfg.PushPlusTopic, NotificationEvent{
		Severity:  "info",
		Title:     "Netwatch 测试通知",
		Body:      fmt.Sprintf("PushPlus 推送通道测试成功，通知功能正常工作。\n\n分组：%s\n测试时间：%s", firstNonEmpty(cfg.PushPlusTopic, "默认"), localTimestamp()),
		CreatedAt: localTimestamp(),
	})
}

func (h *notifyHub) registerDevice(id, name, platform string) []RegisteredDevice {
	if id == "" {
		return h.listDevices()
	}
	h.devicesMu.Lock()
	defer h.devicesMu.Unlock()
	now := localTimestamp()
	if dev, ok := h.devices[id]; ok {
		dev.LastSeen = now
		dev.Name = firstNonEmpty(name, dev.Name)
		dev.Platform = firstNonEmpty(platform, dev.Platform)
		h.devices[id] = dev
	} else {
		h.devices[id] = RegisteredDevice{
			ID:        id,
			Name:      name,
			Platform:  platform,
			FirstSeen: now,
			LastSeen:  now,
			Notify:    true,
		}
	}
	_ = h.saveDevicesLocked()
	return h.listDevicesLocked()
}

func (h *notifyHub) listDevices() []RegisteredDevice {
	h.devicesMu.Lock()
	defer h.devicesMu.Unlock()
	return h.listDevicesLocked()
}

func (h *notifyHub) listDevicesLocked() []RegisteredDevice {
	out := make([]RegisteredDevice, 0, len(h.devices))
	for _, dev := range h.devices {
		out = append(out, dev)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastSeen > out[j].LastSeen
	})
	return out
}

func (h *notifyHub) saveDevicesLocked() error {
	if h.dataDir == "" {
		return nil
	}
	return writeJSONFile(registeredDevicesPath(h.dataDir), registeredDevicesStore{Devices: h.devices}, true)
}

func (h *notifyHub) setDeviceIDs(ids []string) {
	h.mu.Lock()
	h.cfg.DeviceIDs = append([]string(nil), ids...)
	h.mu.Unlock()
}

func (h *notifyHub) deviceIDs() []string {
	return h.snapshotConfig().DeviceIDs
}

func (h *notifyHub) evaluateRules(cur Summary, sampleTraffic func() RealtimeNetStats) {
	cfg := h.snapshotConfig()
	h.baselineMu.Lock()
	prevReady := h.baselineReady
	prev := h.lastSummary
	h.baselineReady = true
	h.lastSummary = cur
	h.baselineMu.Unlock()

	if !prevReady {
		return
	}

	if cfg.NotifyEgressChange {
		if prev.NetworkInfo.EgressIPv4 != "" && cur.NetworkInfo.EgressIPv4 != "" && prev.NetworkInfo.EgressIPv4 != cur.NetworkInfo.EgressIPv4 {
			h.push("egress_ipv4_changed", "warn", "出口 IPv4 发生变化",
				fmt.Sprintf("公网出口 IPv4 地址已变更，可能存在网络切换或运营商分配变化。\n\n原地址：%s\n新地址：%s\n检测时间：%s",
					prev.NetworkInfo.EgressIPv4, cur.NetworkInfo.EgressIPv4, localTimestamp()))
		}
		if prev.NetworkInfo.EgressIPv6 != "" && cur.NetworkInfo.EgressIPv6 != "" && prev.NetworkInfo.EgressIPv6 != cur.NetworkInfo.EgressIPv6 {
			h.push("egress_ipv6_changed", "warn", "出口 IPv6 发生变化",
				fmt.Sprintf("公网出口 IPv6 地址已变更。\n\n原地址：%s\n新地址：%s\n检测时间：%s",
					prev.NetworkInfo.EgressIPv6, cur.NetworkInfo.EgressIPv6, localTimestamp()))
		}
	}

	if cfg.NotifyConnectivityChange {
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
			h.push("global_connectivity_changed", severity, title, body)
		}
	}

	if cfg.NotifyAbnormalTraffic && sampleTraffic != nil {
		h.evaluateTraffic(cfg.AbnormalTrafficThreshold, sampleTraffic)
	}
}

func (h *notifyHub) evaluateTraffic(thresholdMbps int, sampleTraffic func() RealtimeNetStats) {
	if thresholdMbps <= 0 {
		thresholdMbps = 100
	}
	stats := sampleTraffic()
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

	h.baselineMu.Lock()
	wasHigh := h.trafficHigh
	h.trafficHigh = high
	h.baselineMu.Unlock()

	if high && !wasHigh {
		mbps := float64(peak.RxBps+peak.TxBps) * 8 / 1000 / 1000
		rxMbps := float64(peak.RxBps) * 8 / 1000 / 1000
		txMbps := float64(peak.TxBps) * 8 / 1000 / 1000
		h.push("abnormal_traffic", "warn", "检测到异常流量",
			fmt.Sprintf("网卡「%s」流量超过设定阈值，可能存在大文件传输、备份或异常占用。\n\n当前速率：%.2f Mbps（↓ %.2f / ↑ %.2f）\n告警阈值：%d Mbps\n检测时间：%s",
				peak.Name, mbps, rxMbps, txMbps, thresholdMbps, localTimestamp()))
	}
}

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
	if store.Devices == nil {
		return out
	}
	return store.Devices
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

func sendPushPlusNotification(ctx context.Context, token, topic string, ev NotificationEvent) error {
	payload := map[string]any{
		"token":    token,
		"title":    ev.Title,
		"content":  strings.ReplaceAll(ev.Body, "\n", "<br>"),
		"template": "txt",
	}
	if topic != "" {
		payload["topic"] = topic
	}
	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.pushplus.plus/send", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	_ = json.Unmarshal(respBody, &result)
	if result.Code != 200 {
		msg := strings.TrimSpace(result.Msg)
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("pushplus: %s", msg)
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
