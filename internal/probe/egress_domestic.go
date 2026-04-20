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
		out.IPv6 = lookupDomesticIPVersion(ctx, 6)
		if out.IPv6.IP != "" {
			out.IPv6.PortProbe = probeIPv6HighPort(ctx, cfg)
		} else {
			out.IPv6.PortProbe = IPReachabilityProbe{Status: "unavailable", Error: "未检测到 IPv6 出口"}
		}
	}()
	wg.Wait()
	return out
}

func lookupDomesticIPVersion(ctx context.Context, version int) DomesticIPEntry {
	entry := DomesticIPEntry{Source: "zxinc"}
	ip, err := fetchDomesticIP(ctx, version)
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
	entry.Location = normalizeDomesticLocation(firstNonEmpty(location, lookupPConlineLocation(ctx, ip)))
	entry.ISP = isp
	return entry
}

func fetchDomesticIP(ctx context.Context, version int) (string, error) {
	if version == 4 {
		return fetchIPIPDomesticIPv4(ctx)
	}
	return fetchZXINCIP(ctx, version)
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

func fetchIPIPDomesticIPv4(ctx context.Context) (string, error) {
	body, err := httpGetString(ctx, "https://myip.ipip.net/")
	if err != nil {
		return "", err
	}
	body = strings.TrimSpace(body)
	m := ipipRE.FindStringSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("parse failed: %s", body)
	}
	if net.ParseIP(m[1]) == nil || net.ParseIP(m[1]).To4() == nil {
		return "", fmt.Errorf("invalid ipv4 response")
	}
	return m[1], nil
}

func fetchZXINCLocation(ctx context.Context, ip string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ip.zxinc.org/api.php?type=json&ip="+ip, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", zxincUserAgent)

	resp, err := egressHTTPClient.Do(req)
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
