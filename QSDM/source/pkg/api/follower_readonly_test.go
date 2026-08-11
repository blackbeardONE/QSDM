package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFollowerReadOnlyMiddleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := FollowerReadOnlyMiddleware(true)(next)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"status read", http.MethodGet, "/api/v1/status", http.StatusNoContent},
		{"preflight", http.MethodOptions, "/api/v1/wallet/submit-signed", http.StatusNoContent},
		{"local login", http.MethodPost, "/api/v1/auth/login", http.StatusNoContent},
		{"node telemetry", http.MethodPost, "/api/v1/monitoring/ngc-proof", http.StatusNoContent},
		{"wallet mutation", http.MethodPost, "/api/v1/wallet/submit-signed", http.StatusServiceUnavailable},
		{"mining proof", http.MethodPost, "/api/v1/mining/submit", http.StatusServiceUnavailable},
		{"task action", http.MethodPost, "/api/v1/tasks/actions/submit-signed", http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, nil))
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.want, recorder.Body.String())
			}
			if tc.want == http.StatusServiceUnavailable && recorder.Header().Get("X-QSDM-Node-Role") != "network-follower" {
				t.Fatal("read-only rejection did not identify the follower role")
			}
		})
	}
}

func TestFollowerReadOnlyMiddlewareDisabled(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	recorder := httptest.NewRecorder()
	FollowerReadOnlyMiddleware(false)(next).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodPost, "/api/v1/wallet/submit-signed", nil),
	)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusCreated)
	}
}
