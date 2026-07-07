package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/logging"
)

func TestAuthMiddlewareMiningProofEndpointsRemainPublic(t *testing.T) {
	logger := logging.NewLogger("", false)
	handler := AuthMiddleware(nil, logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/v1/mining/work"},
		{method: http.MethodPost, path: "/api/v1/mining/submit"},
		{method: http.MethodGet, path: "/api/v1/mining/challenge"},
		{method: http.MethodGet, path: "/api/v1/mining/enrollment/node-1"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("public mining endpoint returned %d, want %d", rec.Code, http.StatusNoContent)
			}
		})
	}
}

func TestAuthMiddlewareStillProtectsNonPublicEndpoints(t *testing.T) {
	logger := logging.NewLogger("", false)
	handler := AuthMiddleware(nil, logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/config", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("protected endpoint returned %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
