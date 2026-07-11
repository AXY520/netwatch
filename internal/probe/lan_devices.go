package probe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jsimonetti/rtnetlink/v2"
	"golang.org/x/sys/unix"

	"netwatch/internal/logger"
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
	s.lan.mu.Lock()
	defer s.lan.mu.Unlock()
	if s.lan.snapshot.GeneratedAt != "" {
		return s.lan.snapshot
	}
	snap := s.loadLANDeviceSnapshotCached()
	s.lan.snapshot = snap
	return snap
}

func (s *Service) ScanLANDevices(ctx context.Context) LANDeviceSnapshot {
	return s.scanLANDevices(ctx, false)
}

func (s *Service) scanLANDevices(ctx context.Context, allowNotify bool) LANDeviceSnapshot {
	if ctx == nil {
		ctx = context.Background()
	}
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
	seen := readLANNeighborDevices()
	ipv6Map := readIPv6Neighbors()
	dhcpHosts := readDHCPLeases()
	seenConfirmed := confirmedLANDevicesByMAC(seen)

	// Merge IPv6 addresses into seen devices by MAC
	for i := range seen {
		if v6, ok := ipv6Map[strings.ToLower(seen[i].MAC)]; ok {
			seen[i].IPv6 = v6
		}
	}

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
	return snap
}

// isLANFlapping checks if a device has been changing state too frequently.
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

func (s *Service) startLANInterfaceMonitor() {
	go func() {
		defer close(s.lanInterfaceDone)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.lanInterfaceStop:
				return
			case <-ticker.C:
				s.mu.RLock()
				notifyLAN := s.notify.snapshotConfig().NotifyLANDeviceChange
				s.mu.RUnlock()
				events := s.refreshLANInterfaceStatusSnapshot()
				if notifyLAN {
					for _, ev := range events {
						s.pushNotification(ev.Kind, ev.Severity, ev.Title, ev.Body)
					}
				}
			}
		}
	}()
}

func (s *Service) refreshLANInterfaceStatusSnapshot() []NotificationEvent {
	networks := discoverLANScanNetworks()
	interfaceUp := lanInterfaceUpMap(networks)
	stored := s.getLANDevicesCopy()
	removeInternalLANDevices(stored)
	addStoredLANInterfaces(interfaceUp, stored)
	events := s.updateLANInterfaceState(interfaceUp)
	if len(interfaceUp) == 0 {
		return events
	}

	nowText := localTimestamp()
	changed := false
	for key, dev := range stored {
		if dev.Interface == "" {
			continue
		}
		up, ok := interfaceUp[dev.Interface]
		if !ok {
			continue
		}
		if !up {
			if dev.Status != "interface_down" {
				dev.Status = "interface_down"
				dev.LastChanged = nowText
				dev.NotifyState = "interface_down"
				stored[key] = dev
				changed = true
			}
			continue
		}
		if dev.Status == "interface_down" {
			dev.Status = "interface_pending"
			dev.LastChanged = nowText
			stored[key] = dev
			changed = true
			continue
		}
		if dev.Status == "interface_pending" {
			continue
		}
	}
	if !changed {
		return events
	}
	snap := buildLANDeviceSnapshotFromMap(stored, networks, "网卡状态已更新。")
	_ = s.putLANDevices(stored)
	s.lan.mu.Lock()
	s.lan.snapshot = snap
	s.lan.mu.Unlock()
	return events
}

func lanInterfaceUpMap(networks []LANScanNetwork) map[string]bool {
	out := make(map[string]bool, len(networks))
	for _, network := range networks {
		if network.Interface == "" {
			continue
		}
		if _, exists := out[network.Interface]; !exists {
			out[network.Interface] = network.LinkUp
			continue
		}
		out[network.Interface] = out[network.Interface] || network.LinkUp
	}
	return out
}

func addStoredLANInterfaces(interfaceUp map[string]bool, stored map[string]LANDevice) {
	for _, dev := range stored {
		if dev.Interface == "" {
			continue
		}
		if shouldIgnoreInterface(dev.Interface) {
			continue
		}
		if _, exists := interfaceUp[dev.Interface]; !exists {
			interfaceUp[dev.Interface] = false
		}
	}
}

func (s *Service) updateLANInterfaceState(current map[string]bool) []NotificationEvent {
	s.lan.mu.Lock()
	defer s.lan.mu.Unlock()
	if s.lan.interfaceState == nil {
		s.lan.interfaceState = make(map[string]bool, len(current))
		for name, up := range current {
			s.lan.interfaceState[name] = up
		}
		return nil
	}
	var events []NotificationEvent
	for name, up := range current {
		prev, exists := s.lan.interfaceState[name]
		s.lan.interfaceState[name] = up
		if !exists || prev == up {
			continue
		}
		label := lanInterfaceLabel(name)
		if up {
			events = append(events, NotificationEvent{
				Kind:     "lan_interface_up",
				Severity: "info",
				Title:    label + " 网卡已连接",
				Body:     fmt.Sprintf("网络接口「%s」（%s）已恢复连接，链路可用。\n检测时间：%s", name, label, localTimestamp()),
			})
		} else {
			events = append(events, NotificationEvent{
				Kind:     "lan_interface_down",
				Severity: "warn",
				Title:    label + " 网卡已断开",
				Body:     fmt.Sprintf("网络接口「%s」（%s）连接已断开，请检查网线或 Wi-Fi 连接。\n检测时间：%s", name, label, localTimestamp()),
			})
		}
	}
	return events
}

func lanInterfaceLabel(name string) string {
	meta := nicMetaForName(name)
	if strings.TrimSpace(meta.Label) != "" {
		return meta.Label
	}
	return name
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

func discoverLANScanNetworks() []LANScanNetwork {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	monitored := map[string]struct{}{}
	for _, name := range autoMonitoredNICs(ifaces) {
		monitored[name] = struct{}{}
	}
	var out []LANScanNetwork
	for _, iface := range ifaces {
		if _, ok := monitored[iface.Name]; !ok {
			continue
		}
		meta := nicMetaForName(iface.Name)
		operState := readOperState(iface.Name)
		kernelUp := lanInterfaceKernelUp(iface, operState)
		added := false
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ip, ipNet, ok := parseIPv4Net(addr)
			if !ok || ip.IsLoopback() || !isPrivateLANIPv4(ip) {
				continue
			}
			ones, bits := ipNet.Mask.Size()
			item := LANScanNetwork{
				Interface: iface.Name,
				CIDR:      ipNet.String(),
				LinkType:  meta.LinkType,
				LinkLabel: meta.Label,
				LinkUp:    kernelUp,
				OperState: operState,
			}
			size := 1 << max(0, bits-ones)
			if !kernelUp {
				item.Skipped = true
				item.Reason = "网卡未连接"
			} else if ones < 23 || size > 512 {
				item.Skipped = true
				item.Reason = "网段过大，避免主动扫描"
			}
			out = append(out, item)
			added = true
		}
		if !added {
			reason := "未获取到局域网 IPv4 地址"
			if !kernelUp {
				reason = "网卡未连接"
			}
			out = append(out, LANScanNetwork{
				Interface: iface.Name,
				Skipped:   true,
				Reason:    reason,
				LinkType:  meta.LinkType,
				LinkLabel: meta.Label,
				LinkUp:    kernelUp,
				OperState: operState,
			})
		}
	}
	return out
}

func lanInterfaceKernelUp(iface net.Interface, operState string) bool {
	if iface.Flags&net.FlagUp == 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(operState)) {
	case "down", "lowerlayerdown", "dormant", "notpresent":
		return false
	default:
		return true
	}
}

func monitoredLANInterfaceSet() map[string]struct{} {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := map[string]struct{}{}
	for _, name := range autoMonitoredNICs(ifaces) {
		out[name] = struct{}{}
	}
	return out
}

func warmLANNeighbors(ctx context.Context, cidr string) int {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil || ip.To4() == nil {
		return 0
	}
	ips := hostsInIPv4Net(ipNet)
	if len(ips) > 512 {
		return 0
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	count := 0
	for _, target := range ips {
		if target.Equal(ip) {
			continue
		}
		select {
		case <-ctx.Done():
			return count
		default:
		}
		count++
		wg.Add(1)
		sem <- struct{}{}
		go func(addr string) {
			defer wg.Done()
			defer func() { <-sem }()
			d := net.Dialer{Timeout: 250 * time.Millisecond}
			conn, err := d.DialContext(ctx, "udp4", net.JoinHostPort(addr, "9"))
			if err == nil {
				_, _ = conn.Write([]byte{0})
				_ = conn.Close()
			}
		}(target.String())
	}
	wg.Wait()
	time.Sleep(250 * time.Millisecond)
	return count
}

func readLANNeighborDevices() []LANDevice {
	if devices := readNetlinkNeighborDevices(); len(devices) > 0 {
		return devices
	}
	return readARPDevices()
}

func readNetlinkNeighborDevices() []LANDevice {
	conn, err := rtnetlink.Dial(nil)
	if err != nil {
		return nil
	}
	defer conn.Close()

	msgs, err := conn.Neigh.List()
	if err != nil {
		return nil
	}
	ifaces, _ := net.Interfaces()
	monitored := monitoredLANInterfaceSet()
	ifaceNames := make(map[int]string, len(ifaces))
	for _, iface := range ifaces {
		ifaceNames[iface.Index] = iface.Name
	}

	var out []LANDevice
	for _, msg := range msgs {
		if msg.Family != unix.AF_INET || msg.Attributes == nil {
			continue
		}
		ifaceName := ifaceNames[int(msg.Index)]
		if !isMonitoredLANInterface(monitored, ifaceName) {
			continue
		}
		ip := msg.Attributes.Address.To4()
		if ip == nil || ip.IsLoopback() || !isPrivateLANIPv4(ip) {
			continue
		}
		mac := strings.ToLower(msg.Attributes.LLAddress.String())
		if mac == "" || mac == "00:00:00:00:00:00" {
			continue
		}
		state := lanNeighborStateName(msg.State)
		out = append(out, LANDevice{
			IP:           ip.String(),
			MAC:          mac,
			Interface:    ifaceName,
			Hostname:     lookupLANHostname(ip.String()),
			VendorHint:   macAddressHint(mac),
			Reachability: state,
		})
	}
	return out
}

func readARPDevices() []LANDevice {
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []LANDevice
	monitored := monitoredLANInterfaceSet()
	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		// header
	}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 {
			continue
		}
		flags, _ := strconv.ParseInt(strings.TrimPrefix(fields[2], "0x"), 16, 64)
		if flags == 0 {
			continue
		}
		ip := fields[0]
		mac := strings.ToLower(fields[3])
		ifaceName := fields[5]
		if !isMonitoredLANInterface(monitored, ifaceName) {
			continue
		}
		if net.ParseIP(ip) == nil || mac == "00:00:00:00:00:00" {
			continue
		}
		out = append(out, LANDevice{
			IP:           ip,
			MAC:          mac,
			Interface:    ifaceName,
			Hostname:     lookupLANHostname(ip),
			VendorHint:   macAddressHint(mac),
			Reachability: "arp-cache",
		})
	}
	return out
}

func loadLANDeviceSnapshot(dataDir string) LANDeviceSnapshot {
	devices := loadLANDeviceMap(dataDir)
	removeInternalLANDevices(devices)
	return buildLANDeviceSnapshotFromMap(devices, nil, "")
}

func (s *Service) loadLANDeviceSnapshotCached() LANDeviceSnapshot {
	devices := s.getLANDevicesCopy()
	removeInternalLANDevices(devices)
	return buildLANDeviceSnapshotFromMap(devices, nil, "")
}

func loadLANDeviceMap(dataDir string) map[string]LANDevice {
	out := map[string]LANDevice{}
	body, err := os.ReadFile(lanDevicesPath(dataDir))
	if err != nil {
		return out
	}
	var store lanDeviceStore
	if err := json.Unmarshal(body, &store); err != nil {
		return out
	}
	for mac, dev := range store.Devices {
		if dev.MAC == "" {
			dev.MAC = mac
		}
		dev.MAC = normalizeMAC(dev.MAC)
		dev.Known = lanDeviceKnown(dev)
		out[dev.MAC] = dev
	}
	return out
}

func saveLANDeviceMap(dataDir string, devices map[string]LANDevice) error {
	if dataDir == "" {
		return errors.New("data dir is empty")
	}
	return writeJSONFile(lanDevicesPath(dataDir), lanDeviceStore{Devices: devices}, true)
}

func buildLANDeviceSnapshotFromMap(stored map[string]LANDevice, networks []LANScanNetwork, note string) LANDeviceSnapshot {
	devices := make([]LANDevice, 0, len(stored))
	for _, dev := range stored {
		if isInternalLANDevice(dev) {
			continue
		}
		dev.Known = lanDeviceKnown(dev)
		if !dev.Known {
			continue
		}
		devices = append(devices, dev)
	}
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].Status != devices[j].Status {
			return devices[i].Status == "online"
		}
		return devices[i].IP < devices[j].IP
	})
	snap := LANDeviceSnapshot{GeneratedAt: localTimestamp(), Networks: networks, Note: note}
	for _, dev := range devices {
		if dev.Ignored {
			snap.IgnoredDevices = append(snap.IgnoredDevices, dev)
			continue
		}
		snap.Devices = append(snap.Devices, dev)
		switch dev.Status {
		case "online":
			snap.Online++
		case "offline":
			snap.Offline++
		}
		if dev.NewDevice {
			snap.NewCount++
		}
		if !dev.Known {
			snap.Unknown++
		}
	}
	return snap
}

func parseIPv4Net(addr net.Addr) (net.IP, *net.IPNet, bool) {
	ipNet, ok := addr.(*net.IPNet)
	if !ok {
		return nil, nil, false
	}
	ip := ipNet.IP.To4()
	if ip == nil {
		return nil, nil, false
	}
	return ip, ipNet, true
}

func hostsInIPv4Net(ipNet *net.IPNet) []net.IP {
	ip := ipNet.IP.To4()
	if ip == nil {
		return nil
	}
	start := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
	mask := ipNet.Mask
	base := start & (uint32(mask[0])<<24 | uint32(mask[1])<<16 | uint32(mask[2])<<8 | uint32(mask[3]))
	ones, bits := mask.Size()
	size := 1 << max(0, bits-ones)
	if size <= 2 || size > 512 {
		return nil
	}
	out := make([]net.IP, 0, size-2)
	for i := 1; i < size-1; i++ {
		v := base + uint32(i)
		out = append(out, net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v)))
	}
	return out
}

func confirmedLANDevicesByMAC(devices []LANDevice) map[string]bool {
	out := make(map[string]bool, len(devices))
	for _, dev := range devices {
		if dev.MAC == "" || !lanNeighborConfirmsOnline(dev.Reachability) {
			continue
		}
		out[strings.ToLower(dev.MAC)] = true
	}
	return out
}

func isMonitoredLANInterface(monitored map[string]struct{}, name string) bool {
	if name == "" || shouldIgnoreInterface(name) {
		return false
	}
	if len(monitored) == 0 {
		return false
	}
	_, ok := monitored[name]
	return ok
}

func removeInternalLANDevices(devices map[string]LANDevice) {
	for mac, dev := range devices {
		if isInternalLANDevice(dev) {
			delete(devices, mac)
		}
	}
}

// removeStaleLANDevices removes offline devices that haven't been seen
// for more than the configured number of days. Pinned and ignored devices
// are preserved. Returns the number of removed devices.
func removeStaleLANDevices(devices map[string]LANDevice, autoRemoveDays int) int {
	if autoRemoveDays <= 0 {
		return 0
	}
	cutoff := time.Now().AddDate(0, 0, -autoRemoveDays)
	removed := 0
	for mac, dev := range devices {
		if dev.Pinned || dev.Ignored {
			continue
		}
		if dev.Status != "offline" {
			continue
		}
		lastSeen, err := time.ParseInLocation("2006-01-02 15:04:05", dev.LastSeen, time.Local)
		if err != nil {
			continue
		}
		if lastSeen.Before(cutoff) {
			delete(devices, mac)
			removed++
		}
	}
	return removed
}

func isInternalLANDevice(dev LANDevice) bool {
	host := strings.ToLower(dev.Hostname)
	if strings.HasSuffix(host, ".lzcapp") || strings.Contains(host, ".lzcapp.") {
		return true
	}
	if shouldIgnoreInterface(dev.Interface) {
		return true
	}
	if strings.HasPrefix(normalizeMAC(dev.MAC), "02:42:") {
		return true
	}
	return false
}

func lanDeviceKnown(dev LANDevice) bool {
	if strings.TrimSpace(dev.Note) != "" || dev.Ignored {
		return true
	}
	if strings.TrimSpace(dev.Hostname) != "" {
		return true
	}
	vendor := strings.TrimSpace(dev.VendorHint)
	return vendor != "" && vendor != "未知厂商" && vendor != "本地随机 MAC"
}

func normalizeMAC(mac string) string {
	hw, err := net.ParseMAC(strings.TrimSpace(mac))
	if err != nil {
		return strings.ToLower(strings.TrimSpace(mac))
	}
	return strings.ToLower(hw.String())
}

func lanNeighborConfirmsOnline(state string) bool {
	switch strings.ToLower(state) {
	case "reachable", "delay", "probe":
		return true
	default:
		return false
	}
}

func lanNeighborConfirmsOffline(state string) bool {
	switch strings.ToLower(state) {
	case "", "arp-cache", "stale", "failed", "incomplete", "none":
		return true
	default:
		return false
	}
}

func lanNeighborStateName(state uint16) string {
	switch {
	case state&unix.NUD_FAILED != 0:
		return "failed"
	case state&unix.NUD_INCOMPLETE != 0:
		return "incomplete"
	case state&unix.NUD_REACHABLE != 0:
		return "reachable"
	case state&unix.NUD_DELAY != 0:
		return "delay"
	case state&unix.NUD_PROBE != 0:
		return "probe"
	case state&unix.NUD_STALE != 0:
		return "stale"
	case state&unix.NUD_NOARP != 0:
		return "noarp"
	case state&unix.NUD_PERMANENT != 0:
		return "permanent"
	default:
		return "none"
	}
}

func lookupLANHostname(ip string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(ctx, ip)
	if err != nil || len(names) == 0 {
		return ""
	}
	return strings.TrimSuffix(names[0], ".")
}

func macAddressHint(mac string) string {
	mac = normalizeMAC(mac)
	parts := strings.Split(mac, ":")
	if len(parts) != 6 {
		return ""
	}
	first, err := strconv.ParseUint(parts[0], 16, 8)
	if err != nil {
		return ""
	}
	if first&0x02 != 0 {
		return "本地随机 MAC"
	}
	if vendor := lookupOUIVendor(parts[0] + parts[1] + parts[2]); vendor != "" {
		return vendor
	}
	return "未知厂商"
}

func lookupOUIVendor(prefix string) string {
	prefix = normalizeOUIPrefix(prefix)
	if prefix == "" {
		return ""
	}
	if vendor := lookupOUIVendorFromFiles(prefix); vendor != "" {
		return vendor
	}
	return fallbackOUIVendors()[prefix]
}

var ouiVendorCache struct {
	once    sync.Once
	vendors map[string]string
}

func lookupOUIVendorFromFiles(prefix string) string {
	ouiVendorCache.once.Do(func() {
		ouiVendorCache.vendors = loadOUIVendors()
	})
	return ouiVendorCache.vendors[prefix]
}

func loadOUIVendors() map[string]string {
	out := map[string]string{}
	paths := []string{
		"/usr/share/nmap/nmap-mac-prefixes",
		"/usr/share/misc/oui.txt",
		"/usr/share/ieee-data/oui.txt",
		"/var/lib/ieee-data/oui.txt",
		"/etc/oui.txt",
	}
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		parseOUIVendorFile(f, out)
		f.Close()
	}
	return out
}

func parseOUIVendorFile(r io.Reader, out map[string]string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		prefix := normalizeOUIPrefix(fields[0])
		if prefix == "" {
			continue
		}
		vendor := ""
		if strings.EqualFold(fields[1], "(hex)") || strings.EqualFold(fields[1], "(base") {
			if i := strings.Index(line, ")"); i >= 0 && i+1 < len(line) {
				vendor = strings.TrimSpace(line[i+1:])
			}
		} else {
			vendor = strings.TrimSpace(line[len(fields[0]):])
		}
		vendor = strings.Join(strings.Fields(vendor), " ")
		if vendor != "" {
			out[prefix] = vendor
		}
	}
}

func normalizeOUIPrefix(prefix string) string {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	var b strings.Builder
	for _, ch := range prefix {
		if (ch >= '0' && ch <= '9') || (ch >= 'A' && ch <= 'F') {
			b.WriteRune(ch)
			if b.Len() == 6 {
				break
			}
		}
	}
	if b.Len() != 6 {
		return ""
	}
	return b.String()
}

func fallbackOUIVendors() map[string]string {
	return map[string]string{
		"001122": "Cisco",
		"001A11": "Google",
		"001B63": "Apple",
		"001D4F": "Apple",
		"002248": "Microsoft",
		"002332": "Apple",
		"00236C": "Apple",
		"002500": "Apple",
		"00259C": "Cisco-Linksys",
		"0026BB": "Apple",
		"0050E4": "Apple",
		"0050F2": "Microsoft",
		"006171": "Apple",
		"007D60": "Ubiquiti",
		"008865": "Apple",
		"00A040": "Apple",
		"00E04C": "Realtek",
		"042B58": "Shenzhen Hanzsung Technology",
		"10AE60": "Private",
		"14CC20": "TP-Link",
		"18FE34": "Espressif",
		"1C1B0D": "Giga-Byte",
		"1C36BB": "Apple",
		"20A2E4": "Apple",
		"24A160": "Espressif",
		"28CFE9": "Apple",
		"2C54CF": "LG",
		"34AB37": "Apple",
		"3C22FB": "Apple",
		"3C5A37": "Samsung",
		"3C7C3F": "Apple",
		"44D884": "Apple",
		"50C7BF": "TP-Link",
		"5C497D": "Samsung",
		"60F81D": "Apple",
		"64B0A6": "Apple",
		"6C4008": "Apple",
		"701CE7": "Intel",
		"748114": "Apple",
		"7824AF": "ASUSTek",
		"784F43": "Apple",
		"7C04D0": "Apple",
		"7C2EDD": "Samsung",
		"80E650": "Apple",
		"843A4B": "Intel",
		"847303": "Letv",
		"881FA1": "Apple",
		"8C8590": "Apple",
		"98E743": "Dell",
		"A020A6": "Espressif",
		"A4C138": "Telink",
		"A4D1D2": "Apple",
		"A85B36": "Huawei",
		"ACBC32": "Apple",
		"B827EB": "Raspberry Pi",
		"BC5436": "Apple",
		"C0A53E": "Apple",
		"C8BCC8": "Apple",
		"D0C5D3": "Apple",
		"D8BB2C": "Apple",
		"DC2B2A": "Apple",
		"E0B9A5": "Apple",
		"E4A7A0": "Intel",
		"E4CE8F": "Apple",
		"F0D5BF": "Intel",
		"F4F5D8": "Google",
		"F8FF0B": "Apple",
	}
}

func lanDeviceBody(dev LANDevice) string {
	name := firstNonEmpty(dev.Note, dev.Hostname, "未知设备")
	parts := []string{fmt.Sprintf("设备名称：%s", name)}
	parts = append(parts, fmt.Sprintf("IP 地址：%s", dev.IP))
	if len(dev.IPv6) > 0 {
		parts = append(parts, fmt.Sprintf("IPv6：%s", strings.Join(dev.IPv6, ", ")))
	}
	parts = append(parts, fmt.Sprintf("MAC 地址：%s", dev.MAC))
	if dev.VendorHint != "" {
		parts = append(parts, fmt.Sprintf("厂商：%s", dev.VendorHint))
	}
	if dev.Interface != "" {
		parts = append(parts, fmt.Sprintf("接口：%s", dev.Interface))
	}
	if len(dev.DetectionMethods) > 0 {
		parts = append(parts, fmt.Sprintf("检测方式：%s", strings.Join(dev.DetectionMethods, ", ")))
	}
	if dev.LastSeen != "" {
		parts = append(parts, fmt.Sprintf("最后在线：%s", dev.LastSeen))
	}
	parts = append(parts, fmt.Sprintf("检测时间：%s", localTimestamp()))
	return strings.Join(parts, "\n")
}

func isPrivateLANIPv4(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] == 10 ||
		(v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31) ||
		(v4[0] == 192 && v4[1] == 168) ||
		(v4[0] == 169 && v4[1] == 254)
}
