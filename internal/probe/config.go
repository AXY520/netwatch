package probe

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidRefreshInterval = errors.New("invalid refresh interval: must be > 0")
	ErrInvalidHTTPTimeout     = errors.New("invalid http timeout: must be > 0")
	ErrInvalidNATTimeout      = errors.New("invalid nat timeout: must be > 0")
	ErrInvalidDataDir         = errors.New("invalid data dir")
)

type FileConfig struct {
	Port                  string `json:"port"`
	RefreshIntervalSec    int    `json:"refresh_interval_sec"`
	HTTPTimeoutSec        int    `json:"http_timeout_sec"`
	NATTimeoutSec         int    `json:"nat_timeout_sec"`
	PublicIPv4Endpoint    string `json:"public_ipv4_endpoint"`
	PublicIPv6Endpoint    string `json:"public_ipv6_endpoint"`
	DataDir               string `json:"data_dir"`
	BroadbandTestSec      int    `json:"broadband_test_sec"`
	BroadbandDomesticOnly *bool  `json:"broadband_domestic_only"`
	BroadbandStreams      int    `json:"broadband_streams"`
	LocalTransferTestSec  int    `json:"local_transfer_test_sec"`

	LocalTransferPayloadMB int    `json:"local_transfer_payload_mb"`
}

func LoadConfig(path string) (FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, err
	}
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

func (f FileConfig) Apply(cfg *Config) error {
	if f.Port != "" {
		if err := validatePort(f.Port); err != nil {
			return err
		}
		cfg.Port = f.Port
	}
	if f.RefreshIntervalSec > 0 {
		cfg.RefreshInterval = time.Duration(f.RefreshIntervalSec) * time.Second
	}
	if f.HTTPTimeoutSec > 0 {
		cfg.HTTPTimeout = time.Duration(f.HTTPTimeoutSec) * time.Second
	}
	if f.NATTimeoutSec > 0 {
		cfg.NATTimeout = time.Duration(f.NATTimeoutSec) * time.Second
	}
	if f.PublicIPv4Endpoint != "" {
		cfg.PublicIPv4Endpoint = f.PublicIPv4Endpoint
	}
	if f.PublicIPv6Endpoint != "" {
		cfg.PublicIPv6Endpoint = f.PublicIPv6Endpoint
	}
	if f.DataDir != "" {
		cfg.DataDir = f.DataDir
	}
	if f.BroadbandTestSec > 0 {
		cfg.BroadbandDuration = time.Duration(f.BroadbandTestSec) * time.Second
	}
	if f.BroadbandDomesticOnly != nil {
		cfg.BroadbandDomesticOnly = *f.BroadbandDomesticOnly
	}
	if f.BroadbandStreams > 0 {
		cfg.BroadbandStreams = f.BroadbandStreams
	}
	if f.LocalTransferTestSec > 0 {
		cfg.LocalTransferDuration = time.Duration(f.LocalTransferTestSec) * time.Second
	}
	if f.LocalTransferPayloadMB > 0 {
		cfg.LocalTransferPayloadMB = f.LocalTransferPayloadMB
	}
	return nil
}

func validatePort(port string) error {
	if port == "" {
		return errors.New("port cannot be empty")
	}
	if port[0] == ':' {
		port = port[1:]
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("invalid port: %w", err)
	}
	if p < 1 || p > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	return nil
}

type Config struct {
	Port                   string
	DomesticSites          []SiteTarget
	GlobalSites            []SiteTarget
	STUNServers            []string
	HTTPTimeout            time.Duration
	NATTimeout             time.Duration
	RefreshInterval        time.Duration
	PublicIPv4Endpoint     string
	PublicIPv6Endpoint     string
	DataDir                string
	BroadbandDuration      time.Duration
	BroadbandDomesticOnly  bool
	BroadbandStreams       int
	LocalTransferDuration  time.Duration
	LocalTransferPayloadMB int
}

func DefaultConfig() Config {
	return Config{
		Port: envOrDefault("PORT", "8080"),
		DomesticSites: envTargets("DOMESTIC_SITES", []SiteTarget{
			{Name: "Baidu", URL: "https://www.baidu.com"},
			{Name: "Bilibili", URL: "https://www.bilibili.com"},
		}),
		GlobalSites: envTargets("GLOBAL_SITES", []SiteTarget{
			{Name: "GitHub", URL: "https://github.com"},
			{Name: "YouTube", URL: "https://www.youtube.com"},
		}),
		STUNServers: envCSV("STUN_SERVERS", []string{
			"stun.chat.bilibili.com:3478",
			"stun.miwifi.com:3478",
			"stun.l.google.com:19302",
		}),
		HTTPTimeout:            envDurationValue("HTTP_TIMEOUT_SEC", 6*time.Second),
		NATTimeout:             envDurationValue("NAT_TIMEOUT_SEC", 1500*time.Millisecond),
		RefreshInterval:        envDurationValue("REFRESH_INTERVAL_SEC", 10*time.Second),
		PublicIPv4Endpoint:     envOrDefault("PUBLIC_IPV4_ENDPOINT", "https://api.ipify.org"),
		PublicIPv6Endpoint:     envOrDefault("PUBLIC_IPV6_ENDPOINT", "https://api64.ipify.org"),
		DataDir:                envOrDefault("DATA_DIR", "/app/data"),
		BroadbandDuration:      envDurationValue("BROADBAND_TEST_SEC", 15*time.Second),
		BroadbandDomesticOnly:  envBool("BROADBAND_DOMESTIC_ONLY", true),
		BroadbandStreams:       envInt("BROADBAND_STREAMS", 8),
		LocalTransferDuration:  envDurationValue("LOCAL_TRANSFER_TEST_SEC", 10*time.Second),
		LocalTransferPayloadMB: envInt("LOCAL_TRANSFER_PAYLOAD_MB", 32),
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	return value == "true" || value == "1" || value == "yes" || value == "on"
}

func envDurationValue(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	if strings.Contains(value, ".") {
		seconds, err := strconv.ParseFloat(value, 64)
		if err != nil || seconds <= 0 {
			return fallback
		}
		return time.Duration(seconds * float64(time.Second))
	}

	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func envCSV(key string, fallback []string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			items = append(items, part)
		}
	}
	if len(items) == 0 {
		return fallback
	}
	return items
}

func envTargets(key string, fallback []SiteTarget) []SiteTarget {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	rawTargets := strings.Split(value, ",")
	targets := make([]SiteTarget, 0, len(rawTargets))
	for _, raw := range rawTargets {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}

		parts := strings.SplitN(raw, "|", 2)
		if len(parts) == 1 {
			targets = append(targets, SiteTarget{Name: parts[0], URL: parts[0]})
			continue
		}
		targets = append(targets, SiteTarget{Name: strings.TrimSpace(parts[0]), URL: strings.TrimSpace(parts[1])})
	}

	if len(targets) == 0 {
		return fallback
	}
	return targets
}

func (c *Config) Validate() error {
	if err := validatePort(c.Port); err != nil {
		return err
	}
	if c.RefreshInterval <= 0 {
		return ErrInvalidRefreshInterval
	}
	if c.HTTPTimeout <= 0 {
		return ErrInvalidHTTPTimeout
	}
	if c.NATTimeout <= 0 {
		return ErrInvalidNATTimeout
	}
	if strings.TrimSpace(c.DataDir) == "" {
		return ErrInvalidDataDir
	}
	return nil
}
