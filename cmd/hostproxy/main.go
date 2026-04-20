package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		if err := runHealthcheck(); err != nil {
			log.Printf("healthcheck failed: %v", err)
			os.Exit(1)
		}
		return
	}

	listenPort := envOrDefault("LISTEN_PORT", "23088")
	targetPort := envOrDefault("TARGET_PORT", "23087")
	if _, err := strconv.Atoi(listenPort); err != nil {
		log.Fatalf("invalid LISTEN_PORT: %v", err)
	}
	if _, err := strconv.Atoi(targetPort); err != nil {
		log.Fatalf("invalid TARGET_PORT: %v", err)
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			targetURL, err := backendURL(targetPort)
			if err != nil {
				return
			}
			req.URL.Scheme = targetURL.Scheme
			req.URL.Host = targetURL.Host
			req.Host = targetURL.Host
		},
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          20,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       60 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "upstream unavailable: "+err.Error(), http.StatusBadGateway)
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if err := runHealthcheck(); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", proxy)

	server := &http.Server{
		Addr:              ":" + listenPort,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Printf("host proxy listening on :%s -> target port %s", listenPort, targetPort)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func runHealthcheck() error {
	targetPort := envOrDefault("TARGET_PORT", "23087")
	targetURL, err := backendURL(targetPort)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL.String()+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return errors.New(resp.Status)
	}
	return nil
}

func backendURL(targetPort string) (*url.URL, error) {
	gateway := readDefaultIPv4Gateway()
	if gateway == "" {
		return nil, errors.New("default gateway not found")
	}
	return url.Parse("http://" + net.JoinHostPort(gateway, targetPort))
}

func readDefaultIPv4Gateway() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	firstLine := true
	for scanner.Scan() {
		if firstLine {
			firstLine = false
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		return decodeIPv4Hex(fields[2])
	}
	return ""
}

func decodeIPv4Hex(input string) string {
	data, err := hex.DecodeString(input)
	if err != nil || len(data) != 4 {
		return ""
	}
	return net.IPv4(data[3], data[2], data[1], data[0]).String()
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
