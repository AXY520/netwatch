package probe

import (
	"testing"

	"github.com/cilium/ebpf"
	tcnetlink "github.com/vishvananda/netlink"
)

func TestHostLimitConfigUsesSharedApplicationRecord(t *testing.T) {
	config, err := hostLimitConfig(AppTrafficLimit{UploadKbps: 800, DownloadKbps: 1600}, 7, 9)
	if err != nil {
		t.Fatal(err)
	}
	if config.Generation != 7 || config.DownloadBytesPerSecond != 200000 || config.UploadMark != 9<<16 || config.MarkMask != hostTrafficLimitMarkMask {
		t.Fatalf("config=%#v", config)
	}
	if config.DownloadBurstBytes < 128*1024 {
		t.Fatalf("download burst cannot admit a GRO skb: %d", config.DownloadBurstBytes)
	}
	if _, err := hostLimitConfig(AppTrafficLimit{UploadKbps: -1}, 1, 1); err == nil {
		t.Fatal("negative limit was accepted")
	}
	if _, err := hostLimitConfig(AppTrafficLimit{}, 0, 1); err == nil {
		t.Fatal("zero generation was accepted")
	}
}

func TestHostLimitBPFCollectionShape(t *testing.T) {
	spec, err := loadHostlimit()
	if err != nil {
		t.Fatal(err)
	}
	tagger := spec.Programs["hostlimit_tag_socket"]
	if tagger == nil || tagger.Type != ebpf.CGroupSock || tagger.AttachType != ebpf.AttachCGroupInetSockCreate {
		t.Fatalf("tagger spec=%#v", tagger)
	}
	for _, name := range []string{"hostlimit_tc_ingress", "hostlimit_tc_egress"} {
		program := spec.Programs[name]
		if program == nil || program.Type != ebpf.SchedCLS {
			t.Fatalf("program %s=%#v", name, program)
		}
	}
	mapTypes := map[string]ebpf.MapType{
		"config": ebpf.Array, "download_states": ebpf.Array, "socket_tags": ebpf.SkStorage,
		"bridge_ifindexes": ebpf.Hash, "flows": ebpf.LRUHash, "local_upload_bytes": ebpf.Array,
	}
	for name, want := range mapTypes {
		if spec.Maps[name] == nil || spec.Maps[name].Type != want {
			t.Fatalf("map %s=%#v want=%v", name, spec.Maps[name], want)
		}
	}
}

func TestAcceptedHostUploadBytesUsesPostPoliceAction(t *testing.T) {
	police := tcnetlink.NewPoliceAction()
	police.ActionAttrs.Statistics = &tcnetlink.ActionStatistic{
		Basic: &tcnetlink.GnetStatsBasic{Bytes: 21_254_751},
	}
	clearMark := tcnetlink.NewSkbEditAction()
	clearMark.ActionAttrs.Statistics = &tcnetlink.ActionStatistic{
		Basic: &tcnetlink.GnetStatsBasic{Bytes: 13_018_945},
	}
	instance := &hostTrafficLimitInstance{slot: 7, limit: AppTrafficLimit{UploadKbps: 1000}}
	filters := []tcnetlink.Filter{&tcnetlink.FwFilter{
		FilterAttrs: tcnetlink.FilterAttrs{Handle: 7 << 16, Priority: hostTrafficLimitPolicePreference},
		Actions:     []tcnetlink.Action{police, clearMark},
	}}

	got, ok := acceptedHostUploadBytes(filters, instance)
	if !ok || got != 13_018_945 {
		t.Fatalf("accepted bytes=%d ok=%v, want post-police skbedit counter", got, ok)
	}
}

func TestAcceptedHostUploadBytesRejectsPrePoliceOrWrongFilter(t *testing.T) {
	clearMark := tcnetlink.NewSkbEditAction()
	clearMark.ActionAttrs.Statistics = &tcnetlink.ActionStatistic{
		Basic: &tcnetlink.GnetStatsBasic{Bytes: 1234},
	}
	instance := &hostTrafficLimitInstance{slot: 3, limit: AppTrafficLimit{UploadKbps: 1000}}
	filters := []tcnetlink.Filter{&tcnetlink.FwFilter{
		FilterAttrs: tcnetlink.FilterAttrs{Handle: 4 << 16, Priority: hostTrafficLimitPolicePreference},
		Actions:     []tcnetlink.Action{clearMark},
	}}
	if got, ok := acceptedHostUploadBytes(filters, instance); ok || got != 0 {
		t.Fatalf("accepted bytes=%d ok=%v for unrelated filter", got, ok)
	}
}
