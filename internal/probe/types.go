package probe

type ProbeStatus string

const (
	StatusOK       ProbeStatus = "ok"
	StatusDown     ProbeStatus = "down"
	StatusDegraded ProbeStatus = "degraded"
	StatusUnknown  ProbeStatus = "unknown"
)

type SiteTarget struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type TargetResult struct {
	Name          string      `json:"name"`
	URL           string      `json:"url,omitempty"`
	Status        ProbeStatus `json:"status"`
	LatencyMS     int64       `json:"latency_ms"`
	DNSMs         int64       `json:"dns_ms"`
	ConnectMS     int64       `json:"connect_ms"`
	TLSMs         int64       `json:"tls_ms"`
	TTFBMs        int64       `json:"ttfb_ms"`
	JitterMS      int64       `json:"jitter_ms"`
	PacketLossPct float64     `json:"packet_loss_pct"`
	TLSExpiresAt  string      `json:"tls_expires_at,omitempty"`
	TLSDaysLeft   int         `json:"tls_days_left,omitempty"`
	Error         string      `json:"error,omitempty"`
	CheckedAt     string      `json:"checked_at"`
}

type TimeseriesPoint struct {
	Timestamp      string             `json:"timestamp"`
	UnixMS         int64              `json:"unix_ms"`
	DomesticStatus ProbeStatus        `json:"domestic_status"`
	GlobalStatus   ProbeStatus        `json:"global_status"`
	TargetLatency  map[string]int64   `json:"target_latency_ms"`
	TargetLoss     map[string]float64 `json:"target_loss_pct"`
	EgressIPv4     string             `json:"egress_ipv4,omitempty"`
	EgressIPv6     string             `json:"egress_ipv6,omitempty"`
	NATType        string             `json:"nat_type,omitempty"`
}

type TraceHop struct {
	Hop       int          `json:"hop"`
	Host      string       `json:"host,omitempty"`
	IP        string       `json:"ip,omitempty"`
	LatencyMS int64        `json:"latency_ms"`
	Location  string       `json:"location,omitempty"`
	Probes    []TraceProbe `json:"probes,omitempty"`
}

type TraceProbe struct {
	IP        string `json:"ip,omitempty"`
	Host      string `json:"host,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
	Location  string `json:"location,omitempty"`
	Timeout   bool   `json:"timeout,omitempty"`
}

type TraceResult struct {
	Target    string     `json:"target"`
	Timestamp string     `json:"timestamp"`
	Tool      string     `json:"tool"`
	Hops      []TraceHop `json:"hops"`
	Running   bool       `json:"running,omitempty"`
	Finished  bool       `json:"finished,omitempty"`
	Error     string     `json:"error,omitempty"`
}

type MutableSettings struct {
	RefreshIntervalSec     int          `json:"refresh_interval_sec"`
	NICRealtimeEnabled     bool         `json:"nic_realtime_enabled"`
	NICRealtimeIntervalSec int          `json:"nic_realtime_interval_sec"`
	BroadbandDomesticOnly  bool         `json:"broadband_domestic_only"`
	DomesticSites          []SiteTarget `json:"domestic_sites"`
	GlobalSites            []SiteTarget `json:"global_sites"`
	AlertWebhookURL        string       `json:"alert_webhook_url"`

	// Background detection and client notification settings. When disabled,
	// netwatch does not periodically initiate external probes after startup.
	BackgroundMonitorEnabled       bool   `json:"background_monitor_enabled"`
	BackgroundMonitorIntervalSec   int    `json:"background_monitor_interval_sec"`
	NotificationsEnabled           bool   `json:"notifications_enabled"`
	ClientNotificationEnabled      bool   `json:"client_notification_enabled"`
	NotifyAbnormalTraffic          bool   `json:"notify_abnormal_traffic"`
	NotifyEgressChange             bool   `json:"notify_egress_change"`
	NotifyConnectivityChange       bool   `json:"notify_connectivity_change"`
	NotifyLANDeviceChange          bool   `json:"notify_lan_device_change"`
	LANDeviceOfflineAfterSec       int    `json:"lan_device_offline_after_sec"`
	LANDeviceOnlineAfterSec        int    `json:"lan_device_online_after_sec"`
	LANDeviceOfflineNotifyDelaySec int    `json:"lan_device_offline_notify_delay_sec"`
	LANDeviceOnlineNotifyDelaySec  int    `json:"lan_device_online_notify_delay_sec"`
	AbnormalTrafficThresholdMbps   int    `json:"abnormal_traffic_threshold_mbps"`
	BarkEnabled                    bool   `json:"bark_enabled"`
	BarkServerURL                  string `json:"bark_server_url"`
	BarkDeviceKey                  string `json:"bark_device_key"`
	BarkGroup                      string `json:"bark_group"`

	// Do Not Disturb settings
	DNDEnabled bool   `json:"dnd_enabled"`
	DNDStart   string `json:"dnd_start"` // "HH:MM"
	DNDEnd     string `json:"dnd_end"`   // "HH:MM"

	// Scheduled notification settings
	ScheduledNotifyEnabled bool   `json:"scheduled_notify_enabled"`
	ScheduledNotifyTime    string `json:"scheduled_notify_time"` // "HH:MM"

	// LAN detection settings
	LANMaxCheckAttempts     int `json:"lan_max_check_attempts"`     // consecutive misses before offline
	LANNotifyCooldownSec    int `json:"lan_notify_cooldown_sec"`    // min seconds between notifications per device
	LANFlappingThreshold    int `json:"lan_flapping_threshold"`     // max state changes in window before suppression
	LANFlappingWindowSec    int `json:"lan_flapping_window_sec"`    // sliding window duration in seconds
	LANDeviceAutoRemoveDays int `json:"lan_device_auto_remove_days"` // auto-remove offline devices after N days (0 = disabled)

	// Traffic sampling settings
	TrafficSamplingEnabled     bool           `json:"traffic_sampling_enabled"`
	TrafficSamplingIntervalSec int            `json:"traffic_sampling_interval_sec"`
	PerAppSamplingInterval     map[string]int `json:"per_app_sampling_interval,omitempty"` // bridge → interval sec
	PersistentTrafficBridges   []string       `json:"persistent_traffic_bridges,omitempty"`

	// Traffic chart settings
	ChartTimeLabelInterval int `json:"chart_time_label_interval"`

	// Notification device selection
	NotificationDeviceIDs []string `json:"notification_device_ids,omitempty"` // device IDs that should receive client notifications; empty = all
}

type RegisteredDevice struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	Platform   string `json:"platform,omitempty"`
	FirstSeen  string `json:"first_seen"`
	LastSeen   string `json:"last_seen"`
	Notify     bool   `json:"notify"`
}

type NotificationEvent struct {
	ID          int64  `json:"id"`
	Kind        string `json:"kind"`
	Severity    string `json:"severity"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	DeeplinkURL string `json:"deeplink_url,omitempty"`
	CreatedAt   string `json:"created_at"`
}

type LANDevice struct {
	IP               string   `json:"ip"`
	IPv6             []string `json:"ipv6,omitempty"`
	MAC              string   `json:"mac"`
	Hostname         string   `json:"hostname,omitempty"`
	Note             string   `json:"note,omitempty"`
	Interface        string   `json:"interface,omitempty"`
	VendorHint       string   `json:"vendor_hint,omitempty"`
	Reachability     string   `json:"reachability,omitempty"`
	DetectionMethods []string `json:"detection_methods,omitempty"`
	Status           string   `json:"status"`
	FirstSeen        string   `json:"first_seen"`
	LastSeen         string   `json:"last_seen"`
	LastChanged      string   `json:"last_changed,omitempty"`
	LastNotified     string   `json:"last_notified,omitempty"`
	NotifyState      string   `json:"notify_state,omitempty"`
	MissCount        int      `json:"miss_count,omitempty"`
	SeenCount        int      `json:"seen_count"`
	NewDevice        bool     `json:"new_device,omitempty"`
	Ignored          bool     `json:"ignored,omitempty"`
	Pinned           bool     `json:"pinned,omitempty"`
	Known            bool     `json:"known,omitempty"`
}

type LANScanNetwork struct {
	Interface string `json:"interface"`
	CIDR      string `json:"cidr"`
	Scanned   int    `json:"scanned"`
	Skipped   bool   `json:"skipped,omitempty"`
	Reason    string `json:"reason,omitempty"`
	LinkType  string `json:"link_type,omitempty"`
	LinkLabel string `json:"link_label,omitempty"`
	LinkUp    bool   `json:"link_up"`
	OperState string `json:"oper_state,omitempty"`
}

type LANDeviceSnapshot struct {
	GeneratedAt    string           `json:"generated_at"`
	Devices        []LANDevice      `json:"devices"`
	IgnoredDevices []LANDevice      `json:"ignored_devices,omitempty"`
	PinnedDevices  []LANDevice      `json:"pinned_devices,omitempty"`
	Networks       []LANScanNetwork `json:"networks"`
	Online         int              `json:"online"`
	Offline        int              `json:"offline"`
	NewCount       int              `json:"new_count"`
	Unknown        int              `json:"unknown"`
	Note           string           `json:"note,omitempty"`
}

type EgressLookup struct {
	Provider   string `json:"provider"`
	Scope      string `json:"scope"`
	IP         string `json:"ip,omitempty"`
	Country    string `json:"country,omitempty"`
	Region     string `json:"region,omitempty"`
	City       string `json:"city,omitempty"`
	ISP        string `json:"isp,omitempty"`
	ASN        string `json:"asn,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

type EgressLookupResult struct {
	GeneratedAt string             `json:"generated_at"`
	Lookups     []EgressLookup     `json:"lookups"`
	DomesticIP  DomesticIPSnapshot `json:"domestic_ip"`
}

type IPReachabilityProbe struct {
	Status     string `json:"status"`
	LatencyMS  int64  `json:"latency_ms,omitempty"`
	RemoteAddr string `json:"remote_addr,omitempty"`
	Error      string `json:"error,omitempty"`
}

type DomesticIPEntry struct {
	IP            string              `json:"ip,omitempty"`
	Location      string              `json:"location,omitempty"`
	ISP           string              `json:"isp,omitempty"`
	HasPublicPath bool                `json:"has_public_path"`
	Source        string              `json:"source,omitempty"`
	Error         string              `json:"error,omitempty"`
	PortProbe     IPReachabilityProbe `json:"port_probe,omitempty"`
}

type DomesticIPSnapshot struct {
	IPv4 DomesticIPEntry `json:"ipv4"`
	IPv6 DomesticIPEntry `json:"ipv6"`
}

type WebsiteConnectivity struct {
	GeneratedAt    string         `json:"generated_at"`
	DomesticStatus ProbeStatus    `json:"domestic_status"`
	GlobalStatus   ProbeStatus    `json:"global_status"`
	Domestic       []TargetResult `json:"domestic"`
	Global         []TargetResult `json:"global"`
}

type DefaultRoute struct {
	Interface string `json:"interface"`
	Gateway   string `json:"gateway,omitempty"`
}

type InterfaceInfo struct {
	Name         string   `json:"name"`
	Label        string   `json:"label,omitempty"`
	LinkType     string   `json:"link_type,omitempty"` // "wired" / "wifi"
	Present      bool     `json:"present"`
	OperState    string   `json:"oper_state,omitempty"` // "up", "down", "unknown" from kernel
	MTU          int      `json:"mtu"`
	HardwareAddr string   `json:"hardware_addr,omitempty"`
	Flags        []string `json:"flags,omitempty"`
	IPv4         []string `json:"ipv4,omitempty"`
	IPv6         []string `json:"ipv6,omitempty"`
	DeviceStatus string   `json:"device_status,omitempty"`  // connected/disconnected/disabled/...
	LinkSpeedBps int64    `json:"link_speed_bps,omitempty"` // 协商速率 (bit/s)
	WifiSSID     string   `json:"wifi_ssid,omitempty"`
	WifiSignal   int32    `json:"wifi_signal,omitempty"` // 0..100
}

type EgressLocation struct {
	IP      string `json:"ip,omitempty"`
	Country string `json:"country,omitempty"`
	Region  string `json:"region,omitempty"`
	City    string `json:"city,omitempty"`
	ISP     string `json:"isp,omitempty"`
	Source  string `json:"source,omitempty"`
}

type NATObservation struct {
	Server       string `json:"server"`
	LocalAddr    string `json:"local_addr,omitempty"`
	ExternalAddr string `json:"external_addr,omitempty"`
	Error        string `json:"error,omitempty"`
}

type NATInfo struct {
	GeneratedAt string `json:"generated_at"`
	Type        string `json:"type"`
	Reachable   bool   `json:"reachable"`
	Note        string `json:"note"`
}

type NetworkInfo struct {
	GeneratedAt          string          `json:"generated_at"`
	Hostname             string          `json:"hostname"`
	Interfaces           []InterfaceInfo `json:"interfaces"`
	DefaultIPv4          DefaultRoute    `json:"default_ipv4"`
	DefaultIPv6          DefaultRoute    `json:"default_ipv6"`
	EgressIPv4           string          `json:"egress_ipv4,omitempty"`
	EgressIPv6           string          `json:"egress_ipv6,omitempty"`
	EgressIPv4Region     EgressLocation  `json:"egress_ipv4_region"`
	EgressIPv6Region     EgressLocation  `json:"egress_ipv6_region"`
	NAT                  NATInfo         `json:"nat"`
	PlatformConnectivity string          `json:"platform_connectivity,omitempty"` // Full/Limited/Portal/None/Unknown
	HasInternet          bool            `json:"has_internet,omitempty"`
	WifiSSID             string          `json:"wifi_ssid,omitempty"`
	WifiSignal           int32           `json:"wifi_signal,omitempty"`
	DetectionNotes       []string        `json:"detection_notes,omitempty"`
}

type BroadbandSpeedResult struct {
	Timestamp      string                  `json:"timestamp"`
	DownloadMbps   float64                 `json:"download_mbps"`
	UploadMbps     float64                 `json:"upload_mbps"`
	LatencyMS      int64                   `json:"latency_ms"`
	JitterMS       int64                   `json:"jitter_ms"`
	Provider       string                  `json:"provider,omitempty"`
	ServerRegion   string                  `json:"server_region,omitempty"`
	ServerID       string                  `json:"server_id,omitempty"`
	ServerName     string                  `json:"server_name,omitempty"`
	ServerCountry  string                  `json:"server_country,omitempty"`
	ServerHost     string                  `json:"server_host,omitempty"`
	NodeSource     string                  `json:"node_source,omitempty"`
	DomesticNode   bool                    `json:"domestic_node,omitempty"`
	FailureStage   string                  `json:"failure_stage,omitempty"`
	FailureReason  string                  `json:"failure_reason,omitempty"`
	StageDurations BroadbandStageDurations `json:"stage_durations,omitempty"`
	Error          string                  `json:"error,omitempty"`
}

type BroadbandStageDurations struct {
	NodeSelectionMS int64 `json:"node_selection_ms,omitempty"`
	LatencyTestMS   int64 `json:"latency_test_ms,omitempty"`
	DownloadTestMS  int64 `json:"download_test_ms,omitempty"`
	UploadTestMS    int64 `json:"upload_test_ms,omitempty"`
	TotalMS         int64 `json:"total_ms,omitempty"`
}

type LocalTransferResult struct {
	Timestamp          string  `json:"timestamp"`
	DownloadMbps       float64 `json:"download_mbps"`
	UploadMbps         float64 `json:"upload_mbps"`
	PayloadMB          float64 `json:"payload_mb"`
	DownloadMB         float64 `json:"download_mb,omitempty"`
	UploadMB           float64 `json:"upload_mb,omitempty"`
	DurationMS         int64   `json:"duration_ms,omitempty"`
	RoundTripLatencyMS int64   `json:"round_trip_latency_ms"`
	RTTMinMS           int64   `json:"rtt_min_ms,omitempty"`
	RTTAvgMS           int64   `json:"rtt_avg_ms,omitempty"`
	RTTMaxMS           int64   `json:"rtt_max_ms,omitempty"`
	JitterMS           int64   `json:"jitter_ms"`
	Error              string  `json:"error,omitempty"`
}

type SpeedConfig struct {
	BroadbandDurationSec     int64 `json:"broadband_duration_sec"`
	LocalTransferDurationSec int64 `json:"local_transfer_duration_sec"`
	LocalTransferPayloadMB   int   `json:"local_transfer_payload_mb"`
}

type BroadbandTaskStatus struct {
	ID              string               `json:"id,omitempty"`
	Stage           string               `json:"stage"`
	ProgressPercent int                  `json:"progress_percent"`
	Running         bool                 `json:"running"`
	Finished        bool                 `json:"finished"`
	Canceled        bool                 `json:"canceled"`
	Message         string               `json:"message,omitempty"`
	UpdatedAt       string               `json:"updated_at"`
	Result          BroadbandSpeedResult `json:"result"`
}

type Summary struct {
	GeneratedAt         string              `json:"generated_at"`
	RefreshIntervalSec  int64               `json:"refresh_interval_sec"`
	Ready               bool                `json:"ready"`
	LastError           string              `json:"last_error,omitempty"`
	WebsiteConnectivity WebsiteConnectivity `json:"website_connectivity"`
	NetworkInfo         NetworkInfo         `json:"network_info"`
}
