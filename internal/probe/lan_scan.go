package probe

import (
	"context"
	"fmt"
	"strings"
	"time"

	"netwatch/internal/logger"
)

func (s *Service) scanLANDevices(ctx context.Context, allowNotify bool) LANDeviceSnapshot {
	if ctx == nil {
		ctx = context.Background()
	}
	// Hard cap even if caller forgets a deadline (UI previously waited forever).
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
	}
	// Never inherit a cancelled request context from callers that forget to detach.
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
	}
	scanStarted := time.Now()
	logger.Info("LAN scan start")
	now := time.Now()
	nowText := localTimestamp()
	policy := s.lan.policySnapshot()
	offlineAfter := time.Duration(policy.OfflineAfterSec) * time.Second
	onlineAfter := time.Duration(policy.OnlineAfterSec) * time.Second
	offlineNotifyDelay := time.Duration(policy.OfflineNotifyDelaySec) * time.Second
	onlineNotifyDelay := time.Duration(policy.OnlineNotifyDelaySec) * time.Second
	maxMiss := policy.MaxCheckAttempts
	cooldownSec := policy.NotifyCooldownSec
	flapThreshold := policy.FlappingThreshold
	flapWindow := policy.FlappingWindow
	if offlineAfter < 10*time.Second {
		offlineAfter = 3 * time.Minute
	}
	if onlineAfter < 0 {
		onlineAfter = 0
	}
	if maxMiss < 1 {
		maxMiss = 3
	}
	if cooldownSec < 60 {
		cooldownSec = 600
	}
	if flapThreshold < 3 {
		flapThreshold = 5
	}
	if flapWindow < time.Minute {
		flapWindow = 10 * time.Minute
	}

	// Phase 1: Discover networks and interfaces
	networks := discoverLANScanNetworks()
	interfaceUp := lanInterfaceUpMap(networks)
	stored := s.getLANDevicesCopy()
	removeInternalLANDevices(stored)
	addStoredLANInterfaces(interfaceUp, stored)
	interfaceEvents := s.updateLANInterfaceState(interfaceUp)

	// Phase 2: Warm neighbors with UDP + ICMP
	for i := range networks {
		if !networks[i].Skipped {
			networks[i].Scanned = warmNeighborsWithICMP(ctx, networks[i].CIDR)
		}
	}

	// Phase 3: Read neighbor tables (ARP + IPv6 NDP)
	// IMPORTANT: do not reverse-DNS every incomplete ARP entry here. Warming a
	// /24 can produce hundreds of incomplete neighbors; serial LookupAddr then
	// hangs the scan far past the UI timeout (and cgo DNS often ignores ctx).
	seen := readLANNeighborDevices()
	ipv6Map := readIPv6Neighbors()
	dhcpHosts := readDHCPLeases()
	seenConfirmed := confirmedLANDevicesByMAC(seen)

	// Merge IPv6 addresses into seen devices by MAC and enrich hostnames cheaply.
	for i := range seen {
		key := strings.ToLower(seen[i].MAC)
		if v6, ok := ipv6Map[key]; ok {
			seen[i].IPv6 = v6
		}
		if seen[i].Hostname == "" {
			if h, ok := dhcpHosts[key]; ok {
				seen[i].Hostname = h
			}
		}
	}
	// Bounded reverse-DNS only for confirmed-online devices still missing names.
	fillLANHostnamesBounded(ctx, seen, 12, 150*time.Millisecond)

	var events []NotificationEvent

	// Phase 4: Update devices that were seen
	for _, dev := range seen {
		if dev.MAC == "" || dev.IP == "" || dev.MAC == "00:00:00:00:00:00" {
			continue
		}
		key := strings.ToLower(dev.MAC)
		prev, exists := stored[key]

		// Enrich hostname from DHCP leases
		if dev.Hostname == "" {
			if h, ok := dhcpHosts[key]; ok {
				dev.Hostname = h
			}
		}

		// Track detection methods
		dev.DetectionMethods = []string{"arp_neighbor"}
		if len(dev.IPv6) > 0 {
			dev.DetectionMethods = append(dev.DetectionMethods, "ndp_ipv6")
		}

		if !lanNeighborConfirmsOnline(dev.Reachability) {
			if exists {
				prev.IP = firstNonEmpty(dev.IP, prev.IP)
				prev.Interface = firstNonEmpty(dev.Interface, prev.Interface)
				prev.Reachability = dev.Reachability
				// Merge IPv6
				if len(dev.IPv6) > 0 {
					prev.IPv6 = mergeIPv6(prev.IPv6, dev.IPv6)
				}
				if prev.Status == "online" && lanNeighborConfirmsOffline(dev.Reachability) {
					prev.MissCount++
					if prev.MissCount >= maxMiss {
						prev.Status = "offline"
						prev.LastChanged = nowText
					}
				}
				stored[key] = prev
			}
			continue
		}

		if !exists {
			dev.FirstSeen = nowText
			dev.LastChanged = nowText
			dev.NewDevice = true
			dev.MissCount = 0
		} else {
			wasOffline := prev.Status == "offline" || prev.Status == "online_pending"
			wasInterfaceDown := prev.Status == "interface_down" || prev.Status == "interface_pending" || prev.NotifyState == "interface_down"
			offlineSince, offlineStable := parseLANDeviceTime(prev.LastChanged)
			dev.FirstSeen = prev.FirstSeen
			dev.SeenCount = prev.SeenCount
			dev.Hostname = firstNonEmpty(dev.Hostname, prev.Hostname)
			dev.Note = prev.Note
			dev.Ignored = prev.Ignored
			dev.LastNotified = prev.LastNotified
			dev.NotifyState = prev.NotifyState
			dev.VendorHint = firstNonEmpty(dev.VendorHint, prev.VendorHint)
			dev.Pinned = prev.Pinned
			dev.MissCount = 0 // Reset miss count on successful detection
			// Merge IPv6
			dev.IPv6 = mergeIPv6(prev.IPv6, dev.IPv6)

			if wasInterfaceDown {
				dev.LastChanged = nowText
				dev.NotifyState = ""
			} else if wasOffline {
				firstConfirmedOnline, onlineStable := parseLANDeviceTime(prev.LastChanged)
				if !onlineStable {
					firstConfirmedOnline = now
				}
				if onlineAfter > 0 && now.Sub(firstConfirmedOnline) < onlineAfter {
					dev.Status = "online_pending"
					dev.LastChanged = prev.LastChanged
					if prev.Status == "offline" {
						dev.LastChanged = nowText
					}
					dev.LastSeen = nowText
					dev.SeenCount++
					stored[key] = dev
					continue
				}
				dev.LastChanged = nowText
				if prev.NotifyState != "interface_down" && lanDeviceKnown(dev) && !dev.Ignored && offlineStable && now.Sub(offlineSince) >= onlineNotifyDelay {
					if !s.isLANFlapping(key, now, flapThreshold, flapWindow) && s.canNotifyLANDevice(key, now, cooldownSec) {
						devName := firstNonEmpty(dev.Note, dev.Hostname, dev.IP)
						events = append(events, NotificationEvent{
							Kind:     "lan_device_online",
							Severity: "info",
							Title:    fmt.Sprintf("设备上线：%s", devName),
							Body:     lanDeviceBody(dev),
						})
						dev.LastNotified = nowText
						dev.NotifyState = "online"
					}
				}
			} else {
				dev.LastChanged = prev.LastChanged
			}
		}
		dev.Status = "online"
		dev.LastSeen = nowText
		dev.SeenCount++
		stored[key] = dev
	}

	// Phase 5: Update devices that were NOT seen (potential offline)
	for key, dev := range stored {
		if dev.Interface != "" && !interfaceUp[dev.Interface] {
			if dev.Status != "interface_down" {
				dev.Status = "interface_down"
				dev.LastChanged = nowText
				dev.NotifyState = "interface_down"
				stored[key] = dev
			}
			continue
		}
		if dev.Status == "interface_down" {
			dev.LastChanged = nowText
			stored[key] = dev
			continue
		}
		if dev.Status == "interface_pending" {
			continue
		}
		if dev.Status == "online_pending" && !seenConfirmed[key] {
			dev.MissCount++
			if dev.MissCount >= maxMiss {
				dev.Status = "offline"
				dev.LastChanged = nowText
			}
			stored[key] = dev
			continue
		}
		if dev.Status == "online" && !seenConfirmed[key] {
			lastSeen, err := time.ParseInLocation(time.DateTime, dev.LastSeen, time.Local)
			if err == nil && now.Sub(lastSeen) >= offlineAfter {
				dev.MissCount++
				if dev.MissCount >= maxMiss {
					dev.Status = "offline"
					dev.LastChanged = nowText
					stored[key] = dev
				} else {
					stored[key] = dev
				}
			}
		}
		if dev.Status == "offline" {
			lastChanged, ok := parseLANDeviceTime(dev.LastChanged)
			if lanDeviceKnown(dev) && !dev.Ignored && ok && now.Sub(lastChanged) >= offlineNotifyDelay && dev.NotifyState != "offline" {
				if !s.isLANFlapping(key, now, flapThreshold, flapWindow) && s.canNotifyLANDevice(key, now, cooldownSec) {
					devName := firstNonEmpty(dev.Note, dev.Hostname, dev.IP)
					offlineDur := now.Sub(lastChanged).Truncate(time.Minute)
					events = append(events, NotificationEvent{
						Kind:     "lan_device_offline",
						Severity: "warn",
						Title:    fmt.Sprintf("设备离线：%s", devName),
						Body:     fmt.Sprintf("%s\n离线时长：%s", lanDeviceBody(dev), offlineDur.String()),
					})
					dev.LastNotified = nowText
					dev.NotifyState = "offline"
				}
			}
			stored[key] = dev
		}
	}

	removeInternalLANDevices(stored)
	autoRemoveDays := s.lan.policySnapshot().AutoRemoveDays
	if autoRemoveDays > 0 {
		if n := removeStaleLANDevices(stored, autoRemoveDays); n > 0 {
			logger.Info("LAN auto-cleanup: removed %d stale devices (offline > %d days)", n, autoRemoveDays)
		}
	}

	snap := buildLANDeviceSnapshotFromMap(stored, networks, "综合 ARP/NDP/ICMP/DHCP 多源检测，仅扫描真实有线/Wi-Fi 网卡；Docker/Lazycat 内部网桥设备已过滤。")

	_ = s.putLANDevices(stored)
	s.lan.mu.Lock()
	s.lan.snapshot = snap
	s.lan.mu.Unlock()
	s.broadcastLANDevices(snap)
	s.mu.RLock()
	notifyLAN := allowNotify && s.notify.snapshotConfig().NotifyLANDeviceChange
	s.mu.RUnlock()

	if notifyLAN {
		for _, ev := range interfaceEvents {
			s.pushNotification(ev.Kind, ev.Severity, ev.Title, ev.Body)
		}
		for _, ev := range events {
			s.pushNotification(ev.Kind, ev.Severity, ev.Title, ev.Body)
		}
	}
	logger.Info("LAN scan done: devices=%d online=%d networks=%d elapsed=%s",
		len(snap.Devices), snap.Online, len(networks), time.Since(scanStarted).Round(time.Millisecond))
	return s.lan.attachScanMeta(snap)
}

// isLANFlapping checks if a device has been changing state too frequently.
