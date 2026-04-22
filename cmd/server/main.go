package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"path"
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
		Handler:           api.BasicAuth(accessLogMiddleware(mux)),
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

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		elapsed := time.Since(start)
		path := r.URL.Path

		// 高频健康检查和实时网卡轮询默认静默，避免生产日志被刷屏。
		if path == "/healthz" || path == "/api/v1/network/realtime" {
			if rec.status >= http.StatusBadRequest {
				logger.Warn("%s %s -> %d (%s)", r.Method, path, rec.status, elapsed.Round(time.Millisecond))
			}
			return
		}

		// 普通请求只记录异常或慢请求；测速/诊断类接口保留完成日志。
		if rec.status >= http.StatusInternalServerError {
			logger.Error("%s %s -> %d (%s)", r.Method, path, rec.status, elapsed.Round(time.Millisecond))
			return
		}
		if rec.status >= http.StatusBadRequest {
			logger.Warn("%s %s -> %d (%s)", r.Method, path, rec.status, elapsed.Round(time.Millisecond))
			return
		}
		if elapsed >= 2*time.Second || strings.HasPrefix(path, "/api/v1/speed/") || strings.HasPrefix(path, "/api/v1/diagnostics/trace") {
			logger.Info("%s %s -> %d (%s)", r.Method, path, rec.status, elapsed.Round(time.Millisecond))
		}
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
