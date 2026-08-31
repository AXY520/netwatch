package probe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	appFirewallForwardChainA = "NETWATCH-FWD-A"
	appFirewallForwardChainB = "NETWATCH-FWD-B"
	appFirewallOutputChainA  = "NETWATCH-OUT-A"
	appFirewallOutputChainB  = "NETWATCH-OUT-B"
	appFirewallCommandLimit  = 5 * time.Second
)

var hostFirewallIPv4Paths = []string{"/usr/sbin/iptables", "/usr/bin/iptables", "/sbin/iptables", "/bin/iptables"}
var hostFirewallIPv6Paths = []string{"/usr/sbin/ip6tables", "/usr/bin/ip6tables", "/sbin/ip6tables", "/bin/ip6tables"}

var runAppFirewallCommand = func(ctx context.Context, ipv6 bool, args ...string) (string, error) {
	findBinPaths()
	if nsenterPath == "" {
		return "", errors.New("nsenter command is unavailable")
	}
	binary, ok := hostFirewallPath(ipv6)
	if !ok {
		if ipv6 {
			return "", errors.New("host ip6tables command is unavailable")
		}
		return "", errors.New("host iptables command is unavailable")
	}
	command := hostFirewallCommandArgs(binary, args...)
	cmd := exec.CommandContext(ctx, nsenterPath, command...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func hostFirewallCommandArgs(binary string, args ...string) []string {
	// xt_cgroup --path is resolved relative to the caller's cgroup namespace.
	// Joining only mount/network namespaces leaves Netwatch in its container
	// cgroup namespace, where Lazycat's host application slices don't exist and
	// the kernel rejects RULE_APPEND with EINVAL. Join PID 1's cgroup namespace
	// as well so both validation and packet matching use the host cgroup tree.
	command := []string{"-t", "1", "-m", "-n", "-C", "-r", "--", binary, "-w", "5"}
	return append(command, args...)
}

type appInternetTargetRuntime struct {
	Blocked    bool
	InSync     bool
	Diagnostic string
	CheckedAt  string
}

type appInternetController struct {
	mu            sync.RWMutex
	runtime       map[string]appInternetTargetRuntime
	signature     string
	lastApplied   time.Time
	nextRetry     time.Time
	lastErr       error
	lastV4        appFirewallRuleSet
	lastV6        appFirewallRuleSet
	legacyTargets string
}

type appFirewallRuleSet struct {
	forward [][]string
	output  [][]string
}

func newAppInternetController() *appInternetController {
	return &appInternetController{runtime: make(map[string]appInternetTargetRuntime)}
}

func hostFirewallPath(ipv6 bool) (string, bool) {
	candidates := hostFirewallIPv4Paths
	if ipv6 {
		candidates = hostFirewallIPv6Paths
	}
	return hostFirewallPathAt("/proc/1/root", candidates)
}

func hostFirewallPathAt(root string, candidates []string) (string, bool) {
	for _, candidate := range candidates {
		path := filepath.Join(root, strings.TrimPrefix(candidate, "/"))
		// Host iptables commonly points to an absolute /etc/alternatives link.
		// os.Stat follows that link in Netwatch's root and reports a false
		// negative, while nsenter -r resolves it correctly in the host root.
		if info, err := os.Lstat(path); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func hostIPv6Enabled() bool {
	body, err := os.ReadFile("/proc/net/if_inet6")
	return err == nil && strings.TrimSpace(string(body)) != ""
}

func appFirewallAvailable() bool {
	if !nsenterAvailable() {
		return false
	}
	if _, ok := hostFirewallPath(false); !ok {
		return false
	}
	if hostIPv6Enabled() {
		_, ok := hostFirewallPath(true)
		return ok
	}
	return true
}

func appNetworkTargets(items []AppBridgeStats) []AppNetworkTarget {
	byID := make(map[string]AppNetworkTarget)
	for _, item := range items {
		target, ok := appNetworkTargetFromStats(item)
		if !ok {
			continue
		}
		byID[target.ID] = target
	}
	targets := make([]AppNetworkTarget, 0, len(byID))
	for _, target := range byID {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Kind != targets[j].Kind {
			return targets[i].Kind < targets[j].Kind
		}
		return targets[i].ID < targets[j].ID
	})
	return targets
}

func (c *appInternetController) status(targetID string) appInternetTargetRuntime {
	if c == nil {
		return appInternetTargetRuntime{Diagnostic: "应用外网控制器不可用"}
	}
	c.mu.RLock()
	status, ok := c.runtime[targetID]
	c.mu.RUnlock()
	if !ok {
		return appInternetTargetRuntime{Diagnostic: "等待核验宿主机防火墙规则"}
	}
	return status
}

func (c *appInternetController) reconcile(ctx context.Context, activeTargets []AppNetworkTarget, desiredBlocked map[string]string) error {
	if c == nil {
		return errors.New("application internet controller is unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if !appFirewallAvailable() {
		err := errors.New("宿主机 iptables/ip6tables 或 nsenter 不可用")
		c.markFailedLocked(activeTargets, err)
		return err
	}
	v4, v6, err := buildAppFirewallRules(activeTargets, desiredBlocked)
	if err != nil {
		c.markFailedLocked(activeTargets, err)
		return err
	}
	signature := fmt.Sprintf("v4=%q|v6=%q", v4, v6)
	nowTime := time.Now()
	if signature == c.signature {
		if c.lastErr != nil && nowTime.Before(c.nextRetry) {
			return c.lastErr
		}
		if c.lastErr == nil && nowTime.Sub(c.lastApplied) < 30*time.Second {
			return nil
		}
	}
	if err := applyAppFirewallRules(ctx, false, v4); err != nil {
		if !c.lastApplied.IsZero() {
			if rollbackErr := applyAppFirewallRules(ctx, false, c.lastV4); rollbackErr != nil {
				err = fmt.Errorf("%w; rollback IPv4 firewall policy: %v", err, rollbackErr)
			}
		}
		c.markFailedLocked(activeTargets, err)
		c.signature, c.lastErr, c.nextRetry = signature, err, nowTime.Add(10*time.Second)
		return err
	}
	if hostIPv6Enabled() {
		if err := applyAppFirewallRules(ctx, true, v6); err != nil {
			if !c.lastApplied.IsZero() {
				if rollbackErr := applyAppFirewallRules(ctx, false, c.lastV4); rollbackErr != nil {
					err = fmt.Errorf("%w; rollback IPv4 firewall policy: %v", err, rollbackErr)
				}
			}
			c.markFailedLocked(activeTargets, err)
			c.signature, c.lastErr, c.nextRetry = signature, err, nowTime.Add(10*time.Second)
			return err
		}
	}
	legacyTargets := appInternetTargetSetSignature(activeTargets)
	if legacyTargets != c.legacyTargets {
		if err := cleanupLegacyAppInternetRules(ctx, activeTargets, desiredBlocked); err != nil {
			if !c.lastApplied.IsZero() {
				if rollbackErr := applyAppFirewallRules(ctx, false, c.lastV4); rollbackErr != nil {
					err = fmt.Errorf("%w; rollback IPv4 firewall policy: %v", err, rollbackErr)
				}
				if hostIPv6Enabled() {
					if rollbackErr := applyAppFirewallRules(ctx, true, c.lastV6); rollbackErr != nil {
						err = fmt.Errorf("%w; rollback IPv6 firewall policy: %v", err, rollbackErr)
					}
				}
			}
			c.markFailedLocked(activeTargets, err)
			c.signature, c.lastErr, c.nextRetry = signature, err, nowTime.Add(10*time.Second)
			return err
		}
		c.legacyTargets = legacyTargets
	}

	now := time.Now().Format(time.DateTime)
	next := make(map[string]appInternetTargetRuntime, len(activeTargets))
	for _, target := range activeTargets {
		next[target.ID] = appInternetTargetRuntime{
			Blocked: desiredBlocked[target.ID] != "", InSync: true, CheckedAt: now,
		}
	}
	c.runtime = next
	c.signature = signature
	c.lastApplied = nowTime
	c.nextRetry = time.Time{}
	c.lastErr = nil
	c.lastV4 = cloneAppFirewallRuleSet(v4)
	c.lastV6 = cloneAppFirewallRuleSet(v6)
	return nil
}

func appInternetTargetSetSignature(targets []AppNetworkTarget) string {
	parts := make([]string, 0, len(targets))
	for _, target := range targets {
		parts = append(parts, string(target.Kind)+"\x00"+target.ID)
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x00")
}

func cloneAppFirewallRuleSet(in appFirewallRuleSet) appFirewallRuleSet {
	cloneRules := func(rules [][]string) [][]string {
		out := make([][]string, len(rules))
		for index := range rules {
			out[index] = append([]string(nil), rules[index]...)
		}
		return out
	}
	return appFirewallRuleSet{forward: cloneRules(in.forward), output: cloneRules(in.output)}
}

func (s *Service) reconcileAppInternetControls(ctx context.Context, items []AppBridgeStats, desiredApps map[string]string) error {
	if s == nil || s.appInternetController == nil || s.containers == nil {
		return errors.New("application internet controller is unavailable")
	}
	targets := appNetworkTargets(items)
	if len(targets) == 0 {
		// A transient Docker/socket failure must not flush a working firewall
		// policy. Wait for the next lifecycle/reconcile pass instead.
		return nil
	}
	legacy := s.containers.snapshotBlocked()
	unsupported := unsupportedAppNetworkPolicies(targets)
	migrated := false
	for _, target := range targets {
		policyID := appNetworkTargetPolicyID(target)
		if legacy[target.ID] == "" || policyID == "" || unsupported[policyID] {
			continue
		}
		if desiredApps[policyID] == "" {
			desiredApps[policyID] = "internet"
		}
		delete(legacy, target.ID)
		migrated = true
	}
	blockedTargets := make(map[string]string)
	for _, target := range targets {
		policyID := appNetworkTargetPolicyID(target)
		if desiredApps[policyID] != "" && !unsupported[policyID] {
			blockedTargets[target.ID] = "internet"
		}
	}
	if err := s.appInternetController.reconcile(ctx, targets, blockedTargets); err != nil {
		return err
	}
	if migrated {
		s.containers.replaceBlockedApps(desiredApps)
		s.containers.replaceBlocked(legacy)
		s.saveBlockedBridges()
	}
	return nil
}

func (c *appInternetController) markFailedLocked(targets []AppNetworkTarget, err error) {
	now := time.Now().Format(time.DateTime)
	for _, target := range targets {
		status := c.runtime[target.ID]
		status.InSync = false
		status.Diagnostic = err.Error()
		status.CheckedAt = now
		c.runtime[target.ID] = status
	}
}

func buildAppFirewallRules(targets []AppNetworkTarget, desiredBlocked map[string]string) (appFirewallRuleSet, appFirewallRuleSet, error) {
	var v4 appFirewallRuleSet
	var v6 appFirewallRuleSet
	for _, target := range targets {
		if desiredBlocked[target.ID] == "" {
			continue
		}
		switch target.Kind {
		case AppNetworkTargetBridge:
			if target.Interface == "" || !strings.HasPrefix(target.Interface, lzcBridgePrefix) {
				return v4, v6, fmt.Errorf("invalid Bridge internet-control target %q", target.ID)
			}
			for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
				v4.forward = append(v4.forward, []string{"-i", target.Interface, "-d", cidr, "-j", "RETURN"})
			}
			v4.forward = append(v4.forward, []string{"-i", target.Interface, "-j", "DROP"})
			for _, cidr := range []string{"fc00::/7", "fe80::/10"} {
				v6.forward = append(v6.forward, []string{"-i", target.Interface, "-d", cidr, "-j", "RETURN"})
			}
			v6.forward = append(v6.forward, []string{"-i", target.Interface, "-j", "DROP"})
		case AppNetworkTargetCgroup:
			path := strings.TrimSpace(target.CgroupPath)
			if path != "" {
				path = strings.TrimPrefix(filepathDir(path), "/")
			}
			if path == "" {
				resolved, err := hostNetworkControlPath(target.ID)
				if err != nil {
					return v4, v6, err
				}
				path = resolved
			}
			for _, matchPath := range hostFirewallCgroupPaths(path) {
				for _, cidr := range hostNetworkPrivateCIDRs {
					v4.output = append(v4.output, hostIptablesRuleArgs(matchPath, cidr, "RETURN"))
				}
				v4.output = append(v4.output, hostIptablesRuleArgs(matchPath, "", "DROP"))
				for _, cidr := range hostNetworkPrivateCIDRs6 {
					v6.output = append(v6.output, hostIptablesRuleArgs(matchPath, cidr, "RETURN"))
				}
				v6.output = append(v6.output, hostIptablesRuleArgs(matchPath, "", "DROP"))
			}
		default:
			return v4, v6, fmt.Errorf("unsupported internet-control target kind %q", target.Kind)
		}
	}
	return v4, v6, nil
}

func applyAppFirewallRules(ctx context.Context, ipv6 bool, rules appFirewallRuleSet) error {
	currentA := firewallHookExists(ctx, ipv6, "FORWARD", appFirewallForwardChainA) || firewallHookExists(ctx, ipv6, "OUTPUT", appFirewallOutputChainA)
	currentB := firewallHookExists(ctx, ipv6, "FORWARD", appFirewallForwardChainB) || firewallHookExists(ctx, ipv6, "OUTPUT", appFirewallOutputChainB)
	forwardStaging, outputStaging := appFirewallForwardChainA, appFirewallOutputChainA
	forwardOld, outputOld := appFirewallForwardChainB, appFirewallOutputChainB
	if currentA && !currentB {
		forwardStaging, outputStaging = appFirewallForwardChainB, appFirewallOutputChainB
		forwardOld, outputOld = appFirewallForwardChainA, appFirewallOutputChainA
	}
	for _, chain := range []string{appFirewallForwardChainA, appFirewallForwardChainB, appFirewallOutputChainA, appFirewallOutputChainB} {
		if err := ensureFirewallChain(ctx, ipv6, chain); err != nil {
			return err
		}
	}
	if err := flushAndPopulateFirewallChain(ctx, ipv6, forwardStaging, rules.forward); err != nil {
		return err
	}
	if err := flushAndPopulateFirewallChain(ctx, ipv6, outputStaging, rules.output); err != nil {
		return err
	}
	if _, err := firewallCommand(ctx, ipv6, "-I", "FORWARD", "1", "-j", forwardStaging); err != nil {
		return fmt.Errorf("install Netwatch FORWARD hook: %w", err)
	}
	if _, err := firewallCommand(ctx, ipv6, "-I", "OUTPUT", "1", "-j", outputStaging); err != nil {
		_ = deleteFirewallRuleAll(ctx, ipv6, "FORWARD", []string{"-j", forwardStaging})
		return fmt.Errorf("install Netwatch OUTPUT hook: %w", err)
	}
	if err := deleteFirewallRuleAll(ctx, ipv6, "FORWARD", []string{"-j", forwardOld}); err != nil {
		return err
	}
	if err := deleteFirewallRuleAll(ctx, ipv6, "OUTPUT", []string{"-j", outputOld}); err != nil {
		return err
	}
	if err := normalizeFirewallHook(ctx, ipv6, "FORWARD", forwardStaging); err != nil {
		return err
	}
	if err := normalizeFirewallHook(ctx, ipv6, "OUTPUT", outputStaging); err != nil {
		return err
	}
	_, _ = firewallCommand(ctx, ipv6, "-F", forwardOld)
	_, _ = firewallCommand(ctx, ipv6, "-F", outputOld)
	return nil
}

func firewallCommand(parent context.Context, ipv6 bool, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, appFirewallCommandLimit)
	defer cancel()
	out, err := runAppFirewallCommand(ctx, ipv6, args...)
	if err != nil {
		if out != "" {
			return out, fmt.Errorf("%s", out)
		}
		return out, err
	}
	return out, nil
}

func ensureFirewallChain(ctx context.Context, ipv6 bool, chain string) error {
	if _, err := firewallCommand(ctx, ipv6, "-S", chain); err == nil {
		return nil
	}
	if _, err := firewallCommand(ctx, ipv6, "-N", chain); err != nil {
		return fmt.Errorf("create firewall chain %s: %w", chain, err)
	}
	return nil
}

func flushAndPopulateFirewallChain(ctx context.Context, ipv6 bool, chain string, rules [][]string) error {
	if _, err := firewallCommand(ctx, ipv6, "-F", chain); err != nil {
		return fmt.Errorf("flush firewall chain %s: %w", chain, err)
	}
	for _, rule := range rules {
		args := append([]string{"-A", chain}, rule...)
		if _, err := firewallCommand(ctx, ipv6, args...); err != nil {
			_, _ = firewallCommand(ctx, ipv6, "-F", chain)
			return fmt.Errorf("populate firewall chain %s: %w", chain, err)
		}
	}
	return nil
}

func firewallHookExists(ctx context.Context, ipv6 bool, builtin, chain string) bool {
	_, err := firewallCommand(ctx, ipv6, "-C", builtin, "-j", chain)
	return err == nil
}

func normalizeFirewallHook(ctx context.Context, ipv6 bool, builtin, chain string) error {
	out, err := firewallCommand(ctx, ipv6, "-S", builtin)
	if err != nil {
		return err
	}
	needle := "-A " + builtin + " -j " + chain
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == needle {
			count++
		}
	}
	for count > 1 {
		if _, err := firewallCommand(ctx, ipv6, "-D", builtin, "-j", chain); err != nil {
			return err
		}
		count--
	}
	return nil
}

func deleteFirewallRuleAll(ctx context.Context, ipv6 bool, chain string, rule []string) error {
	for attempts := 0; attempts < 64; attempts++ {
		checkArgs := append([]string{"-C", chain}, rule...)
		if _, err := firewallCommand(ctx, ipv6, checkArgs...); err != nil {
			return nil
		}
		deleteArgs := append([]string{"-D", chain}, rule...)
		if _, err := firewallCommand(ctx, ipv6, deleteArgs...); err != nil {
			return fmt.Errorf("delete firewall rule from %s: %w", chain, err)
		}
	}
	return fmt.Errorf("too many duplicate firewall rules in %s", chain)
}

func cleanupLegacyAppInternetRules(ctx context.Context, activeTargets []AppNetworkTarget, desiredBlocked map[string]string) error {
	targets := make(map[string]AppNetworkTarget, len(activeTargets)+len(desiredBlocked))
	for _, target := range activeTargets {
		targets[target.ID] = target
	}
	for targetID := range desiredBlocked {
		if _, exists := targets[targetID]; exists {
			continue
		}
		if appID, ok := hostAppIDFromTarget(targetID); ok {
			targets[targetID] = AppNetworkTarget{ID: targetID, Kind: AppNetworkTargetCgroup, AppID: appID}
		} else if strings.HasPrefix(targetID, lzcBridgePrefix) {
			targets[targetID] = AppNetworkTarget{ID: targetID, Kind: AppNetworkTargetBridge, Interface: targetID}
		}
	}
	for _, target := range targets {
		switch target.Kind {
		case AppNetworkTargetBridge:
			if err := removeLegacyBridgeInternetRules(ctx, target.Interface); err != nil {
				return err
			}
		case AppNetworkTargetCgroup:
			path := strings.TrimSpace(target.CgroupPath)
			if path != "" {
				path = strings.TrimPrefix(filepathDir(path), "/")
			} else {
				resolved, err := hostNetworkControlPath(target.ID)
				if err != nil {
					continue
				}
				path = resolved
			}
			if err := removeLegacyHostInternetRules(ctx, path); err != nil {
				return err
			}
		}
	}
	return nil
}

func removeLegacyBridgeInternetRules(ctx context.Context, bridge string) error {
	if bridge == "" {
		return nil
	}
	for _, ipv6 := range []bool{false, true} {
		if ipv6 && !hostIPv6Enabled() {
			continue
		}
		cidrs := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
		if ipv6 {
			cidrs = []string{"fc00::/7", "fe80::/10"}
		}
		if err := deleteFirewallRuleAll(ctx, ipv6, "FORWARD", []string{"-i", bridge, "-j", "DROP"}); err != nil {
			return err
		}
		for _, cidr := range cidrs {
			if err := deleteFirewallRuleAll(ctx, ipv6, "FORWARD", []string{"-i", bridge, "-d", cidr, "-j", "ACCEPT"}); err != nil {
				return err
			}
		}
	}
	return nil
}

func removeLegacyHostInternetRules(ctx context.Context, path string) error {
	for _, ipv6 := range []bool{false, true} {
		if ipv6 && !hostIPv6Enabled() {
			continue
		}
		cidrs := hostNetworkPrivateCIDRs
		if ipv6 {
			cidrs = hostNetworkPrivateCIDRs6
		}
		for _, matchPath := range hostFirewallCgroupPaths(path) {
			if err := deleteFirewallRuleAll(ctx, ipv6, "OUTPUT", hostIptablesRuleArgs(matchPath, "", "DROP")); err != nil {
				return err
			}
			for _, cidr := range cidrs {
				if err := deleteFirewallRuleAll(ctx, ipv6, "OUTPUT", hostIptablesRuleArgs(matchPath, cidr, "ACCEPT")); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
