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

// icmpPings sends ICMP echo requests and returns hosts that replied.
// Uses a single PacketConn with separate write/read phases. The previous
// concurrent SetDeadline+ReadFrom design raced on the shared conn and could
// hang indefinitely, which surfaces as a frontend "scan timeout" with no
// backend error log.
func icmpPings(ctx context.Context, ips []string, timeout time.Duration) map[string]bool {
	responded := make(map[string]bool, len(ips))
	if len(ips) == 0 {
		return responded
	}
	if timeout <= 0 {
		timeout = 250 * time.Millisecond
	}

	conn, err := net.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return icmpPingsFallback(ctx, ips, timeout)
	}
	defer conn.Close()

	// Bound the whole ICMP wave so one bad path cannot stall LAN scan.
	budget := timeout
	if n := len(ips); n > 64 {
		// Rough upper bound: ~4 concurrent waves worth of timeout, capped.
		wave := time.Duration(n/64+1) * timeout
		if wave > 2*time.Second {
			wave = 2 * time.Second
		}
		if wave > budget {
			budget = wave
		}
	}
	if budget > 3*time.Second {
		budget = 3 * time.Second
	}
	deadline := time.Now().Add(budget)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)

	id := uint16(time.Now().UnixNano() & 0xFFFF)
	// Phase 1: blast echo requests (writes only).
	for i, ipStr := range ips {
		select {
		case <-ctx.Done():
			return responded
		default:
		}
		ip := net.ParseIP(ipStr)
		if ip == nil || ip.To4() == nil {
			continue
		}
		seq := uint16(i + 1)
		msg := []byte{
			8, 0, 0, 0,
			byte(id >> 8), byte(id),
			byte(seq >> 8), byte(seq),
		}
		cs := icmpChecksum(msg)
		msg[2] = byte(cs >> 8)
		msg[3] = byte(cs & 0xFF)
		_, _ = conn.WriteTo(msg, &net.IPAddr{IP: ip.To4()})
	}

	// Phase 2: drain replies until budget expires.
	buf := make([]byte, 1500)
	for {
		select {
		case <-ctx.Done():
			return responded
		default:
		}
		if time.Now().After(deadline) {
			break
		}
		_ = conn.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if time.Now().After(deadline) {
					break
				}
				continue
			}
			break
		}
		if n < 8 || addr == nil {
			continue
		}
		// IPv4 raw ICMP may include IP header on some platforms; locate ICMP echo reply.
		off := 0
		if n >= 28 && buf[0]>>4 == 4 {
			off = int(buf[0]&0x0f) * 4
			if off >= n {
				continue
			}
		}
		icmp := buf[off:n]
		if len(icmp) < 8 || icmp[0] != 0 { // type 0 = echo reply
			continue
		}
		host := addr.String()
		if hostIP, _, err := net.SplitHostPort(host); err == nil {
			host = hostIP
		}
		responded[host] = true
	}
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
	// Without raw ICMP capability we only warm the ARP table via UDP.
	// Actual reachability comes from neighbor table reads after warm-up.
	responded := make(map[string]bool)
	if timeout <= 0 {
		timeout = 120 * time.Millisecond
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 128)

	for _, ipStr := range ips {
		select {
		case <-ctx.Done():
			wg.Wait()
			return responded
		default:
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(target string) {
			defer wg.Done()
			defer func() { <-sem }()
			d := net.Dialer{Timeout: timeout}
			conn, err := d.DialContext(ctx, "udp4", net.JoinHostPort(target, "9"))
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte{0})
			_ = conn.Close()
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

	// Prefer pure-Go resolver so short deadlines are honored.
	resolver := &net.Resolver{PreferGo: true}
	names, err := resolver.LookupAddr(ctx, ip)
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

// warmNeighborsWithICMP combines UDP warm-up with a short ICMP wave.
// Total time is hard-capped so multi-NIC environments stay interactive.
func warmNeighborsWithICMP(ctx context.Context, cidr string) int {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil || ip.To4() == nil {
		return 0
	}
	ips := hostsInIPv4Net(ipNet)
	if len(ips) == 0 || len(ips) > 512 {
		return 0
	}

	// Per-network budget; keep total scan snappy even with 2-3 LANs.
	netCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()

	udpCount := warmLANNeighbors(netCtx, cidr)

	// ICMP is optional enrichment; skip if UDP already used most of the budget.
	icmpCount := 0
	if netCtx.Err() == nil {
		ipStrings := make([]string, len(ips))
		for i, host := range ips {
			ipStrings[i] = host.String()
		}
		icmpCount = len(icmpPings(netCtx, ipStrings, 150*time.Millisecond))
	}

	select {
	case <-netCtx.Done():
	case <-time.After(50 * time.Millisecond):
	}

	return udpCount + icmpCount
}
