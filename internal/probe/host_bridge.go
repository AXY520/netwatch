package probe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"netwatch/internal/logger"
)

// Linux IFNAMSIZ is 16 (incl. NUL) → max 15 visible chars.
const hostBridgeNameMax = 15
const hostBridgeNamePrefix = "nw-"
const hostBridgeRollbackDelay = 3 * time.Minute

var hostBridgeNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,14}$`)

type hostBridgeRollback struct {
	ID       string                `json:"id"`
	Bridge   string                `json:"bridge"`
	Device   string                `json:"device"`
	Record   HostBridgeRecord      `json:"record"`
	Original networkConfigSnapshot `json:"original"`
	Until    time.Time             `json:"until"`
	Timer    *time.Timer           `json:"-"`
}

func hostBridgesPath(dataDir string) string {
	return filepath.Join(dataDir, "host_bridges.json")
}

func (s *Service) ListHostBridges(ctx context.Context) HostBridgeListResult {
	backend := hostBridgeBackend()
	if backend == "" {
		// Still surface pending confirm after reconnect even if tools are momentarily unavailable.
		return HostBridgeListResult{
			Enabled: false,
			Bridges: s.loadHostBridgeRecords(),
			Pending: s.getHostBridgePending(),
			Error:   "无法管理网桥：需要懒猫系统 nmcli 接口（lzc-apis）或本机 nmcli",
		}
	}
	records := s.loadHostBridgeRecords()
	// Refresh runtime note if bridge iface missing
	for i := range records {
		if _, err := os.Stat(filepath.Join("/sys/class/net", records[i].Bridge)); err != nil {
			records[i].Note = "接口不存在（可能已手动删除或未重建）"
		} else if records[i].Note == "接口不存在（可能已手动删除或未重建）" {
			records[i].Note = ""
		}
	}
	// Heal DNS on already-created bridges (common after inherit→manual with empty dns).
	if backend == "nmcli" {
		if fixed := s.repairManagedBridgeDNS(ctx, records); fixed {
			records = s.loadHostBridgeRecords()
		}
	}
	out := HostBridgeListResult{
		Enabled: true,
		Backend: backend,
		Bridges: records,
		Pending: s.getHostBridgePending(),
	}
	// Candidates for create when nmcli device list is empty (ip backend / degraded env).
	if backend == "ip" {
		out.Candidates = listBridgeCandidateDevices()
	} else if devices, err := listNetworkConfigDevices(ctx); err == nil {
		var cands []NetworkConfigDevice
		for _, d := range devices {
			if strings.EqualFold(d.Type, "ethernet") {
				cands = append(cands, d)
			}
		}
		out.Candidates = cands
	} else {
		out.Candidates = listBridgeCandidateDevices()
	}
	return out
}

func (s *Service) CreateHostBridge(ctx context.Context, req HostBridgeCreateRequest) HostBridgeOpResult {
	req.Device = strings.TrimSpace(req.Device)
	req.Bridge = strings.TrimSpace(req.Bridge)
	req.Method = strings.TrimSpace(req.Method)
	if req.Method == "" {
		req.Method = "inherit"
	}
	req.Address = strings.TrimSpace(req.Address)
	req.Gateway = strings.TrimSpace(req.Gateway)
	req.DNS = normalizeDNS(req.DNS)
	// IPv6 is always auto for host bridges (not user-configurable).
	req.IPv6Method = "auto"
	req.IPv6Address = ""

	backend := hostBridgeBackend()
	if backend == "" {
		return HostBridgeOpResult{Error: "无法创建网桥：需要懒猫系统 nmcli 接口（lzc-apis）或本机 nmcli"}
	}
	if req.Device == "" {
		return HostBridgeOpResult{Error: "device required"}
	}
	if isUnsafeNetworkDevice(req.Device) {
		return HostBridgeOpResult{Error: "拒绝操作虚拟或不安全网卡"}
	}

	var dev NetworkConfigDevice
	if backend == "nmcli" {
		// User-triggered mutation: refresh the process-owned inventory before
		// validating and changing the selected device.
		invalidateHostNetworkDeviceInventoryCache()
		devices, err := listNetworkConfigDevices(ctx)
		if err != nil {
			return HostBridgeOpResult{Device: req.Device, Error: err.Error()}
		}
		var ok bool
		dev, ok = findNetworkConfigDevice(devices, req.Device)
		if !ok {
			return HostBridgeOpResult{Device: req.Device, Error: "网卡不可配置、未连接，或已是桥端口"}
		}
		if !strings.EqualFold(dev.Type, "ethernet") {
			return HostBridgeOpResult{Device: req.Device, Error: "仅支持有线网卡（Wi-Fi 客户端模式通常无法做 L2 桥接）"}
		}
	} else {
		// ip backend: accept ethernet-like host NICs.
		if !isEthernetLikeIface(req.Device) {
			return HostBridgeOpResult{Device: req.Device, Error: "仅支持有线网卡（Wi-Fi 客户端模式通常无法做 L2 桥接）"}
		}
		if isIfaceBridgePort(req.Device) {
			return HostBridgeOpResult{Device: req.Device, Error: "该网卡已是网桥端口"}
		}
		dev = NetworkConfigDevice{Device: req.Device, Type: "ethernet"}
		if v4, _, gw := readIfaceIPv4AndGateway(req.Device); v4 != "" {
			dev.IPv4, dev.Gateway, dev.IPv4Method = v4, gw, "manual"
		} else {
			dev.IPv4Method = "auto"
		}
		dev.DNS = readResolvDNS()
	}

	// Resolve IP plan. Capture DNS/gateway BEFORE tearing anything down —
	// after connection down, resolv.conf and runtime DNS often go empty.
	method := req.Method
	address, gateway, dns := req.Address, req.Gateway, req.DNS
	var snapDNS, runtimeDNS, runtimeGW string
	if backend == "nmcli" && dev.Connection != "" {
		if snap, err := readNetworkConfigSnapshot(ctx, dev.Connection); err == nil {
			snapDNS = snap.DNS
		}
	}
	if runtime, err := readNetworkDeviceRuntimeConfig(ctx, req.Device); err == nil {
		runtimeDNS = runtime.DNS
		runtimeGW = runtime.Gateway
		if runtime.IPv4 != "" && address == "" && (method == "inherit" || method == "") {
			// filled below in inherit branch
		}
	}
	preResolvDNS := readResolvDNS()
	preDefaultGW := readDefaultIPv4Route().Gateway

	switch method {
	case "inherit":
		// Prefer runtime addresses so host keeps the same LAN identity after bridging.
		if runtime, err := readNetworkDeviceRuntimeConfig(ctx, req.Device); err == nil && runtime.IPv4 != "" {
			address = runtime.IPv4
			if gateway == "" {
				gateway = runtime.Gateway
			}
			if dns == "" {
				dns = runtime.DNS
			}
		} else {
			address = dev.IPv4
			if gateway == "" {
				gateway = dev.Gateway
			}
			if dns == "" {
				dns = dev.DNS
			}
		}
		// inherit → concrete auto/manual for NM bridge connection
		if snap, err := readNetworkConfigSnapshot(ctx, dev.Connection); err == nil && snap.Method == "auto" && address == "" {
			method = "auto"
		} else if address != "" {
			method = "manual"
		} else {
			method = "auto"
		}
	case "auto":
		address, gateway, dns = "", "", ""
	case "manual":
		if _, ipNet, err := net.ParseCIDR(address); err != nil || ipNet.IP.To4() == nil {
			return HostBridgeOpResult{Device: req.Device, Error: "手动模式需要合法 IPv4 CIDR，例如 192.168.1.10/24"}
		}
		if gateway != "" && net.ParseIP(gateway).To4() == nil {
			return HostBridgeOpResult{Device: req.Device, Error: "gateway 必须是 IPv4"}
		}
	default:
		return HostBridgeOpResult{Device: req.Device, Error: "method 必须是 inherit、auto 或 manual"}
	}

	// Manual (including inherit→manual): empty DNS after bridge up is catastrophic —
	// browser then hits ERR_NAME_NOT_RESOLVED for the app host / SSE.
	if method == "manual" {
		if gateway == "" {
			gateway = firstNonEmpty(runtimeGW, dev.Gateway, preDefaultGW)
		}
		dns = ensureHostBridgeDNS(dns, runtimeDNS, snapDNS, dev.DNS, preResolvDNS, gateway)
		if dns == "" {
			return HostBridgeOpResult{Device: req.Device, Error: "无法确定 DNS（运行时/连接配置/resolv 均为空），请改用手动模式填写 DNS"}
		}
		if gateway == "" {
			return HostBridgeOpResult{Device: req.Device, Error: "无法确定默认网关，请改用手动模式填写网关"}
		}
	}

	// IPv6 method fixed to auto (SLAAC/DHCPv6 via NetworkManager).
	ipv6Method := "auto"
	ipv6Address := ""

	bridgeName, err := resolveHostBridgeName(req.Bridge, req.Device)
	if err != nil {
		return HostBridgeOpResult{Device: req.Device, Error: err.Error()}
	}
	if _, err := os.Stat(filepath.Join("/sys/class/net", bridgeName)); err == nil {
		return HostBridgeOpResult{Device: req.Device, Bridge: bridgeName, Error: "网桥接口已存在: " + bridgeName}
	}

	// One managed bridge per physical device
	for _, rec := range s.loadHostBridgeRecords() {
		if rec.Device == req.Device {
			return HostBridgeOpResult{Device: req.Device, Bridge: rec.Bridge, Error: "该网卡已有托管网桥 " + rec.Bridge + "，请先拆除"}
		}
		if rec.Bridge == bridgeName {
			return HostBridgeOpResult{Device: req.Device, Bridge: bridgeName, Error: "网桥名已被托管: " + bridgeName}
		}
	}

	id, err := s.reserveNetworkMutation(networkMutationBridge, bridgeName)
	if err != nil {
		return HostBridgeOpResult{Device: req.Device, Bridge: bridgeName, Error: err.Error()}
	}
	activated := false
	defer func() {
		if !activated {
			s.abortNetworkMutation(id)
		}
	}()

	if backend == "ip" {
		result := s.createHostBridgeViaIP(ctx, req, bridgeName, method, address, gateway, dns, id)
		activated = result.OK
		return result
	}

	origSnap, err := readNetworkConfigSnapshot(ctx, dev.Connection)
	if err != nil {
		return HostBridgeOpResult{Device: req.Device, Error: "读取原连接失败: " + err.Error()}
	}

	bridgeConn := bridgeName
	portConn := bridgeName + "-port"
	if len(portConn) > 64 {
		portConn = bridgeName + "-p"
	}

	var logs []string
	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		logs = append(logs, line)
		logger.Info("host-bridge: %s", line)
	}

	// Disable autoconnect on original so NM won't race the bridge.
	if out, err := nmcli(ctx, []string{"connection", "modify", origSnap.Connection, "connection.autoconnect", "no"}, 8*time.Second); err != nil {
		return HostBridgeOpResult{Device: req.Device, Output: joinLogs(logs, out), Error: "关闭原连接自动连接失败: " + err.Error()}
	}
	logf("disabled autoconnect on %s", origSnap.Connection)

	// Create bridge connection
	createArgs := []string{
		"connection", "add",
		"type", "bridge",
		"ifname", bridgeName,
		"con-name", bridgeConn,
		"bridge.stp", "no",
		"connection.autoconnect", "yes",
	}
	if method == "auto" {
		createArgs = append(createArgs, "ipv4.method", "auto")
	} else {
		createArgs = append(createArgs,
			"ipv4.method", "manual",
			"ipv4.addresses", address,
			"ipv4.gateway", gateway,
			"ipv4.dns", dns,
		)
	}
	createArgs = append(createArgs, "ipv6.method", "auto")
	if out, err := nmcli(ctx, createArgs, 12*time.Second); err != nil {
		_, _ = nmcli(ctx, []string{"connection", "modify", origSnap.Connection, "connection.autoconnect", "yes"}, 5*time.Second)
		return HostBridgeOpResult{Device: req.Device, Bridge: bridgeName, Output: joinLogs(logs, out), Error: "创建网桥连接失败: " + err.Error()}
	}
	logf("created bridge connection %s", bridgeConn)

	// Create port (slave) for physical NIC
	portArgs := []string{
		"connection", "add",
		"type", "ethernet",
		"ifname", req.Device,
		"con-name", portConn,
		"master", bridgeName,
		"slave-type", "bridge",
		"connection.autoconnect", "yes",
	}
	if out, err := nmcli(ctx, portArgs, 12*time.Second); err != nil {
		_, _ = nmcli(ctx, []string{"connection", "delete", bridgeConn}, 8*time.Second)
		_, _ = nmcli(ctx, []string{"connection", "modify", origSnap.Connection, "connection.autoconnect", "yes"}, 5*time.Second)
		return HostBridgeOpResult{Device: req.Device, Bridge: bridgeName, Output: joinLogs(logs, out), Error: "创建桥端口连接失败: " + err.Error()}
	}
	logf("created port connection %s master %s", portConn, bridgeName)

	// Release the physical NIC from the original profile before activating the bridge.
	if out, err := nmcli(ctx, []string{"connection", "down", origSnap.Connection}, 15*time.Second); err != nil {
		logf("original down warning: %v %s", err, out)
	} else {
		logf("down original connection %s", origSnap.Connection)
	}

	// Bring bridge up first (critical path for host connectivity).
	if out, err := nmcli(ctx, []string{"connection", "up", bridgeConn}, 20*time.Second); err != nil {
		_ = s.cleanupHostBridgeConnections(ctx, bridgeConn, portConn, origSnap.Connection)
		return HostBridgeOpResult{Device: req.Device, Bridge: bridgeName, Output: joinLogs(logs, out), Error: "激活网桥失败: " + err.Error()}
	}
	logf("activated bridge %s", bridgeName)

	// Port is usually auto-activated via master; keep this short.
	if out, err := nmcli(ctx, []string{"connection", "up", portConn}, 8*time.Second); err != nil {
		logf("port up warning: %v %s", err, out)
	}

	// Prefer fast DNS fix over a second full connection re-up (extra ~25s outage).
	if method == "manual" {
		if out, err := ensureNMBridgeDNSAfterUp(ctx, bridgeConn, dns, gateway); err != nil {
			logf("dns ensure warning: %v %s", err, out)
		} else if out != "" {
			logf("dns ensure: %s", out)
		}
	} else if method == "auto" && !usableResolvDNS() {
		if werr := writeResolvDNS(ensureHostBridgeDNS("", readResolvDNS(), gateway)); werr != nil {
			logf("seed resolv warning: %v", werr)
		} else {
			logf("seeded resolv while DHCP settles")
		}
	}

	until := time.Now().Add(hostBridgeRollbackDelay)
	rec := HostBridgeRecord{
		Bridge:           bridgeName,
		Device:           req.Device,
		Backend:          "nmcli",
		BridgeConnection: bridgeConn,
		PortConnection:   portConn,
		OriginalConn:     origSnap.Connection,
		Method:           method,
		Address:          address,
		Gateway:          gateway,
		DNS:              dns,
		IPv6Method:       ipv6Method,
		IPv6Address:      ipv6Address,
		CreatedAt:        localTimestamp(),
		RollbackID:       id,
		RollbackUntil:    until.Format(time.DateTime),
		Confirmed:        false,
		Note:             "待确认：3 分钟内未确认将自动回滚",
	}
	records := s.loadHostBridgeRecords()
	records = append(records, rec)
	if err := s.saveHostBridgeRecords(records); err != nil {
		logf("persist warning: %v", err)
	}

	rb := &hostBridgeRollback{
		ID:     id,
		Bridge: bridgeName,
		Device: req.Device,
		Record: rec,
		Until:  until,
	}
	if err := s.activateNetworkMutation(&networkMutation{
		ID: id, Kind: networkMutationBridge, Target: bridgeName, Until: until, Bridge: rb,
	}); err != nil {
		out, restoreErr := s.teardownHostBridge(ctx, rec, true)
		if restoreErr != nil {
			return HostBridgeOpResult{Device: req.Device, Bridge: bridgeName, Output: out, Error: fmt.Sprintf("登记网桥变更失败: %v；恢复原连接失败: %v", err, restoreErr)}
		}
		return HostBridgeOpResult{Device: req.Device, Bridge: bridgeName, Output: out, Error: "登记网桥变更失败: " + err.Error()}
	}
	activated = true
	s.scheduleNetworkMutationRollback(s.networkMutationSnapshot())
	verification := s.verifyNetworkMutation(ctx, id)
	if verification.Status == "failed" {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		rollbackOut, rollbackErr := s.rollbackHostBridge(rollbackCtx, id, "verification_rollback")
		cancel()
		errText := "网桥未通过应用后验证，已自动回滚"
		if rollbackErr != nil {
			errText = "网桥未通过应用后验证，自动回滚失败: " + rollbackErr.Error()
		}
		return HostBridgeOpResult{Device: req.Device, Bridge: bridgeName, Output: strings.TrimSpace(strings.Join([]string{strings.Join(logs, "\n"), rollbackOut}, "\n")), Error: errText, Verification: &verification}
	}

	s.auditHostBridge(map[string]any{
		"action": "create", "bridge": bridgeName, "device": req.Device,
		"method": method, "address": address, "rollback_id": id, "ok": true,
	})

	return HostBridgeOpResult{
		OK:            true,
		Bridge:        bridgeName,
		Device:        req.Device,
		RollbackID:    id,
		RollbackUntil: until.Format(time.DateTime),
		Output:        strings.Join(logs, "\n"),
		Note:          "网桥已创建，请在 3 分钟内确认；超时自动回滚",
		Verification:  &verification,
	}
}

func (s *Service) ConfirmHostBridge(id string) HostBridgeOpResult {
	id = strings.TrimSpace(id)
	mutation, err := s.getNetworkMutation(id, networkMutationBridge)
	if err != nil || mutation.Bridge == nil {
		return HostBridgeOpResult{Error: "没有匹配的待确认网桥变更"}
	}
	rb := mutation.Bridge
	bridge, device := rb.Bridge, rb.Device

	// Mark confirmed in inventory
	records := s.loadHostBridgeRecords()
	for i := range records {
		if records[i].Bridge == bridge {
			records[i].Confirmed = true
			records[i].RollbackID = ""
			records[i].RollbackUntil = ""
			records[i].Note = "已确认：可将网卡桥接到 " + bridge
		}
	}
	if err := s.saveHostBridgeRecords(records); err != nil {
		return HostBridgeOpResult{Bridge: bridge, Device: device, Error: "保存网桥确认状态失败: " + err.Error()}
	}
	if _, err := s.confirmNetworkMutation(id, networkMutationBridge); err != nil {
		return HostBridgeOpResult{Bridge: bridge, Device: device, Error: err.Error()}
	}

	s.auditHostBridge(map[string]any{"action": "confirm", "id": id, "bridge": bridge, "device": device, "ok": true})
	return HostBridgeOpResult{OK: true, Bridge: bridge, Device: device, Note: "已确认网桥可用，不再自动回滚"}
}

func (s *Service) RollbackHostBridge(ctx context.Context, id string) HostBridgeOpResult {
	out, err := s.rollbackHostBridge(ctx, strings.TrimSpace(id), "manual_rollback")
	if err != nil {
		return HostBridgeOpResult{OK: false, Output: out, Error: err.Error()}
	}
	return HostBridgeOpResult{OK: true, Output: out, Note: "已拆除网桥并尝试恢复原网卡连接"}
}

func (s *Service) DissolveHostBridge(ctx context.Context, bridge string) HostBridgeOpResult {
	bridge = strings.TrimSpace(bridge)
	if bridge == "" {
		return HostBridgeOpResult{Error: "bridge required"}
	}
	if !isManagedHostBridgeName(bridge) {
		return HostBridgeOpResult{Error: "只能拆除 netwatch 托管网桥（nw- 前缀）"}
	}

	var rec *HostBridgeRecord
	records := s.loadHostBridgeRecords()
	for i := range records {
		if records[i].Bridge == bridge {
			rec = &records[i]
			break
		}
	}
	if rec == nil {
		return HostBridgeOpResult{Bridge: bridge, Error: "未找到托管记录: " + bridge}
	}

	// Dissolving an unconfirmed bridge is the same transaction as rolling it
	// back; keep the mutation if teardown fails so the user can retry.
	s.network.mu.Lock()
	pendingID := ""
	if rb := s.network.bridgeRollback; rb != nil && rb.Bridge == bridge {
		pendingID = rb.ID
	}
	s.network.mu.Unlock()
	if pendingID != "" {
		return s.RollbackHostBridge(ctx, pendingID)
	}

	out, err := s.teardownHostBridge(ctx, *rec, true)
	if err != nil {
		s.auditHostBridge(map[string]any{"action": "dissolve", "bridge": bridge, "ok": false, "error": err.Error()})
		return HostBridgeOpResult{Bridge: bridge, Device: rec.Device, Output: out, Error: err.Error()}
	}

	// Drop from inventory
	next := make([]HostBridgeRecord, 0, len(records))
	for _, r := range records {
		if r.Bridge != bridge {
			next = append(next, r)
		}
	}
	_ = s.saveHostBridgeRecords(next)
	s.auditHostBridge(map[string]any{"action": "dissolve", "bridge": bridge, "device": rec.Device, "ok": true})
	return HostBridgeOpResult{OK: true, Bridge: bridge, Device: rec.Device, Output: out, Note: "网桥已拆除，原网卡连接已尝试恢复"}
}

func (s *Service) GetHostBridgePending() HostBridgePending {
	if p := s.getHostBridgePending(); p != nil {
		return *p
	}
	return HostBridgePending{Pending: false}
}

func (s *Service) getHostBridgePending() *HostBridgePending {
	s.ensureHostBridgeRollbackRestored()
	s.network.mu.Lock()
	defer s.network.mu.Unlock()
	rb := s.network.bridgeRollback
	if rb == nil {
		return nil
	}
	remain := int(time.Until(rb.Until).Seconds())
	if remain < 0 {
		remain = 0
	}
	return &HostBridgePending{
		Pending:       true,
		ID:            rb.ID,
		Bridge:        rb.Bridge,
		Device:        rb.Device,
		Method:        rb.Record.Method,
		Address:       rb.Record.Address,
		Gateway:       rb.Record.Gateway,
		DNS:           rb.Record.DNS,
		RollbackUntil: rb.Until.Format(time.DateTime),
		RemainingSec:  remain,
	}
}

// ensureHostBridgeRollbackRestored rebuilds in-memory pending + timer from
// host_bridges.json after process restart / disconnect recovery.
// Unconfirmed bridges past the 3-minute window are torn down asynchronously.
func (s *Service) ensureHostBridgeRollbackRestored() {
	s.network.mu.Lock()
	if s.network.active != nil || s.network.bridgeRollback != nil {
		s.network.mu.Unlock()
		return
	}
	s.network.mu.Unlock()

	records := s.loadHostBridgeRecords()
	active, until, expired := pickHostBridgePendingRestore(records, time.Now())
	for _, rec := range expired {
		r := rec
		go s.autoExpireUnconfirmedHostBridge(r)
	}
	if active == nil {
		return
	}

	id := strings.TrimSpace(active.RollbackID)
	if id == "" {
		id = newRollbackID()
	}
	rb := &hostBridgeRollback{
		ID:     id,
		Bridge: active.Bridge,
		Device: active.Device,
		Record: *active,
		Until:  until,
	}
	delay := time.Until(until)
	if delay < time.Second {
		delay = time.Second
	}
	rb.Timer = time.AfterFunc(delay, func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if out, err := s.rollbackHostBridge(rollbackCtx, id, "auto_rollback"); err != nil {
			logger.Warn("host bridge auto rollback failed id=%s bridge=%s err=%v out=%q", id, active.Bridge, err, out)
		}
	})

	s.network.mu.Lock()
	if s.network.active == nil && s.network.bridgeRollback == nil {
		s.network.active = &networkMutation{
			Version: 1, ID: id, Kind: networkMutationBridge, Target: active.Bridge,
			Status: networkMutationPending, StartedAt: until.Add(-hostBridgeRollbackDelay),
			Until: until, Bridge: rb,
		}
		s.syncNetworkMutationViewsLocked()
		if err := s.persistNetworkMutationLocked(); err != nil {
			logger.Warn("persist restored host bridge mutation: %v", err)
		}
		// Persist restored id/until so subsequent restarts keep the same deadline.
		needSave := false
		for i := range records {
			if records[i].Bridge == active.Bridge {
				if records[i].RollbackID == "" {
					records[i].RollbackID = id
					needSave = true
				}
				if records[i].RollbackUntil == "" {
					records[i].RollbackUntil = until.Format(time.DateTime)
					needSave = true
				}
			}
		}
		s.network.mu.Unlock()
		if needSave {
			_ = s.saveHostBridgeRecords(records)
		}
		logger.Info("host bridge pending restored from disk bridge=%s device=%s until=%s", active.Bridge, active.Device, until.Format(time.DateTime))
		return
	}
	// Race: another create/restore won; drop our timer.
	if rb.Timer != nil {
		rb.Timer.Stop()
	}
	s.network.mu.Unlock()
}

func (s *Service) autoExpireUnconfirmedHostBridge(rec HostBridgeRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	// Skip if already confirmed/removed or currently tracked as pending.
	s.network.mu.Lock()
	if rb := s.network.bridgeRollback; rb != nil && rb.Bridge == rec.Bridge {
		s.network.mu.Unlock()
		return
	}
	s.network.mu.Unlock()

	records := s.loadHostBridgeRecords()
	found := false
	for _, r := range records {
		if r.Bridge == rec.Bridge {
			if r.Confirmed {
				return
			}
			rec = r
			found = true
			break
		}
	}
	if !found {
		return
	}
	out, err := s.teardownHostBridge(ctx, rec, true)
	next := make([]HostBridgeRecord, 0, len(records))
	for _, r := range records {
		if r.Bridge != rec.Bridge {
			next = append(next, r)
		}
	}
	_ = s.saveHostBridgeRecords(next)
	s.auditHostBridge(map[string]any{
		"action": "auto_rollback", "bridge": rec.Bridge, "device": rec.Device,
		"ok": err == nil, "error": errString(err), "note": "expired_unconfirmed_on_restore",
	})
	if err != nil {
		logger.Warn("host bridge expire teardown failed bridge=%s err=%v out=%q", rec.Bridge, err, out)
	} else {
		logger.Info("host bridge expired unconfirmed removed bridge=%s", rec.Bridge)
	}
}

// pickHostBridgePendingRestore chooses the newest still-valid unconfirmed bridge
// and lists expired ones for async teardown. Pure helper for tests.
func pickHostBridgePendingRestore(records []HostBridgeRecord, now time.Time) (active *HostBridgeRecord, until time.Time, expired []HostBridgeRecord) {
	var best *HostBridgeRecord
	var bestUntil time.Time
	var bestCreated time.Time
	for i := range records {
		r := records[i]
		if r.Confirmed || strings.TrimSpace(r.Bridge) == "" {
			continue
		}
		deadline, created := hostBridgeRecordDeadline(r)
		if !deadline.After(now) {
			expired = append(expired, r)
			continue
		}
		if best == nil || created.After(bestCreated) {
			cp := r
			best = &cp
			bestUntil = deadline
			bestCreated = created
		}
	}
	return best, bestUntil, expired
}

func hostBridgeRecordDeadline(r HostBridgeRecord) (deadline time.Time, created time.Time) {
	now := time.Now()
	if ts := strings.TrimSpace(r.RollbackUntil); ts != "" {
		if t, err := time.ParseInLocation(time.DateTime, ts, time.Local); err == nil {
			deadline = t
		} else if t, err := time.Parse(time.RFC3339, ts); err == nil {
			deadline = t
		}
	}
	if ts := strings.TrimSpace(r.CreatedAt); ts != "" {
		if t, err := time.ParseInLocation(time.DateTime, ts, time.Local); err == nil {
			created = t
		} else if t, err := time.Parse(time.RFC3339, ts); err == nil {
			created = t
		}
	}
	if deadline.IsZero() {
		if created.IsZero() {
			// Unknown age: give a fresh window so reconnect can still confirm.
			created = now
			deadline = now.Add(hostBridgeRollbackDelay)
		} else {
			deadline = created.Add(hostBridgeRollbackDelay)
		}
	} else if created.IsZero() {
		created = deadline.Add(-hostBridgeRollbackDelay)
	}
	return deadline, created
}

func (s *Service) rollbackHostBridge(ctx context.Context, id, action string) (string, error) {
	mutation, err := s.beginNetworkMutationRollback(id, networkMutationBridge)
	if err != nil || mutation.Bridge == nil {
		return "", errors.New("没有待回滚的网桥变更")
	}
	rb := mutation.Bridge
	rec := rb.Record

	out, err := s.teardownHostBridge(ctx, rec, true)
	// Remove inventory entry
	if err == nil {
		records := s.loadHostBridgeRecords()
		next := make([]HostBridgeRecord, 0, len(records))
		for _, r := range records {
			if r.Bridge != rec.Bridge {
				next = append(next, r)
			}
		}
		if saveErr := s.saveHostBridgeRecords(next); saveErr != nil {
			err = fmt.Errorf("网桥已拆除，但保存网桥记录失败: %w", saveErr)
		}
	}
	s.finishNetworkMutationRollback(rb.ID, err)

	s.auditHostBridge(map[string]any{
		"action": action, "id": rb.ID, "bridge": rec.Bridge, "device": rec.Device,
		"ok": err == nil, "error": errString(err),
	})
	return out, err
}

func (s *Service) teardownHostBridge(ctx context.Context, rec HostBridgeRecord, restoreOriginal bool) (string, error) {
	if rec.Backend == "ip" || (rec.Backend == "" && rec.BridgeConnection == "" && strings.HasPrefix(rec.OriginalConn, "ip:")) {
		return s.teardownHostBridgeViaIP(rec)
	}
	var logs []string
	appendOut := func(label, out string, err error) {
		line := label
		if out != "" {
			line += ": " + out
		}
		if err != nil {
			line += " err=" + err.Error()
		}
		logs = append(logs, line)
	}

	// Down bridge first
	out, err := nmcli(ctx, []string{"connection", "down", rec.BridgeConnection}, 15*time.Second)
	appendOut("bridge down", out, err)

	// Delete port then bridge connections
	if rec.PortConnection != "" {
		out, err = nmcli(ctx, []string{"connection", "delete", rec.PortConnection}, 12*time.Second)
		appendOut("delete port", out, err)
	}
	if rec.BridgeConnection != "" {
		out, err = nmcli(ctx, []string{"connection", "delete", rec.BridgeConnection}, 12*time.Second)
		appendOut("delete bridge", out, err)
	}

	// Best-effort: remove leftover kernel iface
	findBinPaths()
	if ipPath != "" {
		out2, err2 := execCmd(ipPath, 5*time.Second, "link", "delete", rec.Bridge)
		if err2 == nil {
			appendOut("ip link delete", out2, nil)
		}
	}

	var restoreErr error
	if restoreOriginal && rec.OriginalConn != "" {
		out, err = nmcli(ctx, []string{"connection", "modify", rec.OriginalConn, "connection.autoconnect", "yes"}, 8*time.Second)
		appendOut("restore autoconnect", out, err)
		out, err = nmcli(ctx, []string{"connection", "up", rec.OriginalConn}, 25*time.Second)
		appendOut("restore connection up", out, err)
		restoreErr = err
	}

	joined := strings.Join(logs, "\n")
	if restoreErr != nil {
		return joined, fmt.Errorf("拆除完成但恢复原连接失败: %w", restoreErr)
	}
	return joined, nil
}

func (s *Service) cleanupHostBridgeConnections(ctx context.Context, bridgeConn, portConn, originalConn string) error {
	_, _ = nmcli(ctx, []string{"connection", "delete", portConn}, 8*time.Second)
	_, _ = nmcli(ctx, []string{"connection", "delete", bridgeConn}, 8*time.Second)
	if originalConn != "" {
		_, _ = nmcli(ctx, []string{"connection", "modify", originalConn, "connection.autoconnect", "yes"}, 5*time.Second)
		_, _ = nmcli(ctx, []string{"connection", "up", originalConn}, 15*time.Second)
	}
	return nil
}

func (s *Service) loadHostBridgeRecords() []HostBridgeRecord {
	path := hostBridgesPath(s.cfg.DataDir)
	body, err := os.ReadFile(path)
	if err != nil {
		return []HostBridgeRecord{}
	}
	var records []HostBridgeRecord
	if err := json.Unmarshal(body, &records); err != nil {
		return []HostBridgeRecord{}
	}
	return records
}

func (s *Service) saveHostBridgeRecords(records []HostBridgeRecord) error {
	if records == nil {
		records = []HostBridgeRecord{}
	}
	return writeJSONFile(hostBridgesPath(s.cfg.DataDir), records, true)
}

func (s *Service) auditHostBridge(event map[string]any) {
	if event == nil {
		return
	}
	if _, ok := event["timestamp"]; !ok {
		event["timestamp"] = localTimestamp()
	}
	path := filepath.Join(s.cfg.DataDir, "host_bridge_audit.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(event)
}

func resolveHostBridgeName(requested, device string) (string, error) {
	name := strings.TrimSpace(requested)
	if name == "" {
		name = defaultHostBridgeName(device)
	} else {
		// Prefix nw- is fixed and not user-configurable. Accept full name or bare suffix.
		if strings.HasPrefix(name, hostBridgeNamePrefix) {
			// ok
		} else if strings.HasPrefix(strings.ToLower(name), hostBridgeNamePrefix) {
			// Normalize accidental mixed-case prefix.
			name = hostBridgeNamePrefix + name[len(hostBridgeNamePrefix):]
		} else {
			name = hostBridgeNamePrefix + name
		}
	}
	if len(name) > hostBridgeNameMax {
		return "", fmt.Errorf("网桥名最长 %d 字符（含固定前缀 %s）", hostBridgeNameMax, hostBridgeNamePrefix)
	}
	if name == hostBridgeNamePrefix {
		return "", errors.New("网桥名后缀不能为空")
	}
	if !hostBridgeNameRE.MatchString(name) {
		return "", errors.New("网桥名不合法：字母或数字开头，仅允许 a-z A-Z 0-9 . _ -，最长 15")
	}
	if !isManagedHostBridgeName(name) {
		return "", errors.New("网桥名必须以 nw- 开头（netwatch 托管前缀，不可修改）")
	}
	// Reject names that look like other system ifaces even under nw- (paranoid).
	if isUnsafeNetworkDevice(name) {
		return "", errors.New("网桥名与系统保留前缀冲突")
	}
	return name, nil
}

func defaultHostBridgeName(device string) string {
	dev := sanitizeIfaceToken(device)
	if dev == "" {
		dev = "0"
	}
	name := hostBridgeNamePrefix + dev
	if len(name) > hostBridgeNameMax {
		name = name[:hostBridgeNameMax]
	}
	// trim trailing separators after cut
	name = strings.TrimRight(name, ".-_")
	if name == hostBridgeNamePrefix || name == "" {
		name = "nw-br0"
	}
	return name
}

func isManagedHostBridgeName(name string) bool {
	return strings.HasPrefix(name, hostBridgeNamePrefix)
}

func sanitizeIfaceToken(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func joinLogs(logs []string, extra string) string {
	extra = strings.TrimSpace(extra)
	if extra == "" {
		return strings.Join(logs, "\n")
	}
	if len(logs) == 0 {
		return extra
	}
	return strings.Join(logs, "\n") + "\n" + extra
}

// repairManagedBridgeDNS fills empty/missing DNS on managed bridge NM profiles.
// Returns true if any profile was modified. Safe to call from List (idempotent).
func (s *Service) repairManagedBridgeDNS(ctx context.Context, records []HostBridgeRecord) bool {
	if !usableResolvDNS() {
		// Host already broken — try to repair from record inventory + gateway fallbacks.
	}
	changed := false
	next := make([]HostBridgeRecord, 0, len(records))
	for _, rec := range records {
		if rec.Backend != "" && rec.Backend != "nmcli" {
			next = append(next, rec)
			continue
		}
		if rec.BridgeConnection == "" {
			next = append(next, rec)
			continue
		}
		if _, err := os.Stat(filepath.Join("/sys/class/net", rec.Bridge)); err != nil {
			next = append(next, rec)
			continue
		}
		// Profile DNS
		snap, err := readNetworkConfigSnapshot(ctx, rec.BridgeConnection)
		profileDNS := ""
		if err == nil {
			profileDNS = snap.DNS
		}
		want := ensureHostBridgeDNS(rec.DNS, profileDNS, readResolvDNS(), rec.Gateway)
		if want == "" {
			next = append(next, rec)
			continue
		}
		// Skip if profile already has usable non-loopback DNS matching inventory.
		if ensureHostBridgeDNS(profileDNS) != "" && usableResolvDNS() {
			if rec.DNS == "" {
				rec.DNS = ensureHostBridgeDNS(profileDNS)
				changed = true
			}
			next = append(next, rec)
			continue
		}
		out, err := ensureNMBridgeDNSAfterUp(ctx, rec.BridgeConnection, want, rec.Gateway)
		if err != nil {
			logger.Warn("repair bridge dns bridge=%s conn=%s err=%v out=%q", rec.Bridge, rec.BridgeConnection, err, out)
			next = append(next, rec)
			continue
		}
		logger.Info("repaired bridge dns bridge=%s dns=%s", rec.Bridge, want)
		rec.DNS = want
		if rec.Note == "" || strings.Contains(rec.Note, "DNS") {
			rec.Note = "已自动补齐 DNS（修复 ERR_NAME_NOT_RESOLVED）"
		}
		changed = true
		next = append(next, rec)
	}
	if changed {
		_ = s.saveHostBridgeRecords(next)
	}
	return changed
}

// ensureHostBridgeDNS merges candidate DNS lists and drops unusable entries
// (empty, non-IPv4, loopback like 127.0.0.53 from systemd-resolved stub).
// Order: explicit → runtime → connection profile → resolv.conf → gateway →
// public fallbacks (AliDNS + DNSPod, common on Lazycat domestic networks).
func ensureHostBridgeDNS(candidates ...string) string {
	seen := map[string]struct{}{}
	var out []string
	add := func(raw string) {
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == ';'
		}) {
			part = strings.TrimSpace(part)
			ip := net.ParseIP(part)
			if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() {
				continue
			}
			v := ip.To4().String()
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
			if len(out) >= 3 {
				return
			}
		}
	}
	for _, c := range candidates {
		add(c)
		if len(out) >= 3 {
			break
		}
	}
	if len(out) == 0 {
		// Last-resort public resolvers so the host never ends up with zero DNS
		// after enslaving the only uplink NIC.
		add("223.5.5.5,119.29.29.29")
	}
	return strings.Join(out, ",")
}

// ensureNMBridgeDNSAfterUp re-applies DNS (and gateway if missing) on the bridge
// profile. Prefer a direct resolv.conf write over a second full "connection up",
// which can add another ~25s outage right after the first activation.
func ensureNMBridgeDNSAfterUp(ctx context.Context, bridgeConn, dns, gateway string) (string, error) {
	dns = ensureHostBridgeDNS(dns, readResolvDNS(), gateway)
	if dns == "" {
		return "", errors.New("no usable dns")
	}
	// Always write DNS onto the profile so reboot/reconnect keeps it.
	args := []string{"connection", "modify", bridgeConn, "ipv4.dns", dns, "ipv4.ignore-auto-dns", "no"}
	if gateway != "" && net.ParseIP(gateway).To4() != nil {
		args = append(args, "ipv4.gateway", gateway)
	}
	out1, err := nmcli(ctx, args, 8*time.Second)
	if err != nil {
		return out1, err
	}
	if usableResolvDNS() {
		return strings.TrimSpace(out1 + " (resolv ok)"), nil
	}
	// Fast path: patch resolv without re-activating the link.
	if werr := writeResolvDNS(dns); werr == nil && usableResolvDNS() {
		return strings.TrimSpace(out1 + " (resolv written)"), nil
	}
	// Last resort only — shorter timeout; client is already reconnecting.
	out2, err := nmcli(ctx, []string{"connection", "up", bridgeConn}, 12*time.Second)
	return strings.TrimSpace(strings.Join([]string{out1, out2}, "\n")), err
}

func usableResolvDNS() bool {
	return ensureHostBridgeDNS(readResolvDNS()) != "" && !resolvOnlyLoopback()
}

func resolvOnlyLoopback() bool {
	raw := readResolvDNSRaw()
	if raw == "" {
		return true
	}
	hasReal := false
	for _, part := range strings.Split(raw, ",") {
		ip := net.ParseIP(strings.TrimSpace(part))
		if ip == nil || ip.IsLoopback() {
			continue
		}
		if ip.To4() != nil {
			hasReal = true
		}
	}
	return !hasReal
}

// readResolvDNSRaw includes loopback resolvers (for diagnostics).
func readResolvDNSRaw() string {
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return ""
	}
	defer f.Close()
	var dns []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "nameserver") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && net.ParseIP(fields[1]) != nil {
			dns = append(dns, fields[1])
		}
	}
	return strings.Join(dns, ",")
}
