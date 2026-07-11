package probe

import (
	"math"
	"time"
)

func clampProgress(progress int) int {
	switch {
	case progress < 0:
		return 0
	case progress > 100:
		return 100
	default:
		return progress
	}
}

func sanitizeSpeedMetric(value float64, min float64, max float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func validSpeedMbps(primary float64, fallback float64) float64 {
	if !math.IsNaN(primary) && !math.IsInf(primary, 0) && primary > 0 {
		return primary
	}
	if !math.IsNaN(fallback) && !math.IsInf(fallback, 0) && fallback > 0 {
		return fallback
	}
	return 0
}

func broadbandTaskTimeout(duration time.Duration) time.Duration {
	if duration <= 0 {
		duration = 15 * time.Second
	}
	timeout := duration*4 + 90*time.Second
	if timeout < 2*time.Minute {
		return 2 * time.Minute
	}
	return timeout
}

func progressRange(elapsed, total time.Duration, width int) int {
	if total <= 0 || width <= 0 {
		return 0
	}
	ratio := float64(elapsed) / float64(total)
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	return int(math.Round(ratio * float64(width)))
}

func elapsedMS(startedAt time.Time) int64 {
	if startedAt.IsZero() {
		return 0
	}
	return int64(time.Since(startedAt) / time.Millisecond)
}
