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
	RefreshIntervalSec   int          `json:"refresh_interval_sec"`
	AutoRefreshEnabled   bool         `json:"auto_refresh_enabled"`
	NICRealtimeEnabled   bool         `json:"nic_realtime_enabled"`
	NICRealtimeIntervalSec int        `json:"nic_realtime_interval_sec"`
	DomesticSites        []SiteTarget `json:"domestic_sites"`
	GlobalSites          []SiteTarget `json:"global_sites"`
	AlertWebhookURL      string       `json:"alert_webhook_url"`
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
	IP             string             `json:"ip,omitempty"`
	Location       string             `json:"location,omitempty"`
	ISP            string             `json:"isp,omitempty"`
	HasPublicPath  bool               `json:"has_public_path"`
	Source         string             `json:"source,omitempty"`
	Error          string             `json:"error,omitempty"`
	PortProbe      IPReachabilityProbe `json:"port_probe,omitempty"`
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
	Present      bool     `json:"present"`
	MTU          int      `json:"mtu"`
	HardwareAddr string   `json:"hardware_addr,omitempty"`
	Flags        []string `json:"flags,omitempty"`
	IPv4         []string `json:"ipv4,omitempty"`
	IPv6         []string `json:"ipv6,omitempty"`
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
	GeneratedAt      string          `json:"generated_at"`
	Hostname         string          `json:"hostname"`
	Interfaces       []InterfaceInfo `json:"interfaces"`
	DefaultIPv4      DefaultRoute    `json:"default_ipv4"`
	DefaultIPv6      DefaultRoute    `json:"default_ipv6"`
	EgressIPv4       string          `json:"egress_ipv4,omitempty"`
	EgressIPv6       string          `json:"egress_ipv6,omitempty"`
	EgressIPv4Region EgressLocation  `json:"egress_ipv4_region"`
	EgressIPv6Region EgressLocation  `json:"egress_ipv6_region"`
	NAT              NATInfo         `json:"nat"`
	DetectionNotes   []string        `json:"detection_notes,omitempty"`
}

type BroadbandSpeedResult struct {
	Timestamp         string  `json:"timestamp"`
	DownloadMbps      float64 `json:"download_mbps"`
	UploadMbps        float64 `json:"upload_mbps"`
	LatencyMS         int64   `json:"latency_ms"`
	JitterMS          int64   `json:"jitter_ms"`
	Provider          string  `json:"provider,omitempty"`
	ServerRegion      string  `json:"server_region,omitempty"`
	Error             string  `json:"error,omitempty"`
}

type LocalTransferResult struct {
	Timestamp           string  `json:"timestamp"`
	DownloadMbps        float64 `json:"download_mbps"`
	UploadMbps          float64 `json:"upload_mbps"`
	PayloadMB           float64 `json:"payload_mb"`
	RoundTripLatencyMS  int64   `json:"round_trip_latency_ms"`
	JitterMS            int64   `json:"jitter_ms"`
	Error               string  `json:"error,omitempty"`
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
	NextRefreshAt       string              `json:"next_refresh_at,omitempty"`
	RefreshIntervalSec  int64               `json:"refresh_interval_sec"`
	Ready               bool                `json:"ready"`
	LastError           string              `json:"last_error,omitempty"`
	WebsiteConnectivity WebsiteConnectivity `json:"website_connectivity"`
	NetworkInfo         NetworkInfo         `json:"network_info"`
}
