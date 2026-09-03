package probe

import (
	"errors"
	"strings"
	"testing"

	"netwatch/internal/dockerlzc"
)

func TestMergeAppTrafficDockerMetadataKeepsLastGoodTopologyOnFailure(t *testing.T) {
	previous := appTrafficMetadata{
		bridgeMap: map[string]dockerlzc.BridgeAppInfo{
			"lzc-br-known": {AppID: "app.known"},
		},
		hostAppIDs:   map[string]bool{"app.host": true},
		hostProjects: map[string]bool{"apphost": true},
	}

	got := mergeAppTrafficDockerMetadata(previous, nil, nil, errors.New("temporary docker failure"), true)
	if got.bridgeMap["lzc-br-known"].AppID != "app.known" || !got.hostAppIDs["app.host"] || !got.hostProjects["apphost"] {
		t.Fatalf("last good topology was discarded: %#v", got)
	}
	if !got.dockerStale || !strings.Contains(got.dockerDiagnostic, "暂时不可用") {
		t.Fatalf("failure state = stale:%v diagnostic:%q", got.dockerStale, got.dockerDiagnostic)
	}
	if note := appTrafficDockerMetadataNote(got); !strings.Contains(note, "最近一次成功读取") {
		t.Fatalf("note=%q", note)
	}
}

func TestMergeAppTrafficDockerMetadataReplacesTopologyAfterRecovery(t *testing.T) {
	previous := appTrafficMetadata{
		bridgeMap:        map[string]dockerlzc.BridgeAppInfo{"lzc-br-old": {AppID: "app.old"}},
		dockerDiagnostic: "old failure",
		dockerStale:      true,
	}
	bridgeMap := map[string]dockerlzc.BridgeAppInfo{"lzc-br-new": {AppID: "app.new"}}
	containers := []dockerlzc.ContainerRuntimeInfo{
		{AppID: "app.host", Project: "apphost", Running: true, NetworkMode: "host"},
		{AppID: "app.stopped", Project: "stopped", Running: false, NetworkMode: "host"},
	}

	got := mergeAppTrafficDockerMetadata(previous, bridgeMap, containers, nil, true)
	if got.bridgeMap["lzc-br-new"].AppID != "app.new" || got.bridgeMap["lzc-br-old"].AppID != "" {
		t.Fatalf("recovered bridge map=%#v", got.bridgeMap)
	}
	if !got.hostAppIDs["app.host"] || !got.hostProjects["apphost"] || got.hostAppIDs["app.stopped"] {
		t.Fatalf("recovered host topology=%#v projects=%#v", got.hostAppIDs, got.hostProjects)
	}
	if got.dockerStale || got.dockerDiagnostic != "" || appTrafficDockerMetadataNote(got) != "" {
		t.Fatalf("recovered state = stale:%v diagnostic:%q", got.dockerStale, got.dockerDiagnostic)
	}
}

func TestMergeAppTrafficDockerMetadataDistinguishesMissingSocket(t *testing.T) {
	got := mergeAppTrafficDockerMetadata(appTrafficMetadata{}, nil, nil, errors.New("missing"), false)
	if got.dockerStale || got.dockerDiagnostic != "未检测到 lzc-docker socket 挂载" {
		t.Fatalf("state = stale:%v diagnostic:%q", got.dockerStale, got.dockerDiagnostic)
	}
	if note := appTrafficDockerMetadataNote(got); !strings.Contains(note, "仅展示网桥级流量") {
		t.Fatalf("note=%q", note)
	}
}
