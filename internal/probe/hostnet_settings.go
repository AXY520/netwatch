package probe

import "strings"

// pruneHostNetworkExperimentalState removes stale host targets from the
// in-memory block registry after the experimental switch is disabled. The
// caller persists the returned map as part of the normal settings update.
func pruneHostNetworkExperimentalState(blocked map[string]string) map[string]string {
	cleaned := make(map[string]string, len(blocked))
	for target, mode := range blocked {
		if strings.HasPrefix(target, hostAppTargetPrefix) {
			continue
		}
		cleaned[target] = mode
	}
	return cleaned
}

func (s *Service) clearHostNetworkExperimentalState(persist bool) {
	if s == nil || s.containers == nil {
		return
	}
	items := CollectAppTraffic().Bridges
	hostApps := make(map[string]bool)
	for _, item := range items {
		if item.AppID != "" && (item.NetworkMode == "host" || strings.HasPrefix(item.Bridge, hostAppTargetPrefix)) {
			hostApps[item.AppID] = true
		}
	}
	blockedApps := s.containers.snapshotBlockedApps()
	proxyConfig, proxyApps, proxyConfigs := s.settings.appProxyState()
	for appID := range hostApps {
		if blockedApps[appID] != "" {
			delete(blockedApps, appID)
		}
		delete(proxyApps, appID)
	}
	legacy := pruneHostNetworkExperimentalState(s.containers.snapshotBlocked())
	s.containers.replaceBlockedApps(blockedApps)
	s.containers.replaceBlocked(legacy)
	s.settings.setAppProxy(proxyConfig, proxyApps, proxyConfigs)
	_ = s.reconcileAppProxyControls(s.LifecycleContext(), items, proxyApps, proxyConfigs, blockedApps)
	_ = s.reconcileAppInternetControls(s.LifecycleContext(), items, blockedApps)
	s.reconcileAppTrafficLimits(items)
	if persist {
		s.saveBlockedBridges()
	}
}
