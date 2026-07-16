package probe

import (
	"os"
	"os/exec"

	"netwatch/internal/dockerlzc"
	"netwatch/internal/lzcsdk"
)

// CapabilityReport describes runtime features available in the current environment.
type CapabilityReport struct {
	GeneratedAt          string   `json:"generated_at"`
	HostNetworkLikely    bool     `json:"host_network_likely"`
	MTR                  bool     `json:"mtr"`
	Nmcli                bool     `json:"nmcli"`
	Iptables             bool     `json:"iptables"`
	Nsenter              bool     `json:"nsenter"`
	DockerSocket         bool     `json:"docker_socket"`
	LazycatBridgeTraffic bool     `json:"lazycat_bridge_traffic"`
	ContainerControl     bool     `json:"container_control"`
	NetworkConfig        bool     `json:"network_config"`
	HostBridge           bool     `json:"host_bridge"`
	Trace                bool     `json:"trace"`
	AppTraffic           bool     `json:"app_traffic"`
	LANDiscovery         bool     `json:"lan_discovery"`
	Notes                []string `json:"notes,omitempty"`
}

func binaryAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// nmcliTransportAvailable reports whether network_config / host bridge can run
// nmcli commands. On Lazycat this is true via lzcsdk even when no nmcli binary
// exists inside the app image.
func nmcliTransportAvailable() bool {
	if lzcsdk.Available() {
		return true
	}
	return binaryAvailable("nmcli")
}

func (s *Service) Capabilities() CapabilityReport {
	findBinPaths()
	mtrOK := binaryAvailable("mtr")
	nmcliOK := nmcliTransportAvailable()
	iptOK := iptablesAvailable()
	nsenterOK := nsenterAvailable()
	dockerOK := dockerlzc.Available()
	// App bridge traffic is readable whenever /sys/class/net is visible; mapping to
	// app titles benefits from docker socket but is not strictly required.
	appTrafficOK := true
	if _, err := os.Stat("/sys/class/net"); err != nil {
		appTrafficOK = false
	}
	report := CapabilityReport{
		GeneratedAt:          localTimestamp(),
		HostNetworkLikely:    true, // best-effort; host network is the supported deploy mode
		MTR:                  mtrOK,
		Nmcli:                nmcliOK,
		Iptables:             iptOK,
		Nsenter:              nsenterOK,
		DockerSocket:         dockerOK,
		LazycatBridgeTraffic: appTrafficOK && dockerOK,
		ContainerControl:     dockerOK && (iptOK || nsenterOK),
		NetworkConfig:        nmcliOK,
		HostBridge:           nmcliOK,
		Trace:                mtrOK,
		AppTraffic:           appTrafficOK,
		LANDiscovery:         true,
	}
	if !mtrOK {
		report.Notes = append(report.Notes, "mtr unavailable: route tracing disabled")
	}
	if !nmcliOK {
		report.Notes = append(report.Notes, "nmcli transport unavailable: need lzc-apis (Lazycat) or local nmcli binary")
	}
	if !dockerOK {
		report.Notes = append(report.Notes, "docker socket unavailable: app titles and container control limited")
	}
	if !iptOK && !nsenterOK {
		report.Notes = append(report.Notes, "iptables/nsenter unavailable: container network block disabled")
	}
	return report
}
