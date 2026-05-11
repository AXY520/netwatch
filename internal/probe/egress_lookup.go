package probe

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
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

type egressProvider struct {
	name  string
	scope string
	fetch func(ctx context.Context) (EgressLookup, error)
}

// Egress lookup uses fixed endpoints per user request:
//   - domestic perspective → https://ipinfo.io/json
//   - global   perspective → http://ip-api.com/json
var egressProviders = []egressProvider{
	{name: "ipinfo.io", scope: "domestic", fetch: fetchIPInfo},
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

// --- ipinfo.io (domestic) ---

type ipinfoResponse struct {
	IP       string `json:"ip"`
	City     string `json:"city"`
	Region   string `json:"region"`
	Country  string `json:"country"` // ISO-2 like "CN"
	Org      string `json:"org"`     // "AS9808 China Mobile ..."
	Hostname string `json:"hostname"`
}

func fetchIPInfo(ctx context.Context) (EgressLookup, error) {
	var p ipinfoResponse
	if err := httpGetJSON(ctx, "https://ipinfo.io/json", &p); err != nil {
		return EgressLookup{}, err
	}
	ip := strings.TrimSpace(p.IP)
	if ip == "" {
		return EgressLookup{}, fmt.Errorf("ipinfo.io: empty ip")
	}
	out := EgressLookup{
		IP:      ip,
		Country: p.Country,
		Region:  p.Region,
		City:    p.City,
		ISP:     p.Org,
	}
	// ipinfo.io 的 country 是 ISO-2 (例如 "CN")，转个直观写法
	if strings.EqualFold(p.Country, "CN") {
		out.Country = "中国"
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
