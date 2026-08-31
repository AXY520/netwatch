package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"netwatch/internal/logger"
)

type networkMutationKind string

const (
	networkMutationIP      networkMutationKind = "ip"
	networkMutationBridge  networkMutationKind = "bridge"
	networkMutationDNS     networkMutationKind = "dns"
	networkMutationRestart networkMutationKind = "restart"
)

type networkMutationStatus string

const (
	networkMutationPreparing   networkMutationStatus = "preparing"
	networkMutationPending     networkMutationStatus = "pending"
	networkMutationConfirmed   networkMutationStatus = "confirmed"
	networkMutationRollingBack networkMutationStatus = "rolling_back"
	networkMutationRolledBack  networkMutationStatus = "rolled_back"
	networkMutationFailed      networkMutationStatus = "failed"
)

type networkMutation struct {
	Version      int                          `json:"version"`
	ID           string                       `json:"id"`
	Kind         networkMutationKind          `json:"kind"`
	Target       string                       `json:"target"`
	Status       networkMutationStatus        `json:"status"`
	StartedAt    time.Time                    `json:"started_at"`
	Until        time.Time                    `json:"rollback_at,omitempty"`
	LastError    string                       `json:"last_error,omitempty"`
	Verification *NetworkMutationVerification `json:"verification,omitempty"`
	IP           *networkConfigRollback       `json:"ip,omitempty"`
	Bridge       *hostBridgeRollback          `json:"bridge,omitempty"`
	DNS          *hostDNSRollback             `json:"dns,omitempty"`
}

func networkMutationPath(dataDir string) string {
	return filepath.Join(dataDir, "network_mutation.json")
}

func networkMutationBusyMessage(active *networkMutation) string {
	if active == nil {
		return "已有待处理的网络变更，请先确认或回滚"
	}
	label := map[networkMutationKind]string{
		networkMutationIP:      "网卡配置",
		networkMutationBridge:  "网桥",
		networkMutationDNS:     "DNS",
		networkMutationRestart: "网卡重启",
	}[active.Kind]
	if label == "" {
		label = "网络"
	}
	return fmt.Sprintf("已有待处理的%s变更，请先确认或回滚", label)
}

// reserveNetworkMutation closes the gap between checking for a pending change
// and actually applying it. The reservation is in-memory only because there is
// nothing to recover before the host has been modified.
func (s *Service) reserveNetworkMutation(kind networkMutationKind, target string) (string, error) {
	s.network.mu.Lock()
	defer s.network.mu.Unlock()
	if s.network.active != nil {
		return "", errors.New(networkMutationBusyMessage(s.network.active))
	}
	id := newRollbackID()
	s.network.active = &networkMutation{
		Version: 1, ID: id, Kind: kind, Target: strings.TrimSpace(target),
		Status: networkMutationPreparing, StartedAt: time.Now(),
	}
	return id, nil
}

func (s *Service) abortNetworkMutation(id string) {
	s.network.mu.Lock()
	if active := s.network.active; active != nil && active.ID == id && active.Status == networkMutationPreparing {
		s.network.active = nil
	}
	s.network.mu.Unlock()
}

func (s *Service) activateNetworkMutation(mutation *networkMutation) error {
	if mutation == nil {
		return errors.New("network mutation is nil")
	}
	s.network.mu.Lock()
	active := s.network.active
	if active == nil || active.ID != mutation.ID || active.Kind != mutation.Kind || active.Status != networkMutationPreparing {
		s.network.mu.Unlock()
		return errors.New("network mutation reservation lost")
	}
	mutation.Version = 1
	mutation.StartedAt = active.StartedAt
	mutation.Status = networkMutationPending
	s.network.active = mutation
	s.syncNetworkMutationViewsLocked()
	if err := s.persistNetworkMutationLocked(); err != nil {
		s.network.active = nil
		s.syncNetworkMutationViewsLocked()
		s.network.mu.Unlock()
		return err
	}
	s.network.mu.Unlock()
	s.auditNetworkMutation(NetworkMutationAuditEvent{Action: "apply", State: string(networkMutationPending)}, mutation)
	return nil
}

func (s *Service) networkMutationSnapshot() *networkMutation {
	s.network.mu.Lock()
	defer s.network.mu.Unlock()
	if s.network.active == nil {
		return nil
	}
	return s.network.active
}

func (s *Service) getNetworkMutation(id string, kind networkMutationKind) (*networkMutation, error) {
	s.network.mu.Lock()
	defer s.network.mu.Unlock()
	active := s.network.active
	if active == nil || active.ID != strings.TrimSpace(id) || active.Kind != kind {
		return nil, errors.New("没有匹配的网络变更")
	}
	return active, nil
}

func (s *Service) confirmNetworkMutation(id string, kind networkMutationKind) (*networkMutation, error) {
	s.network.mu.Lock()
	defer s.network.mu.Unlock()
	active := s.network.active
	if active == nil || active.ID != strings.TrimSpace(id) || active.Kind != kind {
		return nil, errors.New("没有匹配的待确认网络变更")
	}
	if active.Status != networkMutationPending {
		return nil, fmt.Errorf("网络变更当前状态为 %s，不能确认", active.Status)
	}
	stopNetworkMutationTimer(active)
	active.Status = networkMutationConfirmed
	s.network.active = nil
	s.syncNetworkMutationViewsLocked()
	if err := s.persistNetworkMutationLocked(); err != nil {
		// Keep the confirmed operation cleared in memory. A stale disk record is
		// safer removed best-effort than resurrected on restart.
		_ = os.Remove(networkMutationPath(s.cfg.DataDir))
		logger.Warn("clear confirmed network mutation: %v", err)
	}
	s.auditNetworkMutation(NetworkMutationAuditEvent{Action: "confirm", State: string(networkMutationConfirmed)}, active)
	return active, nil
}

func (s *Service) beginNetworkMutationRollback(id string, kind networkMutationKind) (*networkMutation, error) {
	s.network.mu.Lock()
	defer s.network.mu.Unlock()
	active := s.network.active
	if active == nil || active.Kind != kind {
		return nil, errors.New("没有匹配的待回滚网络变更")
	}
	if strings.TrimSpace(id) != "" && active.ID != strings.TrimSpace(id) {
		return nil, errors.New("rollback id 不匹配")
	}
	if active.Status == networkMutationRollingBack {
		return nil, errors.New("网络变更正在回滚")
	}
	stopNetworkMutationTimer(active)
	active.Status = networkMutationRollingBack
	active.LastError = ""
	_ = s.persistNetworkMutationLocked()
	s.auditNetworkMutation(NetworkMutationAuditEvent{Action: "rollback_start", State: string(networkMutationRollingBack)}, active)
	return active, nil
}

func (s *Service) finishNetworkMutationRollback(id string, rollbackErr error) {
	s.network.mu.Lock()
	defer s.network.mu.Unlock()
	active := s.network.active
	if active == nil || active.ID != id {
		return
	}
	if rollbackErr != nil {
		active.Status = networkMutationFailed
		active.LastError = rollbackErr.Error()
		_ = s.persistNetworkMutationLocked()
		s.auditNetworkMutation(NetworkMutationAuditEvent{Action: "rollback", State: string(networkMutationFailed), Error: rollbackErr.Error()}, active)
		return
	}
	active.Status = networkMutationRolledBack
	s.auditNetworkMutation(NetworkMutationAuditEvent{Action: "rollback", State: string(networkMutationRolledBack)}, active)
	s.network.active = nil
	s.syncNetworkMutationViewsLocked()
	if err := s.persistNetworkMutationLocked(); err != nil {
		logger.Warn("clear rolled back network mutation: %v", err)
	}
}

func (s *Service) syncNetworkMutationViewsLocked() {
	s.network.rollbacks = map[string]*networkConfigRollback{}
	s.network.bridgeRollback = nil
	s.network.dnsRollback = nil
	active := s.network.active
	if active == nil {
		return
	}
	if active.IP != nil {
		s.network.rollbacks[active.IP.ID] = active.IP
		s.network.rollbacks[active.IP.Device] = active.IP
	}
	s.network.bridgeRollback = active.Bridge
	s.network.dnsRollback = active.DNS
}

func stopNetworkMutationTimer(m *networkMutation) {
	if m == nil {
		return
	}
	if m.IP != nil && m.IP.Timer != nil {
		m.IP.Timer.Stop()
	}
	if m.Bridge != nil && m.Bridge.Timer != nil {
		m.Bridge.Timer.Stop()
	}
	if m.DNS != nil && m.DNS.Timer != nil {
		m.DNS.Timer.Stop()
	}
}

func (s *Service) persistNetworkMutationLocked() error {
	path := networkMutationPath(s.cfg.DataDir)
	if s.network.active == nil || s.network.active.Status == networkMutationPreparing {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeJSONFile(path, s.network.active, true)
}

func (s *Service) restoreNetworkMutation() {
	path := networkMutationPath(s.cfg.DataDir)
	body, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warn("load network mutation: %v", err)
		}
		// Backward compatibility for bridge records created before the unified file.
		s.ensureHostBridgeRollbackRestored()
		return
	}
	var mutation networkMutation
	if err := json.Unmarshal(body, &mutation); err != nil || mutation.Version != 1 || mutation.ID == "" {
		logger.Warn("ignore invalid network mutation file: %v", err)
		return
	}
	s.network.mu.Lock()
	if s.network.active != nil {
		s.network.mu.Unlock()
		return
	}
	s.network.active = &mutation
	s.syncNetworkMutationViewsLocked()
	s.network.mu.Unlock()

	if mutation.Status == networkMutationRollingBack || mutation.Status == networkMutationFailed || !mutation.Until.After(time.Now()) {
		go s.runRestoredNetworkMutationRollback(mutation.ID, mutation.Kind)
		return
	}
	s.scheduleNetworkMutationRollback(&mutation)
	logger.Info("network mutation restored id=%s kind=%s target=%s until=%s", mutation.ID, mutation.Kind, mutation.Target, mutation.Until.Format(time.DateTime))
}

func (s *Service) scheduleNetworkMutationRollback(m *networkMutation) {
	if m == nil {
		return
	}
	delay := time.Until(m.Until)
	if delay < time.Second {
		delay = time.Second
	}
	timer := time.AfterFunc(delay, func() { s.runRestoredNetworkMutationRollback(m.ID, m.Kind) })
	s.network.mu.Lock()
	if active := s.network.active; active != nil && active.ID == m.ID {
		switch active.Kind {
		case networkMutationIP:
			active.IP.Timer = timer
		case networkMutationBridge:
			active.Bridge.Timer = timer
		case networkMutationDNS:
			active.DNS.Timer = timer
		}
		s.network.mu.Unlock()
		return
	}
	s.network.mu.Unlock()
	timer.Stop()
}

func (s *Service) runRestoredNetworkMutationRollback(id string, kind networkMutationKind) {
	var timeout time.Duration
	switch kind {
	case networkMutationBridge:
		timeout = 45 * time.Second
	default:
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var err error
	switch kind {
	case networkMutationIP:
		_, err = s.rollbackNetworkConfig(ctx, id, "auto_rollback")
	case networkMutationBridge:
		_, err = s.rollbackHostBridge(ctx, id, "auto_rollback")
	case networkMutationDNS:
		_, err = s.rollbackHostDNS(ctx, id, "auto_rollback")
	default:
		err = fmt.Errorf("unknown network mutation kind %q", kind)
	}
	if err != nil {
		logger.Warn("restored network mutation rollback failed id=%s kind=%s err=%v", id, kind, err)
	}
}
