package probe

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/miekg/dns"
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
	GeneratedAt string          `json:"generated_at"`
	Name        string          `json:"name"`
	Type        string          `json:"type"`
	System      DNSQueryResult  `json:"system"`
	Specified   *DNSQueryResult `json:"specified,omitempty"`
	Differences []string        `json:"differences"`
}

func RunDNSDiagnostic(ctx context.Context, request DNSDiagnosticRequest) (DNSDiagnosticResult, error) {
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return DNSDiagnosticResult{}, errors.New("name required")
	}
	if _, ok := dns.IsDomainName(name); !ok {
		return DNSDiagnosticResult{}, errors.New("invalid domain name")
	}
	queryType := strings.ToUpper(strings.TrimSpace(request.Type))
	if queryType == "" {
		queryType = "A"
	}
	qtype, ok := map[string]uint16{"A": dns.TypeA, "AAAA": dns.TypeAAAA, "CNAME": dns.TypeCNAME}[queryType]
	if !ok {
		return DNSDiagnosticResult{}, errors.New("type must be A, AAAA, or CNAME")
	}
	systemServer, err := systemDNSServer()
	if err != nil {
		return DNSDiagnosticResult{}, err
	}
	result := DNSDiagnosticResult{
		GeneratedAt: localTimestamp(),
		Name:        dns.Fqdn(name),
		Type:        queryType,
		System:      queryDNSServer(ctx, systemServer, dns.Fqdn(name), qtype),
		Differences: []string{},
	}
	if strings.TrimSpace(request.Server) != "" {
		specified, err := normalizeDNSServer(request.Server)
		if err != nil {
			return DNSDiagnosticResult{}, err
		}
		query := queryDNSServer(ctx, specified, dns.Fqdn(name), qtype)
		result.Specified = &query
		result.Differences = compareDNSResults(result.System, query)
	}
	return result, nil
}

func systemDNSServer() (string, error) {
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
	default:
		return DNSDiagnosticAnswer{}, false
	}
	return answer, true
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
