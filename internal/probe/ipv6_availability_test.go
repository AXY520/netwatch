package probe

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestSummarizeIPv6 验证分层结果到结论的综合判断(纯函数)。
func TestSummarizeIPv6(t *testing.T) {
	tests := []struct {
		name string
		a    IPv6Availability
		want string
	}{
		{"no global address", IPv6Availability{HasGlobalAddress: false}, ipv6SummaryNoGlobal},
		{"address but no outbound", IPv6Availability{HasGlobalAddress: true, OutboundReachable: false}, ipv6SummaryAddressOnly},
		{"outbound but no https/dns", IPv6Availability{HasGlobalAddress: true, OutboundReachable: true}, ipv6SummaryOutboundOnly},
		{"outbound + https (dns optional)", IPv6Availability{HasGlobalAddress: true, OutboundReachable: true, HTTPSReachable: true}, ipv6SummaryFullyUsable},
		{"fully usable", IPv6Availability{HasGlobalAddress: true, OutboundReachable: true, HTTPSReachable: true, DNSResolvable: true}, ipv6SummaryFullyUsable},
		{"outbound + dns no https", IPv6Availability{HasGlobalAddress: true, OutboundReachable: true, DNSResolvable: true}, ipv6SummaryOutboundOnly},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summarizeIPv6(tt.a); got != tt.want {
				t.Fatalf("summarizeIPv6(%+v) = %q, want %q", tt.a, got, tt.want)
			}
		})
	}
}

func TestProbeIPv6AvailabilityNoAddressDoesNotProbePublicNetwork(t *testing.T) {
	publicCalls := 0
	got := probeIPv6AvailabilityWithOps(context.Background(), "", ipv6ProbeOps{
		pickAddress: func() string { return "" },
		https: func(context.Context) ipv6HTTPSProbeResult {
			publicCalls++
			return ipv6HTTPSProbeResult{}
		},
		outbound: func(context.Context) ipv6OutboundProbeResult {
			publicCalls++
			return ipv6OutboundProbeResult{}
		},
	})
	if got.Summary != ipv6SummaryNoGlobal || got.HasGlobalAddress {
		t.Fatalf("unexpected result: %+v", got)
	}
	if publicCalls != 0 {
		t.Fatalf("no-address probe made %d public calls, want 0", publicCalls)
	}
}

func TestProbeIPv6AvailabilityHTTPSSuccessStopsAndUsesActualSource(t *testing.T) {
	outboundCalls := 0
	got := probeIPv6AvailabilityWithOps(context.Background(), "2408:8000::10", ipv6ProbeOps{
		https: func(context.Context) ipv6HTTPSProbeResult {
			return ipv6HTTPSProbeResult{
				host: "www.baidu.com", latencyMS: 12,
				localAddress: "2409:8000::20", dnsResolvable: true,
			}
		},
		outbound: func(context.Context) ipv6OutboundProbeResult {
			outboundCalls++
			return ipv6OutboundProbeResult{}
		},
	})
	if got.Summary != ipv6SummaryFullyUsable || !got.HTTPSReachable || !got.OutboundReachable || !got.DNSResolvable {
		t.Fatalf("unexpected result: %+v", got)
	}
	if got.GlobalAddress != "2409:8000::20" {
		t.Fatalf("global address=%q, want actual source", got.GlobalAddress)
	}
	if outboundCalls != 0 {
		t.Fatalf("HTTPS success still made %d outbound fallback calls", outboundCalls)
	}
}

func TestProbeIPv6AvailabilityReusesDNSBeforeOutboundFallback(t *testing.T) {
	got := probeIPv6AvailabilityWithOps(context.Background(), "2408:8000::10", ipv6ProbeOps{
		https: func(context.Context) ipv6HTTPSProbeResult {
			return ipv6HTTPSProbeResult{dnsResolvable: true, httpsError: "TLS failed"}
		},
		outbound: func(context.Context) ipv6OutboundProbeResult {
			return ipv6OutboundProbeResult{
				target: "[2400:3200:baba::1]:443", latencyMS: 8,
				localAddress: "2409:8000::20",
			}
		},
	})
	if got.Summary != ipv6SummaryOutboundOnly || !got.OutboundReachable || !got.DNSResolvable || got.HTTPSReachable {
		t.Fatalf("unexpected result: %+v", got)
	}
	if got.GlobalAddress != "2409:8000::20" {
		t.Fatalf("global address=%q, want actual source", got.GlobalAddress)
	}
}

func TestProbeIPv6HTTPSUsesAtMostOneFallbackAndStopsOnSuccess(t *testing.T) {
	calls := 0
	got := probeIPv6HTTPSWith(context.Background(), []string{"primary", "backup", "forbidden"}, func(_ context.Context, host string) ipv6HTTPSProbeResult {
		calls++
		if host == "backup" {
			return ipv6HTTPSProbeResult{host: host, dnsResolvable: true}
		}
		return ipv6HTTPSProbeResult{dnsResolvable: true, httpsError: "failed"}
	})
	if got.host != "backup" || calls != 2 {
		t.Fatalf("result=%+v calls=%d, want backup success after exactly 2 attempts", got, calls)
	}
}

func TestEgressCacheWithin(t *testing.T) {
	now := time.Now()
	if !egressCacheWithin(now.Add(-time.Minute), now, 2*time.Minute) {
		t.Fatal("one-minute cache should be fresh")
	}
	if egressCacheWithin(now.Add(-3*time.Minute), now, 2*time.Minute) {
		t.Fatal("three-minute cache should be stale")
	}
	if egressCacheWithin(time.Time{}, now, 2*time.Minute) {
		t.Fatal("zero timestamp should not be fresh")
	}
}

// TestIsPublicIPv6 验证公网可路由判定的边界(排除 link-local/ULA/loopback/文档段)。
func TestIsPublicIPv6(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"2408:8000::1", true},         // 公网(运营商段)
		{"2606:4700:4700::1111", true}, // Cloudflare 公网
		{"240e:1::1", true},            // 公网(电信段)
		{"fe80::1", false},             // link-local
		{"::1", false},                 // loopback
		{"fc00::1", false},             // ULA
		{"fd12:3456::1", false},        // ULA
		{"2001:db8::1", false},         // 文档保留段
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			if got := isPublicIPv6(net.ParseIP(tt.ip)); got != tt.want {
				t.Errorf("isPublicIPv6(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}
