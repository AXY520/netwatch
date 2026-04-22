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

var speedtestCNIDMirrors = []string{
	speedtestCNIDBaseURL,
	"https://cdn0.spiritlhl.top/" + speedtestCNIDBaseURL,
	"http://cdn1.spiritlhl.net/" + speedtestCNIDBaseURL,
	"http://cdn2.spiritlhl.net/" + speedtestCNIDBaseURL,
	"http://cdn3.spiritlhl.net/" + speedtestCNIDBaseURL,
	"http://cdn4.spiritlhl.net/" + speedtestCNIDBaseURL,
}

var domesticSpeedtestSources = []domesticSpeedtestSource{
	{file: "CN_Unicom.csv", isp: "联通"},
	{file: "CN_Telecom.csv", isp: "电信"},
	{file: "CN_Mobile.csv", isp: "移动"},
}

// 兜底列表来自 spiritLHLS/speedtest.net-CN-ID 当前三网 CSV 的国内节点。
var fallbackDomesticSpeedtestCandidates = []domesticSpeedtestCandidate{
	{id: "24447", isp: "联通", city: "上海5G", supplier: "China Unicom 5G"},
	{id: "43752", isp: "联通", city: "北京", supplier: "BJ Unicom"},
	{id: "5396", isp: "电信", city: "Suzhou5G", supplier: "China Telecom JiangSu 5G"},
	{id: "36663", isp: "电信", city: "Zhenjiang5G", supplier: "China Telecom JiangSu 5G"},
	{id: "59387", isp: "电信", city: "浙江", supplier: "浙江电信"},
	{id: "16204", isp: "移动", city: "Suzhou", supplier: "JSQY - Suzhou"},
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
	// 获取 Service 实例以读取配置
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

	// 延迟采样
	_ = server.PingTestContext(ctx, nil)
	latencyMS := int64(server.Latency.Milliseconds())
	jitterMS := int64(server.Jitter.Milliseconds())
	setResult(func(r *BroadbandSpeedResult) {
		r.LatencyMS = latencyMS
		r.JitterMS = jitterMS
	})
	report("latency", 25, fmt.Sprintf("延迟 %d ms · 抖动 %d ms", latencyMS, jitterMS))

	// 下载测速（由 speedtest-go 的实时采样回调上报）
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

	// 上传测速（由 speedtest-go 的实时采样回调上报）
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

func selectDomesticSpeedtestServer(ctx context.Context, stClient *speedtest.Speedtest, svc *Service) *speedtest.Server {
	candidates := fetchDomesticSpeedtestCandidates(ctx)
	if len(candidates) == 0 {
		logger.Warn("broadband: remote CN speedtest CSV unavailable, fallback to embedded candidates")
		candidates = fallbackDomesticSpeedtestCandidates
	}

	preferredISP := detectPreferredDomesticISP(ctx, svc)
	logger.Info("broadband: preferred domestic isp=%q candidates=%d", preferredISP, len(candidates))
	if preferredISP != "" {
		preferred := filterDomesticSpeedtestCandidatesByISP(candidates, preferredISP)
		if len(preferred) > 0 {
			logger.Info("broadband: using ISP-matched candidates isp=%s count=%d", preferredISP, len(preferred))
			candidates = preferred
		} else {
			logger.Warn("broadband: no ISP-matched domestic candidates for isp=%s, fallback to all domestic candidates", preferredISP)
		}
	}

	var best *speedtest.Server
	for _, candidate := range nearestDomesticSpeedtestCandidates(ctx, candidates, 2) {
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
	} else {
		logger.Warn("broadband: no domestic speedtest candidate selected")
	}
	return best
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
