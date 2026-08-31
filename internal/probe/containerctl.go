package probe

import (
	"context"
	"os"
	"os/exec"
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
