package probe

import (
	"math"
	"time"
)

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
