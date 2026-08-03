package probe

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"netwatch/internal/logger"
)

// Host DNS changes are intentionally short-lived before confirm: a bad DNS
// breaks name resolution for the whole host (including this app origin).
const hostDNSRollbackDelay = 60 * time.Second

type hostDNSSnapshot struct {
	Connection    string
	Device        string
	DNS           string
	IgnoreAutoDNS string // yes|no|""
	Method        string // connection ipv4.method (untouched by DNS-only apply)
}

type hostDNSRollback struct {
	ID       string
	Device   string
	Request  HostDNSApplyRequest
	Snapshot hostDNSSnapshot
	Timer    *time.Timer
	Until    time.Time
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

	// Mutual exclusion with IP config / bridge / other DNS pending.
	s.network.mu.Lock()
	if s.network.dnsRollback != nil {
		s.network.mu.Unlock()
		return HostDNSOpResult{Error: "已有待确认的 DNS 变更，请先确认或回滚"}
	}
	if s.network.bridgeRollback != nil {
		s.network.mu.Unlock()
		return HostDNSOpResult{Error: "已有待确认的网桥变更，请先确认或回滚"}
	}
	for _, rb := range s.network.rollbacks {
		if rb != nil {
			s.network.mu.Unlock()
			return HostDNSOpResult{Error: "已有待确认的网卡配置变更，请先确认或回滚"}
		}
	}
	s.network.mu.Unlock()

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

	id := newRollbackID()
	until := time.Now().Add(hostDNSRollbackDelay)
	rb := &hostDNSRollback{
		ID:       id,
		Device:   target.Device,
		Request:  req,
		Snapshot: snap,
		Until:    until,
	}
	rb.Timer = time.AfterFunc(hostDNSRollbackDelay, func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if out, err := s.rollbackHostDNS(rollbackCtx, id, "auto_rollback"); err != nil {
			logger.Warn("host dns auto rollback failed id=%s device=%s err=%v out=%q", id, target.Device, err, out)
		}
	})

	s.network.mu.Lock()
	if old := s.network.dnsRollback; old != nil && old.Timer != nil {
		old.Timer.Stop()
	}
	s.network.dnsRollback = rb
	s.network.mu.Unlock()

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
	}
}

func (s *Service) ConfirmHostDNS(id string) HostDNSOpResult {
	id = strings.TrimSpace(id)
	if id == "" {
		return HostDNSOpResult{Error: "rollback id required"}
	}
	s.network.mu.Lock()
	rb := s.network.dnsRollback
	if rb == nil || (id != "" && rb.ID != id) {
		s.network.mu.Unlock()
		return HostDNSOpResult{Error: "没有匹配的待确认 DNS 变更"}
	}
	if rb.Timer != nil {
		rb.Timer.Stop()
	}
	device, conn := rb.Device, rb.Snapshot.Connection
	s.network.dnsRollback = nil
	s.network.mu.Unlock()
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
	if remaining == 0 {
		return nil
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
	s.network.mu.Lock()
	rb := s.network.dnsRollback
	if rb == nil {
		s.network.mu.Unlock()
		return "", errors.New("没有待回滚的 DNS 变更")
	}
	if id != "" && rb.ID != id {
		s.network.mu.Unlock()
		return "", errors.New("rollback id 不匹配")
	}
	if rb.Timer != nil {
		rb.Timer.Stop()
	}
	snap := rb.Snapshot
	s.network.dnsRollback = nil
	s.network.mu.Unlock()

	out, err := restoreHostDNSSnapshot(ctx, snap)
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
