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
// 远程列表近年大幅缩水，始终与内置兜底合并，保证有 host 可直连。
func selectDomesticViaCSV(ctx context.Context, stClient *speedtest.Speedtest, preferredISP string) *speedtest.Server {
	candidates := mergeDomesticSpeedtestCandidates(fetchDomesticSpeedtestCandidates(ctx), fallbackDomesticSpeedtestCandidates)
	if len(candidates) == 0 {
		logger.Warn("broadband: remote CN speedtest CSV unavailable")
		return nil
	}
	logger.Info("broadband: CSV+fallback candidates=%d", len(candidates))
	return selectDomesticFromCandidates(ctx, stClient, candidates, preferredISP)
}

// selectDomesticFromCandidates 从候选列表中选最优节点：先按 ISP 筛选，再按延迟排序。
// 候选已是国内列表时，优先用 host 构造 Server（不依赖 Ookla API）；API 仅作补全。
func selectDomesticFromCandidates(ctx context.Context, stClient *speedtest.Speedtest, candidates []domesticSpeedtestCandidate, preferredISP string) *speedtest.Server {
	if len(candidates) == 0 {
		return nil
	}
	pool := candidates
	if preferredISP != "" {
		preferred := filterDomesticSpeedtestCandidatesByISP(candidates, preferredISP)
		if len(preferred) > 0 {
			logger.Info("broadband: ISP-filtered candidates isp=%s count=%d", preferredISP, len(preferred))
			pool = preferred
		} else {
			logger.Warn("broadband: no ISP-matched candidates for isp=%s, using all %d", preferredISP, len(candidates))
		}
	}

	best := pickBestFromDomesticCandidates(ctx, stClient, pool)
	// 同运营商全挂时，跨网兜底，避免「有节点却硬失败」。
	if best == nil && preferredISP != "" && len(pool) != len(candidates) {
		logger.Warn("broadband: preferred ISP=%s candidates all failed, retrying all carriers", preferredISP)
		emitStep(ctx, "node_selection", "info", "同运营商节点不可用，尝试其他运营商节点")
		best = pickBestFromDomesticCandidates(ctx, stClient, candidates)
	}
	return best
}

func pickBestFromDomesticCandidates(ctx context.Context, stClient *speedtest.Speedtest, candidates []domesticSpeedtestCandidate) *speedtest.Server {
	// 有 host 的优先，且多测几个；TCP 预筛后再 HTTP ping。
	candidateList := nearestDomesticSpeedtestCandidates(ctx, candidates, 4)
	// 确保带 host 的条目不会被 TCP 失败排序挤掉后完全不测：补进最多 6 个有 host 的
	candidateList = ensureHostCandidates(candidateList, candidates, 6)
	emitStep(ctx, "node_selection", "info", fmt.Sprintf("逐一测试 %d 个候选节点延迟", len(candidateList)))

	var best *speedtest.Server
	for _, candidate := range candidateList {
		if ctx.Err() != nil {
			return best
		}
		// 单节点 ping 预算收紧：串行测多个节点时避免整体卡死。
		serverCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		s, err := serverFromDomesticCandidate(serverCtx, stClient, candidate)
		if err != nil || s == nil {
			if err != nil && !isCanceledErr(err) {
				logger.Warn("broadband: build server failed id=%s isp=%s city=%s host=%s err=%v", candidate.id, candidate.isp, candidate.city, candidate.host, err)
			}
			cancel()
			continue
		}
		if err := s.PingTestContext(serverCtx, nil); err != nil {
			if !isCanceledErr(err) {
				logger.Warn("broadband: ping candidate failed id=%s isp=%s city=%s host=%s err=%v", candidate.id, candidate.isp, candidate.city, candidate.host, err)
			}
			cancel()
			continue
		}
		cancel()
		if s.Latency <= 0 {
			logger.Warn("broadband: candidate latency invalid id=%s isp=%s city=%s", candidate.id, candidate.isp, candidate.city)
			continue
		}
		logger.Info("broadband: candidate ok id=%s isp=%s city=%s sponsor=%s latency=%s host=%s", candidate.id, candidate.isp, candidate.city, s.Sponsor, s.Latency.Round(time.Millisecond), s.Host)
		emitStep(ctx, "node_selection", "ok", fmt.Sprintf("候选 %s%s 延迟 %d ms", candidate.city, candidate.isp, s.Latency.Milliseconds()))
		if best == nil || s.Latency < best.Latency {
			best = s
		}
		// 已有足够好的节点就收工，不必把列表里的死节点全测完。
		if best.Latency > 0 && best.Latency <= 80*time.Millisecond {
			break
		}
	}
	if best != nil {
		logger.Info("broadband: selected server id=%s sponsor=%s name=%s latency=%s", best.ID, best.Sponsor, best.Name, best.Latency.Round(time.Millisecond))
	}
	return best
}

// serverFromDomesticCandidate 优先 host 直连构造，避免依赖 Ookla ios-config API。
// 国内列表本身已校验为中国节点，不再用 isChinaSpeedtestServer 二次误杀（API 常返回空 Country）。
func serverFromDomesticCandidate(ctx context.Context, stClient *speedtest.Speedtest, candidate domesticSpeedtestCandidate) (*speedtest.Server, error) {
	if candidate.host != "" {
		if s, err := customServerFromHost(stClient, candidate); err == nil && s != nil {
			return s, nil
		} else if err != nil && !isCanceledErr(err) {
			logger.Warn("broadband: custom server from host failed id=%s host=%s err=%v", candidate.id, candidate.host, err)
		}
	}

	s, err := stClient.FetchServerByIDContext(ctx, candidate.id)
	if err != nil {
		// API 挂了但有 host：再试一次 host 构造（上面已试过则这里也无 host）
		if candidate.host != "" {
			return customServerFromHost(stClient, candidate)
		}
		return nil, err
	}
	if s == nil {
		if candidate.host != "" {
			return customServerFromHost(stClient, candidate)
		}
		return nil, fmt.Errorf("server id %s not found", candidate.id)
	}
	// 补全元数据：ios-config 经常缺 Country/Host
	if s.Country == "" {
		s.Country = "China"
	}
	if s.Sponsor == "" && candidate.supplier != "" {
		s.Sponsor = candidate.supplier
	}
	if s.Name == "" && candidate.city != "" {
		s.Name = candidate.city
	}
	if s.Host == "" {
		if candidate.host != "" {
			port := candidate.port
			if port == "" {
				port = "8080"
			}
			if strings.Contains(candidate.host, ":") {
				s.Host = candidate.host
			} else {
				s.Host = net.JoinHostPort(candidate.host, port)
			}
		} else if s.URL != "" {
			if u, uerr := parseHTTPHost(s.URL); uerr == nil {
				s.Host = u
			}
		}
	}
	if s.Context == nil {
		s.Context = stClient
	}
	return s, nil
}

func customServerFromHost(stClient *speedtest.Speedtest, candidate domesticSpeedtestCandidate) (*speedtest.Server, error) {
	baseURL := candidateHTTPBase(candidate)
	if baseURL == "" {
		return nil, fmt.Errorf("empty host for id=%s", candidate.id)
	}
	s, err := stClient.CustomServer(baseURL)
	if err != nil {
		return nil, err
	}
	if candidate.id != "" {
		s.ID = candidate.id
	}
	s.Country = "China"
	if candidate.city != "" {
		s.Name = candidate.city
	}
	if candidate.supplier != "" {
		s.Sponsor = candidate.supplier
	}
	s.Context = stClient
	return s, nil
}

func candidateHTTPBase(candidate domesticSpeedtestCandidate) string {
	host := strings.TrimSpace(candidate.host)
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return host
	}
	port := strings.TrimSpace(candidate.port)
	if port == "" {
		port = "8080"
	}
	if strings.Contains(host, ":") {
		// already host:port
		return "http://" + host
	}
	return "http://" + net.JoinHostPort(host, port)
}

func parseHTTPHost(raw string) (string, error) {
	// local tiny helper without importing net/url at call sites repeatedly
	if raw == "" {
		return "", fmt.Errorf("empty url")
	}
	// speedtest URL like http://host:8080/speedtest/upload.php
	rest := raw
	for _, p := range []string{"https://", "http://"} {
		if strings.HasPrefix(rest, p) {
			rest = rest[len(p):]
			break
		}
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" {
		return "", fmt.Errorf("no host in %q", raw)
	}
	return rest, nil
}

func mergeDomesticSpeedtestCandidates(lists ...[]domesticSpeedtestCandidate) []domesticSpeedtestCandidate {
	seen := make(map[string]struct{})
	var out []domesticSpeedtestCandidate
	for _, list := range lists {
		for _, item := range list {
			key := item.id
			if key == "" {
				key = item.host + "|" + item.port
			}
			if key == "" || key == "|" {
				continue
			}
			if _, ok := seen[key]; ok {
				// 后写不覆盖；但若已有条目无 host、新条目有 host，则升级
				if item.host == "" {
					continue
				}
				for i := range out {
					idKey := out[i].id
					if idKey == "" {
						idKey = out[i].host + "|" + out[i].port
					}
					if idKey == key && out[i].host == "" && item.host != "" {
						out[i] = item
					}
				}
				continue
			}
			seen[key] = struct{}{}
			out = append(out, item)
		}
	}
	return out
}

// ensureHostCandidates 保证最终探测列表里有足够带 host 的节点（可脱离 Ookla API）。
func ensureHostCandidates(selected, all []domesticSpeedtestCandidate, maxN int) []domesticSpeedtestCandidate {
	if maxN <= 0 {
		maxN = 6
	}
	seen := make(map[string]struct{}, len(selected))
	out := make([]domesticSpeedtestCandidate, 0, maxN)
	for _, c := range selected {
		key := c.id
		if key == "" {
			key = c.host
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
	}
	if len(out) >= maxN {
		return out[:maxN]
	}
	// 补齐有 host 的
	for _, c := range all {
		if c.host == "" {
			continue
		}
		key := c.id
		if key == "" {
			key = c.host
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
		if len(out) >= maxN {
			break
		}
	}
	return out
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
		// 丢掉 TCP 完全不可达的（探测返回 24h 哨兵值）
		for _, item := range items {
			if latencies[item.id] >= 24*time.Hour {
				continue
			}
			selected = append(selected, item)
		}
	}
	// 若 TCP 预筛后为空（host 缺失导致全员 24h），回退原 perISP 截断结果，交给后续 host/API 再试。
	if len(selected) == 0 {
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
