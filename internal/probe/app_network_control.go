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
	ID               string               `json:"id"`
	Kind             AppNetworkTargetKind `json:"kind"`
	AppID            string               `json:"app_id"`
	Interface        string               `json:"interface,omitempty"`
	CgroupPath       string               `json:"cgroup_path,omitempty"`
	NetworkMode      string               `json:"network_mode"`
	AccountingSource string               `json:"accounting_source,omitempty"`
	Diagnostic       string               `json:"diagnostic,omitempty"`
}

type AppNetworkCapabilities struct {
	Accounting      bool `json:"accounting"`
	UploadLimit     bool `json:"upload_limit"`
	DownloadLimit   bool `json:"download_limit"`
	InternetControl bool `json:"internet_control"`
}

type AppNetworkTargetStatus struct {
	Target       AppNetworkTarget       `json:"target"`
	Capabilities AppNetworkCapabilities `json:"capabilities"`
	Blocked      bool                   `json:"blocked"`
	LimitInSync  bool                   `json:"limit_in_sync"`
	Diagnostic   string                 `json:"diagnostic,omitempty"`
}

type AppNetworkPolicy struct {
	UploadKbps      int64 `json:"upload_kbps"`
	DownloadKbps    int64 `json:"download_kbps"`
	InternetAllowed bool  `json:"internet_allowed"`
}

type appNetworkPolicyUpdate struct {
	UploadKbps      *int64
	DownloadKbps    *int64
	InternetAllowed *bool
}

type AppNetworkPolicyStatus struct {
	Desired        AppNetworkPolicy         `json:"desired"`
	Capabilities   AppNetworkCapabilities   `json:"capabilities"`
	LimitState     string                   `json:"limit_state"`
	LimitInSync    bool                     `json:"limit_in_sync"`
	InternetState  string                   `json:"internet_state"`
	InternetInSync bool                     `json:"internet_in_sync"`
	Diagnostic     string                   `json:"diagnostic,omitempty"`
	Targets        []AppNetworkTargetStatus `json:"targets"`
}

type appNetworkTargetDriver interface {
	Capabilities() AppNetworkCapabilities
	ApplyLimit(context.Context, AppNetworkTarget, AppTrafficLimit) error
}

type appNetworkController struct {
	mu      sync.Mutex
	drivers map[AppNetworkTargetKind]appNetworkTargetDriver
}

type bridgeNetworkDriver struct {
	service *Service
	limiter *appTrafficLimiter
}

type cgroupNetworkDriver struct {
	service *Service
}

func newAppNetworkController(service *Service, limiter *appTrafficLimiter) *appNetworkController {
	return &appNetworkController{drivers: map[AppNetworkTargetKind]appNetworkTargetDriver{
		AppNetworkTargetBridge: bridgeNetworkDriver{service: service, limiter: limiter},
		AppNetworkTargetCgroup: cgroupNetworkDriver{service: service},
	}}
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
	return AppNetworkCapabilities{Accounting: true, UploadLimit: true, DownloadLimit: true, InternetControl: true}
}

func (d bridgeNetworkDriver) ApplyLimit(ctx context.Context, target AppNetworkTarget, limit AppTrafficLimit) error {
	if d.limiter == nil {
		return errors.New("application traffic limiter is unavailable")
	}
	return d.limiter.apply(ctx, target.Interface, limit)
}

func (d cgroupNetworkDriver) Capabilities() AppNetworkCapabilities {
	return AppNetworkCapabilities{Accounting: true, InternetControl: true}
}

func (d cgroupNetworkDriver) ApplyLimit(_ context.Context, target AppNetworkTarget, _ AppTrafficLimit) error {
	return fmt.Errorf("target %s does not support traffic limiting", target.ID)
}

func appNetworkTargetFromStats(item AppBridgeStats) (AppNetworkTarget, bool) {
	if item.Target.ID != "" && item.Target.Kind != "" {
		return item.Target, true
	}
	appID := strings.TrimSpace(item.AppID)
	if appID == "" {
		return AppNetworkTarget{}, false
	}
	if item.NetworkMode == "host" || strings.HasPrefix(item.Bridge, hostAppTargetPrefix) {
		id := strings.TrimSpace(item.ControlTarget)
		if id == "" {
			id = hostAppTarget(appID)
		}
		return AppNetworkTarget{
			ID: id, Kind: AppNetworkTargetCgroup, AppID: appID,
			CgroupPath: item.CgroupPath, NetworkMode: "host", AccountingSource: item.Source, Diagnostic: item.Diagnostic,
		}, true
	}
	if strings.HasPrefix(item.Bridge, lzcBridgePrefix) {
		return AppNetworkTarget{
			ID: item.Bridge, Kind: AppNetworkTargetBridge, AppID: appID,
			Interface: item.Bridge, NetworkMode: "bridge", AccountingSource: item.Source,
		}, true
	}
	return AppNetworkTarget{}, false
}

func appNetworkTargetsForApp(items []AppBridgeStats, appID string) []AppNetworkTarget {
	byID := make(map[string]AppNetworkTarget)
	for _, item := range items {
		if strings.TrimSpace(item.AppID) != strings.TrimSpace(appID) {
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
			return targets
		}
	}
	return append(targets, candidate)
}

func (s *Service) appNetworkPolicyStatus(app AppTrafficUsage, blocked map[string]string) AppNetworkPolicyStatus {
	status := AppNetworkPolicyStatus{
		Desired: AppNetworkPolicy{
			UploadKbps: app.Limit.UploadKbps, DownloadKbps: app.Limit.DownloadKbps, InternetAllowed: true,
		},
		LimitState: "unlimited", LimitInSync: true, InternetState: "allowed", InternetInSync: true,
	}
	if len(app.NetworkTargets) == 0 {
		// Persisted records from before the target model are upgraded from the
		// legacy bridge/host fields at read time.
		for _, bridge := range app.Bridges {
			if target, ok := appNetworkTargetFromStats(AppBridgeStats{AppID: app.AppID, Bridge: bridge, NetworkMode: app.NetworkTopology}); ok {
				app.NetworkTargets = appendUniqueAppNetworkTarget(app.NetworkTargets, target)
			}
		}
	}
	if app.Limit.UploadKbps > 0 || app.Limit.DownloadKbps > 0 {
		status.LimitState = "limited"
	}
	blockedCount := 0
	appBlocked := s.containers != nil && s.containers.appBlocked(app.AppID) != ""
	desiredBlocked := appBlocked
	inSync := true
	for _, target := range app.NetworkTargets {
		targetStatus := AppNetworkTargetStatus{Target: target, LimitInSync: true, Diagnostic: target.Diagnostic}
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
			if !appFirewallAvailable() {
				targetStatus.Capabilities.InternetControl = false
				if targetStatus.Diagnostic == "" {
					targetStatus.Diagnostic = "宿主机防火墙控制不可用"
				}
			}
			if target.Kind == AppNetworkTargetBridge && !trafficControlAvailable() {
				targetStatus.Capabilities.UploadLimit = false
				targetStatus.Capabilities.DownloadLimit = false
				if targetStatus.Diagnostic == "" {
					targetStatus.Diagnostic = "宿主机 tc 不可用"
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
		}
		if isWhitelistedApp(app.AppID, app.AppTitle) {
			targetStatus.Capabilities.UploadLimit = false
			targetStatus.Capabilities.DownloadLimit = false
			targetStatus.Capabilities.InternetControl = false
		}
		if target.Kind == AppNetworkTargetBridge && s.appTrafficLimiter != nil {
			runtime := s.appTrafficLimiter.runtimeStatus(target.Interface, AppTrafficLimit{UploadKbps: app.Limit.UploadKbps, DownloadKbps: app.Limit.DownloadKbps})
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
	result := AppNetworkCapabilities{Accounting: true, UploadLimit: true, DownloadLimit: true, InternetControl: true}
	for _, target := range targets {
		result.Accounting = result.Accounting && target.Capabilities.Accounting
		result.UploadLimit = result.UploadLimit && target.Capabilities.UploadLimit
		result.DownloadLimit = result.DownloadLimit && target.Capabilities.DownloadLimit
		result.InternetControl = result.InternetControl && target.Capabilities.InternetControl
	}
	return result
}

func (s *Service) currentAppNetworkPolicy(appID string, targets []AppNetworkTarget) AppNetworkPolicy {
	limit := s.appTraffic.limitForApp(appID)
	blocked := s.containers.snapshotBlocked()
	policy := AppNetworkPolicy{
		UploadKbps: limit.UploadKbps, DownloadKbps: limit.DownloadKbps, InternetAllowed: true,
	}
	if s.containers.appBlocked(appID) != "" {
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
		UploadKbps: &desired.UploadKbps, DownloadKbps: &desired.DownloadKbps, InternetAllowed: &desired.InternetAllowed,
	})
}

func (s *Service) updateAppNetworkPolicy(ctx context.Context, appID string, update appNetworkPolicyUpdate) error {
	appID = strings.TrimSpace(appID)
	if appID == "" || isNetwatchTrafficItem(AppBridgeStats{AppID: appID}) {
		return errors.New("invalid application id")
	}
	if update.UploadKbps == nil && update.DownloadKbps == nil && update.InternetAllowed == nil {
		return errors.New("application network policy update is empty")
	}
	if s.appNetworkController == nil || s.appTrafficLimiter == nil || s.appTraffic == nil || s.containers == nil {
		return errors.New("application network controller is unavailable")
	}
	targets := appNetworkTargetsForApp(CollectAppTraffic().Bridges, appID)
	if len(targets) == 0 {
		return fmt.Errorf("application %s has no controllable network target", appID)
	}
	return s.applyAppNetworkPolicyUpdate(ctx, appID, targets, update)
}

func (s *Service) applyAppNetworkPolicyUpdate(ctx context.Context, appID string, targets []AppNetworkTarget, update appNetworkPolicyUpdate) error {
	desired := s.currentAppNetworkPolicy(appID, targets)
	if update.UploadKbps != nil {
		desired.UploadKbps = *update.UploadKbps
	}
	if update.DownloadKbps != nil {
		desired.DownloadKbps = *update.DownloadKbps
	}
	if update.InternetAllowed != nil {
		desired.InternetAllowed = *update.InternetAllowed
	}
	limitChanged := update.UploadKbps != nil || update.DownloadKbps != nil
	internetChangedRequested := update.InternetAllowed != nil
	if desired.UploadKbps < 0 || desired.DownloadKbps < 0 || desired.UploadKbps > maxAppTrafficLimitKbps || desired.DownloadKbps > maxAppTrafficLimitKbps {
		return fmt.Errorf("traffic limit must be between 0 and %d Kbit/s", maxAppTrafficLimitKbps)
	}
	if isWhitelistedApp(appID, "") {
		return fmt.Errorf("application %s does not allow network policy changes", appID)
	}
	s.appNetworkController.mu.Lock()
	defer s.appNetworkController.mu.Unlock()
	s.appTrafficLimiter.operationMu.Lock()
	defer s.appTrafficLimiter.operationMu.Unlock()

	previousLimit := s.appTraffic.limitForApp(appID)
	previousBlockedApps := s.containers.snapshotBlockedApps()
	type policyTarget struct {
		target AppNetworkTarget
		driver appNetworkTargetDriver
		caps   AppNetworkCapabilities
	}
	plan := make([]policyTarget, 0, len(targets))
	for _, target := range targets {
		driver, err := s.appNetworkController.driver(target)
		if err != nil {
			return err
		}
		capabilities := driver.Capabilities()
		if !appFirewallAvailable() {
			capabilities.InternetControl = false
		}
		if target.Kind == AppNetworkTargetCgroup && !s.hostNetworkExperimentalEnabled() {
			capabilities.InternetControl = false
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
		plan = append(plan, policyTarget{target: target, driver: driver, caps: capabilities})
	}

	limited := make([]policyTarget, 0, len(plan))
	if limitChanged {
		for _, item := range plan {
			if !item.caps.UploadLimit && !item.caps.DownloadLimit {
				continue
			}
			if err := item.driver.ApplyLimit(ctx, item.target, AppTrafficLimit{UploadKbps: desired.UploadKbps, DownloadKbps: desired.DownloadKbps}); err != nil {
				for index := len(limited) - 1; index >= 0; index-- {
					rollback := limited[index]
					if rollbackErr := rollback.driver.ApplyLimit(ctx, rollback.target, previousLimit); rollbackErr != nil {
						logger.Warn("rollback traffic policy on %s: %v", rollback.target.ID, rollbackErr)
					}
				}
				return err
			}
			limited = append(limited, item)
		}
	}

	if internetChangedRequested {
		candidateApps := make(map[string]string, len(previousBlockedApps)+1)
		for applicationID, mode := range previousBlockedApps {
			candidateApps[applicationID] = mode
		}
		if desired.InternetAllowed {
			delete(candidateApps, appID)
		} else {
			candidateApps[appID] = "internet"
		}
		if err := s.reconcileAppInternetControls(ctx, CollectAppTraffic().Bridges, candidateApps); err != nil {
			for index := len(limited) - 1; index >= 0; index-- {
				rollback := limited[index]
				if rollbackErr := rollback.driver.ApplyLimit(ctx, rollback.target, previousLimit); rollbackErr != nil {
					logger.Warn("rollback traffic policy on %s: %v", rollback.target.ID, rollbackErr)
				}
			}
			return err
		}
		s.containers.replaceBlockedApps(candidateApps)
	}

	if limitChanged {
		if err := s.appTraffic.setLimit(appID, AppTrafficLimit{UploadKbps: desired.UploadKbps, DownloadKbps: desired.DownloadKbps}); err != nil {
			if internetChangedRequested {
				s.containers.replaceBlockedApps(previousBlockedApps)
				_ = s.reconcileAppInternetControls(ctx, CollectAppTraffic().Bridges, previousBlockedApps)
			}
			for index := len(limited) - 1; index >= 0; index-- {
				rollback := limited[index]
				if rollbackErr := rollback.driver.ApplyLimit(ctx, rollback.target, previousLimit); rollbackErr != nil {
					logger.Warn("rollback traffic policy after persist failure on %s: %v", rollback.target.ID, rollbackErr)
				}
			}
			_ = s.appTraffic.setLimit(appID, previousLimit)
			return fmt.Errorf("persist application network policy: %w", err)
		}
	}
	if internetChangedRequested {
		if err := s.persistBlockedState(); err != nil {
			s.containers.replaceBlockedApps(previousBlockedApps)
			if rollbackErr := s.reconcileAppInternetControls(ctx, CollectAppTraffic().Bridges, previousBlockedApps); rollbackErr != nil {
				logger.Warn("rollback internet policy after persist failure: %v", rollbackErr)
			}
			return fmt.Errorf("persist application internet policy: %w", err)
		}
	}
	return nil
}

func (s *Service) resolveLegacyAppNetworkTarget(ctx context.Context, targetID string) (AppNetworkTarget, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return AppNetworkTarget{}, errors.New("network target is required")
	}
	if appID, ok := hostAppIDFromTarget(targetID); ok {
		return AppNetworkTarget{ID: targetID, Kind: AppNetworkTargetCgroup, AppID: appID, NetworkMode: "host"}, nil
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
		ID: targetID, Kind: AppNetworkTargetBridge, AppID: appID,
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
	return s.SetAppInternetAccess(ctx, target.AppID, allowed)
}

func (s *Service) SetAppInternetAccess(ctx context.Context, appID string, allowed bool) error {
	return s.updateAppNetworkPolicy(ctx, appID, appNetworkPolicyUpdate{InternetAllowed: &allowed})
}

func (s *Service) UpdateAppNetworkPolicy(ctx context.Context, appID string, uploadKbps, downloadKbps *int64, internetAllowed *bool) error {
	return s.updateAppNetworkPolicy(ctx, appID, appNetworkPolicyUpdate{
		UploadKbps: uploadKbps, DownloadKbps: downloadKbps, InternetAllowed: internetAllowed,
	})
}
