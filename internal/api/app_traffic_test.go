package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleAppTrafficAllowsOnlyGet(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/network/app-traffic", nil)
	rec := httptest.NewRecorder()
	handler.handleAppTraffic(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleAppTrafficHistoryRequiresAppID(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/network/app-traffic/history", nil)
	rec := httptest.NewRecorder()
	handler.handleAppTrafficHistory(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleAppTrafficConnectionsRequiresAppID(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/network/app-traffic/connections", nil)
	rec := httptest.NewRecorder()
	handler.handleAppTrafficConnections(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleAppTrafficConnectionsAllowsOnlyGet(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/network/app-traffic/connections?app_id=app.example", nil)
	rec := httptest.NewRecorder()
	handler.handleAppTrafficConnections(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleAppTrafficLimitRejectsInvalidAppIDBeforeTrafficControl(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/network/app-traffic/limit", strings.NewReader(`{"app_id":"","upload_kbps":1000}`))
	rec := httptest.NewRecorder()
	handler.handleAppTrafficLimit(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}
