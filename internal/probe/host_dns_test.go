package probe

import "testing"

func TestValidateDNSList(t *testing.T) {
	if err := validateDNSList("223.5.5.5,119.29.29.29"); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if err := validateDNSList(""); err == nil {
		t.Fatal("empty should fail")
	}
	if err := validateDNSList("not-an-ip"); err == nil {
		t.Fatal("bad ip should fail")
	}
	if err := validateDNSList("127.0.0.1"); err == nil {
		t.Fatal("loopback should fail")
	}
	if err := validateDNSList("1.1.1.1,2.2.2.2,3.3.3.3,4.4.4.4"); err == nil {
		t.Fatal("more than 3 should fail")
	}
}

func TestDNSMethodFromSnapshot(t *testing.T) {
	if dnsMethodFromSnapshot(hostDNSSnapshot{IgnoreAutoDNS: "yes", DNS: "1.1.1.1"}) != "manual" {
		t.Fatal("expected manual")
	}
	if dnsMethodFromSnapshot(hostDNSSnapshot{IgnoreAutoDNS: "no", DNS: "1.1.1.1"}) != "auto" {
		t.Fatal("expected auto")
	}
}
