package probe

import (
	"fmt"
	"net"
	"strings"
	"time"
)

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
