package probe

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func settingsPath(dataDir string) string {
	return filepath.Join(dataDir, "settings.json")
}

func loadMutableSettings(dataDir string) (MutableSettings, bool) {
	var s MutableSettings
	body, err := os.ReadFile(settingsPath(dataDir))
	if err != nil {
		return s, false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return s, false
	}
	if err := json.Unmarshal(body, &s); err != nil {
		return s, false
	}
	if _, ok := raw["broadband_domestic_only"]; !ok {
		s.BroadbandDomesticOnly = true
	}
	if _, ok := raw["background_monitor_interval_sec"]; !ok {
		s.BackgroundMonitorIntervalSec = 60
	}
	if _, ok := raw["notifications_enabled"]; !ok {
		s.NotificationsEnabled = s.BackgroundMonitorEnabled
	}
	if _, ok := raw["client_notification_enabled"]; !ok {
		s.ClientNotificationEnabled = true
	}
	if _, ok := raw["notify_abnormal_traffic"]; !ok {
		s.NotifyAbnormalTraffic = true
	}
	if _, ok := raw["notify_egress_change"]; !ok {
		s.NotifyEgressChange = true
	}
	if _, ok := raw["notify_connectivity_change"]; !ok {
		s.NotifyConnectivityChange = true
	}
	if _, ok := raw["notify_lan_device_change"]; !ok {
		s.NotifyLANDeviceChange = true
	}
	if _, ok := raw["lan_device_offline_after_sec"]; !ok {
		s.LANDeviceOfflineAfterSec = 180
	}
	if _, ok := raw["lan_device_online_after_sec"]; !ok {
		s.LANDeviceOnlineAfterSec = 0
	}
	if _, ok := raw["lan_device_offline_notify_delay_sec"]; !ok {
		s.LANDeviceOfflineNotifyDelaySec = 120
	}
	if _, ok := raw["lan_device_online_notify_delay_sec"]; !ok {
		s.LANDeviceOnlineNotifyDelaySec = 120
	}
	if _, ok := raw["abnormal_traffic_threshold_mbps"]; !ok {
		s.AbnormalTrafficThresholdMbps = 100
	}
	if _, ok := raw["bark_server_url"]; !ok {
		s.BarkServerURL = "https://api.day.app"
	}
	if _, ok := raw["bark_group"]; !ok {
		s.BarkGroup = "Netwatch"
	}
	if _, ok := raw["dnd_start"]; !ok {
		s.DNDStart = "22:00"
	}
	if _, ok := raw["dnd_end"]; !ok {
		s.DNDEnd = "08:00"
	}
	if _, ok := raw["scheduled_notify_time"]; !ok {
		s.ScheduledNotifyTime = "09:00"
	}
	if _, ok := raw["lan_max_check_attempts"]; !ok {
		s.LANMaxCheckAttempts = 3
	}
	if _, ok := raw["lan_notify_cooldown_sec"]; !ok {
		s.LANNotifyCooldownSec = 600
	}
	if _, ok := raw["lan_flapping_threshold"]; !ok {
		s.LANFlappingThreshold = 5
	}
	if _, ok := raw["lan_flapping_window_sec"]; !ok {
		s.LANFlappingWindowSec = 600
	}
	if _, ok := raw["lan_device_auto_remove_days"]; !ok {
		s.LANDeviceAutoRemoveDays = 30
	}
	return s, true
}

func saveMutableSettings(dataDir string, s MutableSettings) error {
	return writeJSONFile(settingsPath(dataDir), s, true)
}
