// Package dockerlzc reads the lzc-docker daemon over a unix socket to build
// a "bridge name → app id" map. The socket is mounted by lzc-build.yml's
// compose_override; when the socket isn't available (lzc forbids the bind),
// every call returns an empty map and netwatch falls back to bridge-only stats.
package dockerlzc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const socketPath = "/var/run/docker.sock"

var client = &http.Client{
	Timeout: 3 * time.Second,
	Transport: &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.DialTimeout("unix", socketPath, 1*time.Second)
		},
	},
}

var eventClient = &http.Client{
	Transport: &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.DialTimeout("unix", socketPath, 1*time.Second)
		},
	},
}

// Available returns true when the lzc-docker socket is accessible.
func Available() bool {
	_, err := os.Stat(socketPath)
	return err == nil
}

// parseDockerStatusStart parses Docker status text like "Up 36 hours (healthy)"
// and returns the approximate start time. Returns zero time if parsing fails.
func parseDockerStatusStart(status string) time.Time {
	s := strings.TrimSpace(status)
	if !strings.HasPrefix(s, "Up ") {
		return time.Time{}
	}
	s = s[3:] // strip "Up "
	// Extract number and unit: "36 hours", "2 minutes", "5 days"
	parts := strings.SplitN(s, " ", 2)
	if len(parts) < 2 {
		return time.Time{}
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n <= 0 {
		return time.Time{}
	}
	unit := strings.ToLower(strings.TrimSpace(parts[1]))
	// Strip anything after unit (e.g. "(healthy)")
	if idx := strings.IndexAny(unit, " ("); idx >= 0 {
		unit = unit[:idx]
	}
	var d time.Duration
	switch unit {
	case "second", "seconds":
		d = time.Duration(n) * time.Second
	case "minute", "minutes":
		d = time.Duration(n) * time.Minute
	case "hour", "hours":
		d = time.Duration(n) * time.Hour
	case "day", "days", "week", "weeks":
		mul := 24
		if strings.HasPrefix(unit, "week") {
			mul = 24 * 7
		}
		d = time.Duration(n*mul) * time.Hour
	default:
		return time.Time{}
	}
	return time.Now().Add(-d)
}

// FormatDockerStatus converts Docker status like "Up 36 hours (healthy)" into
// a concise start time string.
// Returns e.g. "今天 14:30" or "5月14日 08:30".
// Non-running containers return empty (they won't appear in traffic data).
func FormatDockerStatus(status string) string {
	s := strings.TrimSpace(status)
	if s == "" || !strings.HasPrefix(s, "Up ") {
		return ""
	}
	start := parseDockerStatusStart(status)
	if start.IsZero() {
		return ""
	}
	now := time.Now()
	if start.Year() == now.Year() && start.YearDay() == now.YearDay() {
		return "今天 " + start.Format("15:04")
	}
	yesterday := now.AddDate(0, 0, -1)
	if start.Year() == yesterday.Year() && start.YearDay() == yesterday.YearDay() {
		return "昨天 " + start.Format("15:04")
	}
	return start.Format("1月2日 15:04")
}

// FormatContainerCreated formats a unix timestamp as a concise start time string.
// Used as fallback when Docker status text can't be parsed.
func FormatContainerCreated(created int64) string {
	if created <= 0 {
		return ""
	}
	t := time.Unix(created, 0)
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return "今天 " + t.Format("15:04")
	}
	yesterday := now.AddDate(0, 0, -1)
	if t.Year() == yesterday.Year() && t.YearDay() == yesterday.YearDay() {
		return "昨天 " + t.Format("15:04")
	}
	return t.Format("1月2日 15:04")
}

func FormatContainerStarted(started int64) string {
	if started <= 0 {
		return ""
	}
	t := time.Unix(started, 0)
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return "今天 " + t.Format("15:04")
	}
	yesterday := now.AddDate(0, 0, -1)
	if t.Year() == yesterday.Year() && t.YearDay() == yesterday.YearDay() {
		return "昨天 " + t.Format("15:04")
	}
	return t.Format("1月2日 15:04")
}

// BridgeAppInfo is the joined record returned by BuildBridgeMap.
type BridgeAppInfo struct {
	AppID          string // e.g. "cloud.lazycat.app.netwatch" — empty if unknown
	Project        string // docker compose project name, e.g. "cloudlazycatappnetwatch"
	Title          string // human-friendly app name; falls back to AppID when missing
	ContainerCount int    // total containers in this project
	RunningCount   int    // containers in "running" state
	StatusText     string // human-readable status, e.g. "Up 2 hours"
	CreatedAt      int64  // earliest container start timestamp, creation time as fallback
}

const bridgeMapCacheTTL = time.Minute

var bridgeMapCache struct {
	sync.Mutex
	at   time.Time
	data map[string]BridgeAppInfo
}

// InvalidateBridgeMapCache forces the next topology read to query Docker.
// Docker event consumers call this before rebuilding application state.
func InvalidateBridgeMapCache() {
	bridgeMapCache.Lock()
	bridgeMapCache.at = time.Time{}
	bridgeMapCache.data = nil
	bridgeMapCache.Unlock()
}

// Event is the minimal Docker event payload needed to detect topology changes.
type Event struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Time   int64  `json:"time"`
}

// WatchEvents blocks while streaming container and network events from Docker.
// It returns when the context is cancelled or the stream disconnects.
func WatchEvents(ctx context.Context, onEvent func(Event)) error {
	filters := `{"type":["container","network"]}`
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/events?filters="+url.QueryEscape(filters), nil)
	if err != nil {
		return err
	}
	resp, err := eventClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("docker events: %s", resp.Status)
	}

	decoder := json.NewDecoder(resp.Body)
	for {
		var event Event
		if err := decoder.Decode(&event); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if onEvent != nil {
			onEvent(event)
		}
	}
}

// ContainerRuntimeInfo is the small runtime slice netwatch needs from Docker.
// Docker's list endpoint does not include HostConfig.NetworkMode or the real
// host PID, so ListContainerRuntime joins /containers/json with per-container
// inspect data. This is intentionally kept independent from Lazycat app APIs:
// labels are enough to group most lzc-docker containers by app/project.
type ContainerRuntimeInfo struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Image       string            `json:"image,omitempty"`
	AppID       string            `json:"app_id,omitempty"`
	Project     string            `json:"project,omitempty"`
	State       string            `json:"state,omitempty"`
	Status      string            `json:"status,omitempty"`
	Created     int64             `json:"created,omitempty"`
	StartedAt   int64             `json:"started_at,omitempty"`
	NetworkMode string            `json:"network_mode,omitempty"`
	Networks    []string          `json:"networks,omitempty"`
	PID         int               `json:"pid,omitempty"`
	Running     bool              `json:"running,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// BuildBridgeMap returns a "host bridge name → app info" map by joining the
// docker daemon's network list (which carries the bridge name option) with
// the container list (which carries the `lzcapp.app-id` label).
func BuildBridgeMap(ctx context.Context) (map[string]BridgeAppInfo, error) {
	if !Available() {
		return nil, errors.New("docker socket not mounted")
	}
	bridgeMapCache.Lock()
	if time.Since(bridgeMapCache.at) < bridgeMapCacheTTL && bridgeMapCache.data != nil {
		cached := cloneBridgeMap(bridgeMapCache.data)
		bridgeMapCache.Unlock()
		return cached, nil
	}
	bridgeMapCache.Unlock()

	networks, err := listNetworks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}
	containers, err := ListContainerRuntime(ctx)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	out := buildBridgeMapFromInventory(networks, containers)
	bridgeMapCache.Lock()
	bridgeMapCache.at = time.Now()
	bridgeMapCache.data = cloneBridgeMap(out)
	bridgeMapCache.Unlock()
	return out, nil
}

func buildBridgeMapFromInventory(networks []networkSummary, containers []ContainerRuntimeInfo) map[string]BridgeAppInfo {
	// Keep project identity separate from per-network runtime. During app
	// recreation one project can temporarily own both an old and a new network.
	projectInfo := map[string]BridgeAppInfo{}
	networkInfo := map[string]BridgeAppInfo{}
	primaryByProject := PrimaryAppContainers(containers)
	for _, c := range containers {
		project := c.Project
		appid := c.AppID
		if project == "" {
			continue
		}
		info := projectInfo[project]
		if info.AppID == "" {
			info.AppID = appid
			info.Project = project
			info.Title = appid
		}
		info.ContainerCount++
		if c.State == "running" {
			info.RunningCount++
		}
		projectInfo[project] = info
		for _, network := range c.Networks {
			networkRuntime := networkInfo[network]
			if networkRuntime.AppID == "" {
				networkRuntime.AppID = appid
				networkRuntime.Project = project
				networkRuntime.Title = appid
			}
			networkRuntime.ContainerCount++
			if c.State == "running" {
				networkRuntime.RunningCount++
			}
			networkInfo[network] = networkRuntime
		}
	}
	// Application lifecycle time is defined by the primary app container, not
	// by whichever sidecar Docker happened to return first.
	for project, primary := range primaryByProject {
		info := projectInfo[project]
		info.StatusText = containerStatusStart(primary)
		if primary.StartedAt > 0 {
			info.CreatedAt = primary.StartedAt
		} else if primary.Created > 0 {
			info.CreatedAt = primary.Created
		}
		projectInfo[project] = info
	}
	for network, info := range networkInfo {
		if identity, ok := projectInfo[info.Project]; ok {
			info.StatusText = identity.StatusText
			info.CreatedAt = identity.CreatedAt
			networkInfo[network] = info
		}
	}

	out := map[string]BridgeAppInfo{}
	for _, n := range networks {
		bridge := n.Options["com.docker.network.bridge.name"]
		if bridge == "" {
			continue
		}
		project := n.Labels["com.docker.compose.project"]
		info := networkInfo[n.Name]
		identity := projectInfo[project]
		if info.AppID == "" {
			info.AppID = identity.AppID
			info.Title = identity.Title
		}
		if info.Project == "" {
			info.Project = project
		}
		out[bridge] = info
	}
	return out
}

// PrimaryAppContainers returns the Lazycat application container for each
// compose project. The app service is identified from its container name; the
// running/latest instance wins during a short recreate window.
func PrimaryAppContainers(containers []ContainerRuntimeInfo) map[string]ContainerRuntimeInfo {
	selected := map[string]ContainerRuntimeInfo{}
	priorities := map[string]int{}
	for _, c := range containers {
		project := strings.TrimSpace(c.Project)
		if project == "" {
			continue
		}
		priority := primaryAppContainerPriority(c.Name)
		if priority == 0 {
			continue
		}
		current, exists := selected[project]
		if !exists || primaryAppContainerPreferred(c, priority, current, priorities[project]) {
			selected[project] = c
			priorities[project] = priority
		}
	}
	return selected
}

func primaryAppContainerPreferred(candidate ContainerRuntimeInfo, candidatePriority int, current ContainerRuntimeInfo, currentPriority int) bool {
	if candidatePriority != currentPriority {
		return candidatePriority > currentPriority
	}
	candidateRunning := candidate.State == "running" || candidate.Running
	currentRunning := current.State == "running" || current.Running
	if candidateRunning != currentRunning {
		return candidateRunning
	}
	if candidate.StartedAt != current.StartedAt {
		return candidate.StartedAt > current.StartedAt
	}
	if candidate.Created != current.Created {
		return candidate.Created > current.Created
	}
	return candidate.Name < current.Name
}

// primaryAppContainerPriority identifies the Lazycat application service.
// Compose sidecars may also contain "app" in the project prefix, so a suffix
// such as "-app-1" receives the highest priority.
func primaryAppContainerPriority(name string) int {
	name = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "/")))
	if name == "" {
		return 0
	}
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '/'
	})
	priority := 0
	for i, part := range parts {
		if part != "app" {
			continue
		}
		if i == len(parts)-1 || (i == len(parts)-2 && isDecimalToken(parts[i+1])) {
			priority = 3
		} else {
			priority = maxInt(priority, 1)
		}
	}
	if priority == 0 && strings.Contains(name, "app") {
		// Some runtimes use a service name such as "appserver" instead of a
		// separate "app" token. Keep it above a project-prefix-only match.
		return 2
	}
	return priority
}

func isDecimalToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func containerStatusStart(c ContainerRuntimeInfo) string {
	if value := FormatContainerStarted(c.StartedAt); value != "" {
		return value
	}
	if value := FormatDockerStatus(c.Status); value != "" {
		return value
	}
	return FormatContainerCreated(c.Created)
}

func cloneBridgeMap(source map[string]BridgeAppInfo) map[string]BridgeAppInfo {
	cloned := make(map[string]BridgeAppInfo, len(source))
	for bridge, info := range source {
		cloned[bridge] = info
	}
	return cloned
}

// ListContainerRuntime returns inspected container metadata for all containers.
func ListContainerRuntime(ctx context.Context) ([]ContainerRuntimeInfo, error) {
	if !Available() {
		return nil, errors.New("docker socket not mounted")
	}
	containers, err := listContainers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ContainerRuntimeInfo, 0, len(containers))
	for _, c := range containers {
		info := ContainerRuntimeInfo{
			ID:      c.ID,
			Name:    firstContainerName(c.Names),
			Image:   c.Image,
			AppID:   c.Labels["lzcapp.app-id"],
			Project: c.Labels["com.docker.compose.project"],
			State:   c.State,
			Status:  c.Status,
			Created: c.Created,
			Labels:  c.Labels,
		}
		inspect, err := inspectContainer(ctx, c.ID)
		if err == nil {
			if inspect.ID != "" {
				info.ID = inspect.ID
			}
			if inspect.Name != "" {
				info.Name = strings.TrimPrefix(inspect.Name, "/")
			}
			if inspect.Config.Image != "" {
				info.Image = inspect.Config.Image
			}
			if inspect.Config.Labels != nil {
				info.Labels = inspect.Config.Labels
				info.AppID = inspect.Config.Labels["lzcapp.app-id"]
				info.Project = inspect.Config.Labels["com.docker.compose.project"]
			}
			info.NetworkMode = inspect.HostConfig.NetworkMode
			for network := range inspect.NetworkSettings.Networks {
				info.Networks = append(info.Networks, network)
			}
			info.PID = inspect.State.Pid
			info.Running = inspect.State.Running
			if inspect.State.Status != "" {
				info.State = inspect.State.Status
			}
			if created := parseDockerCreated(inspect.Created); created > 0 {
				info.Created = created
			}
			info.StartedAt = parseDockerCreated(inspect.State.StartedAt)
		}
		out = append(out, info)
	}
	return out, nil
}

type containerSummary struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	Labels  map[string]string `json:"Labels"`
	State   string            `json:"State"`   // "running", "exited", "paused", etc.
	Status  string            `json:"Status"`  // human-readable, e.g. "Up 2 hours", "Exited (0) 5 days ago"
	Created int64             `json:"Created"` // unix timestamp
}

type containerInspect struct {
	ID      string `json:"Id"`
	Name    string `json:"Name"`
	Created string `json:"Created"`
	State   struct {
		Status    string `json:"Status"`
		Running   bool   `json:"Running"`
		Pid       int    `json:"Pid"`
		StartedAt string `json:"StartedAt"`
	} `json:"State"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		NetworkMode string `json:"NetworkMode"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]json.RawMessage `json:"Networks"`
	} `json:"NetworkSettings"`
}

type networkSummary struct {
	ID      string            `json:"Id"`
	Name    string            `json:"Name"`
	Labels  map[string]string `json:"Labels"`
	Options map[string]string `json:"Options"`
}

func listContainers(ctx context.Context) ([]containerSummary, error) {
	var out []containerSummary
	if err := getJSON(ctx, "http://docker/containers/json?all=true", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func listNetworks(ctx context.Context) ([]networkSummary, error) {
	var out []networkSummary
	if err := getJSON(ctx, "http://docker/networks", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func inspectContainer(ctx context.Context, id string) (containerInspect, error) {
	var out containerInspect
	if err := getJSON(ctx, "http://docker/containers/"+id+"/json", &out); err != nil {
		return containerInspect{}, err
	}
	return out, nil
}

func firstContainerName(names []string) string {
	for _, name := range names {
		name = strings.TrimPrefix(strings.TrimSpace(name), "/")
		if name != "" {
			return name
		}
	}
	return ""
}

func parseDockerCreated(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.Unix()
	}
	return 0
}

func getJSON(ctx context.Context, url string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("docker API %s: %s", url, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(target)
}
