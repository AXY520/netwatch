package probe

import (
	"context"
	"fmt"
	"os"

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
		Project string
		AppID   string
		Bridge  string
		Title   string
		Conts   []ContainerRuntimeInfo
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
			g = &projGroup{Project: c.Project, AppID: c.AppID, Bridge: projBridge[proj]}
			if bi, has := bridgeMap[g.Bridge]; has {
				g.AppID = bi.AppID
				g.Title = bi.Title
			}
			projGroups[proj] = g
		}
		g.Conts = append(g.Conts, ContainerRuntimeInfo{
			ID: c.ID, Name: c.Name, Image: c.Image, State: c.State,
		})
	}

	s.mu.RLock()
	blocked := s.containers.snapshotBlocked()
	s.mu.RUnlock()

	apps := make([]AppContainerGroup, 0, len(projGroups))
	for _, g := range projGroups {
		if g.Bridge == "" && len(g.Conts) == 0 {
			continue
		}
		blockMode := blocked[g.Bridge]
		// Skip whitelisted apps (cannot be blocked)
		if isWhitelistedApp(g.AppID, g.Title) {
			continue
		}
		appTitle := g.Title
		if appTitle == "" {
			appTitle = g.AppID
		}
		apps = append(apps, AppContainerGroup{
			Bridge:     g.Bridge,
			AppID:      g.AppID,
			AppTitle:   appTitle,
			Project:    g.Project,
			BlockMode:  blockMode,
			Containers: g.Conts,
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

	// Check whitelist
	if dockerlzc.Available() {
		bridgeMap, err := dockerlzc.BuildBridgeMap(ctx)
		if err == nil {
			if info, ok := bridgeMap[bridge]; ok {
				if isWhitelistedApp(info.AppID, info.Title) {
					return fmt.Errorf("app %s is whitelisted and cannot be blocked", info.Title)
				}
			}
		}
	}

	// Check bridge exists
	if _, err := os.Stat(fmt.Sprintf("/sys/class/net/%s", bridge)); os.IsNotExist(err) {
		return fmt.Errorf("bridge %s not found on host", bridge)
	}

	err := bridgeBlockInternet(bridge)
	if err != nil {
		logger.Warn("bridge block internet via iptables failed: %v; trying nsenter fallback", err)
		if nsenterErr := s.blockAppInternetViaContainers(ctx, bridge); nsenterErr != nil {
			return fmt.Errorf("bridge block internet: iptables: %w; nsenter: %v", err, nsenterErr)
		}
	}

	s.containers.setBlocked(bridge, "internet")
	s.saveBlockedBridges()
	return nil
}

// blockAppInternetViaContainers falls back to nsenter into each container.
func (s *Service) blockAppInternetViaContainers(ctx context.Context, bridge string) error {
	logger.Info("blockAppInternetViaContainers bridge=%s", bridge)
	if !nsenterAvailable() || !ipAvailable() {
		return fmt.Errorf("nsenter/ip not available for internet block fallback")
	}
	infos, err := dockerlzc.ListContainerRuntime(ctx)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	// Find containers belonging to this bridge's project
	// We need the project name for this bridge
	bridgeMap, err := dockerlzc.BuildBridgeMap(ctx)
	if err != nil {
		return fmt.Errorf("build bridge map: %w", err)
	}
	appInfo, ok := bridgeMap[bridge]
	if !ok {
		return fmt.Errorf("bridge %s not found in bridge map", bridge)
	}
	var lastErr error
	for _, c := range infos {
		if c.Project != appInfo.Project || c.PID <= 0 {
			continue
		}
		if _, _, err := containerBlockInternet(c.PID); err != nil {
			logger.Warn("block internet for container %s (pid %d): %v", c.Name, c.PID, err)
			lastErr = err
		} else {
			logger.Info("blocked internet for container %s (pid %d)", c.Name, c.PID)
		}
	}
	return lastErr
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
	if iptablesAvailable() {
		if err := bridgeUnblockInternet(bridge); err != nil {
			logger.Warn("bridge unblock internet via iptables: %v", err)
		}
	}
	if err := s.unblockAppInternetViaContainers(ctx, bridge); err != nil {
		logger.Warn("bridge unblock internet fallback: %v", err)
	}

	s.containers.clearBlocked(bridge)
	s.saveBlockedBridges()
	return nil
}

func (s *Service) unblockAppInternetViaContainers(ctx context.Context, bridge string) error {
	if !nsenterAvailable() || !ipAvailable() {
		return nil
	}
	infos, err := dockerlzc.ListContainerRuntime(ctx)
	if err != nil {
		return err
	}
	bridgeMap, err := dockerlzc.BuildBridgeMap(ctx)
	if err != nil {
		return err
	}
	appInfo, ok := bridgeMap[bridge]
	if !ok {
		return nil
	}
	for _, c := range infos {
		if c.Project != appInfo.Project || c.PID <= 0 {
			continue
		}
		routes := containerDefaultRoutes(c.PID)
		for iface, gw := range routes {
			if err := containerUnblockInternet(c.PID, gw, iface); err != nil {
				logger.Warn("unblock internet for %s: %v", c.Name, err)
			}
		}
	}
	return nil
}

func (s *Service) saveBlockedBridges() {
	settings := s.GetMutableSettings()
	settings.BlockedBridges = s.containers.snapshotBlocked()
	if err := saveMutableSettings(s.cfg.DataDir, settings); err != nil {
		logger.Warn("saveBlockedBridges: %v", err)
	}
}
