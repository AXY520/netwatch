package probe

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestGetEgressLookupsEmptyPageLoadDoesNotProbe(t *testing.T) {
	service := &Service{cfg: DefaultConfig()}
	service.egressCond = sync.NewCond(&service.egressMu)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := service.GetEgressLookups(ctx)
	if got.GeneratedAt != "" || len(got.Lookups) != 0 {
		t.Fatalf("page load created an egress observation: %+v", got)
	}
}

func TestGetEgressLookupsPageLoadKeepsOldCache(t *testing.T) {
	service := &Service{
		egressCache: EgressLookupResult{
			GeneratedAt: "2026-09-01 12:00:00",
			Lookups:     []EgressLookup{{Provider: "cached", IP: "203.0.113.10"}},
		},
		egressUpdatedAt: time.Now().Add(-24 * time.Hour),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // A regression must not fall through to any outbound lookup.

	got := service.GetEgressLookups(ctx)
	if got.GeneratedAt != service.egressCache.GeneratedAt || len(got.Lookups) != 1 || got.Lookups[0].Provider != "cached" {
		t.Fatalf("page load replaced old cache: %+v", got)
	}
}

func TestParseCipCCResponse(t *testing.T) {
	raw := "IP\t: 183.95.49.153\n地址\t: 中国 湖北 武汉\n运营商\t: 联通\n"
	lu, err := parseCipCCResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if lu.IP != "183.95.49.153" {
		t.Fatalf("ip=%q", lu.IP)
	}
	if lu.Country != "中国" || lu.Region != "湖北" || lu.City != "武汉" {
		t.Fatalf("geo=%q/%q/%q", lu.Country, lu.Region, lu.City)
	}
	if lu.ISP != "联通" {
		t.Fatalf("isp=%q", lu.ISP)
	}
}

func TestPickInternationalEgressMajority(t *testing.T) {
	// Three mainland, one flaky overseas → pick mainland majority IP.
	cands := []EgressLookup{
		{Provider: "a", IP: "1.1.1.1", Country: "China"},
		{Provider: "b", IP: "1.1.1.1", Country: "China"},
		{Provider: "c", IP: "1.1.1.1", Country: "CN"},
		{Provider: "d", IP: "9.9.9.9", Country: "Netherlands"},
	}
	got := pickInternationalEgress(cands)
	if got.IP != "1.1.1.1" {
		t.Fatalf("want majority 1.1.1.1, got %+v", got)
	}
}

func TestPickInternationalEgressOverseasAgreement(t *testing.T) {
	cands := []EgressLookup{
		{Provider: "a", IP: "8.8.8.8", Country: "United States"},
		{Provider: "b", IP: "8.8.8.8", Country: "US"},
		{Provider: "c", IP: "1.2.3.4", Country: "China"},
	}
	got := pickInternationalEgress(cands)
	if got.IP != "8.8.8.8" {
		t.Fatalf("want overseas majority, got %+v", got)
	}
}

func TestLookupEgressProvidersSuccessStopsImmediately(t *testing.T) {
	calls := 0
	providers := []egressProvider{
		{name: "primary", scope: "global", fetch: func(context.Context) (EgressLookup, error) {
			calls++
			return EgressLookup{IP: "203.0.113.10"}, nil
		}},
		{name: "backup", scope: "global", fetch: func(context.Context) (EgressLookup, error) {
			calls++
			return EgressLookup{IP: "203.0.113.11"}, nil
		}},
	}
	got := lookupEgressWithProviders(context.Background(), providers)
	if calls != 1 || len(got.Lookups) != 1 || got.Lookups[0].Provider != "primary" {
		t.Fatalf("calls=%d result=%+v", calls, got)
	}
}

func TestLookupEgressProvidersUsesOnlyOneFallback(t *testing.T) {
	calls := 0
	providers := []egressProvider{
		{name: "primary", scope: "global", fetch: func(context.Context) (EgressLookup, error) {
			calls++
			return EgressLookup{}, errors.New("primary failed")
		}},
		{name: "backup", scope: "global", fetch: func(context.Context) (EgressLookup, error) {
			calls++
			return EgressLookup{}, errors.New("backup failed")
		}},
		{name: "forbidden", scope: "global", fetch: func(context.Context) (EgressLookup, error) {
			calls++
			return EgressLookup{IP: "203.0.113.12"}, nil
		}},
	}
	got := lookupEgressWithProviders(context.Background(), providers)
	if calls != 2 {
		t.Fatalf("calls=%d, want exactly primary + one fallback", calls)
	}
	if len(got.Lookups) != 1 || got.Lookups[0].Provider != "primary" || got.Lookups[0].Error == "" {
		t.Fatalf("unexpected failure result: %+v", got)
	}
}
