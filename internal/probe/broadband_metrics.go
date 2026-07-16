package probe

import (
	"context"
	"sort"
	"time"
)

type stepFunc func(stage, status, message string)

type stepCtxKey struct{}

// withStepReporter 把 step 上报器注入 ctx,使深层节点选择函数无需改签名即可上报进度。
func withStepReporter(ctx context.Context, step stepFunc) context.Context {
	if step == nil {
		return ctx
	}
	return context.WithValue(ctx, stepCtxKey{}, step)
}

// emitStep 从 ctx 取出 step 上报器并上报一步;无上报器时静默。
func emitStep(ctx context.Context, stage, status, message string) {
	if f, ok := ctx.Value(stepCtxKey{}).(stepFunc); ok && f != nil {
		f(stage, status, message)
	}
}

const (
	// defaultBroadbandStreams 是并发连接数默认值。库默认 runtime.NumCPU(),
	// 在低核设备上测不满高带宽;显式提到 10 以更好跑满千兆/2.5G。
	defaultBroadbandStreams = 10
	// 预热期(grace)内的采样不计入稳态速度,用于跳过 TCP 慢启动。
	broadbandDownloadGrace = 2 * time.Second
	broadbandUploadGrace   = 3 * time.Second
)

// speedSample 是一次实时速率采样(Mbps)及其时刻。
type speedSample struct {
	at   time.Time
	mbps float64
}

// percentileSorted 返回已排序切片的线性插值百分位。p 取 [0,1]。
func percentileSorted(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	pos := p * float64(len(sorted)-1)
	lo := int(pos)
	hi := lo + 1
	if hi >= len(sorted) {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// medianSorted 返回已排序切片的中位数。
func medianSorted(sorted []float64) float64 {
	return percentileSorted(sorted, 0.5)
}

// trimmedMeanSorted 去掉两端各 trim 比例后取均值,抑制尖峰与偶发低谷。
func trimmedMeanSorted(sorted []float64, trim float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if trim < 0 {
		trim = 0
	}
	if trim > 0.4 {
		trim = 0.4
	}
	drop := int(float64(n) * trim)
	if drop*2 >= n {
		drop = 0
	}
	sum := 0.0
	cnt := 0
	for i := drop; i < n-drop; i++ {
		sum += sorted[i]
		cnt++
	}
	if cnt == 0 {
		return medianSorted(sorted)
	}
	return sum / float64(cnt)
}

// steadyStateMbps 丢弃 grace 预热期后估计稳态吞吐。
// 策略:优先用后半段采样(链路已爬升完毕)的中位数,再与全段修剪均值融合,
// 比单纯 P90 更接近真实可持续带宽,也比全程平均更少被慢启动拖低。
func steadyStateMbps(samples []speedSample, start time.Time, grace time.Duration) float64 {
	vals := make([]float64, 0, len(samples))
	for _, s := range samples {
		if s.mbps <= 0 {
			continue
		}
		if s.at.Sub(start) >= grace {
			vals = append(vals, s.mbps)
		}
	}
	if len(vals) == 0 {
		for _, s := range samples {
			if s.mbps > 0 {
				vals = append(vals, s.mbps)
			}
		}
	}
	if len(vals) == 0 {
		return 0
	}

	// 后半段:反映稳态;样本少时退回全部。
	tail := vals
	if len(vals) >= 6 {
		tail = vals[len(vals)/2:]
	} else if len(vals) >= 4 {
		tail = vals[len(vals)*2/5:]
	}
	tailSorted := append([]float64(nil), tail...)
	sort.Float64s(tailSorted)
	allSorted := append([]float64(nil), vals...)
	sort.Float64s(allSorted)

	tailMed := medianSorted(tailSorted)
	allTrim := trimmedMeanSorted(allSorted, 0.15)
	if tailMed <= 0 {
		return allTrim
	}
	if allTrim <= 0 {
		return tailMed
	}
	// 两者接近则取均值;差距大时以后半段中位数为准(更接近稳态)。
	lo, hi := tailMed, allTrim
	if lo > hi {
		lo, hi = hi, lo
	}
	if hi > 0 && (hi-lo)/hi <= 0.12 {
		return (tailMed + allTrim) / 2
	}
	return tailMed
}

// pickAccurateMbps 在稳态估计与库 EWMA 之间选更可靠的最终值。
// 两者都有效且偏差不大时取均值以降低单算法噪声;偏差大时优先稳态估计。
func pickAccurateMbps(steady, ewma float64) float64 {
	steady = validSpeedMbps(steady, 0)
	ewma = validSpeedMbps(ewma, 0)
	switch {
	case steady <= 0 && ewma <= 0:
		return 0
	case steady <= 0:
		return ewma
	case ewma <= 0:
		return steady
	}
	lo, hi := steady, ewma
	if lo > hi {
		lo, hi = hi, lo
	}
	if hi > 0 && (hi-lo)/hi <= 0.15 {
		return (steady + ewma) / 2
	}
	return steady
}

// adaptBroadbandStreams 根据测得 RTT 微调并发:高延迟需要更多流填满管道。
func adaptBroadbandStreams(base int, latencyMS int64) int {
	if base < 1 {
		base = defaultBroadbandStreams
	}
	switch {
	case latencyMS >= 80:
		if base < 16 {
			return 16
		}
	case latencyMS >= 40:
		if base < 12 {
			return 12
		}
	case latencyMS >= 20:
		if base < 10 {
			return 10
		}
	}
	if base > 16 {
		return 16
	}
	return base
}
