package probe

import (
	"context"
	"encoding/csv"
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

const speedtestCNIDBaseURL = "https://raw.githubusercontent.com/spiritLHLS/speedtest.net-CN-ID/main"

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

func executeBroadbandSpeedTest(ctx context.Context, duration time.Duration, progress func(stage string, progress int, message string, partial BroadbandSpeedResult)) (BroadbandSpeedResult, bool) {
	if duration <= 0 {
		duration = 15 * time.Second
	}
	s_ptr := ctx.Value("service").(*Service)
	s_ptr.mu.RLock()
	domesticOnly := s_ptr.cfg.BroadbandDomesticOnly
	s_ptr.mu.RUnlock()

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

	report("starting", 2, "正在初始化测速引擎")
	stClient := speedtest.New()
	stClient.SetCaptureTime(duration)
	stClient.SetRateCaptureFrequency(250 * time.Millisecond)

	var server *speedtest.Server

	if domesticOnly {
		report("starting", 5, "正在检索国内优质运营商节点")
		server = selectDomesticSpeedtestServer(ctx, stClient, s_ptr)
		if server != nil {
			// 确保 server.Context 指向设置了回调的 stClient，
			// 否则 Download/Upload 回调不会触发（实时进度丢失）。
			server.Context = stClient
		}
		if server == nil {
			setResult(func(r *BroadbandSpeedResult) {
				r.Error = "未找到可用的国内 Speedtest 节点"
			})
			return currentResult(), false
		}
	} else {
		report("starting", 10, "正在寻找最近的响应节点")
		serverList, err := stClient.FetchServers()
		if err == nil && len(serverList) > 0 {
			targets, _ := serverList.FindServer([]int{})
			if len(targets) > 0 {
				server = targets[0]
			}
		}
	}

	if server == nil {
		setResult(func(r *BroadbandSpeedResult) {
			r.Error = "无法连接到测速服务器"
		})
		return currentResult(), false
	}

	setResult(func(r *BroadbandSpeedResult) {
		r.Provider = server.Sponsor
		r.ServerRegion = fmt.Sprintf("%s · %s", server.Name, server.Country)
	})
	report("latency", 15, fmt.Sprintf("已选节点：%s (%s)", server.Sponsor, server.Name))

	_ = server.PingTestContext(ctx, nil)
	latencyMS := int64(server.Latency.Milliseconds())
	jitterMS := int64(server.Jitter.Milliseconds())
	setResult(func(r *BroadbandSpeedResult) {
		r.LatencyMS = latencyMS
		r.JitterMS = jitterMS
	})
	report("latency", 25, fmt.Sprintf("延迟 %d ms · 抖动 %d ms", latencyMS, jitterMS))

	report("download", 30, "准备开始下载压测")
	downloadStart := time.Now()
	stClient.SetCallbackDownload(func(rate speedtest.ByteRate) {
		mbps := rate.Mbps()
		if mbps <= 0 {
			return
		}
		setResult(func(r *BroadbandSpeedResult) {
			r.DownloadMbps = mbps
		})
		pct := 30 + progressRange(time.Since(downloadStart), duration, 30)
		report("download", pct, fmt.Sprintf("下载测速中 %.2f Mbps", mbps))
	})

	err := server.DownloadTestContext(ctx)
	stClient.SetCallbackDownload(nil)
	if err != nil {
		setResult(func(r *BroadbandSpeedResult) {
			r.Error = "下载测试失败: " + err.Error()
		})
		return currentResult(), false
	}
	downloadMbps := server.DLSpeed.Mbps()
	setResult(func(r *BroadbandSpeedResult) {
		r.DownloadMbps = downloadMbps
	})
	report("download", 60, fmt.Sprintf("下载完成 %.1f Mbps", downloadMbps))

	report("upload", 65, "准备开始上传压测")
	uploadStart := time.Now()
	stClient.SetCallbackUpload(func(rate speedtest.ByteRate) {
		mbps := rate.Mbps()
		if mbps <= 0 {
			return
		}
		setResult(func(r *BroadbandSpeedResult) {
			r.UploadMbps = mbps
		})
		pct := 65 + progressRange(time.Since(uploadStart), duration, 30)
		report("upload", pct, fmt.Sprintf("上传测速中 %.2f Mbps", mbps))
	})

	err = server.UploadTestContext(ctx)
	stClient.SetCallbackUpload(nil)
	if err != nil {
		setResult(func(r *BroadbandSpeedResult) {
			r.Error = "上传测试失败: " + err.Error()
		})
		return currentResult(), false
	}
	uploadMbps := server.ULSpeed.Mbps()
	setResult(func(r *BroadbandSpeedResult) {
		r.UploadMbps = uploadMbps
	})
	report("upload", 95, fmt.Sprintf("上传完成 %.1f Mbps", uploadMbps))

	setResult(func(r *BroadbandSpeedResult) {
		r.Timestamp = localTimestamp()
	})
	report("finalizing", 100, "测速全部完成")

	return currentResult(), true
}

// selectDomesticSpeedtestServer 按优先级尝试多种策略选择国内测速节点：
//  1. Ookla API 关键词搜索 (search=China)
//  2. Ookla API 地理坐标搜索 (中国中心点)
//  3. spiritLHLS CSV 远程列表
//  4. 内置扩展兜底列表
//
// 任一策略找到可用节点即返回，不继续尝试后续策略。
func selectDomesticSpeedtestServer(ctx context.Context, stClient *speedtest.Speedtest, svc *Service) *speedtest.Server {
	preferredISP := detectPreferredDomesticISP(ctx, svc)
	logger.Info("broadband: preferred domestic isp=%q", preferredISP)

	// 策略 1: Ookla API 关键词搜索
	if s := selectDomesticViaOoklaKeyword(ctx, preferredISP); s != nil {
		return s
	}

	// 策略 2: Ookla API 坐标搜索
	if s := selectDomesticViaOoklaLocation(ctx, preferredISP); s != nil {
		return s
	}

	// 策略 3: spiritLHLS CSV 远程列表
	if s := selectDomesticViaCSV(ctx, stClient, preferredISP); s != nil {
		return s
	}

	// 策略 4: 内置兜底列表
	logger.Warn("broadband: all remote sources failed, using embedded fallback list")
	return selectDomesticFromCandidates(ctx, stClient, fallbackDomesticSpeedtestCandidates, preferredISP)
}

// selectDomesticViaOoklaKeyword 用 Ookla API 的 search=China 关键词搜索。
func selectDomesticViaOoklaKeyword(ctx context.Context, preferredISP string) *speedtest.Server {
	kwClient := speedtest.New()
	kwClient.NewUserConfig(&speedtest.UserConfig{Keyword: "China"})
	serverList, err := kwClient.FetchServerListContext(ctx)
	if err != nil {
		logger.Warn("broadband: ookla keyword search failed: %v", err)
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
	serverList, err := locClient.FetchServerListContext(ctx)
	if err != nil {
		logger.Warn("broadband: ookla location search failed: %v", err)
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

	var best *speedtest.Server
	for _, candidate := range nearestDomesticSpeedtestCandidates(ctx, candidates, 3) {
		if ctx.Err() != nil {
			return nil
		}
		serverCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		s, err := stClient.FetchServerByIDContext(serverCtx, candidate.id)
		if err != nil || s == nil || !isChinaSpeedtestServer(s) {
			if err != nil {
				logger.Warn("broadband: fetch server by id failed id=%s isp=%s city=%s err=%v", candidate.id, candidate.isp, candidate.city, err)
			}
			cancel()
			continue
		}
		if err := s.PingTestContext(serverCtx, nil); err != nil {
			logger.Warn("broadband: ping candidate failed id=%s isp=%s city=%s sponsor=%s err=%v", candidate.id, candidate.isp, candidate.city, s.Sponsor, err)
			cancel()
			continue
		}
		cancel()
		if s.Latency <= 0 {
			logger.Warn("broadband: candidate latency invalid id=%s isp=%s city=%s sponsor=%s", candidate.id, candidate.isp, candidate.city, s.Sponsor)
			continue
		}
		logger.Info("broadband: candidate ok id=%s isp=%s city=%s sponsor=%s latency=%s", candidate.id, candidate.isp, candidate.city, s.Sponsor, s.Latency.Round(time.Millisecond))
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
	for _, base := range speedtestCNIDMirrors {
		reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, base+"/"+source.file, nil)
		if err != nil {
			cancel()
			continue
		}
		resp, err := domesticSpeedtestHTTPClient.Do(req)
		if err != nil || resp == nil {
			if err != nil {
				logger.Warn("broadband: fetch csv failed source=%s base=%s err=%v", source.file, base, err)
			}
			cancel()
			continue
		}
		if resp.StatusCode >= 400 {
			logger.Warn("broadband: fetch csv bad status source=%s base=%s status=%d", source.file, base, resp.StatusCode)
			_ = resp.Body.Close()
			cancel()
			continue
		}

		reader := csv.NewReader(resp.Body)
		reader.FieldsPerRecord = -1
		records, err := reader.ReadAll()
		_ = resp.Body.Close()
		cancel()
		if err != nil {
			logger.Warn("broadband: parse csv failed source=%s base=%s err=%v", source.file, base, err)
			continue
		}

		var out []domesticSpeedtestCandidate
		for _, record := range records {
			if len(record) < 8 || record[0] == "id" || record[2] != "China" {
				continue
			}
			out = append(out, domesticSpeedtestCandidate{
				id:       record[0],
				isp:      source.isp,
				city:     record[3],
				host:     record[5],
				port:     record[6],
				supplier: record[7],
			})
		}
		if len(out) > 0 {
			logger.Info("broadband: fetched csv source=%s base=%s count=%d", source.file, base, len(out))
			return out
		}
	}
	logger.Warn("broadband: no valid candidates from source=%s", source.file)
	return nil
}

func nearestDomesticSpeedtestCandidates(ctx context.Context, candidates []domesticSpeedtestCandidate, perISP int) []domesticSpeedtestCandidate {
	if perISP <= 0 {
		perISP = 2
	}

	grouped := make(map[string][]domesticSpeedtestCandidate)
	for _, candidate := range candidates {
		grouped[candidate.isp] = append(grouped[candidate.isp], candidate)
	}

	var selected []domesticSpeedtestCandidate
	for _, source := range domesticSpeedtestSources {
		items := grouped[source.isp]
		latencies := make(map[string]time.Duration, len(items))
		for _, item := range items {
			latencies[item.id] = pingDomesticCandidate(ctx, item)
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
