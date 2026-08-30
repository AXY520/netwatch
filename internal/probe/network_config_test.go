package probe

import (
	"slices"
	"testing"
)

func TestNetworkConfigMAC(t *testing.T) {
	got, err := normalizeNetworkConfigMAC("02-11-22-33-44-55")
	if err != nil || got != "02:11:22:33:44:55" {
		t.Fatalf("normalize MAC = %q, %v", got, err)
	}
	for _, value := range []string{"not-a-mac", "00:00:00:00:00:00", "ff:ff:ff:ff:ff:ff", "01:11:22:33:44:55"} {
		if _, err := normalizeNetworkConfigMAC(value); err == nil {
			t.Errorf("invalid MAC %q was accepted", value)
		}
	}
	for deviceType, property := range map[string]string{
		"ethernet": "802-3-ethernet.cloned-mac-address",
		"wifi":     "802-11-wireless.cloned-mac-address",
		"bridge":   "802-3-ethernet.cloned-mac-address",
	} {
		if got := networkConfigMACProperty(deviceType); got != property {
			t.Errorf("MAC property for %s = %q, want %q", deviceType, got, property)
		}
	}

	req := NetworkConfigApplyRequest{Device: "eth0", MACAddress: got, MACOnly: true}
	if err := validateNetworkConfigRequest(req); err != nil {
		t.Fatalf("valid MAC-only request rejected: %v", err)
	}
	args := networkConfigApplyArgs("wired", req, networkConfigMACProperty("ethernet"))
	want := []string{"connection", "modify", "wired", "802-3-ethernet.cloned-mac-address", got}
	if !slices.Equal(args, want) {
		t.Fatalf("MAC apply args = %q, want %q", args, want)
	}
	if err := validateNetworkConfigRequest(NetworkConfigApplyRequest{Device: "eth0", MACOnly: true}); err == nil {
		t.Fatal("empty MAC-only request was accepted")
	}
}
