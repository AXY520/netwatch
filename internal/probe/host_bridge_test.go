package probe

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultHostBridgeName(t *testing.T) {
	name := defaultHostBridgeName("eth0")
	if name != "nw-eth0" {
		t.Fatalf("got %q", name)
	}
	if len(name) > hostBridgeNameMax {
		t.Fatalf("too long: %s", name)
	}
	long := defaultHostBridgeName("enp0s31f6-extra-long-name")
	if len(long) > hostBridgeNameMax {
		t.Fatalf("long name not truncated: %q len=%d", long, len(long))
	}
	if !isManagedHostBridgeName(long) {
		t.Fatalf("should keep managed prefix: %q", long)
	}
}

func TestResolveHostBridgeName(t *testing.T) {
	n, err := resolveHostBridgeName("", "eth0")
	if err != nil || n != "nw-eth0" {
		t.Fatalf("default: %q %v", n, err)
	}
	// bare suffix is forced under nw-
	n, err = resolveHostBridgeName("vm0", "eth0")
	if err != nil || n != "nw-vm0" {
		t.Fatalf("suffix: %q %v", n, err)
	}
	n, err = resolveHostBridgeName("nw-vm0", "eth0")
	if err != nil || n != "nw-vm0" {
		t.Fatalf("explicit: %q %v", n, err)
	}
	_, err = resolveHostBridgeName("nw-bad name", "eth0")
	if err == nil {
		t.Fatal("expected invalid name")
	}
	_, err = resolveHostBridgeName("nw-", "eth0")
	if err == nil {
		t.Fatal("expected empty suffix error")
	}
	// full name longer than 15 after force-prefix
	_, err = resolveHostBridgeName("toolongbridge1", "eth0") // nw-toolongbridge1 = 17
	if err == nil {
		t.Fatal("expected length error")
	}
	// valid chars only
	_, err = resolveHostBridgeName("ab/c", "eth0")
	if err == nil {
		t.Fatal("expected invalid chars")
	}
}

func TestHostBridgeNameRE(t *testing.T) {
	ok := []string{"nw-eth0", "nw-enx0", "nw-a", "nw-A1._-x"}
	for _, name := range ok {
		if !hostBridgeNameRE.MatchString(name) {
			t.Fatalf("expected ok: %q", name)
		}
	}
	bad := []string{"", "-nw", "nw eth", "nw@1"}
	for _, name := range bad {
		if hostBridgeNameRE.MatchString(name) {
			t.Fatalf("expected reject: %q", name)
		}
	}
}

func TestEnsureHostBridgeDNS(t *testing.T) {
	got := ensureHostBridgeDNS("192.168.1.1", "127.0.0.53", "8.8.8.8")
	if got != "192.168.1.1,8.8.8.8" {
		t.Fatalf("got %q", got)
	}
	// loopback-only candidates still fall back to public DNS
	got = ensureHostBridgeDNS("127.0.0.53", "::1")
	if got == "" || got == "127.0.0.53" {
		t.Fatalf("expected public fallback, got %q", got)
	}
	if !containsDNS(got, "223.5.5.5") {
		t.Fatalf("expected alidns fallback in %q", got)
	}
}

func containsDNS(list, want string) bool {
	for _, p := range splitDNS(list) {
		if p == want {
			return true
		}
	}
	return false
}

func splitDNS(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func TestPickHostBridgePendingRestore(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.Local)
	records := []HostBridgeRecord{
		{Bridge: "nw-old", Device: "eth0", Confirmed: false, CreatedAt: now.Add(-10 * time.Minute).Format(time.DateTime)},
		{Bridge: "nw-new", Device: "eth1", Confirmed: false, CreatedAt: now.Add(-1 * time.Minute).Format(time.DateTime), RollbackID: "rb-1", RollbackUntil: now.Add(2 * time.Minute).Format(time.DateTime)},
		{Bridge: "nw-done", Device: "eth2", Confirmed: true, CreatedAt: now.Add(-30 * time.Second).Format(time.DateTime)},
	}
	active, until, expired := pickHostBridgePendingRestore(records, now)
	if active == nil || active.Bridge != "nw-new" {
		t.Fatalf("active=%v", active)
	}
	if active.RollbackID != "rb-1" {
		t.Fatalf("rollback id: %q", active.RollbackID)
	}
	if !until.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("until=%v", until)
	}
	if len(expired) != 1 || expired[0].Bridge != "nw-old" {
		t.Fatalf("expired=%v", expired)
	}
}

func TestHostBridgeRecordDeadlineFallback(t *testing.T) {
	nowBefore := time.Now()
	deadline, created := hostBridgeRecordDeadline(HostBridgeRecord{Bridge: "nw-x", Confirmed: false})
	if deadline.Before(nowBefore.Add(hostBridgeRollbackDelay - time.Second)) {
		t.Fatalf("expected fresh window, deadline=%v created=%v", deadline, created)
	}
}
