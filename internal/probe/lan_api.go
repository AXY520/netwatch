package probe

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type lanDeviceStore struct {
	Devices map[string]LANDevice `json:"devices"`
}

type LANDeviceMetaUpdate struct {
	MAC     string  `json:"mac"`
	Note    *string `json:"note,omitempty"`
	Ignored *bool   `json:"ignored,omitempty"`
	Pinned  *bool   `json:"pinned,omitempty"`
}

func lanDevicesPath(dataDir string) string {
	return filepath.Join(dataDir, "lan_devices.json")
}

func (s *Service) GetLANDevices() LANDeviceSnapshot {
	// Fast path: return in-memory snapshot without nested lock calls.
	if snap, ok := s.lan.getSnapshot(); ok {
		return s.lan.attachScanMeta(snap)
	}
	// Cold path: load device map (takes hub lock internally), build snapshot, cache it.
	snap := s.loadLANDeviceSnapshotCached()
	s.lan.setSnapshot(snap)
	return s.lan.attachScanMeta(snap)
}

// StartLANScan kicks off a background discovery pass and returns the current
// snapshot immediately (with scanning=true). Request cancellation must not
// abort discovery — Lazycat hostproxy cancels clients that wait too long.
func (s *Service) StartLANScan() LANDeviceSnapshot {
	scanID := fmt.Sprintf("%d", time.Now().UnixNano())
	if !s.lan.beginScan(scanID) {
		// Already running — return current snapshot with scanning flag.
		return s.GetLANDevices()
	}
	go func(id string) {
		defer s.lan.endScan(id)
		// Detached from the HTTP request, but still cancelled with the service.
		ctx, cancel := context.WithTimeout(s.backgroundCtx(), 15*time.Second)
		defer cancel()
		_ = s.scanLANDevices(ctx, false)
	}(scanID)
	return s.GetLANDevices()
}

// ScanLANDevices runs a synchronous scan (tests/internal callers). Prefer
// StartLANScan for HTTP handlers behind reverse proxies.
func (s *Service) ScanLANDevices(ctx context.Context) LANDeviceSnapshot {
	return s.scanLANDevices(ctx, false)
}

func (s *Service) isLANFlapping(mac string, now time.Time, threshold int, window time.Duration) bool {
	s.lan.mu.Lock()
	defer s.lan.mu.Unlock()

	history := s.lan.flappingHistory[mac]
	// Prune old entries outside the window
	cutoff := now.Add(-window)
	pruned := make([]time.Time, 0, len(history))
	for _, t := range history {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	// Add current state change
	pruned = append(pruned, now)
	s.lan.flappingHistory[mac] = pruned

	return len(pruned) > threshold
}

// canNotifyLANDevice checks if enough time has passed since the last notification for this device.
func (s *Service) canNotifyLANDevice(mac string, now time.Time, cooldownSec int) bool {
	s.lan.mu.Lock()
	defer s.lan.mu.Unlock()

	last, ok := s.lan.notifyCooldown[mac]
	if !ok {
		s.lan.notifyCooldown[mac] = now
		return true
	}
	if now.Sub(last) < time.Duration(cooldownSec)*time.Second {
		return false
	}
	s.lan.notifyCooldown[mac] = now
	return true
}

// mergeIPv6 merges two IPv6 slices, deduplicating.
func mergeIPv6(existing, new []string) []string {
	if len(new) == 0 {
		return existing
	}
	if len(existing) == 0 {
		return new
	}
	seen := make(map[string]bool, len(existing))
	for _, ip := range existing {
		seen[ip] = true
	}
	for _, ip := range new {
		if !seen[ip] {
			existing = append(existing, ip)
			seen[ip] = true
		}
	}
	return existing
}

func parseLANDeviceTime(value string) (time.Time, bool) {
	t, err := time.ParseInLocation(time.DateTime, strings.TrimSpace(value), time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func (s *Service) UpdateLANDeviceMeta(in LANDeviceMetaUpdate) LANDeviceSnapshot {
	mac := normalizeMAC(in.MAC)
	if mac == "" {
		return s.GetLANDevices()
	}
	stored := s.getLANDevicesCopy()
	s.lan.mu.Lock()
	if s.lan.snapshot.GeneratedAt != "" {
		for _, dev := range s.lan.snapshot.Devices {
			key := normalizeMAC(dev.MAC)
			if key != "" {
				stored[key] = dev
			}
		}
		for _, dev := range s.lan.snapshot.IgnoredDevices {
			key := normalizeMAC(dev.MAC)
			if key != "" {
				stored[key] = dev
			}
		}
	}
	dev := stored[mac]
	if dev.MAC == "" {
		dev.MAC = mac
		dev.FirstSeen = localTimestamp()
	}
	noteChanged := in.Note != nil
	if noteChanged {
		dev.Note = strings.TrimSpace(*in.Note)
	}
	if in.Ignored != nil {
		dev.Ignored = *in.Ignored
	}
	dev.Known = lanDeviceKnown(dev)
	stored[mac] = dev
	removeInternalLANDevices(stored)
	_ = s.putLANDevicesLocked(stored)

	networks := s.lan.snapshot.Networks
	s.lan.snapshot = buildLANDeviceSnapshotFromMap(stored, networks, "已更新设备标记。")
	if noteChanged && s.lan.snapshot.GeneratedAt != "" {
		for i := range s.lan.snapshot.Devices {
			if normalizeMAC(s.lan.snapshot.Devices[i].MAC) == mac {
				s.lan.snapshot.Devices[i].Note = dev.Note
				s.lan.snapshot.Devices[i].Known = lanDeviceKnown(s.lan.snapshot.Devices[i])
			}
		}
		for i := range s.lan.snapshot.IgnoredDevices {
			if normalizeMAC(s.lan.snapshot.IgnoredDevices[i].MAC) == mac {
				s.lan.snapshot.IgnoredDevices[i].Note = dev.Note
				s.lan.snapshot.IgnoredDevices[i].Known = lanDeviceKnown(s.lan.snapshot.IgnoredDevices[i])
			}
		}
	}
	snap := s.lan.snapshot
	s.lan.mu.Unlock()
	s.broadcastLANDevices(snap)
	return snap
}
