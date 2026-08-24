package probe

import (
	"errors"
	"testing"
	"time"
)

func TestSameCIDRAddress(t *testing.T) {
	for _, tc := range []struct {
		runtime   string
		requested string
		want      bool
	}{
		{"192.0.2.10/24", "192.0.2.10/24", true},
		{"192.0.2.10/24", "192.0.2.10/25", false},
		{"192.0.2.11/24", "192.0.2.10/24", false},
		{"", "192.0.2.10/24", false},
	} {
		if got := sameCIDRAddress(tc.runtime, tc.requested); got != tc.want {
			t.Errorf("sameCIDRAddress(%q, %q) = %v, want %v", tc.runtime, tc.requested, got, tc.want)
		}
	}
}

func TestDNSContainsAll(t *testing.T) {
	if !dnsContainsAll("1.1.1.1,8.8.8.8", "8.8.8.8 1.1.1.1") {
		t.Fatal("expected DNS sets to match")
	}
	if dnsContainsAll("1.1.1.1", "1.1.1.1,8.8.8.8") {
		t.Fatal("missing requested DNS was accepted")
	}
}

func TestVerifyRuntimeMutationConfigHardFailures(t *testing.T) {
	m := &networkMutation{
		Kind: networkMutationIP,
		IP: &networkConfigRollback{Request: NetworkConfigApplyRequest{
			Method: "manual", Address: "192.0.2.10/24", Gateway: "192.0.2.1", DNS: "1.1.1.1",
		}},
	}
	steps := verifyRuntimeMutationConfig(m, networkDeviceRuntimeConfig{
		IPv4: "192.0.2.20/24", Gateway: "192.0.2.254", DNS: "8.8.8.8",
	}, nil)
	if requiredStepsPassed(steps) {
		t.Fatalf("mismatched runtime config passed: %+v", steps)
	}
	if len(steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(steps))
	}
}

func TestVerifyRuntimeMutationConfigPassesKernelRuntimeState(t *testing.T) {
	m := &networkMutation{
		Kind: networkMutationIP,
		IP: &networkConfigRollback{Request: NetworkConfigApplyRequest{
			Method: "manual", Address: "192.0.2.10/24", Gateway: "192.0.2.1", DNS: "1.1.1.1,8.8.8.8",
		}},
	}
	steps := verifyRuntimeMutationConfig(m, networkDeviceRuntimeConfig{
		IPv4: "192.0.2.10/24", Gateway: "192.0.2.1", DNS: "1.1.1.1,8.8.8.8",
	}, nil)
	if !requiredStepsPassed(steps) {
		t.Fatalf("matching kernel runtime config failed: %+v", steps)
	}
}

func TestVerifyAutoDNSRequiresRuntimeNameserver(t *testing.T) {
	m := &networkMutation{
		Kind: networkMutationDNS,
		DNS:  &hostDNSRollback{Request: HostDNSApplyRequest{Method: "auto"}},
	}
	steps := verifyRuntimeMutationConfig(m, networkDeviceRuntimeConfig{}, nil)
	if requiredStepsPassed(steps) {
		t.Fatalf("empty automatic DNS passed: %+v", steps)
	}
}

func TestMergeRuntimeNetworkConfigFillsKernelGapsFromNmcli(t *testing.T) {
	got := mergeRuntimeNetworkConfig(
		networkDeviceRuntimeConfig{DNS: "192.168.3.1"},
		networkDeviceRuntimeConfig{IPv4: "192.168.3.173/24", Gateway: "192.168.3.1", DNS: "1.1.1.1"},
	)
	if got.IPv4 != "192.168.3.173/24" || got.Gateway != "192.168.3.1" {
		t.Fatalf("merged runtime = %+v, expected nmcli to fill missing address and gateway", got)
	}
	if got.DNS != "1.1.1.1" {
		t.Fatalf("merged DNS = %q, want explicit nmcli DNS", got.DNS)
	}
}

func TestMergeRuntimeNetworkConfigPrefersKernelValues(t *testing.T) {
	got := mergeRuntimeNetworkConfig(
		networkDeviceRuntimeConfig{IPv4: "192.168.3.173/24", Gateway: "192.168.3.1", DNS: "1.1.1.1"},
		networkDeviceRuntimeConfig{IPv4: "192.168.3.200/24", Gateway: "192.168.3.254", DNS: "8.8.8.8"},
	)
	if got.IPv4 != "192.168.3.173/24" || got.Gateway != "192.168.3.1" || got.DNS != "8.8.8.8" {
		t.Fatalf("merged runtime = %+v, want kernel address/gateway and explicit nmcli DNS", got)
	}
}

func TestRequiredStepsIgnoreOptionalFailures(t *testing.T) {
	steps := []NetworkMutationVerificationStep{
		verificationStep("required", true, time.Now(), "", nil),
		verificationStep("optional", false, time.Now(), "", errors.New("offline")),
	}
	if !requiredStepsPassed(steps) {
		t.Fatal("optional failure incorrectly failed required verification")
	}
}
