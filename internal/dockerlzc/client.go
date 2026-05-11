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

// BridgeAppInfo is the joined record returned by BuildBridgeMap.
type BridgeAppInfo struct {
	AppID   string // e.g. "cloud.lazycat.app.netwatch" — empty if unknown
	Project string // docker compose project name, e.g. "cloudlazycatappnetwatch"
	Title   string // human-friendly app name; falls back to AppID when missing
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
		if _, ok := projectInfo[project]; !ok {
			projectInfo[project] = BridgeAppInfo{
				AppID:   appid,
				Project: project,
				Title:   appid,
			}
		}
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
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Labels map[string]string `json:"Labels"`
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
