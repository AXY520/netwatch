package probe

import (
	"netwatch/internal/dockerlzc"
	"testing"
	"time"
)

func TestDockerLifecycleEventRelevant(t *testing.T) {
	tests := []struct {
		event dockerlzc.Event
		want  bool
	}{
		{event: dockerlzc.Event{Type: "container", Action: "start"}, want: true},
		{event: dockerlzc.Event{Type: "container", Action: "die"}, want: true},
		{event: dockerlzc.Event{Type: "network", Action: "connect"}, want: true},
		{event: dockerlzc.Event{Type: "container", Action: "exec_start"}, want: false},
		{event: dockerlzc.Event{Type: "container", Action: "health_status: healthy"}, want: false},
		{event: dockerlzc.Event{Type: "volume", Action: "create"}, want: false},
	}
	for _, test := range tests {
		if got := dockerLifecycleEventRelevant(test.event); got != test.want {
			t.Errorf("dockerLifecycleEventRelevant(%+v) = %v, want %v", test.event, got, test.want)
		}
	}
}

func TestNetworkEventStorePersistsAndDeduplicates(t *testing.T) {
	dir := t.TempDir()
	store := newNetworkEventStore(dir)
	first := store.append(NetworkEvent{Kind: "interface_state_changed", DedupeKey: "iface:wlan0", Title: "changed", Summary: "down"})
	store.append(NetworkEvent{Kind: "egress_ipv4_changed", DedupeKey: "egress", Title: "egress", Summary: "changed"})
	second := store.append(NetworkEvent{Kind: "interface_state_changed", DedupeKey: "iface:wlan0", Title: "changed", Summary: "up"})
	if first.ID != second.ID || second.Count != 2 || second.Summary != "up" {
		t.Fatalf("dedupe result = %+v", second)
	}

	reloaded := newNetworkEventStore(dir)
	events := reloaded.query(NetworkEventQuery{Limit: 10})
	if len(events) != 2 || events[0].Count != 2 || events[0].ID != first.ID {
		t.Fatalf("reloaded events = %+v", events)
	}
}

func TestNetworkEventStoreFilters(t *testing.T) {
	store := newNetworkEventStore(t.TempDir())
	store.append(NetworkEvent{Kind: "egress_ipv4_changed", Severity: "info"})
	store.append(NetworkEvent{Kind: "nat_type_changed", Severity: "warning"})
	got := store.query(NetworkEventQuery{Severity: "warning", Since: time.Now().Add(-time.Minute), Limit: 10})
	if len(got) != 1 || got[0].Kind != "nat_type_changed" {
		t.Fatalf("filtered events = %+v", got)
	}
}

func TestRecordSummaryEventsCapturesNetworkChanges(t *testing.T) {
	service := &Service{events: newNetworkEventStore(t.TempDir())}
	previous := Summary{
		Ready:               true,
		WebsiteConnectivity: WebsiteConnectivity{GlobalStatus: StatusOK, DomesticStatus: StatusOK},
		NetworkInfo: NetworkInfo{
			EgressIPv4:  "1.1.1.1",
			DefaultIPv4: DefaultRoute{Gateway: "192.168.1.1"},
			Interfaces:  []InterfaceInfo{{Name: "wlan0", OperState: "up"}},
		},
	}
	current := previous
	current.NetworkInfo.EgressIPv4 = "2.2.2.2"
	current.NetworkInfo.Interfaces = []InterfaceInfo{{Name: "wlan0", OperState: "down"}}
	service.recordSummaryEvents(previous, current)

	events := service.NetworkEvents(NetworkEventQuery{Limit: 10})
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	kinds := NetworkEventKinds(events)
	if len(kinds) != 2 || kinds[0] != "egress_ipv4_changed" || kinds[1] != "interface_state_changed" {
		t.Fatalf("kinds = %v", kinds)
	}
}

func TestRecordSummaryEventsCollapsesIPv6PrivacyAddressesToPrefix(t *testing.T) {
	service := &Service{events: newNetworkEventStore(t.TempDir())}
	previous := Summary{Ready: true, NetworkInfo: NetworkInfo{Interfaces: []InterfaceInfo{{Name: "wlan0", IPv6: []string{"2001:db8:1::10/64"}}}}}
	current := Summary{Ready: true, NetworkInfo: NetworkInfo{Interfaces: []InterfaceInfo{{Name: "wlan0", IPv6: []string{"2001:db8:1::20/64"}}}}}
	service.recordSummaryEvents(previous, current)
	if events := service.NetworkEvents(NetworkEventQuery{Limit: 10}); len(events) != 0 {
		t.Fatalf("privacy address rotation must not create prefix event: %+v", events)
	}
	current.NetworkInfo.Interfaces[0].IPv6 = []string{"2001:db8:2::20/64"}
	service.recordSummaryEvents(previous, current)
	events := service.NetworkEvents(NetworkEventQuery{Limit: 10})
	if len(events) != 1 || events[0].Kind != "ipv6_prefix_changed" {
		t.Fatalf("events = %+v", events)
	}
}

func TestObserveAppTrafficReportsLifecycleAndThreshold(t *testing.T) {
	store := newNetworkEventStore(t.TempDir())
	start := time.Date(2026, 8, 4, 12, 0, 0, 0, time.Local)
	store.observeAppTraffic([]AppBridgeStats{{Bridge: "lzc-br-a", AppID: "app.a", AppTitle: "应用甲", ContainerCount: 1, RunningCount: 1, RxBytes: 100, TxBytes: 100}}, start, 10)
	store.observeAppTraffic([]AppBridgeStats{
		{Bridge: "lzc-br-a", AppID: "app.a", AppTitle: "应用甲", ContainerCount: 1, RunningCount: 1, RxBytes: 20_000_100, TxBytes: 20_000_100},
		{Bridge: "lzc-br-b", AppID: "app.b", AppTitle: "应用乙", ContainerCount: 1, RunningCount: 1, CreatedAt: start.Add(7 * time.Second).Unix(), RxBytes: 0, TxBytes: 0},
	}, start.Add(10*time.Second), 10)
	events := store.query(NetworkEventQuery{Limit: 10})
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	kinds := NetworkEventKinds(events)
	if kinds[0] != "app_enabled" || kinds[1] != "app_traffic_high" {
		t.Fatalf("kinds = %v", kinds)
	}
	enabled := store.query(NetworkEventQuery{Kind: "app_enabled", Limit: 10})
	if len(enabled) != 1 || enabled[0].Timestamp != start.Add(7*time.Second).Format(time.DateTime) {
		t.Fatalf("enabled events = %+v", enabled)
	}
	store.observeAppTraffic([]AppBridgeStats{{Bridge: "lzc-br-b", AppID: "app.b", AppTitle: "应用乙", ContainerCount: 1, RunningCount: 1}}, start.Add(20*time.Second), 10)
	events = store.query(NetworkEventQuery{Kind: "app_disabled", Limit: 10})
	if len(events) != 1 || events[0].Summary != "应用甲" || events[0].Title != "应用已停用" {
		t.Fatalf("disappearance events = %+v", events)
	}
}

func TestObserveAppTrafficUsesRunningStateInsteadOfBridgePresence(t *testing.T) {
	store := newNetworkEventStore(t.TempDir())
	start := time.Date(2026, 8, 4, 12, 0, 0, 0, time.Local)
	store.observeAppTraffic([]AppBridgeStats{{Bridge: "lzc-br-a", AppTitle: "应用甲", ContainerCount: 1, RunningCount: 1}}, start, 0)
	store.observeAppTraffic([]AppBridgeStats{{Bridge: "lzc-br-a", AppTitle: "应用甲", ContainerCount: 1, RunningCount: 0}}, start.Add(10*time.Second), 0)
	store.observeAppTraffic([]AppBridgeStats{{Bridge: "lzc-br-a", AppTitle: "应用甲", ContainerCount: 1, RunningCount: 1, CreatedAt: start.Add(18 * time.Second).Unix()}}, start.Add(20*time.Second), 0)
	events := store.query(NetworkEventQuery{Limit: 10})
	if len(events) != 2 || events[0].Kind != "app_enabled" || events[1].Kind != "app_disabled" {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Timestamp != start.Add(18*time.Second).Format(time.DateTime) || events[1].Timestamp != start.Add(10*time.Second).Format(time.DateTime) {
		t.Fatalf("event timestamps = %+v", events)
	}
}

func TestObserveAppTrafficGroupsLifecycleByApplication(t *testing.T) {
	store := newNetworkEventStore(t.TempDir())
	start := time.Date(2026, 8, 5, 9, 30, 0, 0, time.Local)
	app := "cloud.lazycat.app.browser"
	project := "cloudlazycatappbrowser"
	store.observeAppTraffic([]AppBridgeStats{
		{Bridge: "lzc-br-old", AppID: app, Project: project, ContainerCount: 0, RunningCount: 0},
		{Bridge: "lzc-br-current", AppID: app, Project: project, ContainerCount: 2, RunningCount: 2},
	}, start, 0)

	// Removing an unused old bridge must not look like an application stop.
	store.observeAppTraffic([]AppBridgeStats{
		{Bridge: "lzc-br-current", AppID: app, Project: project, ContainerCount: 2, RunningCount: 2},
	}, start.Add(time.Minute), 0)
	if events := store.query(NetworkEventQuery{Kind: "app_disabled", Limit: 10}); len(events) != 0 {
		t.Fatalf("old bridge removal produced false stop: %+v", events)
	}

	// The application stops only when its aggregated runtime reaches zero.
	store.observeAppTraffic([]AppBridgeStats{
		{Bridge: "lzc-br-current", AppID: app, Project: project, ContainerCount: 2, RunningCount: 0},
	}, start.Add(2*time.Minute), 0)
	events := store.query(NetworkEventQuery{Kind: "app_disabled", Limit: 10})
	if len(events) != 1 || events[0].DedupeKey != "app_lifecycle:app:"+app {
		t.Fatalf("application stop events = %+v", events)
	}
}

func TestObserveAppTrafficBridgeReplacementDoesNotToggleApplication(t *testing.T) {
	store := newNetworkEventStore(t.TempDir())
	start := time.Date(2026, 8, 5, 9, 30, 0, 0, time.Local)
	app := "cloud.lazycat.app.browser"
	store.observeAppTraffic([]AppBridgeStats{{Bridge: "lzc-br-old", AppID: app, ContainerCount: 1, RunningCount: 1}}, start, 0)
	store.observeAppTraffic([]AppBridgeStats{{Bridge: "lzc-br-new", AppID: app, ContainerCount: 1, RunningCount: 1}}, start.Add(time.Minute), 0)
	if events := store.query(NetworkEventQuery{Limit: 10}); len(events) != 0 {
		t.Fatalf("bridge replacement produced lifecycle events: %+v", events)
	}
}

func TestNetworkEventsHideLegacyLifecycleTimestamps(t *testing.T) {
	store := newNetworkEventStore(t.TempDir())
	store.append(NetworkEvent{Kind: "app_bridge_appeared", Title: "legacy bridge"})
	store.append(NetworkEvent{Kind: "app_enabled", Title: "legacy lifecycle", Details: map[string]any{"bridge": "lzc-br-a"}})
	store.append(NetworkEvent{Kind: "app_enabled", Title: "runtime lifecycle", Details: map[string]any{"lifecycle_source": "container_runtime_v2"}})
	events := store.query(NetworkEventQuery{Limit: 10})
	if len(events) != 1 || events[0].Title != "runtime lifecycle" {
		t.Fatalf("events = %+v", events)
	}
}
