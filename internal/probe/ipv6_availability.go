package probe

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	ipv6ProbeTotalTimeout      = 10 * time.Second
	ipv6HTTPSLayerTimeout      = 6 * time.Second
	ipv6HTTPSAttemptTimeout    = 3 * time.Second
	ipv6OutboundLayerTimeout   = 3 * time.Second
	ipv6OutboundAttemptTimeout = 1500 * time.Millisecond
)

// 每层只保留一个主目标和一个不同服务商的备用目标。探测按顺序执行，成功即停。
var ipv6OutboundTargets = []string{
	"[2400:3200:baba::1]:443", // 阿里公共 DNS（主）
	"[240e:1:68::68]:443",     // 电信公共 DNS（备）
}

var ipv6CheckHosts = []string{
	"www.baidu.com", // 百度（主）
	"www.qq.com",    // 腾讯（备）
}

// IPv6 可用性结论。
const (
	ipv6SummaryFullyUsable  = "fully_usable"  // 地址 + 出站 + 应用层 + DNS 全通
	ipv6SummaryOutboundOnly = "outbound_only" // 出站通但应用层/DNS 不完整
	ipv6SummaryAddressOnly  = "address_only"  // 有公网地址但出站不可达（路由不通）
	ipv6SummaryNoGlobal     = "no_global"     // 无公网可路由 IPv6
)

type ipv6HTTPSProbeResult struct {
	host          string
	latencyMS     int64
	localAddress  string
	dnsResolvable bool
	httpsError    string
	dnsError      string
}

type ipv6OutboundProbeResult struct {
	target       string
	latencyMS    int64
	localAddress string
	err          string
}

type ipv6ProbeOps struct {
	pickAddress func() string
	https       func(context.Context) ipv6HTTPSProbeResult
	outbound    func(context.Context) ipv6OutboundProbeResult
}

var defaultIPv6ProbeOps = ipv6ProbeOps{
	pickAddress: pickPublicIPv6FromInterfaces,
	https:       probeIPv6HTTPS,
	outbound:    probeIPv6Outbound,
}

// probeIPv6Availability 分层检测 IPv6 真实可用性。
//
//  1. 只从本地网卡读取公网地址；没有地址时不访问公网。
//  2. HTTPS 层按“主目标 → 一个备用目标”串行检测，成功即停；HTTPS
//     已经同时证明 DNS、IPv6 出站和应用层可用，不再发起额外请求。
//  3. 只有 HTTPS 失败时才补做固定 IPv6 的 TCP 出站检测；DNS 结果复用
//     HTTPS 前已经执行过的 AAAA 解析，不再单独重复查询。
func probeIPv6Availability(ctx context.Context, globalAddr string) IPv6Availability {
	return probeIPv6AvailabilityWithOps(ctx, globalAddr, defaultIPv6ProbeOps)
}

func probeIPv6AvailabilityWithOps(ctx context.Context, globalAddr string, ops ipv6ProbeOps) IPv6Availability {
	probeCtx, cancel := context.WithTimeout(ctx, ipv6ProbeTotalTimeout)
	defer cancel()

	avail := IPv6Availability{CheckedAt: localTimestamp()}
	if globalAddr != "" && isPublicIPv6(net.ParseIP(globalAddr)) {
		avail.HasGlobalAddress = true
		avail.GlobalAddress = globalAddr
	} else if ops.pickAddress != nil {
		if local := ops.pickAddress(); local != "" && isPublicIPv6(net.ParseIP(local)) {
			avail.HasGlobalAddress = true
			avail.GlobalAddress = local
		}
	}

	// 没有可路由地址时，连接必然无法建立。直接返回，避免一次没有意义的公网请求。
	if !avail.HasGlobalAddress {
		avail.AddressError = "未找到公网可路由 IPv6 地址"
		avail.Summary = ipv6SummaryNoGlobal
		return avail
	}

	if ops.https != nil {
		httpsCtx, httpsCancel := context.WithTimeout(probeCtx, ipv6HTTPSLayerTimeout)
		result := ops.https(httpsCtx)
		httpsCancel()
		avail.DNSResolvable = result.dnsResolvable
		avail.HTTPSError = result.httpsError
		avail.DNSError = result.dnsError
		if result.host != "" {
			avail.HTTPSReachable = true
			avail.DNSResolvable = true
			avail.OutboundReachable = true
			avail.OutboundLatencyMS = result.latencyMS
			avail.OutboundTarget = result.host
			useIPv6ProbeSourceAddress(&avail, result.localAddress)
			avail.HTTPSError = ""
			avail.DNSError = ""
			avail.Summary = summarizeIPv6(avail)
			return avail
		}
	}

	if ops.outbound != nil && probeCtx.Err() == nil {
		outboundCtx, outboundCancel := context.WithTimeout(probeCtx, ipv6OutboundLayerTimeout)
		result := ops.outbound(outboundCtx)
		outboundCancel()
		avail.OutboundError = result.err
		if result.target != "" {
			avail.OutboundReachable = true
			avail.OutboundTarget = result.target
			avail.OutboundLatencyMS = result.latencyMS
			avail.OutboundError = ""
			useIPv6ProbeSourceAddress(&avail, result.localAddress)
		}
	}

	if !avail.DNSResolvable && avail.DNSError == "" {
		avail.DNSError = "AAAA 解析失败"
	}
	if !avail.HTTPSReachable && avail.HTTPSError == "" {
		avail.HTTPSError = "未能通过 IPv6 完成 HTTPS 请求"
	}
	if !avail.OutboundReachable && avail.OutboundError == "" {
		avail.OutboundError = "IPv6 出站连接失败"
	}
	avail.Summary = summarizeIPv6(avail)
	return avail
}

func useIPv6ProbeSourceAddress(avail *IPv6Availability, raw string) {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil || !isPublicIPv6(ip) {
		return
	}
	avail.GlobalAddress = ip.String()
	avail.HasGlobalAddress = true
}

// summarizeIPv6 综合各层结果给出结论（纯函数，便于测试）。
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

// probeIPv6Outbound 依次尝试固定 IPv6 TCP 443，最多一个备用目标。
func probeIPv6Outbound(ctx context.Context) ipv6OutboundProbeResult {
	return probeIPv6OutboundWith(ctx, ipv6OutboundTargets, func(ctx context.Context, target string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp6", target)
	})
}

func probeIPv6OutboundWith(ctx context.Context, targets []string, dial func(context.Context, string) (net.Conn, error)) ipv6OutboundProbeResult {
	var failures []string
	for i, target := range targets {
		if i >= 2 || ctx.Err() != nil {
			break
		}
		attemptCtx, cancel := context.WithTimeout(ctx, ipv6OutboundAttemptTimeout)
		start := time.Now()
		conn, err := dial(attemptCtx, target)
		cancel()
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", target, err))
			continue
		}
		latency := time.Since(start).Milliseconds()
		localAddress := ipv6FromNetAddr(conn.LocalAddr())
		_ = conn.Close()
		return ipv6OutboundProbeResult{target: target, latencyMS: latency, localAddress: localAddress}
	}
	return ipv6OutboundProbeResult{err: strings.Join(failures, "; ")}
}

// probeIPv6HTTPS 强制使用 tcp6，并复用每个目标开始时获得的 AAAA 解析结果。
func probeIPv6HTTPS(ctx context.Context) ipv6HTTPSProbeResult {
	return probeIPv6HTTPSWith(ctx, ipv6CheckHosts, probeIPv6HTTPSHost)
}

func probeIPv6HTTPSWith(ctx context.Context, hosts []string, attempt func(context.Context, string) ipv6HTTPSProbeResult) ipv6HTTPSProbeResult {
	var out ipv6HTTPSProbeResult
	var httpsFailures []string
	var dnsFailures []string
	for i, host := range hosts {
		if i >= 2 || ctx.Err() != nil {
			break
		}
		result := attempt(ctx, host)
		if result.dnsResolvable {
			out.dnsResolvable = true
		}
		if result.dnsError != "" {
			dnsFailures = append(dnsFailures, host+": "+result.dnsError)
		}
		if result.host != "" {
			result.dnsResolvable = true
			result.dnsError = ""
			result.httpsError = ""
			return result
		}
		if result.httpsError != "" {
			httpsFailures = append(httpsFailures, host+": "+result.httpsError)
		}
	}
	out.httpsError = strings.Join(httpsFailures, "; ")
	if !out.dnsResolvable {
		out.dnsError = strings.Join(dnsFailures, "; ")
	}
	return out
}

func probeIPv6HTTPSHost(ctx context.Context, host string) ipv6HTTPSProbeResult {
	attemptCtx, cancel := context.WithTimeout(ctx, ipv6HTTPSAttemptTimeout)
	defer cancel()
	start := time.Now()

	ips, err := net.DefaultResolver.LookupIP(attemptCtx, "ip6", host)
	if err != nil {
		return ipv6HTTPSProbeResult{dnsError: err.Error()}
	}
	var remoteIP net.IP
	for _, ip := range ips {
		if ip != nil && ip.To4() == nil {
			remoteIP = ip
			break
		}
	}
	if remoteIP == nil {
		return ipv6HTTPSProbeResult{dnsError: "未返回 AAAA 记录"}
	}

	var localMu sync.Mutex
	var localAddress string
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(dialCtx context.Context, _, addr string) (net.Conn, error) {
			_, port, splitErr := net.SplitHostPort(addr)
			if splitErr != nil || port == "" {
				port = "443"
			}
			conn, dialErr := (&net.Dialer{}).DialContext(dialCtx, "tcp6", net.JoinHostPort(remoteIP.String(), port))
			if dialErr == nil {
				localMu.Lock()
				localAddress = ipv6FromNetAddr(conn.LocalAddr())
				localMu.Unlock()
			}
			return conn, dialErr
		},
		TLSHandshakeTimeout:   2 * time.Second,
		ResponseHeaderTimeout: 2 * time.Second,
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     false,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodHead, "https://"+host+"/", nil)
	if err != nil {
		return ipv6HTTPSProbeResult{dnsResolvable: true, httpsError: err.Error()}
	}
	req.Header.Set("User-Agent", "netwatch/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return ipv6HTTPSProbeResult{dnsResolvable: true, httpsError: err.Error()}
	}
	_ = resp.Body.Close()
	localMu.Lock()
	source := localAddress
	localMu.Unlock()
	return ipv6HTTPSProbeResult{
		host:          host,
		latencyMS:     time.Since(start).Milliseconds(),
		localAddress:  source,
		dnsResolvable: true,
	}
}

func ipv6FromNetAddr(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	if tcpAddr, ok := addr.(*net.TCPAddr); ok && tcpAddr.IP != nil && tcpAddr.IP.To4() == nil {
		return tcpAddr.IP.String()
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return ""
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || ip.To4() != nil {
		return ""
	}
	return ip.String()
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
	for _, name := range monitored {
		target[name] = struct{}{}
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
	if value := pickFrom(true); value != "" {
		return value
	}
	return pickFrom(false)
}
