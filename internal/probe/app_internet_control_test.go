package probe

import (
	"reflect"
	"testing"
)

func TestBuildAppFirewallRulesUsesPrivateBypassBeforeDrop(t *testing.T) {
	v4, v6, err := buildAppFirewallRules([]AppNetworkTarget{{
		ID: "lzc-br-demo", Kind: AppNetworkTargetBridge, AppID: "app.demo", Interface: "lzc-br-demo",
	}}, map[string]string{"lzc-br-demo": "internet"})
	if err != nil {
		t.Fatal(err)
	}
	wantV4 := [][]string{
		{"-i", "lzc-br-demo", "-d", "10.0.0.0/8", "-j", "RETURN"},
		{"-i", "lzc-br-demo", "-d", "172.16.0.0/12", "-j", "RETURN"},
		{"-i", "lzc-br-demo", "-d", "192.168.0.0/16", "-j", "RETURN"},
		{"-i", "lzc-br-demo", "-j", "DROP"},
	}
	if !reflect.DeepEqual(v4.forward, wantV4) {
		t.Fatalf("IPv4 forward rules=%#v", v4.forward)
	}
	if len(v6.forward) != 3 || v6.forward[len(v6.forward)-1][len(v6.forward[len(v6.forward)-1])-1] != "DROP" {
		t.Fatalf("IPv6 forward rules=%#v", v6.forward)
	}
}

func TestBuildAppFirewallRulesUsesAppCgroupParent(t *testing.T) {
	v4, _, err := buildAppFirewallRules([]AppNetworkTarget{{
		ID: "host-app:app.demo", Kind: AppNetworkTargetCgroup, AppID: "app.demo",
		CgroupPath: "system.slice/lzcapp.slice/lzcapp-app.demo.slice/docker-container.scope",
	}}, map[string]string{"host-app:app.demo": "internet"})
	if err != nil {
		t.Fatal(err)
	}
	wantPrefix := []string{"-m", "cgroup", "--path", "system.slice/lzcapp.slice/lzcapp-app.demo.slice"}
	if len(v4.output) == 0 || !reflect.DeepEqual(v4.output[0][:len(wantPrefix)], wantPrefix) {
		t.Fatalf("Host output rules=%#v", v4.output)
	}
}

func TestAppNetworkTargetsReplaceRecreatedBridge(t *testing.T) {
	targets := appNetworkTargets([]AppBridgeStats{
		{AppID: "app.demo", Bridge: "lzc-br-new", NetworkMode: "bridge"},
	})
	if len(targets) != 1 || targets[0].ID != "lzc-br-new" || targets[0].AppID != "app.demo" {
		t.Fatalf("targets=%#v", targets)
	}
}
