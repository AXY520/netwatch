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
	"sync"
	"time"

	"github.com/vishvananda/netlink"

	"netwatch/internal/logger"
	"netwatch/internal/lzcsdk"
)

const networkConfigRollbackDelay = 3 * time.Minute

type networkConfigSnapshot struct {
	Connection  string `json:"connection"`
	Method      string `json:"method"`
	Addresses   string `json:"addresses"`
	Gateway     string `json:"gateway"`
	DNS         string `json:"dns"`
	MACProperty string `json:"mac_property,omitempty"`
	MACAddress  string `json:"mac_address,omitempty"`
}

type networkConfigRollback struct {
	ID       string                    `json:"id"`
	Device   string                    `json:"device"`
	Request  NetworkConfigApplyRequest `json:"request"`
	Previous NetworkConfigDevice       `json:"previous"`
	Snapshot networkConfigSnapshot     `json:"snapshot"`
	Timer    *time.Timer               `json:"-"`
	Until    time.Time                 `json:"until"`
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

func (s *Service) RestartNetworkConfigDevice(ctx context.Context, req NetworkConfigRestartRequest) NetworkConfigRestartResult {
	req.Device = strings.TrimSpace(req.Device)
	if req.Device == "" {
		return NetworkConfigRestartResult{Error: "device required"}
	}
	if isUnsafeNetworkDevice(req.Device) {
		return NetworkConfigRestartResult{Device: req.Device, Error: "refuse to restart virtual or unsafe device"}
	}
	id, err := s.reserveNetworkMutation(networkMutationRestart, req.Device)
	if err != nil {
		return NetworkConfigRestartResult{Device: req.Device, Error: err.Error()}
	}
	defer s.abortNetworkMutation(id)
	started := time.Now()
	audit := func(ok bool, connection, errText string) {
		state := "completed"
		if !ok {
			state = "failed"
		}
		s.auditNetworkMutation(NetworkMutationAuditEvent{
			ID: id, Kind: string(networkMutationRestart), Target: req.Device,
			Action: "restart", State: state, DurationMS: time.Since(started).Milliseconds(), Error: errText,
		}, nil)
		s.auditNetworkConfig(networkConfigAuditEvent{
			Action: "restart", ID: id, Device: req.Device, Connection: connection, OK: ok, Error: errText,
		})
	}

	invalidateHostNetworkDeviceInventoryCache()
	devices, err := listNetworkConfigDevices(ctx)
	if err != nil {
		audit(false, "", err.Error())
		return NetworkConfigRestartResult{Device: req.Device, Error: err.Error()}
	}
	dev, ok := findNetworkConfigDevice(devices, req.Device)
	if !ok || strings.TrimSpace(dev.Connection) == "" {
		errText := "网卡不可配置或未连接"
		audit(false, "", errText)
		return NetworkConfigRestartResult{Device: req.Device, Error: errText}
	}

	downOut, err := nmcli(ctx, []string{"device", "disconnect", req.Device}, 20*time.Second)
	if err != nil {
		errText := "断开网卡连接失败: " + err.Error()
		audit(false, dev.Connection, errText)
		return NetworkConfigRestartResult{Device: req.Device, Connection: dev.Connection, Output: strings.TrimSpace(downOut), Error: errText}
	}
	upOut, err := nmcli(ctx, []string{"connection", "up", dev.Connection, "ifname", req.Device}, 30*time.Second)
	output := strings.TrimSpace(strings.Join([]string{downOut, upOut}, "\n"))
	invalidateHostNetworkDeviceInventoryCache()
	if err != nil {
		errText := "重新连接网卡失败: " + err.Error()
		audit(false, dev.Connection, errText)
		return NetworkConfigRestartResult{Device: req.Device, Connection: dev.Connection, Output: output, Error: errText}
	}
	audit(true, dev.Connection, "")
	return NetworkConfigRestartResult{OK: true, Device: req.Device, Connection: dev.Connection, Output: output}
}

func (s *Service) ApplyNetworkConfig(ctx context.Context, req NetworkConfigApplyRequest) NetworkConfigApplyResult {
	req.Device = strings.TrimSpace(req.Device)
	req.Method = strings.TrimSpace(req.Method)
	if req.MACOnly {
		req.Method, req.Address, req.Gateway, req.DNS = "", "", "", ""
	} else if req.Method == "" {
		req.Method = "manual"
	}
	req.Address = strings.TrimSpace(req.Address)
	req.Gateway = strings.TrimSpace(req.Gateway)
	req.DNS = normalizeDNS(req.DNS)
	var err error
	req.MACAddress, err = normalizeNetworkConfigMAC(req.MACAddress)
	if err != nil {
		return NetworkConfigApplyResult{Device: req.Device, Error: err.Error()}
	}
	if err := validateNetworkConfigRequest(req); err != nil {
		return NetworkConfigApplyResult{Device: req.Device, Error: err.Error()}
	}
	id, err := s.reserveNetworkMutation(networkMutationIP, req.Device)
	if err != nil {
		return NetworkConfigApplyResult{Device: req.Device, Error: err.Error()}
	}
	activated := false
	defer func() {
		if !activated {
			s.abortNetworkMutation(id)
		}
	}()

	devices, err := listNetworkConfigDevices(ctx)
	if err != nil {
		return NetworkConfigApplyResult{Device: req.Device, Error: err.Error()}
	}
	dev, ok := findNetworkConfigDevice(devices, req.Device)
	if !ok {
		return NetworkConfigApplyResult{Device: req.Device, Error: "网卡不可配置或未连接"}
	}
	macProperty := ""
	if req.MACAddress != "" {
		macProperty = networkConfigMACProperty(dev.Type)
		if macProperty == "" {
			return NetworkConfigApplyResult{Device: req.Device, Connection: dev.Connection, Error: "该网卡类型不支持修改 MAC 地址"}
		}
	}
	if !req.MACOnly && req.Method == "manual" {
		if err := checkIPv4Conflict(ctx, req.Device, req.Address); err != nil {
			return NetworkConfigApplyResult{Device: req.Device, Connection: dev.Connection, Error: err.Error()}
		}
	}

	snapshot, err := readNetworkConfigSnapshot(ctx, dev.Connection)
	if err != nil {
		return NetworkConfigApplyResult{Device: req.Device, Connection: dev.Connection, Error: err.Error()}
	}
	if macProperty != "" {
		snapshot.MACProperty = macProperty
		snapshot.MACAddress, err = readNetworkConfigMACSetting(ctx, dev.Connection, macProperty)
		if err != nil {
			return NetworkConfigApplyResult{Device: req.Device, Connection: dev.Connection, Error: err.Error()}
		}
	}
	args := networkConfigApplyArgs(dev.Connection, req, macProperty)
	out1, err := nmcli(ctx, args, 10*time.Second)
	if err != nil {
		s.auditNetworkConfig(networkConfigAuditEvent{Action: "apply", ID: id, Device: req.Device, Connection: dev.Connection, Request: &req, Snapshot: &snapshot, OK: false, Error: err.Error()})
		return NetworkConfigApplyResult{Device: req.Device, Connection: dev.Connection, Output: strings.TrimSpace(out1), Error: err.Error()}
	}
	out2, err := activateNetworkConfigConnection(ctx, req.Device, dev.Connection)
	out1 = strings.TrimSpace(strings.Join([]string{out1, out2}, "\n"))
	if err != nil {
		restoreOut, restoreErr := restoreNetworkConfigSnapshot(ctx, req.Device, snapshot, req.MACOnly)
		out1 = strings.TrimSpace(strings.Join([]string{out1, restoreOut}, "\n"))
		errText := "激活新配置失败，已恢复原配置: " + err.Error()
		if restoreErr != nil {
			errText = fmt.Sprintf("激活新配置失败: %v；恢复原配置失败: %v", err, restoreErr)
		}
		s.auditNetworkConfig(networkConfigAuditEvent{Action: "apply", ID: id, Device: req.Device, Connection: dev.Connection, Request: &req, Snapshot: &snapshot, OK: false, Error: errText})
		return NetworkConfigApplyResult{Device: req.Device, Connection: dev.Connection, Output: out1, Error: errText}
	}
	invalidateHostNetworkDeviceInventoryCache()

	until := time.Now().Add(networkConfigRollbackDelay)
	rb := &networkConfigRollback{ID: id, Device: req.Device, Request: req, Previous: dev, Snapshot: snapshot, Until: until}
	if err := s.activateNetworkMutation(&networkMutation{
		ID: id, Kind: networkMutationIP, Target: req.Device, Until: until, IP: rb,
	}); err != nil {
		_, restoreErr := restoreNetworkConfigSnapshot(ctx, rb.Device, rb.Snapshot, rb.Request.MACOnly)
		if restoreErr != nil {
			return NetworkConfigApplyResult{Device: req.Device, Connection: dev.Connection, Error: fmt.Sprintf("登记网络变更失败: %v；恢复原配置失败: %v", err, restoreErr)}
		}
		return NetworkConfigApplyResult{Device: req.Device, Connection: dev.Connection, Error: "登记网络变更失败: " + err.Error()}
	}
	activated = true
	s.scheduleNetworkMutationRollback(s.networkMutationSnapshot())
	verification := s.verifyNetworkMutation(ctx, id)
	if verification.Status == "failed" {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		rollbackOut, rollbackErr := s.rollbackNetworkConfig(rollbackCtx, id, "verification_rollback")
		cancel()
		errText := "网络配置未通过应用后验证，已自动回滚"
		if rollbackErr != nil {
			errText = "网络配置未通过应用后验证，自动回滚失败: " + rollbackErr.Error()
		}
		return NetworkConfigApplyResult{Device: req.Device, Connection: dev.Connection, Output: strings.TrimSpace(strings.Join([]string{out1, rollbackOut}, "\n")), Error: errText, Verification: &verification}
	}

	s.auditNetworkConfig(networkConfigAuditEvent{Action: "apply", ID: id, Device: req.Device, Connection: dev.Connection, Request: &req, Snapshot: &snapshot, OK: true})
	return NetworkConfigApplyResult{OK: true, Device: req.Device, Connection: dev.Connection, RollbackID: id, RollbackUntil: until.Format(time.DateTime), Output: strings.TrimSpace(out1), Verification: &verification}
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
	mutation, err := s.confirmNetworkMutation(id, networkMutationIP)
	if err != nil || mutation.IP == nil {
		return NetworkConfigConfirmResult{ID: id, Error: "rollback task not found"}
	}
	rb := mutation.IP
	s.auditNetworkConfig(networkConfigAuditEvent{Action: "confirm", ID: id, Device: rb.Device, Connection: rb.Snapshot.Connection, OK: true})
	return NetworkConfigConfirmResult{OK: true, ID: id}
}

func (s *Service) GetNetworkConfigPending() NetworkConfigPendingResult {
	s.network.mu.Lock()
	defer s.network.mu.Unlock()
	now := time.Now()
	seen := map[*networkConfigRollback]struct{}{}
	for _, rb := range s.network.rollbacks {
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
			Pending:        true,
			ID:             rb.ID,
			Device:         rb.Device,
			Connection:     rb.Snapshot.Connection,
			Method:         rb.Request.Method,
			Address:        rb.Request.Address,
			Gateway:        rb.Request.Gateway,
			DNS:            rb.Request.DNS,
			MACAddress:     rb.Request.MACAddress,
			MACOnly:        rb.Request.MACOnly,
			PrevMethod:     rb.Previous.IPv4Method,
			PrevAddress:    rb.Previous.IPv4,
			PrevGateway:    rb.Previous.Gateway,
			PrevDNS:        rb.Previous.DNS,
			PrevMACAddress: rb.Previous.MACAddress,
			RollbackUntil:  rb.Until.Format(time.DateTime),
			RemainingSec:   remaining,
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
	mutation, err := s.beginNetworkMutationRollback(id, networkMutationIP)
	if err != nil || mutation.IP == nil {
		return "", errors.New("rollback task not found")
	}
	rb := mutation.IP

	out, err := restoreNetworkConfigSnapshot(ctx, rb.Device, rb.Snapshot, rb.Request.MACOnly)
	s.finishNetworkMutationRollback(rb.ID, err)
	s.auditNetworkConfig(networkConfigAuditEvent{Action: action, ID: id, Device: rb.Device, Connection: rb.Snapshot.Connection, Snapshot: &rb.Snapshot, OK: err == nil, Error: errString(err)})
	return out, err
}

func listNetworkConfigDevices(ctx context.Context) ([]NetworkConfigDevice, error) {
	inventory, err := listHostNetworkDeviceInventory(ctx)
	if err != nil {
		return nil, err
	}
	devices := make([]NetworkConfigDevice, 0, len(inventory))
	for _, item := range inventory {
		macAddress := item.Runtime.MACAddress
		if macAddress == "" {
			macAddress = normalizeMAC(readMACFromSys(item.Device))
		}
		dev := NetworkConfigDevice{
			Device: item.Device, Type: item.Type, State: item.State, Connection: item.Connection,
			IPv4Method: item.Snapshot.Method, IPv4: item.Snapshot.Addresses,
			Gateway: item.Snapshot.Gateway, DNS: item.Snapshot.DNS, MACAddress: macAddress,
			PermanentMACAddress: readPermanentNetworkConfigMAC(item.Device),
		}
		if item.Runtime.IPv4 != "" {
			dev.IPv4 = item.Runtime.IPv4
		}
		if item.Runtime.Gateway != "" {
			dev.Gateway = item.Runtime.Gateway
		}
		if item.Runtime.DNS != "" {
			dev.DNS = item.Runtime.DNS
		}
		devices = append(devices, dev)
	}
	return devices, nil
}

type hostNetworkDeviceInventoryItem struct {
	Device     string
	Type       string
	State      string
	Connection string
	Snapshot   networkConfigSnapshot
	Runtime    networkDeviceRuntimeConfig
}

const hostNetworkDeviceInventoryTTL = 2 * time.Second

var hostNetworkDeviceInventoryCache struct {
	sync.Mutex
	at      time.Time
	devices []hostNetworkDeviceInventoryItem
}

func invalidateHostNetworkDeviceInventoryCache() {
	hostNetworkDeviceInventoryCache.Lock()
	hostNetworkDeviceInventoryCache.at = time.Time{}
	hostNetworkDeviceInventoryCache.devices = nil
	hostNetworkDeviceInventoryCache.Unlock()
}

// listHostNetworkDeviceInventory is the single source of truth for connected
// host devices that may be configured or used as DNS resolver sources.
func listHostNetworkDeviceInventory(ctx context.Context) ([]hostNetworkDeviceInventoryItem, error) {
	hostNetworkDeviceInventoryCache.Lock()
	if time.Since(hostNetworkDeviceInventoryCache.at) < hostNetworkDeviceInventoryTTL && hostNetworkDeviceInventoryCache.devices != nil {
		devices := append([]hostNetworkDeviceInventoryItem(nil), hostNetworkDeviceInventoryCache.devices...)
		hostNetworkDeviceInventoryCache.Unlock()
		return devices, nil
	}
	hostNetworkDeviceInventoryCache.Unlock()

	out, err := nmcli(ctx, []string{"-t", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device", "status"}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	var candidates []hostNetworkDeviceInventoryItem
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := splitNMCLIFields(line)
		if len(fields) < 4 {
			continue
		}
		dev := hostNetworkDeviceInventoryItem{Device: fields[0], Type: fields[1], State: fields[2], Connection: fields[3]}
		typeLower := strings.ToLower(dev.Type)
		isManagedBridge := typeLower == "bridge" && isManagedHostBridgeName(dev.Device)
		if (typeLower != "ethernet" && typeLower != "wifi" && !isManagedBridge) || !strings.HasPrefix(dev.State, "connected") || dev.Connection == "" {
			continue
		}
		if isUnsafeNetworkDevice(dev.Device) && !isManagedBridge {
			continue
		}
		candidates = append(candidates, dev)
	}

	resolved := make([]hostNetworkDeviceInventoryItem, len(candidates))
	keep := make([]bool, len(candidates))
	var wg sync.WaitGroup
	for index := range candidates {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			dev := candidates[index]
			isManagedBridge := strings.EqualFold(dev.Type, "bridge") && isManagedHostBridgeName(dev.Device)
			if !isManagedBridge && connectionIsBridgePort(ctx, dev.Connection) {
				return
			}
			var detailWG sync.WaitGroup
			detailWG.Add(2)
			go func() {
				defer detailWG.Done()
				if snap, err := readNetworkConfigSnapshot(ctx, dev.Connection); err == nil {
					dev.Snapshot = snap
				}
			}()
			go func() {
				defer detailWG.Done()
				if runtime, err := readNetworkDeviceRuntimeConfig(ctx, dev.Device); err == nil {
					dev.Runtime = runtime
				}
			}()
			detailWG.Wait()
			resolved[index] = dev
			keep[index] = true
		}(index)
	}
	wg.Wait()
	devices := make([]hostNetworkDeviceInventoryItem, 0, len(resolved))
	for index, dev := range resolved {
		if keep[index] {
			devices = append(devices, dev)
		}
	}
	hostNetworkDeviceInventoryCache.Lock()
	hostNetworkDeviceInventoryCache.at = time.Now()
	hostNetworkDeviceInventoryCache.devices = append([]hostNetworkDeviceInventoryItem(nil), devices...)
	hostNetworkDeviceInventoryCache.Unlock()
	return devices, nil
}

type networkDeviceRuntimeConfig struct {
	IPv4       string
	Gateway    string
	DNS        string
	MACAddress string
}

func readNetworkDeviceRuntimeConfig(ctx context.Context, device string) (networkDeviceRuntimeConfig, error) {
	out, err := nmcli(ctx, []string{"-t", "-f", "GENERAL.HWADDR,IP4.ADDRESS,IP4.GATEWAY,IP4.DNS", "device", "show", device}, 5*time.Second)
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
		case strings.HasPrefix(key, "GENERAL.HWADDR") && cfg.MACAddress == "":
			cfg.MACAddress = normalizeMAC(value)
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

func readNetworkConfigMACSetting(ctx context.Context, connection, property string) (string, error) {
	out, err := nmcli(ctx, []string{"-g", property, "connection", "show", connection}, 5*time.Second)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(strings.TrimSpace(out), `\:`, ":"), nil
}

func restoreNetworkConfigSnapshot(ctx context.Context, device string, snap networkConfigSnapshot, macOnly bool) (string, error) {
	args := []string{"connection", "modify", snap.Connection}
	if !macOnly {
		args = append(args,
			"ipv4.method", valueOrDefault(snap.Method, "auto"),
			"ipv4.addresses", snap.Addresses,
			"ipv4.gateway", snap.Gateway,
			"ipv4.dns", snap.DNS,
		)
	}
	if snap.MACProperty != "" {
		args = append(args, snap.MACProperty, snap.MACAddress)
	}
	out1, err := nmcli(ctx, args, 10*time.Second)
	if err != nil {
		return strings.TrimSpace(out1), err
	}
	out2, err := activateNetworkConfigConnection(ctx, device, snap.Connection)
	invalidateHostNetworkDeviceInventoryCache()
	return strings.TrimSpace(strings.Join([]string{out1, out2}, "\n")), err
}

// activateNetworkConfigConnection makes profile changes effective immediately.
// `device reapply` reports success for some ipv4.method transitions without
// replacing the active address, so reconnect the profile first. Older
// NetworkManager versions may reject that operation; retain reapply as a
// compatibility fallback for profiles that can be updated in place.
func activateNetworkConfigConnection(ctx context.Context, device, connection string) (string, error) {
	device = strings.TrimSpace(device)
	connection = strings.TrimSpace(connection)
	if device == "" || connection == "" {
		return "", errors.New("device and connection required")
	}
	upOut, upErr := nmcli(ctx, []string{"connection", "up", connection, "ifname", device}, 30*time.Second)
	if upErr == nil {
		return strings.TrimSpace(upOut), nil
	}

	reapplyOut, reapplyErr := nmcli(ctx, []string{"device", "reapply", device}, 20*time.Second)
	combined := strings.TrimSpace(strings.Join([]string{upOut, reapplyOut}, "\n"))
	if reapplyErr == nil {
		return combined, nil
	}
	return combined, fmt.Errorf("connection up 失败: %v；device reapply 失败: %v", upErr, reapplyErr)
}

func validateNetworkConfigRequest(req NetworkConfigApplyRequest) error {
	if req.Device == "" {
		return errors.New("device required")
	}
	if isUnsafeNetworkDevice(req.Device) {
		return errors.New("refuse to configure virtual or unsafe device")
	}
	if req.MACOnly {
		if req.MACAddress == "" {
			return errors.New("MAC 地址不能为空")
		}
		return nil
	}
	if req.Method != "auto" && req.Method != "manual" {
		return errors.New("method must be auto or manual")
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

func normalizeNetworkConfigMAC(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	hw, err := net.ParseMAC(value)
	if err != nil || len(hw) != 6 {
		return "", errors.New("MAC 地址格式不正确，请输入类似 02:11:22:33:44:55 的地址")
	}
	allZero := true
	for _, octet := range hw {
		if octet != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return "", errors.New("MAC 地址不能为全零")
	}
	if hw[0]&1 != 0 {
		return "", errors.New("MAC 地址必须是单播地址")
	}
	return strings.ToLower(hw.String()), nil
}

func networkConfigMACProperty(deviceType string) string {
	switch strings.ToLower(strings.TrimSpace(deviceType)) {
	case "ethernet", "bridge":
		return "802-3-ethernet.cloned-mac-address"
	case "wifi":
		return "802-11-wireless.cloned-mac-address"
	default:
		return ""
	}
}

func readPermanentNetworkConfigMAC(device string) string {
	link, err := netlink.LinkByName(strings.TrimSpace(device))
	if err != nil || link == nil || link.Attrs() == nil {
		return ""
	}
	mac, err := normalizeNetworkConfigMAC(link.Attrs().PermHWAddr.String())
	if err != nil {
		return ""
	}
	return mac
}

func networkConfigApplyArgs(connection string, req NetworkConfigApplyRequest, macProperty string) []string {
	var args []string
	if req.MACOnly {
		args = []string{"connection", "modify", connection}
	} else if req.Method == "auto" {
		args = []string{"connection", "modify", connection,
			"ipv4.method", "auto",
			"ipv4.addresses", "",
			"ipv4.gateway", "",
			"ipv4.dns", "",
		}
	} else {
		args = []string{"connection", "modify", connection,
			"ipv4.method", "manual",
			"ipv4.addresses", req.Address,
			"ipv4.gateway", req.Gateway,
			"ipv4.dns", req.DNS,
		}
	}
	if req.MACAddress != "" && macProperty != "" {
		args = append(args, macProperty, req.MACAddress)
	}
	return args
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
	// Proxy TUN (Meta/mihomo) and other virtual stacks must not be reconfigured here.
	if isProxyTunIface(name) {
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
