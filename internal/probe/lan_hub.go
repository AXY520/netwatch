package probe

import (
	"sync"
	"time"
)

// lanPolicy is the tunable LAN discovery/notification policy.
type lanPolicy struct {
	OfflineAfterSec       int
	OnlineAfterSec        int
	OfflineNotifyDelaySec int
	OnlineNotifyDelaySec  int
	MaxCheckAttempts      int
	NotifyCooldownSec     int
	FlappingThreshold     int
	FlappingWindow        time.Duration
	AutoRemoveDays        int
}

// lanHub owns LAN device cache, interface state and anti-flap bookkeeping.
type lanHub struct {
	mu              sync.Mutex
	dataDir         string
	deviceMap       map[string]LANDevice
	snapshot        LANDeviceSnapshot
	interfaceState  map[string]bool
	notifyCooldown  map[string]time.Time
	flappingHistory map[string][]time.Time
	policy          lanPolicy
}

func newLANHub(dataDir string, def MutableSettings) *lanHub {
	return &lanHub{
		dataDir:         dataDir,
		notifyCooldown:  make(map[string]time.Time),
		flappingHistory: make(map[string][]time.Time),
		policy: lanPolicy{
			OfflineAfterSec:       def.LANDeviceOfflineAfterSec,
			OnlineAfterSec:        def.LANDeviceOnlineAfterSec,
			OfflineNotifyDelaySec: def.LANDeviceOfflineNotifyDelaySec,
			OnlineNotifyDelaySec:  def.LANDeviceOnlineNotifyDelaySec,
			MaxCheckAttempts:      def.LANMaxCheckAttempts,
			NotifyCooldownSec:     def.LANNotifyCooldownSec,
			FlappingThreshold:     def.LANFlappingThreshold,
			FlappingWindow:        time.Duration(def.LANFlappingWindowSec) * time.Second,
			AutoRemoveDays:        def.LANDeviceAutoRemoveDays,
		},
	}
}

func (h *lanHub) applyPolicy(in MutableSettings) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if in.LANDeviceOfflineAfterSec >= 10 {
		h.policy.OfflineAfterSec = in.LANDeviceOfflineAfterSec
	}
	if in.LANDeviceOnlineAfterSec >= 0 {
		h.policy.OnlineAfterSec = in.LANDeviceOnlineAfterSec
	}
	if in.LANDeviceOfflineNotifyDelaySec >= 0 {
		h.policy.OfflineNotifyDelaySec = in.LANDeviceOfflineNotifyDelaySec
	}
	if in.LANDeviceOnlineNotifyDelaySec >= 0 {
		h.policy.OnlineNotifyDelaySec = in.LANDeviceOnlineNotifyDelaySec
	}
	if in.LANMaxCheckAttempts >= 1 {
		h.policy.MaxCheckAttempts = in.LANMaxCheckAttempts
	}
	if in.LANNotifyCooldownSec >= 60 {
		h.policy.NotifyCooldownSec = in.LANNotifyCooldownSec
	}
	if in.LANFlappingThreshold >= 3 {
		h.policy.FlappingThreshold = in.LANFlappingThreshold
	}
	if in.LANFlappingWindowSec >= 60 {
		h.policy.FlappingWindow = time.Duration(in.LANFlappingWindowSec) * time.Second
	}
	h.policy.AutoRemoveDays = in.LANDeviceAutoRemoveDays
}

func (h *lanHub) writePolicy(out *MutableSettings) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out.LANDeviceOfflineAfterSec = h.policy.OfflineAfterSec
	out.LANDeviceOnlineAfterSec = h.policy.OnlineAfterSec
	out.LANDeviceOfflineNotifyDelaySec = h.policy.OfflineNotifyDelaySec
	out.LANDeviceOnlineNotifyDelaySec = h.policy.OnlineNotifyDelaySec
	out.LANMaxCheckAttempts = h.policy.MaxCheckAttempts
	out.LANNotifyCooldownSec = h.policy.NotifyCooldownSec
	out.LANFlappingThreshold = h.policy.FlappingThreshold
	out.LANFlappingWindowSec = int(h.policy.FlappingWindow.Seconds())
	out.LANDeviceAutoRemoveDays = h.policy.AutoRemoveDays
}

func (h *lanHub) policySnapshot() lanPolicy {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.policy
}

func (h *lanHub) getDevicesCopy() map[string]LANDevice {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.deviceMap == nil {
		h.deviceMap = loadLANDeviceMap(h.dataDir)
	}
	return cloneLANDeviceMap(h.deviceMap)
}

func (h *lanHub) putDevices(devices map[string]LANDevice) error {
	cloned := cloneLANDeviceMap(devices)
	h.mu.Lock()
	h.deviceMap = cloned
	h.mu.Unlock()
	return saveLANDeviceMap(h.dataDir, cloned)
}

func (h *lanHub) putDevicesLocked(devices map[string]LANDevice) error {
	cloned := cloneLANDeviceMap(devices)
	h.deviceMap = cloned
	return saveLANDeviceMap(h.dataDir, cloned)
}

func (h *lanHub) getSnapshot() (LANDeviceSnapshot, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.snapshot.GeneratedAt == "" {
		return LANDeviceSnapshot{}, false
	}
	return h.snapshot, true
}

func (h *lanHub) setSnapshot(snap LANDeviceSnapshot) {
	h.mu.Lock()
	h.snapshot = snap
	h.mu.Unlock()
}

func (h *lanHub) withLock(fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fn()
}
