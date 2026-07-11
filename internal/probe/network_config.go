package probe

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"netwatch/internal/logger"
	"netwatch/internal/lzcsdk"
)

const networkConfigRollbackDelay = 60 * time.Second

type networkConfigSnapshot struct {
	Connection string `json:"connection"`
	Method     string `json:"method"`
	Addresses  string `json:"addresses"`
	Gateway    string `json:"gateway"`
	DNS        string `json:"dns"`
}

type networkConfigRollback struct {
	ID       string
	Device   string
	Request  NetworkConfigApplyRequest
	Previous NetworkConfigDevice
	Snapshot networkConfigSnapshot
	Timer    *time.Timer
	Until    time.Time
}

type networkConfigAuditEvent struct {
	Timestamp  string                     `json:"timestamp"`
	Action     string                     `json:"action"`
	ID         string                     `json:"id,omitempty"`
	Device     string                     `json:"device,omitempty"`
	Connection string                     `json:"connection,omitempty"`
	Request    *NetworkConfigApplyRequest `json:"request,omitempty"`
	Snapshot   *networkConfigSnapshot     `json:"snapshot,omitempty"`
	OK         bool                       `json:"ok"`
	Error      string                     `json:"error,omitempty"`
}

func (s *Service) ListNetworkConfigDevices(ctx context.Context) NetworkConfigDevicesResponse {
	devices, err := listNetworkConfigDevices(ctx)
	if err != nil {
		return NetworkConfigDevicesResponse{Enabled: true, Error: err.Error()}
	}
	return NetworkConfigDevicesResponse{Enabled: true, Devices: devices}
}

func (s *Service) ApplyNetworkConfig(ctx context.Context, req NetworkConfigApplyRequest) NetworkConfigApplyResult {
	req.Device = strings.TrimSpace(req.Device)
	req.Method = strings.TrimSpace(req.Method)
	if req.Method == "" {
		req.Method = "manual"
	}
	req.Address = strings.TrimSpace(req.Address)
	req.Gateway = strings.TrimSpace(req.Gateway)
	req.DNS = normalizeDNS(req.DNS)
	if err := validateNetworkConfigRequest(req); err != nil {
		return NetworkConfigApplyResult{Device: req.Device, Error: err.Error()}
	}

	devices, err := listNetworkConfigDevices(ctx)
	if err != nil {
		return NetworkConfigApplyResult{Device: req.Device, Error: err.Error()}
	}
	dev, ok := findNetworkConfigDevice(devices, req.Device)
	if !ok {
		return NetworkConfigApplyResult{Device: req.Device, Error: "网卡不可配置或未连接"}
	}
	if req.Method == "manual" {
		if err := checkIPv4Conflict(ctx, req.Device, req.Address); err != nil {
			return NetworkConfigApplyResult{Device: req.Device, Connection: dev.Connection, Error: err.Error()}
		}
	}

	snapshot, err := readNetworkConfigSnapshot(ctx, dev.Connection)
	if err != nil {
		return NetworkConfigApplyResult{Device: req.Device, Connection: dev.Connection, Error: err.Error()}
	}
	id := newRollbackID()

	args := networkConfigApplyArgs(dev.Connection, req)
	out1, err := nmcli(ctx, args, 10*time.Second)
	if err == nil {
		var out2 string
		out2, err = nmcli(ctx, []string{"device", "reapply", req.Device}, 20*time.Second)
		out1 = strings.TrimSpace(strings.Join([]string{out1, out2}, "\n"))
	}
	if err != nil {
		s.auditNetworkConfig(networkConfigAuditEvent{Action: "apply", ID: id, Device: req.Device, Connection: dev.Connection, Request: &req, Snapshot: &snapshot, OK: false, Error: err.Error()})
		return NetworkConfigApplyResult{Device: req.Device, Connection: dev.Connection, Error: err.Error()}
	}

	until := time.Now().Add(networkConfigRollbackDelay)
	rb := &networkConfigRollback{ID: id, Device: req.Device, Request: req, Previous: dev, Snapshot: snapshot, Until: until}
	rb.Timer = time.AfterFunc(networkConfigRollbackDelay, func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if out, err := s.rollbackNetworkConfig(rollbackCtx, id, "auto_rollback"); err != nil {
			logger.Warn("network config auto rollback failed id=%s device=%s err=%v out=%q", id, req.Device, err, out)
		}
	})

	s.control.netcfgMu.Lock()
	if old := s.control.rollbacks[req.Device]; old != nil && old.Timer != nil {
		old.Timer.Stop()
	}
	s.control.rollbacks[req.Device] = rb
	s.control.rollbacks[id] = rb
	s.control.netcfgMu.Unlock()

	s.auditNetworkConfig(networkConfigAuditEvent{Action: "apply", ID: id, Device: req.Device, Connection: dev.Connection, Request: &req, Snapshot: &snapshot, OK: true})
	return NetworkConfigApplyResult{OK: true, Device: req.Device, Connection: dev.Connection, RollbackID: id, RollbackUntil: until.Format(time.DateTime), Output: strings.TrimSpace(out1)}
}

func (s *Service) CheckNetworkConfigIP(ctx context.Context, req NetworkConfigIPCheckRequest) NetworkConfigIPCheckResult {
	req.Device = strings.TrimSpace(req.Device)
	req.Address = strings.TrimSpace(req.Address)
	if req.Device == "" || req.Address == "" {
		return NetworkConfigIPCheckResult{Error: "device and address required"}
	}
	ip, _, err := net.ParseCIDR(req.Address)
	if err != nil {
		if parsed := net.ParseIP(req.Address); parsed != nil {
			ip = parsed
		} else {
			return NetworkConfigIPCheckResult{Error: "invalid IPv4 address"}
		}
	}
	if ip == nil || ip.To4() == nil {
		return NetworkConfigIPCheckResult{Error: "invalid IPv4 address"}
	}
	if err := checkIPv4Conflict(ctx, req.Device, ip.String()+"/32"); err != nil {
		return NetworkConfigIPCheckResult{OK: true, Available: false, IP: ip.String(), Error: err.Error()}
	}
	return NetworkConfigIPCheckResult{OK: true, Available: true, IP: ip.String()}
}

func (s *Service) ConfirmNetworkConfig(id string) NetworkConfigConfirmResult {
	id = strings.TrimSpace(id)
	if id == "" {
		return NetworkConfigConfirmResult{Error: "rollback id required"}
	}
	s.control.netcfgMu.Lock()
	rb := s.control.rollbacks[id]
	if rb == nil {
		s.control.netcfgMu.Unlock()
		return NetworkConfigConfirmResult{ID: id, Error: "rollback task not found"}
	}
	if rb.Timer != nil {
		rb.Timer.Stop()
	}
	delete(s.control.rollbacks, id)
	delete(s.control.rollbacks, rb.Device)
	s.control.netcfgMu.Unlock()
	s.auditNetworkConfig(networkConfigAuditEvent{Action: "confirm", ID: id, Device: rb.Device, Connection: rb.Snapshot.Connection, OK: true})
	return NetworkConfigConfirmResult{OK: true, ID: id}
}

func (s *Service) GetNetworkConfigPending() NetworkConfigPendingResult {
	s.control.netcfgMu.Lock()
	defer s.control.netcfgMu.Unlock()
	now := time.Now()
	seen := map[*networkConfigRollback]struct{}{}
	for _, rb := range s.control.rollbacks {
		if rb == nil {
			continue
		}
		if _, ok := seen[rb]; ok {
			continue
		}
		seen[rb] = struct{}{}
		remaining := int(time.Until(rb.Until).Seconds())
		if remaining < 0 || now.After(rb.Until) {
			remaining = 0
		}
		return NetworkConfigPendingResult{
			Pending:       remaining > 0,
			ID:            rb.ID,
			Device:        rb.Device,
			Connection:    rb.Snapshot.Connection,
			Method:        rb.Request.Method,
			Address:       rb.Request.Address,
			Gateway:       rb.Request.Gateway,
			DNS:           rb.Request.DNS,
			PrevMethod:    rb.Previous.IPv4Method,
			PrevAddress:   rb.Previous.IPv4,
			PrevGateway:   rb.Previous.Gateway,
			PrevDNS:       rb.Previous.DNS,
			RollbackUntil: rb.Until.Format(time.DateTime),
			RemainingSec:  remaining,
		}
	}
	return NetworkConfigPendingResult{Pending: false}
}

func (s *Service) RollbackNetworkConfig(ctx context.Context, id string) NetworkConfigConfirmResult {
	out, err := s.rollbackNetworkConfig(ctx, id, "manual_rollback")
	if err != nil {
		return NetworkConfigConfirmResult{ID: id, Error: err.Error(), Output: out}
	}
	return NetworkConfigConfirmResult{OK: true, ID: id, Output: out}
}

func (s *Service) rollbackNetworkConfig(ctx context.Context, id, action string) (string, error) {
	id = strings.TrimSpace(id)
	s.control.netcfgMu.Lock()
	rb := s.control.rollbacks[id]
	if rb == nil {
		s.control.netcfgMu.Unlock()
		return "", errors.New("rollback task not found")
	}
	if rb.Timer != nil {
		rb.Timer.Stop()
	}
	delete(s.control.rollbacks, id)
	delete(s.control.rollbacks, rb.Device)
	s.control.netcfgMu.Unlock()

	out, err := restoreNetworkConfigSnapshot(ctx, rb.Device, rb.Snapshot)
	s.auditNetworkConfig(networkConfigAuditEvent{Action: action, ID: id, Device: rb.Device, Connection: rb.Snapshot.Connection, Snapshot: &rb.Snapshot, OK: err == nil, Error: errString(err)})
	return out, err
}

func listNetworkConfigDevices(ctx context.Context) ([]NetworkConfigDevice, error) {
	out, err := nmcli(ctx, []string{"-t", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device", "status"}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	var devices []NetworkConfigDevice
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := splitNMCLIFields(line)
		if len(fields) < 4 {
			continue
		}
		dev := NetworkConfigDevice{Device: fields[0], Type: fields[1], State: fields[2], Connection: fields[3]}
		if (dev.Type != "ethernet" && dev.Type != "wifi") || !strings.HasPrefix(dev.State, "connected") || dev.Connection == "" {
			continue
		}
		if isUnsafeNetworkDevice(dev.Device) {
			continue
		}
		if snap, err := readNetworkConfigSnapshot(ctx, dev.Connection); err == nil {
			dev.IPv4Method = snap.Method
			dev.IPv4 = snap.Addresses
			dev.Gateway = snap.Gateway
			dev.DNS = snap.DNS
		}
		if runtime, err := readNetworkDeviceRuntimeConfig(ctx, dev.Device); err == nil {
			if runtime.IPv4 != "" {
				dev.IPv4 = runtime.IPv4
			}
			if runtime.Gateway != "" {
				dev.Gateway = runtime.Gateway
			}
			if runtime.DNS != "" {
				dev.DNS = runtime.DNS
			}
		}
		devices = append(devices, dev)
	}
	return devices, nil
}

type networkDeviceRuntimeConfig struct {
	IPv4    string
	Gateway string
	DNS     string
}

func readNetworkDeviceRuntimeConfig(ctx context.Context, device string) (networkDeviceRuntimeConfig, error) {
	out, err := nmcli(ctx, []string{"-t", "-f", "IP4.ADDRESS,IP4.GATEWAY,IP4.DNS", "device", "show", device}, 5*time.Second)
	if err != nil {
		return networkDeviceRuntimeConfig{}, err
	}
	var cfg networkDeviceRuntimeConfig
	var dns []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := splitNMCLIFields(line)
		if len(fields) < 2 {
			continue
		}
		key := fields[0]
		value := strings.TrimSpace(strings.Join(fields[1:], ":"))
		switch {
		case strings.HasPrefix(key, "IP4.ADDRESS") && cfg.IPv4 == "":
			cfg.IPv4 = value
		case strings.HasPrefix(key, "IP4.GATEWAY") && cfg.Gateway == "":
			cfg.Gateway = value
		case strings.HasPrefix(key, "IP4.DNS") && value != "":
			dns = append(dns, value)
		}
	}
	if len(dns) > 0 {
		cfg.DNS = strings.Join(dns, ",")
	}
	return cfg, nil
}

func findNetworkConfigDevice(devices []NetworkConfigDevice, name string) (NetworkConfigDevice, bool) {
	for _, dev := range devices {
		if dev.Device == name {
			return dev, true
		}
	}
	return NetworkConfigDevice{}, false
}

func readNetworkConfigSnapshot(ctx context.Context, connection string) (networkConfigSnapshot, error) {
	connection = strings.TrimSpace(connection)
	if connection == "" {
		return networkConfigSnapshot{}, errors.New("empty connection")
	}
	out, err := nmcli(ctx, []string{"-g", "ipv4.method,ipv4.addresses,ipv4.gateway,ipv4.dns", "connection", "show", connection}, 5*time.Second)
	if err != nil {
		return networkConfigSnapshot{}, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for len(lines) < 4 {
		lines = append(lines, "")
	}
	return networkConfigSnapshot{Connection: connection, Method: strings.TrimSpace(lines[0]), Addresses: strings.TrimSpace(lines[1]), Gateway: strings.TrimSpace(lines[2]), DNS: strings.TrimSpace(lines[3])}, nil
}

func restoreNetworkConfigSnapshot(ctx context.Context, device string, snap networkConfigSnapshot) (string, error) {
	args := []string{"connection", "modify", snap.Connection,
		"ipv4.method", valueOrDefault(snap.Method, "auto"),
		"ipv4.addresses", snap.Addresses,
		"ipv4.gateway", snap.Gateway,
		"ipv4.dns", snap.DNS,
	}
	out1, err := nmcli(ctx, args, 10*time.Second)
	if err != nil {
		return strings.TrimSpace(out1), err
	}
	out2, err := nmcli(ctx, []string{"device", "reapply", device}, 20*time.Second)
	return strings.TrimSpace(strings.Join([]string{out1, out2}, "\n")), err
}

func validateNetworkConfigRequest(req NetworkConfigApplyRequest) error {
	if req.Device == "" {
		return errors.New("device required")
	}
	if req.Method != "auto" && req.Method != "manual" {
		return errors.New("method must be auto or manual")
	}
	if isUnsafeNetworkDevice(req.Device) {
		return errors.New("refuse to configure virtual or unsafe device")
	}
	if req.Method == "auto" {
		return nil
	}
	if _, ipNet, err := net.ParseCIDR(req.Address); err != nil || ipNet.IP.To4() == nil {
		return errors.New("address must be IPv4 CIDR, example: 192.168.1.10/24")
	}
	if req.Gateway != "" && net.ParseIP(req.Gateway).To4() == nil {
		return errors.New("gateway must be IPv4 address")
	}
	if req.DNS == "" {
		return errors.New("dns required")
	}
	for _, dns := range strings.Split(req.DNS, ",") {
		if net.ParseIP(strings.TrimSpace(dns)).To4() == nil {
			return fmt.Errorf("invalid dns %q", dns)
		}
	}
	return nil
}

func networkConfigApplyArgs(connection string, req NetworkConfigApplyRequest) []string {
	if req.Method == "auto" {
		return []string{"connection", "modify", connection,
			"ipv4.method", "auto",
			"ipv4.addresses", "",
			"ipv4.gateway", "",
			"ipv4.dns", "",
		}
	}
	return []string{"connection", "modify", connection,
		"ipv4.method", "manual",
		"ipv4.addresses", req.Address,
		"ipv4.gateway", req.Gateway,
		"ipv4.dns", req.DNS,
	}
}

func checkIPv4Conflict(ctx context.Context, device, cidr string) error {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return err
	}
	if err := checkLocalIPv4Conflict(device, ip); err != nil {
		return err
	}
	if isIPv4OnDevice(device, ip) {
		return nil
	}
	if hasNeighborForIP(ip.String()) {
		return fmt.Errorf("目标 IP %s 已出现在邻居表中，疑似被占用", ip.String())
	}
	if arpingIPv4(ctx, device, ip.String()) {
		return fmt.Errorf("目标 IP %s 有 ARP 响应，疑似被占用", ip.String())
	}
	if pingIPv4(ctx, device, ip.String()) {
		return fmt.Errorf("目标 IP %s 有 ping 响应，疑似被占用", ip.String())
	}
	return nil
}

func isIPv4OnDevice(device string, ip net.IP) bool {
	iface, err := net.InterfaceByName(device)
	if err != nil {
		return false
	}
	target := ip.String()
	addrs, _ := iface.Addrs()
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if ok && ipNet.IP.To4() != nil && ipNet.IP.String() == target {
			return true
		}
	}
	return false
}

func arpingIPv4(ctx context.Context, device, ip string) bool {
	bin, err := exec.LookPath("arping")
	if err != nil {
		return false
	}
	arpCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	args := []string{"-c", "1", "-w", "2"}
	if device != "" {
		args = append(args, "-I", device)
	}
	args = append(args, ip)
	cmd := exec.CommandContext(arpCtx, bin, args...)
	return cmd.Run() == nil
}

func checkLocalIPv4Conflict(device string, ip net.IP) error {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	target := ip.String()
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil || ipNet.IP.String() != target {
				continue
			}
			if iface.Name == device {
				return nil
			}
			return fmt.Errorf("目标 IP %s 已被本机接口 %s 使用", target, iface.Name)
		}
	}
	return nil
}

func hasNeighborForIP(ip string) bool {
	for _, path := range []string{filepath.Join(hostProcRoot(), "net/arp"), "/proc/net/arp"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		first := true
		for scanner.Scan() {
			if first {
				first = false
				continue
			}
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 4 && fields[0] == ip && fields[3] != "00:00:00:00:00:00" {
				_ = f.Close()
				return true
			}
		}
		_ = f.Close()
	}
	return false
}

func pingIPv4(ctx context.Context, device, ip string) bool {
	bin, err := exec.LookPath("ping")
	if err != nil {
		return false
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	args := []string{"-4", "-c", "1", "-W", "1"}
	if device != "" {
		args = append(args, "-I", device)
	}
	args = append(args, ip)
	cmd := exec.CommandContext(pingCtx, bin, args...)
	return cmd.Run() == nil
}

func nmcli(ctx context.Context, args []string, timeout time.Duration) (string, error) {
	if lzcsdk.Available() {
		return lzcsdk.NmcliCall(ctx, args, timeout)
	}
	bin, err := exec.LookPath("nmcli")
	if err != nil {
		return "", errors.New("nmcli 不可用")
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, bin, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func splitNMCLIFields(line string) []string {
	var fields []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '\\' && i+1 < len(line) && line[i+1] == ':' {
			cur.WriteByte(':')
			i++
			continue
		}
		if c == ':' {
			fields = append(fields, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	fields = append(fields, cur.String())
	return fields
}

func normalizeDNS(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, ",")
}

func isUnsafeNetworkDevice(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "lo" {
		return true
	}
	for _, prefix := range []string{"docker", "br-", "lzc-br", "veth", "tun", "tap", "wg", "zt", "tailscale"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func newRollbackID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("rb-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Service) auditNetworkConfig(event networkConfigAuditEvent) {
	if event.Timestamp == "" {
		event.Timestamp = localTimestamp()
	}
	path := filepath.Join(s.cfg.DataDir, "network_config_audit.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		logger.Warn("network config audit mkdir: %v", err)
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		logger.Warn("network config audit open: %v", err)
		return
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(event)
}
