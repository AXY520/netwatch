package probe

import (
	"context"
	"sync"
	"time"

	"netwatch/internal/dockerlzc"
	"netwatch/internal/logger"
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
	broadbandHistory      []BroadbandSpeedResult
	localTransferHistory  []LocalTransferResult
	timeseries            *timeseriesStore
	alert                 *alertState
	alertWebhookURL       string
	nicStats              *nicStatsTracker
	egressCache           EgressLookupResult
	egressMu              sync.Mutex
	egressCond            *sync.Cond
	egressInflight        bool
	publicIPCache         publicIPCacheData
	publicIPMu            sync.Mutex
	events                *networkEventStore
	lastConnectivityProbe time.Time
	lastEgressProbe       time.Time

	// lifecycle workers
	nicStop               chan struct{}
	nicDone               chan struct{}
	appLifecycleStop      chan struct{}
	appLifecycleDone      chan struct{}
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

	// domain subsystems
	settings              *settingsStore
	lan                   *lanHub
	notify                *notifyHub
	containers            *containerControlState
	appTraffic            *appTrafficState
	appTrafficLimiter     *appTrafficLimiter
	appNetworkController  *appNetworkController
	appInternetController *appInternetController
	appProxyController    *appProxyController
	network               *networkMutationState
	tasks                 *taskRuntime

	closeCtx    context.Context
	closeCancel context.CancelFunc
}

func NewService(cfg Config) *Service {
	def := DefaultMutableSettings()
	saved, hasSaved := loadMutableSettings(cfg.DataDir)
	s := &Service{
		cfg:                   cfg,
		timeseries:            newTimeseriesStore(cfg.DataDir),
		alert:                 newAlertState(),
		nicStats:              newNICStatsTracker(),
		nicStop:               make(chan struct{}),
		nicDone:               make(chan struct{}),
		events:                newNetworkEventStore(cfg.DataDir),
		appLifecycleStop:      make(chan struct{}),
		appLifecycleDone:      make(chan struct{}),
		backgroundMonitorStop: make(chan struct{}),
		backgroundMonitorDone: make(chan struct{}),
		lanInterfaceStop:      make(chan struct{}),
		lanInterfaceDone:      make(chan struct{}),
		settings:              newSettingsStore(def),
		lan:                   newLANHub(cfg.DataDir, def),
		notify:                newNotifyHub(cfg.DataDir, def),
		containers:            newContainerControlState(),
		appTraffic:            newAppTrafficState(cfg.DataDir),
		appTrafficLimiter:     newAppTrafficLimiter(),
		network:               newNetworkMutationState(),
		tasks:                 newTaskRuntime(),
	}
	s.egressCond = sync.NewCond(&s.egressMu)
	s.appNetworkController = newAppNetworkController(s, s.appTrafficLimiter)
	s.appInternetController = newAppInternetController()
	s.appProxyController = newAppProxyController()
	s.nicStats.onSampled = s.broadcastNICRealtime
	s.closeCtx, s.closeCancel = context.WithCancel(context.Background())
	s.loadHistory()
	if hasSaved {
		s.applyMutableSettings(saved, false)
	}
	// Host network controls are opt-in. Clean rules persisted by an older
	// enabled session before any worker or request can observe them.
	if !s.settings.hostNetworkExperimental() {
		s.clearHostNetworkExperimentalState(true)
	}
	s.nicStats.start(s.nicStop, s.nicDone)
	s.startAppLifecycleObserver()
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

	// Rebuild the single host network mutation and its rollback timer from disk.
	go func() {
		s.restoreNetworkMutation()
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

func (s *Service) Close() {
	s.closeOnce.Do(func() {
		if s.tasks != nil {
			s.tasks.cancelAll()
		}
		s.closeCancel()
		close(s.nicStop)
		close(s.appLifecycleStop)
		close(s.backgroundMonitorStop)
		close(s.lanInterfaceStop)
		<-s.nicDone
		<-s.appLifecycleDone
		<-s.backgroundMonitorDone
		<-s.lanInterfaceDone
		if s.appTraffic != nil {
			s.appTraffic.flush()
		}
		if s.appTrafficLimiter != nil && s.appTrafficLimiter.host != nil {
			if err := s.appTrafficLimiter.host.close(); err != nil {
				logger.Warn("close Host/Mixed traffic limiter: %v", err)
			}
		}
		if s.appProxyController != nil {
			s.appProxyController.close()
		}
		resetHostCgroupBPF()
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
	return s.lan.getDevicesCopy()
}

// putLANDevices updates the in-memory map and persists to disk.
func (s *Service) putLANDevices(devices map[string]LANDevice) error {
	return s.lan.putDevices(devices)
}

// putLANDevicesLocked updates the in-memory map while lan lock is already held, then persists.
func (s *Service) putLANDevicesLocked(devices map[string]LANDevice) error {
	return s.lan.putDevicesLocked(devices)
}

func (s *Service) GetMutableSettings() MutableSettings {
	s.mu.RLock()
	out := MutableSettings{
		RefreshIntervalSec:     int(s.cfg.RefreshInterval / time.Second),
		NICRealtimeEnabled:     s.nicStats.enabled(),
		NICRealtimeIntervalSec: s.nicStats.intervalSeconds(),
		DomesticSites:          append([]SiteTarget(nil), s.cfg.DomesticSites...),
		GlobalSites:            append([]SiteTarget(nil), s.cfg.GlobalSites...),
		AlertWebhookURL:        s.alertWebhookURL,
		BlockedApps:            s.containers.snapshotBlockedApps(),
		BlockedBridges:         s.containers.snapshotBlocked(),
	}
	s.mu.RUnlock()
	s.settings.writeToSettings(&out)
	s.lan.writePolicy(&out)
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
	if len(in.DomesticSites) > 0 {
		s.cfg.DomesticSites = in.DomesticSites
	}
	if len(in.GlobalSites) > 0 {
		s.cfg.GlobalSites = in.GlobalSites
	}
	s.alertWebhookURL = in.AlertWebhookURL
	if in.BlockedBridges != nil {
		s.containers.replaceBlocked(in.BlockedBridges)
	}
	if in.BlockedApps != nil {
		s.containers.replaceBlockedApps(in.BlockedApps)
	}
	dataDir := s.cfg.DataDir
	s.mu.Unlock()

	previousHostExperimental := s.settings.hostNetworkExperimental()
	if persist {
		// Proxy changes need firewall reconciliation and rollback, so the generic
		// settings endpoint cannot mutate them behind the dedicated controller.
		in.AppProxy, in.ProxyApps, in.AppProxyConfigs = s.settings.appProxyState()
	}
	s.settings.apply(in)
	if previousHostExperimental != in.HostNetworkExperimentalEnabled {
		InvalidateAppTrafficMetadataCache()
		dockerlzc.InvalidateBridgeMapCache()
	}
	if persist && !in.HostNetworkExperimentalEnabled {
		s.clearHostNetworkExperimentalState(false)
	}
	s.lan.applyPolicy(in)
	s.notify.applyFromSettings(in)
	s.nicStats.configure(in.NICRealtimeEnabled, in.NICRealtimeIntervalSec)
	s.alert.setWebhook(in.AlertWebhookURL)
	if persist {
		if err := saveMutableSettings(dataDir, s.GetMutableSettings()); err != nil {
			logger.Warn("saveMutableSettings: %v", err)
		}
	}
}
