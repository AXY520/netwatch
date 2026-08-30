package probe

import (
	"bytes"
	"net"
	"reflect"
	"testing"
)

func TestBuildAppProxyRulesHTTPBlocksPublicUDP(t *testing.T) {
	target := AppNetworkTarget{ID: "lzc-br-demo", Kind: AppNetworkTargetBridge, AppID: "app.demo", Interface: "lzc-br-demo"}
	config := AppProxySettings{Protocol: "http", Host: "192.168.1.2", Port: 7890}
	guard, _, v4, _, err := buildAppProxyRules([]AppNetworkTarget{target}, map[string]AppProxySettings{target.AppID: config}, nil, map[string]int{target.AppID: 23089})
	if err != nil {
		t.Fatal(err)
	}
	if len(guard.filterForward) == 0 || !reflect.DeepEqual(guard.filterForward[len(guard.filterForward)-1], []string{"-i", "lzc-br-demo", "-j", "DROP"}) {
		t.Fatalf("guard rules=%#v", guard.filterForward)
	}
	if !rulesContain(v4.natPre, "-p", "tcp", "-j", "REDIRECT") || rulesContain(v4.natPre, "-p", "udp", "-j", "REDIRECT") {
		t.Fatalf("HTTP NAT rules=%#v", v4.natPre)
	}
	if !rulesContain(v4.filterForward, "-j", "DROP") {
		t.Fatalf("HTTP filter rules=%#v", v4.filterForward)
	}
}

func TestBuildAppProxyRulesSOCKSRelaysUDPAndPausedAppFailsClosed(t *testing.T) {
	target := AppNetworkTarget{ID: "lzc-br-demo", Kind: AppNetworkTargetBridge, AppID: "app.demo", Interface: "lzc-br-demo"}
	config := AppProxySettings{Protocol: "socks5", Host: "192.168.1.2", Port: 7890}
	configs := map[string]AppProxySettings{target.AppID: config}
	ports := map[string]int{target.AppID: 23089}
	_, _, active, _, err := buildAppProxyRules([]AppNetworkTarget{target}, configs, nil, ports)
	if err != nil {
		t.Fatal(err)
	}
	if !rulesContain(active.natPre, "-p", "tcp", "-j", "REDIRECT") || !rulesContain(active.natPre, "-p", "udp", "-j", "REDIRECT") {
		t.Fatalf("SOCKS5 NAT rules=%#v", active.natPre)
	}
	if !rulesContain(active.filterForward, "-j", "DROP") {
		t.Fatalf("SOCKS5 unsupported-protocol guard=%#v", active.filterForward)
	}
	_, _, paused, _, err := buildAppProxyRules([]AppNetworkTarget{target}, configs, map[string]bool{target.ID: true}, ports)
	if err != nil {
		t.Fatal(err)
	}
	if len(paused.natPre) != 0 || !rulesContain(paused.filterForward, "-j", "DROP") {
		t.Fatalf("paused rules=%#v", paused)
	}
}

func TestBuildAppProxyRulesUsesPerAppPorts(t *testing.T) {
	targets := []AppNetworkTarget{
		{ID: "lzc-br-a", Kind: AppNetworkTargetBridge, AppID: "app.a", Interface: "lzc-br-a"},
		{ID: "lzc-br-b", Kind: AppNetworkTargetBridge, AppID: "app.b", Interface: "lzc-br-b"},
	}
	configs := map[string]AppProxySettings{
		"app.a": {Protocol: "http", Host: "192.168.1.2", Port: 7890},
		"app.b": {Protocol: "socks5", Host: "192.168.1.3", Port: 7891},
	}
	_, _, v4, _, err := buildAppProxyRules(targets, configs, nil, map[string]int{"app.a": 23089, "app.b": 23090})
	if err != nil {
		t.Fatal(err)
	}
	if !rulesContain(v4.natPre, "-i", "lzc-br-a", "-p", "tcp", "-j", "REDIRECT", "--to-ports", "23089") ||
		!rulesContain(v4.natPre, "-i", "lzc-br-b", "-p", "tcp", "-j", "REDIRECT", "--to-ports", "23090") ||
		!rulesContain(v4.natPre, "-i", "lzc-br-b", "-p", "udp", "-j", "REDIRECT", "--to-ports", "23090") ||
		rulesContain(v4.natPre, "-i", "lzc-br-a", "-p", "udp") {
		t.Fatalf("per-app NAT rules=%#v", v4.natPre)
	}
}

func TestAppProxyAdaptersUseDistinctPorts(t *testing.T) {
	config := AppProxySettings{Protocol: "http", Host: "192.0.2.1", Port: 7890}
	first, second := newAppProxyAdapter(), newAppProxyAdapter()
	defer first.close()
	defer second.close()
	if err := first.ensureStarted(config); err != nil {
		t.Fatal(err)
	}
	if err := second.ensureStarted(config); err != nil {
		t.Fatal(err)
	}
	if first.port() == 0 || second.port() == 0 || first.port() == second.port() {
		t.Fatalf("adapter ports first=%d second=%d", first.port(), second.port())
	}
}

func TestSOCKSUDPDatagramRoundTrip(t *testing.T) {
	payload := []byte("dns-payload")
	for _, address := range []*net.UDPAddr{
		{IP: net.ParseIP("8.8.8.8"), Port: 53},
		{IP: net.ParseIP("2001:4860:4860::8888"), Port: 53},
	} {
		packet, err := encodeSocksUDPDatagram(address, payload)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeSocksUDPDatagram(packet)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("decoded payload=%q", got)
		}
	}
}

func rulesContain(rules [][]string, sequence ...string) bool {
	for _, rule := range rules {
		for start := 0; start+len(sequence) <= len(rule); start++ {
			if reflect.DeepEqual(rule[start:start+len(sequence)], sequence) {
				return true
			}
		}
	}
	return false
}
