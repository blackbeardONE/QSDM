package api

import "net/http"

// ngcIngestPaths are the NGC attestation ingest and read routes. Each one has a
// purpose-sized per-endpoint cap in RateLimiter.getEndpointLimit
// (ngc-proof 30/min, ngc-challenge 15/min, ngc-proofs 60/min), and
// TestNGCIngestPathsHaveDedicatedPreAuthLimits fails if any entry here ever
// loses that cap -- so exempting a path from the role limiter can never leave
// it with no limit at all.
//
// Listed exactly rather than by /api/v1/monitoring/ prefix, so a route added
// under that prefix later does not silently inherit the exemption.
var ngcIngestPaths = map[string]struct{}{
	"/api/v1/monitoring/ngc-proof":     {},
	"/api/v1/monitoring/ngc-challenge": {},
	"/api/v1/monitoring/ngc-proofs":    {},
}

// isNGCMonitoringIngest reports whether a request targets the NGC attestation
// ingest surface, and may therefore bypass the ROLE limiter specifically.
//
// # Why only the role limiter
//
// RoleRateLimiter buckets on role+identifier and NOT on path
// (Allow: key = "<role>:<identifier>"), so one bucket covers every non-exempt
// endpoint. These routes are in the public-paths list, so AuthMiddleware never
// runs and no claims are set: the sidecar authenticates with the
// X-QSDM-NGC-Secret header, which the role limiter never sees. It is therefore
// "anonymous" despite being an authenticated POST, and draws from the same
// 30/min bucket as every browser hitting any other unexempted endpoint.
//
// The observed effect: the sidecar's proof POST returned 429 and the trust
// surface fell to 0 of 3 fresh attestations, while a 10-minute sidecar cycle
// cannot come close to exhausting its own 30/min allowance. It was starved by
// unrelated traffic, not by its own rate.
//
// The pre-auth RateLimiter is deliberately left in place. It keys on
// identifier:METHOD:PATH with the caps above, which is the gate actually sized
// for this traffic, and it is what should bind. This bypass only removes the
// blunt global tier that was pre-empting it.
//
// This is the third instance of the same root cause, after /trust/ and
// /audit/. The general fix is per-client identity in the role limiter -- honour
// X-Forwarded-For under QSDM_TRUST_PROXY_HEADERS -- which is a separate change
// gated on confirming the node is reachable only through the proxy.
func isNGCMonitoringIngest(r *http.Request) bool {
	if r == nil {
		return false
	}
	_, ok := ngcIngestPaths[r.URL.Path]
	return ok
}
