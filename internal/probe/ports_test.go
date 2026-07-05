package probe

import "testing"

func TestParseProcAddressIPv4(t *testing.T) {
	addr, port, ok := parseProcAddress("0100007F:1F90", "ipv4")
	if !ok {
		t.Fatal("parse failed")
	}
	if addr != "127.0.0.1" || port != 8080 {
		t.Fatalf("got %s:%d", addr, port)
	}
}

func TestParseProcAddressIPv6(t *testing.T) {
	addr, port, ok := parseProcAddress("00000000000000000000000000000000:0050", "ipv6")
	if !ok {
		t.Fatal("parse failed")
	}
	if addr != "::" || port != 80 {
		t.Fatalf("got %s:%d", addr, port)
	}
}

func TestTCPStateListen(t *testing.T) {
	if got := tcpState("0A"); got != "LISTEN" {
		t.Fatalf("got %q", got)
	}
}
