package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
		_ = json.NewEncoder(w).Encode(h.service.GetSummary())
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

	writeEvent := func(name string, data []byte) bool {
		_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
		if err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if initial, err := json.Marshal(h.service.GetSummary()); err == nil {
		if !writeEvent("summary", initial) {
			return
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
			body, err := json.Marshal(summary)
			if err != nil {
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
				continue
			}
			if !writeEvent("notification", body) {
				return
			}
		}
	}
}
