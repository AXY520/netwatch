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
	"sync"
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

// International egress lookup probes several overseas sources but returns only
// one result. In split-routing environments, different "what is my IP" APIs may
// be classified differently by the upstream router; prefer a non-mainland
// result when one is available, otherwise show the first valid result.
var internationalEgressProviders = []egressProvider{
	{name: "ifconfig.co", scope: "global", fetch: fetchIfconfigCO},
	{name: "icanhazip.com", scope: "global", fetch: fetchICanHazIP},
	{name: "ip.sb", scope: "global", fetch: fetchIPSB},
	{name: "ipwho.is", scope: "global", fetch: fetchIPWhoIs},
	{name: "ipinfo.io", scope: "global", fetch: fetchIPInfo},
}

// LookupEgressIPs returns one international egress result, total timeout 10s.
func LookupEgressIPs(ctx context.Context) EgressLookupResult {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	candidates := make([]EgressLookup, len(internationalEgressProviders))
	var wg sync.WaitGroup
	for i, p := range internationalEgressProviders {
		wg.Add(1)
		go func(i int, p egressProvider) {
			defer wg.Done()
			start := time.Now()
			lu, err := p.fetch(ctx)
			lu.Provider = p.name
			lu.Scope = p.scope
			lu.DurationMS = time.Since(start).Milliseconds()
			if err != nil {
				lu.Error = err.Error()
			}
			candidates[i] = lu
		}(i, p)
	}
	wg.Wait()
	return EgressLookupResult{
		GeneratedAt: localTimestamp(),
		Lookups:     []EgressLookup{pickInternationalEgress(candidates)},
	}
}

func pickInternationalEgress(candidates []EgressLookup) EgressLookup {
	var firstValid EgressLookup
	var firstUnknownCountry EgressLookup
	var firstError EgressLookup
	for _, item := range candidates {
		if item.IP == "" {
			if firstError.Provider == "" && item.Error != "" {
				firstError = item
			}
			continue
		}
		if firstValid.Provider == "" {
			firstValid = item
		}
		if strings.TrimSpace(item.Country) == "" {
			if firstUnknownCountry.Provider == "" {
				firstUnknownCountry = item
			}
			continue
		}
		if !isMainlandChina(item.Country) {
			return item
		}
	}
	if firstUnknownCountry.Provider != "" {
		return firstUnknownCountry
	}
	if firstValid.Provider != "" {
		return firstValid
	}
	return firstError
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
	req.Header.Set("User-Agent", "netwatch/1.0")

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
