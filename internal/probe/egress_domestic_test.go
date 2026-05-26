package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsDomesticIPv6CandidateAcceptsCNCarrierPrefix(t *testing.T) {
	if !isDomesticIPv6Candidate("2408:8207:1234::1", "") {
		t.Fatal("expected China Unicom IPv6 prefix to be accepted")
	}
}

func TestIsDomesticIPv6CandidateRejectsOverseasIPv6(t *testing.T) {
	if isDomesticIPv6Candidate("2605:52c0:2:b21:be24:11ff:fe7e:c6c5", "美国 California州 Los Angeles DMIT Cloud Services") {
		t.Fatal("expected overseas DMIT IPv6 to be rejected")
	}
}

func TestIsCNIPv6RejectsOverseasIPv6(t *testing.T) {
	if isCNIPv6(mustParseIP(t, "2605:52c0:2:b21:be24:11ff:fe7e:c6c5")) {
		t.Fatal("expected overseas DMIT IPv6 prefix to be rejected")
	}
}

func TestIsMainlandChinaLocationRejectsNonMainlandChina(t *testing.T) {
	cases := []string{
		"中国 香港",
		"China Hong Kong",
		"China Taiwan",
	}
	for _, tc := range cases {
		if isMainlandChinaLocation(tc) {
			t.Fatalf("expected %q to be rejected as non-mainland", tc)
		}
	}
}

func TestIsMainlandChinaLocationAcceptsMainlandChina(t *testing.T) {
	cases := []string{
		"中国 湖北 武汉",
		"China Hubei Wuhan",
	}
	for _, tc := range cases {
		if !isMainlandChinaLocation(tc) {
			t.Fatalf("expected %q to be accepted as mainland China", tc)
		}
	}
}

func TestLookupDomesticIPv4ViaIPIPParsesCurrentResponse(t *testing.T) {
	oldClient := domesticHTTPClient
	defer func() { domesticHTTPClient = oldClient }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ret":"ok","data":{"ip":"183.95.49.36","location":["中国","湖北","武汉","","联通"]}}`))
	}))
	defer server.Close()

	domesticHTTPClient = &http.Client{Transport: rewriteHostTransport{target: server.URL}}

	entry := lookupDomesticIPv4ViaIPIP(context.Background())
	if entry.Error != "" {
		t.Fatalf("unexpected error: %s", entry.Error)
	}
	if entry.IP != "183.95.49.36" {
		t.Fatalf("ip = %q, want 183.95.49.36", entry.IP)
	}
	if entry.Location != "中国 湖北 武汉 联通" {
		t.Fatalf("location = %q", entry.Location)
	}
	if entry.ISP != "联通" {
		t.Fatalf("isp = %q, want 联通", entry.ISP)
	}
}

type rewriteHostTransport struct {
	target string
}

func (t rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	next, err := http.NewRequestWithContext(req.Context(), req.Method, t.target, req.Body)
	if err != nil {
		return nil, err
	}
	next.Header = req.Header.Clone()
	return http.DefaultTransport.RoundTrip(next)
}

func mustParseIP(t *testing.T, raw string) net.IP {
	t.Helper()
	ip := net.ParseIP(raw)
	if ip == nil {
		t.Fatalf("invalid test IP: %s", raw)
	}
	return ip
}
