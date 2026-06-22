package probe

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"netwatch/internal/logger"
)

var (
	binPathsOnce  sync.Once
	nsenterPath   string
	ipPath        string
	iptablesPath  string
	ip6tablesPath string
)

func findBinPaths() {
	binPathsOnce.Do(func() {
		for _, p := range []string{"/usr/bin/nsenter", "/usr/sbin/nsenter", "/sbin/nsenter"} {
			if _, err := os.Stat(p); err == nil {
				nsenterPath = p
				break
			}
		}
		if nsenterPath == "" {
			nsenterPath, _ = exec.LookPath("nsenter")
		}
		for _, p := range []string{"/usr/sbin/ip", "/sbin/ip", "/usr/bin/ip"} {
			if _, err := os.Stat(p); err == nil {
				ipPath = p
				break
			}
		}
		if ipPath == "" {
			ipPath, _ = exec.LookPath("ip")
		}
		for _, p := range []string{"/usr/sbin/iptables", "/sbin/iptables", "/usr/bin/iptables"} {
			if _, err := os.Stat(p); err == nil {
				iptablesPath = p
				break
			}
		}
		if iptablesPath == "" {
			iptablesPath, _ = exec.LookPath("iptables")
		}
		for _, p := range []string{"/usr/sbin/ip6tables", "/sbin/ip6tables", "/usr/bin/ip6tables"} {
			if _, err := os.Stat(p); err == nil {
				ip6tablesPath = p
				break
			}
		}
		if ip6tablesPath == "" {
			ip6tablesPath, _ = exec.LookPath("ip6tables")
		}
		logger.Info("containerctl: nsenter=%q ip=%q iptables=%q ip6tables=%q",
			nsenterPath, ipPath, iptablesPath, ip6tablesPath)
	})
}

func nsenterAvailable() bool {
	findBinPaths()
	return nsenterPath != ""
}

func ipAvailable() bool {
	findBinPaths()
	return ipPath != ""
}

func iptablesAvailable() bool {
	findBinPaths()
	return iptablesPath != ""
}

func execCmd(bin string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// --- Bridge-level operations (host network namespace) ---

func bridgeBlockAll(bridge string) error {
	logger.Info("bridge block all: %s", bridge)
	_, err := execCmd(ipPath, 5*time.Second, "link", "set", bridge, "down")
	return err
}

func bridgeUnblockAll(bridge string) error {
	logger.Info("bridge unblock all: %s", bridge)
	_, err := execCmd(ipPath, 5*time.Second, "link", "set", bridge, "up")
	return err
}

// bridgeBlockInternet adds iptables rules to DROP forwarded traffic from the
// bridge to non-LAN destinations, preserving LAN access.
// Uses ACCEPT for LAN ranges first, then DROP for everything else.
func bridgeBlockInternet(bridge string) error {
	logger.Info("bridge block internet: %s", bridge)
	if !iptablesAvailable() {
		return fmt.Errorf("iptables not available")
	}
	// Insert DROP first, then ACCEPT rules before it (each -I goes to position 1)
	if _, err := execCmd(iptablesPath, 3*time.Second,
		"-I", "FORWARD", "-i", bridge, "-j", "DROP",
	); err != nil {
		return fmt.Errorf("iptables drop: %w", err)
	}
	for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		if _, err := execCmd(iptablesPath, 3*time.Second,
			"-I", "FORWARD", "-i", bridge, "-d", cidr, "-j", "ACCEPT",
		); err != nil {
			return fmt.Errorf("iptables accept %s: %w", cidr, err)
		}
	}
	// IPv6: drop first, then accept ULA + link-local
	if ip6tablesPath != "" {
		if _, err := execCmd(ip6tablesPath, 3*time.Second,
			"-I", "FORWARD", "-i", bridge, "-j", "DROP",
		); err != nil {
			logger.Warn("ip6tables drop: %v", err)
		}
		for _, cidr := range []string{"fc00::/7", "fe80::/10"} {
			if _, err := execCmd(ip6tablesPath, 3*time.Second,
				"-I", "FORWARD", "-i", bridge, "-d", cidr, "-j", "ACCEPT",
			); err != nil {
				logger.Warn("ip6tables accept %s: %v", cidr, err)
			}
		}
	}
	return nil
}

// bridgeUnblockInternet removes the internet-blocking iptables rules for a bridge.
func bridgeUnblockInternet(bridge string) error {
	logger.Info("bridge unblock internet: %s", bridge)
	if !iptablesAvailable() {
		return nil
	}
	// Remove DROP rule first, then ACCEPT rules
	for {
		if _, err := execCmd(iptablesPath, 3*time.Second,
			"-D", "FORWARD", "-i", bridge, "-j", "DROP",
		); err != nil {
			break
		}
	}
	for _, cidr := range []string{"192.168.0.0/16", "172.16.0.0/12", "10.0.0.0/8"} {
		for {
			if _, err := execCmd(iptablesPath, 3*time.Second,
				"-D", "FORWARD", "-i", bridge, "-d", cidr, "-j", "ACCEPT",
			); err != nil {
				break
			}
		}
	}
	if ip6tablesPath != "" {
		for {
			if _, err := execCmd(ip6tablesPath, 3*time.Second,
				"-D", "FORWARD", "-i", bridge, "-j", "DROP",
			); err != nil {
				break
			}
		}
		for _, cidr := range []string{"fe80::/10", "fc00::/7"} {
			for {
				if _, err := execCmd(ip6tablesPath, 3*time.Second,
					"-D", "FORWARD", "-i", bridge, "-d", cidr, "-j", "ACCEPT",
				); err != nil {
					break
				}
			}
		}
	}
	return nil
}

// --- Container-level nsenter helpers (fallback for non-Lazycat envs) ---

func containerDefaultRoutes(pid int) map[string]string {
	routes := map[string]string{}
	f, err := os.Open(path.Join("/proc", strconv.Itoa(pid), "net", "route"))
	if err != nil {
		return routes
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "Iface\t") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		iface := fields[0]
		dstHex := fields[1]
		gwHex := fields[2]
		if dstHex == "00000000" {
			if ip := parseHexIP(gwHex); ip != "" {
				routes[iface] = ip
			}
		}
	}
	return routes
}

func parseHexIP(hex string) string {
	if len(hex) != 8 {
		return ""
	}
	ip := fmt.Sprintf("%d.%d.%d.%d",
		hexToInt(hex[6:8]),
		hexToInt(hex[4:6]),
		hexToInt(hex[2:4]),
		hexToInt(hex[0:2]),
	)
	if net.ParseIP(ip) == nil {
		return ""
	}
	return ip
}

func hexToInt(s string) int {
	n, _ := strconv.ParseInt(s, 16, 32)
	return int(n)
}

func containerHasDefaultRoute(pid int) bool {
	return len(containerDefaultRoutes(pid)) > 0
}

func containerMainIface(pid int) string {
	for iface := range containerDefaultRoutes(pid) {
		return iface
	}
	return "eth0"
}

func containerIfaceUp(pid int, iface string) bool {
	out, err := execNsenter(pid, 3*time.Second, "link", "show", iface)
	if err != nil {
		return false
	}
	return strings.Contains(out, "state UP")
}

func execNsenter(pid int, timeout time.Duration, args ...string) (string, error) {
	if nsenterPath == "" || ipPath == "" {
		return "", fmt.Errorf("nsenter/ip not available")
	}
	pidStr := strconv.Itoa(pid)
	cmdArgs := append([]string{"-t", pidStr, "-n", "--", ipPath}, args...)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, nsenterPath, cmdArgs...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func containerBlockInternet(pid int) (string, string, error) {
	routes := containerDefaultRoutes(pid)
	for iface, gw := range routes {
		_, err := execNsenter(pid, 5*time.Second, "route", "del", "default")
		return gw, iface, err
	}
	return "", "", nil
}

func containerUnblockInternet(pid int, gw, iface string) error {
	if containerHasDefaultRoute(pid) {
		return nil
	}
	_, err := execNsenter(pid, 5*time.Second, "route", "add", "default", "via", gw, "dev", iface)
	return err
}

func containerBlockAll(pid int) error {
	iface := containerMainIface(pid)
	_, err := execNsenter(pid, 5*time.Second, "link", "set", iface, "down")
	return err
}

func containerUnblockAll(pid int) error {
	iface := containerMainIface(pid)
	_, err := execNsenter(pid, 5*time.Second, "link", "set", iface, "up")
	return err
}
