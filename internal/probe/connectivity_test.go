package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
