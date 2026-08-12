package api

import (
	"net/http"
	"strings"
)

// transparencyReadPrefixes are the public, unauthenticated, read-only
// transparency surfaces: /api/v1/trust/attestations/{summary,recent} and
// /api/v1/audit/{summary,items,badge.svg}.
var transparencyReadPrefixes = []string{
	"/api/v1/trust/",
	"/api/v1/audit/",
}

// isPublicTransparencyRead reports whether a request is a read of the public
// transparency surface, and may therefore bypass HTTP rate limiting.
//
// # Why these bypass the limiter
//
// Both limiters bucket on the client IP, and every deployment terminates TLS
// at a reverse proxy while QSDM_TRUST_PROXY_HEADERS defaults false. clientIP
// therefore resolves to the proxy's address for every caller, so one bucket is
// shared by the entire internet. The anonymous tier is 30 requests/minute, and
// trust.html, audit.html, the audit badge and the external trustcheck probe all
// read these paths -- so ordinary page traffic exhausts the quota and the probe
// receives 429.
//
// That failure is worse than it looks. trustcheck fetches the trust summary
// first, so a 429 there aborts the whole probe with a network-class error
// before a single contract assertion runs. The surface the project publishes to
// demonstrate liveness becomes unmeasurable precisely when it is being read,
// and a red probe reports as an outage rather than as rate limiting.
//
// These handlers serve in-memory aggregates and a compile-time checklist, so
// they are cheap. This mirrors the existing bypasses for /api/v1/health and
// /api/v1/mining/, which exist for the same reason: the HTTP limiter is the
// wrong gate for the traffic.
//
// Restricted to GET and HEAD so that a mutating route added under these
// prefixes later cannot silently inherit the bypass. All five current routes
// are read-only.
//
// One predicate, used by both RateLimiter and RoleRateLimiter. The middlewares
// are mounted in series, so a bypass must land in BOTH or the caller still sees
// 429 from the other -- a drift hazard the /api/v1/mining/ comments call out
// after it bit a live miner.
func isPublicTransparencyRead(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	for _, prefix := range transparencyReadPrefixes {
		if strings.HasPrefix(r.URL.Path, prefix) {
			return true
		}
	}
	return false
}
