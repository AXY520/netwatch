package probe

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"netwatch/internal/dockerlzc"
)

func TestReadAppConnectionSocketFileSkipsListenersAndInfersDirection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tcp")
	body := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n" +
		"   0: 0200000A:01BB 00000000:0000 0A 00000000:00000000 00:00000000 00000000 0 0 100 1\n" +
		"   1: 0200000A:01BB 08080808:C350 01 00000000:00000000 00:00000000 00000000 0 0 101 1\n" +
		"   2: 0200000A:C351 01010101:01BB 02 00000000:00000000 00:00000000 00000000 0 0 102 1\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, readable := readAppConnectionSocketFile(path, "tcp", "ipv4")
	if !readable || len(rows) != 2 {
		t.Fatalf("readable=%v rows=%+v", readable, rows)
	}
	if got := rows[0].entry; got.LocalAddress != "10.0.0.2" || got.RemoteAddress != "8.8.8.8" || got.RemotePort != 50000 || got.State != "ESTABLISHED" || got.Direction != "inbound" {
		t.Fatalf("inbound row = %+v", got)
	}
	if got := rows[1].entry; got.RemoteAddress != "1.1.1.1" || got.RemotePort != 443 || got.State != "SYN_SENT" || got.Direction != "outbound" {
		t.Fatalf("outbound row = %+v", got)
	}
}

func TestReadAppConnectionSocketFileSkipsUnconnectedUDP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "udp")
	body := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n" +
		"   0: 00000000:14E9 00000000:0000 07 00000000:00000000 00:00000000 00000000 0 0 201 1\n" +
		"   1: 0200000A:14E9 08080808:0035 01 00000000:00000000 00:00000000 00000000 0 0 202 1\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, readable := readAppConnectionSocketFile(path, "udp", "ipv4")
	if !readable || len(rows) != 1 || rows[0].entry.State != "ACTIVE" || rows[0].entry.RemotePort != 53 {
		t.Fatalf("readable=%v rows=%+v", readable, rows)
	}
	if got := rows[0].entry.Direction; got != "unknown" {
		t.Fatalf("connected UDP direction=%q, want unknown", got)
	}
}

func TestUnknownPrivateConnectionDoesNotScheduleAutomaticPTR(t *testing.T) {
	if host := cachedAppConnectionRemoteHost("192.168.3.254", nil); host != "" {
		t.Fatalf("unexpected hostname=%q", host)
	}
}

func TestReadAppConnectionSocketFileFiltersUnownedRowsBeforeParsing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tcp")
	body := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n" +
		"   0: 0200000A:01BB 00000000:0000 0A 00000000:00000000 00:00000000 00000000 0 0 100 1\n" +
		"   1: 0200000A:01BB 08080808:C350 01 00000000:00000000 00:00000000 00000000 0 0 101 1\n" +
		"   2: 0200000A:C351 01010101:01BB 01 00000000:00000000 00:00000000 00000000 0 0 102 1\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	owners := map[string]appConnectionSocketOwner{"100": {}, "101": {}}
	rows, readable := readAppConnectionSocketFileForOwners(path, "tcp", "ipv4", owners)
	if !readable || len(rows) != 1 {
		t.Fatalf("readable=%v rows=%+v", readable, rows)
	}
	if rows[0].inode != "101" || rows[0].entry.Direction != "inbound" {
		t.Fatalf("filtered row = %+v", rows[0])
	}
}

func TestGroupAppConnectionNetworkNamespacesDeduplicatesSharedNetNS(t *testing.T) {
	procRoot := t.TempDir()
	for _, item := range []struct {
		pid    int
		target string
	}{{101, "net:[42]"}, {102, "net:[42]"}, {103, "net:[43]"}} {
		nsDir := filepath.Join(procRoot, fmt.Sprint(item.pid), "ns")
		if err := os.MkdirAll(nsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(item.target, filepath.Join(nsDir, "net")); err != nil {
			t.Fatal(err)
		}
	}
	groups := groupAppConnectionNetworkNamespaces(procRoot, []dockerlzc.ContainerRuntimeInfo{
		{ID: "one", PID: 101, NetworkMode: "app_default"},
		{ID: "two", PID: 102, NetworkMode: "container:one"},
		{ID: "three", PID: 103, NetworkMode: "app_default"},
	})
	if len(groups) != 2 {
		t.Fatalf("groups=%+v", groups)
	}
	if len(groups[0].containers) != 2 || !groups[0].shared {
		t.Fatalf("shared group=%+v", groups[0])
	}
}

func TestIsInternalAppConnectionAddress(t *testing.T) {
	tests := []struct {
		address string
		want    bool
	}{
		{"10.0.0.2", true},
		{"172.28.4.133", true},
		{"192.168.3.101", true},
		{"169.254.10.20", true},
		{"fd12:3456::10", true},
		{"fe80::1", true},
		{"::ffff:192.168.3.101", true},
		{"127.0.0.1", false},
		{"8.8.8.8", false},
		{"111.63.65.247", false},
		{"2001:4860:4860::8888", false},
		{"224.0.0.251", false},
		{"::", false},
	}
	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			address := netip.MustParseAddr(test.address)
			if got := isInternalAppConnectionAddress(address); got != test.want {
				t.Fatalf("isInternalAppConnectionAddress(%s)=%v, want %v", test.address, got, test.want)
			}
		})
	}
}

func TestAppConnectionRuntimeHostnamesUsesOnlyInternalLzcAppNames(t *testing.T) {
	containers := []dockerlzc.ContainerRuntimeInfo{
		{
			AppID: "cloud.lazycat.app.ai", Running: true,
			Labels: map[string]string{"com.docker.compose.service": "postgres"},
			NetworkEndpoints: []dockerlzc.ContainerNetworkEndpoint{
				{
					IPv4:     "172.28.4.133",
					DNSNames: []string{"postgres", "postgres.cloud.lazycat.app.ai.lzcapp"},
				},
			},
		},
		{
			AppID: "cloud.lazycat.app.cache", Running: true,
			Labels: map[string]string{"com.docker.compose.service": "redis"},
			NetworkEndpoints: []dockerlzc.ContainerNetworkEndpoint{
				{IPv4: "172.28.4.134", Aliases: []string{"redis"}},
			},
		},
		{
			AppID: "cloud.lazycat.app.stopped", Running: false,
			NetworkEndpoints: []dockerlzc.ContainerNetworkEndpoint{
				{IPv4: "172.28.4.135", DNSNames: []string{"hidden.cloud.lazycat.app.stopped.lzcapp"}},
			},
		},
		{
			AppID: "cloud.lazycat.app.one", Running: true,
			Labels:           map[string]string{"com.docker.compose.service": "one"},
			NetworkEndpoints: []dockerlzc.ContainerNetworkEndpoint{{IPv4: "172.30.0.9"}},
		},
		{
			AppID: "cloud.lazycat.app.two", Running: true,
			Labels:           map[string]string{"com.docker.compose.service": "two"},
			NetworkEndpoints: []dockerlzc.ContainerNetworkEndpoint{{IPv4: "172.30.0.9"}},
		},
	}
	hostnames := appConnectionRuntimeHostnames(containers)
	if got := hostnames["172.28.4.133"]; got != "postgres.cloud.lazycat.app.ai.lzcapp" {
		t.Fatalf("Docker DNS hostname=%q", got)
	}
	if got := hostnames["172.28.4.134"]; got != "redis.cloud.lazycat.app.cache.lzcapp" {
		t.Fatalf("derived container hostname=%q", got)
	}
	if _, ok := hostnames["172.28.4.135"]; ok {
		t.Fatal("stopped container hostname unexpectedly indexed")
	}
	if _, ok := hostnames["172.30.0.9"]; ok {
		t.Fatal("ambiguous address from isolated networks unexpectedly indexed")
	}
}

func TestAppConnectionLANHostnamesReusesInternalDeviceNames(t *testing.T) {
	hostnames := appConnectionLANHostnames(LANDeviceSnapshot{
		Devices: []LANDevice{
			{
				IP:       "192.168.3.101",
				IPv6:     []string{"fd12:3456::101", "2001:db8::101"},
				Hostname: "lzc-pod-vpqvOD.lan.",
				Status:   "online",
			},
			{IP: "192.168.3.102", Hostname: "old-device.lan", Status: "offline"},
			{IP: "192.168.3.103", Hostname: "printer", Status: "online"},
			{IP: "8.8.8.8", Hostname: "public.example", Status: "online"},
		},
	})
	if got := hostnames["192.168.3.101"]; got != "lzc-pod-vpqvOD.lan" {
		t.Fatalf("IPv4 LAN hostname=%q", got)
	}
	if got := hostnames["fd12:3456::101"]; got != "lzc-pod-vpqvOD.lan" {
		t.Fatalf("IPv6 LAN hostname=%q", got)
	}
	if _, ok := hostnames["192.168.3.102"]; ok {
		t.Fatal("offline LAN hostname unexpectedly reused")
	}
	if _, ok := hostnames["192.168.3.103"]; ok {
		t.Fatal("non-.lan hostname unexpectedly exposed in connection details")
	}
	if _, ok := hostnames["8.8.8.8"]; ok {
		t.Fatal("public hostname unexpectedly indexed")
	}
	if _, ok := hostnames["2001:db8::101"]; ok {
		t.Fatal("public IPv6 hostname unexpectedly indexed")
	}
	if got := cachedAppConnectionRemoteHost("192.168.3.101", hostnames); got != "lzc-pod-vpqvOD.lan" {
		t.Fatalf("cached LAN hostname=%q", got)
	}
}

func TestResolveAppConnectionPolicyIDRequiresInstanceWhenAmbiguous(t *testing.T) {
	const appID = "cloud.lazycat.app.downloader"
	containers := []dockerlzc.ContainerRuntimeInfo{
		{AppID: appID, InstanceID: appID + "@user:axy", Running: true},
		{AppID: appID, InstanceID: appID + "@user:damn", Running: true},
	}
	if _, err := resolveAppConnectionPolicyID(containers, appID, ""); err == nil {
		t.Fatal("ambiguous application unexpectedly resolved without instance_id")
	}
	want := appID + "@user:axy"
	if got, err := resolveAppConnectionPolicyID(containers, appID, want); err != nil || got != want {
		t.Fatalf("resolved=%q err=%v, want %q", got, err, want)
	}
	if _, err := resolveAppConnectionPolicyID(containers, appID, "other-instance"); err == nil {
		t.Fatal("foreign instance unexpectedly accepted")
	}
}

func TestResolveAppConnectionPolicyIDKeepsSingleInstanceCompatibility(t *testing.T) {
	const appID = "cloud.lazycat.app.firefox"
	containers := []dockerlzc.ContainerRuntimeInfo{{AppID: appID, Running: true}}
	got, err := resolveAppConnectionPolicyID(containers, appID, "")
	if err != nil || got != appID {
		t.Fatalf("resolved=%q err=%v, want %q", got, err, appID)
	}
}
