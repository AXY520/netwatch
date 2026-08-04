package probe

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"netwatch/internal/dockerlzc"
)

var ErrAppNetworkNotFound = errors.New("application network not found")

func (s *Service) GetAppNetworkDetail(ctx context.Context, bridge, appID, project string, since time.Time, limit int) (AppNetworkDetail, error) {
	bridge = strings.TrimSpace(bridge)
	appID = strings.TrimSpace(appID)
	project = strings.TrimSpace(project)
	if bridge != "" && !strings.HasPrefix(bridge, lzcBridgePrefix) {
		return AppNetworkDetail{}, ErrAppNetworkNotFound
	}
	if bridge == "" && appID == "" && project == "" {
		return AppNetworkDetail{}, ErrAppNetworkNotFound
	}

	runtime, err := dockerlzc.ListContainerRuntime(ctx)
	if err != nil && bridge == "" {
		return AppNetworkDetail{}, err
	}
	bridgeMap, _ := dockerlzc.BuildBridgeMap(ctx)
	if bridge == "" {
		for candidate, info := range bridgeMap {
			if (appID != "" && info.AppID == appID) || (project != "" && info.Project == project) {
				bridge = candidate
				break
			}
		}
	}
	if bridge != "" {
		if info, ok := bridgeMap[bridge]; ok {
			if appID == "" {
				appID = info.AppID
			}
			if project == "" {
				project = info.Project
			}
		}
	}
	containers := filterAppRuntime(runtime, appID, project)
	if bridge == "" && (len(containers) == 0 || !hasHostNetworkContainer(containers)) {
		return AppNetworkDetail{}, ErrAppNetworkNotFound
	}

	detail := AppNetworkDetail{
		GeneratedAt: localTimestamp(), AppID: appID, Project: project,
		Containers: []ContainerRuntimeInfo{}, Ports: []HostPortEntry{}, History: []AppTrafficPoint{},
		Events: []NetworkEvent{}, ConnectionTargets: []string{},
		ConnectionNote: "远端连接目标尚未采集，将在连接追踪视图中提供",
	}
	for _, c := range containers {
		detail.Containers = append(detail.Containers, ContainerRuntimeInfo{ID: c.ID, Name: c.Name, Image: c.Image, State: c.State})
	}

	if bridge != "" {
		stats, ok := CollectBridgeTraffic(bridge)
		if !ok {
			return AppNetworkDetail{}, ErrAppNetworkNotFound
		}
		for _, item := range CollectAppTraffic().Bridges {
			if item.Bridge == bridge {
				stats = item
				break
			}
		}
		detail.Mode = "bridge"
		detail.StatisticsAvailable = true
		detail.Bridge = &stats
		detail.AppID, detail.AppTitle, detail.Project = stats.AppID, stats.AppTitle, stats.Project
		detail.SampledAt, detail.AgeSeconds, detail.Stale = stats.SampledAt, stats.AgeSeconds, stats.Stale
		detail.History = s.GetAppTrafficHistorySince(bridge, since, limit)
		detail.Live = appNetworkRate(detail.History)
	} else {
		detail.Mode = "host"
		detail.StatisticsAvailable = false
		detail.Limitation = "host network 服务绕过应用独立网桥，无法提供可靠的按应用流量统计"
	}
	if detail.AppTitle == "" {
		detail.AppTitle = detail.AppID
	}

	ports := CollectHostPorts(ctx).Ports
	detail.Ports = filterAppPorts(ports, detail.AppID, detail.Project, containers)
	detail.Events = filterAppEvents(s.NetworkEvents(NetworkEventQuery{Since: since, Limit: 500}), bridge, detail.AppID, detail.Project, limit)
	return detail, nil
}

func hasHostNetworkContainer(items []dockerlzc.ContainerRuntimeInfo) bool {
	for _, item := range items {
		if item.NetworkMode == "host" {
			return true
		}
	}
	return false
}

func filterAppRuntime(items []dockerlzc.ContainerRuntimeInfo, appID, project string) []dockerlzc.ContainerRuntimeInfo {
	out := make([]dockerlzc.ContainerRuntimeInfo, 0)
	for _, item := range items {
		if appID != "" && item.AppID == appID || project != "" && item.Project == project {
			out = append(out, item)
		}
	}
	return out
}

func filterAppPorts(items []HostPortEntry, appID, project string, runtime []dockerlzc.ContainerRuntimeInfo) []HostPortEntry {
	ids := map[string]bool{}
	for _, item := range runtime {
		ids[shortContainerID(item.ID)] = true
		ids[item.ID] = true
	}
	out := make([]HostPortEntry, 0)
	for _, item := range items {
		c := item.Container
		if c != nil && ((appID != "" && c.AppID == appID) || (project != "" && c.Project == project) || ids[c.ID]) {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port == out[j].Port {
			return out[i].Protocol < out[j].Protocol
		}
		return out[i].Port < out[j].Port
	})
	return out
}

func filterAppEvents(items []NetworkEvent, bridge, appID, project string, limit int) []NetworkEvent {
	out := make([]NetworkEvent, 0)
	for _, item := range items {
		matches := detailString(item.Details, "bridge") == bridge && bridge != "" || detailString(item.Details, "app_id") == appID && appID != "" || detailString(item.Details, "project") == project && project != ""
		if matches {
			out = append(out, item)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out
}

func detailString(details map[string]any, key string) string {
	if value, ok := details[key].(string); ok {
		return value
	}
	return ""
}

func appNetworkRate(points []AppTrafficPoint) AppNetworkRate {
	for i := len(points) - 1; i > 0; i-- {
		newer, older := points[i], points[i-1]
		if newer.Discontinuity || older.Discontinuity || newer.UploadBytes < older.UploadBytes || newer.DownloadBytes < older.DownloadBytes {
			continue
		}
		newerAt, ok1 := parseEventTime(newer.Timestamp)
		olderAt, ok2 := parseEventTime(older.Timestamp)
		seconds := newerAt.Sub(olderAt).Seconds()
		if !ok1 || !ok2 || seconds <= 0 {
			continue
		}
		rate := AppNetworkRate{UploadBPS: float64(newer.UploadBytes-older.UploadBytes) / seconds, DownloadBPS: float64(newer.DownloadBytes-older.DownloadBytes) / seconds}
		rate.TotalBPS = rate.UploadBPS + rate.DownloadBPS
		return rate
	}
	return AppNetworkRate{}
}
