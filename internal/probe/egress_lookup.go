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

var egressHTTPClient = &http.Client{
	Timeout: 12 * time.Second,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ForceAttemptHTTP2:     false,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	},
}

// domesticHTTPClient is used for domestic endpoints (cip.cc, zxinc).
// It forces IPv4 to avoid Go's happy-eyeballs trying IPv6 first in containers
// where IPv6 routing may be broken, and keeps a shorter timeout.
var domesticHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   6 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		ForceAttemptHTTP2:     false,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Force IPv4 for domestic endpoints — containers often have
			// broken IPv6 routing but DNS still returns AAAA records.
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp4", addr)
		},
	},
}

type egressProvider struct {
	name  string
	scope string
	fetch func(ctx context.Context) (EgressLookup, error)
}

// Egress lookup uses fixed endpoints:
//   - domestic perspective → http://cip.cc (纯文本，国内可靠)
//   - global   perspective → http://ip-api.com/json
var egressProviders = []egressProvider{
	{name: "cip.cc", scope: "domestic", fetch: fetchCipCC},
	{name: "ip-api.com", scope: "global", fetch: fetchIPAPI},
}

// LookupEgressIPs 并发查询所有源，总超时 15s。
func LookupEgressIPs(ctx context.Context) EgressLookupResult {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	results := make([]EgressLookup, len(egressProviders))
	var wg sync.WaitGroup
	for i, p := range egressProviders {
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
			results[i] = lu
		}(i, p)
	}
	wg.Wait()
	return EgressLookupResult{
		GeneratedAt: localTimestamp(),
		Lookups:     results,
	}
}

func httpGetJSON(ctx context.Context, url string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 netwatch")
	req.Header.Set("Accept", "application/json")
	resp, err := egressHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(target)
}

// isMainlandChina returns true when a Country/location string clearly
// resolves to mainland China. Tolerates both "中国" and "CN"/"China" so it
// works regardless of which provider produced the field.
func isMainlandChina(country string) bool {
	c := strings.TrimSpace(country)
	if c == "" {
		return false
	}
	return strings.Contains(c, "中国") || strings.Contains(c, "China") || strings.EqualFold(c, "CN")
}

// --- cip.cc (domestic) ---

// fetchCipCC 查询 https://cip.cc 获取国内出口 IP 信息。
// 返回格式为纯文本：
//
//	IP	: 1.2.3.4
//	地址	: 中国 湖北 武汉
//	运营商	: 联通
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
			// "中国 湖北 武汉" → Country="中国", Region="湖北", City="武汉"
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

// --- ip-api.com (global) ---

type ipapiResponse struct {
	Status     string  `json:"status"`
	Message    string  `json:"message"`
	Query      string  `json:"query"`
	Country    string  `json:"country"`
	RegionName string  `json:"regionName"`
	City       string  `json:"city"`
	ISP        string  `json:"isp"`
	Org        string  `json:"org"`
	AS         string  `json:"as"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
}

func fetchIPAPI(ctx context.Context) (EgressLookup, error) {
	var p ipapiResponse
	if err := httpGetJSON(ctx, "http://ip-api.com/json", &p); err != nil {
		return EgressLookup{}, err
	}
	if p.Status != "" && p.Status != "success" {
		return EgressLookup{}, fmt.Errorf("ip-api.com: %s", firstNonEmpty(p.Message, p.Status))
	}
	ip := strings.TrimSpace(p.Query)
	if ip == "" {
		return EgressLookup{}, fmt.Errorf("ip-api.com: empty query ip")
	}
	isp := p.ISP
	if isp == "" {
		isp = p.Org
	}
	if p.AS != "" {
		if isp != "" {
			isp = p.AS + " · " + isp
		} else {
			isp = p.AS
		}
	}
	return EgressLookup{
		IP:      ip,
		Country: p.Country,
		Region:  p.RegionName,
		City:    p.City,
		ISP:     isp,
	}, nil
}
