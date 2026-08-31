package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	appProxyFilterForwardChainA = "NETWATCH-PRX-FWD-A"
	appProxyFilterForwardChainB = "NETWATCH-PRX-FWD-B"
	appProxyFilterOutputChainA  = "NETWATCH-PRX-OUT-A"
	appProxyFilterOutputChainB  = "NETWATCH-PRX-OUT-B"
	appProxyNATPreChainA        = "NETWATCH-PRX-PR-A"
	appProxyNATPreChainB        = "NETWATCH-PRX-PR-B"
	appProxyNATOutputChainA     = "NETWATCH-PRX-NOUT-A"
	appProxyNATOutputChainB     = "NETWATCH-PRX-NOUT-B"
)

type appProxyTargetRuntime struct {
	Desired    bool
	Active     bool
	InSync     bool
	Diagnostic string
	CheckedAt  string
}

type appProxyRuleSet struct {
	filterForward [][]string
	filterOutput  [][]string
	natPre        [][]string
	natOutput     [][]string
}

type appProxyController struct {
	mu          sync.RWMutex
	adapters    map[string]*appProxyAdapter
	runtime     map[string]appProxyTargetRuntime
	signature   string
	lastApplied time.Time
	lastErr     error
	lastV4      appProxyRuleSet
	lastV6      appProxyRuleSet
}

func newAppProxyController() *appProxyController {
	return &appProxyController{adapters: make(map[string]*appProxyAdapter), runtime: make(map[string]appProxyTargetRuntime)}
}

func defaultAppProxySettings() AppProxySettings {
	host := "127.0.0.1"
	if route := readDefaultIPv4Route(); route.Interface != "" {
		if _, address, _ := readIfaceIPv4AndGateway(route.Interface); address != "" {
			host = address
		}
	}
	return AppProxySettings{Protocol: "socks5", Host: host, Port: 7890}
}

func normalizeAppProxySettings(input, fallback AppProxySettings) AppProxySettings {
	input.Protocol = strings.ToLower(strings.TrimSpace(input.Protocol))
	input.Host = strings.TrimSpace(input.Host)
	if validateAppProxySettings(input) == nil {
		return input
	}
	fallback.Protocol = strings.ToLower(strings.TrimSpace(fallback.Protocol))
	fallback.Host = strings.TrimSpace(fallback.Host)
	if validateAppProxySettings(fallback) == nil {
		return fallback
	}
	return AppProxySettings{Protocol: "socks5", Host: "127.0.0.1", Port: 7890}
}

func normalizeAppProxyConfigs(input map[string]AppProxySettings, enabled map[string]bool, fallback AppProxySettings) map[string]AppProxySettings {
	out := make(map[string]AppProxySettings, len(input)+len(enabled))
	for appID, config := range input {
		appID = strings.TrimSpace(appID)
		if appID == "" {
			continue
		}
		config.Protocol = strings.ToLower(strings.TrimSpace(config.Protocol))
		config.Host = strings.TrimSpace(config.Host)
		if validateAppProxySettings(config) == nil {
			out[appID] = config
		}
	}
	for appID, active := range enabled {
		if appID = strings.TrimSpace(appID); active && appID != "" {
			if _, ok := out[appID]; !ok {
				out[appID] = fallback
			}
		}
	}
	return out
}

func validateAppProxySettings(config AppProxySettings) error {
	if config.Protocol != "socks5" && config.Protocol != "http" {
		return errors.New("代理类型必须是 SOCKS5 或 HTTP")
	}
	if net.ParseIP(strings.TrimSpace(config.Host)) == nil {
		return errors.New("代理地址必须是有效的 IPv4 或 IPv6 地址")
	}
	if config.Port < 1 || config.Port > 65535 {
		return errors.New("代理端口必须在 1 至 65535 之间")
	}
	return nil
}

func (c *appProxyController) close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	adapters := c.adapters
	c.adapters = make(map[string]*appProxyAdapter)
	c.mu.Unlock()
	for _, adapter := range adapters {
		adapter.close()
	}
}

func (c *appProxyController) status(targetID string) appProxyTargetRuntime {
	if c == nil {
		return appProxyTargetRuntime{Diagnostic: "应用代理控制器不可用"}
	}
	c.mu.RLock()
	status, ok := c.runtime[targetID]
	c.mu.RUnlock()
	if !ok {
		return appProxyTargetRuntime{Diagnostic: "等待核验应用代理规则"}
	}
	return status
}

func (c *appProxyController) reconcile(ctx context.Context, targets []AppNetworkTarget, configs map[string]AppProxySettings, blocked map[string]bool) error {
	if c == nil {
		return errors.New("application proxy controller is unavailable")
	}
	for appID, config := range configs {
		if strings.TrimSpace(appID) == "" {
			return errors.New("application proxy app id is required")
		}
		if err := validateAppProxySettings(config); err != nil {
			return fmt.Errorf("application %s: %w", appID, err)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	nextAdapters := make(map[string]*appProxyAdapter, len(configs))
	listenPorts := make(map[string]int, len(configs))
	allReady := true
	for appID, config := range configs {
		adapter := c.adapters[appID]
		if adapter == nil || !adapter.ready(config) {
			allReady = false
			continue
		}
		nextAdapters[appID] = adapter
		listenPorts[appID] = adapter.port()
	}

	var v4Guard, v6Guard, v4, v6 appProxyRuleSet
	var err error
	if allReady {
		v4Guard, v6Guard, v4, v6, err = buildAppProxyRules(targets, configs, blocked, listenPorts)
		if err != nil {
			c.markFailedLocked(targets, configs, err)
			return err
		}
		signature := appProxySignature(configs, blocked, v4, v6)
		if signature == c.signature && c.lastErr == nil && time.Since(c.lastApplied) < 30*time.Second {
			return nil
		}
	} else {
		v4Guard, v6Guard, _, _, err = buildAppProxyRules(targets, configs, blocked, nil)
		if err != nil {
			c.markFailedLocked(targets, configs, err)
			return err
		}
	}

	ipv6Enabled := hostIPv6Enabled()
	if err = applyAppProxyFilterRules(ctx, false, v4Guard); err == nil && ipv6Enabled {
		err = applyAppProxyFilterRules(ctx, true, v6Guard)
	}
	if err != nil {
		c.lastErr = err
		c.markFailedLocked(targets, configs, err)
		return err
	}

	created := make([]*appProxyAdapter, 0, len(configs))
	for _, appID := range sortedAppProxyConfigKeys(configs) {
		config := configs[appID]
		if adapter := c.adapters[appID]; adapter != nil && adapter.ready(config) {
			nextAdapters[appID] = adapter
			listenPorts[appID] = adapter.port()
			continue
		}
		adapter := newAppProxyAdapter()
		if err = adapter.ensureStarted(config); err != nil {
			for _, item := range created {
				item.close()
			}
			c.lastErr = err
			c.markFailedLocked(targets, configs, err)
			return err
		}
		created = append(created, adapter)
		nextAdapters[appID] = adapter
		listenPorts[appID] = adapter.port()
	}

	v4Guard, v6Guard, v4, v6, err = buildAppProxyRules(targets, configs, blocked, listenPorts)
	if err != nil {
		for _, adapter := range created {
			adapter.close()
		}
		c.lastErr = err
		c.markFailedLocked(targets, configs, err)
		return err
	}
	signature := appProxySignature(configs, blocked, v4, v6)
	now := time.Now()
	if err == nil {
		err = applyAppProxyNATRules(ctx, false, v4)
	}
	if err == nil && ipv6Enabled {
		err = applyAppProxyNATRules(ctx, true, v6)
	}
	if err == nil {
		err = applyAppProxyFilterRules(ctx, false, v4)
	}
	if err == nil && ipv6Enabled {
		err = applyAppProxyFilterRules(ctx, true, v6)
	}
	if err != nil {
		for _, adapter := range created {
			adapter.close()
		}
		if !c.lastApplied.IsZero() {
			var rollbackErrors []error
			if rollbackErr := applyAppProxyNATRules(ctx, false, c.lastV4); rollbackErr != nil {
				rollbackErrors = append(rollbackErrors, rollbackErr)
			}
			if rollbackErr := applyAppProxyFilterRules(ctx, false, c.lastV4); rollbackErr != nil {
				rollbackErrors = append(rollbackErrors, rollbackErr)
			}
			if hostIPv6Enabled() {
				if rollbackErr := applyAppProxyNATRules(ctx, true, c.lastV6); rollbackErr != nil {
					rollbackErrors = append(rollbackErrors, rollbackErr)
				}
				if rollbackErr := applyAppProxyFilterRules(ctx, true, c.lastV6); rollbackErr != nil {
					rollbackErrors = append(rollbackErrors, rollbackErr)
				}
			}
			err = errors.Join(err, errors.Join(rollbackErrors...))
		}
		c.signature, c.lastErr = signature, err
		c.markFailedLocked(targets, configs, err)
		return err
	}

	checkedAt := now.Format(time.DateTime)
	next := make(map[string]appProxyTargetRuntime, len(targets))
	for _, target := range targets {
		_, desired := configs[appNetworkTargetPolicyID(target)]
		next[target.ID] = appProxyTargetRuntime{
			Desired: desired, Active: desired && !blocked[target.ID], InSync: true, CheckedAt: checkedAt,
		}
	}
	previousAdapters := c.adapters
	c.adapters = nextAdapters
	c.runtime = next
	c.signature, c.lastErr, c.lastApplied = signature, nil, now
	c.lastV4, c.lastV6 = cloneAppProxyRuleSet(v4), cloneAppProxyRuleSet(v6)
	for appID, adapter := range previousAdapters {
		if nextAdapters[appID] != adapter {
			adapter.close()
		}
	}
	return nil
}

func (c *appProxyController) markFailedLocked(targets []AppNetworkTarget, configs map[string]AppProxySettings, err error) {
	now := time.Now().Format(time.DateTime)
	for _, target := range targets {
		status := c.runtime[target.ID]
		_, status.Desired = configs[appNetworkTargetPolicyID(target)]
		status.InSync = false
		status.Diagnostic = err.Error()
		status.CheckedAt = now
		c.runtime[target.ID] = status
	}
}

func sortedAppProxyConfigKeys(configs map[string]AppProxySettings) []string {
	keys := make([]string, 0, len(configs))
	for appID := range configs {
		keys = append(keys, appID)
	}
	sort.Strings(keys)
	return keys
}

func appProxySignature(configs map[string]AppProxySettings, blocked map[string]bool, v4, v6 appProxyRuleSet) string {
	entries := make([]string, 0, len(configs))
	for _, appID := range sortedAppProxyConfigKeys(configs) {
		entries = append(entries, fmt.Sprintf("%s=%#v", appID, configs[appID]))
	}
	return fmt.Sprintf("configs=%v|blocked=%v|v4=%q|v6=%q", entries, sortedBoolMapKeys(blocked), v4, v6)
}

func sortedBoolMapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key, enabled := range values {
		if enabled {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func cloneAppProxyRuleSet(in appProxyRuleSet) appProxyRuleSet {
	clone := func(rules [][]string) [][]string {
		out := make([][]string, len(rules))
		for index := range rules {
			out[index] = append([]string(nil), rules[index]...)
		}
		return out
	}
	return appProxyRuleSet{
		filterForward: clone(in.filterForward), filterOutput: clone(in.filterOutput),
		natPre: clone(in.natPre), natOutput: clone(in.natOutput),
	}
}

func buildAppProxyRules(targets []AppNetworkTarget, configs map[string]AppProxySettings, blocked map[string]bool, listenPorts map[string]int) (v4Guard, v6Guard, v4, v6 appProxyRuleSet, err error) {
	for _, target := range targets {
		policyID := appNetworkTargetPolicyID(target)
		config, proxied := configs[policyID]
		if !proxied {
			continue
		}
		listenPort := listenPorts[policyID]
		switch target.Kind {
		case AppNetworkTargetBridge:
			if target.Interface == "" || !strings.HasPrefix(target.Interface, lzcBridgePrefix) {
				return v4Guard, v6Guard, v4, v6, fmt.Errorf("invalid Bridge proxy target %q", target.ID)
			}
			appendAppProxyTargetRules(&v4Guard, &v4, []string{"-i", target.Interface}, false, true, blocked[target.ID], config.Protocol, listenPort)
			appendAppProxyTargetRules(&v6Guard, &v6, []string{"-i", target.Interface}, true, true, blocked[target.ID], config.Protocol, listenPort)
		case AppNetworkTargetCgroup:
			path := strings.TrimSpace(target.CgroupPath)
			if path != "" {
				path = strings.TrimPrefix(filepathDir(path), "/")
			}
			if path == "" {
				resolved, resolveErr := hostNetworkControlPath(target.ID)
				if resolveErr != nil {
					return v4Guard, v6Guard, v4, v6, resolveErr
				}
				path = resolved
			}
			for _, matchPath := range hostFirewallCgroupPaths(path) {
				match := []string{"-m", "cgroup", "--path", matchPath}
				appendAppProxyTargetRules(&v4Guard, &v4, match, false, false, blocked[target.ID], config.Protocol, listenPort)
				appendAppProxyTargetRules(&v6Guard, &v6, match, true, false, blocked[target.ID], config.Protocol, listenPort)
			}
		default:
			return v4Guard, v6Guard, v4, v6, fmt.Errorf("unsupported proxy target kind %q", target.Kind)
		}
	}
	return v4Guard, v6Guard, v4, v6, nil
}

func appendAppProxyTargetRules(guard, desired *appProxyRuleSet, match []string, ipv6, bridge, paused bool, protocol string, listenPort int) {
	privateCIDRs := hostNetworkPrivateCIDRs
	if ipv6 {
		privateCIDRs = hostNetworkPrivateCIDRs6
	}
	guardFilter := &guard.filterOutput
	desiredFilter := &desired.filterOutput
	desiredNAT := &desired.natOutput
	if bridge {
		guardFilter = &guard.filterForward
		desiredFilter = &desired.filterForward
		desiredNAT = &desired.natPre
	}
	for _, cidr := range privateCIDRs {
		*guardFilter = append(*guardFilter, proxyRule(match, "-d", cidr, "-j", "RETURN"))
	}
	*guardFilter = append(*guardFilter, proxyRule(match, "-j", "DROP"))
	if listenPort == 0 {
		return
	}

	if paused {
		for _, cidr := range privateCIDRs {
			*desiredFilter = append(*desiredFilter, proxyRule(match, "-d", cidr, "-j", "RETURN"))
		}
		*desiredFilter = append(*desiredFilter, proxyRule(match, "-j", "DROP"))
		return
	}
	for _, cidr := range privateCIDRs {
		*desiredNAT = append(*desiredNAT, proxyRule(match, "-d", cidr, "-j", "RETURN"))
	}
	*desiredNAT = append(*desiredNAT, proxyRule(match, "-p", "tcp", "-j", "REDIRECT", "--to-ports", strconv.Itoa(listenPort)))
	if protocol == "socks5" {
		*desiredNAT = append(*desiredNAT, proxyRule(match, "-p", "udp", "-j", "REDIRECT", "--to-ports", strconv.Itoa(listenPort)))
	}
	for _, cidr := range privateCIDRs {
		*desiredFilter = append(*desiredFilter, proxyRule(match, "-d", cidr, "-j", "RETURN"))
	}
	// TCP (and SOCKS5 UDP) has already been redirected to a private/local
	// destination before the filter hook. Anything public left here is an
	// unsupported protocol and must fail closed.
	*desiredFilter = append(*desiredFilter, proxyRule(match, "-j", "DROP"))
}

func proxyRule(match []string, suffix ...string) []string {
	return append(append([]string(nil), match...), suffix...)
}

func applyAppProxyFilterRules(ctx context.Context, ipv6 bool, rules appProxyRuleSet) error {
	return applyAppProxyChainPair(ctx, ipv6, "filter", "FORWARD", appProxyFilterForwardChainA, appProxyFilterForwardChainB, rules.filterForward,
		"OUTPUT", appProxyFilterOutputChainA, appProxyFilterOutputChainB, rules.filterOutput)
}

func applyAppProxyNATRules(ctx context.Context, ipv6 bool, rules appProxyRuleSet) error {
	return applyAppProxyChainPair(ctx, ipv6, "nat", "PREROUTING", appProxyNATPreChainA, appProxyNATPreChainB, rules.natPre,
		"OUTPUT", appProxyNATOutputChainA, appProxyNATOutputChainB, rules.natOutput)
}

func applyAppProxyChainPair(ctx context.Context, ipv6 bool, table, firstHook, firstA, firstB string, firstRules [][]string, secondHook, secondA, secondB string, secondRules [][]string) error {
	currentA := proxyFirewallHookExists(ctx, ipv6, table, firstHook, firstA) || proxyFirewallHookExists(ctx, ipv6, table, secondHook, secondA)
	currentB := proxyFirewallHookExists(ctx, ipv6, table, firstHook, firstB) || proxyFirewallHookExists(ctx, ipv6, table, secondHook, secondB)
	firstStaging, secondStaging := firstA, secondA
	firstOld, secondOld := firstB, secondB
	if currentA && !currentB {
		firstStaging, secondStaging = firstB, secondB
		firstOld, secondOld = firstA, secondA
	}
	for _, chain := range []string{firstA, firstB, secondA, secondB} {
		if err := ensureProxyFirewallChain(ctx, ipv6, table, chain); err != nil {
			return err
		}
	}
	if err := flushAndPopulateProxyFirewallChain(ctx, ipv6, table, firstStaging, firstRules); err != nil {
		return err
	}
	if err := flushAndPopulateProxyFirewallChain(ctx, ipv6, table, secondStaging, secondRules); err != nil {
		return err
	}
	if _, err := proxyFirewallCommand(ctx, ipv6, table, "-I", firstHook, "1", "-j", firstStaging); err != nil {
		return fmt.Errorf("install Netwatch %s %s hook: %w", table, firstHook, err)
	}
	if _, err := proxyFirewallCommand(ctx, ipv6, table, "-I", secondHook, "1", "-j", secondStaging); err != nil {
		_ = deleteProxyFirewallRuleAll(ctx, ipv6, table, firstHook, []string{"-j", firstStaging})
		return fmt.Errorf("install Netwatch %s %s hook: %w", table, secondHook, err)
	}
	if err := deleteProxyFirewallRuleAll(ctx, ipv6, table, firstHook, []string{"-j", firstOld}); err != nil {
		return err
	}
	if err := deleteProxyFirewallRuleAll(ctx, ipv6, table, secondHook, []string{"-j", secondOld}); err != nil {
		return err
	}
	if err := normalizeProxyFirewallHook(ctx, ipv6, table, firstHook, firstStaging); err != nil {
		return err
	}
	if err := normalizeProxyFirewallHook(ctx, ipv6, table, secondHook, secondStaging); err != nil {
		return err
	}
	_, _ = proxyFirewallCommand(ctx, ipv6, table, "-F", firstOld)
	_, _ = proxyFirewallCommand(ctx, ipv6, table, "-F", secondOld)
	return nil
}

func proxyFirewallCommand(ctx context.Context, ipv6 bool, table string, args ...string) (string, error) {
	return firewallCommand(ctx, ipv6, append([]string{"-t", table}, args...)...)
}

func ensureProxyFirewallChain(ctx context.Context, ipv6 bool, table, chain string) error {
	if _, err := proxyFirewallCommand(ctx, ipv6, table, "-S", chain); err == nil {
		return nil
	}
	if _, err := proxyFirewallCommand(ctx, ipv6, table, "-N", chain); err != nil {
		return fmt.Errorf("create %s chain %s: %w", table, chain, err)
	}
	return nil
}

func flushAndPopulateProxyFirewallChain(ctx context.Context, ipv6 bool, table, chain string, rules [][]string) error {
	if _, err := proxyFirewallCommand(ctx, ipv6, table, "-F", chain); err != nil {
		return fmt.Errorf("flush %s chain %s: %w", table, chain, err)
	}
	for _, rule := range rules {
		if _, err := proxyFirewallCommand(ctx, ipv6, table, append([]string{"-A", chain}, rule...)...); err != nil {
			_, _ = proxyFirewallCommand(ctx, ipv6, table, "-F", chain)
			return fmt.Errorf("populate %s chain %s: %w", table, chain, err)
		}
	}
	return nil
}

func proxyFirewallHookExists(ctx context.Context, ipv6 bool, table, hook, chain string) bool {
	_, err := proxyFirewallCommand(ctx, ipv6, table, "-C", hook, "-j", chain)
	return err == nil
}

func deleteProxyFirewallRuleAll(ctx context.Context, ipv6 bool, table, chain string, rule []string) error {
	for attempts := 0; attempts < 64; attempts++ {
		if _, err := proxyFirewallCommand(ctx, ipv6, table, append([]string{"-C", chain}, rule...)...); err != nil {
			return nil
		}
		if _, err := proxyFirewallCommand(ctx, ipv6, table, append([]string{"-D", chain}, rule...)...); err != nil {
			return fmt.Errorf("delete %s rule from %s: %w", table, chain, err)
		}
	}
	return fmt.Errorf("too many duplicate %s rules in %s", table, chain)
}

func normalizeProxyFirewallHook(ctx context.Context, ipv6 bool, table, hook, chain string) error {
	out, err := proxyFirewallCommand(ctx, ipv6, table, "-S", hook)
	if err != nil {
		return err
	}
	needle := "-A " + hook + " -j " + chain
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == needle {
			count++
		}
	}
	for count > 1 {
		if _, err := proxyFirewallCommand(ctx, ipv6, table, "-D", hook, "-j", chain); err != nil {
			return err
		}
		count--
	}
	return nil
}

func (s *Service) reconcileAppProxyControls(ctx context.Context, items []AppBridgeStats, proxyApps map[string]bool, proxyConfigs map[string]AppProxySettings, blockedApps map[string]string) error {
	if s == nil || s.appProxyController == nil || s.settings == nil {
		return errors.New("application proxy controller is unavailable")
	}
	targets := appNetworkTargets(items)
	if len(targets) == 0 {
		return nil
	}
	defaultConfig, _, _ := s.settings.appProxyState()
	legacyBlocked := s.containers.snapshotBlocked()
	unsupported := unsupportedAppNetworkPolicies(targets)
	activeConfigs := make(map[string]AppProxySettings)
	blockedTargets := make(map[string]bool)
	for _, target := range targets {
		policyID := appNetworkTargetPolicyID(target)
		if proxyApps[policyID] && !unsupported[policyID] {
			config, ok := proxyConfigs[policyID]
			if !ok {
				config = defaultConfig
			}
			activeConfigs[policyID] = config
		}
		if blockedApps[policyID] != "" || legacyBlocked[target.ID] != "" {
			blockedTargets[target.ID] = true
		}
	}
	return s.appProxyController.reconcile(ctx, targets, activeConfigs, blockedTargets)
}

func (s *Service) GetAppProxySettings() AppProxySettings {
	config, _, _ := s.settings.appProxyState()
	return config
}

func (s *Service) SetAppProxySettings(ctx context.Context, config AppProxySettings) (AppProxySettings, error) {
	config.Protocol = strings.ToLower(strings.TrimSpace(config.Protocol))
	config.Host = strings.TrimSpace(config.Host)
	if err := validateAppProxySettings(config); err != nil {
		return AppProxySettings{}, err
	}
	if s.appNetworkController == nil || s.settings == nil {
		return AppProxySettings{}, errors.New("application proxy controller is unavailable")
	}
	s.appNetworkController.mu.Lock()
	defer s.appNetworkController.mu.Unlock()
	previous, _, _ := s.settings.appProxyState()
	s.settings.setAppProxy(config, nil, nil)
	if err := saveMutableSettings(s.cfg.DataDir, s.GetMutableSettings()); err != nil {
		s.settings.setAppProxy(previous, nil, nil)
		return AppProxySettings{}, fmt.Errorf("persist application proxy settings: %w", err)
	}
	return config, nil
}
