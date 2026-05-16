package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const zxincUserAgent = "netwatch/1.0"

type zxincLocationResponse struct {
	Code int `json:"code"`
	Data struct {
		Location string `json:"location"`
		Country  string `json:"country"`
		Local    string `json:"local"`
	} `json:"data"`
}

func lookupDomesticIPs(ctx context.Context, cfg Config) DomesticIPSnapshot {
	var out DomesticIPSnapshot
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		out.IPv4 = lookupDomesticIPVersion(ctx, 4)
	}()
	go func() {
		defer wg.Done()
		out.IPv6 = lookupDomesticIPv6Local(ctx, cfg)
		if out.IPv6.IP == "" {
			// 本机没有可用的国内 IPv6 段就回落到 ZXINC（可能给出隧道 IP）
			out.IPv6 = lookupDomesticIPVersion(ctx, 6)
		}
		if out.IPv6.IP != "" {
			out.IPv6.PortProbe = probeIPv6HighPort(ctx, cfg)
		} else {
			out.IPv6.PortProbe = IPReachabilityProbe{Status: "unavailable", Error: "未检测到 IPv6 出口"}
		}
	}()
	wg.Wait()
	return out
}

// lookupDomesticIPv6Local prefers a public IPv6 from the host's own NICs
// that falls inside a known mainland-China carrier prefix
// (240e:/2408:/2409:/2400:3200/2001:da8:). This avoids reporting a tunneled
// US IPv6 (e.g. DMIT 2605:52c0:) when the user's box also has a real CN IPv6
// from the ISP. Falls back to ZXINC when no such address is found.
func lookupDomesticIPv6Local(ctx context.Context, cfg Config) DomesticIPEntry {
	entry := DomesticIPEntry{Source: "local-nic"}
	ip := pickDomesticIPv6FromInterfaces(cfg.MonitoredNICs)
	if ip == "" {
		return entry
	}
	entry.IP = ip
	entry.HasPublicPath = true
	if location, isp, err := fetchZXINCLocation(ctx, ip); err == nil {
		entry.Location = normalizeDomesticLocation(firstNonEmpty(location, lookupPConlineLocation(ctx, ip)))
		entry.ISP = isp
	}
	return entry
}

func pickDomesticIPv6FromInterfaces(monitored []string) string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
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
			if isCNIPv6(ipNet.IP) {
				return ipNet.IP.String()
			}
		}
	}
	return ""
}

// isCNIPv6 checks whether an IPv6 address falls in a known mainland-China
// carrier or APNIC-CN allocation. Conservative — we don't try to be exhaustive,
// only cover the ones common in residential / SMB lines:
//
//	240e::/20  CT (China Telecom)
//	2408::/20  CU (China Unicom)
//	2409::/20  CM (China Mobile)
//	2400:3200::/32  Aliyun-CN
//	2001:da8::/32   CERNET
func isCNIPv6(ip net.IP) bool {
	if ip == nil || ip.To4() != nil {
		return false
	}
	for _, cidr := range []string{
		"240e::/20",
		"2408::/20",
		"2409::/20",
		"2400:3200::/32",
		"2001:da8::/32",
	} {
		_, n, _ := net.ParseCIDR(cidr)
		if n != nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

func lookupDomesticIPVersion(ctx context.Context, version int) DomesticIPEntry {
	if version == 4 {
		return lookupDomesticIPv4ViaCipCC(ctx)
	}
	return lookupDomesticIPv6ViaZXINC(ctx)
}

// lookupDomesticIPv4ViaCipCC calls http://cip.cc and packages the result as a
// DomesticIPEntry. cip.cc reaches us over the box's default IPv4 route,
// so the IP it sees is the actual v4 egress.
func lookupDomesticIPv4ViaCipCC(ctx context.Context) DomesticIPEntry {
	entry := DomesticIPEntry{Source: "cip.cc"}
	out, err := fetchCipCC(ctx)
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	if net.ParseIP(out.IP).To4() == nil {
		entry.Error = "cip.cc 未返回 IPv4"
		return entry
	}
	entry.IP = out.IP
	entry.HasPublicPath = isPublicIP(out.IP)
	loc := strings.TrimSpace(strings.Join([]string{out.Country, out.Region, out.City}, " "))
	if !isMainlandChina(out.Country) {
		return DomesticIPEntry{
			Source: entry.Source,
			Error:  "未检测到国内出口（实际经海外: " + strings.TrimSpace(loc) + "）",
		}
	}
	entry.Location = loc
	entry.ISP = out.ISP
	return entry
}

// lookupDomesticIPv6ViaZXINC is the fallback when the host doesn't have a CN
// IPv6 prefix on a NIC. ipinfo.io is reachable only over IPv4 from the box,
// so we keep ZXINC for the IPv6 path.
func lookupDomesticIPv6ViaZXINC(ctx context.Context) DomesticIPEntry {
	entry := DomesticIPEntry{Source: "zxinc"}
	ip, err := fetchZXINCIP(ctx, 6)
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	entry.IP = ip
	entry.HasPublicPath = isPublicIP(ip)
	if !entry.HasPublicPath {
		return entry
	}
	location, isp, err := fetchZXINCLocation(ctx, ip)
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	location = normalizeDomesticLocation(firstNonEmpty(location, lookupPConlineLocation(ctx, ip)))
	if !isMainlandChina(location) {
		return DomesticIPEntry{
			Source: entry.Source,
			Error:  "未检测到国内出口（实际经海外: " + location + "）",
		}
	}
	entry.Location = location
	entry.ISP = isp
	return entry
}

func fetchZXINCIP(ctx context.Context, version int) (string, error) {
	endpoint := "http://v4.ip.zxinc.org/getip"
	network := "tcp4"
	if version == 6 {
		endpoint = "http://v6.ip.zxinc.org/getip"
		network = "tcp6"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", zxincUserAgent)

	client := &http.Client{
		Timeout: 6 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
			},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(body))
	if parsed := net.ParseIP(ip); parsed == nil {
		return "", fmt.Errorf("invalid ip response")
	}
	return ip, nil
}

func fetchZXINCLocation(ctx context.Context, ip string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ip.zxinc.org/api.php?type=json&ip="+ip, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", zxincUserAgent)

	resp, err := domesticHTTPClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var payload zxincLocationResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&payload); err != nil {
		return "", "", err
	}
	if payload.Code != 0 {
		return "", "", fmt.Errorf("query failed")
	}
	return firstNonEmpty(payload.Data.Location, payload.Data.Country), payload.Data.Local, nil
}

func probeIPv6HighPort(ctx context.Context, cfg Config) IPReachabilityProbe {
	host := strings.TrimSpace(cfg.IPv6HighPortProbeHost)
	port := cfg.IPv6HighPortProbePort
	if host == "" || port <= 0 {
		return IPReachabilityProbe{Status: "unavailable", Error: "未配置探针"}
	}

	dialCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	start := time.Now()
	conn, err := (&net.Dialer{Timeout: 4 * time.Second}).DialContext(dialCtx, "tcp6", net.JoinHostPort(host, fmt.Sprintf("%d", port)))
	if err != nil {
		status := "blocked"
		if strings.Contains(strings.ToLower(err.Error()), "refused") {
			status = "closed"
		}
		return IPReachabilityProbe{
			Status: status,
			Error:  err.Error(),
		}
	}
	defer conn.Close()

	return IPReachabilityProbe{
		Status:     "reachable",
		LatencyMS:  time.Since(start).Milliseconds(),
		RemoteAddr: conn.RemoteAddr().String(),
	}
}

func isPublicIP(raw string) bool {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		return isPublicIPv4(v4)
	}
	return isPublicIPv6(ip)
}

func isPublicIPv6(ip net.IP) bool {
	if ip == nil {
		return false
	}
	privateRanges := []string{
		"::/128",
		"::1/128",
		"::ffff:0:0/96",
		"64:ff9b::/96",
		"100::/64",
		"2001:db8::/32",
		"fc00::/7",
		"fe80::/10",
	}
	for _, cidr := range privateRanges {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func normalizeDomesticLocation(value string) string {
	value = strings.ReplaceAll(value, "\t", " ")
	value = strings.Join(strings.Fields(value), " ")
	value = strings.TrimSpace(value)
	return strings.ReplaceAll(value, " 中国联通", "")
}
