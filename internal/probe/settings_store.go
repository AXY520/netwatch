package probe

import "sync"

// settingsStore holds mutable runtime knobs that are neither notification policy
// nor host-control state. Service reads/writes through this store.
type settingsStore struct {
	mu sync.RWMutex

	chartTimeLabelInterval    int
	containerControlEnabled   bool
	trafficSamplingEnabled    bool
	trafficSamplingInterval   int
	perAppSamplingInterval    map[string]int
	persistentTrafficBridges  []string
	backgroundMonitorEnabled  bool
	backgroundMonitorInterval int
}

func newSettingsStore(def MutableSettings) *settingsStore {
	return &settingsStore{
		chartTimeLabelInterval:    def.ChartTimeLabelInterval,
		containerControlEnabled:   def.ContainerControlEnabled,
		trafficSamplingEnabled:    def.TrafficSamplingEnabled,
		trafficSamplingInterval:   def.TrafficSamplingIntervalSec,
		perAppSamplingInterval:    map[string]int{},
		backgroundMonitorEnabled:  def.BackgroundMonitorEnabled,
		backgroundMonitorInterval: def.BackgroundMonitorIntervalSec,
	}
}

func (st *settingsStore) snapshotTraffic() (enabled bool, interval int, perApp map[string]int, persistent []string) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	perApp = make(map[string]int, len(st.perAppSamplingInterval))
	for k, v := range st.perAppSamplingInterval {
		perApp[k] = v
	}
	return st.trafficSamplingEnabled, st.trafficSamplingInterval, perApp, append([]string(nil), st.persistentTrafficBridges...)
}

func (st *settingsStore) writeToSettings(out *MutableSettings) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	out.ChartTimeLabelInterval = st.chartTimeLabelInterval
	out.ContainerControlEnabled = st.containerControlEnabled
	out.TrafficSamplingEnabled = st.trafficSamplingEnabled
	out.TrafficSamplingIntervalSec = st.trafficSamplingInterval
	out.BackgroundMonitorEnabled = st.backgroundMonitorEnabled
	out.BackgroundMonitorIntervalSec = st.backgroundMonitorInterval
	if len(st.perAppSamplingInterval) > 0 {
		out.PerAppSamplingInterval = make(map[string]int, len(st.perAppSamplingInterval))
		for k, v := range st.perAppSamplingInterval {
			out.PerAppSamplingInterval[k] = v
		}
	}
	out.PersistentTrafficBridges = append([]string(nil), st.persistentTrafficBridges...)
}

func (st *settingsStore) apply(in MutableSettings) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.chartTimeLabelInterval = in.ChartTimeLabelInterval
	st.containerControlEnabled = in.ContainerControlEnabled
	st.trafficSamplingEnabled = in.TrafficSamplingEnabled
	if in.TrafficSamplingIntervalSec >= 5 {
		st.trafficSamplingInterval = in.TrafficSamplingIntervalSec
	}
	if in.BackgroundMonitorIntervalSec >= 10 {
		st.backgroundMonitorInterval = in.BackgroundMonitorIntervalSec
	}
	st.backgroundMonitorEnabled = in.BackgroundMonitorEnabled
	if in.PerAppSamplingInterval != nil {
		st.perAppSamplingInterval = make(map[string]int, len(in.PerAppSamplingInterval))
		for k, v := range in.PerAppSamplingInterval {
			st.perAppSamplingInterval[k] = v
		}
	}
	st.persistentTrafficBridges = append([]string(nil), in.PersistentTrafficBridges...)
}

func (st *settingsStore) backgroundConfig() (enabled bool, intervalSec int) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.backgroundMonitorEnabled, st.backgroundMonitorInterval
}

func (st *settingsStore) setPersistentBridges(bridges []string) {
	st.mu.Lock()
	st.persistentTrafficBridges = append([]string(nil), bridges...)
	st.mu.Unlock()
}

func (st *settingsStore) containerControl() bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.containerControlEnabled
}
