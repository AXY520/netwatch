package probe

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"netwatch/internal/logger"
)

const (
	appTrafficTCHandle       = "194:"
	appTrafficTCFilterPref   = "49152"
	appTrafficTCFilterHandle = "194"
	appTrafficTCCommandLimit = 5 * time.Second
	maxAppTrafficLimitKbps   = int64(10_000_000)
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

type appTrafficLimiter struct {
	mu      sync.Mutex
	applied map[string]AppTrafficLimit
}

func newAppTrafficLimiter() *appTrafficLimiter {
	return &appTrafficLimiter{applied: map[string]AppTrafficLimit{}}
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
	current, known := l.applied[bridge]
	if known && sameAppTrafficLimit(current, limit) {
		return nil
	}
	if err := applyBridgeTrafficLimit(ctx, bridge, limit); err != nil {
		// Download shaping is configured before ingress policing. Restore the
		// last owned rule when the second operation fails so API callers never
		// receive an error while a partial new ceiling is left behind.
		if rollbackErr := applyBridgeTrafficLimit(ctx, bridge, current); rollbackErr != nil {
			logger.Warn("rollback partial traffic limit on %s: %v", bridge, rollbackErr)
		}
		return err
	}
	if limit.UploadKbps == 0 && limit.DownloadKbps == 0 {
		delete(l.applied, bridge)
	} else {
		l.applied[bridge] = limit
	}
	return nil
}

func (l *appTrafficLimiter) reconcile(ctx context.Context, items []AppBridgeStats, limits map[string]AppTrafficLimit) {
	desired := make(map[string]AppTrafficLimit)
	for _, item := range items {
		if item.AppID == "" || item.Bridge == "" || isNetwatchTrafficItem(item) {
			continue
		}
		limit := limits[item.AppID]
		if limit.UploadKbps == 0 && limit.DownloadKbps == 0 {
			continue
		}
		desired[item.Bridge] = limit
	}
	for bridge, limit := range desired {
		if err := l.apply(ctx, bridge, limit); err != nil {
			logger.Warn("restore traffic limit on %s: %v", bridge, err)
		}
	}
	l.mu.Lock()
	for bridge := range l.applied {
		if _, ok := desired[bridge]; !ok {
			// The bridge has been removed or the limit was cleared. A removed
			// network loses its qdisc with it, so dropping this in-memory mark is
			// enough and avoids touching an unknown future interface of the same name.
			delete(l.applied, bridge)
		}
	}
	l.mu.Unlock()
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
		"burst", strconv.FormatInt(burst, 10), "conform-exceed", "ok/drop"}
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
	return strings.Contains(value, "cannot find") || strings.Contains(value, "no such file") || strings.Contains(value, "not found")
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
	appID = strings.TrimSpace(appID)
	if appID == "" || isNetwatchTrafficItem(AppBridgeStats{AppID: appID}) {
		return fmt.Errorf("invalid application id")
	}
	if limit.UploadKbps < 0 || limit.DownloadKbps < 0 || limit.UploadKbps > maxAppTrafficLimitKbps || limit.DownloadKbps > maxAppTrafficLimitKbps {
		return fmt.Errorf("traffic limit must be between 0 and %d Kbit/s", maxAppTrafficLimitKbps)
	}
	items := CollectAppTraffic().Bridges
	bridges := make([]string, 0)
	for _, item := range items {
		if item.AppID == appID && item.Bridge != "" {
			bridges = append(bridges, item.Bridge)
		}
	}
	if len(bridges) == 0 {
		return fmt.Errorf("application %s has no controllable bridge", appID)
	}
	previous := s.appTraffic.limitForApp(appID)
	applied := make([]string, 0, len(bridges))
	for _, bridge := range dedupeSortedStrings(bridges) {
		if err := s.appTrafficLimiter.apply(ctx, bridge, limit); err != nil {
			for _, rollbackBridge := range applied {
				if rollbackErr := s.appTrafficLimiter.apply(ctx, rollbackBridge, previous); rollbackErr != nil {
					logger.Warn("rollback traffic limit on %s: %v", rollbackBridge, rollbackErr)
				}
			}
			return err
		}
		applied = append(applied, bridge)
	}
	if err := s.appTraffic.setLimit(appID, limit); err != nil {
		for _, rollbackBridge := range applied {
			if rollbackErr := s.appTrafficLimiter.apply(ctx, rollbackBridge, previous); rollbackErr != nil {
				logger.Warn("rollback traffic limit after persist failure on %s: %v", rollbackBridge, rollbackErr)
			}
		}
		return fmt.Errorf("persist traffic limit: %w", err)
	}
	return nil
}

func (s *Service) reconcileAppTrafficLimits(items []AppBridgeStats) {
	if s.appTraffic == nil || s.appTrafficLimiter == nil {
		return
	}
	s.appTrafficLimiter.reconcile(s.LifecycleContext(), items, s.appTraffic.limitsSnapshot())
}
