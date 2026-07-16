package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"netwatch/internal/logger"
)

// IPv6RenewNIC 是一块可执行 IPv6 续约的网卡(透传给前端选择)。
type IPv6RenewNIC struct {
	Device     string `json:"device"`
	Type       string `json:"type"`
	State      string `json:"state"`
	Connection string `json:"connection,omitempty"`
}

// IPv6RenewResult 是续约操作的结果。
type IPv6RenewResult struct {
	OK       bool     `json:"ok"`
	Device   string   `json:"device,omitempty"`
	Output   string   `json:"output,omitempty"`
	Error    string   `json:"error,omitempty"`
	IPv6Before []string `json:"ipv6_before,omitempty"`
	IPv6After  []string `json:"ipv6_after,omitempty"`
	Note     string   `json:"note,omitempty"`
}

// ListIPv6RenewNICs 列出当前可对其执行 IPv6 续约的接口。
// 使用与网卡配置相同的 nmcli 传输(懒猫 lzc-apis 或本机 nmcli)。
func (s *Service) ListIPv6RenewNICs(ctx context.Context) ([]IPv6RenewNIC, error) {
	if !nmcliTransportAvailable() {
		return nil, errors.New("nmcli 不可用(需懒猫系统接口或本机 nmcli)")
	}
	out, err := nmcli(ctx, []string{"-t", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device", "status"}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	var devices []IPv6RenewNIC
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := splitNmcliTerseLine(line)
		if len(fields) < 3 {
			continue
		}
		dev := IPv6RenewNIC{Device: fields[0], Type: fields[1], State: fields[2]}
		if len(fields) >= 4 {
			dev.Connection = fields[3]
		}
		if !isIPv6RenewableDevice(dev) {
			continue
		}
		devices = append(devices, dev)
	}
	return devices, nil
}

func isIPv6RenewableDevice(dev IPv6RenewNIC) bool {
	name := strings.TrimSpace(dev.Device)
	if name == "" || isUnsafeNetworkDevice(name) {
		return false
	}
	// Physical NICs + netwatch-managed bridges (host address often lives on the bridge).
	typ := strings.ToLower(strings.TrimSpace(dev.Type))
	switch typ {
	case "ethernet", "wifi", "bridge":
	default:
		return false
	}
	if typ == "bridge" && !isManagedHostBridgeName(name) {
		return false
	}
	if !strings.HasPrefix(strings.ToLower(dev.State), "connected") {
		// Kernel-up bridges may show differently; allow managed bridge if iface is up.
		if !(typ == "bridge" && isManagedHostBridgeName(name) && readOperState(name) == "up") {
			return false
		}
	}
	if strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "lzc-br") || strings.HasPrefix(name, "docker") {
		return false
	}
	return true
}

// RenewIPv6 对指定网卡执行 reapply,触发其重新获取 IPv6 配置,并做前后地址对比。
func (s *Service) RenewIPv6(ctx context.Context, iface string) IPv6RenewResult {
	iface = strings.TrimSpace(iface)
	if iface == "" {
		return IPv6RenewResult{Error: "device required"}
	}
	if isUnsafeNetworkDevice(iface) {
		return IPv6RenewResult{Device: iface, Error: "拒绝操作虚拟或不安全网卡"}
	}
	if !nmcliTransportAvailable() {
		return IPv6RenewResult{Device: iface, Error: "nmcli 不可用(需懒猫系统接口或本机 nmcli)"}
	}

	// Ensure device is listed as renewable when possible.
	if list, err := s.ListIPv6RenewNICs(ctx); err == nil {
		ok := false
		for _, d := range list {
			if d.Device == iface {
				ok = true
				break
			}
		}
		if !ok {
			// Still allow explicit managed bridge / physical name if present in sysfs.
			if _, err := os.Stat(filepath.Join("/sys/class/net", iface)); err != nil {
				return IPv6RenewResult{Device: iface, Error: "网卡不存在或不支持续约"}
			}
		}
	}

	before := publicIPv6OnIface(iface)
	out, err := nmcli(ctx, []string{"device", "reapply", iface}, 20*time.Second)
	if err != nil {
		// Fallback: connection up of the active connection on this device.
		conn := activeConnectionForDevice(ctx, iface)
		if conn != "" {
			out2, err2 := nmcli(ctx, []string{"connection", "up", conn}, 25*time.Second)
			out = strings.TrimSpace(out + "\n" + out2)
			if err2 != nil {
				logger.Warn("ipv6 renew failed iface=%s reapply=%v up=%v", iface, err, err2)
				return IPv6RenewResult{Device: iface, Output: out, Error: fmt.Sprintf("reapply 失败: %v; connection up 失败: %v", err, err2), IPv6Before: before}
			}
			err = nil
		} else {
			logger.Warn("ipv6 renew failed iface=%s err=%v", iface, err)
			return IPv6RenewResult{Device: iface, Output: out, Error: err.Error(), IPv6Before: before}
		}
	}

	// Allow DAD / RA a moment, then sample again.
	after := waitIPv6Change(iface, before, 6*time.Second)
	note := "已触发重新获取 IPv6"
	if len(after) == 0 {
		note = "已执行 reapply，但尚未观察到全局 IPv6（可能上行未下发或需更长时间）"
	} else if sameStringSet(before, after) {
		note = "已执行 reapply，IPv6 地址未变化（租约可能仍有效）"
	} else {
		note = "已重新获取 IPv6"
	}
	logger.Info("ipv6 renew ok iface=%s before=%v after=%v", iface, before, after)
	return IPv6RenewResult{
		OK:         true,
		Device:     iface,
		Output:     strings.TrimSpace(out),
		IPv6Before: before,
		IPv6After:  after,
		Note:       note,
	}
}

func activeConnectionForDevice(ctx context.Context, iface string) string {
	out, err := nmcli(ctx, []string{"-t", "-f", "GENERAL.CONNECTION", "device", "show", iface}, 4*time.Second)
	if err != nil {
		return ""
	}
	// Output like: GENERAL.CONNECTION:Wired connection 1
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			v := strings.TrimSpace(parts[1])
			if v != "" && v != "--" {
				return v
			}
		}
	}
	return ""
}

func publicIPv6OnIface(iface string) []string {
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		// fallback sys
		_, v6 := readIPsFromSys(iface)
		var out []string
		for _, s := range v6 {
			ip := net.ParseIP(strings.Split(s, "/")[0])
			if isPublicIPv6(ip) {
				out = append(out, ip.String())
			}
		}
		return out
	}
	addrs, _ := ifi.Addrs()
	var out []string
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.To4() != nil {
			continue
		}
		if isPublicIPv6(ipNet.IP) {
			out = append(out, ipNet.IP.String())
		}
	}
	return out
}

func waitIPv6Change(iface string, before []string, budget time.Duration) []string {
	deadline := time.Now().Add(budget)
	last := publicIPv6OnIface(iface)
	for time.Now().Before(deadline) {
		time.Sleep(400 * time.Millisecond)
		last = publicIPv6OnIface(iface)
		if len(last) > 0 && !sameStringSet(before, last) {
			return last
		}
	}
	return last
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		if m[s] == 0 {
			return false
		}
		m[s]--
	}
	return true
}

// splitNmcliTerseLine 按 nmcli terse 规则拆分一行。
func splitNmcliTerseLine(line string) []string {
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
