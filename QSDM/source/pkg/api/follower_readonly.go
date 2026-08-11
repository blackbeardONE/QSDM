package api

import (
	"net/http"
	"strings"
)

// FollowerReadOnlyMiddleware prevents a synchronized network follower from
// acknowledging ledger writes it cannot seal. Session management and
// node-local trust telemetry remain available because they do not mutate the
// shared ledger.
func FollowerReadOnlyMiddleware(enabled bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enabled || followerReadOnlyAllows(r.Method, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("X-QSDM-Node-Role", "network-follower")
			writeErrorResponse(w, http.StatusServiceUnavailable,
				"network follower is read-only; send state-changing requests to the authoritative QSDM Core")
		})
	}
}

func followerReadOnlyAllows(method, path string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if !strings.HasPrefix(path, "/api/v1/") {
		return true
	}
	if strings.HasPrefix(path, "/api/v1/auth/") {
		return true
	}
	return path == "/api/v1/monitoring/ngc-proof"
}
