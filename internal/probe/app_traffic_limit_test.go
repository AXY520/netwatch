package probe

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	tcnetlink "github.com/vishvananda/netlink"
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

func TestBridgeHasIngressHook(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{name: "clsact", output: "qdisc clsact ffff: parent ffff:fff1", want: true},
		{name: "legacy ingress", output: "qdisc ingress ffff: parent ffff:fff1", want: true},
		{name: "root only", output: "qdisc noqueue 0: root refcnt 2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := bridgeHasIngressHook(test.output); got != test.want {
				t.Fatalf("bridgeHasIngressHook(%q) = %v, want %v", test.output, got, test.want)
			}
		})
	}
}

func TestTrafficControlNotFoundRecognizesMissingParentQdisc(t *testing.T) {
	outputs := []string{
		"Error: Parent Qdisc doesn't exists.\nWe have an error talking to the kernel",
		"Error: Parent Qdisc doesn't exist.",
		"Error: Parent Qdisc does not exist.",
	}
	for _, output := range outputs {
		if !trafficControlNotFound(output) {
			t.Fatalf("missing parent qdisc was not recognized: %q", output)
		}
	}
	if trafficControlNotFound("RTNETLINK answers: Operation not permitted") {
		t.Fatal("permission error must not be treated as an absent rule")
	}
}

func TestClearBridgeUploadFiltersSkipsBridgeWithoutIngressHook(t *testing.T) {
	previous := runTrafficControlCommand
	t.Cleanup(func() { runTrafficControlCommand = previous })
	var commands [][]string
	runTrafficControlCommand = func(_ context.Context, args ...string) (string, error) {
		commands = append(commands, append([]string(nil), args...))
		return "qdisc noqueue 0: root refcnt 2", nil
	}

	if err := clearBridgeUploadFilters(context.Background(), "lzc-br-example"); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 || strings.Join(commands[0], " ") != "qdisc show dev lzc-br-example" {
		t.Fatalf("unexpected cleanup commands: %#v", commands)
	}
}

func TestClearBridgeUploadFiltersHandlesDisappearingIngressHook(t *testing.T) {
	previous := runTrafficControlCommand
	t.Cleanup(func() { runTrafficControlCommand = previous })
	deleteCalls := 0
	runTrafficControlCommand = func(_ context.Context, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "qdisc" && args[1] == "show" {
			return "qdisc clsact ffff: parent ffff:fff1", nil
		}
		if len(args) >= 2 && args[0] == "filter" && args[1] == "del" {
			deleteCalls++
			return "Error: Parent Qdisc doesn't exists.\nWe have an error talking to the kernel", fmt.Errorf("parent missing")
		}
		return "", nil
	}

	if err := clearBridgeUploadFilters(context.Background(), "lzc-br-example"); err != nil {
		t.Fatal(err)
	}
	if deleteCalls != len(appTrafficTCUploadBypassFilters)+1 {
		t.Fatalf("delete calls = %d, want %d", deleteCalls, len(appTrafficTCUploadBypassFilters)+1)
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
		if len(args) >= 2 && args[0] == "filter" && args[1] == "del" {
			return "RTNETLINK answers: No such file or directory", fmt.Errorf("filter missing")
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
	if !strings.Contains(all, "filter add dev lzc-br-example ingress pref 49152 handle 194 protocol all matchall action police rate 2048kbit") {
		t.Fatalf("missing owned upload filter: %s", all)
	}
	if !strings.Contains(all, "mtu 65535 conform-exceed drop/pipe action gact pass") {
		t.Fatalf("upload police must pass conforming packets and drop excess traffic: %s", all)
	}
	if !strings.Contains(all, "rate 2048kbit burst 131072 mtu 65535") {
		t.Fatalf("upload police burst must admit an offloaded skb: %s", all)
	}
	if !strings.Contains(all, "filter add dev lzc-br-example ingress pref 49140 protocol ip flower dst_ip 10.0.0.0/8 action gact pass") {
		t.Fatalf("missing local upload bypass filter: %s", all)
	}
}

func TestConfigureBridgeUploadLimitClearsDuplicateLegacyFilters(t *testing.T) {
	previous := runTrafficControlCommand
	t.Cleanup(func() { runTrafficControlCommand = previous })
	var commands [][]string
	deleteCalls := map[string]int{}
	runTrafficControlCommand = func(_ context.Context, args ...string) (string, error) {
		commands = append(commands, append([]string(nil), args...))
		if len(args) >= 2 && args[0] == "qdisc" && args[1] == "show" {
			return "qdisc clsact ffff: parent ffff:fff1", nil
		}
		if len(args) >= 2 && args[0] == "filter" && args[1] == "del" {
			pref := ""
			for index := 0; index+1 < len(args); index++ {
				if args[index] == "pref" {
					pref = args[index+1]
					break
				}
			}
			deleteCalls[pref]++
			if pref == appTrafficTCFilterPref && deleteCalls[pref] <= 2 {
				return "", nil
			}
			return "RTNETLINK answers: No such file or directory", fmt.Errorf("filter missing")
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
	if deleteCalls[appTrafficTCFilterPref] != 3 {
		t.Fatalf("main filter cleanup calls = %d, want 3", deleteCalls[appTrafficTCFilterPref])
	}
	if !strings.Contains(all, "filter add dev lzc-br-example ingress pref 49152 handle 194") {
		t.Fatalf("missing deterministic filter creation after cleanup: %s", all)
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

func TestParseTrafficRateKbps(t *testing.T) {
	tests := []struct {
		line string
		want int64
	}{
		{line: "qdisc tbf 194: root rate 10000Kbit burst 125000", want: 10000},
		{line: "police 0x1 rate 10Mbit burst 125000 conform-exceed drop/ok", want: 10000},
		{line: "police 0x1 rate 1Gbit", want: 1000000},
	}
	for _, test := range tests {
		got, ok := parseTrafficRateKbps(test.line)
		if !ok || got != test.want {
			t.Fatalf("parseTrafficRateKbps(%q) = (%d, %v), want (%d, true)", test.line, got, ok, test.want)
		}
	}
	if _, ok := parseTrafficRateKbps("qdisc noqueue 0: root"); ok {
		t.Fatal("rate-less tc output must not parse")
	}
}

func TestParseAppTrafficLimitInspection(t *testing.T) {
	qdisc := "qdisc noqueue 0: root refcnt 2\nqdisc tbf 194: root refcnt 2 rate 4096Kbit burst 51200 latency 50.0ms\nqdisc clsact ffff: parent ffff:fff1"
	filters := "filter protocol ip pref 49140 flower\n action order 1: gact action pass\nfilter protocol all pref 49152 matchall chain 0\n action order 1: police 0x1 rate 2048Kbit burst 128Kb mtu 64Kb conform-exceed drop/pipe\n action order 2: gact action pass"
	got := parseAppTrafficLimitInspection(qdisc, filters)
	if !got.DownloadRule || got.DownloadKbps != 4096 || !got.UploadRule || got.UploadKbps != 2048 || len(got.UploadBypass) != 1 || !got.UploadAction || !got.UploadCount || !got.UploadMTU || !got.UploadBurst || !got.Ingress {
		t.Fatalf("inspection=%#v", got)
	}
}

func TestParseAppTrafficLimitInspectionAcceptsExpandedPoliceAction(t *testing.T) {
	filters := "filter protocol all pref 49152 matchall chain 0\n action order 1: police 0x1 rate 1024Kbit burst 16384 mtu 64Kb action drop\n action order 2: gact action pass"
	got := parseAppTrafficLimitInspection("qdisc clsact ffff: parent ffff:fff1", filters)
	if !got.UploadRule || !got.UploadAction || !got.UploadCount || got.UploadKbps != 1024 {
		t.Fatalf("inspection=%#v", got)
	}
}

func TestAcceptedBridgeUploadBytesIncludesPublicAndLocalActions(t *testing.T) {
	action := func(bytes uint64) *tcnetlink.GenericAction {
		return &tcnetlink.GenericAction{ActionAttrs: tcnetlink.ActionAttrs{
			Statistics: &tcnetlink.ActionStatistic{Basic: &tcnetlink.GnetStatsBasic{Bytes: bytes}},
		}}
	}
	mainPref, _ := strconv.ParseUint(appTrafficTCFilterPref, 10, 16)
	filters := []tcnetlink.Filter{&tcnetlink.MatchAll{
		FilterAttrs: tcnetlink.FilterAttrs{Priority: uint16(mainPref)},
		Actions:     []tcnetlink.Action{tcnetlink.NewPoliceAction(), action(700)},
	}}
	for index, bypass := range appTrafficTCUploadBypassFilters {
		pref, _ := strconv.ParseUint(bypass.pref, 10, 16)
		filters = append(filters, &tcnetlink.Flower{
			FilterAttrs: tcnetlink.FilterAttrs{Priority: uint16(pref)},
			Actions:     []tcnetlink.Action{action(uint64(index + 1))},
		})
	}
	bytes, ok := acceptedBridgeUploadBytesFromFilters(filters)
	if !ok || bytes != 715 {
		t.Fatalf("bytes=%d ok=%v", bytes, ok)
	}
	filters = filters[:len(filters)-1]
	if _, ok := acceptedBridgeUploadBytesFromFilters(filters); ok {
		t.Fatal("incomplete bypass accounting must not replace raw bridge counters")
	}
}

func TestInspectBridgeTrafficLimitReportsDrift(t *testing.T) {
	previous := runTrafficControlCommand
	t.Cleanup(func() { runTrafficControlCommand = previous })
	runTrafficControlCommand = func(_ context.Context, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "qdisc" && args[1] == "show" {
			return "qdisc noqueue 0: root refcnt 2", nil
		}
		return "filter protocol all pref 49152 matchall\n action order 1: police 0x1 rate 512Kbit conform-exceed drop/ok", nil
	}
	status, err := inspectBridgeTrafficLimit(context.Background(), "lzc-br-example", AppTrafficLimit{UploadKbps: 1024})
	if err != nil || status.InSync || !strings.Contains(status.Diagnostic, "上传速率为 512 Kbit/s") {
		t.Fatalf("status=%#v err=%v", status, err)
	}
}

func TestInspectBridgeTrafficLimitAcceptsProductionExpandedActions(t *testing.T) {
	previous := runTrafficControlCommand
	t.Cleanup(func() { runTrafficControlCommand = previous })
	var filters strings.Builder
	for _, bypass := range appTrafficTCUploadBypassFilters {
		fmt.Fprintf(&filters, "filter protocol %s pref %s flower\n action order 1: gact action pass\n", bypass.protocol, bypass.pref)
	}
	filters.WriteString("filter protocol all pref 49152 matchall chain 0\n")
	filters.WriteString(" action order 1: police 0x1 rate 1024Kbit burst 128Kb mtu 64Kb action drop\n")
	filters.WriteString(" action order 2: gact action pass")
	runTrafficControlCommand = func(_ context.Context, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "qdisc" && args[1] == "show" {
			return "qdisc noqueue 0: root refcnt 2\nqdisc clsact ffff: parent ffff:fff1", nil
		}
		return filters.String(), nil
	}
	status, err := inspectBridgeTrafficLimit(context.Background(), "lzc-br-example", AppTrafficLimit{UploadKbps: 1024})
	if err != nil || !status.InSync || status.Diagnostic != "" {
		t.Fatalf("status=%#v err=%v", status, err)
	}
}

func TestLimiterRepairsCachedRuleWhenKernelRuleDrifts(t *testing.T) {
	previousInspect := runTrafficControlCommand
	previousApply := applyBridgeTrafficLimitFunc
	t.Cleanup(func() {
		runTrafficControlCommand = previousInspect
		applyBridgeTrafficLimitFunc = previousApply
	})
	applyCalls := 0
	runTrafficControlCommand = func(_ context.Context, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "qdisc" && args[1] == "show" {
			return "qdisc noqueue 0: root refcnt 2", nil
		}
		return "", nil
	}
	applyBridgeTrafficLimitFunc = func(_ context.Context, _ string, _ AppTrafficLimit) error {
		applyCalls++
		return nil
	}
	limiter := newAppTrafficLimiter()
	limiter.applied["lzc-br-example"] = AppTrafficLimit{UploadKbps: 1000}
	limiter.runtime["lzc-br-example"] = appTrafficLimitRuntime{
		Desired: AppTrafficLimit{UploadKbps: 1000}, Applied: AppTrafficLimit{UploadKbps: 1000},
		InSync: true, CheckedAt: time.Now(),
	}
	if err := limiter.inspectAndRepair(context.Background(), "lzc-br-example", AppTrafficLimit{UploadKbps: 1000}, true); err != nil {
		t.Fatal(err)
	}
	if applyCalls != 1 {
		t.Fatalf("repair apply calls=%d, want 1", applyCalls)
	}
}
