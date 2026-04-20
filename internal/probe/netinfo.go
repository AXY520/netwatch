package probe

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var nicLabels = map[string]string{
	"enp2s0": "有线网络",
	"wlp4s0": "无线网络",
}

type ipWhoResponse struct {
	IP      string `json:"ip"`
	Country string `json:"country"`
	Region  string `json:"region"`
	City    string `json:"city"`
	ISP     string `json:"isp"`
}

type ipInfoResponse struct {
	IP      string `json:"ip"`
	City    string `json:"city"`
	Region  string `json:"region"`
	Country string `json:"country"`
	Org     string `json:"org"`
}

type ipSBResponse struct {
	IP      string `json:"ip"`
	Country string `json:"country"`
	Region  string `json:"region"`
	City    string `json:"city"`
	ISP     string `json:"isp"`
}

func (s *Service) ProbeNetworkInfo(ctx context.Context) NetworkInfo {
	hostname, _ := os.Hostname()
	hostname = sanitizeHostname(hostname)
	interfaces := collectInterfaces(s.cfg.MonitoredNICs)
	egressIPv4 := fetchPublicIP(ctx, s.cfg.PublicIPv4Endpoint, "tcp4", s.cfg.HTTPTimeout)
	egressIPv6 := fetchPublicIP(ctx, s.cfg.PublicIPv6Endpoint, "tcp6", s.cfg.HTTPTimeout)

	var egressIPv4Region EgressLocation
	var egressIPv6Region EgressLocation
	locationTimeout := s.cfg.HTTPTimeout
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		egressIPv4Region = lookupIPLocation(ctx, egressIPv4, locationTimeout)
	}()
	go func() {
		defer wg.Done()
		egressIPv6Region = lookupIPLocation(ctx, egressIPv6, locationTimeout)
	}()
	wg.Wait()

	info := NetworkInfo{
		GeneratedAt:      localTimestamp(),
		Hostname:         hostname,
		Interfaces:       interfaces,
		DefaultIPv4:      readDefaultIPv4Route(),
		DefaultIPv6:      readDefaultIPv6Route(),
		EgressIPv4:       egressIPv4,
		EgressIPv6:       egressIPv6,
		EgressIPv4Region: egressIPv4Region,
		EgressIPv6Region: egressIPv6Region,
		DetectionNotes: []string{
			"结果以当前容器网络命名空间为准，建议使用 host 网络模式。",
			"界面仅展示 enp2s0 和 wlp4s0 两个目标网卡。",
			"出口地区主要用于判断代理是否启用以及流量分流是否符合预期。",
		},
	}

	return info
}

func sanitizeHostname(hostname string) string {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return ""
	}
	// In containerized production deployments, os.Hostname() often returns
	// the app/container identifier rather than the actual host machine name.
	// Hiding that value is less misleading than showing a fake "host" name.
	if strings.HasPrefix(hostname, "cloud.lazycat.app.") {
		return ""
	}
	return hostname
}

func collectInterfaces(monitored []string) []InterfaceInfo {
	ifaces, err := net.Interfaces()
	if err != nil {
		return placeholderInterfaces(monitored)
	}

	byName := make(map[string]net.Interface, len(ifaces))
	for _, iface := range ifaces {
		byName[iface.Name] = iface
	}

	result := make([]InterfaceInfo, 0, len(monitored))
	for _, name := range monitored {
		iface, exists := byName[name]
		if !exists {
			result = append(result, InterfaceInfo{Name: name, Label: nicLabels[name], Present: false})
			continue
		}

		addrs, _ := iface.Addrs()
		info := InterfaceInfo{
			Name:         iface.Name,
			Label:        nicLabels[iface.Name],
			Present:      true,
			MTU:          iface.MTU,
			HardwareAddr: iface.HardwareAddr.String(),
			Flags:        interfaceFlags(iface.Flags),
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ipNet.IP.To4() != nil {
				info.IPv4 = append(info.IPv4, ipNet.String())
				continue
			}
			if ipNet.IP.To16() != nil {
				info.IPv6 = append(info.IPv6, ipNet.String())
			}
		}
		result = append(result, info)
	}

	return result
}

func placeholderInterfaces(monitored []string) []InterfaceInfo {
	result := make([]InterfaceInfo, 0, len(monitored))
	for _, name := range monitored {
		result = append(result, InterfaceInfo{Name: name, Label: nicLabels[name], Present: false})
	}
	return result
}

func interfaceFlags(flags net.Flags) []string {
	var result []string
	if flags&net.FlagUp != 0 {
		result = append(result, "up")
	}
	if flags&net.FlagBroadcast != 0 {
		result = append(result, "broadcast")
	}
	if flags&net.FlagLoopback != 0 {
		result = append(result, "loopback")
	}
	if flags&net.FlagPointToPoint != 0 {
		result = append(result, "point_to_point")
	}
	if flags&net.FlagMulticast != 0 {
		result = append(result, "multicast")
	}
	return result
}

func readDefaultIPv4Route() DefaultRoute {
	file, err := os.Open("/proc/net/route")
	if err != nil {
		return DefaultRoute{}
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	firstLine := true
	for scanner.Scan() {
		if firstLine {
			firstLine = false
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		return DefaultRoute{Interface: fields[0], Gateway: decodeIPv4Hex(fields[2])}
	}
	return DefaultRoute{}
}

func readDefaultIPv6Route() DefaultRoute {
	file, err := os.Open("/proc/net/ipv6_route")
	if err != nil {
		return DefaultRoute{}
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}
		if fields[0] != strings.Repeat("0", 32) || fields[1] != "00000000" {
			continue
		}
		return DefaultRoute{Interface: fields[len(fields)-1], Gateway: decodeIPv6Hex(fields[4])}
	}
	return DefaultRoute{}
}

func decodeIPv4Hex(input string) string {
	data, err := hex.DecodeString(input)
	if err != nil || len(data) != 4 {
		return ""
	}
	return net.IPv4(data[3], data[2], data[1], data[0]).String()
}

func decodeIPv6Hex(input string) string {
	data, err := hex.DecodeString(input)
	if err != nil || len(data) != 16 {
		return ""
	}
	return net.IP(data).String()
}

func fetchPublicIP(ctx context.Context, endpoint, network string, timeout time.Duration) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ""
	}

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				return (&net.Dialer{Timeout: timeout}).DialContext(ctx, network, addr)
			},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

func lookupIPLocation(ctx context.Context, ip string, timeout time.Duration) EgressLocation {
	if strings.TrimSpace(ip) == "" {
		return EgressLocation{}
	}

	type resultWithIndex struct {
		index int
		item  EgressLocation
	}

	resultChan := make(chan resultWithIndex, 3)
	fetchers := []func() EgressLocation{
		func() EgressLocation { return lookupIPInfo(ctx, ip, timeout) },
		func() EgressLocation { return lookupIPWho(ctx, ip, timeout) },
		func() EgressLocation { return lookupIPSB(ctx, ip, timeout) },
	}

	for i, fetcher := range fetchers {
		go func(idx int, fn func() EgressLocation) {
			item := fn()
			select {
			case resultChan <- resultWithIndex{idx, item}:
			case <-ctx.Done():
			}
		}(i, fetcher)
	}

	merged := EgressLocation{IP: ip}
	received := 0
	complete := false

	for !complete {
		select {
		case <-ctx.Done():
			complete = true
		case r := <-resultChan:
			received++
			if merged.Country != "" && merged.Region != "" && merged.City != "" && merged.ISP != "" {
				continue
			}
			merged.IP = firstNonEmpty(merged.IP, r.item.IP)
			merged.Country = firstNonEmpty(merged.Country, r.item.Country)
			merged.Region = firstNonEmpty(merged.Region, r.item.Region)
			merged.City = firstNonEmpty(merged.City, r.item.City)
			merged.ISP = firstNonEmpty(merged.ISP, r.item.ISP)
			merged.Source = firstNonEmpty(merged.Source, r.item.Source)
			if received >= 3 || (merged.Country != "" && merged.Region != "" && merged.City != "" && merged.ISP != "") {
				complete = true
			}
		}
	}

	return merged
}

func lookupIPInfo(parent context.Context, ip string, timeout time.Duration) EgressLocation {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	endpoint := "https://ipinfo.io/" + url.PathEscape(ip) + "/json"
	body, err := fetchJSON(ctx, endpoint, timeout)
	if err != nil {
		return EgressLocation{IP: ip}
	}

	var payload ipInfoResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return EgressLocation{IP: ip}
	}

	return EgressLocation{
		IP:      firstNonEmpty(payload.IP, ip),
		Country: payload.Country,
		Region:  payload.Region,
		City:    payload.City,
		ISP:     payload.Org,
		Source:  "ipinfo.io",
	}
}

func lookupIPWho(parent context.Context, ip string, timeout time.Duration) EgressLocation {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	endpoint := "https://ipwho.is/" + url.PathEscape(ip)
	body, err := fetchJSON(ctx, endpoint, timeout)
	if err != nil {
		return EgressLocation{IP: ip}
	}

	var payload struct {
		IP         string `json:"ip"`
		Country    string `json:"country"`
		Region     string `json:"region"`
		City       string `json:"city"`
		Connection struct {
			ISP string `json:"isp"`
			Org string `json:"org"`
		} `json:"connection"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return EgressLocation{IP: ip}
	}

	return EgressLocation{
		IP:      firstNonEmpty(payload.IP, ip),
		Country: payload.Country,
		Region:  payload.Region,
		City:    payload.City,
		ISP:     firstNonEmpty(payload.Connection.ISP, payload.Connection.Org),
		Source:  "ipwho.is",
	}
}

func lookupIPSB(parent context.Context, ip string, timeout time.Duration) EgressLocation {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	endpoint := "https://api.ip.sb/geoip/" + url.PathEscape(ip)
	body, err := fetchJSON(ctx, endpoint, timeout)
	if err != nil {
		return EgressLocation{IP: ip}
	}

	var payload ipSBResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return EgressLocation{IP: ip}
	}

	return EgressLocation{
		IP:      firstNonEmpty(payload.IP, ip),
		Country: payload.Country,
		Region:  payload.Region,
		City:    payload.City,
		ISP:     payload.ISP,
		Source:  "api.ip.sb",
	}
}

func fetchJSON(ctx context.Context, endpoint string, timeout time.Duration) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(io.LimitReader(resp.Body, 4096))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func localPublicIPv4s(monitored []string) []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	allowed := make(map[string]struct{}, len(monitored))
	for _, name := range monitored {
		allowed[name] = struct{}{}
	}

	var results []string
	for _, iface := range ifaces {
		if len(allowed) > 0 {
			if _, ok := allowed[iface.Name]; !ok {
				continue
			}
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue
			}
			if isPublicIPv4(ipNet.IP) {
				results = append(results, ipNet.IP.String())
			}
		}
	}
	return uniqueStrings(results)
}

func isPublicIPv4(ip net.IP) bool {
	if ip == nil {
		return false
	}
	privateRanges := []string{
		"0.0.0.0/8",
		"10.0.0.0/8",
		"100.64.0.0/10",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"198.18.0.0/15",
		"127.0.0.0/8",
		"169.254.0.0/16",
	}
	for _, cidr := range privateRanges {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return false
		}
	}
	return true
}
