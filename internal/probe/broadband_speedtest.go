package probe

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/showwin/speedtest-go/speedtest"
)

const speedtestCNIDBaseURL = "https://raw.githubusercontent.com/spiritLHLS/speedtest.net-CN-ID/main"

const (
	maxOoklaPingCandidates        = 24
	maxDomesticTCPProbeCandidates = 24
	maxConcurrentCandidatePings   = 8
	// remoteNodeStrategyTimeout 限制单个远程发现策略(Ookla 列表拉取)的耗时,
	// 避免某个源卡死拖累整体。三个远程策略并发跑,任一先返回可用节点即采用。
	remoteNodeStrategyTimeout = 12 * time.Second
)

// 镜像按国内可达性排序：cdn0/cdn2 较稳定，cdn1/cdn3 经常超时放后面。
var speedtestCNIDMirrors = []string{
	speedtestCNIDBaseURL,
	"https://cdn0.spiritlhl.top/" + speedtestCNIDBaseURL,
	"http://cdn2.spiritlhl.net/" + speedtestCNIDBaseURL,
	"http://cdn4.spiritlhl.net/" + speedtestCNIDBaseURL,
	"http://cdn1.spiritlhl.net/" + speedtestCNIDBaseURL,
	"http://cdn3.spiritlhl.net/" + speedtestCNIDBaseURL,
}

var domesticSpeedtestSources = []domesticSpeedtestSource{
	{file: "CN_Unicom.csv", isp: "联通"},
	{file: "CN_Telecom.csv", isp: "电信"},
	{file: "CN_Mobile.csv", isp: "移动"},
}

// 兜底列表：优先带 host，可在 Ookla API 不可用时用 CustomServer 直连。
// 来源：Ookla JS API + spiritLHLS speedtest.net-CN-ID（列表会变动，host 是主路径）。
var fallbackDomesticSpeedtestCandidates = []domesticSpeedtestCandidate{
	// 联通
	{id: "24447", isp: "联通", city: "上海", supplier: "China Unicom 5G", host: "mobile.shunicomtest.com.prod.hosts.ooklaserver.net", port: "8080"},
	{id: "43752", isp: "联通", city: "北京", supplier: "BJ Unicom", host: "beijing.unicomtest.com", port: "8080"},
	// 电信
	{id: "5396", isp: "电信", city: "苏州", supplier: "China Telecom JiangSu 5G", host: "4gsuzhou1.speedtest.jsinfo.net.prod.hosts.ooklaserver.net", port: "8080"},
	{id: "36663", isp: "电信", city: "镇江", supplier: "China Telecom JiangSu 5G", host: "5gzhenjiang.speedtest.jsinfo.net.prod.hosts.ooklaserver.net", port: "8080"},
	{id: "59386", isp: "电信", city: "杭州", supplier: "浙江电信", host: "cesu-hz.zjtelecom.com.cn", port: "8080"},
	{id: "59387", isp: "电信", city: "宁波", supplier: "浙江电信", host: "cesu-nb.zjtelecom.com.cn", port: "8080"},
	{id: "30852", isp: "电信", city: "昆山", supplier: "Duke Kunshan University", host: "speedtest.dukekunshan.edu.cn", port: "8080"},
	// 移动
	{id: "16204", isp: "移动", city: "苏州", supplier: "JSQY", host: "speedtest.jsqiuying.com", port: "8080"},
}

var domesticSpeedtestHTTPClient = &http.Client{Timeout: 8 * time.Second}

type domesticSpeedtestSource struct {
	file string
	isp  string
}

type domesticSpeedtestCandidate struct {
	id       string
	isp      string
	city     string
	host     string
	port     string
	supplier string
}

type selectedSpeedtestServer struct {
	server *speedtest.Server
	source string
}

func executeBroadbandSpeedTest(ctx context.Context, svc *Service, duration time.Duration, progress func(stage string, progress int, message string, partial BroadbandSpeedResult), step stepFunc) (BroadbandSpeedResult, bool) {
	if duration <= 0 {
		duration = 15 * time.Second
	}
	ctx = withStepReporter(ctx, step)
	domesticOnly := true
	streams := defaultBroadbandStreams
	dataDir := ""
	if svc != nil {
		svc.mu.RLock()
		domesticOnly = svc.cfg.BroadbandDomesticOnly
		if svc.cfg.BroadbandStreams > 0 {
			streams = svc.cfg.BroadbandStreams
		}
		dataDir = svc.cfg.DataDir
		svc.mu.RUnlock()
	}

	result := BroadbandSpeedResult{
		Timestamp: localTimestamp(),
		Provider:  "Speedtest.net",
	}

	var resultMu sync.Mutex
	report := func(stage string, pct int, message string) {
		if progress != nil {
			resultMu.Lock()
			partial := result
			resultMu.Unlock()
			progress(stage, clampProgress(pct), message, partial)
		}
	}
	setResult := func(update func(*BroadbandSpeedResult)) {
		resultMu.Lock()
		update(&result)
		resultMu.Unlock()
	}
	currentResult := func() BroadbandSpeedResult {
		resultMu.Lock()
		defer resultMu.Unlock()
		return result
	}
	fail := func(stage, reason string) (BroadbandSpeedResult, bool) {
		emitStep(ctx, stage, "fail", reason)
		setResult(func(r *BroadbandSpeedResult) {
			r.FailureStage = stage
			r.FailureReason = reason
			r.Error = reason
		})
		return currentResult(), false
	}

	report("starting", 2, "正在初始化测速引擎")
	emitStep(ctx, "starting", "info", "初始化测速引擎")
	stClient := speedtest.New()
	stClient.SetCaptureTime(duration)
	stClient.SetRateCaptureFrequency(200 * time.Millisecond)
	stClient.SetNThread(streams)
	setResult(func(r *BroadbandSpeedResult) {
		r.DownloadStreams = streams
	})
	emitStep(ctx, "starting", "info", fmt.Sprintf("并发连接数: %d", streams))

	var sampleMu sync.Mutex
	var downloadSamples, uploadSamples []speedSample

	var server *speedtest.Server

	if domesticOnly {
		report("starting", 5, "正在检索国内优质运营商节点")
		emitStep(ctx, "node_selection", "running", "开始检索国内测速节点")
		var selected *selectedSpeedtestServer
		if cached := tryLoadCachedSpeedtestServer(ctx, stClient, dataDir); cached != nil {
			emitStep(ctx, "node_selection", "ok", fmt.Sprintf("命中缓存节点: %s (跳过发现)", cached.server.Sponsor))
			selected = cached
		} else {
			selected = selectDomesticSpeedtestServer(ctx, stClient, svc)
		}
		if selected != nil {
			server = selected.server
			// 确保 server.Context 指向设置了回调的 stClient，
			// 否则 Download/Upload 回调不会触发（实时进度丢失）。
			server.Context = stClient
			setResult(func(r *BroadbandSpeedResult) {
				r.NodeSource = selected.source
			})
		}
		if server == nil {
			return fail("node_selection", "未找到可用的国内 Speedtest 节点")
		}
	} else {
		report("starting", 10, "正在寻找最近的响应节点")
		emitStep(ctx, "node_selection", "running", "寻找最近的 Ookla 响应节点")
		serverList, err := stClient.FetchServers()
		if err == nil && len(serverList) > 0 {
			targets, _ := serverList.FindServer([]int{})
			if len(targets) > 0 {
				server = targets[0]
				setResult(func(r *BroadbandSpeedResult) {
					r.NodeSource = "Ookla 最近节点"
				})
			}
		}
	}

	if server == nil {
		return fail("node_selection", "无法连接到测速服务器")
	}

	setResult(func(r *BroadbandSpeedResult) {
		r.Provider = server.Sponsor
		r.ServerRegion = fmt.Sprintf("%s · %s", server.Name, server.Country)
		r.ServerID = server.ID
		r.ServerName = server.Name
		r.ServerCountry = server.Country
		r.ServerHost = server.Host
		r.DomesticNode = isChinaSpeedtestServer(server)
	})
	report("latency", 15, fmt.Sprintf("已选节点：%s (%s)", server.Sponsor, server.Name))
	emitStep(ctx, "node_selection", "ok", fmt.Sprintf("已选节点: %s · %s", server.Sponsor, server.Name))

	_ = server.PingTestContext(ctx, nil)
	latencyMS := int64(server.Latency.Milliseconds())
	jitterMS := int64(server.Jitter.Milliseconds())
	setResult(func(r *BroadbandSpeedResult) {
		r.LatencyMS = latencyMS
		r.JitterMS = jitterMS
	})
	report("latency", 25, fmt.Sprintf("延迟 %d ms · 抖动 %d ms", latencyMS, jitterMS))
	emitStep(ctx, "latency", "ok", fmt.Sprintf("延迟 %d ms · 抖动 %d ms", latencyMS, jitterMS))

	// 按 RTT 适配并发后再开压测,避免高延迟链路测不满。
	streams = adaptBroadbandStreams(streams, latencyMS)
	stClient.SetNThread(streams)
	setResult(func(r *BroadbandSpeedResult) {
		r.DownloadStreams = streams
	})

	report("download", 30, "准备开始下载压测")
	emitStep(ctx, "download", "running", "开始下载压测")
	downloadStart := time.Now()
	stClient.SetCallbackDownload(func(rate speedtest.ByteRate) {
		mbps := rate.Mbps()
		if mbps <= 0 {
			return
		}
		setResult(func(r *BroadbandSpeedResult) {
			r.DownloadMbps = mbps
		})
		sampleMu.Lock()
		downloadSamples = append(downloadSamples, speedSample{at: time.Now(), mbps: mbps})
		sampleMu.Unlock()
		pct := 30 + progressRange(time.Since(downloadStart), duration, 30)
		report("download", pct, fmt.Sprintf("下载测速中 %.2f Mbps", mbps))
	})

	err := server.DownloadTestContext(ctx)
	stClient.SetCallbackDownload(nil)
	if err != nil {
		return fail("download", "下载测试失败: "+err.Error())
	}
	dlEWMA := server.DLSpeed.Mbps()
	sampleMu.Lock()
	dlSteady := steadyStateMbps(downloadSamples, downloadStart, broadbandDownloadGrace)
	sampleMu.Unlock()
	downloadMbps := pickAccurateMbps(dlSteady, dlEWMA)
	if downloadMbps <= 0 {
		downloadMbps = validSpeedMbps(currentResult().DownloadMbps, 0)
	}
	if downloadMbps <= 0 {
		return fail("download", "下载测试未获取到有效速度样本")
	}
	setResult(func(r *BroadbandSpeedResult) {
		r.DownloadMbps = downloadMbps
		r.DownloadMbpsEWMA = dlEWMA
	})
	report("download", 60, fmt.Sprintf("下载完成 %.1f Mbps", downloadMbps))
	emitStep(ctx, "download", "ok", fmt.Sprintf("下载完成 %.1f Mbps (EWMA %.1f)", downloadMbps, dlEWMA))

	report("upload", 65, "准备开始上传压测")
	emitStep(ctx, "upload", "running", "开始上传压测")
	uploadStart := time.Now()
	stClient.SetCallbackUpload(func(rate speedtest.ByteRate) {
		mbps := rate.Mbps()
		if mbps <= 0 {
			return
		}
		setResult(func(r *BroadbandSpeedResult) {
			r.UploadMbps = mbps
		})
		sampleMu.Lock()
		uploadSamples = append(uploadSamples, speedSample{at: time.Now(), mbps: mbps})
		sampleMu.Unlock()
		pct := 65 + progressRange(time.Since(uploadStart), duration, 30)
		report("upload", pct, fmt.Sprintf("上传测速中 %.2f Mbps", mbps))
	})

	err = server.UploadTestContext(ctx)
	stClient.SetCallbackUpload(nil)
	if err != nil {
		return fail("upload", "上传测试失败: "+err.Error())
	}
	ulEWMA := server.ULSpeed.Mbps()
	sampleMu.Lock()
	ulSteady := steadyStateMbps(uploadSamples, uploadStart, broadbandUploadGrace)
	sampleMu.Unlock()
	uploadMbps := pickAccurateMbps(ulSteady, ulEWMA)
	if uploadMbps <= 0 {
		uploadMbps = validSpeedMbps(currentResult().UploadMbps, 0)
	}
	if uploadMbps <= 0 {
		return fail("upload", "上传测试未获取到有效速度样本")
	}
	setResult(func(r *BroadbandSpeedResult) {
		r.UploadMbps = uploadMbps
		r.UploadMbpsEWMA = ulEWMA
	})
	report("upload", 95, fmt.Sprintf("上传完成 %.1f Mbps", uploadMbps))
	emitStep(ctx, "upload", "ok", fmt.Sprintf("上传完成 %.1f Mbps", uploadMbps))

	setResult(func(r *BroadbandSpeedResult) {
		r.Timestamp = localTimestamp()
	})
	report("finalizing", 100, "测速全部完成")
	emitStep(ctx, "finalizing", "ok", "测速全部完成")

	if server != nil {
		final := currentResult()
		saveBroadbandNodeCache(dataDir, broadbandNodeCache{
			ServerID:     server.ID,
			Host:         server.Host,
			Source:       final.NodeSource,
			DownloadMbps: final.DownloadMbps,
			CachedAt:     localTimestamp(),
		})
	}

	return currentResult(), true
}

// selectDomesticSpeedtestServer 选择国内测速节点。
// 三个远程发现策略并发执行,任一先返回可用节点即采用(其余被取消),
// 显著缩短首次冷启动选节点耗时;全部失败时回退到内置兜底列表。
//  1. Ookla API 关键词搜索 (search=China)
//  2. Ookla API 地理坐标搜索 (中国中心点)
//  3. spiritLHLS CSV 远程列表 (GitHub)
