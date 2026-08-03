package probe

import (
	"testing"
	"time"
)

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
