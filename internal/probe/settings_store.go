package probe

import (
	"strings"
	"sync"
)

// settingsStore holds mutable runtime knobs that are neither notification policy
// nor host-control state. Service reads/writes through this store.
type settingsStore struct {
	mu sync.RWMutex

	chartTimeLabelInterval         int
	appTrafficRealtimeEnabled      bool
	hostNetworkExperimentalEnabled bool
	dashboardCollapsedSections     []string
	backgroundMonitorEnabled       bool
	backgroundMonitorInterval      int
	appProxy                       AppProxySettings
	proxyApps                      map[string]bool
	appProxyConfigs                map[string]AppProxySettings
}

func newSettingsStore(def MutableSettings) *settingsStore {
	return &settingsStore{
		chartTimeLabelInterval:         def.ChartTimeLabelInterval,
		appTrafficRealtimeEnabled:      def.AppTrafficRealtimeEnabled,
		hostNetworkExperimentalEnabled: def.HostNetworkExperimentalEnabled,
		dashboardCollapsedSections:     normalizeDashboardCollapsedSections(def.DashboardCollapsedSections),
		backgroundMonitorEnabled:       def.BackgroundMonitorEnabled,
		backgroundMonitorInterval:      def.BackgroundMonitorIntervalSec,
		appProxy:                       def.AppProxy,
		proxyApps:                      cloneProxyApps(def.ProxyApps),
		appProxyConfigs:                normalizeAppProxyConfigs(def.AppProxyConfigs, def.ProxyApps, def.AppProxy),
	}
}

func (st *settingsStore) writeToSettings(out *MutableSettings) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	out.ChartTimeLabelInterval = st.chartTimeLabelInterval
	out.AppTrafficRealtimeEnabled = st.appTrafficRealtimeEnabled
	out.HostNetworkExperimentalEnabled = st.hostNetworkExperimentalEnabled
	out.DashboardCollapsedSections = append([]string(nil), st.dashboardCollapsedSections...)
	out.BackgroundMonitorEnabled = st.backgroundMonitorEnabled
	out.BackgroundMonitorIntervalSec = st.backgroundMonitorInterval
	out.AppProxy = st.appProxy
	out.ProxyApps = cloneProxyApps(st.proxyApps)
	out.AppProxyConfigs = cloneAppProxyConfigs(st.appProxyConfigs)
}

func (st *settingsStore) apply(in MutableSettings) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.chartTimeLabelInterval = in.ChartTimeLabelInterval
	st.appTrafficRealtimeEnabled = in.AppTrafficRealtimeEnabled
	st.hostNetworkExperimentalEnabled = in.HostNetworkExperimentalEnabled
	st.dashboardCollapsedSections = normalizeDashboardCollapsedSections(in.DashboardCollapsedSections)
	if in.BackgroundMonitorIntervalSec >= 10 {
		st.backgroundMonitorInterval = in.BackgroundMonitorIntervalSec
	}
	st.backgroundMonitorEnabled = in.BackgroundMonitorEnabled
	st.appProxy = normalizeAppProxySettings(in.AppProxy, st.appProxy)
	if in.ProxyApps != nil {
		st.proxyApps = cloneProxyApps(in.ProxyApps)
	}
	if in.AppProxyConfigs != nil {
		st.appProxyConfigs = cloneAppProxyConfigs(in.AppProxyConfigs)
	}
}

func (st *settingsStore) hostNetworkExperimental() bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.hostNetworkExperimentalEnabled
}

func (st *settingsStore) appProxyState() (AppProxySettings, map[string]bool, map[string]AppProxySettings) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.appProxy, cloneProxyApps(st.proxyApps), cloneAppProxyConfigs(st.appProxyConfigs)
}

func (st *settingsStore) setAppProxy(config AppProxySettings, apps map[string]bool, configs map[string]AppProxySettings) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.appProxy = config
	if apps != nil {
		st.proxyApps = cloneProxyApps(apps)
	}
	if configs != nil {
		st.appProxyConfigs = cloneAppProxyConfigs(configs)
	}
}

func (st *settingsStore) appProxyEnabled(appID string) bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.proxyApps[appID]
}

func (st *settingsStore) appProxyConfig(appID string) AppProxySettings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	if config, ok := st.appProxyConfigs[appID]; ok {
		return config
	}
	return st.appProxy
}

func cloneProxyApps(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for appID, enabled := range in {
		if enabled {
			out[appID] = true
		}
	}
	return out
}

func cloneAppProxyConfigs(in map[string]AppProxySettings) map[string]AppProxySettings {
	out := make(map[string]AppProxySettings, len(in))
	for appID, config := range in {
		if appID = strings.TrimSpace(appID); appID != "" {
			out[appID] = config
		}
	}
	return out
}

func normalizeDashboardCollapsedSections(sections []string) []string {
	allowed := map[string]bool{"app_traffic": true, "host_ports": true}
	out := make([]string, 0, len(allowed))
	seen := make(map[string]bool, len(allowed))
	for _, section := range sections {
		if allowed[section] && !seen[section] {
			seen[section] = true
			out = append(out, section)
		}
	}
	return out
}

func (st *settingsStore) backgroundConfig() (enabled bool, intervalSec int) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.backgroundMonitorEnabled, st.backgroundMonitorInterval
}
