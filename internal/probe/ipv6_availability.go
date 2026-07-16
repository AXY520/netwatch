package probe

import (
	"context"
	"net"
	"net/http"
	"time"
)

// ipv6OutboundTargets 是 IPv6 出站连通性探测目标:国内公共 DNS 的固定 IPv6(任一通即可),
// 全程走国内、不经境外、不受 GFW 干扰。多目标避免单一主机腐烂。
var ipv6OutboundTargets = []string{
	"[2400:3200:baba::1]:443", // 阿里 DNS(主)
	"[2400:3200::1]:443",      // 阿里 DNS(备)
	"[240C::6666]:443",        // CERNET DNS
	"[2400:da00::6666]:443",   // 百度 DNS
	"[240e:1:68::68]:443",     // 电信 114 DNS IPv6
}

// ipv6CheckHosts 是用于 HTTPS-over-IPv6 与 AAAA 解析检测的国内双栈域名(任一通即可)。
// 优先使用 IPv6 专用/强双栈域名，降低被解析到假地址或 TLS 异常导致的误判。
var ipv6CheckHosts = []string{
	"ipv6.baidu.com",
	"www.baidu.com",
	"www.qq.com",
	"www.taobao.com",
}

// IPv6 可用性结论。
const (
	ipv6SummaryFullyUsable  = "fully_usable"  // 地址 + 出站 + 应用层 + DNS 全通
	ipv6SummaryOutboundOnly = "outbound_only" // 出站通但应用层/DNS 不完整
	ipv6SummaryAddressOnly  = "address_only"  // 有公网地址但出站不可达(路由不通)
	ipv6SummaryNoGlobal     = "no_global"     // 无公网可路由 IPv6
)

// probeIPv6Availability 分层检测 IPv6 真实可用性。
// 使用独立超时预算，避免被上层 egress/lookup 的短 deadline 截断导致假阴性。
//
//	① 有无公网可路由地址
//	② HTTPS-over-IPv6(国内域名):成功即派生 fully_usable
//	③ HTTPS 失败时补做出站拨号 + DNS
func probeIPv6Availability(ctx context.Context, globalAddr string) IPv6Availability {
	// Detach from parent cancel but keep a hard budget so a short parent
	// deadline cannot leave every layer half-probed as false negatives.
	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()

	avail := IPv6Availability{CheckedAt: localTimestamp()}

	// ① 公网可路由地址
	if globalAddr != "" && isPublicIPv6(net.ParseIP(globalAddr)) {
		avail.HasGlobalAddress = true
		avail.GlobalAddress = globalAddr
	} else if local := pickPublicIPv6FromInterfaces(); local != "" {
		avail.HasGlobalAddress = true
		avail.GlobalAddress = local
	}

	// 无地址时仍尝试一次出站：地址探测可能漏掉非监测网卡上的临时地址。
	if !avail.HasGlobalAddress {
		if target, latency, ok := probeIPv6Outbound(probeCtx); ok {
			avail.OutboundReachable = true
			avail.OutboundTarget = target
			avail.OutboundLatencyMS = latency
			if local := pickPublicIPv6FromInterfaces(); local != "" {
				avail.HasGlobalAddress = true
				avail.GlobalAddress = local
			}
		}
		if !avail.HasGlobalAddress {
			avail.Summary = ipv6SummaryNoGlobal
			return avail
		}
	}

	// ③ 应用层 HTTPS over IPv6 —— 最强信号
	if host, latency, ok := probeIPv6HTTPS(probeCtx); ok {
		avail.HTTPSReachable = true
		avail.DNSResolvable = true
		avail.OutboundReachable = true
		avail.OutboundLatencyMS = latency
		avail.OutboundTarget = host
		avail.Summary = summarizeIPv6(avail)
		return avail
	}

	// HTTPS 失败，回退逐层探测
	if target, latency, ok := probeIPv6Outbound(probeCtx); ok {
		avail.OutboundReachable = true
		avail.OutboundTarget = target
		avail.OutboundLatencyMS = latency
	}
	avail.DNSResolvable = probeIPv6DNS(probeCtx)
	avail.Summary = summarizeIPv6(avail)
	return avail
}

// summarizeIPv6 综合各层结果给出结论(纯函数,便于测试)。
// fully_usable: 出站 + HTTPS 成功（DNS 由 HTTPS 隐含，或单独解析成功）
// 出站 + DNS 成功但 HTTPS 失败 → outbound_only（避免因个别站点 TLS/策略误判为完全不可用）
func summarizeIPv6(a IPv6Availability) string {
	if !a.HasGlobalAddress {
		return ipv6SummaryNoGlobal
	}
	if !a.OutboundReachable {
		return ipv6SummaryAddressOnly
	}
	if a.HTTPSReachable {
		return ipv6SummaryFullyUsable
	}
	return ipv6SummaryOutboundOnly
}

// probeIPv6Outbound 依次尝试多个大厂 anycast 的 tcp6 443,任一通即返回命中目标与延迟。
func probeIPv6Outbound(ctx context.Context) (target string, latencyMS int64, ok bool) {
	for _, t := range ipv6OutboundTargets {
		if ctx.Err() != nil {
			return "", 0, false
		}
		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		start := time.Now()
		conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp6", t)
		cancel()
		if err != nil {
			continue
		}
		latency := time.Since(start).Milliseconds()
		_ = conn.Close()
		return t, latency, true
	}
	return "", 0, false
}

// probeIPv6HTTPS 用强制 tcp6 的 client 对国内双栈域名发 HTTPS GET。
// 拿到任意 HTTP 响应(含 3xx/4xx)即证明 IPv6 应用层可达。
func probeIPv6HTTPS(ctx context.Context) (host string, latencyMS int64, ok bool) {
	client := &http.Client{
		Timeout: 6 * time.Second,
		Transport: &http.Transport{
			DialContext: func(c context.Context, _, addr string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(c, "tcp6", addr)
			},
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 5 * time.Second,
			DisableKeepAlives:     true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	for _, h := range ipv6CheckHosts {
		if ctx.Err() != nil {
			return "", 0, false
		}
		reqCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "https://"+h+"/", nil)
		if err != nil {
			cancel()
			continue
		}
		req.Header.Set("User-Agent", "netwatch/1.0")
		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			continue
		}
		latency := time.Since(start).Milliseconds()
		_ = resp.Body.Close()
		cancel()
		if resp.StatusCode > 0 {
			return h, latency, true
		}
	}
	return "", 0, false
}

// probeIPv6DNS 验证国内双栈域名能否解析出 AAAA 记录。
func probeIPv6DNS(ctx context.Context) bool {
	for _, host := range ipv6CheckHosts {
		if ctx.Err() != nil {
			return false
		}
		reqCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		ips, err := net.DefaultResolver.LookupIP(reqCtx, "ip6", host)
		cancel()
		if err == nil && len(ips) > 0 {
			return true
		}
	}
	return false
}

// pickPublicIPv6FromInterfaces 从本机网卡选取公网可路由 IPv6。
// 优先监测网卡，再回退到全部非虚拟接口，减少“有地址却判 no_global”的误判。
func pickPublicIPv6FromInterfaces() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	monitored := autoMonitoredNICs(ifaces)
	target := make(map[string]struct{}, len(monitored))
	for _, n := range monitored {
		target[n] = struct{}{}
	}
	pickFrom := func(restrict bool) string {
		for _, iface := range ifaces {
			name := iface.Name
			if shouldIgnoreInterface(name) {
				continue
			}
			if restrict {
				if _, ok := target[name]; !ok {
					continue
				}
			}
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				ipNet, ok := addr.(*net.IPNet)
				if !ok || ipNet.IP.To4() != nil {
					continue
				}
				if isPublicIPv6(ipNet.IP) {
					return ipNet.IP.String()
				}
			}
		}
		return ""
	}
	if v := pickFrom(true); v != "" {
		return v
	}
	return pickFrom(false)
}
