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

	"golang.org/x/text/encoding/simplifiedchinese"
)

type pcOnlineIPResp struct {
	IP   string `json:"ip"`
	Pro  string `json:"pro"`
	City string `json:"city"`
	Addr string `json:"addr"`
	Err  string `json:"err"`
}

func lookupPConlineLocation(ctx context.Context, ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://whois.pconline.com.cn/ipJson.jsp?ip="+url.QueryEscape(ip)+"&json=true", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "netwatch/1.0")
	req.Header.Set("Referer", "https://whois.pconline.com.cn/")

	resp, err := egressHTTPClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return ""
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(decodePCOnlineJSON(body))
	if text == "" {
		return ""
	}

	var payload pcOnlineIPResp
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return ""
	}

	addr := strings.TrimSpace(payload.Addr)
	if addr != "" {
		return addr
	}
	parts := []string{strings.TrimSpace(payload.Pro), strings.TrimSpace(payload.City)}
	return strings.TrimSpace(strings.Join(filterNonEmpty(parts), " "))
}

func classifyIPLocation(ctx context.Context, ip string) string {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return ""
	}
	if isPrivateIPv4(parsed) || isPrivateIPv6(parsed) {
		return "局域网"
	}
	if isCGNATIPv4(parsed) {
		return "运营商级NATIP地址"
	}
	return firstNonEmpty(lookupIPAPI(ctx, ip), lookupPConlineLocation(ctx, ip))
}

func decodePCOnlineJSON(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	text := string(body)
	if decoded, err := simplifiedchinese.GBK.NewDecoder().String(text); err == nil {
		text = decoded
	}
	start := strings.Index(text, "{")
	if start >= 0 {
		text = text[start:]
	}
	return strings.TrimSpace(strings.ToValidUTF8(text, ""))
}

func filterNonEmpty(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			out = append(out, strings.TrimSpace(item))
		}
	}
	return out
}

func lookupIPAPI(ctx context.Context, ip string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://ip-api.com/json/"+url.PathEscape(ip)+"?lang=zh-CN", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "netwatch/1.0")

	resp, err := egressHTTPClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return ""
	}

	var payload struct {
		Status     string `json:"status"`
		Message    string `json:"message"`
		Country    string `json:"country"`
		RegionName string `json:"regionName"`
		City       string `json:"city"`
		ISP        string `json:"isp"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&payload); err != nil {
		return ""
	}
	if payload.Status != "success" {
		return ""
	}
	parts := filterNonEmpty([]string{payload.Country, payload.RegionName, payload.City, payload.ISP})
	return strings.Join(parts, " ")
}

func fetchGlobalEgress(ctx context.Context) (EgressLookup, error) {
	type fetcher func(context.Context) (EgressLookup, error)
	sources := []struct {
		name string
		fn   fetcher
	}{
		{name: "IP.SB", fn: fetchIPSB},
		{name: "ipwho.is", fn: fetchIPWhoIs},
	}

	var lastErr error
	for _, source := range sources {
		out, err := source.fn(ctx)
		if err == nil && strings.TrimSpace(out.IP) != "" {
			out.Provider = source.name
			return out, nil
		}
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", source.name, err)
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no global source available")
	}
	return EgressLookup{}, lastErr
}

func isPrivateIPv4(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
	}
	for _, cidr := range privateRanges {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(v4) {
			return true
		}
	}
	return false
}

func isCGNATIPv4(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	_, network, _ := net.ParseCIDR("100.64.0.0/10")
	return network.Contains(v4)
}

func isPrivateIPv6(ip net.IP) bool {
	if ip == nil || ip.To4() != nil {
		return false
	}
	privateRanges := []string{
		"::/128",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}
	for _, cidr := range privateRanges {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func fetchIPSB(ctx context.Context) (EgressLookup, error) {
	var r struct {
		IP              string `json:"ip"`
		City            string `json:"city"`
		Region          string `json:"region"`
		Country         string `json:"country"`
		ISP             string `json:"isp"`
		ASNOrganization string `json:"asn_organization"`
		ASN             int    `json:"asn"`
	}
	if err := httpGetJSON(ctx, "https://api.ip.sb/geoip", &r); err != nil {
		return EgressLookup{}, err
	}
	out := EgressLookup{
		IP:      r.IP,
		Country: r.Country,
		Region:  r.Region,
		City:    r.City,
		ISP:     firstNonEmpty(r.ISP, r.ASNOrganization),
	}
	if r.ASN > 0 {
		out.ASN = fmt.Sprintf("AS%d", r.ASN)
	}
	return out, nil
}

func fetchIPWhoIs(ctx context.Context) (EgressLookup, error) {
	var r struct {
		IP      string `json:"ip"`
		Success bool   `json:"success"`
		City    string `json:"city"`
		Region  string `json:"region"`
		Country string `json:"country"`
		Connection struct {
			ASN  int    `json:"asn"`
			Org  string `json:"org"`
			ISP  string `json:"isp"`
		} `json:"connection"`
	}
	if err := httpGetJSON(ctx, "https://ipwho.is/", &r); err != nil {
		return EgressLookup{}, err
	}
	if !r.Success {
		return EgressLookup{}, fmt.Errorf("query failed")
	}
	out := EgressLookup{
		IP:      r.IP,
		Country: r.Country,
		Region:  r.Region,
		City:    r.City,
		ISP:     firstNonEmpty(r.Connection.ISP, r.Connection.Org),
	}
	if r.Connection.ASN > 0 {
		out.ASN = fmt.Sprintf("AS%d", r.Connection.ASN)
	}
	return out, nil
}
