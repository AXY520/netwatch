package probe

import (
	"fmt"
	"sort"
	"strings"

	"netwatch/internal/appidentity"
	"netwatch/internal/logger"
)

func appTrafficPolicyID(item AppBridgeStats) string {
	if instanceID := strings.TrimSpace(item.InstanceID); instanceID != "" {
		return instanceID
	}
	return strings.TrimSpace(item.AppID)
}

func appNetworkTargetPolicyID(target AppNetworkTarget) string {
	if instanceID := strings.TrimSpace(target.InstanceID); instanceID != "" {
		return instanceID
	}
	return strings.TrimSpace(target.AppID)
}

func appUsagePolicyID(app AppTrafficUsage) string {
	if instanceID := strings.TrimSpace(app.InstanceID); instanceID != "" {
		return instanceID
	}
	return strings.TrimSpace(app.AppID)
}

// resolveAppInstancePolicyID keeps the old app_id-only API safe: it remains
// valid for one current instance, but refuses to choose when several user
// instances of the same application are running.
func resolveAppInstancePolicyID(items []AppBridgeStats, appID, requestedInstanceID string) (string, error) {
	appID = strings.TrimSpace(appID)
	requestedInstanceID = strings.TrimSpace(requestedInstanceID)
	if requestedInstanceID != "" {
		for _, item := range items {
			if appTrafficPolicyID(item) != requestedInstanceID {
				continue
			}
			if appID != "" && strings.TrimSpace(item.AppID) != appID {
				return "", fmt.Errorf("instance %s does not belong to application %s", requestedInstanceID, appID)
			}
			return requestedInstanceID, nil
		}
		return "", fmt.Errorf("application instance %s was not found", requestedInstanceID)
	}
	if appID == "" {
		return "", fmt.Errorf("application id is required")
	}
	instances := make(map[string]bool)
	for _, item := range items {
		if strings.TrimSpace(item.AppID) == appID {
			if policyID := appTrafficPolicyID(item); policyID != "" {
				instances[policyID] = true
			}
		}
	}
	if len(instances) == 0 {
		return "", fmt.Errorf("application %s has no controllable network target", appID)
	}
	if len(instances) > 1 {
		return "", fmt.Errorf("应用 %s 当前有多个实例，请指定 instance_id", appID)
	}
	for instanceID := range instances {
		return instanceID, nil
	}
	return "", fmt.Errorf("application %s has no controllable network target", appID)
}

func activeAppInstances(items []AppBridgeStats) map[string][]string {
	instances := make(map[string]map[string]bool)
	unsafeApps := make(map[string]bool)
	for _, item := range items {
		appID := strings.TrimSpace(item.AppID)
		policyID := appTrafficPolicyID(item)
		if appID == "" || policyID == "" || policyID == appID {
			continue
		}
		if instances[appID] == nil {
			instances[appID] = make(map[string]bool)
		}
		instances[appID][policyID] = true
		if item.Target.ControlDiagnostic != "" {
			unsafeApps[appID] = true
		}
	}
	out := make(map[string][]string, len(instances))
	for appID, set := range instances {
		if unsafeApps[appID] {
			continue
		}
		for instanceID := range set {
			out[appID] = append(out[appID], instanceID)
		}
		sort.Strings(out[appID])
	}
	return out
}

func unsupportedAppNetworkPolicies(targets []AppNetworkTarget) map[string]bool {
	unsupported := make(map[string]bool)
	for _, target := range targets {
		if target.ControlDiagnostic != "" {
			unsupported[appNetworkTargetPolicyID(target)] = true
		}
	}
	return unsupported
}

func baseAppID(instanceID string) string {
	return appidentity.Base(instanceID)
}

func firstNonEmptyProbe(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// migrateLegacyAppInstancePolicies upgrades the former application-wide keys
// only after all current instance identities are known. Each current instance
// receives the old desired state, then the ambiguous base key is removed so a
// future user instance cannot unexpectedly inherit it.
func (s *Service) migrateLegacyAppInstancePolicies(items []AppBridgeStats) error {
	instances := activeAppInstances(items)
	if len(instances) == 0 || s == nil || s.appTraffic == nil || s.containers == nil || s.settings == nil {
		return nil
	}
	if s.appNetworkController != nil {
		s.appNetworkController.mu.Lock()
		defer s.appNetworkController.mu.Unlock()
	}
	limitChanged, err := s.appTraffic.migrateLegacyLimits(instances)
	if err != nil {
		return fmt.Errorf("migrate instance traffic limits: %w", err)
	}

	blocked := s.containers.snapshotBlockedApps()
	proxyDefault, proxyApps, proxyConfigs := s.settings.appProxyState()
	controlChanged := migrateLegacyAppInstanceControlMaps(instances, blocked, proxyApps, proxyConfigs)
	if controlChanged {
		candidate := s.GetMutableSettings()
		candidate.BlockedApps = blocked
		candidate.ProxyApps = proxyApps
		candidate.AppProxyConfigs = proxyConfigs
		if err := saveMutableSettings(s.cfg.DataDir, candidate); err != nil {
			return fmt.Errorf("migrate instance network policies: %w", err)
		}
		s.containers.replaceBlockedApps(blocked)
		s.settings.setAppProxy(proxyDefault, proxyApps, proxyConfigs)
	}
	if limitChanged || controlChanged {
		logger.Info("migrated application-wide network policy to %d multi-instance applications", len(instances))
	}
	return nil
}

func migrateLegacyAppInstanceControlMaps(instances map[string][]string, blocked map[string]string, proxyApps map[string]bool, proxyConfigs map[string]AppProxySettings) bool {
	changed := false
	for appID, instanceIDs := range instances {
		if mode := blocked[appID]; mode != "" {
			for _, instanceID := range instanceIDs {
				if blocked[instanceID] == "" {
					blocked[instanceID] = mode
				}
			}
			delete(blocked, appID)
			changed = true
		}
		if proxyApps[appID] {
			for _, instanceID := range instanceIDs {
				if !proxyApps[instanceID] {
					proxyApps[instanceID] = true
				}
			}
			delete(proxyApps, appID)
			changed = true
		}
		if config, ok := proxyConfigs[appID]; ok {
			for _, instanceID := range instanceIDs {
				if _, exists := proxyConfigs[instanceID]; !exists {
					proxyConfigs[instanceID] = config
				}
			}
			delete(proxyConfigs, appID)
			changed = true
		}
	}
	return changed
}
