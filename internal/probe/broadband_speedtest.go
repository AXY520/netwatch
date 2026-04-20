package probe

import (
	"context"
	"fmt"
	"time"
)

func executeBroadbandSpeedTest(ctx context.Context, duration time.Duration, progress func(stage string, progress int, message string, partial BroadbandSpeedResult)) (BroadbandSpeedResult, bool) {
	if duration <= 0 {
		duration = 15 * time.Second
	}

	result := BroadbandSpeedResult{
		Timestamp: localTimestamp(),
		Provider:  "国内 CDN 镜像",
	}

	report := func(stage string, pct int, message string) {
		if progress != nil {
			progress(stage, clampProgress(pct), message, result)
		}
	}

	report("starting", 2, "正在并发探测国内镜像节点")
	picked := probeCDNEndpoints(ctx)
	if len(picked) == 0 {
		result.Error = "国内镜像节点全部不可达，请检查网络"
		return result, false
	}

	top := picked
	if len(top) > 4 {
		top = top[:4]
	}
	main := top[0]
	result.Provider = main.Name
	result.ServerRegion = fmt.Sprintf("%s · %s", main.Region, main.ISP)
	report("starting", 8, fmt.Sprintf("已选节点：%s（TTFB %d ms），并发使用前 %d 个", main.Name, main.ttfbMS, len(top)))

	report("latency", 10, "采样延迟与抖动")
	latency, jitter := measureCDNLatency(ctx, main.URL, 6)
	result.LatencyMS = latency
	result.JitterMS = jitter
	report("latency", 18, fmt.Sprintf("延迟 %d ms · 抖动 %d ms", latency, jitter))

	dlStart := time.Now()
	dlMbps := runCDNDownload(ctx, top, duration, 4, func(mbps float64, elapsed time.Duration) {
		result.DownloadMbps = mbps
		pct := 18 + int(elapsed.Seconds()/duration.Seconds()*37)
		report("download", pct, fmt.Sprintf("下载测速中 %.2f Mbps", mbps))
	})
	if ctx.Err() != nil {
		result.Error = "测速已取消"
		return result, false
	}
	if dlMbps > 0 {
		result.DownloadMbps = dlMbps
	}
	report("download", 55, fmt.Sprintf("下载完成 %.2f Mbps · 用时 %s", result.DownloadMbps, time.Since(dlStart).Round(time.Second)))

	upStart := time.Now()
	upMbps := runCloudflareUpload(ctx, duration, 4, func(mbps float64, elapsed time.Duration) {
		result.UploadMbps = mbps
		pct := 55 + int(elapsed.Seconds()/duration.Seconds()*40)
		report("upload", pct, fmt.Sprintf("上传测速中 %.2f Mbps（走 Cloudflare 回程）", mbps))
	})
	if ctx.Err() != nil {
		result.Error = "测速已取消"
		return result, false
	}
	if upMbps > 0 {
		result.UploadMbps = upMbps
	}
	report("upload", 96, fmt.Sprintf("上传完成 %.2f Mbps · 用时 %s", result.UploadMbps, time.Since(upStart).Round(time.Second)))

	result.Timestamp = localTimestamp()
	report("finalizing", 98, fmt.Sprintf("完成：↓ %.1f Mbps / ↑ %.1f Mbps · 节点 %s", result.DownloadMbps, result.UploadMbps, main.Name))
	return result, true
}
