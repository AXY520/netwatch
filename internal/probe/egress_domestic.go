package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"netwatch/internal/logger"
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
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic in domestic IPv4 lookup: %v", r)
			}
		}()
		out.IPv4 = lookupDomesticIPv4(ctx)
	}()
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic in domestic IPv6 lookup: %v", r)
			}
		}()
		out.IPv6 = lookupDomesticIPv6Local(ctx, cfg)
		if out.IPv6.IP == "" {
			out.IPv6 = lookupDomesticIPv6(ctx)
		}
		// IPv6 真实可用性分层检测(地址→出站→HTTPS→DNS)。
		out.IPv6Avail = probeIPv6Availability(ctx, out.IPv6.IP)
	}()
	wg.Wait()
	return out
}

// lookupDomesticIPv4 queries domestic sources in a stable order. Do not race
// these endpoints: some sources may follow upstream overseas routing and return
// the international exit faster than the real domestic result.
func lookupDomesticIPv4(ctx context.Context) DomesticIPEntry {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	type source struct {
		name string
		fn   func(context.Context) DomesticIPEntry
	}
	sources := []source{
		{name: "cip.cc", fn: lookupDomesticIPv4ViaCipCC},
		{name: "ipip.net", fn: lookupDomesticIPv4ViaIPIP},
		{name: "zxinc", fn: lookupDomesticIPv4ViaZXINC},
	}

	var lastErr string
	for _, s := range sources {
		select {
		case <-ctx.Done():
			return DomesticIPEntry{Error: firstNonEmpty(lastErr, "国内 IPv4 查询超时")}
		default:
		}
		entry := s.fn(ctx)
		if entry.Source == "" {
			entry.Source = s.name
		}
		if entry.IP != "" && entry.Error == "" {
			return entry
		}
		lastErr = firstNonEmpty(entry.Error, lastErr)
	}
	return DomesticIPEntry{Error: firstNonEmpty(lastErr, "所有国内 IPv4 源均失败")}
}

// lookupDomesticIPv6 queries ZXINC v6 after local NIC detection fails.
func lookupDomesticIPv6(ctx context.Context) DomesticIPEntry {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return lookupDomesticIPv6ViaZXINC(ctx)
}

// lookupDomesticIPv6Local prefers a public IPv6 from the host's own NICs
// that falls inside a known mainland-China carrier prefix.
func lookupDomesticIPv6Local(ctx context.Context, cfg Config) DomesticIPEntry {
	entry := DomesticIPEntry{Source: "local-nic"}
	ip := pickDomesticIPv6FromInterfaces()
	if ip == "" {
		return entry
	}
	entry.IP = ip
	entry.HasPublicPath = true
	if location, isp, err := fetchZXINCLocation(ctx, ip); err == nil {
		entry.Location = normalizeDomesticLocation(firstNonEmpty(location, lookupPConlineLocation(ctx, ip)))
		entry.ISP = isp
	} else if !isCNIPv6(net.ParseIP(ip)) {
		entry.IP = ""
		entry.HasPublicPath = false
		entry.Error = "海外 IPv6 出口或归属未知: " + ip
		return entry
	}
	if !isDomesticIPv6Candidate(ip, entry.Location) {
		entry.IP = ""
		entry.HasPublicPath = false
		entry.Error = "海外 IPv6 出口: " + firstNonEmpty(entry.Location, ip)
	}
	return entry
}

func pickDomesticIPv6FromInterfaces() string {
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
		"2408:8000::/20",
		"2409::/20",
		"2409:8000::/20",
		"2400:3200::/32",
		"2001:da8::/32",
	} {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if n.Contains(ip) {
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
	if !isMainlandChinaLocation(location) {
		entry.Error = "海外 IPv4 出口: " + firstNonEmpty(location, ip)
		entry.IP = ""
		entry.HasPublicPath = false
		return entry
	}
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
	if !isDomesticIPv6Candidate(ip, location) {
		entry.Error = "海外 IPv6 出口: " + firstNonEmpty(location, ip)
		entry.IP = ""
		entry.HasPublicPath = false
		return entry
	}
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
		Ret  string `json:"ret"`
		Data struct {
			IP       string   `json:"ip"`
			Location []string `json:"location"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&payload); err != nil {
		entry.Error = err.Error()
		return entry
	}
	entry.IP = payload.Data.IP
	entry.HasPublicPath = isPublicIP(payload.Data.IP)
	locParts := filterNonEmpty(payload.Data.Location)
	loc := strings.TrimSpace(strings.Join(locParts, " "))
	if !isMainlandChinaLocation(loc) {
		entry.Error = "海外出口: " + loc
		entry.IP = ""
		entry.HasPublicPath = false
		return entry
	}
	entry.Location = loc
	if len(locParts) > 0 {
		entry.ISP = locParts[len(locParts)-1]
	}
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ip.zxinc.org/api.php?type=json&ip="+url.QueryEscape(ip), nil)
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
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func isDomesticIPv6Candidate(rawIP, location string) bool {
	ip := net.ParseIP(strings.TrimSpace(rawIP))
	if ip == nil || ip.To4() != nil {
		return false
	}
	if isCNIPv6(ip) {
		return true
	}
	return isMainlandChinaLocation(location)
}

func isMainlandChinaLocation(location string) bool {
	loc := strings.TrimSpace(location)
	if loc == "" {
		return false
	}
	if strings.Contains(loc, "香港") || strings.Contains(loc, "澳门") || strings.Contains(loc, "台湾") ||
		strings.Contains(strings.ToLower(loc), "hong kong") || strings.Contains(strings.ToLower(loc), "macao") ||
		strings.Contains(strings.ToLower(loc), "macau") || strings.Contains(strings.ToLower(loc), "taiwan") {
		return false
	}
	return strings.Contains(loc, "中国") || strings.Contains(strings.ToLower(loc), "china")
}

func normalizeDomesticLocation(value string) string {
	value = strings.ReplaceAll(value, "\t", " ")
	value = strings.Join(strings.Fields(value), " ")
	value = strings.TrimSpace(value)
	return strings.ReplaceAll(value, " 中国联通", "")
}
