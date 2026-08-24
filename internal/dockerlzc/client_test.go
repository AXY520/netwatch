package dockerlzc

import "testing"

func TestBuildBridgeMapUsesNetworkSpecificRuntime(t *testing.T) {
	networks := []networkSummary{
		{Name: "app_default_old", Labels: map[string]string{"com.docker.compose.project": "app"}, Options: map[string]string{"com.docker.network.bridge.name": "lzc-br-old"}},
		{Name: "app_default", Labels: map[string]string{"com.docker.compose.project": "app"}, Options: map[string]string{"com.docker.network.bridge.name": "lzc-br-current"}},
	}
	containers := []ContainerRuntimeInfo{
		{AppID: "cloud.lazycat.app.test", Project: "app", State: "running", Networks: []string{"app_default"}},
		{AppID: "cloud.lazycat.app.test", Project: "app", State: "exited", Networks: []string{"app_default"}},
	}

	got := buildBridgeMapFromInventory(networks, containers)
	old := got["lzc-br-old"]
	if old.AppID != "cloud.lazycat.app.test" || old.ContainerCount != 0 || old.RunningCount != 0 {
		t.Fatalf("old bridge runtime = %+v", old)
	}
	current := got["lzc-br-current"]
	if current.ContainerCount != 2 || current.RunningCount != 1 {
		t.Fatalf("current bridge runtime = %+v", current)
	}
}

func TestBuildBridgeMapUsesPrimaryAppContainerStart(t *testing.T) {
	networks := []networkSummary{
		{Name: "app_default", Labels: map[string]string{"com.docker.compose.project": "app"}, Options: map[string]string{"com.docker.network.bridge.name": "lzc-br-current"}},
	}
	containers := []ContainerRuntimeInfo{
		{Name: "app-worker-1", AppID: "cloud.lazycat.app.test", Project: "app", State: "running", StartedAt: 100, Networks: []string{"app_default"}},
		{Name: "app-app-1", AppID: "cloud.lazycat.app.test", Project: "app", State: "running", StartedAt: 200, Networks: []string{"app_default"}},
	}
	got := buildBridgeMapFromInventory(networks, containers)["lzc-br-current"]
	if got.CreatedAt != 200 {
		t.Fatalf("created at = %d, want primary app start 200: %+v", got.CreatedAt, got)
	}

	// Docker response ordering must not affect which container supplies the
	// application lifecycle timestamp.
	containers[0], containers[1] = containers[1], containers[0]
	got = buildBridgeMapFromInventory(networks, containers)["lzc-br-current"]
	if got.CreatedAt != 200 {
		t.Fatalf("reordered created at = %d, want primary app start 200: %+v", got.CreatedAt, got)
	}
}

func TestPrimaryAppContainerPriority(t *testing.T) {
	for name, want := range map[string]int{
		"cloud-lazycat-app-netwatch-app-1":    3,
		"cloud-lazycat-app-netwatch-worker-1": 1,
		"cloud-lazycat.app.netwatch.sidecar":  1,
		"worker-1":                            0,
	} {
		if got := primaryAppContainerPriority(name); got != want {
			t.Errorf("primaryAppContainerPriority(%q) = %d, want %d", name, got, want)
		}
	}
}
