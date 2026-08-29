package probe

import (
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilium/ebpf"
)

var (
	hostLimitProbeCgroups  = flag.String("hostlimit-cgroups", "", "comma-separated cgroup paths for the privileged Host limit probe")
	hostLimitProbeUpload   = flag.Int64("hostlimit-upload-kbps", 1000, "probe upload ceiling in Kbit/s")
	hostLimitProbeDownload = flag.Int64("hostlimit-download-kbps", 1000, "probe download ceiling in Kbit/s")
	hostLimitProbeDuration = flag.Duration("hostlimit-duration", 20*time.Second, "time to keep the privileged probe attached")
	hostLimitProbeBypass   = flag.Bool("hostlimit-bypass-private", true, "bypass private, loopback and link-local peers")
)

func TestHostLimitPolicyUsesOneAtomicApplicationRecord(t *testing.T) {
	policy, err := hostLimitPolicy(AppTrafficLimit{UploadKbps: 800, DownloadKbps: 1600}, 7)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Generation != 7 || policy.UploadBytesPerSecond != 100000 || policy.DownloadBytesPerSecond != 200000 {
		t.Fatalf("policy=%#v", policy)
	}
	if policy.UploadBurstBytes == 0 || policy.DownloadBurstBytes == 0 {
		t.Fatalf("policy has empty burst: %#v", policy)
	}
	if policy.UploadBurstBytes < 128*1024 || policy.DownloadBurstBytes < 128*1024 {
		t.Fatalf("policy burst cannot admit a GRO skb: %#v", policy)
	}
	if _, err := hostLimitPolicy(AppTrafficLimit{UploadKbps: -1}, 1); err == nil {
		t.Fatal("negative limit was accepted")
	}
}

func TestHostLimitBPFCollectionShape(t *testing.T) {
	spec, err := loadHostlimit()
	if err != nil {
		t.Fatal(err)
	}
	ingress := spec.Programs["hostlimit_ingress"]
	egress := spec.Programs["hostlimit_egress"]
	if ingress == nil || ingress.Type != ebpf.CGroupSKB || ingress.AttachType != ebpf.AttachCGroupInetIngress {
		t.Fatalf("ingress spec=%#v", ingress)
	}
	if egress == nil || egress.Type != ebpf.CGroupSKB || egress.AttachType != ebpf.AttachCGroupInetEgress {
		t.Fatalf("egress spec=%#v", egress)
	}
	for _, name := range []string{"policies", "upload_states", "download_states"} {
		if spec.Maps[name] == nil || spec.Maps[name].Type != ebpf.Hash {
			t.Fatalf("map %s=%#v", name, spec.Maps[name])
		}
	}
}

func TestHostLimitPrototypePrivileged(t *testing.T) {
	if strings.TrimSpace(*hostLimitProbeCgroups) == "" {
		t.Skip("set -hostlimit-cgroups to run the privileged Lazycat probe")
	}
	if *hostLimitProbeDuration <= 0 || *hostLimitProbeDuration > 2*time.Minute {
		t.Fatalf("invalid probe duration %s", *hostLimitProbeDuration)
	}
	paths := strings.Split(*hostLimitProbeCgroups, ",")
	prototype, err := newHostLimitPrototype()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := prototype.close(); closeErr != nil {
			t.Errorf("close Host limit prototype: %v", closeErr)
		}
	}()
	limit := AppTrafficLimit{UploadKbps: *hostLimitProbeUpload, DownloadKbps: *hostLimitProbeDownload}
	if err := prototype.stage(paths, limit, uint64(time.Now().UnixNano()), *hostLimitProbeBypass); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("HOSTLIMIT_READY upload_kbps=%d download_kbps=%d cgroups=%d bypass_private=%t\n", limit.UploadKbps, limit.DownloadKbps, len(paths), *hostLimitProbeBypass)
	time.Sleep(*hostLimitProbeDuration)
	snapshot, err := prototype.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"upload": map[string]uint64{
			"passed_bytes": snapshot.Upload.PassedBytes, "passed_packets": snapshot.Upload.PassedPackets,
			"dropped_bytes": snapshot.Upload.DroppedBytes, "dropped_packets": snapshot.Upload.DroppedPackets,
		},
		"download": map[string]uint64{
			"passed_bytes": snapshot.Download.PassedBytes, "passed_packets": snapshot.Download.PassedPackets,
			"dropped_bytes": snapshot.Download.DroppedBytes, "dropped_packets": snapshot.Download.DroppedPackets,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("HOSTLIMIT_RESULT %s\n", body)
}
