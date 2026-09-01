package probe

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"golang.org/x/sys/unix"

	"netwatch/internal/dockerlzc"
)

func TestHostNetworkExperimentalDefaultsDisabled(t *testing.T) {
	if DefaultMutableSettings().HostNetworkExperimentalEnabled {
		t.Fatal("host network experimental controls must be disabled by default")
	}
}

func TestHostBPFMapPinRootAtSelectsWritableBPFSMount(t *testing.T) {
	statfs := func(path string, stat *unix.Statfs_t) error {
		if path == "/not-bpf" {
			stat.Type = 0xEF53
			return nil
		}
		stat.Type = 0xcafe4a11
		return nil
	}
	var created string
	root, ok := hostBPFMapPinRootAt([]string{"/not-bpf", "/bpf"}, func(path string, _ os.FileMode) error {
		created = path
		return nil
	}, statfs)
	if !ok || root != "/bpf/netwatch" || created != root {
		t.Fatalf("root=%q ok=%v created=%q", root, ok, created)
	}
}

func TestHostBPFMapPinRootAtSkipsUnavailableMounts(t *testing.T) {
	statfs := func(path string, stat *unix.Statfs_t) error {
		if path == "/missing" {
			return errors.New("missing")
		}
		stat.Type = 0xcafe4a11
		return nil
	}
	root, ok := hostBPFMapPinRootAt([]string{"/missing", "/bpf"}, func(string, os.FileMode) error {
		return errors.New("read only")
	}, statfs)
	if ok || root != "" {
		t.Fatalf("root=%q ok=%v, want no usable mount", root, ok)
	}
}

func TestHostBPFMountCommandEntersHostMountNamespaceAndRoot(t *testing.T) {
	want := []string{"-t", "1", "-m", "-r", "--", "/usr/bin/mount", "-t", "bpf", "bpf", "/sys/fs/bpf"}
	if got := hostBPFMountCommandArgs("/usr/bin/mount"); !reflect.DeepEqual(got, want) {
		t.Fatalf("mount args=%#v want=%#v", got, want)
	}
}

func TestJoinHostBPFPinNotesDeduplicatesSameFallback(t *testing.T) {
	const note = "未找到可写 bpffs，Host 统计 map 将在 Netwatch 重启时重置"
	if got := joinHostBPFPinNotes(note, note); got != note {
		t.Fatalf("note=%q", got)
	}
	if got := joinHostBPFPinNotes(" ingress ", "egress"); got != "ingress；egress" {
		t.Fatalf("note=%q", got)
	}
}

func TestHostCgroupBPFPrunePlanRemovesStaleAndReusedCgroups(t *testing.T) {
	attachments := map[string]hostCgroupBPFAttachment{
		"/host/cgroup/current": {cgroupID: 11},
		"/host/cgroup/reused":  {cgroupID: 22},
		"/host/cgroup/stopped": {cgroupID: 33},
	}
	active := map[string]uint64{
		"/host/cgroup/current": 11,
		"/host/cgroup/reused":  222,
	}
	stalePaths, staleIDs := hostCgroupBPFPrunePlan(attachments, []uint64{11, 22, 33, 44, 222}, active)
	if got, want := strings.Join(stalePaths, ","), "/host/cgroup/reused,/host/cgroup/stopped"; got != want {
		t.Fatalf("stale paths=%q want=%q", got, want)
	}
	if !slices.Equal(staleIDs, []uint64{22, 33, 44}) {
		t.Fatalf("stale map ids=%v", staleIDs)
	}
}

func TestHostAppTargetRoundTrip(t *testing.T) {
	target := hostAppTarget("cloud.lazycat.app.example")
	if target != "host-app:cloud.lazycat.app.example" {
		t.Fatalf("target=%q", target)
	}
	appID, ok := hostAppIDFromTarget(target)
	if !ok || appID != "cloud.lazycat.app.example" {
		t.Fatalf("round trip appID=%q ok=%v", appID, ok)
	}
	if _, ok := hostAppIDFromTarget("lzc-br-example"); ok {
		t.Fatal("bridge target must not parse as host target")
	}
}

func TestPruneHostNetworkExperimentalState(t *testing.T) {
	got := pruneHostNetworkExperimentalState(map[string]string{
		"host-app:cloud.lazycat.app.one": "internet",
		"lzc-br-abc":                     "internet",
	})
	want := map[string]string{"lzc-br-abc": "internet"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestHostIptablesRuleArgs(t *testing.T) {
	got := hostIptablesRuleArgs("system.slice/lzcapp.slice/demo.slice", "192.168.0.0/16", "ACCEPT")
	want := []string{"-m", "cgroup", "--path", "system.slice/lzcapp.slice/demo.slice", "-d", "192.168.0.0/16", "-j", "ACCEPT"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestHostFirewallCgroupPathsIncludeLazycatExecTree(t *testing.T) {
	const canonical = "system.slice/runc-lzc-os.scope/lzcapp.slice/lzcapp-cloud.lazycat.app.demo.slice"
	const alternate = "lzcapp.slice/lzcapp-cloud.lazycat.app.demo.slice"
	got := hostFirewallCgroupPathsAt(canonical, func(path string) bool { return path == alternate })
	want := []string{canonical, alternate}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths=%#v want=%#v", got, want)
	}
	got = hostFirewallCgroupPathsAt(canonical, func(string) bool { return false })
	if !reflect.DeepEqual(got, []string{canonical}) {
		t.Fatalf("paths without exec tree=%#v", got)
	}
}

func TestAppTrafficControlCapabilities(t *testing.T) {
	tests := []struct {
		name         string
		entry        AppTrafficUsage
		wantTopology string
		wantLimit    bool
		wantInternet bool
	}{
		{
			name:         "bridge",
			entry:        AppTrafficUsage{AppID: "cloud.lazycat.app.bridge", Bridges: []string{"lzc-br-bridge"}, NetworkModes: []string{"bridge"}},
			wantTopology: "bridge",
			wantLimit:    true,
			wantInternet: true,
		},
		{
			name:         "host",
			entry:        AppTrafficUsage{AppID: "cloud.lazycat.app.host", Bridges: []string{"host-app:cloud.lazycat.app.host"}, NetworkModes: []string{"host"}},
			wantTopology: "host",
			wantLimit:    true,
			wantInternet: true,
		},
		{
			name: "mixed",
			entry: AppTrafficUsage{
				AppID:        "cloud.lazycat.app.mixed",
				Bridges:      []string{"lzc-br-mixed", "host-app:cloud.lazycat.app.mixed"},
				NetworkModes: []string{"bridge", "host"},
			},
			wantTopology: "mixed",
			wantLimit:    true,
			wantInternet: true,
		},
		{
			name: "whitelisted mixed",
			entry: AppTrafficUsage{
				AppID:        "cloud.lazycat.shell.files",
				AppTitle:     "懒猫网盘",
				Bridges:      []string{"lzc-br-files", "host-app:cloud.lazycat.shell.files"},
				NetworkModes: []string{"bridge", "host"},
			},
			wantTopology: "mixed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			topology, limitAllowed, internetAllowed := appTrafficControlCapabilities(test.entry, true)
			if topology != test.wantTopology || limitAllowed != test.wantLimit || internetAllowed != test.wantInternet {
				t.Fatalf("got topology=%q limit=%v internet=%v", topology, limitAllowed, internetAllowed)
			}
		})
	}
}

func TestAppTrafficControlCapabilitiesHonorsMissingTC(t *testing.T) {
	entry := AppTrafficUsage{
		AppID:        "cloud.lazycat.app.bridge",
		Bridges:      []string{"lzc-br-bridge"},
		NetworkModes: []string{"bridge"},
	}
	topology, limitAllowed, internetAllowed := appTrafficControlCapabilities(entry, false)
	if topology != "bridge" || limitAllowed || !internetAllowed {
		t.Fatalf("got topology=%q limit=%v internet=%v", topology, limitAllowed, internetAllowed)
	}
}

func TestHostTrafficRemainsVisibleWhenExperimentalControlsAreDisabled(t *testing.T) {
	state := newAppTrafficState(t.TempDir())
	now := time.Date(2026, 8, 27, 9, 0, 0, 0, time.Local)
	appID := "cloud.lazycat.app.host"
	state.sample([]AppBridgeStats{
		{
			Bridge:         hostAppTarget(appID),
			AppID:          appID,
			AppTitle:       "Host 应用",
			NetworkMode:    "host",
			UploadBytes:    1024,
			DownloadBytes:  2048,
			ContainerCount: 1,
			RunningCount:   1,
		},
	}, now)

	overview := state.overviewForActiveAppsWithControls(true, map[string]bool{appID: true}, false)
	if len(overview.Apps) != 1 {
		t.Fatalf("apps=%#v, want Host application to remain visible", overview.Apps)
	}
	app := overview.Apps[0]
	if app.NetworkTopology != "host" {
		t.Fatalf("topology=%q, want host", app.NetworkTopology)
	}
	if app.TrafficLimitAllowed || app.InternetControlAllowed {
		t.Fatalf("controls must be disabled: limit=%v internet=%v", app.TrafficLimitAllowed, app.InternetControlAllowed)
	}
	if app.TotalUpload != 1024 || app.TotalDownload != 2048 {
		t.Fatalf("traffic totals=%d/%d, want 1024/2048", app.TotalUpload, app.TotalDownload)
	}
}

func TestExperimentalSwitchDoesNotDisablePureBridgeControls(t *testing.T) {
	entry := AppTrafficUsage{
		AppID:        "cloud.lazycat.app.bridge",
		Bridges:      []string{"lzc-br-bridge"},
		NetworkModes: []string{"bridge"},
	}
	topology, limitAllowed, internetAllowed := appTrafficControlCapabilitiesWithControls(entry, true, false)
	if topology != "bridge" || !limitAllowed || !internetAllowed {
		t.Fatalf("got topology=%q limit=%v internet=%v", topology, limitAllowed, internetAllowed)
	}
}

func TestAppTrafficControlCapabilitiesPreferNetworkTargets(t *testing.T) {
	entry := AppTrafficUsage{
		AppID:        "cloud.lazycat.app.bridge",
		Bridges:      []string{"host-app:stale-legacy-value"},
		NetworkModes: []string{"host"},
		NetworkTargets: []AppNetworkTarget{{
			ID: "lzc-br-current", Kind: AppNetworkTargetBridge, AppID: "cloud.lazycat.app.bridge",
		}},
	}
	topology, limitAllowed, internetAllowed := appTrafficControlCapabilitiesWithControls(entry, true, false)
	if topology != "bridge" || !limitAllowed || !internetAllowed {
		t.Fatalf("got topology=%q limit=%v internet=%v", topology, limitAllowed, internetAllowed)
	}
}

func TestHostCgroupProgramInstructionsContainNoInvalidOpcode(t *testing.T) {
	for _, attach := range []ebpf.AttachType{ebpf.AttachCGroupInetIngress, ebpf.AttachCGroupInetEgress} {
		instructions := hostCgroupProgramInstructions(attach, 1)
		if len(instructions) == 0 {
			t.Fatalf("attach=%v generated no instructions", attach)
		}
		for index, instruction := range instructions {
			if instruction.OpCode == asm.InvalidOpCode {
				t.Fatalf("attach=%v instruction %d has invalid opcode", attach, index)
			}
		}
		var encoded bytes.Buffer
		if err := instructions.Marshal(&encoded, binary.LittleEndian); err != nil {
			t.Fatalf("attach=%v marshal failed: %v", attach, err)
		}
	}
}

func TestHostCgroupProgramsUseSocketCgroupForBothDirections(t *testing.T) {
	want := asm.FnSkbCgroupId.Call()
	for _, attach := range []ebpf.AttachType{ebpf.AttachCGroupInetIngress, ebpf.AttachCGroupInetEgress} {
		instructions := cgroupIDInstructions(attach)
		found := slices.ContainsFunc(instructions, func(instruction asm.Instruction) bool {
			return instruction.OpCode == want.OpCode && instruction.Constant == want.Constant
		})
		if !found {
			t.Fatalf("attach=%v does not resolve the cgroup from skb->sk: %#v", attach, instructions)
		}
		current := asm.FnGetCurrentCgroupId.Call()
		if slices.ContainsFunc(instructions, func(instruction asm.Instruction) bool {
			return instruction.OpCode == current.OpCode && instruction.Constant == current.Constant
		}) {
			t.Fatalf("attach=%v still uses the current task cgroup", attach)
		}
	}
}

func TestHostTrafficDiagnosticReturnsConcreteFailure(t *testing.T) {
	items := []AppBridgeStats{
		{Source: "linux_bridge_sysfs"},
		{Source: "cgroup_skb_ebpf_unavailable", Diagnostic: "加载 ingress cgroup eBPF 失败: invalid opcode"},
	}
	if got := hostTrafficDiagnostic(items); got != "加载 ingress cgroup eBPF 失败: invalid opcode" {
		t.Fatalf("diagnostic=%q", got)
	}
}

func TestHostTrafficAvailabilityNoteReportsPartialFailure(t *testing.T) {
	items := []AppBridgeStats{
		{Source: "cgroup_skb_ebpf"},
		{Source: "cgroup_skb_ebpf"},
		{Source: "cgroup_skb_ebpf_unavailable", Diagnostic: "missing nested cgroup"},
	}
	got := hostTrafficAvailabilityNote(items)
	want := "Host 模式流量统计部分不可用：1/3 个 Host 容器不可用（missing nested cgroup）；其余 Host 与 Bridge 流量统计不受影响。"
	if got != want {
		t.Fatalf("note=%q want=%q", got, want)
	}
}

func TestHostTrafficAvailabilityNoteReportsCompleteFailure(t *testing.T) {
	items := []AppBridgeStats{
		{Source: "cgroup_skb_ebpf_unavailable", Diagnostic: "attach failed"},
		{Source: "cgroup_skb_ebpf_unavailable", Diagnostic: "attach failed"},
	}
	got := hostTrafficAvailabilityNote(items)
	want := "Host 模式流量统计不可用：attach failed；Bridge 流量统计不受影响。"
	if got != want {
		t.Fatalf("note=%q want=%q", got, want)
	}
}

func TestResolveContainerHostCgroupPathFallsBackToProcessCgroup(t *testing.T) {
	container := dockerlzc.ContainerRuntimeInfo{
		CgroupPath: "/stale/docker/path",
		PID:        1234,
	}
	path, diagnostic := resolveContainerHostCgroupPath(
		container,
		func(pid int) (string, error) {
			if pid != 1234 {
				t.Fatalf("pid=%d", pid)
			}
			return "/actual/process/path", nil
		},
		func(candidate string) string {
			if candidate == "/actual/process/path" {
				return "/host/sys/fs/cgroup/actual/process/path"
			}
			return ""
		},
	)
	if path != "/host/sys/fs/cgroup/actual/process/path" || diagnostic != "" {
		t.Fatalf("path=%q diagnostic=%q", path, diagnostic)
	}
}

func TestResolveContainerHostCgroupPathUsesLazycatLayoutFallback(t *testing.T) {
	container := dockerlzc.ContainerRuntimeInfo{
		ID:    "abc123",
		AppID: "cloud.lazycat.app.example",
		PID:   1234,
	}
	wantCandidate := "/system.slice/runc-lzc-os.scope/lzcapp.slice/lzcapp-cloud.lazycat.app.example.slice/docker-abc123.scope"
	path, diagnostic := resolveContainerHostCgroupPath(
		container,
		func(int) (string, error) { return "", errors.New("proc unavailable") },
		func(candidate string) string {
			if candidate == wantCandidate {
				return "/host/sys/fs/cgroup" + candidate
			}
			return ""
		},
	)
	if path != "/host/sys/fs/cgroup"+wantCandidate || diagnostic != "" {
		t.Fatalf("path=%q diagnostic=%q", path, diagnostic)
	}
}

func TestResolveContainerHostCgroupPathExpandsSystemdSliceHierarchy(t *testing.T) {
	container := dockerlzc.ContainerRuntimeInfo{
		ID:    "abc123",
		AppID: "community.lazycat.czyt.rustdesk-server",
		PID:   1234,
	}
	wantCandidate := "/system.slice/runc-lzc-os.scope/lzcapp.slice/lzcapp-community.lazycat.czyt.rustdesk.slice/lzcapp-community.lazycat.czyt.rustdesk-server.slice/docker-abc123.scope"
	path, diagnostic := resolveContainerHostCgroupPath(
		container,
		func(int) (string, error) { return "", errors.New("proc unavailable") },
		func(candidate string) string {
			if candidate == wantCandidate {
				return "/host/sys/fs/cgroup" + candidate
			}
			return ""
		},
	)
	if path != "/host/sys/fs/cgroup"+wantCandidate || diagnostic != "" {
		t.Fatalf("path=%q diagnostic=%q", path, diagnostic)
	}
}

func TestHostCgroupCandidatePathsRebasesPrivateNamespacePath(t *testing.T) {
	input := "/../../lzcapp-community.lazycat.czyt.rustdesk.slice/lzcapp-community.lazycat.czyt.rustdesk-server.slice/docker-abc123.scope"
	want := "/system.slice/runc-lzc-os.scope/lzcapp.slice/lzcapp-community.lazycat.czyt.rustdesk.slice/lzcapp-community.lazycat.czyt.rustdesk-server.slice/docker-abc123.scope"
	if got := hostCgroupCandidatePaths(input); !slices.Contains(got, want) {
		t.Fatalf("candidates=%v want %q", got, want)
	}
}

func TestResolveContainerHostCgroupPathReturnsConcreteDiagnostic(t *testing.T) {
	container := dockerlzc.ContainerRuntimeInfo{PID: 1234}
	path, diagnostic := resolveContainerHostCgroupPath(
		container,
		func(int) (string, error) { return "", errors.New("permission denied") },
		func(string) string { return "" },
	)
	if path != "" {
		t.Fatalf("path=%q", path)
	}
	if diagnostic != "未找到容器 cgroup 路径：permission denied" {
		t.Fatalf("diagnostic=%q", diagnostic)
	}
}
