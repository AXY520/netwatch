package probe

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func settingsPath(dataDir string) string {
	return filepath.Join(dataDir, "settings.json")
}

// DefaultMutableSettings is the single source of truth for settings defaults.
// NewService, loadMutableSettings, and GetMutableSettings fall backs all derive from here.
func DefaultMutableSettings() MutableSettings {
	return MutableSettings{
		RefreshIntervalSec:             10,
		NICRealtimeEnabled:             true,
		NICRealtimeIntervalSec:         1,
		BackgroundMonitorEnabled:       false,
		BackgroundMonitorIntervalSec:   60,
		NotificationsEnabled:           false,
		ClientNotificationEnabled:      true,
		NotifyAbnormalTraffic:          false,
		NotifyEgressChange:             true,
		NotifyConnectivityChange:       true,
		NotifyLANDeviceChange:          true,
		LANDeviceOfflineAfterSec:       180,
		LANDeviceOnlineAfterSec:        0,
		LANDeviceOfflineNotifyDelaySec: 120,
		LANDeviceOnlineNotifyDelaySec:  120,
		AbnormalTrafficThresholdMbps:   100,
		BarkEnabled:                    false,
		BarkServerURL:                  "https://api.day.app",
		BarkGroup:                      "Netwatch",
		PushPlusEnabled:                false,
		DNDEnabled:                     false,
		DNDStart:                       "22:00",
		DNDEnd:                         "08:00",
		ScheduledNotifyEnabled:         false,
		ScheduledNotifyTime:            "09:00",
		LANMaxCheckAttempts:            3,
		LANNotifyCooldownSec:           600,
		LANFlappingThreshold:           5,
		LANFlappingWindowSec:           600,
		LANDeviceAutoRemoveDays:        30,
		ChartTimeLabelInterval:         0,
		AppTrafficRealtimeEnabled:      true,
		HostNetworkExperimentalEnabled: false,
		AppProxy:                       defaultAppProxySettings(),
	}
}

func loadMutableSettings(dataDir string) (MutableSettings, bool) {
	body, err := os.ReadFile(settingsPath(dataDir))
	if err != nil {
		return DefaultMutableSettings(), false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return DefaultMutableSettings(), false
	}
	s := DefaultMutableSettings()
	if err := json.Unmarshal(body, &s); err != nil {
		return DefaultMutableSettings(), false
	}
	// Re-apply defaults for keys absent from the on-disk document so new fields
	// introduced later do not silently stay zero-valued after upgrade.
	def := DefaultMutableSettings()
	if _, ok := raw["background_monitor_interval_sec"]; !ok {
		s.BackgroundMonitorIntervalSec = def.BackgroundMonitorIntervalSec
	}
	if _, ok := raw["notifications_enabled"]; !ok {
		// Historical behavior: notifications followed background monitor when unset.
		s.NotificationsEnabled = s.BackgroundMonitorEnabled
	}
	if _, ok := raw["client_notification_enabled"]; !ok {
		s.ClientNotificationEnabled = def.ClientNotificationEnabled
	}
	if _, ok := raw["notify_abnormal_traffic"]; !ok {
		s.NotifyAbnormalTraffic = def.NotifyAbnormalTraffic
	}
	if _, ok := raw["notify_egress_change"]; !ok {
		s.NotifyEgressChange = def.NotifyEgressChange
	}
	if _, ok := raw["notify_connectivity_change"]; !ok {
		s.NotifyConnectivityChange = def.NotifyConnectivityChange
	}
	if _, ok := raw["notify_lan_device_change"]; !ok {
		s.NotifyLANDeviceChange = def.NotifyLANDeviceChange
	}
	if _, ok := raw["lan_device_offline_after_sec"]; !ok {
		s.LANDeviceOfflineAfterSec = def.LANDeviceOfflineAfterSec
	}
	if _, ok := raw["lan_device_online_after_sec"]; !ok {
		s.LANDeviceOnlineAfterSec = def.LANDeviceOnlineAfterSec
	}
	if _, ok := raw["lan_device_offline_notify_delay_sec"]; !ok {
		s.LANDeviceOfflineNotifyDelaySec = def.LANDeviceOfflineNotifyDelaySec
	}
	if _, ok := raw["lan_device_online_notify_delay_sec"]; !ok {
		s.LANDeviceOnlineNotifyDelaySec = def.LANDeviceOnlineNotifyDelaySec
	}
	if _, ok := raw["abnormal_traffic_threshold_mbps"]; !ok {
		s.AbnormalTrafficThresholdMbps = def.AbnormalTrafficThresholdMbps
	}
	if _, ok := raw["bark_server_url"]; !ok {
		s.BarkServerURL = def.BarkServerURL
	}
	if _, ok := raw["bark_group"]; !ok {
		s.BarkGroup = def.BarkGroup
	}
	if _, ok := raw["dnd_start"]; !ok {
		s.DNDStart = def.DNDStart
	}
	if _, ok := raw["dnd_end"]; !ok {
		s.DNDEnd = def.DNDEnd
	}
	if _, ok := raw["scheduled_notify_time"]; !ok {
		s.ScheduledNotifyTime = def.ScheduledNotifyTime
	}
	if _, ok := raw["lan_max_check_attempts"]; !ok {
		s.LANMaxCheckAttempts = def.LANMaxCheckAttempts
	}
	if _, ok := raw["lan_notify_cooldown_sec"]; !ok {
		s.LANNotifyCooldownSec = def.LANNotifyCooldownSec
	}
	if _, ok := raw["lan_flapping_threshold"]; !ok {
		s.LANFlappingThreshold = def.LANFlappingThreshold
	}
	if _, ok := raw["lan_flapping_window_sec"]; !ok {
		s.LANFlappingWindowSec = def.LANFlappingWindowSec
	}
	if _, ok := raw["lan_device_auto_remove_days"]; !ok {
		s.LANDeviceAutoRemoveDays = def.LANDeviceAutoRemoveDays
	}
	if _, ok := raw["chart_time_label_interval"]; !ok {
		s.ChartTimeLabelInterval = def.ChartTimeLabelInterval
	}
	if _, ok := raw["app_traffic_realtime_enabled"]; !ok {
		s.AppTrafficRealtimeEnabled = def.AppTrafficRealtimeEnabled
	}
	if _, ok := raw["host_network_experimental_enabled"]; !ok {
		s.HostNetworkExperimentalEnabled = def.HostNetworkExperimentalEnabled
	}
	// Preserve the stored default long enough to migrate legacy enabled apps,
	// then refresh the fallback for newly configured apps from the current host
	// address. The global proxy setting is no longer user-facing, so carrying a
	// DHCP-era address forward only creates a stale default.
	storedProxy := normalizeAppProxySettings(s.AppProxy, def.AppProxy)
	s.AppProxyConfigs = normalizeAppProxyConfigs(s.AppProxyConfigs, s.ProxyApps, storedProxy)
	s.AppProxy = def.AppProxy
	return s, true
}

func saveMutableSettings(dataDir string, s MutableSettings) error {
	return writeJSONFile(settingsPath(dataDir), s, true)
}
