package probe

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
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

var egressProviders = []egressProvider{
	{name: "IPIP.net", scope: "domestic", fetch: fetchIPIP},
	{name: "Global", scope: "global", fetch: fetchGlobalEgress},
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

func httpGetString(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "curl/8.5.0")
	req.Header.Set("Accept", "*/*")
	resp, err := egressHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", err
	}
	return string(body), nil
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

var (
	ipipRE = regexp.MustCompile(`当前\s*IP[:：]\s*([\d.a-fA-F:]+)\s*来自于[:：]?\s*(.+)`)
)

func fetchIPIP(ctx context.Context) (EgressLookup, error) {
	body, err := httpGetString(ctx, "https://myip.ipip.net/")
	if err != nil {
		return EgressLookup{}, err
	}
	body = strings.TrimSpace(body)
	m := ipipRE.FindStringSubmatch(body)
	if m == nil {
		return EgressLookup{}, fmt.Errorf("parse failed: %s", body)
	}
	out := EgressLookup{IP: m[1]}
	parts := strings.Fields(m[2])
	if len(parts) > 0 {
		out.Country = parts[0]
	}
	if len(parts) > 1 {
		out.Region = parts[1]
	}
	if len(parts) > 2 {
		out.City = parts[2]
	}
	if len(parts) > 3 {
		out.ISP = strings.Join(parts[3:], " ")
	}
	return out, nil
}
