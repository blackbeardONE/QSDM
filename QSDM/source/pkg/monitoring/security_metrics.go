package monitoring

import (
	"sync/atomic"
)

// Security metrics surface the rate of security-relevant events that
// security operators want to alert on. They are exposed via the standard
// Prometheus collector wiring (see SecurityMetricsCollector below) and via
// the JSON GetSecurityStats() snapshot used by the dashboard.
//
// Counters are intentionally process-local atomics rather than going
// through the global Metrics struct — they are read on hot paths
// (every failed auth, every rate-limit reject, every CSRF check) and the
// shared sync.RWMutex on Metrics is a measurable contention source when
// the validator is under load.
var (
	failedLoginsTotal                       atomic.Int64
	accountLockoutsTotal                    atomic.Int64
	rateLimitViolationsTotal                atomic.Int64
	csrfFailuresTotal                       atomic.Int64
	authInvalidTokenTotal                   atomic.Int64
	authMissingTokenTotal                   atomic.Int64
	tokenRevocationsTotal                   atomic.Int64
	tokenRevokedHitsTotal                   atomic.Int64
	requestSignatureFailedTotal             atomic.Int64
	requestTimeoutTotal                     atomic.Int64
	corsRejectionsTotal                     atomic.Int64
	jwtSecondaryKeyHitsTotal                atomic.Int64
	requestSignatureSecondaryKeyHitsTotal   atomic.Int64
)

// RecordFailedLogin increments the failed-login counter. Called from the
// /auth/login handler whenever authentication fails (wrong password,
// unknown user, etc).
func RecordFailedLogin() { failedLoginsTotal.Add(1) }

// RecordAccountLockout increments the lockout counter. Called from the
// account-lockout subsystem when an address is moved into the locked
// state after exceeding the failed-attempt threshold.
func RecordAccountLockout() { accountLockoutsTotal.Add(1) }

// RecordRateLimitViolation increments the rate-limit counter. Called from
// the rate-limit middleware whenever a request is rejected with 429.
func RecordRateLimitViolation() { rateLimitViolationsTotal.Add(1) }

// RecordCSRFFailure increments the CSRF failure counter. Called from the
// CSRF middleware whenever a state-changing request is rejected (missing
// token, mismatched cookie, user-binding mismatch).
func RecordCSRFFailure() { csrfFailuresTotal.Add(1) }

// RecordAuthInvalidToken increments the invalid-JWT counter. Distinct
// from "missing" so dashboards can separate "client forgot the header"
// (low-severity) from "client presented a forged/expired token"
// (high-severity, potential attack).
func RecordAuthInvalidToken() { authInvalidTokenTotal.Add(1) }

// RecordAuthMissingToken increments the missing-Authorization counter.
func RecordAuthMissingToken() { authMissingTokenTotal.Add(1) }

// RecordTokenRevocation increments the explicit-revocation counter
// (e.g. on /auth/logout).
func RecordTokenRevocation() { tokenRevocationsTotal.Add(1) }

// RecordTokenRevokedHit increments the counter for "validated token was
// found on the revocation list" — i.e. an attacker (or an honest client
// that lost the logout race) tried to reuse a revoked token.
func RecordTokenRevokedHit() { tokenRevokedHitsTotal.Add(1) }

// RecordRequestSignatureFailed increments the counter for failed request
// signatures (X-Signature/X-Timestamp/X-Nonce). High-cardinality signal
// for forged-request attack waves.
func RecordRequestSignatureFailed() { requestSignatureFailedTotal.Add(1) }

// RecordRequestTimeout increments the counter for requests that exceeded
// the per-request context deadline. High values typically indicate either
// a downstream stall (DB hot-spot) or a slowloris/long-poll attack.
func RecordRequestTimeout() { requestTimeoutTotal.Add(1) }

// RecordCORSRejection increments the counter for requests rejected by
// the CORS middleware (origin not on the allowlist). High values from a
// single origin indicate a misconfigured client or a probing attacker.
func RecordCORSRejection() { corsRejectionsTotal.Add(1) }

// RecordJWTSecondaryKeyHit increments the counter for JWT verifications
// that succeeded against the VERIFY-ONLY secondary key during a
// rotation window (audit row rotation-01). The counter is the
// operator's "is the rotation window being exercised?" signal — a
// flat counter for >= max(access-token-TTL, refresh-token-TTL)
// means no in-flight tokens still need the old key, so the operator
// can clear the secondary (cutover complete). A non-zero rate after
// the window should have closed means a stale client is still
// presenting old-key tokens; usually benign (long-lived refresh
// tokens) but worth investigating.
func RecordJWTSecondaryKeyHit() { jwtSecondaryKeyHitsTotal.Add(1) }

// RecordRequestSignatureSecondaryKeyHit increments the counter for
// X-Signature verifications that succeeded against the VERIFY-ONLY
// secondary request-signing key during a rotation window. Same
// semantic as RecordJWTSecondaryKeyHit but for the per-request
// HMAC path (security.go::RequestSigner.VerifyRequest).
func RecordRequestSignatureSecondaryKeyHit() {
	requestSignatureSecondaryKeyHitsTotal.Add(1)
}

// --- Read accessors (used by SecurityMetricsCollector and dashboard JSON) ---

func FailedLoginsCount() int64                       { return failedLoginsTotal.Load() }
func AccountLockoutsCount() int64                    { return accountLockoutsTotal.Load() }
func RateLimitViolationsCount() int64                { return rateLimitViolationsTotal.Load() }
func CSRFFailuresCount() int64                       { return csrfFailuresTotal.Load() }
func AuthInvalidTokenCount() int64                   { return authInvalidTokenTotal.Load() }
func AuthMissingTokenCount() int64                   { return authMissingTokenTotal.Load() }
func TokenRevocationsCount() int64                   { return tokenRevocationsTotal.Load() }
func TokenRevokedHitsCount() int64                   { return tokenRevokedHitsTotal.Load() }
func RequestSignatureFailedCount() int64             { return requestSignatureFailedTotal.Load() }
func RequestTimeoutCount() int64                     { return requestTimeoutTotal.Load() }
func CORSRejectionsCount() int64                     { return corsRejectionsTotal.Load() }
func JWTSecondaryKeyHitsCount() int64                { return jwtSecondaryKeyHitsTotal.Load() }
func RequestSignatureSecondaryKeyHitsCount() int64   { return requestSignatureSecondaryKeyHitsTotal.Load() }

// ResetSecurityMetricsForTest zeroes every counter. Test-only.
func ResetSecurityMetricsForTest() {
	failedLoginsTotal.Store(0)
	accountLockoutsTotal.Store(0)
	rateLimitViolationsTotal.Store(0)
	csrfFailuresTotal.Store(0)
	authInvalidTokenTotal.Store(0)
	authMissingTokenTotal.Store(0)
	tokenRevocationsTotal.Store(0)
	tokenRevokedHitsTotal.Store(0)
	requestSignatureFailedTotal.Store(0)
	requestTimeoutTotal.Store(0)
	corsRejectionsTotal.Store(0)
	jwtSecondaryKeyHitsTotal.Store(0)
	requestSignatureSecondaryKeyHitsTotal.Store(0)
}

// SecurityMetricsCollector returns a Prometheus collector for the security
// counters. Wire with prometheus.RegisterCollector("security", SecurityMetricsCollector()).
//
// All metrics use the qsdm_security_* prefix so they sit in a dedicated
// dashboard panel separate from the chain / mempool / mining counters.
func SecurityMetricsCollector() MetricCollector {
	return func() []Metric {
		return []Metric{
			{Name: "qsdm_security_failed_logins_total", Help: "Failed login attempts (wrong password, unknown user).", Type: MetricCounter, Value: float64(FailedLoginsCount())},
			{Name: "qsdm_security_account_lockouts_total", Help: "Accounts locked after exceeding failed-attempt threshold.", Type: MetricCounter, Value: float64(AccountLockoutsCount())},
			{Name: "qsdm_security_rate_limit_violations_total", Help: "Requests rejected by the rate limiter (HTTP 429).", Type: MetricCounter, Value: float64(RateLimitViolationsCount())},
			{Name: "qsdm_security_csrf_failures_total", Help: "State-changing requests rejected by CSRF middleware.", Type: MetricCounter, Value: float64(CSRFFailuresCount())},
			{Name: "qsdm_security_auth_invalid_token_total", Help: "Requests rejected with invalid or expired JWT.", Type: MetricCounter, Value: float64(AuthInvalidTokenCount())},
			{Name: "qsdm_security_auth_missing_token_total", Help: "Requests rejected for missing Authorization header.", Type: MetricCounter, Value: float64(AuthMissingTokenCount())},
			{Name: "qsdm_security_token_revocations_total", Help: "Tokens explicitly revoked (e.g. via /auth/logout).", Type: MetricCounter, Value: float64(TokenRevocationsCount())},
			{Name: "qsdm_security_token_revoked_hits_total", Help: "Requests that presented a token already on the revocation list.", Type: MetricCounter, Value: float64(TokenRevokedHitsCount())},
			{Name: "qsdm_security_request_signature_failed_total", Help: "Requests with failed X-Signature verification.", Type: MetricCounter, Value: float64(RequestSignatureFailedCount())},
			{Name: "qsdm_security_request_timeout_total", Help: "Requests cancelled by the per-request context deadline.", Type: MetricCounter, Value: float64(RequestTimeoutCount())},
			{Name: "qsdm_security_cors_rejections_total", Help: "Requests rejected by CORS middleware (origin not on allowlist).", Type: MetricCounter, Value: float64(CORSRejectionsCount())},
			{Name: "qsdm_security_jwt_secondary_key_hits_total", Help: "JWT verifications that succeeded against the rotation-window secondary HMAC key (audit row rotation-01). Flat for >= max-token-TTL means cutover is safe.", Type: MetricCounter, Value: float64(JWTSecondaryKeyHitsCount())},
			{Name: "qsdm_security_request_signature_secondary_key_hits_total", Help: "X-Signature verifications that succeeded against the rotation-window secondary HMAC key (audit row rotation-01). Flat means no in-flight signed requests still need the old key.", Type: MetricCounter, Value: float64(RequestSignatureSecondaryKeyHitsCount())},
		}
	}
}

// GetSecurityStats returns a JSON-friendly snapshot of every security
// counter. Used by the dashboard /api/v1/health and admin endpoints to
// render a security panel without going through Prometheus.
func GetSecurityStats() map[string]interface{} {
	return map[string]interface{}{
		"failed_logins_total":            FailedLoginsCount(),
		"account_lockouts_total":         AccountLockoutsCount(),
		"rate_limit_violations_total":    RateLimitViolationsCount(),
		"csrf_failures_total":            CSRFFailuresCount(),
		"auth_invalid_token_total":       AuthInvalidTokenCount(),
		"auth_missing_token_total":       AuthMissingTokenCount(),
		"token_revocations_total":        TokenRevocationsCount(),
		"token_revoked_hits_total":       TokenRevokedHitsCount(),
		"request_signature_failed_total": RequestSignatureFailedCount(),
		"request_timeout_total":          RequestTimeoutCount(),
		"cors_rejections_total":          CORSRejectionsCount(),
	}
}
