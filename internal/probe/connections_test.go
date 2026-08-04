package probe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadConnectionFileSkipsListeners(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tcp")
	body := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n" +
		"   0: 0100007F:0050 00000000:0000 0A 0:0 0:0 0 0 0\n" +
		"   1: 0200000A:C350 08080808:01BB 01 0:0 0:0 0 0 0\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := readConnectionFile(path, "tcp", "ipv4")
	if len(got) != 1 {
		t.Fatalf("connections = %+v", got)
	}
	if got[0].LocalAddress != "10.0.0.2" || got[0].RemoteAddress != "8.8.8.8" || got[0].RemotePort != 443 || got[0].State != "ESTABLISHED" {
		t.Fatalf("unexpected connection: %+v", got[0])
	}
}

func TestMaskPublicAddress(t *testing.T) {
	if got := maskPublicAddress("8.8.8.8"); got != "8.8.8.0/24" {
		t.Fatalf("mask ipv4 = %q", got)
	}
	if got := maskPublicAddress("192.168.1.8"); got != "192.168.1.8" {
		t.Fatalf("private address changed: %q", got)
	}
	if got := maskPublicAddress("2001:4860:4860::8888"); got != "2001:4860:4860::/48" {
		t.Fatalf("mask ipv6 = %q", got)
	}
}
