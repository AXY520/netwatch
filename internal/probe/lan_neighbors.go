package probe

import (
	"bufio"
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jsimonetti/rtnetlink/v2"
	"golang.org/x/sys/unix"
)

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
	sem := make(chan struct{}, 128)
	count := 0
	for _, target := range ips {
		if target.Equal(ip) {
			continue
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return count
		default:
		}
		count++
		wg.Add(1)
		sem <- struct{}{}
		go func(addr string) {
			defer wg.Done()
			defer func() { <-sem }()
			d := net.Dialer{Timeout: 120 * time.Millisecond}
			conn, err := d.DialContext(ctx, "udp4", net.JoinHostPort(addr, "9"))
			if err == nil {
				_, _ = conn.Write([]byte{0})
				_ = conn.Close()
			}
		}(target.String())
	}
	wg.Wait()
	select {
	case <-ctx.Done():
	case <-time.After(80 * time.Millisecond):
	}
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
			VendorHint:   macAddressHint(mac),
			Reachability: "arp-cache",
		})
	}
	return out
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

// lanResolver uses the pure-Go DNS client so context deadlines are actually
// enforced (cgo/system resolver frequently ignores short timeouts).
