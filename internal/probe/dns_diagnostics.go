package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"netwatch/internal/lzcsdk"
)

type DNSDiagnosticRequest struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Server string `json:"server,omitempty"`
}

type DNSDiagnosticAnswer struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	TTL   uint32 `json:"ttl"`
}

type DNSQueryResult struct {
	Server            string                `json:"server"`
	Transport         string                `json:"transport"`
	Status            string                `json:"status"`
	DurationMS        int64                 `json:"duration_ms"`
	Answers           []DNSDiagnosticAnswer `json:"answers"`
	AuthenticatedData bool                  `json:"authenticated_data"`
	DNSSECStatus      string                `json:"dnssec_status"`
	ErrorCode         string                `json:"error_code,omitempty"`
	Error             string                `json:"error,omitempty"`
}

type DNSDiagnosticResult struct {
	GeneratedAt     string                `json:"generated_at"`
	Name            string                `json:"name"`
	Type            string                `json:"type"`
	ResolverInfo    SystemDNSResolverInfo `json:"resolver_info"`
	System          DNSQueryResult        `json:"system"`
	SystemResolvers []DNSQueryResult      `json:"system_resolvers"`
	Specified       *DNSQueryResult       `json:"specified,omitempty"`
	Differences     []string              `json:"differences"`
	ConclusionCode  string                `json:"conclusion_code"`
}

type SystemDNSResolverInfo struct {
	GeneratedAt string   `json:"generated_at"`
	Source      string   `json:"source"`
	Device      string   `json:"device,omitempty"`
	Connection  string   `json:"connection,omitempty"`
	Servers     []string `json:"servers"`
	Fallback    bool     `json:"fallback"`
	Note        string   `json:"note,omitempty"`
}

func GetSystemDNSResolverInfo(ctx context.Context) (SystemDNSResolverInfo, error) {
	info := SystemDNSResolverInfo{GeneratedAt: localTimestamp(), Servers: []string{}}
	if nmcliTransportAvailable() {
		candidates, err := listHostDNSCandidates(ctx)
		if err == nil {
			if target, ok := pickHostDNSTarget(candidates, ""); ok {
				servers := splitDNSServers(target.DNS)
				if runtime, runtimeErr := readNetworkDeviceRuntimeConfig(ctx, target.Device); runtimeErr == nil {
					if runtimeServers := splitDNSServers(runtime.DNS); len(runtimeServers) > 0 {
						servers = runtimeServers
					}
				}
				if len(servers) > 0 {
					info.Source = "networkmanager"
					if lzcsdk.Available() {
						info.Source = "lazycat_sdk"
					}
					info.Device = target.Device
					info.Connection = target.Connection
					info.Servers = servers
					return info, nil
				}
			}
		}
	}

	server, err := containerDNSServer()
	if err != nil {
		return info, err
	}
	info.Source = "container_resolv_conf"
	info.Servers = []string{server}
	info.Fallback = true
	info.Note = "无法读取宿主真实网卡 DNS，当前使用容器 resolver"
	return info, nil
}

func RunDNSDiagnostic(ctx context.Context, request DNSDiagnosticRequest) (DNSDiagnosticResult, error) {
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return DNSDiagnosticResult{}, errors.New("name required")
	}
	queryType := strings.ToUpper(strings.TrimSpace(request.Type))
	if queryType == "" {
		queryType = "A"
	}
	qtype, ok := map[string]uint16{
		"A": dns.TypeA, "AAAA": dns.TypeAAAA, "CNAME": dns.TypeCNAME,
		"MX": dns.TypeMX, "TXT": dns.TypeTXT, "NS": dns.TypeNS,
		"SOA": dns.TypeSOA, "PTR": dns.TypePTR,
	}[queryType]
	if !ok {
		return DNSDiagnosticResult{}, errors.New("type must be A, AAAA, CNAME, MX, TXT, NS, SOA, or PTR")
	}
	if queryType == "PTR" && net.ParseIP(name) != nil {
		reverse, err := dns.ReverseAddr(name)
		if err != nil {
			return DNSDiagnosticResult{}, errors.New("invalid IP address")
		}
		name = reverse
	} else if _, ok := dns.IsDomainName(name); !ok {
		return DNSDiagnosticResult{}, errors.New("invalid domain name")
	}
	name = dns.Fqdn(name)
	resolverInfo, err := GetSystemDNSResolverInfo(ctx)
	if err != nil {
		return DNSDiagnosticResult{}, err
	}
	systemResults := queryDNSResolvers(ctx, resolverInfo.Servers, name, qtype)
	if len(systemResults) == 0 {
		return DNSDiagnosticResult{}, errors.New("system DNS server unavailable")
	}
	result := DNSDiagnosticResult{
		GeneratedAt:     localTimestamp(),
		Name:            name,
		Type:            queryType,
		ResolverInfo:    resolverInfo,
		System:          systemResults[0],
		SystemResolvers: systemResults,
		Differences:     []string{},
	}
	if strings.TrimSpace(request.Server) != "" {
		specified, err := normalizeDNSServer(request.Server)
		if err != nil {
			return DNSDiagnosticResult{}, err
		}
		query := queryDNSServer(ctx, specified, name, qtype)
		result.Specified = &query
		result.Differences = compareDNSResults(result.System, query)
	}
	result.ConclusionCode = dnsConclusion(result.SystemResolvers, result.Specified, result.Differences)
	return result, nil
}

func containerDNSServer() (string, error) {
	config, err := dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil || len(config.Servers) == 0 {
		return "", errors.New("system DNS server unavailable")
	}
	port := config.Port
	if port == "" {
		port = "53"
	}
	return net.JoinHostPort(config.Servers[0], port), nil
}

func splitDNSServers(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
	seen := make(map[string]struct{})
	servers := make([]string, 0, 3)
	for _, field := range fields {
		server, err := normalizeDNSServer(field)
		if err != nil {
			continue
		}
		if _, ok := seen[server]; ok {
			continue
		}
		seen[server] = struct{}{}
		servers = append(servers, server)
		if len(servers) == 3 {
			break
		}
	}
	return servers
}

func queryDNSResolvers(ctx context.Context, servers []string, name string, qtype uint16) []DNSQueryResult {
	results := make([]DNSQueryResult, len(servers))
	var wg sync.WaitGroup
	for index, server := range servers {
		wg.Add(1)
		go func(index int, server string) {
			defer wg.Done()
			results[index] = queryDNSServer(ctx, server, name, qtype)
		}(index, server)
	}
	wg.Wait()
	return results
}

func normalizeDNSServer(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "://") || strings.ContainsAny(value, "/?#") {
		return "", errors.New("invalid DNS server")
	}
	if host, port, err := net.SplitHostPort(value); err == nil {
		if strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
			return "", errors.New("invalid DNS server")
		}
		return net.JoinHostPort(host, port), nil
	}
	if ip := net.ParseIP(strings.Trim(value, "[]")); ip != nil {
		return net.JoinHostPort(ip.String(), "53"), nil
	}
	if strings.Contains(value, ":") {
		return "", errors.New("invalid DNS server")
	}
	return net.JoinHostPort(value, "53"), nil
}

func queryDNSServer(ctx context.Context, server, name string, qtype uint16) DNSQueryResult {
	result := DNSQueryResult{Server: server, Transport: "udp", Answers: []DNSDiagnosticAnswer{}, DNSSECStatus: "not_present"}
	message := new(dns.Msg)
	message.SetQuestion(name, qtype)
	message.SetEdns0(1232, true)
	client := &dns.Client{Net: "udp", Timeout: 5 * time.Second}
	started := time.Now()
	response, _, err := client.ExchangeContext(ctx, message, server)
	if err == nil && response != nil && response.Truncated {
		client.Net = "tcp"
		result.Transport = "tcp"
		response, _, err = client.ExchangeContext(ctx, message, server)
	}
	result.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		result.ErrorCode, result.Error = classifyDNSError(err)
		result.Status = "ERROR"
		return result
	}
	if response == nil {
		result.Status = "ERROR"
		result.ErrorCode = "invalid_response"
		result.Error = "empty DNS response"
		return result
	}
	result.Status = dns.RcodeToString[response.Rcode]
	result.AuthenticatedData = response.AuthenticatedData
	hasSignature := false
	for _, record := range append(append([]dns.RR{}, response.Answer...), append(response.Ns, response.Extra...)...) {
		if record.Header().Rrtype == dns.TypeRRSIG {
			hasSignature = true
		}
		if answer, ok := dnsAnswer(record); ok {
			result.Answers = append(result.Answers, answer)
		}
	}
	if response.AuthenticatedData {
		result.DNSSECStatus = "authenticated_data"
	} else if hasSignature {
		result.DNSSECStatus = "signatures_present_unvalidated"
	}
	return result
}

func dnsAnswer(record dns.RR) (DNSDiagnosticAnswer, bool) {
	header := record.Header()
	answer := DNSDiagnosticAnswer{Name: header.Name, Type: dns.TypeToString[header.Rrtype], TTL: header.Ttl}
	switch value := record.(type) {
	case *dns.A:
		answer.Value = value.A.String()
	case *dns.AAAA:
		answer.Value = value.AAAA.String()
	case *dns.CNAME:
		answer.Value = value.Target
	case *dns.MX:
		answer.Value = fmt.Sprintf("%d %s", value.Preference, value.Mx)
	case *dns.TXT:
		answer.Value = strings.Join(value.Txt, "")
	case *dns.NS:
		answer.Value = value.Ns
	case *dns.SOA:
		answer.Value = fmt.Sprintf("%s %s serial=%d refresh=%d retry=%d expire=%d minttl=%d", value.Ns, value.Mbox, value.Serial, value.Refresh, value.Retry, value.Expire, value.Minttl)
	case *dns.PTR:
		answer.Value = value.Ptr
	default:
		return DNSDiagnosticAnswer{}, false
	}
	return answer, true
}

func dnsConclusion(system []DNSQueryResult, specified *DNSQueryResult, differences []string) string {
	successes := 0
	hasAnswers := false
	for _, result := range system {
		if result.Status == "NOERROR" && result.Error == "" {
			successes++
			if len(result.Answers) > 0 {
				hasAnswers = true
			}
		}
	}
	if successes == 0 {
		if specified != nil && specified.Status == "NOERROR" && specified.Error == "" {
			return "specified_only_ok"
		}
		return "system_failed"
	}
	if successes < len(system) {
		return "system_partial"
	}
	if specified != nil && len(differences) > 0 {
		return "responses_differ"
	}
	if !hasAnswers {
		return "no_answers"
	}
	return "system_ok"
}

func classifyDNSError(err error) (string, string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", err.Error()
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout", err.Error()
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "network is unreachable"), strings.Contains(message, "no route to host"):
		return "network_unreachable", err.Error()
	case strings.Contains(message, "connection refused"):
		return "connection_refused", err.Error()
	default:
		return "query_failed", err.Error()
	}
}

func compareDNSResults(system, specified DNSQueryResult) []string {
	differences := make([]string, 0, 3)
	if system.Status != specified.Status {
		differences = append(differences, "status")
	}
	if system.DNSSECStatus != specified.DNSSECStatus {
		differences = append(differences, "dnssec")
	}
	values := func(result DNSQueryResult) []string {
		out := make([]string, 0, len(result.Answers))
		for _, answer := range result.Answers {
			out = append(out, answer.Type+":"+answer.Value)
		}
		sort.Strings(out)
		return out
	}
	if strings.Join(values(system), "\n") != strings.Join(values(specified), "\n") {
		differences = append(differences, "answers")
	}
	return differences
}
