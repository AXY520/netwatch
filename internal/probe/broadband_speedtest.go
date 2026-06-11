package probe

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"netwatch/internal/logger"

	"github.com/showwin/speedtest-go/speedtest"
)

// stepFunc 上报一条测速过程步骤,供前端实时展示后端正在做什么。
// status 取值:running|ok|fail|info。
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
	// 在低核设备上测不满高带宽;显式提到 8 让多连接跑满千兆。
	defaultBroadbandStreams = 8
	// 预热期(grace)内的采样不计入稳态速度,用于跳过 TCP 慢启动。
	broadbandDownloadGrace = 2 * time.Second
	broadbandUploadGrace   = 3 * time.Second
)

// speedSample 是一次实时速率采样(Mbps)及其时刻。
type speedSample struct {
	at   time.Time
	mbps float64
}

// steadyStateMbps 丢弃 grace 预热期(TCP 慢启动)后,取剩余采样的 P90 作为稳态速度。
// P90 既反映链路稳定可达的高速(更接近真实带宽),又排除 top 10% 的偶发尖峰而比取峰值稳健。
// 样本不足时退回全部样本。
func steadyStateMbps(samples []speedSample, start time.Time, grace time.Duration) float64 {
	vals := make([]float64, 0, len(samples))
	for _, s := range samples {
		if s.at.Sub(start) >= grace {
			vals = append(vals, s.mbps)
		}
	}
	if len(vals) == 0 {
		for _, s := range samples {
			vals = append(vals, s.mbps)
		}
	}
	if len(vals) == 0 {
		return 0
	}
	sort.Float64s(vals)
	idx := int(0.9 * float64(len(vals)-1))
	return vals[idx]
}

const speedtestCNIDBaseURL = "https://raw.githubusercontent.com/spiritLHLS/speedtest.net-CN-ID/main"

const (
	maxOoklaPingCandidates        = 24
	maxDomesticTCPProbeCandidates = 24
	maxConcurrentCandidatePings   = 8
	// remoteNodeStrategyTimeout 限制单个远程发现策略(Ookla 列表拉取)的耗时,
	// 避免某个源卡死拖累整体。三个远程策略并发跑,任一先返回可用节点即采用。
	remoteNodeStrategyTimeout = 8 * time.Second
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

// 兜底列表：来自 Ookla API + spiritLHLS CSV 的国内节点，覆盖三网主要城市。
var fallbackDomesticSpeedtestCandidates = []domesticSpeedtestCandidate{
	// 联通
	{id: "24447", isp: "联通", city: "上海", supplier: "China Unicom 5G", host: "mobile.shunicomtest.com.prod.hosts.ooklaserver.net", port: "8080"},
	{id: "43752", isp: "联通", city: "北京", supplier: "BJ Unicom"},
	{id: "51413", isp: "联通", city: "广州", supplier: "China Unicom Guangzhou"},
	{id: "48832", isp: "联通", city: "成都", supplier: "China Unicom Chengdu"},
	{id: "57477", isp: "联通", city: "南京", supplier: "China Unicom Nanjing"},
	// 电信
	{id: "5396", isp: "电信", city: "苏州", supplier: "China Telecom JiangSu 5G", host: "4gsuzhou1.speedtest.jsinfo.net.prod.hosts.ooklaserver.net", port: "8080"},
	{id: "36663", isp: "电信", city: "镇江", supplier: "China Telecom JiangSu 5G", host: "5gzhenjiang.speedtest.jsinfo.net.prod.hosts.ooklaserver.net", port: "8080"},
	{id: "59386", isp: "电信", city: "杭州", supplier: "浙江电信", host: "cesu-hz.zjtelecom.com.cn", port: "8080"},
	{id: "59387", isp: "电信", city: "浙江", supplier: "浙江电信"},
	{id: "30852", isp: "电信", city: "昆山", supplier: "Duke Kunshan University", host: "speedtest.dukekunshan.edu.cn", port: "8080"},
	{id: "54156", isp: "电信", city: "上海", supplier: "China Telecom Shanghai"},
	{id: "29026", isp: "电信", city: "南京", supplier: "China Telecom JiangSu"},
	// 移动
	{id: "16204", isp: "移动", city: "苏州", supplier: "JSQY", host: "speedtest.jsqiuying.com", port: "8080"},
	{id: "41906", isp: "移动", city: "上海", supplier: "China Mobile Shanghai"},
	{id: "26970", isp: "移动", city: "北京", supplier: "China Mobile Beijing"},
	{id: "55075", isp: "移动", city: "广州", supplier: "China Mobile Guangzhou"},
	{id: "27249", isp: "移动", city: "杭州", supplier: "China Mobile Hangzhou"},
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
	startedAt := time.Now()
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
			r.StageDurations.TotalMS = elapsedMS(startedAt)
		})
		return currentResult(), false
	}

	report("starting", 2, "正在初始化测速引擎")
	emitStep(ctx, "starting", "info", "初始化测速引擎")
	stClient := speedtest.New()
	stClient.SetCaptureTime(duration)
	stClient.SetRateCaptureFrequency(250 * time.Millisecond)
	stClient.SetNThread(streams)
	setResult(func(r *BroadbandSpeedResult) {
		r.DownloadStreams = streams
	})
	emitStep(ctx, "starting", "info", fmt.Sprintf("并发连接数: %d", streams))

	var sampleMu sync.Mutex
	var downloadSamples, uploadSamples []speedSample

	var server *speedtest.Server
	nodeStartedAt := time.Now()

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
	nodeSelectionMS := elapsedMS(nodeStartedAt)
	setResult(func(r *BroadbandSpeedResult) {
		r.StageDurations.NodeSelectionMS = nodeSelectionMS
	})

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

	latencyStartedAt := time.Now()
	_ = server.PingTestContext(ctx, nil)
	latencyMS := int64(server.Latency.Milliseconds())
	jitterMS := int64(server.Jitter.Milliseconds())
	setResult(func(r *BroadbandSpeedResult) {
		r.LatencyMS = latencyMS
		r.JitterMS = jitterMS
		r.StageDurations.LatencyTestMS = elapsedMS(latencyStartedAt)
	})
	report("latency", 25, fmt.Sprintf("延迟 %d ms · 抖动 %d ms", latencyMS, jitterMS))
	emitStep(ctx, "latency", "ok", fmt.Sprintf("延迟 %d ms · 抖动 %d ms", latencyMS, jitterMS))

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
	downloadMbps := dlSteady
	if downloadMbps <= 0 {
		downloadMbps = validSpeedMbps(dlEWMA, currentResult().DownloadMbps)
	}
	if downloadMbps <= 0 {
		return fail("download", "下载测试未获取到有效速度样本")
	}
	setResult(func(r *BroadbandSpeedResult) {
		r.DownloadMbps = downloadMbps
		r.DownloadMbpsEWMA = dlEWMA
		r.StageDurations.DownloadTestMS = elapsedMS(downloadStart)
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
	uploadMbps := ulSteady
	if uploadMbps <= 0 {
		uploadMbps = validSpeedMbps(ulEWMA, currentResult().UploadMbps)
	}
	if uploadMbps <= 0 {
		return fail("upload", "上传测试未获取到有效速度样本")
	}
	setResult(func(r *BroadbandSpeedResult) {
		r.UploadMbps = uploadMbps
		r.UploadMbpsEWMA = ulEWMA
		r.StageDurations.UploadTestMS = elapsedMS(uploadStart)
	})
	report("upload", 95, fmt.Sprintf("上传完成 %.1f Mbps", uploadMbps))
	emitStep(ctx, "upload", "ok", fmt.Sprintf("上传完成 %.1f Mbps", uploadMbps))

	setResult(func(r *BroadbandSpeedResult) {
		r.Timestamp = localTimestamp()
		r.StageDurations.TotalMS = elapsedMS(startedAt)
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
//  4. 内置扩展兜底列表 (仅当上述全部失败)
func selectDomesticSpeedtestServer(ctx context.Context, stClient *speedtest.Speedtest, svc *Service) *selectedSpeedtestServer {
	preferredISP := detectPreferredDomesticISP(ctx, svc)
	logger.Info("broadband: preferred domestic isp=%q", preferredISP)
	if preferredISP != "" {
		emitStep(ctx, "node_selection", "info", "本机出口运营商: "+preferredISP)
	}

	emitStep(ctx, "node_selection", "info", "并发探测国内节点源(Ookla 关键词/坐标 + GitHub 列表)")

	// 并发跑三个远程策略,首个返回可用节点即采用,取消其余。
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type strategyResult struct {
		server *speedtest.Server
		source string
	}
	results := make(chan strategyResult, 3)
	strategies := []struct {
		source string
		run    func(context.Context) *speedtest.Server
	}{
		{"Ookla 关键词", func(c context.Context) *speedtest.Server { return selectDomesticViaOoklaKeyword(c, preferredISP) }},
		{"Ookla 坐标", func(c context.Context) *speedtest.Server { return selectDomesticViaOoklaLocation(c, preferredISP) }},
		{"国内节点列表", func(c context.Context) *speedtest.Server { return selectDomesticViaCSV(c, stClient, preferredISP) }},
	}
	for _, st := range strategies {
		st := st
		go func() {
			results <- strategyResult{server: st.run(raceCtx), source: st.source}
		}()
	}

	for i := 0; i < len(strategies); i++ {
		r := <-results
		if r.server != nil {
			emitStep(ctx, "node_selection", "ok", fmt.Sprintf("命中节点源: %s", r.source))
			cancel() // 取消其余仍在跑的策略
			return &selectedSpeedtestServer{server: r.server, source: r.source}
		}
	}

	// 策略 4: 内置兜底列表
	logger.Warn("broadband: all remote sources failed, using embedded fallback list")
	emitStep(ctx, "node_selection", "info", "远程源不可用,改用内置兜底节点列表")
	if s := selectDomesticFromCandidates(ctx, stClient, fallbackDomesticSpeedtestCandidates, preferredISP); s != nil {
		return &selectedSpeedtestServer{server: s, source: "内置兜底"}
	}
	return nil
}

// isCanceledErr 判断错误是否由 context 取消/超时引起。并发竞速中,
// 先返回可用节点的策略会取消其余策略,被取消的策略报错属预期,不应记为 WARN。
func isCanceledErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// selectDomesticViaOoklaKeyword 用 Ookla API 的 search=China 关键词搜索。
func selectDomesticViaOoklaKeyword(ctx context.Context, preferredISP string) *speedtest.Server {
	kwClient := speedtest.New()
	kwClient.NewUserConfig(&speedtest.UserConfig{Keyword: "China"})
	fetchCtx, cancel := context.WithTimeout(ctx, remoteNodeStrategyTimeout)
	defer cancel()
	serverList, err := kwClient.FetchServerListContext(fetchCtx)
	if err != nil {
		if !isCanceledErr(err) {
			logger.Warn("broadband: ookla keyword search failed: %v", err)
		}
		return nil
	}
	cnServers := filterChinaServers(serverList)
	if len(cnServers) == 0 {
		logger.Info("broadband: ookla keyword search returned 0 China servers")
		return nil
	}
	logger.Info("broadband: ookla keyword found %d China servers", len(cnServers))
	return pickBestChinaServer(ctx, cnServers, preferredISP)
}

// selectDomesticViaOoklaLocation 用中国中心坐标 (35, 105) 搜索附近节点。
func selectDomesticViaOoklaLocation(ctx context.Context, preferredISP string) *speedtest.Server {
	locClient := speedtest.New()
	locClient.NewUserConfig(&speedtest.UserConfig{Location: &speedtest.Location{Lat: 35, Lon: 105}})
	fetchCtx, cancel := context.WithTimeout(ctx, remoteNodeStrategyTimeout)
	defer cancel()
	serverList, err := locClient.FetchServerListContext(fetchCtx)
	if err != nil {
		if !isCanceledErr(err) {
			logger.Warn("broadband: ookla location search failed: %v", err)
		}
		return nil
	}
	cnServers := filterChinaServers(serverList)
	if len(cnServers) == 0 {
		logger.Info("broadband: ookla location search returned 0 China servers")
		return nil
	}
	logger.Info("broadband: ookla location found %d China servers", len(cnServers))
	return pickBestChinaServer(ctx, cnServers, preferredISP)
}

// selectDomesticViaCSV 从 spiritLHLS 的 CSV 列表获取候选节点。
func selectDomesticViaCSV(ctx context.Context, stClient *speedtest.Speedtest, preferredISP string) *speedtest.Server {
	candidates := fetchDomesticSpeedtestCandidates(ctx)
	if len(candidates) == 0 {
		logger.Warn("broadband: remote CN speedtest CSV unavailable")
		return nil
	}
	logger.Info("broadband: CSV fetched %d domestic candidates", len(candidates))
	return selectDomesticFromCandidates(ctx, stClient, candidates, preferredISP)
}

// selectDomesticFromCandidates 从候选列表中选最优节点：先按 ISP 筛选，再按延迟排序。
func selectDomesticFromCandidates(ctx context.Context, stClient *speedtest.Speedtest, candidates []domesticSpeedtestCandidate, preferredISP string) *speedtest.Server {
	if preferredISP != "" {
		preferred := filterDomesticSpeedtestCandidatesByISP(candidates, preferredISP)
		if len(preferred) > 0 {
			logger.Info("broadband: ISP-filtered candidates isp=%s count=%d", preferredISP, len(preferred))
			candidates = preferred
		} else {
			logger.Warn("broadband: no ISP-matched candidates for isp=%s, using all %d", preferredISP, len(candidates))
		}
	}

	candidateList := nearestDomesticSpeedtestCandidates(ctx, candidates, 3)
	emitStep(ctx, "node_selection", "info", fmt.Sprintf("逐一测试 %d 个候选节点延迟", len(candidateList)))

	var best *speedtest.Server
	for _, candidate := range candidateList {
		if ctx.Err() != nil {
			return nil
		}
		serverCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		s, err := stClient.FetchServerByIDContext(serverCtx, candidate.id)
		if err != nil || s == nil || !isChinaSpeedtestServer(s) {
			if err != nil && !isCanceledErr(err) {
				logger.Warn("broadband: fetch server by id failed id=%s isp=%s city=%s err=%v", candidate.id, candidate.isp, candidate.city, err)
			}
			cancel()
			continue
		}
		if err := s.PingTestContext(serverCtx, nil); err != nil {
			if !isCanceledErr(err) {
				logger.Warn("broadband: ping candidate failed id=%s isp=%s city=%s sponsor=%s err=%v", candidate.id, candidate.isp, candidate.city, s.Sponsor, err)
			}
			cancel()
			continue
		}
		cancel()
		if s.Latency <= 0 {
			logger.Warn("broadband: candidate latency invalid id=%s isp=%s city=%s sponsor=%s", candidate.id, candidate.isp, candidate.city, s.Sponsor)
			continue
		}
		logger.Info("broadband: candidate ok id=%s isp=%s city=%s sponsor=%s latency=%s", candidate.id, candidate.isp, candidate.city, s.Sponsor, s.Latency.Round(time.Millisecond))
		emitStep(ctx, "node_selection", "ok", fmt.Sprintf("候选 %s%s 延迟 %d ms", candidate.city, candidate.isp, s.Latency.Milliseconds()))
		if best == nil || s.Latency < best.Latency {
			best = s
		}
	}
	if best != nil {
		logger.Info("broadband: selected server id=%s sponsor=%s name=%s latency=%s", best.ID, best.Sponsor, best.Name, best.Latency.Round(time.Millisecond))
	}
	return best
}

// filterChinaServers 从 speedtest.Server 列表中筛选中国节点。
func filterChinaServers(servers speedtest.Servers) speedtest.Servers {
	var out speedtest.Servers
	for _, s := range servers {
		if isChinaSpeedtestServer(s) {
			out = append(out, s)
		}
	}
	return out
}

// pickBestChinaServer 从中国节点列表中选延迟最低的，优先匹配 ISP。
func pickBestChinaServer(ctx context.Context, servers speedtest.Servers, preferredISP string) *speedtest.Server {
	if preferredISP != "" {
		var matched speedtest.Servers
		for _, s := range servers {
			if matchServerISP(s, preferredISP) {
				matched = append(matched, s)
			}
		}
		if len(matched) > 0 {
			servers = matched
			logger.Info("broadband: ISP-matched %d servers for isp=%s", len(matched), preferredISP)
		}
	}
	if len(servers) > maxOoklaPingCandidates {
		logger.Info("broadband: limiting Ookla ping candidates from %d to %d", len(servers), maxOoklaPingCandidates)
		servers = servers[:maxOoklaPingCandidates]
	}
	emitStep(ctx, "node_selection", "info", fmt.Sprintf("测试 %d 个 Ookla 候选节点延迟", len(servers)))

	var best *speedtest.Server
	for _, s := range servers {
		if ctx.Err() != nil {
			return nil
		}
		pingCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		if err := s.PingTestContext(pingCtx, nil); err != nil {
			cancel()
			continue
		}
		cancel()
		if s.Latency <= 0 {
			continue
		}
		logger.Info("broadband: ping ok id=%s sponsor=%s latency=%s", s.ID, s.Sponsor, s.Latency.Round(time.Millisecond))
		if best == nil || s.Latency < best.Latency {
			best = s
		}
	}
	if best != nil {
		logger.Info("broadband: pickBest id=%s sponsor=%s name=%s latency=%s", best.ID, best.Sponsor, best.Name, best.Latency.Round(time.Millisecond))
	}
	return best
}

// matchServerISP 检查 speedtest.Server 的 Sponsor/Name 是否匹配指定 ISP。
func matchServerISP(s *speedtest.Server, isp string) bool {
	text := strings.ToLower(s.Sponsor + " " + s.Name)
	switch isp {
	case "联通":
		return strings.Contains(text, "unicom") || strings.Contains(text, "联通")
	case "电信":
		return strings.Contains(text, "telecom") || strings.Contains(text, "电信")
	case "移动":
		return strings.Contains(text, "mobile") || strings.Contains(text, "cmcc") || strings.Contains(text, "移动")
	}
	return false
}

func detectPreferredDomesticISP(ctx context.Context, svc *Service) string {
	if svc == nil {
		return ""
	}
	lookups := svc.GetEgressLookups(ctx)
	for _, isp := range []string{
		lookups.DomesticIP.IPv4.ISP,
		lookups.DomesticIP.IPv6.ISP,
		firstDomesticLookupISP(lookups.Lookups),
	} {
		if normalized := normalizeDomesticISPName(isp); normalized != "" {
			return normalized
		}
	}
	return ""
}

func firstDomesticLookupISP(items []EgressLookup) string {
	for _, item := range items {
		if item.Scope == "domestic" && item.ISP != "" {
			return item.ISP
		}
	}
	return ""
}

func normalizeDomesticISPName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(value, "联通"), strings.Contains(value, "unicom"), strings.Contains(value, "china unicom"):
		return "联通"
	case strings.Contains(value, "电信"), strings.Contains(value, "telecom"), strings.Contains(value, "china telecom"):
		return "电信"
	case strings.Contains(value, "移动"), strings.Contains(value, "cmcc"), strings.Contains(value, "china mobile"):
		return "移动"
	default:
		return ""
	}
}

func filterDomesticSpeedtestCandidatesByISP(candidates []domesticSpeedtestCandidate, isp string) []domesticSpeedtestCandidate {
	if isp == "" {
		return candidates
	}
	out := make([]domesticSpeedtestCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.isp == isp {
			out = append(out, candidate)
		}
	}
	return out
}

func fetchDomesticSpeedtestCandidates(ctx context.Context) []domesticSpeedtestCandidate {
	seen := make(map[string]struct{})
	var out []domesticSpeedtestCandidate
	for _, source := range domesticSpeedtestSources {
		items := fetchDomesticSpeedtestCSV(ctx, source)
		for _, item := range items {
			if _, ok := seen[item.id]; ok {
				continue
			}
			seen[item.id] = struct{}{}
			out = append(out, item)
		}
	}
	return out
}

func fetchDomesticSpeedtestCSV(ctx context.Context, source domesticSpeedtestSource) []domesticSpeedtestCandidate {
	type result struct {
		candidates []domesticSpeedtestCandidate
		base       string
	}
	ch := make(chan result, len(speedtestCNIDMirrors))
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	for _, base := range speedtestCNIDMirrors {
		base := base
		go func() {
			reqCtx, reqCancel := context.WithTimeout(ctx, 8*time.Second)
			defer reqCancel()
			req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, base+"/"+source.file, nil)
			if err != nil {
				return
			}
			resp, err := domesticSpeedtestHTTPClient.Do(req)
			if err != nil || resp == nil {
				if err != nil && !isCanceledErr(err) {
					logger.Warn("broadband: fetch csv failed source=%s base=%s err=%v", source.file, base, err)
				}
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 400 {
				logger.Warn("broadband: fetch csv bad status source=%s base=%s status=%d", source.file, base, resp.StatusCode)
				return
			}

			reader := csv.NewReader(resp.Body)
			reader.FieldsPerRecord = -1
			records, err := reader.ReadAll()
			if err != nil {
				logger.Warn("broadband: parse csv failed source=%s base=%s err=%v", source.file, base, err)
				return
			}

			out := parseDomesticCSV(records, source.isp)
			if len(out) > 0 {
				logger.Info("broadband: fetched csv source=%s base=%s count=%d", source.file, base, len(out))
				ch <- result{candidates: out, base: base}
			}
		}()
	}

	select {
	case r := <-ch:
		return r.candidates
	case <-ctx.Done():
		logger.Warn("broadband: no valid candidates from source=%s (concurrent, %d mirrors attempted)", source.file, len(speedtestCNIDMirrors))
		return nil
	}
}

// csvColumns 记录 CSV 中各关键字段的列索引。
type csvColumns struct {
	id, country, city, host, port, supplier int
}

// defaultCSVColumns 是 spiritLHLS speedtest.net-CN-ID 的历史列布局,
// 当 CSV 表头无法识别时回退使用,保持与旧版兼容。
var defaultCSVColumns = csvColumns{id: 0, country: 2, city: 3, host: 5, port: 6, supplier: 7}

func (c csvColumns) maxIndex() int {
	m := c.id
	for _, v := range []int{c.country, c.city, c.host, c.port, c.supplier} {
		if v > m {
			m = v
		}
	}
	return m
}

// detectCSVColumns 按表头名定位各字段列,使上游调整列顺序时仍能正确解析;
// 返回 false 表示表头不可识别(此时调用方回退到默认列布局)。
func detectCSVColumns(header []string) (csvColumns, bool) {
	cols := defaultCSVColumns
	matched := 0
	for i, name := range header {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "id":
			cols.id, matched = i, matched+1
		case "country", "cc":
			cols.country, matched = i, matched+1
		case "city", "name":
			cols.city, matched = i, matched+1
		case "host", "url":
			cols.host, matched = i, matched+1
		case "port":
			cols.port, matched = i, matched+1
		case "sponsor", "supplier":
			cols.supplier, matched = i, matched+1
		}
	}
	return cols, matched >= 3
}

// parseDomesticCSV 解析 spiritLHLS CSV,优先按表头名映射列,表头不可识别时
// 回退到固定列索引;仅保留 country 为 China 的行。对上游格式变更有韧性。
func parseDomesticCSV(records [][]string, isp string) []domesticSpeedtestCandidate {
	if len(records) == 0 {
		return nil
	}
	cols := defaultCSVColumns
	start := 0
	if c, ok := detectCSVColumns(records[0]); ok {
		cols, start = c, 1
	} else if len(records[0]) > 0 && strings.EqualFold(strings.TrimSpace(records[0][0]), "id") {
		start = 1 // 首行是表头但列名不可识别,跳过它并用默认布局
	}
	maxIdx := cols.maxIndex()
	out := make([]domesticSpeedtestCandidate, 0, len(records))
	for _, record := range records[start:] {
		if len(record) <= maxIdx {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(record[cols.country]), "China") {
			continue
		}
		id := strings.TrimSpace(record[cols.id])
		if id == "" || strings.EqualFold(id, "id") {
			continue
		}
		out = append(out, domesticSpeedtestCandidate{
			id:       id,
			isp:      isp,
			city:     strings.TrimSpace(record[cols.city]),
			host:     strings.TrimSpace(record[cols.host]),
			port:     strings.TrimSpace(record[cols.port]),
			supplier: strings.TrimSpace(record[cols.supplier]),
		})
	}
	return out
}

func nearestDomesticSpeedtestCandidates(ctx context.Context, candidates []domesticSpeedtestCandidate, perISP int) []domesticSpeedtestCandidate {
	if perISP <= 0 {
		perISP = 2
	}

	grouped := make(map[string][]domesticSpeedtestCandidate)
	for _, candidate := range candidates {
		grouped[candidate.isp] = append(grouped[candidate.isp], candidate)
	}

	// 收集各 ISP 限量后的候选,并发探测延迟(避免串行 ping 累积到数分钟)。
	var toProbe []domesticSpeedtestCandidate
	for _, source := range domesticSpeedtestSources {
		items := grouped[source.isp]
		if len(items) > maxDomesticTCPProbeCandidates {
			items = items[:maxDomesticTCPProbeCandidates]
		}
		toProbe = append(toProbe, items...)
	}
	latencies := concurrentPingCandidates(ctx, toProbe)

	var selected []domesticSpeedtestCandidate
	for _, source := range domesticSpeedtestSources {
		items := grouped[source.isp]
		if len(items) > maxDomesticTCPProbeCandidates {
			items = items[:maxDomesticTCPProbeCandidates]
		}
		sort.SliceStable(items, func(i, j int) bool {
			return latencies[items[i].id] < latencies[items[j].id]
		})
		if len(items) > perISP {
			items = items[:perISP]
		}
		selected = append(selected, items...)
	}
	return selected
}

// concurrentPingCandidates 以有界并发对候选做 TCP 拨号探测,返回 id->延迟。
// 限并发避免短时间发起过多连接压垮网络/设备。
func concurrentPingCandidates(ctx context.Context, candidates []domesticSpeedtestCandidate) map[string]time.Duration {
	latencies := make(map[string]time.Duration, len(candidates))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentCandidatePings)
	for _, item := range candidates {
		item := item
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			d := pingDomesticCandidate(ctx, item)
			mu.Lock()
			latencies[item.id] = d
			mu.Unlock()
		}()
	}
	wg.Wait()
	return latencies
}

func pingDomesticCandidate(ctx context.Context, candidate domesticSpeedtestCandidate) time.Duration {
	if candidate.host == "" {
		return 24 * time.Hour
	}
	port := candidate.port
	if port == "" {
		port = "8080"
	}
	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	startedAt := time.Now()
	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "tcp", net.JoinHostPort(candidate.host, port))
	if err != nil {
		return 24 * time.Hour
	}
	_ = conn.Close()
	return time.Since(startedAt)
}

func isChinaSpeedtestServer(server *speedtest.Server) bool {
	if server == nil {
		return false
	}
	if server.Country == "China" || server.Country == "中国" {
		return true
	}
	lat, latErr := strconv.ParseFloat(server.Lat, 64)
	lon, lonErr := strconv.ParseFloat(server.Lon, 64)
	if latErr == nil && lonErr == nil {
		return lat >= 18 && lat <= 54 && lon >= 73 && lon <= 135
	}
	return false
}
