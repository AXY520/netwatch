package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"netwatch/internal/logger"
)

// Host DNS changes are intentionally short-lived before confirm: a bad DNS
// breaks name resolution for the whole host (including this app origin).
const hostDNSRollbackDelay = 60 * time.Second

type hostDNSSnapshot struct {
	Connection    string `json:"connection"`
	Device        string `json:"device"`
	DNS           string `json:"dns"`
	IgnoreAutoDNS string `json:"ignore_auto_dns"` // yes|no|""
	Method        string `json:"method"`          // connection ipv4.method (untouched by DNS-only apply)
}

type hostDNSRollback struct {
	ID       string              `json:"id"`
	Device   string              `json:"device"`
	Request  HostDNSApplyRequest `json:"request"`
	Snapshot hostDNSSnapshot     `json:"snapshot"`
	Timer    *time.Timer         `json:"-"`
	Until    time.Time           `json:"until"`
}

// GetHostDNS returns DNS for the preferred device (or auto-picked uplink) plus candidates.
func (s *Service) GetHostDNS(ctx context.Context, preferredDevice string) HostDNSInfo {
	if !nmcliTransportAvailable() {
		return HostDNSInfo{Enabled: false, Error: "无法管理 DNS：需要懒猫系统接口（lzc-apis）或本机 nmcli"}
	}
	cands, err := listHostDNSCandidates(ctx)
	if err != nil {
		return HostDNSInfo{Enabled: true, Error: err.Error(), Pending: s.getHostDNSPending()}
	}
	info := HostDNSInfo{
		Enabled:    true,
		Candidates: cands,
		Pending:    s.getHostDNSPending(),
		Note:       "修改的是 NetworkManager 连接级 DNS（经懒猫 SDK），不是硬写 resolv.conf。",
	}
	target, ok := pickHostDNSTarget(cands, preferredDevice)
	if !ok {
		info.Error = "未找到可用的联网连接"
		return info
	}
	fillHostDNSInfo(&info, target)
	return info
}

// ApplyHostDNS changes only DNS on the target connection (keeps IP/gateway).
func (s *Service) ApplyHostDNS(ctx context.Context, req HostDNSApplyRequest) HostDNSOpResult {
	req.Device = strings.TrimSpace(req.Device)
	req.Method = strings.ToLower(strings.TrimSpace(req.Method))
	if req.Method == "" {
		req.Method = "manual"
	}
	req.DNS = normalizeDNS(req.DNS)

	if !nmcliTransportAvailable() {
		return HostDNSOpResult{Error: "无法管理 DNS：需要懒猫系统接口（lzc-apis）或本机 nmcli"}
	}
	if req.Method != "auto" && req.Method != "manual" {
		return HostDNSOpResult{Error: "method 必须是 auto 或 manual"}
	}
	if req.Method == "manual" {
		if req.DNS == "" {
			return HostDNSOpResult{Error: "手动模式请填写至少一个 DNS"}
		}
		if err := validateDNSList(req.DNS); err != nil {
			return HostDNSOpResult{Error: err.Error()}
		}
	}

	id, err := s.reserveNetworkMutation(networkMutationDNS, req.Device)
	if err != nil {
		return HostDNSOpResult{Error: err.Error()}
	}
	activated := false
	defer func() {
		if !activated {
			s.abortNetworkMutation(id)
		}
	}()

	cands, err := listHostDNSCandidates(ctx)
	if err != nil {
		return HostDNSOpResult{Error: err.Error()}
	}
	target, ok := pickHostDNSTarget(cands, req.Device)
	if !ok {
		return HostDNSOpResult{Device: req.Device, Error: "目标连接不可用或未连接"}
	}
	if isUnsafeNetworkDevice(target.Device) {
		return HostDNSOpResult{Device: target.Device, Error: "拒绝操作虚拟或不安全网卡"}
	}

	snap, err := readHostDNSSnapshot(ctx, target.Device, target.Connection)
	if err != nil {
		return HostDNSOpResult{Device: target.Device, Connection: target.Connection, Error: err.Error()}
	}

	// DNS-only: never touch ipv4.method / addresses / gateway.
	var args []string
	if req.Method == "auto" {
		args = []string{
			"connection", "modify", target.Connection,
			"ipv4.dns", "",
			"ipv4.ignore-auto-dns", "no",
		}
	} else {
		args = []string{
			"connection", "modify", target.Connection,
			"ipv4.dns", req.DNS,
			"ipv4.ignore-auto-dns", "yes",
		}
	}
	out1, err := nmcli(ctx, args, 10*time.Second)
	if err != nil {
		return HostDNSOpResult{
			Device: target.Device, Connection: target.Connection,
			Output: strings.TrimSpace(out1), Error: "修改 DNS 失败: " + err.Error(),
		}
	}
	out2, err := nmcli(ctx, []string{"device", "reapply", target.Device}, 15*time.Second)
	out := strings.TrimSpace(strings.Join([]string{out1, out2}, "\n"))
	if err != nil {
		// Best-effort restore immediately on reapply failure.
		_, _ = restoreHostDNSSnapshot(ctx, snap)
		return HostDNSOpResult{
			Device: target.Device, Connection: target.Connection,
			Output: out, Error: "应用 DNS 失败: " + err.Error(),
		}
	}

	until := time.Now().Add(hostDNSRollbackDelay)
	rb := &hostDNSRollback{
		ID:       id,
		Device:   target.Device,
		Request:  req,
		Snapshot: snap,
		Until:    until,
	}
	if err := s.activateNetworkMutation(&networkMutation{
		ID: id, Kind: networkMutationDNS, Target: target.Device, Until: until, DNS: rb,
	}); err != nil {
		_, restoreErr := restoreHostDNSSnapshot(ctx, snap)
		if restoreErr != nil {
			return HostDNSOpResult{Device: target.Device, Connection: target.Connection, Error: fmt.Sprintf("登记 DNS 变更失败: %v；恢复原配置失败: %v", err, restoreErr)}
		}
		return HostDNSOpResult{Device: target.Device, Connection: target.Connection, Error: "登记 DNS 变更失败: " + err.Error()}
	}
	activated = true
	s.scheduleNetworkMutationRollback(s.networkMutationSnapshot())
	verification := s.verifyNetworkMutation(ctx, id)
	if verification.Status == "failed" {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		rollbackOut, rollbackErr := s.rollbackHostDNS(rollbackCtx, id, "verification_rollback")
		cancel()
		errText := "DNS 未通过应用后验证，已自动回滚"
		if rollbackErr != nil {
			errText = "DNS 未通过应用后验证，自动回滚失败: " + rollbackErr.Error()
		}
		return HostDNSOpResult{Device: target.Device, Connection: target.Connection, Output: strings.TrimSpace(strings.Join([]string{out, rollbackOut}, "\n")), Error: errText, Verification: &verification}
	}

	logger.Info("host dns applied device=%s conn=%s method=%s dns=%q rollback=%s",
		target.Device, target.Connection, req.Method, req.DNS, id)

	return HostDNSOpResult{
		OK:            true,
		Device:        target.Device,
		Connection:    target.Connection,
		Method:        req.Method,
		DNS:           req.DNS,
		RollbackID:    id,
		RollbackUntil: until.Format(time.DateTime),
		Output:        out,
		Note:          "DNS 已应用，请在 60 秒内确认；超时自动回滚",
		Verification:  &verification,
	}
}

func (s *Service) ConfirmHostDNS(id string) HostDNSOpResult {
	id = strings.TrimSpace(id)
	if id == "" {
		return HostDNSOpResult{Error: "rollback id required"}
	}
	mutation, err := s.confirmNetworkMutation(id, networkMutationDNS)
	if err != nil || mutation.DNS == nil {
		return HostDNSOpResult{Error: "没有匹配的待确认 DNS 变更"}
	}
	rb := mutation.DNS
	device, conn := rb.Device, rb.Snapshot.Connection
	logger.Info("host dns confirmed id=%s device=%s", id, device)
	return HostDNSOpResult{OK: true, Device: device, Connection: conn, Note: "DNS 变更已确认"}
}

func (s *Service) RollbackHostDNS(ctx context.Context, id string) HostDNSOpResult {
	out, err := s.rollbackHostDNS(ctx, strings.TrimSpace(id), "manual_rollback")
	if err != nil {
		return HostDNSOpResult{Output: out, Error: err.Error()}
	}
	return HostDNSOpResult{OK: true, Output: out, Note: "DNS 已回滚"}
}

func (s *Service) GetHostDNSPending() HostDNSPending {
	p := s.getHostDNSPending()
	if p == nil {
		return HostDNSPending{Pending: false}
	}
	return *p
}

func (s *Service) getHostDNSPending() *HostDNSPending {
	s.network.mu.Lock()
	defer s.network.mu.Unlock()
	rb := s.network.dnsRollback
	if rb == nil {
		return nil
	}
	remaining := int(time.Until(rb.Until).Seconds())
	if remaining < 0 {
		remaining = 0
	}
	method := rb.Request.Method
	if method == "" {
		method = "manual"
	}
	return &HostDNSPending{
		Pending:       true,
		ID:            rb.ID,
		Device:        rb.Device,
		Connection:    rb.Snapshot.Connection,
		Method:        method,
		DNS:           rb.Request.DNS,
		PrevMethod:    dnsMethodFromSnapshot(rb.Snapshot),
		PrevDNS:       rb.Snapshot.DNS,
		RollbackUntil: rb.Until.Format(time.DateTime),
		RemainingSec:  remaining,
	}
}

func (s *Service) rollbackHostDNS(ctx context.Context, id, action string) (string, error) {
	mutation, err := s.beginNetworkMutationRollback(id, networkMutationDNS)
	if err != nil || mutation.DNS == nil {
		return "", errors.New("没有待回滚的 DNS 变更")
	}
	rb := mutation.DNS
	snap := rb.Snapshot

	out, err := restoreHostDNSSnapshot(ctx, snap)
	s.finishNetworkMutationRollback(rb.ID, err)
	logger.Info("host dns rollback action=%s device=%s ok=%v", action, snap.Device, err == nil)
	return out, err
}

func dnsMethodFromSnapshot(snap hostDNSSnapshot) string {
	// Manual when the profile ignores DHCP DNS (static nameservers pinned).
	if strings.EqualFold(strings.TrimSpace(snap.IgnoreAutoDNS), "yes") {
		return "manual"
	}
	return "auto"
}

func fillHostDNSInfo(info *HostDNSInfo, c HostDNSCandidate) {
	info.Device = c.Device
	info.Connection = c.Connection
	info.Type = c.Type
	info.Method = c.Method
	info.DNS = c.DNS
	if runtime, err := readNetworkDeviceRuntimeConfig(context.Background(), c.Device); err == nil {
		info.RuntimeDNS = runtime.DNS
		if info.DNS == "" {
			info.DNS = runtime.DNS
		}
	}
}

func listHostDNSCandidates(ctx context.Context) ([]HostDNSCandidate, error) {
	out, err := nmcli(ctx, []string{"-t", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device", "status"}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	var cands []HostDNSCandidate
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := splitNMCLIFields(line)
		if len(fields) < 4 {
			continue
		}
		dev, typ, state, conn := fields[0], fields[1], fields[2], fields[3]
		if !strings.HasPrefix(state, "connected") || conn == "" {
			continue
		}
		typLower := strings.ToLower(typ)
		// Uplink-ish: ethernet, wifi, or netwatch-managed bridge.
		if typLower != "ethernet" && typLower != "wifi" && typLower != "bridge" {
			continue
		}
		if typLower == "bridge" && !isManagedHostBridgeName(dev) {
			// Only expose netwatch bridges (nw-*), not docker/br-*).
			continue
		}
		if isUnsafeNetworkDevice(dev) && !isManagedHostBridgeName(dev) {
			continue
		}
		// Skip bridge *ports* (enslaved physical NICs) — DNS lives on the bridge.
		if connectionIsBridgePort(ctx, conn) {
			continue
		}
		c := HostDNSCandidate{Device: dev, Type: typ, Connection: conn}
		if snap, err := readHostDNSSnapshot(ctx, dev, conn); err == nil {
			c.Method = dnsMethodFromSnapshot(snap)
			c.DNS = snap.DNS
			if c.DNS == "" {
				if runtime, err := readNetworkDeviceRuntimeConfig(ctx, dev); err == nil {
					c.DNS = runtime.DNS
				}
			}
		}
		cands = append(cands, c)
	}
	return cands, nil
}

func pickHostDNSTarget(cands []HostDNSCandidate, device string) (HostDNSCandidate, bool) {
	device = strings.TrimSpace(device)
	if device != "" {
		for _, c := range cands {
			if c.Device == device {
				return c, true
			}
		}
		return HostDNSCandidate{}, false
	}
	// Prefer netwatch bridge (host address usually lives there after create).
	for _, c := range cands {
		if isManagedHostBridgeName(c.Device) {
			return c, true
		}
	}
	// Prefer default route interface.
	if def := readDefaultIPv4Route(); def.Interface != "" {
		for _, c := range cands {
			if c.Device == def.Interface {
				return c, true
			}
		}
	}
	if len(cands) > 0 {
		return cands[0], true
	}
	return HostDNSCandidate{}, false
}

func readHostDNSSnapshot(ctx context.Context, device, connection string) (hostDNSSnapshot, error) {
	connection = strings.TrimSpace(connection)
	if connection == "" {
		return hostDNSSnapshot{}, errors.New("empty connection")
	}
	out, err := nmcli(ctx, []string{
		"-g", "ipv4.method,ipv4.dns,ipv4.ignore-auto-dns",
		"connection", "show", connection,
	}, 5*time.Second)
	if err != nil {
		return hostDNSSnapshot{}, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for len(lines) < 3 {
		lines = append(lines, "")
	}
	return hostDNSSnapshot{
		Connection:    connection,
		Device:        device,
		Method:        strings.TrimSpace(lines[0]),
		DNS:           normalizeDNS(strings.TrimSpace(lines[1])),
		IgnoreAutoDNS: strings.TrimSpace(lines[2]),
	}, nil
}

func restoreHostDNSSnapshot(ctx context.Context, snap hostDNSSnapshot) (string, error) {
	ignore := strings.TrimSpace(snap.IgnoreAutoDNS)
	if ignore == "" {
		if snap.DNS == "" {
			ignore = "no"
		} else {
			// Unknown: prefer no so DHCP can refill.
			ignore = "no"
		}
	}
	args := []string{
		"connection", "modify", snap.Connection,
		"ipv4.dns", snap.DNS,
		"ipv4.ignore-auto-dns", ignore,
	}
	out1, err := nmcli(ctx, args, 10*time.Second)
	if err != nil {
		return strings.TrimSpace(out1), err
	}
	out2, err := nmcli(ctx, []string{"device", "reapply", snap.Device}, 15*time.Second)
	return strings.TrimSpace(strings.Join([]string{out1, out2}, "\n")), err
}

func validateDNSList(list string) error {
	parts := strings.Split(list, ",")
	if len(parts) == 0 {
		return errors.New("DNS 不能为空")
	}
	if len(parts) > 3 {
		return errors.New("最多 3 个 DNS")
	}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		ip := net.ParseIP(p)
		if ip == nil || ip.To4() == nil {
			return errors.New("DNS 必须是 IPv4 地址: " + p)
		}
		if ip.IsLoopback() || ip.IsUnspecified() {
			return errors.New("DNS 不能是回环或 0.0.0.0: " + p)
		}
	}
	return nil
}
