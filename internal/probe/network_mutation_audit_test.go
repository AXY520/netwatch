package probe

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNetworkMutationAuditIncludesPayloadAndNewestFirst(t *testing.T) {
	s := newNetworkMutationTestService(t)
	m := &networkMutation{
		ID: "mutation-1", Kind: networkMutationIP, Target: "eth0",
		Status: networkMutationPending, StartedAt: time.Now(),
		IP: &networkConfigRollback{
			Request:  NetworkConfigApplyRequest{Device: "eth0", Method: "manual", Address: "192.0.2.10/24"},
			Snapshot: networkConfigSnapshot{Connection: "wired", Method: "auto"},
		},
	}
	s.auditNetworkMutation(NetworkMutationAuditEvent{Action: "apply"}, m)
	s.auditNetworkMutation(NetworkMutationAuditEvent{Action: "verify"}, m)

	events := s.NetworkMutationAudit(10)
	if len(events) != 2 || events[0].Action != "verify" || events[1].Action != "apply" {
		t.Fatalf("unexpected audit order: %+v", events)
	}
	var request NetworkConfigApplyRequest
	if err := json.Unmarshal(events[0].Requested, &request); err != nil {
		t.Fatal(err)
	}
	if request.Address != "192.0.2.10/24" {
		t.Fatalf("audit request = %+v", request)
	}
	var previous networkConfigSnapshot
	if err := json.Unmarshal(events[0].Previous, &previous); err != nil {
		t.Fatal(err)
	}
	if previous.Connection != "wired" {
		t.Fatalf("audit previous = %+v", previous)
	}
}

func TestNetworkMutationAuditLimit(t *testing.T) {
	s := newNetworkMutationTestService(t)
	for i := 0; i < 5; i++ {
		s.auditNetworkMutation(NetworkMutationAuditEvent{ID: string(rune('a' + i)), Kind: "dns", Action: "verify"}, nil)
	}
	events := s.NetworkMutationAudit(2)
	if len(events) != 2 || events[0].ID != "e" || events[1].ID != "d" {
		t.Fatalf("limited events = %+v", events)
	}
}
