package probe

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"netwatch/internal/logger"
)

func hostBridgeBackend() string {
	// Same transport as network_config.nmcli(): Lazycat lzc-apis or local nmcli binary.
	if nmcliTransportAvailable() {
		return "nmcli"
	}
	return ""
}

func isEthernetLikeIface(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || isUnsafeNetworkDevice(name) || isManagedHostBridgeName(name) {
		return false
	}
	// ARPHRD_ETHER == 1
	typePath := filepath.Join("/sys/class/net", name, "type")
	body, err := os.ReadFile(typePath)
	if err != nil {
		return false
	}
	if strings.TrimSpace(string(body)) != "1" {
		return false
	}
	// Skip wireless (client mode cannot L2-bridge reliably).
	if _, err := os.Stat(filepath.Join("/sys/class/net", name, "wireless")); err == nil {
		return false
	}
	if isKernelBridgeIface(name) {
		return false
	}
	return true
}

func listBridgeCandidateDevices() []NetworkConfigDevice {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []NetworkConfigDevice
	for _, iface := range ifaces {
		name := iface.Name
		if !isEthernetLikeIface(name) {
			continue
		}
		if isIfaceBridgePort(name) {
			continue
		}
		oper := readOperState(name)
		// Prefer up/unknown with carrier; still list down so UI can show something.
		dev := NetworkConfigDevice{
			Device: name,
			Type:   "ethernet",
			State:  oper,
		}
		if v4, _, gw := readIfaceIPv4AndGateway(name); v4 != "" {
			dev.IPv4 = v4
			dev.Gateway = gw
			dev.IPv4Method = "manual"
		} else {
			dev.IPv4Method = "auto"
		}
		if dns := readResolvDNS(); dns != "" {
			dev.DNS = dns
		}
		out = append(out, dev)
	}
	return out
}

func isIfaceBridgePort(name string) bool {
	// /sys/class/net/<dev>/master exists when enslaved
	_, err := os.Stat(filepath.Join("/sys/class/net", name, "master"))
	return err == nil
}

func readIfaceIPv4AndGateway(name string) (cidr string, ip string, gateway string) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return "", "", ""
	}
	addrs, _ := iface.Addrs()
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.To4() == nil {
			continue
		}
		if ipNet.IP.IsLinkLocalUnicast() {
			continue
		}
		cidr = ipNet.String()
		ip = ipNet.IP.String()
		break
	}
	gateway = defaultGatewayForIface(name)
	return cidr, ip, gateway
}

func defaultGatewayForIface(iface string) string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if sc.Scan() {
		// header
	}
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		if fields[0] != iface {
			continue
		}
		// destination 00000000 = default
		if fields[1] != "00000000" {
			continue
		}
		return decodeIPv4Hex(fields[2])
	}
	return ""
}

func readResolvDNS() string {
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
		if len(fields) < 2 {
			continue
		}
		ip := net.ParseIP(fields[1])
		// Skip systemd-resolved stub / any loopback — useless as NM ipv4.dns.
		if ip == nil || ip.To4() == nil || ip.IsLoopback() {
			continue
		}
		dns = append(dns, ip.To4().String())
		if len(dns) >= 3 {
			break
		}
	}
	return strings.Join(dns, ",")
}

func (s *Service) createHostBridgeViaIP(ctx context.Context, req HostBridgeCreateRequest, bridgeName, method, address, gateway, dns string) HostBridgeOpResult {
	findBinPaths()
	if ipPath == "" {
		return HostBridgeOpResult{Device: req.Device, Error: "ip 命令不可用"}
	}
	if isIfaceBridgePort(req.Device) {
		return HostBridgeOpResult{Device: req.Device, Error: "该网卡已是网桥端口"}
	}
	if _, err := os.Stat(filepath.Join("/sys/class/net", bridgeName)); err == nil {
		return HostBridgeOpResult{Device: req.Device, Bridge: bridgeName, Error: "网桥接口已存在: " + bridgeName}
	}

	// Capture current addressing before we touch the NIC.
	curCIDR, _, curGW := readIfaceIPv4AndGateway(req.Device)
	if method == "manual" || method == "inherit" {
		if address == "" {
			address = curCIDR
		}
		if gateway == "" {
			gateway = curGW
		}
		if gateway == "" {
			gateway = readDefaultIPv4Route().Gateway
		}
		dns = ensureHostBridgeDNS(dns, readResolvDNS(), gateway)
	}
	if method == "auto" {
		// Pure DHCP without NM is environment-dependent; still create L2 bridge and
		// try dhclient/udhcpc on the bridge afterwards.
		address, gateway = "", ""
	}
	if method != "auto" && address == "" {
		return HostBridgeOpResult{Device: req.Device, Error: "无法继承网卡地址，请改用手动模式填写 IPv4/CIDR"}
	}

	var logs []string
	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		logs = append(logs, line)
		logger.Info("host-bridge(ip): %s", line)
	}
	run := func(args ...string) error {
		out, err := execCmd(ipPath, 12*time.Second, args...)
		if out != "" {
			logf("ip %s => %s", strings.Join(args, " "), out)
		} else {
			logf("ip %s", strings.Join(args, " "))
		}
		return err
	}

	// 1) create bridge
	if err := run("link", "add", "name", bridgeName, "type", "bridge"); err != nil {
		return HostBridgeOpResult{Device: req.Device, Bridge: bridgeName, Output: strings.Join(logs, "\n"), Error: "创建网桥失败: " + err.Error()}
	}
	_ = run("link", "set", "dev", bridgeName, "up")

	// 2) move L3 off the physical NIC first (avoid duplicate address)
	if curCIDR != "" {
		_ = run("addr", "del", curCIDR, "dev", req.Device)
	}
	// Best-effort flush remaining v4
	_ = run("addr", "flush", "dev", req.Device)

	// 3) enslave physical NIC
	if err := run("link", "set", "dev", req.Device, "master", bridgeName); err != nil {
		_ = run("link", "delete", bridgeName)
		// try restore address
		if curCIDR != "" {
			_ = run("addr", "add", curCIDR, "dev", req.Device)
		}
		return HostBridgeOpResult{Device: req.Device, Bridge: bridgeName, Output: strings.Join(logs, "\n"), Error: "绑定网卡到网桥失败: " + err.Error()}
	}
	_ = run("link", "set", "dev", req.Device, "up")

	// 4) address on bridge
	if method == "auto" {
		if err := runDHCPOnIface(bridgeName); err != nil {
			logf("dhcp warning: %v", err)
			// keep bridge; user can still use it for VMs even if host has no IP yet
		} else {
			// refresh learned address for inventory
			address, _, gateway = readIfaceIPv4AndGateway(bridgeName)
			if address == "" {
				if v4, _, gw := readIfaceIPv4AndGateway(bridgeName); v4 != "" {
					address, gateway = v4, gw
				}
			}
		}
	} else {
		if err := run("addr", "add", address, "dev", bridgeName); err != nil {
			// may already exist
			logf("addr add warning: %v", err)
		}
		if gateway != "" {
			// replace default route via bridge
			_ = run("route", "replace", "default", "via", gateway, "dev", bridgeName)
		}
		dns = ensureHostBridgeDNS(dns, readResolvDNS(), gateway)
		if dns != "" {
			if err := writeResolvDNS(dns); err != nil {
				logf("write resolv.conf warning: %v", err)
			} else {
				logf("wrote nameserver %s to resolv.conf", dns)
			}
		}
	}

	id := newRollbackID()
	until := time.Now().Add(hostBridgeRollbackDelay)
	rec := HostBridgeRecord{
		Bridge:        bridgeName,
		Device:        req.Device,
		Backend:       "ip",
		OriginalConn:  "", // no NM profile
		Method:        method,
		Address:       address,
		Gateway:       gateway,
		DNS:           dns,
		CreatedAt:     localTimestamp(),
		RollbackID:    id,
		RollbackUntil: until.Format(time.DateTime),
		IPv6Method:    "auto",
		Confirmed:     false,
		Note:          "待确认：3 分钟内未确认将自动回滚",
	}
	// Remember previous address for dissolve restore
	if curCIDR != "" {
		rec.OriginalConn = "ip:" + curCIDR
		if curGW != "" {
			rec.OriginalConn += "|gw:" + curGW
		}
	}
	records := s.loadHostBridgeRecords()
	records = append(records, rec)
	_ = s.saveHostBridgeRecords(records)

	rb := &hostBridgeRollback{
		ID:     id,
		Bridge: bridgeName,
		Device: req.Device,
		Record: rec,
		Until:  until,
	}
	rb.Timer = time.AfterFunc(hostBridgeRollbackDelay, func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if out, err := s.rollbackHostBridge(rollbackCtx, id, "auto_rollback"); err != nil {
			logger.Warn("host bridge auto rollback failed id=%s bridge=%s err=%v out=%q", id, bridgeName, err, out)
		}
	})
	s.control.netcfgMu.Lock()
	if old := s.control.bridgeRollback; old != nil && old.Timer != nil {
		old.Timer.Stop()
	}
	s.control.bridgeRollback = rb
	s.control.netcfgMu.Unlock()

	s.auditHostBridge(map[string]any{
		"action": "create", "backend": "ip", "bridge": bridgeName, "device": req.Device,
		"method": method, "address": address, "rollback_id": id, "ok": true,
	})

	return HostBridgeOpResult{
		OK:            true,
		Bridge:        bridgeName,
		Device:        req.Device,
		RollbackID:    id,
		RollbackUntil: until.Format(time.DateTime),
		Output:        strings.Join(logs, "\n"),
		Note:          "网桥已创建（ip link），请在 3 分钟内确认；超时自动回滚",
	}
}

func runDHCPOnIface(iface string) error {
	// Try common DHCP clients; ignore if none exist.
	for _, candidate := range []struct {
		bin  string
		args []string
	}{
		{"dhclient", []string{"-1", "-v", iface}},
		{"udhcpc", []string{"-i", iface, "-n", "-q"}},
		{"dhcpcd", []string{"-1", iface}},
	} {
		bin, err := exec.LookPath(candidate.bin)
		if err != nil {
			continue
		}
		out, err := execCmd(bin, 20*time.Second, candidate.args...)
		if err != nil {
			return fmt.Errorf("%s: %w (%s)", candidate.bin, err, out)
		}
		return nil
	}
	return fmt.Errorf("未找到 dhclient/udhcpc/dhcpcd，自动获取不可用")
}

func (s *Service) teardownHostBridgeViaIP(rec HostBridgeRecord) (string, error) {
	findBinPaths()
	if ipPath == "" {
		return "", fmt.Errorf("ip 命令不可用")
	}
	var logs []string
	run := func(args ...string) error {
		out, err := execCmd(ipPath, 12*time.Second, args...)
		line := "ip " + strings.Join(args, " ")
		if out != "" {
			line += " => " + out
		}
		if err != nil {
			line += " err=" + err.Error()
		}
		logs = append(logs, line)
		return err
	}

	// Release port
	_ = run("link", "set", "dev", rec.Device, "nomaster")
	_ = run("addr", "flush", "dev", rec.Bridge)
	_ = run("link", "delete", rec.Bridge)

	// Restore previous IPv4 if we captured it in OriginalConn as ip:CIDR|gw:X
	prevCIDR, prevGW := parseIPOriginal(rec.OriginalConn)
	if prevCIDR != "" {
		_ = run("addr", "add", prevCIDR, "dev", rec.Device)
		_ = run("link", "set", "dev", rec.Device, "up")
		if prevGW != "" {
			_ = run("route", "replace", "default", "via", prevGW, "dev", rec.Device)
		}
	} else {
		_ = run("link", "set", "dev", rec.Device, "up")
	}

	return strings.Join(logs, "\n"), nil
}

func parseIPOriginal(v string) (cidr, gw string) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "ip:") {
		return "", ""
	}
	v = strings.TrimPrefix(v, "ip:")
	parts := strings.Split(v, "|")
	cidr = parts[0]
	for _, p := range parts[1:] {
		if strings.HasPrefix(p, "gw:") {
			gw = strings.TrimPrefix(p, "gw:")
		}
	}
	return cidr, gw
}

// writeResolvDNS best-effort updates /etc/resolv.conf nameserver lines.
// Skips when resolv.conf is a symlink (systemd-resolved / resolvconf managed).
func writeResolvDNS(dns string) error {
	dns = ensureHostBridgeDNS(dns)
	if dns == "" {
		return errors.New("empty dns")
	}
	path := "/etc/resolv.conf"
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return errors.New("resolv.conf is a symlink; not rewriting")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var keep []string
	for _, line := range strings.Split(string(raw), "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "nameserver") {
			continue
		}
		keep = append(keep, line)
	}
	var b strings.Builder
	for _, line := range keep {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, ns := range strings.Split(dns, ",") {
		ns = strings.TrimSpace(ns)
		if ns == "" {
			continue
		}
		b.WriteString("nameserver ")
		b.WriteString(ns)
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
