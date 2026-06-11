package probe

import (
	"context"
	"net"
	"net/http"
	"time"
)

// ipv6OutboundTargets 是 IPv6 出站连通性探测目标:国内公共 DNS 的固定 IPv6(任一通即可),
// 全程走国内、不经境外、不受 GFW 干扰,地址长期稳定 —— 用多个目标避免依赖单一会腐烂的主机。
var ipv6OutboundTargets = []string{
	"[2400:3200:baba::1]:443", // 阿里 DNS(主)
	"[2400:3200::1]:443",      // 阿里 DNS(备)
	"[240C::6666]:443",        // 下一代互联网 CERNET DNS
}

// ipv6CheckHosts 是用于 HTTPS-over-IPv6 与 AAAA 解析检测的国内双栈域名(任一通即可)。
// 用国内域名避免境外域名 AAAA 被 DNS 污染成 IPv4-mapped 假地址而误判不可用。
var ipv6CheckHosts = []string{
	"ipv6.baidu.com",
	"www.taobao.com",
}

// IPv6 可用性结论。
const (
	ipv6SummaryFullyUsable  = "fully_usable"  // 地址 + 出站 + 应用层 + DNS 全通
	ipv6SummaryOutboundOnly = "outbound_only" // 出站通但应用层/DNS 不完整
	ipv6SummaryAddressOnly  = "address_only"  // 有公网地址但出站不可达(路由不通)
	ipv6SummaryNoGlobal     = "no_global"     // 无公网可路由 IPv6
)

// probeIPv6Availability 分层检测 IPv6 真实可用性,按"强信号优先、弱信号派生"
// 的顺序短路,避免重复网络开销:
//
//	① 有无公网可路由地址(复用已获取的 globalAddr / 网卡,零额外开销)
//	② 高位端口(诊断信号,不可派生,保留 1 次拨号)
//	③ HTTPS-over-IPv6(国内域名):成功即证明出站✓ + DNS✓(强制 tcp6 必先解析
//	   AAAA 再建连),直接派生为 fully_usable,省掉 ②出站 + ④DNS 两次探测。
//	④ 仅当 HTTPS 失败时,才补做出站拨号 + DNS,定位是路由不通还是 DNS 问题。
//
// globalAddr 为已知公网 IPv6 出口(可空,空则从本机网卡兜底选取)。
func probeIPv6Availability(ctx context.Context, globalAddr string) IPv6Availability {
	avail := IPv6Availability{CheckedAt: localTimestamp()}

	// ① 公网可路由地址(复用上游已拿到的数据,无额外网络开销)
	if globalAddr != "" && isPublicIPv6(net.ParseIP(globalAddr)) {
		avail.HasGlobalAddress = true
		avail.GlobalAddress = globalAddr
	} else if local := pickPublicIPv6FromInterfaces(); local != "" {
		avail.HasGlobalAddress = true
		avail.GlobalAddress = local
	}

	// 无公网地址时出站层不可能通,快速给结论,不做任何应用层拨号。
	if !avail.HasGlobalAddress {
		avail.Summary = ipv6SummaryNoGlobal
		return avail
	}

	// ③ 应用层 HTTPS over IPv6 —— 最强信号:成功则出站与 DNS 必然成立。
	if host, latency, ok := probeIPv6HTTPS(ctx); ok {
		avail.HTTPSReachable = true
		avail.DNSResolvable = true // 强制 tcp6 的 HTTPS 成功隐含 AAAA 解析成功
		avail.OutboundReachable = true
		avail.OutboundLatencyMS = latency
		avail.OutboundTarget = host // 应用层命中的域名即出站证据
		avail.Summary = summarizeIPv6(avail)
		return avail
	}

	// HTTPS 失败,回退到逐层探测以定位问题:
	// ② 出站连通(国内 anycast,任一通即可)
	if target, latency, ok := probeIPv6Outbound(ctx); ok {
		avail.OutboundReachable = true
		avail.OutboundTarget = target
		avail.OutboundLatencyMS = latency
	}
	// ④ AAAA 解析(区分"路由不通"与"DNS 不返回 AAAA")
	avail.DNSResolvable = probeIPv6DNS(ctx)

	avail.Summary = summarizeIPv6(avail)
	return avail
}

// summarizeIPv6 综合各层结果给出结论(纯函数,便于测试)。
func summarizeIPv6(a IPv6Availability) string {
	if !a.HasGlobalAddress {
		return ipv6SummaryNoGlobal
	}
	if !a.OutboundReachable {
		return ipv6SummaryAddressOnly
	}
	if a.HTTPSReachable && a.DNSResolvable {
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
		dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
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

// probeIPv6HTTPS 用强制 tcp6 的 client 对国内双栈域名发 HTTPS GET,
// 任一域名应用层(TLS/HTTP)可用即算通,返回命中域名与连接延迟。用 GET 而非
// HEAD —— 部分站点(如腾讯)对 HEAD 返回 501。
func probeIPv6HTTPS(ctx context.Context) (host string, latencyMS int64, ok bool) {
	client := &http.Client{
		Timeout: 4 * time.Second,
		Transport: &http.Transport{
			DialContext: func(c context.Context, _, addr string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(c, "tcp6", addr)
			},
			TLSHandshakeTimeout: 3 * time.Second,
			DisableKeepAlives:   true,
		},
		// 不跟随重定向:拿到首个响应即证明 IPv6 应用层可达。
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	for _, h := range ipv6CheckHosts {
		if ctx.Err() != nil {
			return "", 0, false
		}
		reqCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "https://"+h, nil)
		if err != nil {
			cancel()
			continue
		}
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

// probeIPv6DNS 验证国内双栈域名能否解析出 AAAA 记录(应用走 IPv6 的前提),
// 任一域名解析到 AAAA 即算通。
func probeIPv6DNS(ctx context.Context) bool {
	for _, host := range ipv6CheckHosts {
		if ctx.Err() != nil {
			return false
		}
		reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		ips, err := net.DefaultResolver.LookupIP(reqCtx, "ip6", host)
		cancel()
		if err == nil && len(ips) > 0 {
			return true
		}
	}
	return false
}

// pickPublicIPv6FromInterfaces 从受监控网卡中选一个公网可路由 IPv6(不限国内),
// 用作 egress 出口检测失败时的兜底地址来源。
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
	for _, iface := range ifaces {
		if _, ok := target[iface.Name]; !ok {
			continue
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
