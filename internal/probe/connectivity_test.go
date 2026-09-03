package probe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestProbeHTTPTargetOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	res := probeHTTPTarget(context.Background(), SiteTarget{Name: "t", URL: srv.URL}, 2*time.Second)
	if res.Status != StatusOK {
		t.Fatalf("status=%s err=%s", res.Status, res.Error)
	}
	if res.LatencyMS <= 0 {
		t.Fatalf("latency=%d", res.LatencyMS)
	}
}

func TestProbeHTTPTargetAnyCodeIsOK(t *testing.T) {
	// zashboard: if the resource loads (or server answers), path is up.
	// 404/500 still prove connectivity.
	for _, code := range []int{204, 301, 404, 500, 503} {
		code := code
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		res := probeHTTPTarget(context.Background(), SiteTarget{Name: "c", URL: srv.URL}, 2*time.Second)
		srv.Close()
		if res.Status != StatusOK {
			t.Fatalf("code %d → status=%s want ok", code, res.Status)
		}
		if res.LatencyMS <= 0 {
			t.Fatalf("code %d → latency=%d", code, res.LatencyMS)
		}
	}
}

func TestProbeHTTPTargetDoesNotDrainHugeBodyIntoLatency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("x", 512*1024)))
	}))
	defer srv.Close()

	start := time.Now()
	res := probeHTTPTarget(context.Background(), SiteTarget{Name: "big", URL: srv.URL}, 3*time.Second)
	if res.Status != StatusOK {
		t.Fatalf("status=%s", res.Status)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("too slow reading body: %v", time.Since(start))
	}
}

func TestProbeHTTPTargetTimeoutIsDownZeroLatency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	res := probeHTTPTarget(context.Background(), SiteTarget{Name: "slow", URL: srv.URL}, 80*time.Millisecond)
	if res.Status != StatusDown {
		t.Fatalf("status=%s want down", res.Status)
	}
	if res.LatencyMS != 0 {
		t.Fatalf("timeout must not report latency_ms=%d", res.LatencyMS)
	}
}

func TestProbeTargetsChecksEveryConfiguredSite(t *testing.T) {
	calls := make([]int, 4)
	targets := make([]SiteTarget, 0, len(calls))
	servers := make([]*httptest.Server, 0, len(calls))
	for index := range calls {
		index := index
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls[index]++
			w.WriteHeader(http.StatusNoContent)
		}))
		servers = append(servers, srv)
		targets = append(targets, SiteTarget{Name: "site-" + string(rune('1'+index)), URL: srv.URL})
	}
	defer func() {
		for _, srv := range servers {
			srv.Close()
		}
	}()

	results := probeTargets(context.Background(), targets, time.Second)
	if len(results) != len(targets) {
		t.Fatalf("results=%d want=%d", len(results), len(targets))
	}
	for index, result := range results {
		if calls[index] != 1 {
			t.Fatalf("site %d calls=%d want=1", index, calls[index])
		}
		if result.Name != targets[index].Name || result.Status != StatusOK {
			t.Fatalf("result %d=%+v want name=%q status=%q", index, result, targets[index].Name, StatusOK)
		}
	}
}

func TestRefreshWebsiteConnectivityKeepsSecondSiteBudgetAfterFirstTimeout(t *testing.T) {
	const timeout = 600 * time.Millisecond
	originalClient := probeClient
	probeClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "slow.test":
			<-req.Context().Done()
			return nil, req.Context().Err()
		case "second.test":
			timer := time.NewTimer(560 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
				return &http.Response{
					StatusCode: http.StatusNoContent,
					Body:       http.NoBody,
					Header:     make(http.Header),
					Request:    req,
				}, nil
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		default:
			return nil, errors.New("unexpected test target")
		}
	})}
	t.Cleanup(func() { probeClient = originalClient })

	service := &Service{cfg: Config{
		HTTPTimeout: timeout,
		DomesticSites: []SiteTarget{
			{Name: "slow", URL: "https://slow.test/ping"},
			{Name: "second", URL: "https://second.test/ping"},
		},
	}}
	result := service.RefreshWebsiteConnectivity(context.Background())
	if len(result.Domestic) != 2 {
		t.Fatalf("domestic results=%d, want 2", len(result.Domestic))
	}
	if got := result.Domestic[1]; got.Status != StatusOK {
		t.Fatalf("second site status=%s error=%q, want ok with its full timeout budget", got.Status, got.Error)
	}
}

func TestWebsiteProbeBudgetAccountsForLongestSequentialLane(t *testing.T) {
	got := websiteProbeBudget(5*time.Second, 2, 1)
	want := 10*time.Second + websiteProbeBatchGrace
	if got != want {
		t.Fatalf("website probe budget=%s, want %s", got, want)
	}
}
