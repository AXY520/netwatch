package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBroadbandPortPolicyMethods(t *testing.T) {
	tests := []struct {
		method  string
		handler http.HandlerFunc
	}{
		{method: http.MethodGet, handler: NewHandler(nil).handleBroadbandPortPolicyStart},
		{method: http.MethodPost, handler: NewHandler(nil).handleBroadbandPortPolicyTask},
		{method: http.MethodGet, handler: NewHandler(nil).handleBroadbandPortPolicyCancel},
	}
	for _, test := range tests {
		req := httptest.NewRequest(test.method, "/", nil)
		recorder := httptest.NewRecorder()
		test.handler(recorder, req)
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d", test.method, recorder.Code)
		}
	}
}
