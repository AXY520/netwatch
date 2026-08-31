package probe

import (
	"math"
	"net"
	"testing"

	"netwatch/internal/lzcsdk"
)

func TestInferLinkType(t *testing.T) {
	cases := map[string]string{
		"enp2s0":          "wired",
		"eth0":            "wired",
		"enx001122334455": "wired",
		"usb0":            "wired",
		"wlp129s0":        "wifi",
		"wlan0":           "wifi",
		"nw-eth0":         "bridge",
		"Meta":            "tun",
		"mihomo":          "tun",
		"Meta0":           "tun",
		"tun0":            "",
		"lzc-br-x":        "",
	}
	for name, want := range cases {
		if got := inferLinkType(name); got != want {
			t.Fatalf("inferLinkType(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestAutoMonitoredNICsIgnoresVirtualInterfaces(t *testing.T) {
	got := autoMonitoredNICs([]net.Interface{
		{Name: "lo"},
		{Name: "lzc-br-1234"},
		{Name: "vethabc"},
		{Name: "tun0"},
		{Name: "Meta"},
		{Name: "enp2s0"},
		{Name: "enx001122334455"},
		{Name: "wlp129s0"},
	})
	want := []string{"enp2s0", "enx001122334455", "wlp129s0", "Meta"}
	if len(got) != len(want) {
		t.Fatalf("autoMonitoredNICs length = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("autoMonitoredNICs[%d] = %q, want %q; all=%#v", i, got[i], want[i], got)
		}
	}
}

func TestIsKnownProxyTunName(t *testing.T) {
	for _, name := range []string{"Meta", "meta", "Meta0", "mihomo", "Clash", "utun"} {
		if !isKnownProxyTunName(name) {
			t.Fatalf("expected proxy tun: %s", name)
		}
	}
	for _, name := range []string{"tun0", "tap0", "eth0", "metadata", "metal"} {
		if isKnownProxyTunName(name) {
			t.Fatalf("unexpected proxy tun: %s", name)
		}
	}
}

func TestIsUnsafeBlocksProxyTun(t *testing.T) {
	if !isUnsafeNetworkDevice("Meta") {
		t.Fatal("Meta must be unsafe for config/bridge")
	}
}

func TestAutoMonitoredNICsStableOrder(t *testing.T) {
	got := autoMonitoredNICs([]net.Interface{
		{Name: "wlp1s0"},
		{Name: "eth1"},
		{Name: "eth0"},
		{Name: "nw-eth0"},
		{Name: "nw-eth1"},
	})
	// wired ASC, wifi, bridges ASC. Kernel bridge check may filter nw-* without /sys;
	// names still classified via prefix when isKernelBridgeIface fails in unit test env.
	// discoverHostBridgeIfaces requires isKernelBridgeIface — so nw-* may be omitted in tests.
	// At least wired+wifi order must be stable.
	if len(got) < 3 {
		t.Fatalf("expected at least eth0,eth1,wlp1s0 got %#v", got)
	}
	if got[0] != "eth0" || got[1] != "eth1" || got[2] != "wlp1s0" {
		t.Fatalf("unexpected order: %#v", got)
	}
}

func TestBridgeDeviceStatus(t *testing.T) {
	if got := bridgeDeviceStatus(false, "up", false); got != "unavailable" {
		t.Fatalf("absent: %q", got)
	}
	if got := bridgeDeviceStatus(true, "up", false); got != "connected" {
		t.Fatalf("up: %q", got)
	}
	if got := bridgeDeviceStatus(true, "down", false); got != "disconnected" {
		t.Fatalf("down: %q", got)
	}
}

func TestNicDisplayRank(t *testing.T) {
	if nicDisplayRank("eth0") >= nicDisplayRank("wlan0") {
		t.Fatal("wired should rank before wifi")
	}
	if nicDisplayRank("wlan0") >= nicDisplayRank("nw-eth0") {
		t.Fatal("wifi should rank before bridge")
	}
	if nicDisplayRank("nw-eth0") >= nicDisplayRank("Meta") {
		t.Fatal("bridge should rank before proxy tun")
	}
}

func TestEffectiveOperStateProxyTun(t *testing.T) {
	// Without sysfs, unknown stays unknown; function must not panic.
	_ = effectiveOperState("Meta")
	_ = ifaceAdminUp("Meta")
}

func TestParseLinkSpeedMbps(t *testing.T) {
	tests := map[string]float64{
		"100\n":      100,
		"1000\n":     1000,
		"2500":       2500,
		"433.3":      433.3,
		"-1\n":       0,
		"4294967295": 0,
		"unknown":    0,
		"":           0,
	}
	for raw, want := range tests {
		if got := parseLinkSpeedMbps(raw); math.Abs(got-want) > 0.001 {
			t.Errorf("parseLinkSpeedMbps(%q) = %g, want %g", raw, got, want)
		}
	}
}

func TestApplySDKToInterfaceIgnoresAmbiguousWiFiLinkSpeed(t *testing.T) {
	info := InterfaceInfo{
		Name:     "testwifi0",
		LinkType: "wifi",
		Present:  true,
		IPv4:     []string{"192.0.2.10/24"},
	}
	status := lzcsdk.NetStatus{
		WirelessStatus: "connected",
		LinkSpeedBps:   433_300_000,
		Wifi:           lzcsdk.WifiInfo{Connected: true, SSID: "test"},
	}
	got := applySDKToInterface(info, status, true)
	if got.LinkSpeedMbps != 0 {
		t.Fatalf("wifi link speed = %g Mbps, want SDK machine-wide speed ignored", got.LinkSpeedMbps)
	}
}

func TestApplySDKToInterfaceKeepsKernelLinkSpeed(t *testing.T) {
	info := InterfaceInfo{
		Name:          "testwifi0",
		LinkType:      "wifi",
		Present:       true,
		IPv4:          []string{"192.0.2.10/24"},
		LinkSpeedMbps: 866.7,
	}
	status := lzcsdk.NetStatus{
		WirelessStatus: "connected",
		LinkSpeedBps:   433_300_000,
		Wifi:           lzcsdk.WifiInfo{Connected: true},
	}
	got := applySDKToInterface(info, status, true)
	if math.Abs(got.LinkSpeedMbps-866.7) > 0.001 {
		t.Fatalf("wifi link speed = %g Mbps, want existing 866.7", got.LinkSpeedMbps)
	}
}
