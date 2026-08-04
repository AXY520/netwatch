package probe

import (
	"context"
	"net"
	"reflect"
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

func TestSplitDNSServersNormalizesDeduplicatesAndLimits(t *testing.T) {
	got := splitDNSServers("192.0.2.1, 192.0.2.1 2001:db8::1;198.51.100.2,203.0.113.9")
	want := []string{"192.0.2.1:53", "[2001:db8::1]:53", "198.51.100.2:53"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("servers = %v, want %v", got, want)
	}
}

func TestDNSAnswerParsesExtendedRecordTypes(t *testing.T) {
	header := func(recordType uint16) dns.RR_Header {
		return dns.RR_Header{Name: "example.test.", Rrtype: recordType, Class: dns.ClassINET, Ttl: 60}
	}
	tests := []struct {
		record dns.RR
		want   string
	}{
		{&dns.MX{Hdr: header(dns.TypeMX), Preference: 10, Mx: "mail.example.test."}, "10 mail.example.test."},
		{&dns.TXT{Hdr: header(dns.TypeTXT), Txt: []string{"v=spf1 ", "-all"}}, "v=spf1 -all"},
		{&dns.NS{Hdr: header(dns.TypeNS), Ns: "ns1.example.test."}, "ns1.example.test."},
		{&dns.SOA{Hdr: header(dns.TypeSOA), Ns: "ns1.example.test.", Mbox: "hostmaster.example.test.", Serial: 7, Refresh: 8, Retry: 9, Expire: 10, Minttl: 11}, "ns1.example.test. hostmaster.example.test. serial=7 refresh=8 retry=9 expire=10 minttl=11"},
		{&dns.PTR{Hdr: header(dns.TypePTR), Ptr: "host.example.test."}, "host.example.test."},
	}
	for _, test := range tests {
		answer, ok := dnsAnswer(test.record)
		if !ok || answer.Value != test.want {
			t.Errorf("answer for %T = %+v, %v; want %q", test.record, answer, ok, test.want)
		}
	}
}

func TestDNSConclusion(t *testing.T) {
	ok := DNSQueryResult{Status: "NOERROR", Answers: []DNSDiagnosticAnswer{{Type: "A", Value: "192.0.2.1"}}}
	empty := DNSQueryResult{Status: "NOERROR"}
	failed := DNSQueryResult{Status: "ERROR", Error: "timeout"}
	if got := dnsConclusion([]DNSQueryResult{ok}, nil, nil); got != "system_ok" {
		t.Fatalf("system ok conclusion = %q", got)
	}
	if got := dnsConclusion([]DNSQueryResult{ok, failed}, nil, nil); got != "system_partial" {
		t.Fatalf("partial conclusion = %q", got)
	}
	if got := dnsConclusion([]DNSQueryResult{failed}, &ok, nil); got != "specified_only_ok" {
		t.Fatalf("specified-only conclusion = %q", got)
	}
	if got := dnsConclusion([]DNSQueryResult{empty}, nil, nil); got != "no_answers" {
		t.Fatalf("no-answer conclusion = %q", got)
	}
	if got := dnsConclusion([]DNSQueryResult{ok}, &ok, []string{"answers"}); got != "responses_differ" {
		t.Fatalf("different conclusion = %q", got)
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
