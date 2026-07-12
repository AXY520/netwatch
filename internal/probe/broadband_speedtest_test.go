package probe

import (
	"testing"
	"time"

	"github.com/showwin/speedtest-go/speedtest"
)

func TestValidSpeedMbpsFallsBackWhenPrimaryInvalid(t *testing.T) {
	tests := []struct {
		name     string
		primary  float64
		fallback float64
		want     float64
	}{
		{name: "primary zero", primary: 0, fallback: 321.5, want: 321.5},
		{name: "primary negative", primary: -1, fallback: 123.4, want: 123.4},
		{name: "primary valid", primary: 456.7, fallback: 123.4, want: 456.7},
		{name: "both invalid", primary: 0, fallback: 0, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validSpeedMbps(tt.primary, tt.fallback); got != tt.want {
				t.Fatalf("validSpeedMbps(%v, %v) = %v, want %v", tt.primary, tt.fallback, got, tt.want)
			}
		})
	}
}

// TestParseDomesticCSV 覆盖表头映射的关键场景:标准表头、列乱序、表头不可识别回退、
// 无表头旧格式、以及 country 过滤与表头行跳过。这是对上游 GitHub 列表格式变更的回归保护。
func TestParseDomesticCSV(t *testing.T) {
	t.Run("header mapped, filters non-China", func(t *testing.T) {
		records := [][]string{
			{"id", "name", "country", "city", "x", "host", "port", "sponsor"},
			{"100", "N1", "China", "上海", "y", "h1.com", "8080", "Unicom"},
			{"200", "N2", "USA", "NY", "y", "h2.com", "8080", "ATT"},
			{"300", "N3", "China", "北京", "y", "h3.com", "8080", "Telecom"},
		}
		out := parseDomesticCSV(records, "联通")
		if len(out) != 2 {
			t.Fatalf("want 2 China candidates, got %d (%+v)", len(out), out)
		}
		if out[0].id != "100" || out[0].city != "上海" || out[0].host != "h1.com" || out[0].port != "8080" || out[0].isp != "联通" {
			t.Fatalf("unexpected first candidate: %+v", out[0])
		}
	})

	t.Run("reordered columns still mapped by name", func(t *testing.T) {
		records := [][]string{
			{"country", "id", "host", "port", "city", "sponsor"},
			{"China", "100", "h1.com", "8080", "上海", "Unicom"},
		}
		out := parseDomesticCSV(records, "电信")
		if len(out) != 1 {
			t.Fatalf("want 1, got %d", len(out))
		}
		if out[0].id != "100" || out[0].host != "h1.com" || out[0].port != "8080" || out[0].city != "上海" {
			t.Fatalf("reordered mapping wrong: %+v", out[0])
		}
	})

	t.Run("unrecognized header falls back to default indices and skips header row", func(t *testing.T) {
		records := [][]string{
			{"id", "foo", "bar", "baz", "x", "y", "z", "w"},
			{"100", "N1", "China", "上海", "x", "h1.com", "8080", "Unicom"},
		}
		out := parseDomesticCSV(records, "联通")
		if len(out) != 1 {
			t.Fatalf("want 1 (header skipped), got %d (%+v)", len(out), out)
		}
		if out[0].id != "100" || out[0].host != "h1.com" {
			t.Fatalf("default-index fallback wrong: %+v", out[0])
		}
	})

	t.Run("empty records", func(t *testing.T) {
		if out := parseDomesticCSV(nil, "联通"); out != nil {
			t.Fatalf("want nil for empty, got %+v", out)
		}
	})
}

// TestSteadyStateMbps 验证稳态采样:丢弃 grace 后以后半段中位数/修剪均值估计。
func TestSteadyStateMbps(t *testing.T) {
	base := time.Unix(1700000000, 0)

	t.Run("discards grace warmup and tracks steady region", func(t *testing.T) {
		samples := []speedSample{
			{at: base.Add(0), mbps: 100},               // grace 内(慢启动)
			{at: base.Add(1 * time.Second), mbps: 200}, // grace 内
			{at: base.Add(2 * time.Second), mbps: 900}, // 稳态
			{at: base.Add(3 * time.Second), mbps: 910},
			{at: base.Add(4 * time.Second), mbps: 950},
			{at: base.Add(5 * time.Second), mbps: 1000},
		}
		got := steadyStateMbps(samples, base, 2*time.Second)
		// 稳态应落在 900~1000 之间,且明显高于慢启动期。
		if got < 900 || got > 1000 {
			t.Fatalf("want steady in [900,1000], got %v", got)
		}
	})

	t.Run("falls back to all samples when grace discards everything", func(t *testing.T) {
		samples := []speedSample{
			{at: base.Add(0), mbps: 100},
			{at: base.Add(1 * time.Second), mbps: 200},
		}
		got := steadyStateMbps(samples, base, 10*time.Second)
		if got < 100 || got > 200 {
			t.Fatalf("want fallback in [100,200], got %v", got)
		}
	})

	t.Run("empty samples", func(t *testing.T) {
		if got := steadyStateMbps(nil, base, time.Second); got != 0 {
			t.Fatalf("want 0 for empty, got %v", got)
		}
	})
}

func TestPickAccurateMbps(t *testing.T) {
	if got := pickAccurateMbps(0, 500); got != 500 {
		t.Fatalf("want ewma fallback 500, got %v", got)
	}
	if got := pickAccurateMbps(480, 0); got != 480 {
		t.Fatalf("want steady 480, got %v", got)
	}
	// close values -> average
	if got := pickAccurateMbps(100, 110); got < 104 || got > 106 {
		t.Fatalf("want ~105 average, got %v", got)
	}
	// large gap -> prefer steady
	if got := pickAccurateMbps(900, 200); got != 900 {
		t.Fatalf("want steady 900 when gap large, got %v", got)
	}
}

func TestAdaptBroadbandStreams(t *testing.T) {
	if got := adaptBroadbandStreams(8, 10); got != 8 {
		t.Fatalf("low rtt keep base, got %d", got)
	}
	if got := adaptBroadbandStreams(8, 50); got < 12 {
		t.Fatalf("mid rtt should raise streams, got %d", got)
	}
	if got := adaptBroadbandStreams(8, 100); got < 16 {
		t.Fatalf("high rtt should raise streams, got %d", got)
	}
}

// TestBroadbandNodeCacheRoundTrip 验证节点缓存的落盘读写与边界。
func TestBroadbandNodeCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok := loadBroadbandNodeCache(dir); ok {
		t.Fatal("expected no cache initially")
	}

	c := broadbandNodeCache{ServerID: "123", Host: "h.com", Source: "测试", DownloadMbps: 500, CachedAt: localTimestamp()}
	saveBroadbandNodeCache(dir, c)
	got, ok := loadBroadbandNodeCache(dir)
	if !ok {
		t.Fatal("expected cache after save")
	}
	if got.ServerID != "123" || got.Host != "h.com" || got.DownloadMbps != 500 {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}

	// 空 ServerID 不应写盘
	dir2 := t.TempDir()
	saveBroadbandNodeCache(dir2, broadbandNodeCache{ServerID: ""})
	if _, ok := loadBroadbandNodeCache(dir2); ok {
		t.Fatal("empty ServerID should not be persisted")
	}
}

// TestMatchServerISP 验证运营商匹配(中英文 sponsor/name)。
func TestMatchServerISP(t *testing.T) {
	tests := []struct {
		sponsor string
		isp     string
		want    bool
	}{
		{"China Unicom", "联通", true},
		{"中国联通", "联通", true},
		{"China Telecom", "电信", true},
		{"China Mobile", "移动", true},
		{"CMCC", "移动", true},
		{"China Unicom", "电信", false},
		{"Some Random ISP", "联通", false},
	}
	for _, tt := range tests {
		s := &speedtest.Server{Sponsor: tt.sponsor}
		if got := matchServerISP(s, tt.isp); got != tt.want {
			t.Errorf("matchServerISP(%q, %q) = %v, want %v", tt.sponsor, tt.isp, got, tt.want)
		}
	}
}
