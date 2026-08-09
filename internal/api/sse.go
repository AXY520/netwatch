package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"netwatch/internal/logger"
)

type basicAuthCreds struct {
	user string
	pass string
}

func loadBasicAuth() *basicAuthCreds {
	user := os.Getenv("BASIC_AUTH_USER")
	pass := os.Getenv("BASIC_AUTH_PASSWORD")
	if user == "" || pass == "" {
		return nil
	}
	return &basicAuthCreds{user: user, pass: pass}
}

func BasicAuth(next http.Handler) http.Handler {
	creds := loadBasicAuth()
	if creds == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != creds.user || subtle.ConstantTimeCompare([]byte(p), []byte(creds.pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="netwatch"`)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprintln(w, "Unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		// Fallback: return current summary as a single JSON response so the
		// client can fall back to polling instead of getting a 500 error.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		summary := h.service.GetSummary()
		body, err := marshalObservationJSON(summary, summary.GeneratedAt, observationStaleAfter(summary.RefreshIntervalSec))
		if err == nil {
			_, _ = w.Write(append(body, '\n'))
		} else {
			_ = json.NewEncoder(w).Encode(summary)
		}
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := h.service.Subscribe()
	defer unsub()
	notifyCh, notifyUnsub := h.service.SubscribeNotifications()
	defer notifyUnsub()
	nicCh, nicUnsub := h.service.SubscribeNICRealtime()
	defer nicUnsub()
	lanCh, lanUnsub := h.service.SubscribeLANDevices()
	defer lanUnsub()

	writeEvent := func(name string, data []byte) bool {
		_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
		if err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	initialSummary := h.service.GetSummary()
	if initial, err := marshalObservationJSON(initialSummary, initialSummary.GeneratedAt, observationStaleAfter(initialSummary.RefreshIntervalSec)); err == nil {
		if !writeEvent("summary", initial) {
			return
		}
	}
	if since, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64); err == nil && since >= 0 {
		for _, ev := range h.service.GetNotificationEvents(since) {
			body, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if !writeEvent("notification", body) {
				return
			}
		}
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case summary, ok := <-ch:
			if !ok {
				return
			}
			body, err := marshalObservationJSON(summary, summary.GeneratedAt, observationStaleAfter(summary.RefreshIntervalSec))
			if err != nil {
				logger.Error("sse summary marshal: %v", err)
				continue
			}
			if !writeEvent("summary", body) {
				return
			}
		case ev, ok := <-notifyCh:
			if !ok {
				return
			}
			body, err := json.Marshal(ev)
			if err != nil {
				logger.Error("sse notification marshal: %v", err)
				continue
			}
			if !writeEvent("notification", body) {
				return
			}
		case snap, ok := <-nicCh:
			if !ok {
				return
			}
			body, err := marshalObservationJSON(snap, snap.Timestamp, 15*time.Second)
			if err != nil {
				logger.Error("sse nic_realtime marshal: %v", err)
				continue
			}
			if !writeEvent("nic_realtime", body) {
				return
			}
		case devices, ok := <-lanCh:
			if !ok {
				return
			}
			body, err := marshalObservationJSON(devices, devices.GeneratedAt, 10*time.Minute)
			if err != nil {
				logger.Error("sse lan_devices marshal: %v", err)
				continue
			}
			if !writeEvent("lan_devices", body) {
				return
			}
		}
	}
}

// mutate path prefixes that change host/network state and must not be left open on host network.
var mutatePathPrefixes = []string{
	"/api/v1/network/config/apply",
	"/api/v1/network/config/confirm",
	"/api/v1/network/config/rollback",
	"/api/v1/network/bridges/create",
	"/api/v1/network/bridges/confirm",
	"/api/v1/network/bridges/rollback",
	"/api/v1/network/bridges/dissolve",
	"/api/v1/network/config/check-ip",
	"/api/v1/network/dns/apply",
	"/api/v1/network/dns/confirm",
	"/api/v1/network/dns/rollback",
	"/api/v1/containers/block",
	"/api/v1/containers/unblock",
	"/api/v1/network/ipv6/renew",
	"/api/v1/settings",
}

func isMutatePath(path string) bool {
	for _, prefix := range mutatePathPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// MutateAuth optionally enforces credentials for high-risk write endpoints.
// Enable with MUTATE_AUTH_USER + MUTATE_AUTH_PASSWORD, or set MUTATE_AUTH_REQUIRED=1
// to reuse BASIC_AUTH_* credentials (returns 401 if neither is configured).
func MutateAuth(next http.Handler) http.Handler {
	mutateUser := strings.TrimSpace(os.Getenv("MUTATE_AUTH_USER"))
	mutatePass := strings.TrimSpace(os.Getenv("MUTATE_AUTH_PASSWORD"))
	required := strings.EqualFold(strings.TrimSpace(os.Getenv("MUTATE_AUTH_REQUIRED")), "1") ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("MUTATE_AUTH_REQUIRED")), "true")

	var creds *basicAuthCreds
	if mutateUser != "" && mutatePass != "" {
		creds = &basicAuthCreds{user: mutateUser, pass: mutatePass}
	} else if basic := loadBasicAuth(); basic != nil {
		creds = basic
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if !isMutatePath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if creds == nil {
			if required {
				w.Header().Set("WWW-Authenticate", `Basic realm="netwatch-mutate"`)
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = fmt.Fprintln(w, "mutate auth required but not configured")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok || u != creds.user || subtle.ConstantTimeCompare([]byte(p), []byte(creds.pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="netwatch-mutate"`)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprintln(w, "Unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}
