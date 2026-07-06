package probe

import "testing"

func TestClassifyNAT1WhenLocalAndExternalMatch(t *testing.T) {
	got, confidence := classifyNAT([]NATObservation{
		{LocalAddr: "203.0.113.10:50000", ExternalAddr: "203.0.113.10:50000"},
		{LocalAddr: "203.0.113.10:50000", ExternalAddr: "203.0.113.10:50000"},
	}, true)
	if got != NAT1 || confidence != "high" {
		t.Fatalf("classifyNAT = %s/%s, want NAT1/high", got, confidence)
	}
}

func TestClassifyNAT4WhenMappingChangesAcrossServers(t *testing.T) {
	got, confidence := classifyNAT([]NATObservation{
		{LocalAddr: "192.168.1.10:50000", ExternalAddr: "198.51.100.20:41000"},
		{LocalAddr: "192.168.1.10:50001", ExternalAddr: "198.51.100.20:42000"},
	}, true)
	if got != NAT4 || confidence != "high" {
		t.Fatalf("classifyNAT = %s/%s, want NAT4/high", got, confidence)
	}
}

func TestClassifyNAT4WhenSTUNUnreachable(t *testing.T) {
	got, confidence := classifyNAT(nil, false)
	if got != NAT4 || confidence != "high" {
		t.Fatalf("classifyNAT = %s/%s, want NAT4/high", got, confidence)
	}
}
