package probe

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestICMPPingsCompletesQuickly(t *testing.T) {
	// Even with a /24-sized list of unreachable IPs, icmpPings must finish
	// within a small budget (regression for shared-conn deadline hang).
	ips := make([]string, 0, 64)
	for i := 1; i <= 64; i++ {
		ips = append(ips, net.IPv4(203, 0, 113, byte(i)).String()) // TEST-NET-3, should not reply
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_ = icmpPings(ctx, ips, 150*time.Millisecond)
	elapsed := time.Since(start)
	if elapsed > 4*time.Second {
		t.Fatalf("icmpPings took too long: %v", elapsed)
	}
}

func TestWarmNeighborsWithICMPRespectsContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_ = warmNeighborsWithICMP(ctx, "203.0.113.0/24")
	if time.Since(start) > 3*time.Second {
		t.Fatalf("warmNeighborsWithICMP exceeded budget: %v", time.Since(start))
	}
}
