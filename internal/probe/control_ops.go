package probe

import (
	"context"
	"fmt"

	"netwatch/internal/dockerlzc"
	"netwatch/internal/logger"
)

func (s *Service) ListContainers(ctx context.Context) AppContainersResponse {
	var empty AppContainersResponse
	if !dockerlzc.Available() {
		return empty
	}
	infos, err := dockerlzc.ListContainerRuntime(ctx)
	if err != nil {
		logger.Warn("ListContainers: list runtime: %v", err)
		return empty
	}
	bridgeMap, err := dockerlzc.BuildBridgeMap(ctx)
	if err != nil {
		logger.Warn("ListContainers: build bridge map: %v", err)
	}

	// Group containers by project
	type projGroup struct {
		Project       string
		AppID         string
		InstanceID    string
		UserID        string
		MultiInstance bool
		Bridge        string
		Title         string
		Conts         []ContainerRuntimeInfo
		Targets       []string
	}
	projGroups := map[string]*projGroup{}
	projBridge := map[string]string{} // project → first bridge found
	for bridge, info := range bridgeMap {
		projBridge[info.Project] = bridge
	}
	for _, c := range infos {
		proj := c.Project
		if proj == "" {
			proj = "_ungrouped"
		}
		g, ok := projGroups[proj]
		if !ok {
			g = &projGroup{Project: c.Project, AppID: c.AppID, InstanceID: c.InstanceID, UserID: c.UserID, MultiInstance: c.MultiInstance, Bridge: projBridge[proj]}
			if bi, has := bridgeMap[g.Bridge]; has {
				g.AppID = bi.AppID
				g.InstanceID = bi.InstanceID
				g.UserID = bi.UserID
				g.MultiInstance = bi.MultiInstance
				g.Title = bi.Title
			}
			projGroups[proj] = g
		}
		g.Conts = append(g.Conts, ContainerRuntimeInfo{
			ID: c.ID, Name: c.Name, Image: c.Image, State: c.State,
		})
		if s.hostNetworkExperimentalEnabled() && c.NetworkMode == "host" && c.Running {
			instanceID := c.InstanceID
			if instanceID == "" {
				instanceID = firstNonEmptyProbe(g.InstanceID, c.AppID, g.AppID)
			}
			if instanceID != "" {
				g.Targets = appendUniqueTrafficValue(g.Targets, hostAppTarget(instanceID))
			}
		}
	}

	s.mu.RLock()
	blocked := s.containers.snapshotBlocked()
	blockedApps := s.containers.snapshotBlockedApps()
	s.mu.RUnlock()

	apps := make([]AppContainerGroup, 0, len(projGroups))
	for _, g := range projGroups {
		if g.Bridge == "" && len(g.Conts) == 0 {
			continue
		}
		targets := append([]string(nil), g.Targets...)
		if g.Bridge != "" {
			targets = appendUniqueTrafficValue(targets, g.Bridge)
		}
		policyID := firstNonEmptyProbe(g.InstanceID, g.AppID)
		blockMode := blockedApps[policyID]
		for _, target := range targets {
			if mode := blocked[target]; mode != "" {
				blockMode = mode
				break
			}
		}
		// Skip whitelisted apps (cannot be blocked)
		if isWhitelistedApp(g.AppID, g.Title) {
			continue
		}
		appTitle := g.Title
		if appTitle == "" {
			appTitle = g.AppID
		}
		apps = append(apps, AppContainerGroup{
			Bridge:         g.Bridge,
			ControlTargets: targets,
			AppID:          g.AppID,
			InstanceID:     policyID,
			UserID:         g.UserID,
			MultiInstance:  g.MultiInstance,
			AppTitle:       appTitle,
			Project:        g.Project,
			BlockMode:      blockMode,
			Containers:     g.Conts,
		})
	}
	return AppContainersResponse{Applications: apps}
}

// BlockApp blocks all containers in an app's bridge.
func (s *Service) BlockApp(ctx context.Context, bridge, mode string) error {
	findBinPaths()
	logger.Info("BlockApp bridge=%s mode=%s", bridge, mode)

	if bridge == "" {
		return fmt.Errorf("bridge name is required")
	}
	if mode != "internet" {
		return fmt.Errorf("only internet blocking is supported")
	}
	return s.setLegacyTargetInternetAccess(ctx, bridge, false)
}

// UnblockApp restores network for all containers in an app's bridge.
func (s *Service) UnblockApp(ctx context.Context, bridge string) error {
	findBinPaths()
	logger.Info("UnblockApp bridge=%s", bridge)

	if bridge == "" {
		return fmt.Errorf("bridge name is required")
	}
	mode := s.containers.getBlocked(bridge)

	if mode == "all" {
		// Backward compat: unblock bridges that were blocked with "all" mode
		_ = bridgeUnblockAll(bridge)
	}
	return s.setLegacyTargetInternetAccess(ctx, bridge, true)
}

func (s *Service) saveBlockedBridges() {
	if err := s.persistBlockedState(); err != nil {
		logger.Warn("saveBlockedBridges: %v", err)
	}
}

func (s *Service) persistBlockedState() error {
	settings := s.GetMutableSettings()
	settings.BlockedApps = s.containers.snapshotBlockedApps()
	settings.BlockedBridges = s.containers.snapshotBlocked()
	return saveMutableSettings(s.cfg.DataDir, settings)
}
