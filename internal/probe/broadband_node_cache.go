package probe

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"netwatch/internal/logger"

	"github.com/showwin/speedtest-go/speedtest"
)

// broadbandNodeCacheTTL 是缓存节点的有效期。超过则重新走完整发现流程,
// 以适配 GitHub 节点列表的不定期更新。
const broadbandNodeCacheTTL = 24 * time.Hour

// broadbandNodeCache 缓存上次成功测速选用的节点,用于下次快速复用、
// 跳过整个节点发现流程,并在外部节点源(GitHub/Ookla)不可用时兜底。
type broadbandNodeCache struct {
	ServerID     string  `json:"server_id"`
	Host         string  `json:"host,omitempty"`
	Source       string  `json:"source,omitempty"`
	DownloadMbps float64 `json:"download_mbps,omitempty"`
	CachedAt     string  `json:"cached_at"`
}

func broadbandNodeCachePath(dataDir string) string {
	return filepath.Join(dataDir, "broadband_node_cache.json")
}

func loadBroadbandNodeCache(dataDir string) (broadbandNodeCache, bool) {
	var c broadbandNodeCache
	body, err := os.ReadFile(broadbandNodeCachePath(dataDir))
	if err != nil {
		return c, false
	}
	if err := json.Unmarshal(body, &c); err != nil {
		return c, false
	}
	return c, true
}

func saveBroadbandNodeCache(dataDir string, c broadbandNodeCache) {
	if dataDir == "" || c.ServerID == "" {
		return
	}
	if err := writeJSONFile(broadbandNodeCachePath(dataDir), c, true); err != nil {
		logger.Warn("broadband: save node cache failed: %v", err)
	}
}

// tryLoadCachedSpeedtestServer 尝试复用缓存节点:未过期、可用 ID 取回、且 ping 可达时返回,
// 否则返回 nil 让调用方走完整发现流程。命中可将选节点从数十秒降到约 1 秒,
// 且在外部节点源不可用时仍能完成测速。
func tryLoadCachedSpeedtestServer(ctx context.Context, stClient *speedtest.Speedtest, dataDir string) *selectedSpeedtestServer {
	cache, ok := loadBroadbandNodeCache(dataDir)
	if !ok || (cache.ServerID == "" && cache.Host == "") {
		return nil
	}
	if cachedAt, err := time.ParseInLocation(time.DateTime, cache.CachedAt, time.Local); err == nil {
		if time.Since(cachedAt) > broadbandNodeCacheTTL {
			logger.Info("broadband: node cache expired (cached_at=%s)", cache.CachedAt)
			return nil
		}
	}
	serverCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// 优先 host 直连（与正式测速路径一致），避免 Ookla API 空 Country / 不可达。
	var s *speedtest.Server
	if cache.Host != "" {
		cand := domesticSpeedtestCandidate{id: cache.ServerID, host: cache.Host, port: "8080", city: "缓存", supplier: cache.Source}
		if built, err := serverFromDomesticCandidate(serverCtx, stClient, cand); err == nil {
			s = built
		}
	}
	if s == nil && cache.ServerID != "" {
		if built, err := stClient.FetchServerByIDContext(serverCtx, cache.ServerID); err == nil && built != nil {
			if built.Country == "" {
				built.Country = "China"
			}
			s = built
		}
	}
	if s == nil {
		logger.Info("broadband: cached node unavailable, falling back to discovery")
		return nil
	}
	if err := s.PingTestContext(serverCtx, nil); err != nil || s.Latency <= 0 {
		logger.Info("broadband: cached node ping failed, falling back to discovery")
		return nil
	}
	// 缓存节点若延迟偏高（跨省），放弃缓存，强制重新择优，避免长期锁在远点。
	if s.Latency > 50*time.Millisecond {
		logger.Info("broadband: cached node latency %s too high, rediscovering", s.Latency.Round(time.Millisecond))
		return nil
	}
	source := cache.Source
	if source == "" {
		source = "缓存节点"
	}
	logger.Info("broadband: reusing cached node id=%s sponsor=%s latency=%s host=%s", s.ID, s.Sponsor, s.Latency.Round(time.Millisecond), s.Host)
	return &selectedSpeedtestServer{server: s, source: source}
}
