package probe

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/showwin/speedtest-go/speedtest"
)

// 默认跳过，避免离线 CI 失败。本地验证：LIVE_NETWORK=1 go test ./internal/probe -run TestPickBestFromFallbackLive -v
func TestPickBestFromFallbackLive(t *testing.T) {
	if os.Getenv("LIVE_NETWORK") != "1" {
		t.Skip("set LIVE_NETWORK=1 to run live node selection")
	}
	st := speedtest.New()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := []domesticSpeedtestCandidate{
		{id: "24447", isp: "联通", city: "上海", supplier: "China Unicom 5G", host: "mobile.shunicomtest.com.prod.hosts.ooklaserver.net", port: "8080"},
		{id: "43752", isp: "联通", city: "北京", supplier: "BJ Unicom", host: "beijing.unicomtest.com", port: "8080"},
	}
	best := pickBestFromDomesticCandidates(ctx, st, pool)
	if best == nil {
		t.Fatal("expected at least one reachable domestic node via host CustomServer")
	}
	t.Logf("selected id=%s sponsor=%s host=%s latency=%s", best.ID, best.Sponsor, best.Host, best.Latency)
}
