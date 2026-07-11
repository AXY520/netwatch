package probe

import (
	"context"
	"errors"
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
	cfg       Config
	mu        sync.RWMutex
	closeOnce sync.Once
	summary   Summary
	lastError string

	// observation / history
	broadbandHistory     []BroadbandSpeedResult
	localTransferHistory []LocalTransferResult
	timeseries           *timeseriesStore
	alert                *alertState
	alertWebhookURL      string
	nicStats             *nicStatsTracker
	egressCache          EgressLookupResult
	egressMu             sync.Mutex
	egressCond           *sync.Cond
	egressInflight       bool
	publicIPCache        publicIPCacheData
	publicIPMu           sync.Mutex
	appTrafficHistory    *appTrafficHistoryStore

	// lifecycle workers
	nicStop               chan struct{}
	nicDone               chan struct{}
	appTrafficStop        chan struct{}
	appTrafficDone        chan struct{}
	backgroundMonitorStop chan struct{}
	backgroundMonitorDone chan struct{}
	lanInterfaceStop      chan struct{}
	lanInterfaceDone      chan struct{}

	// pubsub
	subs            []chan Summary
	subsMu          sync.Mutex
	nicSubs         []chan RealtimeNetStats
	nicSubsMu       sync.Mutex
	lanDeviceSubs   []chan LANDeviceSnapshot
	lanDeviceSubsMu sync.Mutex

	// settings not owned by notify/control hubs
	chartTimeLabelInterval      int
	containerControlEnabled     bool
	trafficSamplingEnabled      bool
	trafficSamplingInterval     int
	perAppSamplingInterval      map[string]int
	persistentTrafficBridges    []string
	backgroundMonitorEnabled    bool
	backgroundMonitorInterval   int
	lastConnectivityProbe       time.Time
	lastEgressProbe             time.Time
	lanDeviceOfflineAfter       int
	lanDeviceOnlineAfter        int
	lanDeviceOfflineNotifyDelay int
	lanDeviceOnlineNotifyDelay  int
	lanMaxCheckAttempts         int
	lanNotifyCooldownSec        int
	lanFlappingThreshold        int
	lanFlappingWindow           time.Duration
	lanDeviceAutoRemoveDays     int

	// LAN observation state
	lanMu              sync.Mutex
	lanDeviceMap       map[string]LANDevice
	lanSnapshot        LANDeviceSnapshot
	lanInterfaceState  map[string]bool
	lanNotifyCooldown  map[string]time.Time
	lanFlappingHistory map[string][]time.Time

	// domain subsystems
	notify  *notifyHub
	control *controlState
	tasks   *taskRuntime

	closeCtx    context.Context
	closeCancel context.CancelFunc
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
		chartTimeLabelInterval:      def.ChartTimeLabelInterval,
		containerControlEnabled:     def.ContainerControlEnabled,
		trafficSamplingEnabled:      def.TrafficSamplingEnabled,
		trafficSamplingInterval:     def.TrafficSamplingIntervalSec,
		perAppSamplingInterval:      make(map[string]int),
		backgroundMonitorEnabled:    def.BackgroundMonitorEnabled,
		backgroundMonitorInterval:   def.BackgroundMonitorIntervalSec,
		lanDeviceOfflineAfter:       def.LANDeviceOfflineAfterSec,
		lanDeviceOnlineAfter:        def.LANDeviceOnlineAfterSec,
		lanDeviceOfflineNotifyDelay: def.LANDeviceOfflineNotifyDelaySec,
		lanDeviceOnlineNotifyDelay:  def.LANDeviceOnlineNotifyDelaySec,
		lanDeviceAutoRemoveDays:     def.LANDeviceAutoRemoveDays,
		notify:                      newNotifyHub(cfg.DataDir, def),
		control:                     newControlState(),
		tasks:                       newTaskRuntime(),
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
		if s.tasks != nil {
			s.tasks.cancelAll()
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
	perApp := make(map[string]int, len(s.perAppSamplingInterval))
	for k, v := range s.perAppSamplingInterval {
		perApp[k] = v
	}
	out := MutableSettings{
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
		LANDeviceOfflineAfterSec:       s.lanDeviceOfflineAfter,
		LANDeviceOnlineAfterSec:        s.lanDeviceOnlineAfter,
		LANDeviceOfflineNotifyDelaySec: s.lanDeviceOfflineNotifyDelay,
		LANDeviceOnlineNotifyDelaySec:  s.lanDeviceOnlineNotifyDelay,
		LANMaxCheckAttempts:            s.lanMaxCheckAttempts,
		LANNotifyCooldownSec:           s.lanNotifyCooldownSec,
		LANFlappingThreshold:           s.lanFlappingThreshold,
		LANFlappingWindowSec:           int(s.lanFlappingWindow.Seconds()),
		LANDeviceAutoRemoveDays:        s.lanDeviceAutoRemoveDays,
		BlockedBridges:                 s.control.snapshotBlocked(),
	}
	s.mu.RUnlock()
	s.notify.writeToSettings(&out)
	return out
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
	if in.BlockedBridges != nil {
		s.control.replaceBlocked(in.BlockedBridges)
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

	s.notify.applyFromSettings(in)
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
