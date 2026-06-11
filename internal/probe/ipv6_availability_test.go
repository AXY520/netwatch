package probe

import (
	"net"
	"testing"
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
		{"outbound + https but no dns", IPv6Availability{HasGlobalAddress: true, OutboundReachable: true, HTTPSReachable: true}, ipv6SummaryOutboundOnly},
		{"fully usable", IPv6Availability{HasGlobalAddress: true, OutboundReachable: true, HTTPSReachable: true, DNSResolvable: true}, ipv6SummaryFullyUsable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summarizeIPv6(tt.a); got != tt.want {
				t.Fatalf("summarizeIPv6(%+v) = %q, want %q", tt.a, got, tt.want)
			}
		})
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
