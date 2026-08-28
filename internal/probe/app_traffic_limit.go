package probe

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"netwatch/internal/logger"
)

const (
	appTrafficTCHandle        = "194:"
	appTrafficTCFilterPref    = "49152"
	appTrafficTCFilterHandle  = "194"
	appTrafficTCCommandLimit  = 5 * time.Second
	appTrafficTCCheckInterval = 20 * time.Second
	maxAppTrafficLimitKbps    = int64(10_000_000)
)

var appTrafficTCUploadBypassFilters = []struct {
	pref     string
	protocol string
	cidr     string
}{
	{pref: "49140", protocol: "ip", cidr: "10.0.0.0/8"},
	{pref: "49141", protocol: "ip", cidr: "172.16.0.0/12"},
	{pref: "49142", protocol: "ip", cidr: "192.168.0.0/16"},
	{pref: "49143", protocol: "ipv6", cidr: "fc00::/7"},
	{pref: "49144", protocol: "ipv6", cidr: "fe80::/10"},
}

var hostTrafficControlPaths = []string{
	"/usr/bin/tc",
	"/usr/sbin/tc",
	"/sbin/tc",
	"/bin/tc",
}

var trafficRatePattern = regexp.MustCompile(`(?i)\brate\s+([0-9]+(?:\.[0-9]+)?)\s*(bit|kbit|mbit|gbit)\b`)

type appTrafficLimitInspection struct {
	DownloadKbps int64
	UploadKbps   int64
	DownloadRule bool
	UploadRule   bool
	UploadBypass map[string]bool
	UploadAction bool
	Ingress      bool
}

// inspectBridgeTrafficLimit reads the rules owned by Netwatch. Text output is
// intentionally used here because older Lazycat hosts may ship tc versions
// without stable JSON fields for police actions; the parser accepts the
// canonical iproute2 output across bit/Kbit/Mbit/Gbit units.
func inspectBridgeTrafficLimit(ctx context.Context, bridge string, desired AppTrafficLimit) (appTrafficLimitRuntime, error) {
	qdiscOutput, err := runTrafficControlCommand(ctx, "qdisc", "show", "dev", bridge)
	if err != nil {
		inspectErr := trafficControlError("inspect qdisc", bridge, qdiscOutput, err)
		return appTrafficLimitRuntime{Desired: desired, InSync: false, Diagnostic: inspectErr.Error(), CheckedAt: time.Now()}, inspectErr
	}
	filterOutput, filterErr := runTrafficControlCommand(ctx, "filter", "show", "dev", bridge, "ingress")
	if filterErr != nil && !trafficControlNotFound(filterOutput) {
		inspectErr := trafficControlError("inspect upload filter", bridge, filterOutput, filterErr)
		return appTrafficLimitRuntime{Desired: desired, InSync: false, Diagnostic: inspectErr.Error(), CheckedAt: time.Now()}, inspectErr
	}
	inspection := parseAppTrafficLimitInspection(qdiscOutput, filterOutput)
	issues := make([]string, 0, 4)
	if desired.DownloadKbps == 0 {
		if inspection.DownloadRule {
			issues = append(issues, "下载 qdisc 仍存在")
		}
	} else if !inspection.DownloadRule {
		issues = append(issues, "下载 qdisc 缺失")
	} else if inspection.DownloadKbps != desired.DownloadKbps {
		issues = append(issues, fmt.Sprintf("下载速率为 %d Kbit/s，期望 %d Kbit/s", inspection.DownloadKbps, desired.DownloadKbps))
	}
	if desired.UploadKbps == 0 {
		if inspection.UploadRule || len(inspection.UploadBypass) > 0 {
			issues = append(issues, "上传 filter 仍存在")
		}
	} else {
		if !inspection.Ingress {
			issues = append(issues, "上传 ingress hook 缺失")
		}
		if !inspection.UploadRule {
			issues = append(issues, "上传 police filter 缺失")
		} else if inspection.UploadKbps != desired.UploadKbps {
			issues = append(issues, fmt.Sprintf("上传速率为 %d Kbit/s，期望 %d Kbit/s", inspection.UploadKbps, desired.UploadKbps))
		}
		if !inspection.UploadAction {
			issues = append(issues, "上传 police 动作不是 conform-exceed drop/ok")
		}
		if len(inspection.UploadBypass) != len(appTrafficTCUploadBypassFilters) {
			issues = append(issues, fmt.Sprintf("上传局域网 bypass filter 不完整（%d/%d）", len(inspection.UploadBypass), len(appTrafficTCUploadBypassFilters)))
		}
	}
	status := appTrafficLimitRuntime{Desired: desired, Applied: AppTrafficLimit{UploadKbps: inspection.UploadKbps, DownloadKbps: inspection.DownloadKbps}, InSync: len(issues) == 0, CheckedAt: time.Now()}
	if len(issues) > 0 {
		status.Diagnostic = strings.Join(issues, "；")
	}
	return status, nil
}

func parseAppTrafficLimitInspection(qdiscOutput, filterOutput string) appTrafficLimitInspection {
	inspection := appTrafficLimitInspection{UploadBypass: make(map[string]bool)}
	for _, line := range strings.Split(qdiscOutput, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "qdisc clsact ") || strings.HasPrefix(line, "qdisc ingress ") {
			inspection.Ingress = true
		}
		if strings.HasPrefix(line, "qdisc tbf "+appTrafficTCHandle+" root") {
			inspection.DownloadRule = true
			if rate, ok := parseTrafficRateKbps(line); ok {
				inspection.DownloadKbps = rate
			}
		}
	}
	currentPref := ""
	for _, line := range strings.Split(filterOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pref := trafficFilterPref(line)
		if pref != "" {
			currentPref = pref
		}
		if pref == "" {
			pref = currentPref
		}
		switch {
		case pref == appTrafficTCFilterPref && strings.Contains(line, "police"):
			inspection.UploadRule = true
			inspection.UploadAction = strings.Contains(strings.ToLower(line), "drop/ok")
			if rate, ok := parseTrafficRateKbps(line); ok {
				inspection.UploadKbps = rate
			}
		case pref == appTrafficTCFilterPref && inspection.UploadRule && strings.Contains(strings.ToLower(line), "drop/ok"):
			inspection.UploadAction = true
		case isAppTrafficUploadBypassPref(pref):
			inspection.UploadBypass[pref] = true
		}
	}
	return inspection
}

func trafficFilterPref(line string) string {
	fields := strings.Fields(line)
	for index := 0; index+1 < len(fields); index++ {
		if fields[index] == "pref" {
			return strings.TrimSuffix(fields[index+1], ":")
		}
	}
	return ""
}

func isAppTrafficUploadBypassPref(pref string) bool {
	for _, bypass := range appTrafficTCUploadBypassFilters {
		if bypass.pref == pref {
			return true
		}
	}
	return false
}

func parseTrafficRateKbps(line string) (int64, bool) {
	match := trafficRatePattern.FindStringSubmatch(line)
	if len(match) != 3 {
		return 0, false
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil || value < 0 {
		return 0, false
	}
	multiplier := float64(1) / 1000
	switch strings.ToLower(match[2]) {
	case "kbit":
		multiplier = 1
	case "mbit":
		multiplier = 1000
	case "gbit":
		multiplier = 1000 * 1000
	}
	return int64(value*multiplier + 0.5), true
}

var runTrafficControlCommand = func(ctx context.Context, args ...string) (string, error) {
	findBinPaths()
	if nsenterPath == "" {
		return "", fmt.Errorf("nsenter command is unavailable")
	}
	hostTC, ok := hostTrafficControlPath()
	if !ok {
		return "", fmt.Errorf("host tc command is unavailable")
	}
	cmd := exec.CommandContext(ctx, nsenterPath, hostTrafficControlCommandArgs(hostTC, args...)...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

var applyBridgeTrafficLimitFunc = applyBridgeTrafficLimit

type appTrafficLimiter struct {
	mu          sync.Mutex
	operationMu sync.Mutex
	applied     map[string]AppTrafficLimit
	runtime     map[string]appTrafficLimitRuntime
	seenBridges map[string]bool
}

// appTrafficLimitRuntime describes what the last kernel inspection found.
// Desired values come from persistent application policy; Applied is only the
// last rule set accepted by tc and must not be treated as proof that the rule
// still exists.
type appTrafficLimitRuntime struct {
	Desired    AppTrafficLimit
	Applied    AppTrafficLimit
	InSync     bool
	Diagnostic string
	CheckedAt  time.Time
}

func newAppTrafficLimiter() *appTrafficLimiter {
	return &appTrafficLimiter{
		applied:     map[string]AppTrafficLimit{},
		runtime:     map[string]appTrafficLimitRuntime{},
		seenBridges: map[string]bool{},
	}
}

func trafficControlAvailable() bool {
	if !nsenterAvailable() {
		return false
	}
	_, ok := hostTrafficControlPath()
	return ok
}

// hostTrafficControlPath probes PID 1's filesystem instead of this scratch
// image. The app runs with host PID access on Lazycat, so PID 1 is the host
// init process and /proc/1/root is its filesystem root.
func hostTrafficControlPath() (string, bool) {
	for _, candidate := range hostTrafficControlPaths {
		if info, err := os.Stat("/proc/1/root" + candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func hostTrafficControlCommandArgs(hostTC string, args ...string) []string {
	// -r changes the process root to PID 1's root. Joining the mount namespace
	// alone would not make the host's /usr/bin/tc visible from a scratch image.
	command := []string{"-t", "1", "-m", "-n", "-r", "--", hostTC}
	return append(command, args...)
}

func sameAppTrafficLimit(a, b AppTrafficLimit) bool {
	return a.UploadKbps == b.UploadKbps && a.DownloadKbps == b.DownloadKbps
}

func (l *appTrafficLimiter) apply(ctx context.Context, bridge string, limit AppTrafficLimit) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	current := l.applied[bridge]
	if err := applyBridgeTrafficLimitFunc(ctx, bridge, limit); err != nil {
		// Download shaping is configured before ingress policing. Restore the
		// last owned rule when the second operation fails so API callers never
		// receive an error while a partial new ceiling is left behind.
		if rollbackErr := applyBridgeTrafficLimitFunc(ctx, bridge, current); rollbackErr != nil {
			logger.Warn("rollback partial traffic limit on %s: %v", bridge, rollbackErr)
		}
		return err
	}
	if limit.UploadKbps == 0 && limit.DownloadKbps == 0 {
		delete(l.applied, bridge)
	} else {
		l.applied[bridge] = limit
	}
	l.runtime[bridge] = appTrafficLimitRuntime{
		Desired: limit, Applied: limit, InSync: true, CheckedAt: time.Now(),
	}
	return nil
}

func (l *appTrafficLimiter) runtimeStatus(bridge string, desired AppTrafficLimit) appTrafficLimitRuntime {
	l.mu.Lock()
	defer l.mu.Unlock()
	status, ok := l.runtime[bridge]
	if !ok || !sameAppTrafficLimit(status.Desired, desired) {
		status = appTrafficLimitRuntime{Desired: desired, InSync: desired.UploadKbps == 0 && desired.DownloadKbps == 0, Diagnostic: "等待核验宿主机 tc 规则"}
	}
	return status
}

func (l *appTrafficLimiter) inspectAndRepair(ctx context.Context, bridge string, desired AppTrafficLimit, force bool) error {
	l.mu.Lock()
	previous, known := l.runtime[bridge]
	if !force && known && sameAppTrafficLimit(previous.Desired, desired) && previous.InSync && time.Since(previous.CheckedAt) < appTrafficTCCheckInterval {
		l.mu.Unlock()
		return nil
	}
	l.mu.Unlock()

	status, err := inspectBridgeTrafficLimit(ctx, bridge, desired)
	if err == nil && status.InSync {
		l.mu.Lock()
		status.Desired = desired
		status.Applied = desired
		l.runtime[bridge] = status
		if desired.UploadKbps == 0 && desired.DownloadKbps == 0 {
			delete(l.applied, bridge)
		} else {
			l.applied[bridge] = desired
		}
		l.mu.Unlock()
		return nil
	}

	if err == nil {
		err = fmt.Errorf("tc 规则与期望状态不一致: %s", status.Diagnostic)
	}
	if applyErr := l.apply(ctx, bridge, desired); applyErr != nil {
		l.mu.Lock()
		l.runtime[bridge] = appTrafficLimitRuntime{
			Desired: desired, Applied: previous.Applied, InSync: false,
			Diagnostic: applyErr.Error(), CheckedAt: time.Now(),
		}
		l.mu.Unlock()
		return applyErr
	}
	return nil
}

func (l *appTrafficLimiter) clearUnlimitedBridge(ctx context.Context, bridge string) error {
	if err := l.inspectAndRepair(ctx, bridge, AppTrafficLimit{}, false); err != nil {
		return err
	}
	l.mu.Lock()
	delete(l.applied, bridge)
	l.mu.Unlock()
	return nil
}

// reconcile restores supported Bridge limits and removes Netwatch-owned tc
// rules from applications that contain any Host-network container. The return
// value lists Host/mixed applications whose bridge rules were fully cleared,
// allowing the persisted, now-invalid limit state to be removed as well.
func (l *appTrafficLimiter) reconcile(ctx context.Context, items []AppBridgeStats, limits map[string]AppTrafficLimit) map[string]bool {
	l.operationMu.Lock()
	defer l.operationMu.Unlock()

	canControlTraffic := trafficControlAvailable()
	hostApps := make(map[string]bool)
	for _, item := range items {
		if item.AppID == "" {
			continue
		}
		if item.NetworkMode == "host" || strings.HasPrefix(item.Bridge, hostAppTargetPrefix) {
			hostApps[item.AppID] = true
		}
	}

	desired := make(map[string]AppTrafficLimit)
	activeBridges := make(map[string]bool)
	clearedHostApps := make(map[string]bool, len(hostApps))
	for appID := range hostApps {
		clearedHostApps[appID] = true
	}
	for _, item := range items {
		if item.AppID == "" || item.Bridge == "" || isNetwatchTrafficItem(item) {
			continue
		}
		if strings.HasPrefix(item.Bridge, hostAppTargetPrefix) {
			continue
		}
		if !strings.HasPrefix(item.Bridge, lzcBridgePrefix) {
			continue
		}
		activeBridges[item.Bridge] = true
		if hostApps[item.AppID] {
			if !canControlTraffic {
				limit := limits[item.AppID]
				if limit.UploadKbps != 0 || limit.DownloadKbps != 0 {
					clearedHostApps[item.AppID] = false
				}
				continue
			}
			if err := l.clearUnlimitedBridge(ctx, item.Bridge); err != nil {
				logger.Warn("clear unsupported traffic limit on %s: %v", item.Bridge, err)
				clearedHostApps[item.AppID] = false
			}
			continue
		}
		limit := limits[item.AppID]
		if limit.UploadKbps == 0 && limit.DownloadKbps == 0 {
			if canControlTraffic {
				if err := l.clearUnlimitedBridge(ctx, item.Bridge); err != nil {
					logger.Warn("clear inactive traffic limit on %s: %v", item.Bridge, err)
				}
			}
			continue
		}
		desired[item.Bridge] = limit
	}
	for bridge, limit := range desired {
		l.mu.Lock()
		force := !l.seenBridges[bridge]
		l.mu.Unlock()
		if err := l.inspectAndRepair(ctx, bridge, limit, force); err != nil {
			logger.Warn("restore traffic limit on %s: %v", bridge, err)
		}
	}
	for bridge := range activeBridges {
		l.mu.Lock()
		l.seenBridges[bridge] = true
		l.mu.Unlock()
	}
	l.mu.Lock()
	for bridge := range l.applied {
		if _, ok := desired[bridge]; !ok {
			// The bridge has been removed or the limit was cleared. A removed
			// network loses its qdisc with it, so dropping this in-memory mark is
			// enough and avoids touching an unknown future interface of the same name.
			delete(l.applied, bridge)
			delete(l.runtime, bridge)
		}
	}
	for bridge := range l.seenBridges {
		if !activeBridges[bridge] {
			delete(l.seenBridges, bridge)
		}
	}
	l.mu.Unlock()
	return clearedHostApps
}

func applyBridgeTrafficLimit(parent context.Context, bridge string, limit AppTrafficLimit) error {
	if !strings.HasPrefix(bridge, lzcBridgePrefix) {
		return fmt.Errorf("invalid application bridge %q", bridge)
	}
	if _, err := os.Stat("/sys/class/net/" + bridge); err != nil {
		return fmt.Errorf("application bridge %s is unavailable", bridge)
	}
	if !trafficControlAvailable() {
		return fmt.Errorf("host tc command or nsenter is unavailable")
	}
	ctx, cancel := context.WithTimeout(parent, appTrafficTCCommandLimit)
	defer cancel()

	if err := configureBridgeDownloadLimit(ctx, bridge, limit.DownloadKbps); err != nil {
		return err
	}
	if err := configureBridgeUploadLimit(ctx, bridge, limit.UploadKbps); err != nil {
		return err
	}
	return nil
}

func configureBridgeDownloadLimit(ctx context.Context, bridge string, kbps int64) error {
	if kbps == 0 {
		out, err := runTrafficControlCommand(ctx, "qdisc", "show", "dev", bridge)
		if err != nil {
			return trafficControlError("read download qdisc", bridge, out, err)
		}
		if strings.Contains(out, "qdisc tbf "+appTrafficTCHandle+" root") {
			out, err = runTrafficControlCommand(ctx, "qdisc", "del", "dev", bridge, "root")
			if err != nil {
				return trafficControlError("remove download limit", bridge, out, err)
			}
		}
		return nil
	}
	out, err := runTrafficControlCommand(ctx, "qdisc", "show", "dev", bridge)
	if err != nil {
		return trafficControlError("read download qdisc", bridge, out, err)
	}
	if !bridgeRootQdiscCanBeManaged(out) {
		return fmt.Errorf("application bridge %s already has an unmanaged root qdisc; refusing to replace it", bridge)
	}
	burst := appTrafficBurstBytes(kbps)
	out, err = runTrafficControlCommand(ctx, "qdisc", "replace", "dev", bridge, "root", "handle", appTrafficTCHandle,
		"tbf", "rate", strconv.FormatInt(kbps, 10)+"kbit", "burst", strconv.FormatInt(burst, 10), "latency", "50ms")
	if err != nil {
		return trafficControlError("set download limit", bridge, out, err)
	}
	return nil
}

func configureBridgeUploadLimit(ctx context.Context, bridge string, kbps int64) error {
	if err := clearBridgeUploadFilters(ctx, bridge); err != nil {
		return err
	}
	if kbps == 0 {
		return nil
	}
	if err := ensureBridgeClsact(ctx, bridge); err != nil {
		return err
	}
	for _, bypass := range appTrafficTCUploadBypassFilters {
		out, err := runTrafficControlCommand(ctx, "filter", "add", "dev", bridge, "ingress", "pref", bypass.pref,
			"protocol", bypass.protocol, "flower", "dst_ip", bypass.cidr, "action", "gact", "pass")
		if err != nil {
			return trafficControlError("set upload local bypass", bridge, out, err)
		}
	}
	burst := appTrafficBurstBytes(kbps)
	filterArgs := []string{"dev", bridge, "ingress", "pref", appTrafficTCFilterPref, "handle", appTrafficTCFilterHandle,
		"protocol", "all", "matchall", "action", "police", "rate", strconv.FormatInt(kbps, 10) + "kbit",
		// tc's conform-exceed syntax is EXCEEDACT/NOTEXCEEDACT. Drop only
		// packets above the ceiling; conforming packets must pass.
		"burst", strconv.FormatInt(burst, 10), "conform-exceed", "drop/ok"}
	out, err := runTrafficControlCommand(ctx, append([]string{"filter", "add"}, filterArgs...)...)
	if err != nil {
		return trafficControlError("set upload limit", bridge, out, err)
	}
	return nil
}

// clearBridgeUploadFilters removes every filter at Netwatch's reserved
// priority. Older releases could leave a legacy no-handle rule beside the
// current rule, and deleting once is not enough when duplicate rules exist.
func clearBridgeUploadFilters(ctx context.Context, bridge string) error {
	out, err := runTrafficControlCommand(ctx, "qdisc", "show", "dev", bridge)
	if err != nil {
		return trafficControlError("read upload qdisc", bridge, out, err)
	}
	if !bridgeHasIngressHook(out) {
		return nil
	}

	prefs := make([]string, 0, len(appTrafficTCUploadBypassFilters)+1)
	for _, bypass := range appTrafficTCUploadBypassFilters {
		prefs = append(prefs, bypass.pref)
	}
	prefs = append(prefs, appTrafficTCFilterPref)
	for _, pref := range prefs {
		if err := clearBridgeTrafficFiltersAtPriority(ctx, bridge, pref); err != nil {
			return err
		}
	}
	return nil
}

func clearBridgeTrafficFiltersAtPriority(ctx context.Context, bridge, pref string) error {
	for attempts := 0; attempts < 16; attempts++ {
		out, err := runTrafficControlCommand(ctx, "filter", "del", "dev", bridge, "ingress", "pref", pref)
		if err != nil {
			if trafficControlNotFound(out) {
				return nil
			}
			return trafficControlError("remove upload limit", bridge, out, err)
		}
	}
	return fmt.Errorf("remove upload limit for %s: too many filters at priority %s", bridge, pref)
}

// ensureBridgeClsact creates the ingress hook only when it does not already
// exist. Replacing an existing clsact qdisc could remove filters installed by
// another component, while clsact coexists with the root TBF used for download
// shaping.
func ensureBridgeClsact(ctx context.Context, bridge string) error {
	out, err := runTrafficControlCommand(ctx, "qdisc", "show", "dev", bridge)
	if err != nil {
		return trafficControlError("read upload qdisc", bridge, out, err)
	}
	if strings.Contains(out, "qdisc clsact ") {
		return nil
	}
	out, err = runTrafficControlCommand(ctx, "qdisc", "add", "dev", bridge, "clsact")
	if err == nil {
		return nil
	}
	// Another operation can create clsact between the read and add. Recheck it
	// rather than replacing a qdisc owned by somebody else.
	recheck, recheckErr := runTrafficControlCommand(ctx, "qdisc", "show", "dev", bridge)
	if recheckErr == nil && strings.Contains(recheck, "qdisc clsact ") {
		return nil
	}
	return trafficControlError("enable upload classifier", bridge, out, err)
}

func bridgeHasIngressHook(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "qdisc clsact ") || strings.HasPrefix(line, "qdisc ingress ") {
			return true
		}
	}
	return false
}

func bridgeRootQdiscCanBeManaged(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, " root ") {
			continue
		}
		return strings.Contains(line, "qdisc noqueue ") || strings.Contains(line, "qdisc tbf "+appTrafficTCHandle+" root")
	}
	return true
}

func appTrafficBurstBytes(kbps int64) int64 {
	burst := kbps * 1000 / 8 / 10 // 100 ms worth of traffic
	if burst < 16*1024 {
		burst = 16 * 1024
	}
	if burst > 4*1024*1024 {
		burst = 4 * 1024 * 1024
	}
	return burst
}

func trafficControlNotFound(output string) bool {
	value := strings.ToLower(output)
	return strings.Contains(value, "cannot find") ||
		strings.Contains(value, "no such file") ||
		strings.Contains(value, "not found") ||
		strings.Contains(value, "parent qdisc doesn't exist") ||
		strings.Contains(value, "parent qdisc does not exist")
}

func trafficControlAlreadyExists(output string) bool {
	value := strings.ToLower(output)
	return strings.Contains(value, "file exists") || strings.Contains(value, "already exists")
}

func trafficControlError(action, bridge, output string, err error) error {
	if output != "" {
		return fmt.Errorf("%s for %s: %s", action, bridge, output)
	}
	return fmt.Errorf("%s for %s: %w", action, bridge, err)
}

// SetAppTrafficLimit updates a limit only after tc accepted it on every active
// bridge of the app. This prevents a persisted "limited" state from lying when
// a kernel rule could not be installed.
func (s *Service) SetAppTrafficLimit(ctx context.Context, appID string, limit AppTrafficLimit) error {
	return s.updateAppNetworkPolicy(ctx, appID, appNetworkPolicyUpdate{
		UploadKbps: &limit.UploadKbps, DownloadKbps: &limit.DownloadKbps,
	})
}

func (s *Service) reconcileAppTrafficLimits(items []AppBridgeStats) {
	if s.appTraffic == nil || s.appTrafficLimiter == nil {
		return
	}
	clearedHostApps := s.appTrafficLimiter.reconcile(s.LifecycleContext(), items, s.appTraffic.limitsSnapshot())
	for appID, cleared := range clearedHostApps {
		if !cleared {
			continue
		}
		limit := s.appTraffic.limitForApp(appID)
		if limit.UploadKbps == 0 && limit.DownloadKbps == 0 {
			continue
		}
		if err := s.appTraffic.setLimit(appID, AppTrafficLimit{}); err != nil {
			logger.Warn("remove unsupported traffic limit for %s: %v", appID, err)
			continue
		}
		logger.Info("removed unsupported traffic limit for Host/mixed application %s", appID)
	}
}
