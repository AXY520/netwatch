package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"path"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"netwatch/internal/api"
	"netwatch/internal/logger"
	"netwatch/internal/probe"
)

func main() {
	logger.Init()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := probe.DefaultConfig()
	if configPath := os.Getenv("CONFIG"); configPath != "" {
		fileCfg, err := probe.LoadConfig(configPath)
		if err != nil {
			logger.Warn("failed to load config: %v", err)
		} else if err := fileCfg.Apply(&cfg); err != nil {
			logger.Warn("failed to apply config: %v", err)
		}
	}
	if err := cfg.Validate(); err != nil {
		logger.Error("config validation failed: %v", err)
		os.Exit(1)
	}
	addr := cfg.Port
	if !strings.HasPrefix(addr, ":") {
		addr = ":" + addr
	}

	service := probe.NewService(cfg)
	if webhook := os.Getenv("ALERT_WEBHOOK_URL"); webhook != "" {
		service.UpdateMutableSettings(probe.MutableSettings{AlertWebhookURL: webhook})
	}
	service.Start(ctx)

	handler := api.NewHandler(service)
	mux := http.NewServeMux()
	handler.Register(mux)

	webRoot := "web"
	if _, err := os.Stat(webRoot); err != nil {
		logger.Error("web directory not found: %v", err)
		os.Exit(1)
	}
	mux.Handle("/", noStoreStatic(http.FileServer(http.Dir(webRoot))))

	server := &http.Server{
		Addr:              addr,
		Handler:           api.BasicAuth(loggingMiddleware(mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       0,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	logger.Info("netwatch listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server error: %v", err)
		os.Exit(1)
	}
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

func noStoreStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ext := strings.ToLower(path.Ext(r.URL.Path))
		switch ext {
		case ".html", ".js", ".css", ".ico", ".json", "":
			w.Header().Set("Cache-Control", "no-store, max-age=0")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		}
		next.ServeHTTP(w, r)
	})
}
