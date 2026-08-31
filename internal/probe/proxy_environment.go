package probe

import (
	"net"
	"os"
	"sort"
	"strings"
)

var proxyEnvironmentVariables = []string{
	"HTTP_PROXY",
	"HTTPS_PROXY",
	"ALL_PROXY",
	"http_proxy",
	"https_proxy",
	"all_proxy",
}

func detectProxyEnvironment() ProxyEnvironmentInfo {
	ifaces, _ := net.Interfaces()
	return detectProxyEnvironmentFrom(ifaces, os.Getenv)
}

func detectProxyEnvironmentFrom(ifaces []net.Interface, lookup func(string) string) ProxyEnvironmentInfo {
	info := ProxyEnvironmentInfo{Mode: "none", Confidence: "medium"}
	for _, iface := range ifaces {
		if !isKnownProxyTunName(iface.Name) || iface.Flags&net.FlagUp == 0 {
			continue
		}
		info.Interfaces = append(info.Interfaces, iface.Name)
	}
	sort.Strings(info.Interfaces)

	if lookup != nil {
		seen := map[string]bool{}
		for _, name := range proxyEnvironmentVariables {
			if strings.TrimSpace(lookup(name)) == "" {
				continue
			}
			canonical := strings.ToUpper(name)
			if seen[canonical] {
				continue
			}
			seen[canonical] = true
			info.EnvironmentVariables = append(info.EnvironmentVariables, canonical)
		}
	}

	hasTUN := len(info.Interfaces) > 0
	hasEnvironment := len(info.EnvironmentVariables) > 0
	info.Detected = hasTUN || hasEnvironment
	info.NATMayBeAffected = hasTUN
	info.Confidence = "high"
	switch {
	case hasTUN && hasEnvironment:
		info.Mode = "mixed"
		info.Note = "检测到代理 TUN 与进程代理环境；UDP NAT 结果可能受 TUN 分流规则影响。"
	case hasTUN:
		info.Mode = "tun"
		info.Note = "检测到代理 TUN；UDP NAT 结果是否代表真实出口取决于 TUN 旁路规则。"
	case hasEnvironment:
		info.Mode = "environment"
		info.Note = "检测到进程代理环境变量；HTTP 探测可能经代理转发，UDP STUN 通常不受其影响。"
	default:
		info.Confidence = "medium"
		info.Note = "未发现已知代理 TUN 或进程代理环境变量；自定义策略路由仍可能无法识别。"
	}
	return info
}
