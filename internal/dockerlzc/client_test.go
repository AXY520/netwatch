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

func TestContainerRuntimeNetworkEndpointsPreserveLocalAddressMetadata(t *testing.T) {
	names, endpoints := containerRuntimeNetworkEndpoints(map[string]containerNetworkEndpoint{
		"z_default": {
			IPAddress:         "172.28.4.133",
			GlobalIPv6Address: "fd12:3456::133",
			Aliases:           []string{"postgres"},
			DNSNames:          []string{"postgres.cloud.lazycat.app.ai.lzcapp"},
		},
		"a_default": {IPAddress: "172.29.0.2"},
	})
	if len(names) != 2 || names[0] != "a_default" || names[1] != "z_default" {
		t.Fatalf("network names=%v", names)
	}
	if got := endpoints[1]; got.Network != "z_default" || got.IPv4 != "172.28.4.133" || got.IPv6 != "fd12:3456::133" || len(got.DNSNames) != 1 {
		t.Fatalf("runtime endpoint=%+v", got)
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

func TestAssignContainerInstanceIdentitiesSeparatesLazycatUsers(t *testing.T) {
	appID := "cloud.lazycat.app.downloader"
	containers := []ContainerRuntimeInfo{
		{Name: "cloudlazycatappdownloader-app-1", AppID: appID, UserID: "axy", Project: "cloudlazycatappdownloader"},
		{Name: "cloudlazycatappdownloader-worker-1", AppID: appID, Project: "cloudlazycatappdownloader"},
		{Name: "cloudlazycatappdownloader1-app-1", AppID: appID, UserID: "damn", Project: "cloudlazycatappdownloader1"},
	}
	got := assignContainerInstanceIdentities(containers)
	if got[0].InstanceID != appID+"@user:axy" || got[1].InstanceID != got[0].InstanceID || got[1].UserID != "axy" {
		t.Fatalf("first user identity was not propagated to sidecars: %+v", got[:2])
	}
	if got[2].InstanceID != appID+"@user:damn" {
		t.Fatalf("second user identity = %+v", got[2])
	}
	for _, container := range got {
		if !container.MultiInstance {
			t.Fatalf("container was not marked multi-instance: %+v", container)
		}
	}
}

func TestBuildBridgeMapKeepsMultiInstanceProjectsSeparate(t *testing.T) {
	appID := "cloud.lazycat.app.downloader"
	networks := []networkSummary{
		{Name: "downloader_default", Labels: map[string]string{"com.docker.compose.project": "downloader"}, Options: map[string]string{"com.docker.network.bridge.name": "lzc-br-one"}},
		{Name: "downloader1_default", Labels: map[string]string{"com.docker.compose.project": "downloader1"}, Options: map[string]string{"com.docker.network.bridge.name": "lzc-br-two"}},
	}
	containers := []ContainerRuntimeInfo{
		{Name: "downloader-app-1", AppID: appID, UserID: "axy", Project: "downloader", State: "running", Networks: []string{"downloader_default"}},
		{Name: "downloader1-app-1", AppID: appID, UserID: "damn", Project: "downloader1", State: "running", Networks: []string{"downloader1_default"}},
	}
	got := buildBridgeMapFromInventory(networks, containers)
	if got["lzc-br-one"].InstanceID != appID+"@user:axy" || got["lzc-br-two"].InstanceID != appID+"@user:damn" {
		t.Fatalf("bridge identities = %+v", got)
	}
}
