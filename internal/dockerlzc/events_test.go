package dockerlzc

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func withEventClient(t *testing.T, status int, body string) {
	t.Helper()
	previous := eventClient
	eventClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/events" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	t.Cleanup(func() { eventClient = previous })
}

func TestWatchEventsStreamsDockerEvents(t *testing.T) {
	withEventClient(t, http.StatusOK, "{\"Type\":\"container\",\"Action\":\"start\",\"time\":123}\n")

	var events []Event
	err := WatchEvents(context.Background(), func(event Event) {
		events = append(events, event)
	})
	if err == nil {
		t.Fatal("expected EOF after test stream closes")
	}
	if len(events) != 1 || events[0].Type != "container" || events[0].Action != "start" || events[0].Time != 123 {
		t.Fatalf("events = %+v", events)
	}
}

func TestWatchEventsRejectsDockerError(t *testing.T) {
	withEventClient(t, http.StatusServiceUnavailable, "unavailable")
	if err := WatchEvents(context.Background(), nil); err == nil {
		t.Fatal("expected Docker event stream error")
	}
}
