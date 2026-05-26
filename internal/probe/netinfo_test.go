package probe

import (
	"net"
	"testing"
)

func TestInferLinkType(t *testing.T) {
	cases := map[string]string{
		"enp2s0":   "wired",
		"eth0":     "wired",
		"wlp129s0": "wifi",
		"wlan0":    "wifi",
		"lzc-br-x": "",
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
		{Name: "enp2s0"},
		{Name: "wlp129s0"},
	})
	want := []string{"enp2s0", "wlp129s0"}
	if len(got) != len(want) {
		t.Fatalf("autoMonitoredNICs length = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("autoMonitoredNICs[%d] = %q, want %q; all=%#v", i, got[i], want[i], got)
		}
	}
}
