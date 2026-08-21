package probe

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newNetworkMutationTestService(t *testing.T) *Service {
	t.Helper()
	return &Service{
		cfg:     Config{DataDir: t.TempDir()},
		network: newNetworkMutationState(),
	}
}

func TestNetworkMutationReservationIsExclusive(t *testing.T) {
	s := newNetworkMutationTestService(t)
	var winners atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := s.reserveNetworkMutation(networkMutationIP, "eth0"); err == nil {
				winners.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := winners.Load(); got != 1 {
		t.Fatalf("reservation winners = %d, want 1", got)
	}
}

func TestNetworkMutationKindsAreMutuallyExclusive(t *testing.T) {
	s := newNetworkMutationTestService(t)
	id, err := s.reserveNetworkMutation(networkMutationDNS, "eth0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.reserveNetworkMutation(networkMutationBridge, "nw-eth0"); err == nil {
		t.Fatal("bridge reservation succeeded while DNS reservation was active")
	}
	if _, err := s.reserveNetworkMutation(networkMutationRestart, "eth0"); err == nil {
		t.Fatal("restart reservation succeeded while DNS reservation was active")
	}
	s.abortNetworkMutation(id)
	restartID, err := s.reserveNetworkMutation(networkMutationRestart, "eth0")
	if err != nil {
		t.Fatalf("restart reservation after abort: %v", err)
	}
	s.abortNetworkMutation(restartID)
	if _, err := s.reserveNetworkMutation(networkMutationBridge, "nw-eth0"); err != nil {
		t.Fatalf("bridge reservation after abort: %v", err)
	}
}

func TestRestartNetworkConfigDeviceRejectsUnsafeDeviceBeforeMutation(t *testing.T) {
	s := newNetworkMutationTestService(t)
	result := s.RestartNetworkConfigDevice(context.Background(), NetworkConfigRestartRequest{Device: "lo"})
	if result.OK || result.Error == "" {
		t.Fatalf("result = %+v, want unsafe-device error", result)
	}
	s.network.mu.Lock()
	defer s.network.mu.Unlock()
	if s.network.active != nil {
		t.Fatalf("unsafe restart reserved a mutation: %+v", s.network.active)
	}
}

func TestNetworkMutationPersistsAndClears(t *testing.T) {
	s := newNetworkMutationTestService(t)
	id, err := s.reserveNetworkMutation(networkMutationIP, "eth0")
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour).Round(0)
	rb := &networkConfigRollback{
		ID: id, Device: "eth0", Until: until,
		Request:  NetworkConfigApplyRequest{Device: "eth0", Method: "manual", Address: "192.0.2.10/24"},
		Snapshot: networkConfigSnapshot{Connection: "wired", Method: "auto"},
	}
	if err := s.activateNetworkMutation(&networkMutation{
		ID: id, Kind: networkMutationIP, Target: "eth0", Until: until, IP: rb,
	}); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(networkMutationPath(s.cfg.DataDir))
	if err != nil {
		t.Fatal(err)
	}
	var saved networkMutation
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ID != id || saved.Kind != networkMutationIP || saved.IP == nil || saved.IP.Snapshot.Connection != "wired" {
		t.Fatalf("unexpected saved mutation: %+v", saved)
	}

	confirmed, err := s.confirmNetworkMutation(id, networkMutationIP)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.IP == nil || confirmed.IP.Device != "eth0" {
		t.Fatalf("unexpected confirmed mutation: %+v", confirmed)
	}
	if _, err := os.Stat(networkMutationPath(s.cfg.DataDir)); !os.IsNotExist(err) {
		t.Fatalf("mutation file still exists after confirm: %v", err)
	}
}

func TestNetworkMutationRollbackFailureRemainsRetryable(t *testing.T) {
	s := newNetworkMutationTestService(t)
	id, err := s.reserveNetworkMutation(networkMutationDNS, "eth0")
	if err != nil {
		t.Fatal(err)
	}
	rb := &hostDNSRollback{ID: id, Device: "eth0", Until: time.Now().Add(time.Hour)}
	if err := s.activateNetworkMutation(&networkMutation{
		ID: id, Kind: networkMutationDNS, Target: "eth0", Until: rb.Until, DNS: rb,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.beginNetworkMutationRollback(id, networkMutationDNS); err != nil {
		t.Fatal(err)
	}
	s.finishNetworkMutationRollback(id, errors.New("restore failed"))

	s.network.mu.Lock()
	active := s.network.active
	s.network.mu.Unlock()
	if active == nil || active.Status != networkMutationFailed || active.LastError != "restore failed" {
		t.Fatalf("failed rollback was not retained: %+v", active)
	}
	if _, err := s.beginNetworkMutationRollback(id, networkMutationDNS); err != nil {
		t.Fatalf("failed rollback is not retryable: %v", err)
	}
	s.finishNetworkMutationRollback(id, nil)
	s.network.mu.Lock()
	defer s.network.mu.Unlock()
	if s.network.active != nil || s.network.dnsRollback != nil {
		t.Fatalf("successful retry did not clear mutation: %+v", s.network.active)
	}
}

func TestNetworkMutationRestoresPendingWithOriginalDeadline(t *testing.T) {
	dataDir := t.TempDir()
	s1 := &Service{cfg: Config{DataDir: dataDir}, network: newNetworkMutationState()}
	id, err := s1.reserveNetworkMutation(networkMutationDNS, "eth0")
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour).Round(0)
	rb := &hostDNSRollback{
		ID: id, Device: "eth0", Until: until,
		Snapshot: hostDNSSnapshot{Device: "eth0", Connection: "wired", DNS: "192.0.2.53"},
	}
	if err := s1.activateNetworkMutation(&networkMutation{
		ID: id, Kind: networkMutationDNS, Target: "eth0", Until: until, DNS: rb,
	}); err != nil {
		t.Fatal(err)
	}

	s2 := &Service{cfg: Config{DataDir: dataDir}, network: newNetworkMutationState()}
	s2.restoreNetworkMutation()
	s2.network.mu.Lock()
	active := s2.network.active
	if active != nil {
		stopNetworkMutationTimer(active)
	}
	s2.network.mu.Unlock()
	if active == nil || active.ID != id || active.Kind != networkMutationDNS || active.DNS == nil {
		t.Fatalf("pending mutation was not restored: %+v", active)
	}
	if !active.Until.Equal(until) {
		t.Fatalf("restored deadline = %v, want %v", active.Until, until)
	}
}

func TestOldMutationIDCannotAffectNewMutation(t *testing.T) {
	s := newNetworkMutationTestService(t)
	oldID, err := s.reserveNetworkMutation(networkMutationDNS, "eth0")
	if err != nil {
		t.Fatal(err)
	}
	oldRB := &hostDNSRollback{ID: oldID, Device: "eth0", Until: time.Now().Add(time.Hour)}
	if err := s.activateNetworkMutation(&networkMutation{
		ID: oldID, Kind: networkMutationDNS, Target: "eth0", Until: oldRB.Until, DNS: oldRB,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.confirmNetworkMutation(oldID, networkMutationDNS); err != nil {
		t.Fatal(err)
	}

	newID, err := s.reserveNetworkMutation(networkMutationDNS, "eth1")
	if err != nil {
		t.Fatal(err)
	}
	newRB := &hostDNSRollback{ID: newID, Device: "eth1", Until: time.Now().Add(time.Hour)}
	if err := s.activateNetworkMutation(&networkMutation{
		ID: newID, Kind: networkMutationDNS, Target: "eth1", Until: newRB.Until, DNS: newRB,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.beginNetworkMutationRollback(oldID, networkMutationDNS); err == nil {
		t.Fatal("old mutation id unexpectedly started rollback of new mutation")
	}
	s.network.mu.Lock()
	defer s.network.mu.Unlock()
	if s.network.active == nil || s.network.active.ID != newID || s.network.active.Status != networkMutationPending {
		t.Fatalf("new mutation changed by old id: %+v", s.network.active)
	}
}
