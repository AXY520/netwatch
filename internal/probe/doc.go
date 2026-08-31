// Package probe is the domain core of netwatch.
//
// File layout (same package, split by responsibility):
//
//	service.go          — Service lifecycle, background workers, pubsub
//	config.go           — runtime config load/validate
//	types.go            — shared DTOs / API payloads
//	settings*.go        — mutable settings store
//
//	observe_ops.go      — summary / connectivity / egress observation
//	netinfo.go          — host network info collection
//	netstats.go         — NIC realtime stats
//	connectivity.go     — website connectivity probes
//	nat.go              — STUN NAT detection
//	egress_*.go         — public egress IP / domestic lookups
//	ipv6_*.go           — IPv6 availability / renew
//
//	lan_hub.go          — LAN policy + in-memory device hub
//	lan_api.go          — LAN public Service methods (get/scan/meta)
//	lan_scan.go         — multi-source discovery pipeline
//	lan_detect.go       — ICMP / DHCP / mDNS / IPv6 NDP helpers
//	lan_neighbors.go    — ARP / netlink neighbor table readers
//	lan_iface.go        — LAN iface discovery + link-state monitor
//	lan_store.go        — lan_devices.json persistence
//	lan_identity.go     — hostname reverse lookup + OUI vendor
//
//	broadband_*.go      — public broadband node catalog / server test engine
//	task_*.go           — async server broadband / trace task runtime
//	trace.go            — mtr-based path diagnostics
//
//	apptraffic*.go      — lazycat bridge traffic
//	traffic_ops.go      — traffic API ops
//	ports.go            — host listening ports
//	network_config.go   — apply/confirm/rollback host IP config
//	host_bridge.go       — VM L2 host bridge (nw-* prefix)
//	containerctl.go     — container network block/unblock
//	control_*.go        — control-plane state
//	notify_*.go         — notification hub (Bark/PushPlus/webhook)
//	capabilities.go     — feature capability reporting
//	timeseries.go       — rate history store
//	history.go          — speedtest history
//	hostnet_bpf.go      — optional host-net BPF helpers
//	sysutil.go / util.go / atomicfile.go — small shared utilities
package probe
