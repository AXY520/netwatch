package probe

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"netwatch/internal/dockerlzc"
)

var hostNetworkPrivateCIDRs = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "169.254.0.0/16"}
var hostNetworkPrivateCIDRs6 = []string{"fc00::/7", "fe80::/10", "::1/128"}

func (s *Service) hostNetworkExperimentalEnabled() bool {
	return s.settings != nil && s.settings.hostNetworkExperimental()
}

func hostNetworkControlPath(target string) (string, error) {
	appID, ok := hostAppIDFromTarget(target)
	if !ok {
		return "", fmt.Errorf("invalid host network target %q", target)
	}
	return hostAppCgroupPath(appID)
}

func hostAppCgroupPath(policyID string) (string, error) {
	policyID = strings.TrimSpace(policyID)
	if dockerlzc.Available() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		containers, err := dockerlzc.ListContainerRuntime(ctx)
		cancel()
		if err == nil {
			for _, container := range containers {
				if !container.Running || container.NetworkMode != "host" {
					continue
				}
				if container.InstanceID != policyID && !(container.InstanceID == "" && container.AppID == policyID) {
					continue
				}
				path := containerHostCgroupPath(container)
				if path == "" {
					continue
				}
				return relativeCgroupPath(filepathDir(path)), nil
			}
		}
	}
	// A stopped app may still have an app-level cgroup and an iptables rule
	// from the previous session. Resolve that slice directly so disabling the
	// feature can remove the rule instead of leaving a rule that would affect a
	// later restart of the same app.
	root := systemCgroupV2Root()
	if root != "" && baseAppID(policyID) == policyID {
		needle := "lzcapp-" + policyID + ".slice"
		var found string
		_ = filepath.WalkDir(root, func(path string, entryDir fs.DirEntry, walkErr error) error {
			if walkErr != nil || entryDir == nil {
				return nil
			}
			if entryDir.IsDir() && entryDir.Name() == needle {
				found = path
				return errStopCgroupWalk
			}
			return nil
		})
		if found != "" {
			return relativeCgroupPath(found), nil
		}
	}
	return "", fmt.Errorf("host application instance %s has no running host container", policyID)
}

var errStopCgroupWalk = errors.New("stop cgroup walk")

// filepathDir is kept tiny to make the cgroup parent intent explicit at call
// sites and to avoid exposing filesystem details in the control API.
func filepathDir(path string) string {
	for index := len(path) - 1; index >= 0; index-- {
		if path[index] == '/' {
			if index == 0 {
				return "/"
			}
			return path[:index]
		}
	}
	return path
}

func hostIptablesRuleArgs(path string, cidr string, action string) []string {
	args := []string{"-m", "cgroup", "--path", path}
	if cidr != "" {
		args = append(args, "-d", cidr)
	}
	return append(args, "-j", action)
}

func hostFirewallCgroupPaths(path string) []string {
	return hostFirewallCgroupPathsAt(path, func(candidate string) bool {
		root := systemCgroupV2Root()
		if root == "" {
			return false
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(candidate)))
		return err == nil && info.IsDir()
	})
}

func hostFirewallCgroupPathsAt(path string, exists func(string) bool) []string {
	path = strings.Trim(strings.TrimSpace(filepath.ToSlash(path)), "/")
	if path == "" {
		return nil
	}
	paths := []string{path}
	const lazycatNestedPrefix = "system.slice/runc-lzc-os.scope/"
	if strings.HasPrefix(path, lazycatNestedPrefix) {
		alternate := strings.TrimPrefix(path, lazycatNestedPrefix)
		// lzc-docker exec processes are placed in the top-level lzcapp.slice
		// tree, while the container's main process lives below
		// system.slice/runc-lzc-os.scope. Cover both app slices so an admin
		// exec shell observes the same internet policy as the application.
		if strings.HasPrefix(alternate, "lzcapp.slice/") && alternate != path && exists != nil && exists(alternate) {
			paths = append(paths, alternate)
		}
	}
	return paths
}
