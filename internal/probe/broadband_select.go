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
