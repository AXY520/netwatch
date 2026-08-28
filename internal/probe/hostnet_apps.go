package probe

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"netwatch/internal/dockerlzc"
)

const hostAppTargetPrefix = "host-app:"

type hostAppRuntime struct {
	AppID      string
	Project    string
	Containers []dockerlzc.ContainerRuntimeInfo
	Primary    dockerlzc.ContainerRuntimeInfo
}

func collectHostNetworkTraffic(metadata appTrafficMetadata) []AppBridgeStats {
	if !dockerlzc.Available() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	containers, err := dockerlzc.ListContainerRuntime(ctx)
	if err != nil {
		return nil
	}
	apps := groupHostAppRuntime(containers)
	appIDsByProject := make(map[string]string)
	for appID := range metadata.appMap {
		appIDsByProject[normalizeAppProject(appID)] = appID
	}
	for appID := range metadata.localTitles {
		appIDsByProject[normalizeAppProject(appID)] = appID
	}

	sampledAt := localTimestamp()
	stats := make([]AppBridgeStats, 0)
	for _, app := range apps {
		appID := strings.TrimSpace(app.AppID)
		if appID == "" {
			appID = appIDsByProject[normalizeAppProject(app.Project)]
		}
		if appID == "" || isNetwatchTrafficItem(AppBridgeStats{AppID: appID, Project: app.Project}) || isExcludedApp(appID, "") {
			continue
		}
		for _, container := range app.Containers {
			path, pathDiagnostic := containerHostCgroupPathDiagnostic(container)
			read := hostCgroupBPFRead{}
			if path != "" {
				read = readHostCgroupBPFStats(path)
			} else {
				read.Note = pathDiagnostic
			}
			item := AppBridgeStats{
				Bridge: hostAppTarget(appID), AppID: appID, Project: app.Project,
				SampledAt: sampledAt, CounterPerspective: "container_cgroup",
				NetworkMode: "host", CgroupPath: relativeCgroupPath(path),
				Experimental: true, ControlTarget: hostAppTarget(appID),
				ContainerCount: 1, RunningCount: 1,
			}
			item.Target = AppNetworkTarget{
				ID: item.ControlTarget, Kind: AppNetworkTargetCgroup, AppID: appID,
				CgroupPath: item.CgroupPath, NetworkMode: "host", AccountingSource: item.Source,
			}
			if read.Available {
				item.RxBytes += read.RxBytes
				item.TxBytes += read.TxBytes
				item.DownloadBytes = item.RxBytes
				item.UploadBytes = item.TxBytes
				item.RxPackets += read.RxPackets
				item.TxPackets += read.TxPackets
			}
			if read.Available {
				item.Source = "cgroup_skb_ebpf"
			} else if item.Source == "" {
				item.Source = "cgroup_skb_ebpf_unavailable"
				item.Diagnostic = strings.TrimSpace(read.Note)
			}
			item.Target.AccountingSource = item.Source
			item.Target.Diagnostic = item.Diagnostic
			if app.Primary.ID != "" {
				item.CreatedAt = app.Primary.StartedAt
				if item.CreatedAt == 0 {
					item.CreatedAt = app.Primary.Created
				}
				item.StatusText = dockerlzc.FormatContainerStarted(app.Primary.StartedAt)
				if item.StatusText == "" {
					item.StatusText = dockerlzc.FormatContainerCreated(app.Primary.Created)
				}
			}
			if title := metadata.localTitles[appID]; title != "" {
				item.AppTitle = title
			} else if info := metadata.appMap[appID]; info.Title != "" && info.Title != appID {
				item.AppTitle = info.Title
			}
			if info, ok := metadata.appMap[appID]; ok {
				item.Domain = info.Domain
				item.Icon = sanitizeIconURL(info.Icon, metadata.boxDomain)
			}
			if item.Icon == "" && metadata.boxDomain != "" {
				item.Icon = "https://" + strings.TrimSuffix(metadata.boxDomain, "/") + "/sys/icons/" + appID + ".png"
			}
			stats = append(stats, item)
		}
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].AppID < stats[j].AppID })
	return stats
}

func groupHostAppRuntime(containers []dockerlzc.ContainerRuntimeInfo) []hostAppRuntime {
	grouped := make(map[string]*hostAppRuntime)
	primary := dockerlzc.PrimaryAppContainers(containers)
	for _, container := range containers {
		if !container.Running || container.PID <= 0 || container.NetworkMode != "host" || strings.TrimSpace(container.Project) == "" {
			continue
		}
		app := grouped[container.Project]
		if app == nil {
			app = &hostAppRuntime{AppID: container.AppID, Project: container.Project, Primary: primary[container.Project]}
			grouped[container.Project] = app
		}
		if app.AppID == "" {
			app.AppID = container.AppID
		}
		app.Containers = append(app.Containers, container)
	}
	out := make([]hostAppRuntime, 0, len(grouped))
	for _, app := range grouped {
		sort.Slice(app.Containers, func(i, j int) bool { return app.Containers[i].ID < app.Containers[j].ID })
		out = append(out, *app)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Project < out[j].Project })
	return out
}

func hostAppTarget(appID string) string {
	return hostAppTargetPrefix + strings.TrimSpace(appID)
}

func hostAppIDFromTarget(target string) (string, bool) {
	if !strings.HasPrefix(target, hostAppTargetPrefix) {
		return "", false
	}
	appID := strings.TrimSpace(strings.TrimPrefix(target, hostAppTargetPrefix))
	return appID, appID != ""
}

func containerHostCgroupPath(container dockerlzc.ContainerRuntimeInfo) string {
	path, _ := containerHostCgroupPathDiagnostic(container)
	return path
}

func containerHostCgroupPathDiagnostic(container dockerlzc.ContainerRuntimeInfo) (string, string) {
	return resolveContainerHostCgroupPath(
		container,
		readProcessCgroup,
		func(value string) string { return hostCgroupPath(hostProcRoot(), value) },
	)
}

func resolveContainerHostCgroupPath(
	container dockerlzc.ContainerRuntimeInfo,
	readProcess func(int) (string, error),
	resolve func(string) string,
) (string, string) {
	candidates := make([]string, 0, 3)
	if value := strings.TrimSpace(container.CgroupPath); value != "" {
		candidates = append(candidates, value)
	}

	var processErr error
	if container.PID > 0 {
		if value, err := readProcess(container.PID); err == nil && strings.TrimSpace(value) != "" {
			candidates = append(candidates, value)
		} else {
			processErr = err
		}
	} else {
		processErr = errors.New("容器 PID 不可用")
	}

	// lzc-docker does not expose State.CgroupPath on current Lazycat builds.
	// Keep the runtime's stable systemd layout as a final validated candidate.
	if appID, containerID := strings.TrimSpace(container.AppID), strings.TrimSpace(container.ID); appID != "" && containerID != "" {
		candidates = append(candidates, filepath.ToSlash(filepath.Join(
			"/system.slice/runc-lzc-os.scope/lzcapp.slice",
			"lzcapp-"+appID+".slice",
			"docker-"+containerID+".scope",
		)))
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if path := resolve(candidate); path != "" {
			return path, ""
		}
	}

	if processErr != nil {
		return "", fmt.Sprintf("未找到容器 cgroup 路径：%v", processErr)
	}
	if len(candidates) == 0 {
		return "", "未找到容器 cgroup 路径：Docker 未返回 cgroup，且容器进程信息不可用"
	}
	return "", fmt.Sprintf("未找到容器 cgroup 路径：%d 个候选路径均不存在", len(seen))
}

func readProcessCgroup(pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("容器 PID 不可用")
	}
	var lastErr error
	for _, root := range []string{hostProcRoot(), "/proc"} {
		file, err := os.Open(filepath.Join(root, fmt.Sprintf("%d", pid), "cgroup"))
		if err != nil {
			lastErr = err
			continue
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.SplitN(strings.TrimSpace(scanner.Text()), ":", 3)
			if len(fields) == 3 && fields[0] == "0" && fields[1] == "" {
				_ = file.Close()
				return fields[2], nil
			}
		}
		if err := scanner.Err(); err != nil {
			lastErr = err
		} else {
			lastErr = errors.New("未发现 cgroup v2 记录")
		}
		_ = file.Close()
	}
	if lastErr == nil {
		lastErr = errors.New("无法读取进程 cgroup")
	}
	return "", lastErr
}

func relativeCgroupPath(path string) string {
	root := systemCgroupV2Root()
	if root != "" {
		if relative, err := filepath.Rel(root, path); err == nil && relative != "." && !strings.HasPrefix(relative, "..") {
			return filepath.ToSlash(relative)
		}
	}
	return strings.TrimPrefix(filepath.ToSlash(path), "/")
}

func hostContainersForApp(ctx context.Context, appID string) []dockerlzc.ContainerRuntimeInfo {
	if !dockerlzc.Available() {
		return nil
	}
	containers, err := dockerlzc.ListContainerRuntime(ctx)
	if err != nil {
		return nil
	}
	out := make([]dockerlzc.ContainerRuntimeInfo, 0)
	normalizedAppID := normalizeAppProject(appID)
	for _, container := range containers {
		matchesApp := container.AppID == appID || (container.AppID == "" && normalizeAppProject(container.Project) == normalizedAppID)
		if matchesApp && container.NetworkMode == "host" {
			out = append(out, container)
		}
	}
	return out
}
