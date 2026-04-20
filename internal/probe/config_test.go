package probe

import (
	"os"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.RefreshInterval <= 0 {
		t.Error("expected refresh interval > 0")
	}
	if cfg.HTTPTimeout <= 0 {
		t.Error("expected http timeout > 0")
	}
	if len(cfg.DomesticSites) == 0 {
		t.Error("expected domestic sites")
	}
	if len(cfg.GlobalSites) == 0 {
		t.Error("expected global sites")
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg    Config
		wantErr error
	}{
		{
			name: "valid config",
			cfg: Config{
				Port:            "8080",
				RefreshInterval: 10 * time.Second,
				HTTPTimeout:     5 * time.Second,
				NATTimeout:     2 * time.Second,
				DataDir:        "/tmp",
			},
			wantErr: nil,
		},
		{
			name: "invalid refresh interval",
			cfg: Config{
				Port:            "8080",
				RefreshInterval: 0,
				HTTPTimeout:     5 * time.Second,
				NATTimeout:     2 * time.Second,
				DataDir:        "/tmp",
			},
			wantErr: ErrInvalidRefreshInterval,
		},
		{
			name: "invalid data dir",
			cfg: Config{
				Port:            "8080",
				RefreshInterval: 10 * time.Second,
				HTTPTimeout:   5 * time.Second,
				NATTimeout:     2 * time.Second,
				DataDir:        "",
			},
			wantErr: ErrInvalidDataDir,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if err != tt.wantErr {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestEnvInt(t *testing.T) {
	os.Setenv("TEST_INT", "42")
	defer os.Unsetenv("TEST_INT")

	if got := envInt("TEST_INT", 10); got != 42 {
		t.Errorf("got %d, want 42", got)
	}

	if got := envInt("NOT_EXIST", 10); got != 10 {
		t.Errorf("got %d, want fallback 10", got)
	}

	os.Setenv("TEST_INT", "invalid")
	if got := envInt("TEST_INT", 10); got != 10 {
		t.Errorf("got %d, want fallback 10", got)
	}
}

func TestEnvDurationValue(t *testing.T) {
	os.Setenv("TEST_DURATION", "5")
	defer os.Unsetenv("TEST_DURATION")

	if got := envDurationValue("TEST_DURATION", time.Second); got != 5*time.Second {
		t.Errorf("got %v, want 5s", got)
	}

	os.Setenv("TEST_DURATION", "2.5")
	if got := envDurationValue("TEST_DURATION", time.Second); got != 2500*time.Millisecond {
		t.Errorf("got %v, want 2.5s", got)
	}

	if got := envDurationValue("NOT_EXIST", 10*time.Second); got != 10*time.Second {
		t.Errorf("got %v, want 10s", got)
	}
}

func TestEnvCSV(t *testing.T) {
	os.Setenv("TEST_CSV", "a,b,c")
	defer os.Unsetenv("TEST_CSV")

	got := envCSV("TEST_CSV", nil)
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("got %v", got)
	}

	if got := envCSV("NOT_EXIST", []string{"x"}); len(got) != 1 || got[0] != "x" {
		t.Errorf("got %v", got)
	}
}

func TestEnvOrDefault(t *testing.T) {
	os.Setenv("TEST_VAL", "test_value")
	defer os.Unsetenv("TEST_VAL")

	if got := envOrDefault("TEST_VAL", "fallback"); got != "test_value" {
		t.Errorf("got %s, want test_value", got)
	}

	if got := envOrDefault("NOT_EXIST", "fallback"); got != "fallback" {
		t.Errorf("got %s, want fallback", got)
	}
}

func TestEnvTargets(t *testing.T) {
	os.Setenv("TEST_TARGETS", "GitHub|https://github.com,YouTube|https://youtube.com")
	defer os.Unsetenv("TEST_TARGETS")

	got := envTargets("TEST_TARGETS", nil)
	if len(got) != 2 || got[0].Name != "GitHub" || got[0].URL != "https://github.com" {
		t.Errorf("got %+v", got)
	}

	if got := envTargets("NOT_EXIST", []SiteTarget{{Name: "Default", URL: "http://default.com"}}); len(got) != 1 {
		t.Errorf("got %v", got)
	}
}
