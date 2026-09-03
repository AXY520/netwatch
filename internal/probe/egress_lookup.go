package probe

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// egressHTTPClient is used for external egress lookup endpoints.
// Respects HTTP_PROXY so it goes through the proxy if configured.
var egressHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   6 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		ForceAttemptHTTP2:     false,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	},
}

// domesticHTTPClient is used for domestic endpoints (cip.cc, zxinc, ipip.net).
// It bypasses HTTP_PROXY so domestic lookups return the real domestic IP
// instead of the proxy exit IP, and forces IPv4 to avoid broken IPv6 routing.
var domesticHTTPClient = &http.Client{
	Timeout: 8 * time.Second,
	Transport: &http.Transport{
		Proxy:                 nil, // bypass proxy for domestic lookups
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 6 * time.Second,
		ForceAttemptHTTP2:     false,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, "tcp4", addr)
		},
	},
}

type egressProvider struct {
	name  string
	scope string
	fetch func(ctx context.Context) (EgressLookup, error)
}

// 出口查询只保留一个主源和一个备用源，按顺序执行并在成功后立即停止。
var internationalEgressProviders = []egressProvider{
	{name: "ifconfig.co", scope: "global", fetch: fetchIfconfigCO},
	{name: "ip.sb", scope: "global", fetch: fetchIPSB},
}

// LookupEgressIPs returns one international egress result. It never races
// public providers and never attempts more than one fallback.
func LookupEgressIPs(ctx context.Context) EgressLookupResult {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return lookupEgressWithProviders(ctx, internationalEgressProviders)
}

func lookupEgressWithProviders(ctx context.Context, providers []egressProvider) EgressLookupResult {
	var firstFailure EgressLookup
	for i, provider := range providers {
		if i >= 2 || ctx.Err() != nil {
			break
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		start := time.Now()
		lookup, err := provider.fetch(attemptCtx)
		cancel()
		lookup.Provider = provider.name
		lookup.Scope = provider.scope
		lookup.DurationMS = time.Since(start).Milliseconds()
		if err != nil {
			lookup.Error = err.Error()
		}
		if strings.TrimSpace(lookup.IP) != "" && net.ParseIP(strings.TrimSpace(lookup.IP)) != nil && lookup.Error == "" {
			return EgressLookupResult{GeneratedAt: localTimestamp(), Lookups: []EgressLookup{lookup}}
		}
		if firstFailure.Provider == "" {
			if lookup.Error == "" {
				lookup.Error = "未返回有效公网 IP"
			}
			firstFailure = lookup
		}
	}
	return EgressLookupResult{
		GeneratedAt: localTimestamp(),
		Lookups:     []EgressLookup{firstFailure},
	}
}

func pickInternationalEgress(candidates []EgressLookup) EgressLookup {
	// Prefer majority agreement on the observed public IP. A single flaky
	// overseas source (wrong anycast / middlebox) must not override the rest.
	type bucket struct {
		sample   EgressLookup
		count    int
		overseas int
		mainland int
	}
	byIP := map[string]*bucket{}
	var order []string
	var firstError EgressLookup
	for _, item := range candidates {
		ip := strings.TrimSpace(item.IP)
		if ip == "" || net.ParseIP(ip) == nil {
			if firstError.Provider == "" && item.Error != "" {
				firstError = item
			}
			continue
		}
		b := byIP[ip]
		if b == nil {
			b = &bucket{sample: item, count: 0}
			byIP[ip] = b
			order = append(order, ip)
		}
		b.count++
		// Keep the sample that carries the richest geo metadata.
		if strings.TrimSpace(item.Country) != "" && strings.TrimSpace(b.sample.Country) == "" {
			b.sample = item
		}
		if strings.TrimSpace(item.Country) == "" {
			continue
		}
		if isMainlandChina(item.Country) {
			b.mainland++
		} else {
			b.overseas++
		}
	}
	if len(order) == 0 {
		return firstError
	}

	// Score: count (agreement) first; when tied, prefer overseas only if that
	// IP itself has overseas geo evidence (split-route / proxy case).
	bestIP := order[0]
	bestScore := -1
	for _, ip := range order {
		b := byIP[ip]
		score := b.count * 10
		if b.overseas > 0 && b.overseas >= b.mainland {
			score += 3
		}
		// Prefer entries that actually have country metadata.
		if strings.TrimSpace(b.sample.Country) != "" {
			score += 1
		}
		if score > bestScore {
			bestScore = score
			bestIP = ip
		}
	}
	return byIP[bestIP].sample
}

func httpGetJSON(ctx context.Context, client *http.Client, url string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 netwatch")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(target)
}

func httpGetText(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 netwatch")
	req.Header.Set("Accept", "text/plain")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

// isMainlandChina returns true when a Country/location string clearly
// resolves to mainland China.
func isMainlandChina(country string) bool {
	c := strings.TrimSpace(country)
	if c == "" {
		return false
	}
	return strings.Contains(c, "中国") || strings.Contains(c, "China") || strings.EqualFold(c, "CN")
}

// --- ifconfig.co (global; often follows upstream overseas routing) ---

type ifconfigCOResponse struct {
	IP         string `json:"ip"`
	Country    string `json:"country"`
	CountryISO string `json:"country_iso"`
	Region     string `json:"region_name"`
	City       string `json:"city"`
	ASN        string `json:"asn"`
	ASNOrg     string `json:"asn_org"`
}

func fetchIfconfigCO(ctx context.Context) (EgressLookup, error) {
	var p ifconfigCOResponse
	if err := httpGetJSON(ctx, egressHTTPClient, "https://ifconfig.co/json", &p); err != nil {
		return EgressLookup{}, err
	}
	ip := strings.TrimSpace(p.IP)
	if ip == "" {
		return EgressLookup{}, fmt.Errorf("ifconfig.co: empty ip")
	}
	return EgressLookup{
		IP:      ip,
		Country: firstNonEmpty(p.Country, p.CountryISO),
		Region:  p.Region,
		City:    p.City,
		ASN:     p.ASN,
		ISP:     p.ASNOrg,
	}, nil
}

// --- icanhazip.com (plain global IP fallback) ---

func fetchICanHazIP(ctx context.Context) (EgressLookup, error) {
	ip, err := httpGetText(ctx, egressHTTPClient, "https://icanhazip.com")
	if err != nil {
		return EgressLookup{}, err
	}
	ip = strings.TrimSpace(ip)
	if net.ParseIP(ip) == nil {
		return EgressLookup{}, fmt.Errorf("icanhazip.com: invalid ip")
	}
	lookup := lookupIPLocation(ctx, ip, 4*time.Second)
	return EgressLookup{
		IP:      ip,
		Country: lookup.Country,
		Region:  lookup.Region,
		City:    lookup.City,
		ISP:     lookup.ISP,
	}, nil
}

// --- cip.cc (domestic) ---

func fetchCipCC(ctx context.Context) (EgressLookup, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://cip.cc", nil)
	if err != nil {
		return EgressLookup{}, err
	}
	// cip.cc only returns CLI text for curl-like UAs; generic/custom UAs get HTML or hang.
	req.Header.Set("User-Agent", "curl/8.5.0 (netwatch/1.0)")
	req.Header.Set("Accept", "text/plain")

	resp, err := domesticHTTPClient.Do(req)
	if err != nil {
		return EgressLookup{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return EgressLookup{}, fmt.Errorf("cip.cc: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return EgressLookup{}, err
	}

	return parseCipCCResponse(string(body))
}

func parseCipCCResponse(text string) (EgressLookup, error) {
	var out EgressLookup
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch {
		case key == "IP":
			out.IP = val
		case strings.HasPrefix(key, "地址"):
			locParts := strings.Fields(val)
			if len(locParts) >= 1 {
				out.Country = locParts[0]
			}
			if len(locParts) >= 2 {
				out.Region = locParts[1]
			}
			if len(locParts) >= 3 {
				out.City = locParts[2]
			}
		case strings.HasPrefix(key, "运营商"):
			out.ISP = val
		}
	}
	if out.IP == "" {
		return EgressLookup{}, fmt.Errorf("cip.cc: empty ip")
	}
	return out, nil
}

// --- ip.sb (global, reliable) ---

type ipsbResponse struct {
	IP          string `json:"ip"`
	CountryCode string `json:"country_code"`
	Country     string `json:"country"`
	Region      string `json:"region"`
	City        string `json:"city"`
	ISP         string `json:"isp"`
	ASN         int    `json:"asn"`
	ASNName     string `json:"asn_organization"`
}

func fetchIPSB(ctx context.Context) (EgressLookup, error) {
	var p ipsbResponse
	if err := httpGetJSON(ctx, egressHTTPClient, "https://api.ip.sb/geoip", &p); err != nil {
		return EgressLookup{}, err
	}
	ip := strings.TrimSpace(p.IP)
	if ip == "" {
		return EgressLookup{}, fmt.Errorf("ip.sb: empty ip")
	}
	isp := p.ISP
	if p.ASN > 0 {
		asn := fmt.Sprintf("AS%d", p.ASN)
		if p.ASNName != "" {
			asn += " " + p.ASNName
		}
		if isp != "" {
			isp = asn + " · " + isp
		} else {
			isp = asn
		}
	}
	return EgressLookup{
		IP:      ip,
		Country: firstNonEmpty(p.Country, p.CountryCode),
		Region:  p.Region,
		City:    p.City,
		ISP:     isp,
	}, nil
}

// --- ipwho.is (global, free, reliable) ---

type ipwhoisResponse struct {
	IP         string `json:"ip"`
	Success    bool   `json:"success"`
	Country    string `json:"country"`
	Region     string `json:"region"`
	City       string `json:"city"`
	Message    string `json:"message"`
	Connection struct {
		ISP string `json:"isp"`
		Org string `json:"org"`
		ASN int    `json:"asn"`
	} `json:"connection"`
}

func fetchIPWhoIs(ctx context.Context) (EgressLookup, error) {
	var p ipwhoisResponse
	if err := httpGetJSON(ctx, egressHTTPClient, "https://ipwho.is/", &p); err != nil {
		return EgressLookup{}, err
	}
	if !p.Success {
		return EgressLookup{}, fmt.Errorf("ipwho.is: %s", firstNonEmpty(p.Message, "failed"))
	}
	ip := strings.TrimSpace(p.IP)
	if ip == "" {
		return EgressLookup{}, fmt.Errorf("ipwho.is: empty ip")
	}
	asn := ""
	if p.Connection.ASN > 0 {
		asn = fmt.Sprintf("AS%d", p.Connection.ASN)
	}
	return EgressLookup{
		IP:      ip,
		Country: p.Country,
		Region:  p.Region,
		City:    p.City,
		ASN:     asn,
		ISP:     firstNonEmpty(p.Connection.ISP, p.Connection.Org),
	}, nil
}

// --- ipinfo.io (global fallback) ---

func fetchIPInfo(ctx context.Context) (EgressLookup, error) {
	var p ipInfoResponse
	if err := httpGetJSON(ctx, egressHTTPClient, "https://ipinfo.io/json", &p); err != nil {
		return EgressLookup{}, err
	}
	ip := strings.TrimSpace(p.IP)
	if ip == "" {
		return EgressLookup{}, fmt.Errorf("ipinfo.io: empty ip")
	}
	return EgressLookup{
		IP:      ip,
		Country: p.Country,
		Region:  p.Region,
		City:    p.City,
		ISP:     p.Org,
	}, nil
}
