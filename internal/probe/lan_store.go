package probe

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"sort"
	"strings"
	"time"
)

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
