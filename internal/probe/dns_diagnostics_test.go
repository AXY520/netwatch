package probe

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestQueryDNSServerParsesAnswersAndDNSSECSignal(t *testing.T) {
	server := startTestDNSServer(t)
	result := queryDNSServer(context.Background(), server, "example.test.", dns.TypeA)
	if result.Status != "NOERROR" || result.Transport != "udp" || result.Error != "" {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Answers) != 1 || result.Answers[0].Value != "192.0.2.10" || result.Answers[0].TTL != 120 {
		t.Fatalf("answers = %+v", result.Answers)
	}
	if !result.AuthenticatedData || result.DNSSECStatus != "authenticated_data" {
		t.Fatalf("dnssec = authenticated %v status %q", result.AuthenticatedData, result.DNSSECStatus)
	}
}

func TestQueryDNSServerParsesCNAME(t *testing.T) {
	server := startTestDNSServer(t)
	result := queryDNSServer(context.Background(), server, "alias.test.", dns.TypeCNAME)
	if len(result.Answers) != 1 || result.Answers[0].Type != "CNAME" || result.Answers[0].Value != "target.test." {
		t.Fatalf("answers = %+v", result.Answers)
	}
}

func TestNormalizeDNSServer(t *testing.T) {
	cases := map[string]string{
		"223.5.5.5":            "223.5.5.5:53",
		"223.5.5.5:5353":       "223.5.5.5:5353",
		"2001:4860:4860::8888": "[2001:4860:4860::8888]:53",
		"dns.example.com":      "dns.example.com:53",
	}
	for input, want := range cases {
		got, err := normalizeDNSServer(input)
		if err != nil || got != want {
			t.Fatalf("normalize %q = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"https://1.1.1.1", "1.1.1.1/path", "host:"} {
		if _, err := normalizeDNSServer(input); err == nil {
			t.Fatalf("normalize %q should fail", input)
		}
	}
}

func TestCompareDNSResults(t *testing.T) {
	system := DNSQueryResult{Status: "NOERROR", DNSSECStatus: "not_present", Answers: []DNSDiagnosticAnswer{{Type: "A", Value: "192.0.2.1"}}}
	specified := DNSQueryResult{Status: "NXDOMAIN", DNSSECStatus: "authenticated_data", Answers: []DNSDiagnosticAnswer{{Type: "A", Value: "192.0.2.2"}}}
	differences := compareDNSResults(system, specified)
	if len(differences) != 3 || differences[0] != "status" || differences[1] != "dnssec" || differences[2] != "answers" {
		t.Fatalf("differences = %v", differences)
	}
}

func startTestDNSServer(t *testing.T) string {
	t.Helper()
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &dns.Server{
		PacketConn: packetConn,
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, request *dns.Msg) {
			response := new(dns.Msg)
			response.SetReply(request)
			response.AuthenticatedData = true
			if len(request.Question) > 0 && request.Question[0].Qtype == dns.TypeCNAME {
				response.Answer = append(response.Answer, &dns.CNAME{Hdr: dns.RR_Header{Name: request.Question[0].Name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 60}, Target: "target.test."})
			} else if len(request.Question) > 0 {
				response.Answer = append(response.Answer, &dns.A{Hdr: dns.RR_Header{Name: request.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 120}, A: net.ParseIP("192.0.2.10")})
			}
			_ = w.WriteMsg(response)
		}),
	}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.ShutdownContext(ctx)
	})
	return packetConn.LocalAddr().String()
}
