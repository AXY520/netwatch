package probe

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestBridgeRootQdiscCanBeManaged(t *testing.T) {
	if !bridgeRootQdiscCanBeManaged("qdisc noqueue 0: root refcnt 2") {
		t.Fatal("noqueue root should be manageable")
	}
	if !bridgeRootQdiscCanBeManaged("qdisc tbf 194: root refcnt 2 rate 1000Kbit") {
		t.Fatal("netwatch tbf root should be manageable")
	}
	if bridgeRootQdiscCanBeManaged("qdisc fq_codel 0: root refcnt 2") {
		t.Fatal("unmanaged root qdisc must not be replaced")
	}
}

func TestHostTrafficControlCommandArgsUseHostNamespaces(t *testing.T) {
	got := hostTrafficControlCommandArgs("/usr/bin/tc", "qdisc", "show", "dev", "lzc-br-example")
	want := []string{"-t", "1", "-m", "-n", "-r", "--", "/usr/bin/tc", "qdisc", "show", "dev", "lzc-br-example"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestConfigureBridgeTrafficLimitsUseDedicatedRuleIDs(t *testing.T) {
	previous := runTrafficControlCommand
	t.Cleanup(func() { runTrafficControlCommand = previous })
	var commands [][]string
	runTrafficControlCommand = func(_ context.Context, args ...string) (string, error) {
		commands = append(commands, append([]string(nil), args...))
		if len(args) >= 2 && args[0] == "qdisc" && args[1] == "show" {
			return "qdisc noqueue 0: root refcnt 2", nil
		}
		return "", nil
	}
	ctx := context.Background()
	if err := configureBridgeDownloadLimit(ctx, "lzc-br-example", 4096); err != nil {
		t.Fatal(err)
	}
	if err := configureBridgeUploadLimit(ctx, "lzc-br-example", 2048); err != nil {
		t.Fatal(err)
	}
	joined := make([]string, len(commands))
	for i, command := range commands {
		joined[i] = strings.Join(command, " ")
	}
	all := strings.Join(joined, "\n")
	if !strings.Contains(all, "root handle 194: tbf rate 4096kbit") {
		t.Fatalf("missing owned download qdisc: %s", all)
	}
	if !strings.Contains(all, "qdisc add dev lzc-br-example clsact") {
		t.Fatalf("missing clsact creation: %s", all)
	}
	if !strings.Contains(all, "ingress pref 49152 handle 194 protocol all matchall action police rate 2048kbit") {
		t.Fatalf("missing owned upload filter: %s", all)
	}
}

func TestConfigureBridgeUploadLimitMigratesLegacyFilter(t *testing.T) {
	previous := runTrafficControlCommand
	t.Cleanup(func() { runTrafficControlCommand = previous })
	var commands [][]string
	runTrafficControlCommand = func(_ context.Context, args ...string) (string, error) {
		commands = append(commands, append([]string(nil), args...))
		if len(args) >= 2 && args[0] == "qdisc" && args[1] == "show" {
			return "qdisc clsact ffff: parent ffff:fff1", nil
		}
		if len(args) >= 2 && args[0] == "filter" && args[1] == "replace" {
			return "RTNETLINK answers: File exists", fmt.Errorf("filter exists")
		}
		return "", nil
	}

	if err := configureBridgeUploadLimit(context.Background(), "lzc-br-example", 4096); err != nil {
		t.Fatal(err)
	}
	joined := make([]string, len(commands))
	for i, command := range commands {
		joined[i] = strings.Join(command, " ")
	}
	all := strings.Join(joined, "\n")
	if !strings.Contains(all, "filter replace dev lzc-br-example ingress pref 49152 handle 194") {
		t.Fatalf("missing handle-based replacement: %s", all)
	}
	if !strings.Contains(all, "filter del dev lzc-br-example ingress pref 49152") {
		t.Fatalf("missing legacy filter removal: %s", all)
	}
	if !strings.Contains(all, "filter add dev lzc-br-example ingress pref 49152 handle 194") {
		t.Fatalf("missing deterministic filter creation: %s", all)
	}
}

func TestEnsureBridgeClsactPreservesExistingQdisc(t *testing.T) {
	previous := runTrafficControlCommand
	t.Cleanup(func() { runTrafficControlCommand = previous })
	var commands [][]string
	runTrafficControlCommand = func(_ context.Context, args ...string) (string, error) {
		commands = append(commands, append([]string(nil), args...))
		if len(args) >= 2 && args[0] == "qdisc" && args[1] == "show" {
			return "qdisc clsact ffff: parent ffff:fff1", nil
		}
		return "", nil
	}
	if err := ensureBridgeClsact(context.Background(), "lzc-br-example"); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 || strings.Join(commands[0], " ") != "qdisc show dev lzc-br-example" {
		t.Fatalf("existing clsact was modified: %#v", commands)
	}
}

func TestAppTrafficBurstBytesHasSaneBounds(t *testing.T) {
	if got := appTrafficBurstBytes(1); got != 16*1024 {
		t.Fatalf("low burst = %d", got)
	}
	if got := appTrafficBurstBytes(maxAppTrafficLimitKbps); got != 4*1024*1024 {
		t.Fatalf("high burst = %d", got)
	}
}
