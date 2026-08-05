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
