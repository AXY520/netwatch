package probe

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"netwatch/internal/logger"
)

type AppNetworkTargetKind string

const (
	AppNetworkTargetBridge AppNetworkTargetKind = "bridge"
	AppNetworkTargetCgroup AppNetworkTargetKind = "cgroup"
)

// AppNetworkTarget is the runtime binding between an application and a kernel
// enforcement/accounting point. IDs remain compatible with the legacy API,
// while Kind removes the need for callers to infer semantics from prefixes.
type AppNetworkTarget struct {
	ID                string               `json:"id"`
	Kind              AppNetworkTargetKind `json:"kind"`
	AppID             string               `json:"app_id"`
	InstanceID        string               `json:"instance_id,omitempty"`
	Interface         string               `json:"interface,omitempty"`
	CgroupPath        string               `json:"cgroup_path,omitempty"`
	NetworkMode       string               `json:"network_mode"`
	AccountingSource  string               `json:"accounting_source,omitempty"`
	Diagnostic        string               `json:"diagnostic,omitempty"`
	ControlDiagnostic string               `json:"control_diagnostic,omitempty"`
}

type AppNetworkCapabilities struct {
	Accounting      bool `json:"accounting"`
	UploadLimit     bool `json:"upload_limit"`
	DownloadLimit   bool `json:"download_limit"`
	InternetControl bool `json:"internet_control"`
	ProxyControl    bool `json:"proxy_control"`
}

type AppNetworkTargetStatus struct {
	Target       AppNetworkTarget       `json:"target"`
	Capabilities AppNetworkCapabilities `json:"capabilities"`
	Blocked      bool                   `json:"blocked"`
	Proxied      bool                   `json:"proxied"`
	ProxyInSync  bool                   `json:"proxy_in_sync"`
	LimitInSync  bool                   `json:"limit_in_sync"`
	Diagnostic   string                 `json:"diagnostic,omitempty"`
}

type AppNetworkPolicy struct {
	UploadKbps      int64 `json:"upload_kbps"`
	DownloadKbps    int64 `json:"download_kbps"`
	InternetAllowed bool  `json:"internet_allowed"`
	ProxyEnabled    bool  `json:"proxy_enabled"`
}

type appNetworkPolicyUpdate struct {
	UploadKbps      *int64
	DownloadKbps    *int64
	InternetAllowed *bool
	ProxyEnabled    *bool
	ProxySettings   *AppProxySettings
}

type AppNetworkPolicyStatus struct {
	Desired        AppNetworkPolicy         `json:"desired"`
	Capabilities   AppNetworkCapabilities   `json:"capabilities"`
	LimitState     string                   `json:"limit_state"`
	LimitInSync    bool                     `json:"limit_in_sync"`
	InternetState  string                   `json:"internet_state"`
	InternetInSync bool                     `json:"internet_in_sync"`
	ProxyState     string                   `json:"proxy_state"`
	ProxyInSync    bool                     `json:"proxy_in_sync"`
	ProxySettings  AppProxySettings         `json:"proxy_settings"`
	Diagnostic     string                   `json:"diagnostic,omitempty"`
	Targets        []AppNetworkTargetStatus `json:"targets"`
}

type appNetworkTargetDriver interface {
	Capabilities() AppNetworkCapabilities
	ApplyLimit(context.Context, AppNetworkTarget, AppTrafficLimit) error
}

type appNetworkController struct {
	mu                    sync.Mutex
	drivers               map[AppNetworkTargetKind]appNetworkTargetDriver
	internetAvailable     func() bool
	reconcileInternet     func(context.Context, []AppBridgeStats, map[string]string) error
	persistInternetPolicy func() error
}

type bridgeNetworkDriver struct {
	service *Service
	limiter *appTrafficLimiter
}

type cgroupNetworkDriver struct {
	service *Service
	limiter *appTrafficLimiter
}

func newAppNetworkController(service *Service, limiter *appTrafficLimiter) *appNetworkController {
	controller := &appNetworkController{
		drivers: map[AppNetworkTargetKind]appNetworkTargetDriver{
			AppNetworkTargetBridge: bridgeNetworkDriver{service: service, limiter: limiter},
			AppNetworkTargetCgroup: cgroupNetworkDriver{service: service, limiter: limiter},
		},
		internetAvailable: appFirewallAvailable,
	}
	if service != nil {
		controller.reconcileInternet = service.reconcileAppInternetControls
		controller.persistInternetPolicy = service.persistBlockedState
	}
	return controller
}

func (c *appNetworkController) canControlInternet() bool {
	return c != nil && c.internetAvailable != nil && c.internetAvailable()
}

func (c *appNetworkController) reconcileInternetPolicy(ctx context.Context, items []AppBridgeStats, desired map[string]string) error {
	if c == nil || c.reconcileInternet == nil {
		return errors.New("application internet policy reconciler is unavailable")
	}
	return c.reconcileInternet(ctx, items, desired)
}

func (c *appNetworkController) persistInternet() error {
	if c == nil || c.persistInternetPolicy == nil {
		return errors.New("application internet policy persistence is unavailable")
	}
	return c.persistInternetPolicy()
}

func (c *appNetworkController) driver(target AppNetworkTarget) (appNetworkTargetDriver, error) {
	if c == nil {
		return nil, errors.New("application network controller is unavailable")
	}
	driver := c.drivers[target.Kind]
	if driver == nil {
		return nil, fmt.Errorf("unsupported application network target kind %q", target.Kind)
	}
	return driver, nil
}

func (d bridgeNetworkDriver) Capabilities() AppNetworkCapabilities {
	return AppNetworkCapabilities{Accounting: true, UploadLimit: true, DownloadLimit: true, InternetControl: true, ProxyControl: true}
}

func (d bridgeNetworkDriver) ApplyLimit(ctx context.Context, target AppNetworkTarget, limit AppTrafficLimit) error {
	if d.limiter == nil {
		return errors.New("application traffic limiter is unavailable")
	}
	return d.limiter.apply(ctx, target.Interface, limit)
}

func (d cgroupNetworkDriver) Capabilities() AppNetworkCapabilities {
	return AppNetworkCapabilities{Accounting: true, UploadLimit: true, DownloadLimit: true, InternetControl: true, ProxyControl: true}
}

func (d cgroupNetworkDriver) ApplyLimit(ctx context.Context, target AppNetworkTarget, limit AppTrafficLimit) error {
	if d.limiter == nil || d.limiter.host == nil {
		return errors.New("Host/Mixed traffic limiter is unavailable")
	}
	policyID := appNetworkTargetPolicyID(target)
	targets := appNetworkTargetsForApp(CollectAppTraffic().Bridges, policyID)
	return d.limiter.host.apply(ctx, policyID, targets, limit)
}

func appNetworkTargetFromStats(item AppBridgeStats) (AppNetworkTarget, bool) {
	if item.Target.ID != "" && item.Target.Kind != "" {
		return item.Target, true
	}
	appID := strings.TrimSpace(item.AppID)
	instanceID := appTrafficPolicyID(item)
	if appID == "" {
		return AppNetworkTarget{}, false
	}
	if item.NetworkMode == "host" || strings.HasPrefix(item.Bridge, hostAppTargetPrefix) {
		id := strings.TrimSpace(item.ControlTarget)
		if id == "" {
			id = hostAppTarget(instanceID)
		}
		return AppNetworkTarget{
			ID: id, Kind: AppNetworkTargetCgroup, AppID: appID, InstanceID: instanceID,
			CgroupPath: item.CgroupPath, NetworkMode: "host", AccountingSource: item.Source, Diagnostic: item.Diagnostic,
		}, true
	}
	if strings.HasPrefix(item.Bridge, lzcBridgePrefix) {
		return AppNetworkTarget{
			ID: item.Bridge, Kind: AppNetworkTargetBridge, AppID: appID, InstanceID: instanceID,
			Interface: item.Bridge, NetworkMode: "bridge", AccountingSource: item.Source,
		}, true
	}
	return AppNetworkTarget{}, false
}

func appNetworkTargetsForApp(items []AppBridgeStats, policyID string) []AppNetworkTarget {
	byID := make(map[string]AppNetworkTarget)
	for _, item := range items {
		if appTrafficPolicyID(item) != strings.TrimSpace(policyID) {
			continue
		}
		target, ok := appNetworkTargetFromStats(item)
		if !ok {
			continue
		}
		if current, exists := byID[target.ID]; exists {
			if current.CgroupPath == "" {
				current.CgroupPath = target.CgroupPath
			}
			if current.AccountingSource == "" {
				current.AccountingSource = target.AccountingSource
			}
			if current.Diagnostic == "" {
				current.Diagnostic = target.Diagnostic
			}
			if current.ControlDiagnostic == "" {
				current.ControlDiagnostic = target.ControlDiagnostic
			}
			byID[target.ID] = current
			continue
		}
		byID[target.ID] = target
	}
	targets := make([]AppNetworkTarget, 0, len(byID))
	for _, target := range byID {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Kind != targets[j].Kind {
			return targets[i].Kind < targets[j].Kind
		}
		return targets[i].ID < targets[j].ID
	})
	return targets
}

func appendUniqueAppNetworkTarget(targets []AppNetworkTarget, candidate AppNetworkTarget) []AppNetworkTarget {
	if candidate.ID == "" || candidate.Kind == "" {
		return targets
	}
	for index := range targets {
		if targets[index].ID == candidate.ID {
			if targets[index].CgroupPath == "" {
				targets[index].CgroupPath = candidate.CgroupPath
			}
			if targets[index].AccountingSource == "" {
				targets[index].AccountingSource = candidate.AccountingSource
			}
			if targets[index].Diagnostic == "" {
				targets[index].Diagnostic = candidate.Diagnostic
			}
			if targets[index].ControlDiagnostic == "" {
				targets[index].ControlDiagnostic = candidate.ControlDiagnostic
			}
			return targets
		}
	}
	return append(targets, candidate)
}

func (s *Service) appNetworkPolicyStatus(app AppTrafficUsage, blocked map[string]string) AppNetworkPolicyStatus {
	policyID := appUsagePolicyID(app)
	status := AppNetworkPolicyStatus{
		Desired: AppNetworkPolicy{
			UploadKbps: app.Limit.UploadKbps, DownloadKbps: app.Limit.DownloadKbps, InternetAllowed: true,
		},
		LimitState: "unlimited", LimitInSync: true, InternetState: "allowed", InternetInSync: true,
		ProxyState: "direct", ProxyInSync: true,
	}
	if s.settings != nil {
		status.Desired.ProxyEnabled = s.settings.appProxyEnabled(policyID)
		status.ProxySettings = s.settings.appProxyConfig(policyID)
	}
	if len(app.NetworkTargets) == 0 {
		// Persisted records from before the target model are upgraded from the
		// legacy bridge/host fields at read time.
		for _, bridge := range app.Bridges {
			if target, ok := appNetworkTargetFromStats(AppBridgeStats{AppID: app.AppID, InstanceID: policyID, Bridge: bridge, NetworkMode: app.NetworkTopology}); ok {
				app.NetworkTargets = appendUniqueAppNetworkTarget(app.NetworkTargets, target)
			}
		}
	}
	hasHostTarget := false
	for _, target := range app.NetworkTargets {
		if target.Kind == AppNetworkTargetCgroup {
			hasHostTarget = true
			break
		}
	}
	if app.Limit.UploadKbps > 0 || app.Limit.DownloadKbps > 0 {
		status.LimitState = "limited"
	}
	blockedCount := 0
	proxiedCount := 0
	appBlocked := s.containers != nil && s.containers.appBlocked(policyID) != ""
	desiredBlocked := appBlocked
	inSync := true
	for _, target := range app.NetworkTargets {
		targetStatus := AppNetworkTargetStatus{Target: target, LimitInSync: true, ProxyInSync: true, Diagnostic: target.Diagnostic}
		if s.appNetworkController == nil {
			targetStatus.Diagnostic = "application network controller is unavailable"
			status.Targets = append(status.Targets, targetStatus)
			continue
		}
		driver, err := s.appNetworkController.driver(target)
		if err != nil {
			targetStatus.Diagnostic = err.Error()
		} else {
			targetStatus.Capabilities = driver.Capabilities()
			if !s.appNetworkController.canControlInternet() {
				targetStatus.Capabilities.InternetControl = false
				targetStatus.Capabilities.ProxyControl = false
				if targetStatus.Diagnostic == "" {
					targetStatus.Diagnostic = "宿主机防火墙控制不可用"
				}
			}
			if s.appProxyController == nil {
				targetStatus.Capabilities.ProxyControl = false
			}
			if target.Kind == AppNetworkTargetBridge && !trafficControlAvailable() {
				targetStatus.Capabilities.UploadLimit = false
				targetStatus.Capabilities.DownloadLimit = false
				if targetStatus.Diagnostic == "" {
					targetStatus.Diagnostic = "宿主机 tc 不可用"
				}
			}
			if target.Kind == AppNetworkTargetCgroup && !hostTrafficLimitAvailable() {
				targetStatus.Capabilities.UploadLimit = false
				targetStatus.Capabilities.DownloadLimit = false
				if targetStatus.Diagnostic == "" {
					targetStatus.Diagnostic = "Host/Mixed TC eBPF 限速环境不可用"
				}
			}
			// A cgroup target is enforceable only when its accounting hook was
			// actually attached. Keep accounting capability truthful so callers
			// can distinguish an available driver from a failed eBPF probe.
			if target.Kind == AppNetworkTargetCgroup && target.AccountingSource != "cgroup_skb_ebpf" {
				targetStatus.Capabilities.Accounting = false
				if targetStatus.Diagnostic == "" && target.AccountingSource != "" {
					targetStatus.Diagnostic = "Host cgroup 流量统计不可用：" + target.AccountingSource
				}
			}
		}
		if target.Kind == AppNetworkTargetCgroup && !s.hostNetworkExperimentalEnabled() {
			targetStatus.Capabilities.UploadLimit = false
			targetStatus.Capabilities.DownloadLimit = false
			targetStatus.Capabilities.InternetControl = false
			targetStatus.Capabilities.ProxyControl = false
		}
		if target.ControlDiagnostic != "" {
			targetStatus.Capabilities.UploadLimit = false
			targetStatus.Capabilities.DownloadLimit = false
			targetStatus.Capabilities.InternetControl = false
			targetStatus.Capabilities.ProxyControl = false
			if targetStatus.Diagnostic == "" {
				targetStatus.Diagnostic = target.ControlDiagnostic
			} else if !strings.Contains(targetStatus.Diagnostic, target.ControlDiagnostic) {
				targetStatus.Diagnostic += "；" + target.ControlDiagnostic
			}
			if status.Diagnostic == "" {
				status.Diagnostic = target.ControlDiagnostic
			}
		}
		if isWhitelistedApp(app.AppID, app.AppTitle) {
			targetStatus.Capabilities.UploadLimit = false
			targetStatus.Capabilities.DownloadLimit = false
			targetStatus.Capabilities.InternetControl = false
			targetStatus.Capabilities.ProxyControl = false
		}
		if s.appTrafficLimiter != nil && (target.Kind == AppNetworkTargetBridge || hasHostTarget) {
			desired := AppTrafficLimit{UploadKbps: app.Limit.UploadKbps, DownloadKbps: app.Limit.DownloadKbps}
			runtime := s.appTrafficLimiter.runtimeStatus(target.Interface, desired)
			if hasHostTarget && s.appTrafficLimiter.host != nil {
				runtime = s.appTrafficLimiter.host.runtimeStatus(policyID, desired)
			}
			targetStatus.LimitInSync = runtime.InSync
			if !runtime.InSync {
				status.LimitInSync = false
				if targetStatus.Diagnostic == "" {
					targetStatus.Diagnostic = runtime.Diagnostic
				}
				if status.Diagnostic == "" {
					status.Diagnostic = runtime.Diagnostic
				}
			}
		}
		if blocked[target.ID] != "" {
			desiredBlocked = true
		}
		runtime := appInternetTargetRuntime{InSync: false, Diagnostic: "等待核验宿主机防火墙规则"}
		if s.appInternetController != nil {
			runtime = s.appInternetController.status(target.ID)
		}
		targetStatus.Blocked = runtime.Blocked
		if !runtime.InSync {
			inSync = false
			if targetStatus.Diagnostic == "" {
				targetStatus.Diagnostic = runtime.Diagnostic
			}
			if status.Diagnostic == "" {
				status.Diagnostic = runtime.Diagnostic
			}
		}
		if targetStatus.Blocked {
			blockedCount++
		}
		proxyRuntime := appProxyTargetRuntime{InSync: true}
		if status.Desired.ProxyEnabled && s.appProxyController != nil {
			proxyRuntime = s.appProxyController.status(target.ID)
		} else if status.Desired.ProxyEnabled {
			proxyRuntime.Diagnostic = "应用代理控制器不可用"
		}
		targetStatus.Proxied = proxyRuntime.Active
		targetStatus.ProxyInSync = proxyRuntime.InSync
		if proxyRuntime.Active {
			proxiedCount++
		}
		if status.Desired.ProxyEnabled && !proxyRuntime.InSync {
			status.ProxyInSync = false
			if targetStatus.Diagnostic == "" {
				targetStatus.Diagnostic = proxyRuntime.Diagnostic
			}
			if status.Diagnostic == "" {
				status.Diagnostic = proxyRuntime.Diagnostic
			}
		}
		status.Targets = append(status.Targets, targetStatus)
	}
	status.Capabilities = aggregateAppNetworkCapabilities(status.Targets)
	status.Desired.InternetAllowed = !desiredBlocked
	status.InternetInSync = inSync
	if desiredBlocked {
		switch {
		case inSync && blockedCount == len(status.Targets) && len(status.Targets) > 0:
			status.InternetState = "blocked"
		default:
			status.InternetState = "partial"
		}
	} else if blockedCount > 0 || !inSync {
		status.InternetState = "partial"
	}
	if status.Desired.ProxyEnabled {
		switch {
		case desiredBlocked:
			status.ProxyState = "paused"
		case status.ProxyInSync && proxiedCount == len(status.Targets) && len(status.Targets) > 0:
			status.ProxyState = "proxied"
		default:
			status.ProxyState = "partial"
		}
	}
	if !status.Capabilities.UploadLimit && !status.Capabilities.DownloadLimit {
		status.LimitState = "unsupported"
	}
	if (app.Limit.UploadKbps > 0 || app.Limit.DownloadKbps > 0) && !status.LimitInSync && status.LimitState != "unsupported" {
		status.LimitState = "drifted"
	}
	return status
}

func aggregateAppNetworkCapabilities(targets []AppNetworkTargetStatus) AppNetworkCapabilities {
	if len(targets) == 0 {
		return AppNetworkCapabilities{}
	}
	result := AppNetworkCapabilities{Accounting: true, UploadLimit: true, DownloadLimit: true, InternetControl: true, ProxyControl: true}
	for _, target := range targets {
		result.Accounting = result.Accounting && target.Capabilities.Accounting
		result.UploadLimit = result.UploadLimit && target.Capabilities.UploadLimit
		result.DownloadLimit = result.DownloadLimit && target.Capabilities.DownloadLimit
		result.InternetControl = result.InternetControl && target.Capabilities.InternetControl
		result.ProxyControl = result.ProxyControl && target.Capabilities.ProxyControl
	}
	return result
}

func (s *Service) currentAppNetworkPolicy(policyID string, targets []AppNetworkTarget) AppNetworkPolicy {
	limit := s.appTraffic.limitForApp(policyID)
	blocked := s.containers.snapshotBlocked()
	policy := AppNetworkPolicy{
		UploadKbps: limit.UploadKbps, DownloadKbps: limit.DownloadKbps, InternetAllowed: true,
	}
	policy.ProxyEnabled = s.settings != nil && s.settings.appProxyEnabled(policyID)
	if s.containers.appBlocked(policyID) != "" {
		policy.InternetAllowed = false
	}
	for _, target := range targets {
		if blocked[target.ID] != "" {
			policy.InternetAllowed = false
			break
		}
	}
	return policy
}

func (s *Service) SetAppNetworkPolicy(ctx context.Context, appID string, desired AppNetworkPolicy) error {
	return s.updateAppNetworkPolicy(ctx, appID, appNetworkPolicyUpdate{
		UploadKbps: &desired.UploadKbps, DownloadKbps: &desired.DownloadKbps, InternetAllowed: &desired.InternetAllowed, ProxyEnabled: &desired.ProxyEnabled,
	})
}

func (s *Service) updateAppNetworkPolicy(ctx context.Context, appID string, update appNetworkPolicyUpdate) error {
	return s.updateAppInstanceNetworkPolicy(ctx, appID, "", update)
}

func (s *Service) updateAppInstanceNetworkPolicy(ctx context.Context, appID, instanceID string, update appNetworkPolicyUpdate) error {
	appID = strings.TrimSpace(appID)
	if appID == "" || isNetwatchTrafficItem(AppBridgeStats{AppID: appID}) {
		return errors.New("invalid application id")
	}
	if update.UploadKbps == nil && update.DownloadKbps == nil && update.InternetAllowed == nil && update.ProxyEnabled == nil && update.ProxySettings == nil {
		return errors.New("application network policy update is empty")
	}
	if s.appNetworkController == nil || s.appTrafficLimiter == nil || s.appTraffic == nil || s.containers == nil || s.settings == nil {
		return errors.New("application network controller is unavailable")
	}
	items := CollectAppTraffic().Bridges
	policyID, err := resolveAppInstancePolicyID(items, appID, instanceID)
	if err != nil {
		return err
	}
	targets := appNetworkTargetsForApp(items, policyID)
	if len(targets) == 0 {
		return fmt.Errorf("application instance %s has no controllable network target", policyID)
	}
	return s.applyAppNetworkPolicyUpdateForInstance(ctx, appID, policyID, targets, update)
}

func (s *Service) applyAppNetworkPolicyUpdate(ctx context.Context, appID string, targets []AppNetworkTarget, update appNetworkPolicyUpdate) error {
	policyID := appID
	if len(targets) > 0 && appNetworkTargetPolicyID(targets[0]) != "" {
		policyID = appNetworkTargetPolicyID(targets[0])
	}
	return s.applyAppNetworkPolicyUpdateForInstance(ctx, appID, policyID, targets, update)
}

func (s *Service) applyAppNetworkPolicyUpdateForInstance(ctx context.Context, appID, policyID string, targets []AppNetworkTarget, update appNetworkPolicyUpdate) error {
	current := s.currentAppNetworkPolicy(policyID, targets)
	desired := current
	if update.UploadKbps != nil {
		desired.UploadKbps = *update.UploadKbps
	}
	if update.DownloadKbps != nil {
		desired.DownloadKbps = *update.DownloadKbps
	}
	if update.InternetAllowed != nil {
		desired.InternetAllowed = *update.InternetAllowed
	}
	if update.ProxyEnabled != nil {
		desired.ProxyEnabled = *update.ProxyEnabled
	}
	limitChanged := update.UploadKbps != nil || update.DownloadKbps != nil
	internetChangedRequested := update.InternetAllowed != nil
	proxyChangedRequested := update.ProxyEnabled != nil || update.ProxySettings != nil
	var requestedProxySettings AppProxySettings
	if update.ProxySettings != nil {
		requestedProxySettings = *update.ProxySettings
		requestedProxySettings.Protocol = strings.ToLower(strings.TrimSpace(requestedProxySettings.Protocol))
		requestedProxySettings.Host = strings.TrimSpace(requestedProxySettings.Host)
		if err := validateAppProxySettings(requestedProxySettings); err != nil {
			return err
		}
		if !desired.ProxyEnabled {
			return errors.New("代理配置只能在启用应用代理时更新")
		}
	}
	if desired.UploadKbps < 0 || desired.DownloadKbps < 0 || desired.UploadKbps > maxAppTrafficLimitKbps || desired.DownloadKbps > maxAppTrafficLimitKbps {
		return fmt.Errorf("traffic limit must be between 0 and %d Kbit/s", maxAppTrafficLimitKbps)
	}
	if isWhitelistedApp(appID, "") {
		return fmt.Errorf("application %s does not allow network policy changes", appID)
	}
	if !current.InternetAllowed && !desired.InternetAllowed {
		if proxyChangedRequested && desired.ProxyEnabled {
			return errors.New("已禁用外网，请先恢复外网后再设置代理")
		}
		if limitChanged && (desired.UploadKbps > 0 || desired.DownloadKbps > 0) {
			return errors.New("已禁用外网，请先恢复外网后再限制网速")
		}
	}
	s.appNetworkController.mu.Lock()
	defer s.appNetworkController.mu.Unlock()
	s.appTrafficLimiter.operationMu.Lock()
	defer s.appTrafficLimiter.operationMu.Unlock()

	previousLimit := s.appTraffic.limitForApp(policyID)
	previousBlockedApps := s.containers.snapshotBlockedApps()
	proxyDefault, previousProxyApps, previousProxyConfigs := s.settings.appProxyState()
	candidateBlockedApps := make(map[string]string, len(previousBlockedApps)+1)
	for applicationID, mode := range previousBlockedApps {
		candidateBlockedApps[applicationID] = mode
	}
	if internetChangedRequested {
		if desired.InternetAllowed {
			delete(candidateBlockedApps, policyID)
		} else {
			candidateBlockedApps[policyID] = "internet"
		}
	}
	candidateProxyApps := cloneProxyApps(previousProxyApps)
	candidateProxyConfigs := cloneAppProxyConfigs(previousProxyConfigs)
	if proxyChangedRequested {
		if desired.ProxyEnabled {
			candidateProxyApps[policyID] = true
			if update.ProxySettings != nil {
				candidateProxyConfigs[policyID] = requestedProxySettings
			} else if _, ok := candidateProxyConfigs[policyID]; !ok {
				candidateProxyConfigs[policyID] = proxyDefault
			}
		} else {
			delete(candidateProxyApps, policyID)
		}
	}
	proxyReconcileNeeded := proxyChangedRequested || len(previousProxyApps) > 0 || len(candidateProxyApps) > 0
	if proxyReconcileNeeded && s.appProxyController == nil {
		return errors.New("application proxy controller is unavailable")
	}
	hasHostTarget := false
	for _, target := range targets {
		if target.Kind == AppNetworkTargetCgroup {
			hasHostTarget = true
			break
		}
	}
	if hasHostTarget && desired.ProxyEnabled && (desired.UploadKbps > 0 || desired.DownloadKbps > 0) && (limitChanged || proxyChangedRequested) {
		return errors.New("Host/Mixed 应用不能同时启用代理和限速，请先恢复直连或取消限速")
	}
	plan := make([]appNetworkPolicyTarget, 0, len(targets))
	for _, target := range targets {
		driver, err := s.appNetworkController.driver(target)
		if err != nil {
			return err
		}
		capabilities := driver.Capabilities()
		if !s.appNetworkController.canControlInternet() {
			capabilities.InternetControl = false
			capabilities.ProxyControl = false
		}
		if target.Kind == AppNetworkTargetCgroup && !s.hostNetworkExperimentalEnabled() {
			capabilities.UploadLimit = false
			capabilities.DownloadLimit = false
			capabilities.InternetControl = false
			capabilities.ProxyControl = false
		}
		if target.ControlDiagnostic != "" {
			capabilities.UploadLimit = false
			capabilities.DownloadLimit = false
			capabilities.InternetControl = false
			capabilities.ProxyControl = false
		}
		if limitChanged && desired.UploadKbps > 0 && !capabilities.UploadLimit {
			return fmt.Errorf("application %s target %s does not support upload limiting", appID, target.ID)
		}
		if limitChanged && desired.DownloadKbps > 0 && !capabilities.DownloadLimit {
			return fmt.Errorf("application %s target %s does not support download limiting", appID, target.ID)
		}
		if internetChangedRequested && !desired.InternetAllowed && !capabilities.InternetControl {
			return fmt.Errorf("application %s target %s does not support internet control", appID, target.ID)
		}
		if proxyChangedRequested && desired.ProxyEnabled && !capabilities.ProxyControl {
			return fmt.Errorf("application %s target %s does not support proxy control", appID, target.ID)
		}
		plan = append(plan, appNetworkPolicyTarget{target: target, driver: driver, caps: capabilities})
	}

	limited := make([]appNetworkPolicyTarget, 0, len(plan))
	if limitChanged {
		for _, item := range plan {
			if hasHostTarget && item.target.Kind == AppNetworkTargetBridge {
				continue
			}
			clearingHostLimit := hasHostTarget && item.target.Kind == AppNetworkTargetCgroup && desired.UploadKbps == 0 && desired.DownloadKbps == 0
			if !item.caps.UploadLimit && !item.caps.DownloadLimit && !clearingHostLimit {
				continue
			}
			if err := item.driver.ApplyLimit(ctx, item.target, AppTrafficLimit{UploadKbps: desired.UploadKbps, DownloadKbps: desired.DownloadKbps}); err != nil {
				return errors.Join(err, rollbackAppNetworkLimits(ctx, limited, previousLimit))
			}
			limited = append(limited, item)
		}
	}

	items := CollectAppTraffic().Bridges
	controlChanged := internetChangedRequested || proxyChangedRequested
	rollbackControls := func() error {
		s.settings.setAppProxy(proxyDefault, previousProxyApps, previousProxyConfigs)
		s.containers.replaceBlockedApps(previousBlockedApps)
		var rollbackErrors []error
		if proxyReconcileNeeded {
			if err := s.reconcileAppProxyControls(ctx, items, previousProxyApps, previousProxyConfigs, previousBlockedApps); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("rollback application proxy policy: %w", err))
			}
		}
		if err := s.appNetworkController.reconcileInternetPolicy(ctx, items, previousBlockedApps); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("rollback application internet policy: %w", err))
		}
		return errors.Join(rollbackErrors...)
	}
	if proxyReconcileNeeded {
		if err := s.reconcileAppProxyControls(ctx, items, candidateProxyApps, candidateProxyConfigs, candidateBlockedApps); err != nil {
			return errors.Join(err, rollbackControls(), rollbackAppNetworkLimits(ctx, limited, previousLimit))
		}
	}
	if internetChangedRequested {
		if err := s.appNetworkController.reconcileInternetPolicy(ctx, items, candidateBlockedApps); err != nil {
			return errors.Join(err, rollbackControls(), rollbackAppNetworkLimits(ctx, limited, previousLimit))
		}
	}
	if controlChanged {
		s.settings.setAppProxy(proxyDefault, candidateProxyApps, candidateProxyConfigs)
		s.containers.replaceBlockedApps(candidateBlockedApps)
	}

	if limitChanged {
		if err := s.appTraffic.setLimit(policyID, AppTrafficLimit{UploadKbps: desired.UploadKbps, DownloadKbps: desired.DownloadKbps}); err != nil {
			var rollbackErrors []error
			if controlChanged {
				if rollbackErr := rollbackControls(); rollbackErr != nil {
					rollbackErrors = append(rollbackErrors, rollbackErr)
				}
			}
			if rollbackErr := rollbackAppNetworkLimits(ctx, limited, previousLimit); rollbackErr != nil {
				rollbackErrors = append(rollbackErrors, rollbackErr)
			}
			if rollbackErr := s.appTraffic.setLimit(policyID, previousLimit); rollbackErr != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("restore persisted application traffic limit: %w", rollbackErr))
			}
			return errors.Join(fmt.Errorf("persist application network policy: %w", err), errors.Join(rollbackErrors...))
		}
	}
	if controlChanged {
		if err := s.appNetworkController.persistInternet(); err != nil {
			var rollbackErrors []error
			if rollbackErr := rollbackControls(); rollbackErr != nil {
				rollbackErrors = append(rollbackErrors, rollbackErr)
			}
			if rollbackErr := rollbackAppNetworkLimits(ctx, limited, previousLimit); rollbackErr != nil {
				rollbackErrors = append(rollbackErrors, rollbackErr)
			}
			if limitChanged {
				if rollbackErr := s.appTraffic.setLimit(policyID, previousLimit); rollbackErr != nil {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("restore persisted application traffic limit: %w", rollbackErr))
				}
			}
			label := "application internet policy"
			if proxyChangedRequested {
				label = "application network policy"
			}
			return errors.Join(fmt.Errorf("persist %s: %w", label, err), errors.Join(rollbackErrors...))
		}
	}
	return nil
}

type appNetworkPolicyTarget struct {
	target AppNetworkTarget
	driver appNetworkTargetDriver
	caps   AppNetworkCapabilities
}

func rollbackAppNetworkLimits(ctx context.Context, limited []appNetworkPolicyTarget, previous AppTrafficLimit) error {
	var rollbackErrors []error
	for index := len(limited) - 1; index >= 0; index-- {
		item := limited[index]
		if err := item.driver.ApplyLimit(ctx, item.target, previous); err != nil {
			logger.Warn("rollback traffic policy on %s: %v", item.target.ID, err)
			rollbackErrors = append(rollbackErrors, fmt.Errorf("rollback traffic policy on %s: %w", item.target.ID, err))
		}
	}
	return errors.Join(rollbackErrors...)
}

func (s *Service) resolveLegacyAppNetworkTarget(ctx context.Context, targetID string) (AppNetworkTarget, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return AppNetworkTarget{}, errors.New("network target is required")
	}
	if appID, ok := hostAppIDFromTarget(targetID); ok {
		return AppNetworkTarget{ID: targetID, Kind: AppNetworkTargetCgroup, AppID: baseAppID(appID), InstanceID: appID, NetworkMode: "host"}, nil
	}
	if !strings.HasPrefix(targetID, lzcBridgePrefix) {
		return AppNetworkTarget{}, fmt.Errorf("unsupported network target %q", targetID)
	}
	metadata := cachedAppTrafficMetadata()
	info, ok := metadata.bridgeMap[targetID]
	if !ok {
		return AppNetworkTarget{}, fmt.Errorf("application bridge %s is not mapped", targetID)
	}
	appID := strings.TrimSpace(info.AppID)
	if appID == "" {
		appID = strings.TrimSpace(info.Project)
	}
	return AppNetworkTarget{
		ID: targetID, Kind: AppNetworkTargetBridge, AppID: appID, InstanceID: firstNonEmptyProbe(info.InstanceID, appID),
		Interface: targetID, NetworkMode: "bridge", AccountingSource: appTrafficSource,
	}, nil
}

func (s *Service) setLegacyTargetInternetAccess(ctx context.Context, targetID string, allowed bool) error {
	target, err := s.resolveLegacyAppNetworkTarget(ctx, targetID)
	if err != nil {
		return err
	}
	if isWhitelistedApp(target.AppID, "") {
		return fmt.Errorf("application %s does not allow internet control", target.AppID)
	}
	return s.updateAppInstanceNetworkPolicy(ctx, target.AppID, appNetworkTargetPolicyID(target), appNetworkPolicyUpdate{InternetAllowed: &allowed})
}

func (s *Service) SetAppInternetAccess(ctx context.Context, appID string, allowed bool) error {
	return s.updateAppNetworkPolicy(ctx, appID, appNetworkPolicyUpdate{InternetAllowed: &allowed})
}

func (s *Service) UpdateAppNetworkPolicy(ctx context.Context, appID string, uploadKbps, downloadKbps *int64, internetAllowed, proxyEnabled *bool, proxySettings *AppProxySettings) error {
	return s.updateAppNetworkPolicy(ctx, appID, appNetworkPolicyUpdate{
		UploadKbps: uploadKbps, DownloadKbps: downloadKbps, InternetAllowed: internetAllowed, ProxyEnabled: proxyEnabled, ProxySettings: proxySettings,
	})
}

func (s *Service) UpdateAppInstanceNetworkPolicy(ctx context.Context, appID, instanceID string, uploadKbps, downloadKbps *int64, internetAllowed, proxyEnabled *bool, proxySettings *AppProxySettings) error {
	return s.updateAppInstanceNetworkPolicy(ctx, appID, instanceID, appNetworkPolicyUpdate{
		UploadKbps: uploadKbps, DownloadKbps: downloadKbps, InternetAllowed: internetAllowed, ProxyEnabled: proxyEnabled, ProxySettings: proxySettings,
	})
}
