package probe

import "sync"

// settingsStore holds mutable runtime knobs that are neither notification policy
// nor host-control state. Service reads/writes through this store.
type settingsStore struct {
	mu sync.RWMutex

	chartTimeLabelInterval     int
	dashboardCollapsedSections []string
	containerControlEnabled    bool
	backgroundMonitorEnabled   bool
	backgroundMonitorInterval  int
}

func newSettingsStore(def MutableSettings) *settingsStore {
	return &settingsStore{
		chartTimeLabelInterval:     def.ChartTimeLabelInterval,
		dashboardCollapsedSections: normalizeDashboardCollapsedSections(def.DashboardCollapsedSections),
		containerControlEnabled:    def.ContainerControlEnabled,
		backgroundMonitorEnabled:   def.BackgroundMonitorEnabled,
		backgroundMonitorInterval:  def.BackgroundMonitorIntervalSec,
	}
}

func (st *settingsStore) writeToSettings(out *MutableSettings) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	out.ChartTimeLabelInterval = st.chartTimeLabelInterval
	out.DashboardCollapsedSections = append([]string(nil), st.dashboardCollapsedSections...)
	out.ContainerControlEnabled = st.containerControlEnabled
	out.BackgroundMonitorEnabled = st.backgroundMonitorEnabled
	out.BackgroundMonitorIntervalSec = st.backgroundMonitorInterval
}

func (st *settingsStore) apply(in MutableSettings) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.chartTimeLabelInterval = in.ChartTimeLabelInterval
	st.dashboardCollapsedSections = normalizeDashboardCollapsedSections(in.DashboardCollapsedSections)
	st.containerControlEnabled = in.ContainerControlEnabled
	if in.BackgroundMonitorIntervalSec >= 10 {
		st.backgroundMonitorInterval = in.BackgroundMonitorIntervalSec
	}
	st.backgroundMonitorEnabled = in.BackgroundMonitorEnabled
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

func (st *settingsStore) containerControl() bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.containerControlEnabled
}
