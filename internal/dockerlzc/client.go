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
	"os"
	"strconv"
	"strings"
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

// BridgeAppInfo is the joined record returned by BuildBridgeMap.
type BridgeAppInfo struct {
	AppID          string // e.g. "cloud.lazycat.app.netwatch" — empty if unknown
	Project        string // docker compose project name, e.g. "cloudlazycatappnetwatch"
	Title          string // human-friendly app name; falls back to AppID when missing
	ContainerCount int    // total containers in this project
	RunningCount   int    // containers in "running" state
	StatusText     string // human-readable status, e.g. "Up 2 hours"
	CreatedAt      int64  // earliest container creation unix timestamp
}

// BuildBridgeMap returns a "host bridge name → app info" map by joining the
// docker daemon's network list (which carries the bridge name option) with
// the container list (which carries the `lzcapp.app-id` label).
func BuildBridgeMap(ctx context.Context) (map[string]BridgeAppInfo, error) {
	if !Available() {
		return nil, errors.New("docker socket not mounted")
	}

	networks, err := listNetworks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}
	containers, err := listContainers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	// project → appid (and title) lookup from container labels
	projectInfo := map[string]BridgeAppInfo{}
	for _, c := range containers {
		project := c.Labels["com.docker.compose.project"]
		appid := c.Labels["lzcapp.app-id"]
		if project == "" || appid == "" {
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
			// Use the first running container's formatted start time
			if info.StatusText == "" {
				info.StatusText = FormatDockerStatus(c.Status)
				// Fallback: use created timestamp if status text can't be parsed
				if info.StatusText == "" && c.Created > 0 {
					info.StatusText = FormatContainerCreated(c.Created)
				}
			}
		}
		// Track earliest creation time
		if c.Created > 0 && (info.CreatedAt == 0 || c.Created < info.CreatedAt) {
			info.CreatedAt = c.Created
		}
		projectInfo[project] = info
	}

	out := map[string]BridgeAppInfo{}
	for _, n := range networks {
		bridge := n.Options["com.docker.network.bridge.name"]
		if bridge == "" {
			continue
		}
		project := n.Labels["com.docker.compose.project"]
		info := projectInfo[project]
		if info.Project == "" {
			info.Project = project
		}
		out[bridge] = info
	}
	return out, nil
}

type containerSummary struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Labels  map[string]string `json:"Labels"`
	State   string            `json:"State"`   // "running", "exited", "paused", etc.
	Status  string            `json:"Status"`  // human-readable, e.g. "Up 2 hours", "Exited (0) 5 days ago"
	Created int64             `json:"Created"` // unix timestamp
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
