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

// DomesticIPResult wraps a single domestic IP lookup result.
type domesticIPResult struct {
	Entry DomesticIPEntry
	Err   error
}

func lookupDomesticIPs(ctx context.Context, cfg Config) DomesticIPSnapshot {
	var out DomesticIPSnapshot
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		out.IPv4 = lookupDomesticIPv4(ctx)
	}()
	go func() {
		defer wg.Done()
		out.IPv6 = lookupDomesticIPv6Local(ctx, cfg)
		if out.IPv6.IP == "" {
			out.IPv6 = lookupDomesticIPv6(ctx)
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

// lookupDomesticIPv4 races multiple domestic IPv4 sources and returns the first
// successful result that identifies a mainland-China egress.
func lookupDomesticIPv4(ctx context.Context) DomesticIPEntry {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	type source struct {
		name string
		fn   func(context.Context) DomesticIPEntry
	}
	sources := []source{
		{name: "zxinc", fn: lookupDomesticIPv4ViaZXINC},
		{name: "cip.cc", fn: lookupDomesticIPv4ViaCipCC},
		{name: "ipip.net", fn: lookupDomesticIPv4ViaIPIP},
	}

	ch := make(chan domesticIPResult, len(sources))
	for _, s := range sources {
		go func(s source) {
			entry := s.fn(ctx)
			if entry.Source == "" {
				entry.Source = s.name
			}
			ch <- domesticIPResult{Entry: entry}
		}(s)
	}

	var lastErr error
	for range sources {
		select {
		case <-ctx.Done():
			return DomesticIPEntry{Error: firstNonEmpty(lastErr.Error(), "国内 IPv4 查询超时")}
		case res := <-ch:
			if res.Err != nil {
				lastErr = res.Err
				continue
			}
			if res.Entry.IP == "" {
				continue
			}
			// Accept any valid result; prefer mainland-China ones
			if res.Entry.Error == "" {
				return res.Entry
			}
			lastErr = fmt.Errorf("%s", res.Entry.Error)
		}
	}
	return DomesticIPEntry{Error: firstNonEmpty(lastErr.Error(), "所有国内 IPv4 源均失败")}
}

// lookupDomesticIPv6 races ZXINC v6 and local NIC detection.
func lookupDomesticIPv6(ctx context.Context) DomesticIPEntry {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	ch := make(chan domesticIPResult, 2)
	go func() {
		ch <- domesticIPResult{Entry: lookupDomesticIPv6ViaZXINC(ctx)}
	}()
	// local NIC scan is done separately in lookupDomesticIPs, so here just ZXINC
	res := <-ch
	if res.Err != nil {
		return DomesticIPEntry{Source: "zxinc", Error: res.Err.Error()}
	}
	return res.Entry
}

// lookupDomesticIPv6Local prefers a public IPv6 from the host's own NICs
// that falls inside a known mainland-China carrier prefix.
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
// carrier or APNIC-CN allocation.
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

// --- ZXINC (primary domestic source) ---

func lookupDomesticIPv4ViaZXINC(ctx context.Context) DomesticIPEntry {
	entry := DomesticIPEntry{Source: "zxinc"}
	ip, err := fetchZXINCIP(ctx, 4)
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
	entry.Location = location
	entry.ISP = isp
	return entry
}

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
	entry.Location = location
	entry.ISP = isp
	return entry
}

// --- cip.cc (fallback domestic source) ---

func lookupDomesticIPv4ViaCipCC(ctx context.Context) DomesticIPEntry {
	entry := DomesticIPEntry{Source: "cip.cc"}
	out, err := fetchCipCC(ctx)
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	if out.Provider != "" {
		entry.Source = out.Provider
	}
	if net.ParseIP(out.IP).To4() == nil {
		entry.Error = entry.Source + " 未返回 IPv4"
		return entry
	}
	entry.IP = out.IP
	entry.HasPublicPath = isPublicIP(out.IP)
	loc := strings.TrimSpace(strings.Join([]string{out.Country, out.Region, out.City}, " "))
	if !isMainlandChina(out.Country) {
		entry.Error = "海外出口: " + strings.TrimSpace(loc)
		return entry
	}
	entry.Location = loc
	entry.ISP = out.ISP
	return entry
}

// --- ipip.net (another domestic source) ---

func lookupDomesticIPv4ViaIPIP(ctx context.Context) DomesticIPEntry {
	entry := DomesticIPEntry{Source: "ipip.net"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://myip.ipip.net/json", nil)
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	resp, err := domesticHTTPClient.Do(req)
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		entry.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return entry
	}
	var payload struct {
		IP      string `json:"ip"`
		BeginIP string `json:"begin_ip"`
		Country string `json:"country"`
		City    string `json:"city"`
		ASN     string `json:"asn"`
		ISP     string `json:"isp"`
		Org     string `json:"org"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&payload); err != nil {
		entry.Error = err.Error()
		return entry
	}
	entry.IP = payload.IP
	entry.HasPublicPath = isPublicIP(payload.IP)
	loc := strings.TrimSpace(strings.Join([]string{payload.Country, payload.City}, " "))
	entry.Location = loc
	entry.ISP = firstNonEmpty(payload.ISP, payload.Org)
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
