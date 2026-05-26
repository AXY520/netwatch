package probe

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jsimonetti/rtnetlink/v2"
	"golang.org/x/sys/unix"
)

// icmpPings sends ICMP echo requests to the given IPs and returns those that responded.
func icmpPings(ctx context.Context, ips []string, timeout time.Duration) map[string]bool {
	responded := make(map[string]bool, len(ips))
	if len(ips) == 0 {
		return responded
	}

	// Try raw ICMP first (requires root/capabilities)
	conn, err := net.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		// Fallback: use UDP "ping" trick (doesn't require root, but less reliable)
		return icmpPingsFallback(ctx, ips, timeout)
	}
	defer conn.Close()

	var wg sync.WaitGroup
	var mu sync.Mutex
	sem := make(chan struct{}, 32)

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil || ip.To4() == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return responded
		default:
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(target string, ip net.IP) {
			defer wg.Done()
			defer func() { <-sem }()

			// Build ICMP echo request
			seq := uint16(time.Now().UnixNano() & 0xFFFF)
			msg := []byte{
				8, 0, 0, 0, // Type=8 (Echo), Code=0, Checksum placeholder
				0, 1, // Identifier
				byte(seq >> 8), byte(seq & 0xFF), // Sequence
			}
			// Calculate checksum
			cs := icmpChecksum(msg)
			msg[2] = byte(cs >> 8)
			msg[3] = byte(cs & 0xFF)

			dst := &net.IPAddr{IP: ip}
			_ = conn.SetDeadline(time.Now().Add(timeout))
			_, err := conn.WriteTo(msg, dst)
			if err != nil {
				return
			}

			buf := make([]byte, 1500)
			_, _, err = conn.ReadFrom(buf)
			if err == nil {
				mu.Lock()
				responded[target] = true
				mu.Unlock()
			}
		}(ipStr, ip.To4())
	}
	wg.Wait()
	return responded
}

func icmpChecksum(data []byte) uint16 {
	var sum uint32
	length := len(data)
	index := 0
	for length > 1 {
		sum += uint32(data[index])<<8 + uint32(data[index+1])
		index += 2
		length -= 2
	}
	if length > 0 {
		sum += uint32(data[index]) << 8
	}
	sum = (sum >> 16) + (sum & 0xFFFF)
	sum += sum >> 16
	return ^uint16(sum)
}

func icmpPingsFallback(ctx context.Context, ips []string, timeout time.Duration) map[string]bool {
	responded := make(map[string]bool, len(ips))
	var wg sync.WaitGroup
	var mu sync.Mutex
	sem := make(chan struct{}, 32)

	for _, ipStr := range ips {
		select {
		case <-ctx.Done():
			return responded
		default:
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(target string) {
			defer wg.Done()
			defer func() { <-sem }()
			d := net.Dialer{Timeout: timeout}
			conn, err := d.DialContext(ctx, "ip4:icmp", target)
			if err == nil {
				conn.Close()
				mu.Lock()
				responded[target] = true
				mu.Unlock()
			}
		}(ipStr)
	}
	wg.Wait()
	return responded
}

// readDHCPLeases reads dnsmasq-style DHCP lease files and returns MAC -> hostname mappings.
func readDHCPLeases() map[string]string {
	hosts := make(map[string]string)
	paths := []string{
		"/var/lib/misc/dnsmasq.leases",
		"/tmp/dhcp.leases",
		"/var/lib/dnsmasq/dnsmasq.leases",
		"/tmp/dnsmasq.leases",
		"/var/lib/NetworkManager/dnsmasq-*.leases",
	}
	for _, pattern := range paths {
		matches, _ := filepath.Glob(pattern)
		for _, path := range matches {
			readDHCPLeaseFile(path, hosts)
		}
	}
	return hosts
}

func readDHCPLeaseFile(path string, out map[string]string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		// Format: timestamp MAC IP hostname client-id
		mac := normalizeMAC(fields[1])
		hostname := fields[3]
		if mac == "" || hostname == "" || hostname == "*" {
			continue
		}
		if _, exists := out[mac]; !exists {
			out[mac] = hostname
		}
	}
}

// readIPv6Neighbors reads IPv6 NDP entries from netlink and returns MAC -> IPv6 list mapping.
func readIPv6Neighbors() map[string][]string {
	result := make(map[string][]string)
	conn, err := rtnetlink.Dial(nil)
	if err != nil {
		return result
	}
	defer conn.Close()

	msgs, err := conn.Neigh.List()
	if err != nil {
		return result
	}

	ifaces, _ := net.Interfaces()
	monitored := monitoredLANInterfaceSet()
	ifaceNames := make(map[int]string, len(ifaces))
	for _, iface := range ifaces {
		ifaceNames[iface.Index] = iface.Name
	}

	for _, msg := range msgs {
		if msg.Family != unix.AF_INET6 || msg.Attributes == nil {
			continue
		}
		ifaceName := ifaceNames[int(msg.Index)]
		if !isMonitoredLANInterface(monitored, ifaceName) {
			continue
		}
		ip := msg.Attributes.Address
		if ip == nil {
			continue
		}
		// Only track link-local and ULA addresses
		if !isTrackableIPv6(ip) {
			continue
		}
		mac := strings.ToLower(msg.Attributes.LLAddress.String())
		if mac == "" || mac == "00:00:00:00:00:00" {
			continue
		}
		ipStr := ip.String()
		// Remove zone identifier if present
		if idx := strings.Index(ipStr, "%"); idx >= 0 {
			ipStr = ipStr[:idx]
		}
		result[mac] = appendUnique(result[mac], ipStr)
	}
	return result
}

func isTrackableIPv6(ip net.IP) bool {
	if ip.To4() != nil {
		return false
	}
	// Link-local: fe80::/10
	if ip[0] == 0xfe && (ip[1]&0xC0) == 0x80 {
		return true
	}
	// ULA: fc00::/7
	if (ip[0] & 0xFE) == 0xfc {
		return true
	}
	// Global unicast (but not multicast)
	if ip[0]&0xE0 == 0x20 {
		return true
	}
	return false
}

func appendUnique(slice []string, item string) []string {
	for _, s := range slice {
		if s == item {
			return slice
		}
	}
	return append(slice, item)
}

// resolveMDNSHostname attempts to resolve a .local hostname via mDNS.
func resolveMDNSHostname(ctx context.Context, ip string) string {
	// Try to resolve the IP via reverse mDNS
	// This is a lightweight approach - just try to resolve common patterns
	ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()

	// Try reverse DNS first (faster, already implemented)
	names, err := net.DefaultResolver.LookupAddr(ctx, ip)
	if err == nil && len(names) > 0 {
		name := strings.TrimSuffix(names[0], ".")
		if !strings.HasSuffix(name, ".in-addr.arpa") {
			return name
		}
	}

	// mDNS resolution would require a multicast DNS library
	// For now, we rely on reverse DNS and DHCP leases
	return ""
}

// warmNeighborsWithICMP combines UDP warm-up with ICMP ping for better detection.
func warmNeighborsWithICMP(ctx context.Context, cidr string) int {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil || ip.To4() == nil {
		return 0
	}
	ips := hostsInIPv4Net(ipNet)
	if len(ips) == 0 || len(ips) > 512 {
		return 0
	}

	ipStrings := make([]string, len(ips))
	for i, ip := range ips {
		ipStrings[i] = ip.String()
	}

	// UDP warm-up first (populates ARP table)
	udpCount := warmLANNeighbors(ctx, cidr)

	// ICMP ping as supplementary (catches devices that don't respond to UDP)
	icmpResponded := icmpPings(ctx, ipStrings, 250*time.Millisecond)

	// Brief pause to let ARP table update
	time.Sleep(100 * time.Millisecond)

	return udpCount + len(icmpResponded)
}
