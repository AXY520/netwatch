package probe

import "testing"

func TestValidSpeedMbpsFallsBackWhenPrimaryInvalid(t *testing.T) {
	tests := []struct {
		name     string
		primary  float64
		fallback float64
		want     float64
	}{
		{name: "primary zero", primary: 0, fallback: 321.5, want: 321.5},
		{name: "primary negative", primary: -1, fallback: 123.4, want: 123.4},
		{name: "primary valid", primary: 456.7, fallback: 123.4, want: 456.7},
		{name: "both invalid", primary: 0, fallback: 0, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validSpeedMbps(tt.primary, tt.fallback); got != tt.want {
				t.Fatalf("validSpeedMbps(%v, %v) = %v, want %v", tt.primary, tt.fallback, got, tt.want)
			}
		})
	}
}
