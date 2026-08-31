package probe

import (
	"net"
	"testing"
)

func TestDetectProxyEnvironmentFromTunAndEnvironment(t *testing.T) {
	ifaces := []net.Interface{
		{Name: "enp2s0", Flags: net.FlagUp},
		{Name: "Meta", Flags: net.FlagUp | net.FlagPointToPoint},
		{Name: "Clash0", Flags: net.FlagPointToPoint},
	}
	values := map[string]string{
		"HTTPS_PROXY": "http://127.0.0.1:7890",
		"https_proxy": "http://127.0.0.1:7890",
	}
	got := detectProxyEnvironmentFrom(ifaces, func(name string) string { return values[name] })
	if !got.Detected || got.Mode != "mixed" || !got.NATMayBeAffected || got.Confidence != "high" {
		t.Fatalf("detectProxyEnvironmentFrom = %+v", got)
	}
	if len(got.Interfaces) != 1 || got.Interfaces[0] != "Meta" {
		t.Fatalf("interfaces = %v", got.Interfaces)
	}
	if len(got.EnvironmentVariables) != 1 || got.EnvironmentVariables[0] != "HTTPS_PROXY" {
		t.Fatalf("environment variables = %v", got.EnvironmentVariables)
	}
}

func TestDetectProxyEnvironmentFromNone(t *testing.T) {
	got := detectProxyEnvironmentFrom([]net.Interface{{Name: "enp2s0", Flags: net.FlagUp}}, func(string) string { return "" })
	if got.Detected || got.Mode != "none" || got.NATMayBeAffected || got.Confidence != "medium" {
		t.Fatalf("detectProxyEnvironmentFrom = %+v", got)
	}
}
