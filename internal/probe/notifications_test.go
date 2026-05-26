package probe

import "testing"

func TestBuildBarkEndpoint(t *testing.T) {
	tests := []struct {
		name   string
		server string
		key    string
		want   string
	}{
		{name: "default server", server: "https://api.day.app", key: "abc123", want: "https://api.day.app/abc123"},
		{name: "server without scheme", server: "api.day.app", key: "abc123", want: "https://api.day.app/abc123"},
		{name: "strip push suffix", server: "https://api.day.app/push", key: "abc123", want: "https://api.day.app/abc123"},
		{name: "sub path", server: "https://push.example.com/bark", key: "abc123", want: "https://push.example.com/bark/abc123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildBarkEndpoint(tt.server, tt.key)
			if err != nil {
				t.Fatalf("buildBarkEndpoint() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("buildBarkEndpoint() = %q, want %q", got, tt.want)
			}
		})
	}
}
