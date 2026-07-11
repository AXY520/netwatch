package probe

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"netwatch/internal/dockerlzc"
	"netwatch/internal/logger"
	"netwatch/internal/lzcsdk"
)

const maxHistoryItems = 30
const maxNotificationEvents = 100
const maxBroadbandSteps = 50

type publicIPCacheData struct {
	IPv4       string
	IPv6       string
	IPv4Region EgressLocation
	IPv6Region EgressLocation
	UpdatedAt  time.Time
}

type Service struct {
	cfg                         Config
	mu                          sync.RWMutex
	closeOnce                   sync.Once
	summary                     Summary
	lastError                   string
	broadbandHistory            []BroadbandSpeedResult
	localTransferHistory        []LocalTransferResult
	broadbandTask               BroadbandTaskStatus
	broadbandTaskCancel         context.CancelFunc
	timeseries                  *timeseriesStore
	alert                       *alertState
	subs                        []chan Summary
	subsMu                      sync.Mutex
	alertWebhookURL             string
	nicStats                    *nicStatsTracker
	nicStop                     chan struct{}
	egressCache                 EgressLookupResult
	egressMu                    sync.Mutex
	egressCond                  *sync.Cond
	egressInflight              bool
	publicIPCache               publicIPCacheData
	publicIPMu                  sync.Mutex
	traceMu                     sync.Mutex
	traceTask                   TraceResult
	traceCancel                 context.CancelFunc
	appTrafficHistory           *appTrafficHistoryStore
	appTrafficStop              chan struct{}
	appTrafficDone              chan struct{}
	nicDone                     chan struct{}
	backgroundMonitorStop       chan struct{}
	backgroundMonitorDone       chan struct{}
	lanInterfaceStop            chan struct{}
	lanInterfaceDone            chan struct{}
	chartTimeLabelInterval      int
	containerControlEnabled     bool
	trafficSamplingEnabled      bool
	trafficSamplingInterval     int // seconds
	perAppSamplingInterval      map[string]int
	persistentTrafficBridges    []string
	backgroundMonitorEnabled    bool
	backgroundMonitorInterval   int
	lastConnectivityProbe       time.Time
	lastEgressProbe             time.Time
	notificationsEnabled        bool
	clientNotificationEnabled   bool
	notifyAbnormalTraffic       bool
	notifyEgressChange          bool
	notifyConnectivityChange    bool
	notifyLANDeviceChange       bool
	lanDeviceOfflineAfter       int
	lanDeviceOnlineAfter        int
	lanDeviceOfflineNotifyDelay int
	lanDeviceOnlineNotifyDelay  int
	abnormalTrafficThreshold    int
	barkEnabled                 bool
	barkServerURL               string
	barkDeviceKey               string
	barkGroup                   string
	pushPlusEnabled             bool
	pushPlusToken               string
	pushPlusTopic               string
	dndEnabled                  bool
	dndStart                    string
	dndEnd                      string
	scheduledNotifyEnabled      bool
	scheduledNotifyTime         string
	lanMu                       sync.Mutex
	lanDeviceMap                map[string]LANDevice
	lanSnapshot                 LANDeviceSnapshot
	lanInterfaceState           map[string]bool
	lanNotifyCooldown           map[string]time.Time   // MAC -> last notification time
	lanFlappingHistory          map[string][]time.Time // MAC -> state change timestamps (sliding window)
	lanMaxCheckAttempts         int                    // consecutive misses before offline
	lanNotifyCooldownSec        int                    // min seconds between notifications per device
	lanFlappingThreshold        int                    // max state changes in window before suppression
	lanFlappingWindow           time.Duration          // sliding window duration
	lanDeviceAutoRemoveDays     int                    // auto-remove offline devices after N days (0=disabled)
	notificationDeviceIDs       []string               // device IDs to receive client notifications; empty = all
	blockedBridges              map[string]string      // bridge name → "internet" | "all"
	registeredDevices           map[string]RegisteredDevice
	registeredDevicesMu         sync.Mutex
	notificationMu              sync.Mutex
	notificationEvents          []NotificationEvent
	notificationNextID          int64
	notificationSubs            []chan NotificationEvent
	notificationSubsMu          sync.Mutex
	nicSubs                     []chan RealtimeNetStats
	nicSubsMu                   sync.Mutex
	lanDeviceSubs               []chan LANDeviceSnapshot
	lanDeviceSubsMu             sync.Mutex
	monitorBaselineReady        bool
	monitorLastSummary          Summary
	monitorTrafficHigh          bool
	networkConfigMu             sync.Mutex
	networkConfigRollbacks      map[string]*networkConfigRollback
	closeCtx                    context.Context
	closeCancel                 context.CancelFunc
}

func NewService(cfg Config) *Service {
	def := DefaultMutableSettings()
	s := &Service{
		cfg:                         cfg,
		timeseries:                  newTimeseriesStore(cfg.DataDir),
		alert:                       newAlertState(),
		nicStats:                    newNICStatsTracker(),
		nicStop:                     make(chan struct{}),
		nicDone:                     make(chan struct{}),
		appTrafficHistory:           newAppTrafficHistoryStore(cfg.DataDir),
		appTrafficStop:              make(chan struct{}),
		appTrafficDone:              make(chan struct{}),
		backgroundMonitorStop:       make(chan struct{}),
		backgroundMonitorDone:       make(chan struct{}),
		lanInterfaceStop:            make(chan struct{}),
		lanInterfaceDone:            make(chan struct{}),
		lanNotifyCooldown:           make(map[string]time.Time),
		lanFlappingHistory:          make(map[string][]time.Time),
		lanMaxCheckAttempts:         def.LANMaxCheckAttempts,
		lanNotifyCooldownSec:        def.LANNotifyCooldownSec,
		lanFlappingThreshold:        def.LANFlappingThreshold,
		lanFlappingWindow:           time.Duration(def.LANFlappingWindowSec) * time.Second,
		registeredDevices:           loadRegisteredDevices(cfg.DataDir),
		chartTimeLabelInterval:      def.ChartTimeLabelInterval,
		containerControlEnabled:     def.ContainerControlEnabled,
		trafficSamplingEnabled:      def.TrafficSamplingEnabled,
		trafficSamplingInterval:     def.TrafficSamplingIntervalSec,
		perAppSamplingInterval:      make(map[string]int),
		backgroundMonitorEnabled:    def.BackgroundMonitorEnabled,
		backgroundMonitorInterval:   def.BackgroundMonitorIntervalSec,
		notificationsEnabled:        def.NotificationsEnabled,
		clientNotificationEnabled:   def.ClientNotificationEnabled,
		notifyAbnormalTraffic:       def.NotifyAbnormalTraffic,
		notifyEgressChange:          def.NotifyEgressChange,
		notifyConnectivityChange:    def.NotifyConnectivityChange,
		notifyLANDeviceChange:       def.NotifyLANDeviceChange,
		lanDeviceOfflineAfter:       def.LANDeviceOfflineAfterSec,
		lanDeviceOnlineAfter:        def.LANDeviceOnlineAfterSec,
		lanDeviceOfflineNotifyDelay: def.LANDeviceOfflineNotifyDelaySec,
		lanDeviceOnlineNotifyDelay:  def.LANDeviceOnlineNotifyDelaySec,
		abnormalTrafficThreshold:    def.AbnormalTrafficThresholdMbps,
		barkEnabled:                 def.BarkEnabled,
		barkServerURL:               def.BarkServerURL,
		barkGroup:                   def.BarkGroup,
		pushPlusEnabled:             def.PushPlusEnabled,
		dndEnabled:                  def.DNDEnabled,
		dndStart:                    def.DNDStart,
		dndEnd:                      def.DNDEnd,
		scheduledNotifyEnabled:      def.ScheduledNotifyEnabled,
		scheduledNotifyTime:         def.ScheduledNotifyTime,
		lanDeviceAutoRemoveDays:     def.LANDeviceAutoRemoveDays,
		blockedBridges:              map[string]string{},
		networkConfigRollbacks:      map[string]*networkConfigRollback{},
	}
	s.egressCond = sync.NewCond(&s.egressMu)
	s.nicStats.onSampled = s.broadcastNICRealtime
	s.closeCtx, s.closeCancel = context.WithCancel(context.Background())
	s.loadHistory()
	if saved, ok := loadMutableSettings(cfg.DataDir); ok {
		s.applyMutableSettings(saved, false)
	}
	s.nicStats.start(s.nicStop, s.nicDone)
	s.startAppTrafficSampling()
	s.startBackgroundMonitor()
	s.startLANInterfaceMonitor()
	s.startScheduledNotifier()
	return s
}

func (s *Service) Start(baseCtx context.Context) {
	startCtx, cancelStart := context.WithCancel(baseCtx)

	go func() {
		s.Refresh(startCtx)
	}()

	go func() {
		<-baseCtx.Done()
		cancelStart()
	}()
}

// backgroundCtx returns a context cancelled when the service is closed.
func (s *Service) backgroundCtx() context.Context {
	return s.closeCtx
}

// LifecycleContext is cancelled only when the service is closed. Use it for
// user-triggered probes whose results should not be invalidated by a browser
// aborting the HTTP request.
func (s *Service) LifecycleContext() context.Context {
	return s.backgroundCtx()
}

// traceCtx returns a cancellable context derived from the service lifecycle.
func (s *Service) traceCtx() context.Context {
	return s.closeCtx
}

// CapabilityReport describes runtime features available in the current environment.
type CapabilityReport struct {
	GeneratedAt          string   `json:"generated_at"`
	HostNetworkLikely    bool     `json:"host_network_likely"`
	MTR                  bool     `json:"mtr"`
	Nmcli                bool     `json:"nmcli"`
	Iptables             bool     `json:"iptables"`
	Nsenter              bool     `json:"nsenter"`
	DockerSocket         bool     `json:"docker_socket"`
	LazycatBridgeTraffic bool     `json:"lazycat_bridge_traffic"`
	ContainerControl     bool     `json:"container_control"`
	NetworkConfig        bool     `json:"network_config"`
	Trace                bool     `json:"trace"`
	AppTraffic           bool     `json:"app_traffic"`
	LANDiscovery         bool     `json:"lan_discovery"`
	Notes                []string `json:"notes,omitempty"`
}

func binaryAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func (s *Service) Capabilities() CapabilityReport {
	findBinPaths()
	mtrOK := binaryAvailable("mtr")
	nmcliOK := binaryAvailable("nmcli")
	iptOK := iptablesAvailable()
	nsenterOK := nsenterAvailable()
	dockerOK := dockerlzc.Available()
	// App bridge traffic is readable whenever /sys/class/net is visible; mapping to
	// app titles benefits from docker socket but is not strictly required.
	appTrafficOK := true
	if _, err := os.Stat("/sys/class/net"); err != nil {
		appTrafficOK = false
	}
	report := CapabilityReport{
		GeneratedAt:          localTimestamp(),
		HostNetworkLikely:    true, // best-effort; host network is the supported deploy mode
		MTR:                  mtrOK,
		Nmcli:                nmcliOK,
		Iptables:             iptOK,
		Nsenter:              nsenterOK,
		DockerSocket:         dockerOK,
		LazycatBridgeTraffic: appTrafficOK && dockerOK,
		ContainerControl:     dockerOK && (iptOK || nsenterOK),
		NetworkConfig:        nmcliOK,
		Trace:                mtrOK,
		AppTraffic:           appTrafficOK,
		LANDiscovery:         true,
	}
	if !mtrOK {
		report.Notes = append(report.Notes, "mtr unavailable: route tracing disabled")
	}
	if !nmcliOK {
		report.Notes = append(report.Notes, "nmcli unavailable: host network config UI will degrade")
	}
	if !dockerOK {
		report.Notes = append(report.Notes, "docker socket unavailable: app titles and container control limited")
	}
	if !iptOK && !nsenterOK {
		report.Notes = append(report.Notes, "iptables/nsenter unavailable: container network block disabled")
	}
	return report
}

func (s *Service) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		broadbandCancel := s.broadbandTaskCancel
		s.mu.Unlock()
		if broadbandCancel != nil {
			broadbandCancel()
		}

		s.traceMu.Lock()
		traceCancel := s.traceCancel
		s.traceMu.Unlock()
		if traceCancel != nil {
			traceCancel()
		}

		s.closeCancel()
		close(s.nicStop)
		close(s.appTrafficStop)
		close(s.backgroundMonitorStop)
		close(s.lanInterfaceStop)
		<-s.nicDone
		<-s.appTrafficDone
		<-s.backgroundMonitorDone
		<-s.lanInterfaceDone
	})
}

func cloneLANDeviceMap(in map[string]LANDevice) map[string]LANDevice {
	out := make(map[string]LANDevice, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// getLANDevicesCopy returns a mutable copy of the in-memory LAN device map.
// Disk is only read on first access.
func (s *Service) getLANDevicesCopy() map[string]LANDevice {
	s.lanMu.Lock()
	defer s.lanMu.Unlock()
	if s.lanDeviceMap == nil {
		s.lanDeviceMap = loadLANDeviceMap(s.cfg.DataDir)
	}
	return cloneLANDeviceMap(s.lanDeviceMap)
}

// putLANDevices updates the in-memory map and persists to disk.
func (s *Service) putLANDevices(devices map[string]LANDevice) error {
	cloned := cloneLANDeviceMap(devices)
	s.lanMu.Lock()
	s.lanDeviceMap = cloned
	s.lanMu.Unlock()
	return saveLANDeviceMap(s.cfg.DataDir, cloned)
}

// putLANDevicesLocked updates the in-memory map while lanMu is already held, then persists.
func (s *Service) putLANDevicesLocked(devices map[string]LANDevice) error {
	cloned := cloneLANDeviceMap(devices)
	s.lanDeviceMap = cloned
	return saveLANDeviceMap(s.cfg.DataDir, cloned)
}

func (s *Service) Refresh(ctx context.Context) Summary {
	// 手动刷新时清除公网 IP 缓存，强制重新查询
	s.publicIPMu.Lock()
	s.publicIPCache = publicIPCacheData{}
	s.publicIPMu.Unlock()
	// 同时清除出口 IP 查询缓存
	s.RefreshEgressLookups(ctx)
	s.refreshFast(ctx)
	return s.GetSummary()
}

func (s *Service) RefreshNAT(ctx context.Context) NATInfo {
	s.refreshNAT(ctx)
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.summary.NetworkInfo.NAT
}

func (s *Service) RefreshWebsiteConnectivity(ctx context.Context) WebsiteConnectivity {
	s.mu.RLock()
	timeout := s.cfg.HTTPTimeout
	s.mu.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, timeout*2)
	defer cancel()

	website := s.ProbeWebsiteConnectivity(ctx)

	s.mu.Lock()
	s.summary.WebsiteConnectivity = website
	s.summary.GeneratedAt = localTimestamp()
	s.mu.Unlock()

	return website
}

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
	if s.broadbandTask.Running {
		task := s.broadbandTask
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
	s.broadbandTask = task
	s.broadbandTaskCancel = cancel
	s.mu.Unlock()

	go s.runBroadbandTask(ctx, duration)
	return task
}

func (s *Service) GetBroadbandTask() BroadbandTaskStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.broadbandTask
}

func (s *Service) CancelBroadbandTask() BroadbandTaskStatus {
	s.mu.Lock()
	cancel := s.broadbandTaskCancel
	if s.broadbandTask.Running {
		s.broadbandTask.Message = "正在取消测速"
		s.broadbandTask.UpdatedAt = localTimestamp()
	}
	task := s.broadbandTask
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
	if n := len(s.broadbandTask.Steps); n > 0 {
		seq = s.broadbandTask.Steps[n-1].Seq + 1
	}
	s.broadbandTask.Steps = append(s.broadbandTask.Steps, BroadbandTaskStep{
		Seq:     seq,
		Time:    localTimestamp(),
		Stage:   stage,
		Status:  status,
		Message: message,
	})
	if len(s.broadbandTask.Steps) > maxBroadbandSteps {
		s.broadbandTask.Steps = s.broadbandTask.Steps[len(s.broadbandTask.Steps)-maxBroadbandSteps:]
	}
	s.broadbandTask.UpdatedAt = localTimestamp()
}

func (s *Service) runBroadbandTask(ctx context.Context, duration time.Duration) {
	result, completed := executeBroadbandSpeedTest(ctx, s, duration, func(stage string, progress int, message string, partial BroadbandSpeedResult) {
		s.mu.Lock()
		s.broadbandTask.Stage = stage
		s.broadbandTask.ProgressPercent = progress
		s.broadbandTask.Message = message
		s.broadbandTask.Result = partial
		s.broadbandTask.UpdatedAt = localTimestamp()
		s.mu.Unlock()
	}, func(stage, status, message string) {
		s.appendBroadbandStep(stage, status, message)
	})

	s.mu.Lock()
	defer s.mu.Unlock()

	s.broadbandTask.Result = result
	s.broadbandTask.UpdatedAt = localTimestamp()
	s.broadbandTask.Running = false
	s.broadbandTaskCancel = nil

	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		s.broadbandTask.Stage = "canceled"
		s.broadbandTask.Canceled = true
		s.broadbandTask.Finished = false
		s.broadbandTask.Message = "宽带测速已取消"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		s.broadbandTask.Stage = "error"
		s.broadbandTask.Finished = false
		s.broadbandTask.Canceled = false
		if result.Error == "" {
			result.Error = "宽带测速超时"
		}
		result.FailureStage = "timeout"
		result.FailureReason = result.Error
		s.broadbandTask.Result = result
		s.broadbandTask.Message = result.Error
	case !completed:
		s.broadbandTask.Stage = "error"
		s.broadbandTask.Finished = false
		if result.Error == "" {
			result.Error = "测速未完成"
		}
		if result.FailureReason == "" {
			result.FailureReason = result.Error
		}
		s.broadbandTask.Result = result
		s.broadbandTask.Message = result.Error
	default:
		s.broadbandTask.Stage = "complete"
		s.broadbandTask.ProgressPercent = 100
		s.broadbandTask.Finished = true
		s.broadbandTask.Canceled = false
		s.broadbandTask.Message = "宽带测速完成"
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

func (s *Service) GetSummary() Summary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.summary
}

func (s *Service) Subscribe() (<-chan Summary, func()) {
	ch := make(chan Summary, 4)
	s.subsMu.Lock()
	s.subs = append(s.subs, ch)
	s.subsMu.Unlock()

	unsub := func() {
		s.subsMu.Lock()
		defer s.subsMu.Unlock()
		for i, c := range s.subs {
			if c == ch {
				s.subs = append(s.subs[:i], s.subs[i+1:]...)
				close(ch)
				return
			}
		}
	}
	return ch, unsub
}

func (s *Service) broadcast(summary Summary) {
	s.subsMu.Lock()
	subs := append([]chan Summary(nil), s.subs...)
	s.subsMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- summary:
		default:
		}
	}
}

func (s *Service) SubscribeNICRealtime() (<-chan RealtimeNetStats, func()) {
	ch := make(chan RealtimeNetStats, 2)
	s.nicSubsMu.Lock()
	s.nicSubs = append(s.nicSubs, ch)
	s.nicSubsMu.Unlock()
	return ch, func() {
		s.nicSubsMu.Lock()
		defer s.nicSubsMu.Unlock()
		for i, c := range s.nicSubs {
			if c == ch {
				s.nicSubs = append(s.nicSubs[:i], s.nicSubs[i+1:]...)
				close(ch)
				return
			}
		}
	}
}

func (s *Service) broadcastNICRealtime(snap RealtimeNetStats) {
	s.nicSubsMu.Lock()
	subs := append([]chan RealtimeNetStats(nil), s.nicSubs...)
	s.nicSubsMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- snap:
		default:
		}
	}
}

func (s *Service) SubscribeLANDevices() (<-chan LANDeviceSnapshot, func()) {
	ch := make(chan LANDeviceSnapshot, 2)
	s.lanDeviceSubsMu.Lock()
	s.lanDeviceSubs = append(s.lanDeviceSubs, ch)
	s.lanDeviceSubsMu.Unlock()
	return ch, func() {
		s.lanDeviceSubsMu.Lock()
		defer s.lanDeviceSubsMu.Unlock()
		for i, c := range s.lanDeviceSubs {
			if c == ch {
				s.lanDeviceSubs = append(s.lanDeviceSubs[:i], s.lanDeviceSubs[i+1:]...)
				close(ch)
				return
			}
		}
	}
}

func (s *Service) broadcastLANDevices(devices LANDeviceSnapshot) {
	s.lanDeviceSubsMu.Lock()
	subs := append([]chan LANDeviceSnapshot(nil), s.lanDeviceSubs...)
	s.lanDeviceSubsMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- devices:
		default:
		}
	}
}

func (s *Service) GetTimeseries(limit int) []TimeseriesPoint {
	return s.timeseries.snapshot(limit)
}

func (s *Service) GetMutableSettings() MutableSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	perApp := make(map[string]int, len(s.perAppSamplingInterval))
	for k, v := range s.perAppSamplingInterval {
		perApp[k] = v
	}
	return MutableSettings{
		RefreshIntervalSec:             int(s.cfg.RefreshInterval / time.Second),
		NICRealtimeEnabled:             s.nicStats.enabled(),
		NICRealtimeIntervalSec:         s.nicStats.intervalSeconds(),
		ChartTimeLabelInterval:         s.chartTimeLabelInterval,
		ContainerControlEnabled:        s.containerControlEnabled,
		BroadbandDomesticOnly:          s.cfg.BroadbandDomesticOnly,
		TrafficSamplingEnabled:         s.trafficSamplingEnabled,
		TrafficSamplingIntervalSec:     s.trafficSamplingInterval,
		PerAppSamplingInterval:         perApp,
		PersistentTrafficBridges:       append([]string(nil), s.persistentTrafficBridges...),
		DomesticSites:                  append([]SiteTarget(nil), s.cfg.DomesticSites...),
		GlobalSites:                    append([]SiteTarget(nil), s.cfg.GlobalSites...),
		AlertWebhookURL:                s.alertWebhookURL,
		BackgroundMonitorEnabled:       s.backgroundMonitorEnabled,
		BackgroundMonitorIntervalSec:   s.backgroundMonitorInterval,
		NotificationsEnabled:           s.notificationsEnabled,
		ClientNotificationEnabled:      s.clientNotificationEnabled,
		NotifyAbnormalTraffic:          s.notifyAbnormalTraffic,
		NotifyEgressChange:             s.notifyEgressChange,
		NotifyConnectivityChange:       s.notifyConnectivityChange,
		NotifyLANDeviceChange:          s.notifyLANDeviceChange,
		LANDeviceOfflineAfterSec:       s.lanDeviceOfflineAfter,
		LANDeviceOnlineAfterSec:        s.lanDeviceOnlineAfter,
		LANDeviceOfflineNotifyDelaySec: s.lanDeviceOfflineNotifyDelay,
		LANDeviceOnlineNotifyDelaySec:  s.lanDeviceOnlineNotifyDelay,
		AbnormalTrafficThresholdMbps:   s.abnormalTrafficThreshold,
		BarkEnabled:                    s.barkEnabled,
		BarkServerURL:                  s.barkServerURL,
		BarkDeviceKey:                  s.barkDeviceKey,
		BarkGroup:                      s.barkGroup,
		PushPlusEnabled:                s.pushPlusEnabled,
		PushPlusToken:                  s.pushPlusToken,
		PushPlusTopic:                  s.pushPlusTopic,
		DNDEnabled:                     s.dndEnabled,
		DNDStart:                       s.dndStart,
		DNDEnd:                         s.dndEnd,
		ScheduledNotifyEnabled:         s.scheduledNotifyEnabled,
		ScheduledNotifyTime:            s.scheduledNotifyTime,
		LANMaxCheckAttempts:            s.lanMaxCheckAttempts,
		LANNotifyCooldownSec:           s.lanNotifyCooldownSec,
		LANFlappingThreshold:           s.lanFlappingThreshold,
		LANFlappingWindowSec:           int(s.lanFlappingWindow.Seconds()),
		LANDeviceAutoRemoveDays:        s.lanDeviceAutoRemoveDays,
		NotificationDeviceIDs:          s.notificationDeviceIDs,
		BlockedBridges:                 s.blockedBridges,
	}
}

func (s *Service) UpdateMutableSettings(in MutableSettings) MutableSettings {
	s.applyMutableSettings(in, true)
	return s.GetMutableSettings()
}

func (s *Service) applyMutableSettings(in MutableSettings, persist bool) {
	s.mu.Lock()
	if in.RefreshIntervalSec > 0 {
		s.cfg.RefreshInterval = time.Duration(in.RefreshIntervalSec) * time.Second
	}
	s.cfg.BroadbandDomesticOnly = in.BroadbandDomesticOnly
	if len(in.DomesticSites) > 0 {
		s.cfg.DomesticSites = in.DomesticSites
	}
	if len(in.GlobalSites) > 0 {
		s.cfg.GlobalSites = in.GlobalSites
	}
	s.alertWebhookURL = in.AlertWebhookURL
	s.backgroundMonitorEnabled = in.BackgroundMonitorEnabled
	if in.BackgroundMonitorIntervalSec >= 10 {
		s.backgroundMonitorInterval = in.BackgroundMonitorIntervalSec
	}
	s.notificationsEnabled = in.NotificationsEnabled
	s.clientNotificationEnabled = in.ClientNotificationEnabled
	s.notifyAbnormalTraffic = in.NotifyAbnormalTraffic
	s.notifyEgressChange = in.NotifyEgressChange
	s.notifyConnectivityChange = in.NotifyConnectivityChange
	s.notifyLANDeviceChange = in.NotifyLANDeviceChange
	if in.LANDeviceOfflineAfterSec >= 10 {
		s.lanDeviceOfflineAfter = in.LANDeviceOfflineAfterSec
	}
	if in.LANDeviceOnlineAfterSec >= 0 {
		s.lanDeviceOnlineAfter = in.LANDeviceOnlineAfterSec
	}
	if in.LANDeviceOfflineNotifyDelaySec >= 0 {
		s.lanDeviceOfflineNotifyDelay = in.LANDeviceOfflineNotifyDelaySec
	}
	if in.LANDeviceOnlineNotifyDelaySec >= 0 {
		s.lanDeviceOnlineNotifyDelay = in.LANDeviceOnlineNotifyDelaySec
	}
	if in.AbnormalTrafficThresholdMbps > 0 {
		s.abnormalTrafficThreshold = in.AbnormalTrafficThresholdMbps
	}
	s.barkEnabled = in.BarkEnabled
	s.barkServerURL = strings.TrimSpace(in.BarkServerURL)
	s.barkDeviceKey = strings.TrimSpace(in.BarkDeviceKey)
	s.barkGroup = strings.TrimSpace(in.BarkGroup)
	s.pushPlusEnabled = in.PushPlusEnabled
	s.pushPlusToken = strings.TrimSpace(in.PushPlusToken)
	s.pushPlusTopic = strings.TrimSpace(in.PushPlusTopic)
	s.dndEnabled = in.DNDEnabled
	s.dndStart = normalizeHHMM(in.DNDStart, "22:00")
	s.dndEnd = normalizeHHMM(in.DNDEnd, "08:00")
	s.scheduledNotifyEnabled = in.ScheduledNotifyEnabled
	s.scheduledNotifyTime = normalizeHHMM(in.ScheduledNotifyTime, "09:00")
	if in.LANMaxCheckAttempts >= 1 {
		s.lanMaxCheckAttempts = in.LANMaxCheckAttempts
	}
	if in.LANNotifyCooldownSec >= 60 {
		s.lanNotifyCooldownSec = in.LANNotifyCooldownSec
	}
	if in.LANFlappingThreshold >= 3 {
		s.lanFlappingThreshold = in.LANFlappingThreshold
	}
	if in.LANFlappingWindowSec >= 60 {
		s.lanFlappingWindow = time.Duration(in.LANFlappingWindowSec) * time.Second
	}
	s.lanDeviceAutoRemoveDays = in.LANDeviceAutoRemoveDays
	s.notificationDeviceIDs = in.NotificationDeviceIDs
	if in.BlockedBridges != nil {
		s.blockedBridges = make(map[string]string, len(in.BlockedBridges))
		for k, v := range in.BlockedBridges {
			s.blockedBridges[k] = v
		}
	}
	if s.notificationsEnabled && !s.barkEnabled && !s.clientNotificationEnabled {
		s.clientNotificationEnabled = true
	}
	s.chartTimeLabelInterval = in.ChartTimeLabelInterval
	s.containerControlEnabled = in.ContainerControlEnabled
	s.trafficSamplingEnabled = in.TrafficSamplingEnabled
	if in.TrafficSamplingIntervalSec >= 5 {
		s.trafficSamplingInterval = in.TrafficSamplingIntervalSec
	}
	if in.PerAppSamplingInterval != nil {
		s.perAppSamplingInterval = make(map[string]int, len(in.PerAppSamplingInterval))
		for k, v := range in.PerAppSamplingInterval {
			s.perAppSamplingInterval[k] = v
		}
	}
	s.persistentTrafficBridges = append([]string(nil), in.PersistentTrafficBridges...)
	dataDir := s.cfg.DataDir
	s.mu.Unlock()

	s.nicStats.configure(in.NICRealtimeEnabled, in.NICRealtimeIntervalSec)
	s.alert.setWebhook(in.AlertWebhookURL)
	if persist {
		if err := saveMutableSettings(dataDir, s.GetMutableSettings()); err != nil {
			logger.Warn("saveMutableSettings: %v", err)
		}
	}
}

func (s *Service) GetRealtimeNetStats() RealtimeNetStats {
	return s.nicStats.sampleAndSnapshot()
}

// GetEgressLookups 返回缓存的多源查询结果，若没缓存则同步触发一次（冷启动场景）。
func (s *Service) GetEgressLookups(ctx context.Context) EgressLookupResult {
	s.egressMu.Lock()
	cache := s.egressCache
	empty := cache.GeneratedAt == ""
	s.egressMu.Unlock()
	if !empty {
		return cache
	}
	return s.RefreshEgressLookups(ctx)
}

// ClearPublicIPCache 清除公网 IP 缓存，下次 ProbeNetworkInfo 会重新查询。
func (s *Service) ClearPublicIPCache() {
	s.publicIPMu.Lock()
	s.publicIPCache = publicIPCacheData{}
	s.publicIPMu.Unlock()
}

// RefreshEgressLookups 强制立刻刷新并更新缓存。
func (s *Service) RefreshEgressLookups(ctx context.Context) EgressLookupResult {
	s.egressMu.Lock()
	if s.egressInflight {
		cache := s.egressCache
		empty := cache.GeneratedAt == ""
		s.egressMu.Unlock()
		if empty {
			return s.waitForEgressLookup(ctx)
		}
		return cache
	}
	s.egressInflight = true
	cfg := s.cfg
	s.egressMu.Unlock()

	result := LookupEgressIPs(ctx)
	result.DomesticIP = lookupDomesticIPs(ctx, cfg)

	s.egressMu.Lock()
	s.egressCache = result
	s.egressInflight = false
	s.egressCond.Broadcast()
	s.egressMu.Unlock()
	return result
}

func (s *Service) waitForEgressLookup(ctx context.Context) EgressLookupResult {
	done := make(chan struct{})
	go func() {
		s.egressMu.Lock()
		for s.egressInflight && s.egressCache.GeneratedAt == "" {
			s.egressCond.Wait()
			select {
			case <-ctx.Done():
				s.egressMu.Unlock()
				close(done)
				return
			default:
			}
		}
		s.egressMu.Unlock()
		close(done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			s.egressCond.Broadcast()
		case <-done:
		}
	}()
	select {
	case <-ctx.Done():
	case <-done:
	}
	s.egressMu.Lock()
	cache := s.egressCache
	s.egressMu.Unlock()
	return cache
}

func (s *Service) StartTraceTask(host string, maxHops int) TraceResult {
	s.traceMu.Lock()
	if s.traceCancel != nil {
		s.traceCancel()
	}
	ctx, cancel := context.WithCancel(s.traceCtx())
	task := TraceResult{
		Target:    host,
		Timestamp: localTimestamp(),
		Tool:      "mtr",
		Running:   true,
	}
	s.traceTask = task
	s.traceCancel = cancel
	s.traceMu.Unlock()

	go func() {
		result := RunTrace(ctx, host, maxHops, func(update TraceResult) {
			s.traceMu.Lock()
			s.traceTask = update
			s.traceMu.Unlock()
		})
		s.traceMu.Lock()
		result.Running = false
		result.Finished = true
		s.traceTask = result
		s.traceCancel = nil
		s.traceMu.Unlock()
	}()

	return task
}

func (s *Service) GetTraceTask() TraceResult {
	s.traceMu.Lock()
	defer s.traceMu.Unlock()
	return s.traceTask
}

func (s *Service) refreshFast(ctx context.Context) {
	s.mu.RLock()
	interval := s.cfg.RefreshInterval
	currentNAT := s.summary.NetworkInfo.NAT
	s.mu.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, interval)
	defer cancel()

	summary, err := s.collectFastSummary(ctx)

	s.mu.Lock()

	if err != nil {
		s.lastError = err.Error()
		if s.summary.GeneratedAt == "" {
			s.summary = emptySummary(interval, s.lastError)
		}
		s.summary.LastError = s.lastError
		s.mu.Unlock()
		return
	}

	summary.NetworkInfo.NAT = currentNAT
	s.lastError = ""
	s.summary = summary
	s.summary.Ready = true
	s.summary.LastError = ""
	s.summary.RefreshIntervalSec = int64(interval / time.Second)
	finalSummary := s.summary
	s.mu.Unlock()

	s.recordTimeseries(finalSummary)
	s.alert.check(finalSummary)
	s.broadcast(finalSummary)
}

func (s *Service) recordTimeseries(summary Summary) {
	point := TimeseriesPoint{
		Timestamp:      summary.GeneratedAt,
		UnixMS:         time.Now().UnixMilli(),
		DomesticStatus: summary.WebsiteConnectivity.DomesticStatus,
		GlobalStatus:   summary.WebsiteConnectivity.GlobalStatus,
		TargetLatency:  map[string]int64{},
		TargetLoss:     map[string]float64{},
		EgressIPv4:     summary.NetworkInfo.EgressIPv4,
		EgressIPv6:     summary.NetworkInfo.EgressIPv6,
		NATType:        summary.NetworkInfo.NAT.Type,
	}
	for _, t := range summary.WebsiteConnectivity.Domestic {
		point.TargetLatency[t.Name] = t.LatencyMS
		point.TargetLoss[t.Name] = t.PacketLossPct
	}
	for _, t := range summary.WebsiteConnectivity.Global {
		point.TargetLatency[t.Name] = t.LatencyMS
		point.TargetLoss[t.Name] = t.PacketLossPct
	}
	s.timeseries.append(point)
}

func (s *Service) refreshNAT(ctx context.Context) {
	s.mu.RLock()
	timeout := s.cfg.NATTimeout
	s.mu.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, timeout*2)
	defer cancel()

	nat := s.ProbeNAT(ctx)
	s.mu.Lock()
	s.summary.NetworkInfo.NAT = nat
	snap := s.summary
	s.mu.Unlock()
	s.alert.check(snap)
	s.broadcast(snap)
}

func (s *Service) collectFastSummary(ctx context.Context) (Summary, error) {
	var website WebsiteConnectivity
	var networkInfo NetworkInfo

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		website = s.ProbeWebsiteConnectivity(ctx)
	}()

	go func() {
		defer wg.Done()
		networkInfo = s.ProbeNetworkInfo(ctx)
		networkInfo.NAT = NATInfo{}
	}()

	wg.Wait()

	return Summary{
		GeneratedAt:         localTimestamp(),
		RefreshIntervalSec:  int64(s.cfg.RefreshInterval / time.Second),
		Ready:               true,
		WebsiteConnectivity: website,
		NetworkInfo:         networkInfo,
	}, nil
}

func (s *Service) collectFastSummaryConditional(ctx context.Context, runConnectivity bool) (Summary, error) {
	if runConnectivity {
		return s.collectFastSummary(ctx)
	}
	s.mu.RLock()
	prev := s.summary
	s.mu.RUnlock()
	return Summary{
		GeneratedAt:         localTimestamp(),
		RefreshIntervalSec:  int64(s.cfg.RefreshInterval / time.Second),
		Ready:               true,
		WebsiteConnectivity: prev.WebsiteConnectivity,
		NetworkInfo:         prev.NetworkInfo,
	}, nil
}

func emptySummary(interval time.Duration, lastError string) Summary {
	return Summary{
		GeneratedAt:        localTimestamp(),
		RefreshIntervalSec: int64(interval / time.Second),
		Ready:              false,
		LastError:          lastError,
	}
}

func summarizeStatus(results []TargetResult) ProbeStatus {
	if len(results) == 0 {
		return StatusUnknown
	}

	hasOK := false
	hasDegraded := false
	for _, result := range results {
		switch result.Status {
		case StatusOK:
			hasOK = true
		case StatusDegraded:
			hasDegraded = true
		}
	}

	switch {
	case hasOK:
		return StatusOK
	case hasDegraded:
		return StatusDegraded
	default:
		return StatusDown
	}
}

func localTimestamp() string {
	return time.Now().Format(time.DateTime)
}

func clampProgress(progress int) int {
	switch {
	case progress < 0:
		return 0
	case progress > 100:
		return 100
	default:
		return progress
	}
}

func sanitizeSpeedMetric(value float64, min float64, max float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func validSpeedMbps(primary float64, fallback float64) float64 {
	if !math.IsNaN(primary) && !math.IsInf(primary, 0) && primary > 0 {
		return primary
	}
	if !math.IsNaN(fallback) && !math.IsInf(fallback, 0) && fallback > 0 {
		return fallback
	}
	return 0
}

func broadbandTaskTimeout(duration time.Duration) time.Duration {
	if duration <= 0 {
		duration = 15 * time.Second
	}
	timeout := duration*4 + 90*time.Second
	if timeout < 2*time.Minute {
		return 2 * time.Minute
	}
	return timeout
}

func progressRange(elapsed, total time.Duration, width int) int {
	if total <= 0 || width <= 0 {
		return 0
	}
	ratio := float64(elapsed) / float64(total)
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	return int(math.Round(ratio * float64(width)))
}

func elapsedMS(startedAt time.Time) int64 {
	if startedAt.IsZero() {
		return 0
	}
	return int64(time.Since(startedAt) / time.Millisecond)
}

func (s *Service) pushBroadbandHistory(result BroadbandSpeedResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadbandHistory = append([]BroadbandSpeedResult{result}, s.broadbandHistory...)
	if len(s.broadbandHistory) > maxHistoryItems {
		s.broadbandHistory = s.broadbandHistory[:maxHistoryItems]
	}
	s.saveBroadbandHistory()
}

func (s *Service) pushLocalTransferHistory(result LocalTransferResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localTransferHistory = append([]LocalTransferResult{result}, s.localTransferHistory...)
	if len(s.localTransferHistory) > maxHistoryItems {
		s.localTransferHistory = s.localTransferHistory[:maxHistoryItems]
	}
	s.saveLocalTransferHistory()
}

func (s *Service) startAppTrafficSampling() {
	go func() {
		defer close(s.appTrafficDone)
		lastSampled := make(map[string]time.Time)
		for {
			enabled, interval, perApp, persistent := s.getTrafficSamplingConfig()
			if !enabled {
				// Wait and re-check
				select {
				case <-s.appTrafficStop:
					return
				case <-time.After(5 * time.Second):
					continue
				}
			}

			now := time.Now()
			s.appTrafficHistory.sampleWithTiming(persistent, lastSampled, now, interval, perApp)

			// Sleep until the next bridge needs sampling
			sleep := time.Duration(interval) * time.Second
			for _, iv := range perApp {
				if iv > 0 {
					d := time.Duration(iv) * time.Second
					if d < sleep {
						sleep = d
					}
				}
			}
			if sleep < 1*time.Second {
				sleep = 1 * time.Second
			}

			select {
			case <-s.appTrafficStop:
				return
			case <-time.After(sleep):
			}
		}
	}()
}

func (s *Service) getTrafficSamplingConfig() (enabled bool, interval int, perApp map[string]int, persistent []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	enabled = s.trafficSamplingEnabled
	interval = s.trafficSamplingInterval
	if interval < 5 {
		interval = 60
	}
	perApp = make(map[string]int, len(s.perAppSamplingInterval))
	for k, v := range s.perAppSamplingInterval {
		perApp[k] = v
	}
	persistent = append([]string(nil), s.persistentTrafficBridges...)
	return
}

func (s *Service) getPersistentTrafficBridges() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.persistentTrafficBridges...)
}

func (s *Service) TogglePersistentTrafficBridge(bridge string, enabled bool) MutableSettings {
	s.mu.Lock()
	found := false
	for i, b := range s.persistentTrafficBridges {
		if b == bridge {
			found = true
			if !enabled {
				s.persistentTrafficBridges = append(s.persistentTrafficBridges[:i], s.persistentTrafficBridges[i+1:]...)
			}
			break
		}
	}
	if enabled && !found {
		s.persistentTrafficBridges = append(s.persistentTrafficBridges, bridge)
	}
	dataDir := s.cfg.DataDir
	s.mu.Unlock()

	settings := s.GetMutableSettings()
	if err := saveMutableSettings(dataDir, settings); err != nil {
		logger.Warn("saveMutableSettings: %v", err)
	}
	return settings
}

func (s *Service) GetAppTrafficHistory(bridge string, limit int) []AppTrafficPoint {
	return s.appTrafficHistory.snapshot(bridge, limit)
}

func (s *Service) GetAppTrafficHistorySince(bridge string, since time.Time, limit int) []AppTrafficPoint {
	return s.appTrafficHistory.snapshotSince(bridge, since, limit)
}

func (s *Service) GetAppTrafficTop(since time.Time, limit int) []AppTrafficTopItem {
	return s.appTrafficHistory.topSince(since, limit)
}

func (s *Service) SampleAppTrafficBridge(bridge string, since time.Time, limit int) (AppTrafficLiveResult, bool) {
	if ok := s.appTrafficHistory.sampleOne(bridge, time.Now()); !ok {
		return AppTrafficLiveResult{}, false
	}
	stats, ok := CollectBridgeTraffic(bridge)
	if !ok {
		return AppTrafficLiveResult{}, false
	}
	return AppTrafficLiveResult{
		GeneratedAt: localTimestamp(),
		Bridge:      stats,
		History:     s.appTrafficHistory.snapshotSince(bridge, since, limit),
	}, true
}

// IPv6RenewNIC 是一块可执行 IPv6 续约的网卡(透传给前端选择)。
type IPv6RenewNIC struct {
	Device     string `json:"device"`
	Type       string `json:"type"`
	State      string `json:"state"`
	Connection string `json:"connection,omitempty"`
}

// IPv6RenewResult 是续约操作的结果。
type IPv6RenewResult struct {
	OK     bool   `json:"ok"`
	Device string `json:"device,omitempty"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ListIPv6RenewNICs 列出当前可续约的网卡(经 lzc-sdk 调用系统 nmcli)。
// SDK 不可用(非懒猫环境)时返回 error,前端据此隐藏功能。
func (s *Service) ListIPv6RenewNICs(ctx context.Context) ([]IPv6RenewNIC, error) {
	if !lzcsdk.Available() {
		return nil, errors.New("lzc-sdk 不可用(非懒猫环境)")
	}
	devs, err := lzcsdk.ListReapplicableNICs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]IPv6RenewNIC, 0, len(devs))
	for _, d := range devs {
		out = append(out, IPv6RenewNIC{
			Device:     d.Device,
			Type:       d.Type,
			State:      d.State,
			Connection: d.Connection,
		})
	}
	return out, nil
}

// RenewIPv6 对指定网卡执行 reapply,触发其重新获取 IPv6 配置。
func (s *Service) RenewIPv6(ctx context.Context, iface string) IPv6RenewResult {
	if !lzcsdk.Available() {
		return IPv6RenewResult{Error: "lzc-sdk 不可用(非懒猫环境)"}
	}
	out, err := lzcsdk.ReapplyNIC(ctx, iface)
	if err != nil {
		logger.Warn("ipv6 renew failed iface=%s err=%v", iface, err)
		return IPv6RenewResult{Device: iface, Error: err.Error()}
	}
	logger.Info("ipv6 renew ok iface=%s out=%q", iface, strings.TrimSpace(out))
	return IPv6RenewResult{OK: true, Device: iface, Output: strings.TrimSpace(out)}
}

// ListContainers returns containers grouped by app (bridge).
func (s *Service) ListContainers(ctx context.Context) AppContainersResponse {
	var empty AppContainersResponse
	if !dockerlzc.Available() {
		return empty
	}
	infos, err := dockerlzc.ListContainerRuntime(ctx)
	if err != nil {
		logger.Warn("ListContainers: list runtime: %v", err)
		return empty
	}
	bridgeMap, err := dockerlzc.BuildBridgeMap(ctx)
	if err != nil {
		logger.Warn("ListContainers: build bridge map: %v", err)
	}

	// Group containers by project
	type projGroup struct {
		Project string
		AppID   string
		Bridge  string
		Title   string
		Conts   []ContainerRuntimeInfo
	}
	projGroups := map[string]*projGroup{}
	projBridge := map[string]string{} // project → first bridge found
	for bridge, info := range bridgeMap {
		projBridge[info.Project] = bridge
	}
	for _, c := range infos {
		proj := c.Project
		if proj == "" {
			proj = "_ungrouped"
		}
		g, ok := projGroups[proj]
		if !ok {
			g = &projGroup{Project: c.Project, AppID: c.AppID, Bridge: projBridge[proj]}
			if bi, has := bridgeMap[g.Bridge]; has {
				g.AppID = bi.AppID
				g.Title = bi.Title
			}
			projGroups[proj] = g
		}
		g.Conts = append(g.Conts, ContainerRuntimeInfo{
			ID: c.ID, Name: c.Name, Image: c.Image, State: c.State,
		})
	}

	s.mu.RLock()
	blocked := s.blockedBridges
	s.mu.RUnlock()

	apps := make([]AppContainerGroup, 0, len(projGroups))
	for _, g := range projGroups {
		if g.Bridge == "" && len(g.Conts) == 0 {
			continue
		}
		blockMode := blocked[g.Bridge]
		// Skip whitelisted apps (cannot be blocked)
		if isWhitelistedApp(g.AppID, g.Title) {
			continue
		}
		appTitle := g.Title
		if appTitle == "" {
			appTitle = g.AppID
		}
		apps = append(apps, AppContainerGroup{
			Bridge:     g.Bridge,
			AppID:      g.AppID,
			AppTitle:   appTitle,
			Project:    g.Project,
			BlockMode:  blockMode,
			Containers: g.Conts,
		})
	}
	return AppContainersResponse{Applications: apps}
}

// BlockApp blocks all containers in an app's bridge.
func (s *Service) BlockApp(ctx context.Context, bridge, mode string) error {
	findBinPaths()
	logger.Info("BlockApp bridge=%s mode=%s", bridge, mode)

	if bridge == "" {
		return fmt.Errorf("bridge name is required")
	}

	// Check whitelist
	if dockerlzc.Available() {
		bridgeMap, err := dockerlzc.BuildBridgeMap(ctx)
		if err == nil {
			if info, ok := bridgeMap[bridge]; ok {
				if isWhitelistedApp(info.AppID, info.Title) {
					return fmt.Errorf("app %s is whitelisted and cannot be blocked", info.Title)
				}
			}
		}
	}

	// Check bridge exists
	if _, err := os.Stat(fmt.Sprintf("/sys/class/net/%s", bridge)); os.IsNotExist(err) {
		return fmt.Errorf("bridge %s not found on host", bridge)
	}

	err := bridgeBlockInternet(bridge)
	if err != nil {
		logger.Warn("bridge block internet via iptables failed: %v; trying nsenter fallback", err)
		if nsenterErr := s.blockAppInternetViaContainers(ctx, bridge); nsenterErr != nil {
			return fmt.Errorf("bridge block internet: iptables: %w; nsenter: %v", err, nsenterErr)
		}
	}

	s.mu.Lock()
	s.blockedBridges[bridge] = "internet"
	s.mu.Unlock()
	s.saveBlockedBridges()
	return nil
}

// blockAppInternetViaContainers falls back to nsenter into each container.
func (s *Service) blockAppInternetViaContainers(ctx context.Context, bridge string) error {
	logger.Info("blockAppInternetViaContainers bridge=%s", bridge)
	if !nsenterAvailable() || !ipAvailable() {
		return fmt.Errorf("nsenter/ip not available for internet block fallback")
	}
	infos, err := dockerlzc.ListContainerRuntime(ctx)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	// Find containers belonging to this bridge's project
	// We need the project name for this bridge
	bridgeMap, err := dockerlzc.BuildBridgeMap(ctx)
	if err != nil {
		return fmt.Errorf("build bridge map: %w", err)
	}
	appInfo, ok := bridgeMap[bridge]
	if !ok {
		return fmt.Errorf("bridge %s not found in bridge map", bridge)
	}
	var lastErr error
	for _, c := range infos {
		if c.Project != appInfo.Project || c.PID <= 0 {
			continue
		}
		if _, _, err := containerBlockInternet(c.PID); err != nil {
			logger.Warn("block internet for container %s (pid %d): %v", c.Name, c.PID, err)
			lastErr = err
		} else {
			logger.Info("blocked internet for container %s (pid %d)", c.Name, c.PID)
		}
	}
	return lastErr
}

// UnblockApp restores network for all containers in an app's bridge.
func (s *Service) UnblockApp(ctx context.Context, bridge string) error {
	findBinPaths()
	logger.Info("UnblockApp bridge=%s", bridge)

	if bridge == "" {
		return fmt.Errorf("bridge name is required")
	}

	s.mu.RLock()
	mode := s.blockedBridges[bridge]
	s.mu.RUnlock()

	if mode == "all" {
		// Backward compat: unblock bridges that were blocked with "all" mode
		_ = bridgeUnblockAll(bridge)
	}
	if iptablesAvailable() {
		if err := bridgeUnblockInternet(bridge); err != nil {
			logger.Warn("bridge unblock internet via iptables: %v", err)
		}
	}
	if err := s.unblockAppInternetViaContainers(ctx, bridge); err != nil {
		logger.Warn("bridge unblock internet fallback: %v", err)
	}

	s.mu.Lock()
	delete(s.blockedBridges, bridge)
	s.mu.Unlock()
	s.saveBlockedBridges()
	return nil
}

func (s *Service) unblockAppInternetViaContainers(ctx context.Context, bridge string) error {
	if !nsenterAvailable() || !ipAvailable() {
		return nil
	}
	infos, err := dockerlzc.ListContainerRuntime(ctx)
	if err != nil {
		return err
	}
	bridgeMap, err := dockerlzc.BuildBridgeMap(ctx)
	if err != nil {
		return err
	}
	appInfo, ok := bridgeMap[bridge]
	if !ok {
		return nil
	}
	for _, c := range infos {
		if c.Project != appInfo.Project || c.PID <= 0 {
			continue
		}
		routes := containerDefaultRoutes(c.PID)
		for iface, gw := range routes {
			if err := containerUnblockInternet(c.PID, gw, iface); err != nil {
				logger.Warn("unblock internet for %s: %v", c.Name, err)
			}
		}
	}
	return nil
}

func (s *Service) saveBlockedBridges() {
	s.mu.RLock()
	bridges := make(map[string]string, len(s.blockedBridges))
	for k, v := range s.blockedBridges {
		bridges[k] = v
	}
	dataDir := s.cfg.DataDir
	s.mu.RUnlock()
	settings := s.GetMutableSettings()
	settings.BlockedBridges = bridges
	if err := saveMutableSettings(dataDir, settings); err != nil {
		logger.Warn("save blocked bridges: %v", err)
	}
}
