package probe

import (
	"testing"

	"netwatch/internal/dockerlzc"
)

func TestAppNetworkRateSkipsDiscontinuity(t *testing.T) {
	points := []AppTrafficPoint{
		{Timestamp: "2026-08-04 10:00:00", UploadBytes: 100, DownloadBytes: 200},
		{Timestamp: "2026-08-04 10:00:10", UploadBytes: 200, DownloadBytes: 400, Discontinuity: true},
		{Timestamp: "2026-08-04 10:00:20", UploadBytes: 500, DownloadBytes: 800},
		{Timestamp: "2026-08-04 10:00:30", UploadBytes: 700, DownloadBytes: 1100},
	}
	rate := appNetworkRate(points)
	if rate.UploadBPS != 20 || rate.DownloadBPS != 30 || rate.TotalBPS != 50 {
		t.Fatalf("unexpected rate: %+v", rate)
	}
}

func TestFilterAppPortsUsesContainerIdentity(t *testing.T) {
	runtime := []dockerlzc.ContainerRuntimeInfo{{ID: "1234567890abcdef"}}
	ports := []HostPortEntry{{Port: 80, Container: &HostPortContainer{ID: "1234567890ab"}}, {Port: 81, Container: &HostPortContainer{AppID: "other"}}}
	got := filterAppPorts(ports, "app", "project", runtime)
	if len(got) != 1 || got[0].Port != 80 {
		t.Fatalf("unexpected ports: %+v", got)
	}
}
