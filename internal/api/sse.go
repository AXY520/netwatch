package api

import (
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
		if !ok || u != creds.user || p != creds.pass {
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
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := h.service.Subscribe()
	defer unsub()

	if initial, err := json.Marshal(h.service.GetSummary()); err == nil {
		fmt.Fprintf(w, "event: summary\ndata: %s\n\n", initial)
		flusher.Flush()
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
			fmt.Fprintf(w, "event: summary\ndata: %s\n\n", body)
			flusher.Flush()
		}
	}
}
