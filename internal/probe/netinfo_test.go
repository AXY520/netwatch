package probe

import (
	"context"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"netwatch/internal/lzcsdk"
)

func TestProbeNetworkInfoRoutineRefreshPreservesPublicIdentity(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"ip":"203.0.113.10"}`))
	}))
	defer server.Close()

	service := &Service{
		cfg: Config{
			HTTPTimeout:        200 * time.Millisecond,
			PublicIPv4Endpoint: server.URL,
			PublicIPv6Endpoint: server.URL,
		},
		summary: Summary{NetworkInfo: NetworkInfo{
			EgressIPv4: "198.51.100.20",
			EgressIPv6: "2001:db8::20",
		}},
	}

	info := service.probeNetworkInfo(context.Background(), false)
	if got := requests.Load(); got != 0 {
		t.Fatalf("routine refresh made %d public identity requests, want 0", got)
	}
	if info.EgressIPv4 != "198.51.100.20" || info.EgressIPv6 != "2001:db8::20" {
		t.Fatalf("routine refresh replaced cached identity: IPv4=%q IPv6=%q", info.EgressIPv4, info.EgressIPv6)
	}
}

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

func TestReconcileStatusFallsBackWhenSDKStatusIsMissing(t *testing.T) {
	for _, sdkStatus := range []string{"", "unknown"} {
		if got := reconcileStatus(sdkStatus, "up", true); got != "connected" {
			t.Fatalf("sdk=%q up with address: got %q, want connected", sdkStatus, got)
		}
	}
	if got := reconcileStatus("", "down", true); got != "disconnected" {
		t.Fatalf("missing SDK with down kernel: got %q, want disconnected", got)
	}
	if got := reconcileStatus("", "unknown", false); got != "unknown" {
		t.Fatalf("missing all evidence: got %q, want unknown", got)
	}
}

func TestReconcileStatusRejectsStaleSDKConnected(t *testing.T) {
	for _, kernelStatus := range []string{"down", "lowerlayerdown", "notpresent"} {
		if got := reconcileStatus("connected", kernelStatus, true); got != "disconnected" {
			t.Fatalf("kernel=%q: got %q, want disconnected", kernelStatus, got)
		}
	}
	if got := reconcileStatus("connected", "up", false); got != "disconnected" {
		t.Fatalf("connected without address: got %q, want disconnected", got)
	}
}

func TestTunDeviceStatusRequiresActiveRoutingEvidence(t *testing.T) {
	if got := tunDeviceStatus(true, "up", false); got != "disconnected" {
		t.Fatalf("inactive up TUN status=%q", got)
	}
	if got := tunDeviceStatus(true, "unknown", true); got != "connected" {
		t.Fatalf("active TUN status=%q", got)
	}
}

func TestUsableProxyTunAddressIgnoresLinkLocalOnly(t *testing.T) {
	for _, address := range []string{"fe80::1/64", "169.254.1.2/16", "127.0.0.1/8", "::1/128"} {
		if usableProxyTunAddress(address) {
			t.Fatalf("%q must not activate proxy TUN status", address)
		}
	}
	for _, address := range []string{"198.18.0.1/30", "fdfe:dcba:9876::1/126"} {
		if !usableProxyTunAddress(address) {
			t.Fatalf("%q should activate proxy TUN status", address)
		}
	}
}

func TestProxyTunRouteEvidence(t *testing.T) {
	ipv4 := "Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT\nMeta 00000000 00000000 0001 0 0 0 00000000 0 0 0\n"
	if !ipv4RouteTableHasInterface(strings.NewReader(ipv4), "Meta") {
		t.Fatal("IPv4 route through Meta should be active evidence")
	}

	linkLocalIPv6 := "fe800000000000000000000000000000 40 00000000000000000000000000000000 00 00000000000000000000000000000000 00000000 00000000 00000000 00000001 Meta\n"
	if ipv6RouteTableHasInterface(strings.NewReader(linkLocalIPv6), "Meta") {
		t.Fatal("link-local IPv6 route alone must not activate TUN status")
	}
	defaultIPv6 := "00000000000000000000000000000000 00 00000000000000000000000000000000 00 00000000000000000000000000000000 00000000 00000000 00000000 00000001 Meta\n"
	if !ipv6RouteTableHasInterface(strings.NewReader(defaultIPv6), "Meta") {
		t.Fatal("non-link-local IPv6 route through Meta should be active evidence")
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

func TestNegotiatedLinkSpeedOnlyAppliesToPhysicalInterfaces(t *testing.T) {
	if !shouldReadNegotiatedLinkSpeed("wired") {
		t.Fatal("wired interface should expose negotiated speed")
	}
	for _, linkType := range []string{"tun", "bridge", ""} {
		if shouldReadNegotiatedLinkSpeed(linkType) {
			t.Fatalf("%q must not expose negotiated speed", linkType)
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
