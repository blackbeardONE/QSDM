# QSDM Security Audit Report

**Date:** May 2026 (Initial: December 2024)
**Status:** Hardening Pass Complete — Re-audit Recommended
**Auditor:** Security Review Team
**Target version:** v0.4.2

---

## Executive Summary

This document outlines the security audit findings for **QSDM** (Quantum-Secure Dynamic Mesh Ledger). The audit covers code review, vulnerability assessment, and security hardening recommendations.

**Overall Security Posture:** ✅ **Strong**

| Severity | Found | Fixed | Remaining |
|----------|------:|------:|----------:|
| 🔴 Critical | 2 | **2** | 0 |
| 🟠 High | 5 | **5** | 0 |
| 🟡 Medium | 8 | **8** | 0 |
| 🟢 Low | 3 | 0 | 3 *(documentation / CI tooling)* |
| **Total** | **18** | **15** | **3** |

All **Critical**, **High**, and **Medium** issues from the initial audit are resolved. The three remaining items are low-priority documentation and CI-tooling tasks tracked separately. The codebase ships with 58 dedicated security regression tests (CSRF, CORS, request-timeout, error-sanitization, log-sanitization, token-revocation, security headers, deprecation, timestamp validation) and clean `go test ./...` across every package.

---

## Security Strengths ✅

### 1. Quantum-Safe Cryptography
- ✅ **ML-DSA-87** — NIST FIPS 204 standard (256-bit security)
- ✅ **Quantum-safe signatures** — All transactions signed with ML-DSA-87
- ✅ **Quantum-safe tokens** — JWT tokens use ML-DSA-87 signatures (HMAC-SHA256 fallback in non-CGO builds)

### 2. SQL Injection Protection
- ✅ **Parameterized queries** — All SQL queries use prepared statements
- ✅ **No string concatenation** — SQL queries properly parameterized

### 3. Network Security
- ✅ **TLS 1.3** — Strong TLS configuration
- ✅ **Complete security header set** — HSTS, CSP (with `frame-ancestors 'none'`, `base-uri 'self'`, `form-action 'self'`, `object-src 'none'`), X-Frame-Options, X-Content-Type-Options, X-XSS-Protection, Referrer-Policy, Permissions-Policy, COOP, CORP
- ✅ **CORS middleware** — strict origin allowlist, env-driven config, denies wildcard+credentials combination
- ✅ **CSRF protection** — Synchronizer-token + double-submit cookie combined; per-user binding; Bearer-token bypass
- ✅ **Rate limiting** — Per-endpoint (login 5/min, register 3/min, wallet 10/min, default 100/min)
- ✅ **Per-request timeout** — 30s context deadline; Slowloris-resistant via `http.TimeoutHandler`

### 4. Authentication & Authorization
- ✅ **JWT tokens** with ML-DSA-87 signatures
- ✅ **Nonce-based replay protection** — Prevents replay attacks
- ✅ **Role-based access control** — RBAC middleware + per-role rate limiter
- ✅ **Account lockout** — 5 failed attempts → 15-minute lockout (15-minute counting window)
- ✅ **Token revocation** — Nonce-keyed blacklist with bounded memory; `/auth/logout` endpoint invalidates the caller's JWT
- ✅ **Strong password policy** — Argon2id hashing, 12+ chars, complexity + weak-password blacklist

### 5. Request Security
- ✅ **Request signing** — ML-DSA-87 or HMAC-SHA256 signature validation
- ✅ **Audit logging** — All API requests logged
- ✅ **Comprehensive input validation** — Address, transaction ID, amount (NaN/Inf-safe), timestamp (±30s clock skew, ≤24h age), parent cells, geo-tag, signature, ML-DSA-87 public key
- ✅ **Log-injection guard** — CRLF, NUL, ANSI escapes stripped before logging
- ✅ **Error sanitization** — `QSDM_PRODUCTION_MODE=true` returns correlation IDs only; full error stays in logs
- ✅ **Request size limit** — 1 MB body cap via `http.MaxBytesReader`

### 6. Security Monitoring
- ✅ **Dedicated security metrics** — 11 counters under `qsdm_security_*` prefix (failed logins, lockouts, rate-limit violations, CSRF failures, invalid/missing JWT, token revocations, signature failures, request timeouts, CORS rejections) — see [MED-8](#med-8-missing-security-monitoring).

---

## Critical Security Issues 🔴

### CRIT-1: Password Storage Without Hashing

**Severity:** 🔴 **CRITICAL** → ✅ **FIXED**

**Location:** `pkg/api/user.go`

**Status:** ✅ **RESOLVED**

**Implementation:**
- ✅ **Argon2id password hashing** implemented
- ✅ Memory-hard algorithm (64MB memory, 3 iterations, 4 threads)
- ✅ Constant-time password comparison
- ✅ Secure salt generation (16 bytes random)

**Fix Date:** Pre-existing (already implemented)

**Note:** Password hashing was already properly implemented using Argon2id, which is more secure than bcrypt for modern systems.

---

### CRIT-2: Insufficient Input Validation

**Severity:** 🔴 **CRITICAL** → ✅ **FIXED**

**Location:** `pkg/api/validation.go`, `pkg/api/handlers.go`, `cmd/qsdm/transaction/transaction.go`

**Status:** ✅ **RESOLVED**

**Implementation:**
- ✅ **Comprehensive validation module** (`pkg/api/validation.go`)
- ✅ Address validation (hex format, length limits: 32–128 chars)
- ✅ Transaction ID validation (alphanumeric, length limits: 16–128 chars)
- ✅ Amount validation (min: 0.00000001, max: 1,000,000,000, NaN/Inf-safe)
- ✅ String length limits (max 10,000 chars for general inputs)
- ✅ Password validation (12+ chars, complexity requirements, weak-password blacklist)
- ✅ Signature validation (hex format, length limits)
- ✅ Parent cells validation (max 10 cells)
- ✅ GeoTag validation (optional, max 100 chars)
- ✅ Timestamp validation (RFC3339, ±30s clock skew, ≤24h age) — added in v0.4.2 (see [MED-3](#med-3-insufficient-transaction-validation))

**Fix Date:** December 2024 (timestamp window: May 2026)

**Files Modified:**
- `pkg/api/validation.go`
- `pkg/api/handlers.go`
- `cmd/qsdm/transaction/transaction.go`

---

## High Priority Issues 🟠

### HIGH-1: Missing CSRF Protection

**Severity:** 🟠 **HIGH** → ✅ **FIXED**

**Location:** `pkg/api/csrf.go`, `pkg/api/middleware.go`, `pkg/api/handlers.go`

**Status:** ✅ **RESOLVED**

**Implementation:**
- ✅ **Combined synchronizer-token + double-submit cookie pattern** (`CSRFMiddleware` in `pkg/api/csrf.go`)
- ✅ Server-side store (`CSRFManager`) — 256-bit random tokens, 1-hour TTL, single periodic cleanup goroutine
- ✅ Constant-time comparison (`crypto/subtle.ConstantTimeCompare`) for both header↔cookie match and user-binding check
- ✅ **Per-user binding** — Tokens minted for an authenticated caller cannot be replayed by a different user
- ✅ **Cookie hardening** — `Secure`, `SameSite=Strict`, `Path=/`, `MaxAge` matches server-side TTL
- ✅ **Bearer-token bypass** — `Authorization: Bearer …` requests skip CSRF; CSRF only applies to ambient credentials
- ✅ **Token issuer endpoint** — `GET /api/v1/csrf-token` returns the token and sets the matching cookie
- ✅ **Safe-method bypass** — GET/HEAD/OPTIONS exempt
- ✅ **24 dedicated regression tests** in `pkg/api/csrf_test.go`
- ✅ **`qsdm_security_csrf_failures_total`** counter on every rejection

**Fix Date:** May 2026 (v0.4.2)

**Client contract:**
```
GET /api/v1/csrf-token
→ 200 { "csrf_token": "<base64url>", "expires_in_seconds": 3600 }
→ Set-Cookie: qsdm_csrf=<token>; Secure; SameSite=Strict; Path=/

Subsequent POST/PUT/DELETE/PATCH (cookie-session callers):
→ X-CSRF-Token: <value from /csrf-token>
   (cookie attached automatically; middleware validates header == cookie)
```

---

### HIGH-2: Insufficient Rate Limiting

**Severity:** 🟠 **HIGH** → ✅ **FIXED**

**Location:** `pkg/api/security.go`, `pkg/api/ratelimit_roles.go`

**Status:** ✅ **RESOLVED**

**Implementation:**
- ✅ **Per-endpoint rate limiting** implemented
- ✅ Login endpoint: 5 requests/minute
- ✅ Registration endpoint: 3 requests/minute
- ✅ Wallet send / submit-signed: 10 requests/minute
- ✅ Token mint/create/list: 15 / 10 / 60 per minute
- ✅ NGC proof / challenge: 30 / 15 per minute
- ✅ IP-based and API-key-based rate limiting
- ✅ Endpoint-specific rate limit keys
- ✅ Per-role rate limiter (`ratelimit_roles.go`)
- ✅ Periodic cleanup goroutine prevents unbounded memory growth
- ✅ `qsdm_security_rate_limit_violations_total` counter

**Fix Date:** December 2024 (metrics: May 2026)

**Note:** Exponential backoff is implicitly provided by the account-lockout subsystem ([HIGH-4](#high-4-weak-password-policy)). IP whitelist/blacklist can be added as a future enhancement.

---

### HIGH-3: Missing Request Size Limits

**Severity:** 🟠 **HIGH** → ✅ **FIXED**

**Location:** `pkg/api/middleware.go`, `pkg/api/server.go`

**Status:** ✅ **RESOLVED**

**Implementation:**
- ✅ **`RequestSizeLimitMiddleware`** in `pkg/api/middleware.go`
- ✅ Request body size limit: 1 MB
- ✅ Uses `http.MaxBytesReader` for automatic rejection
- ✅ Applied as the outermost handler in `Server.Start`

**Fix Date:** December 2024

---

### HIGH-4: Weak Password Policy

**Severity:** 🟠 **HIGH** → ✅ **FIXED**

**Location:** `pkg/api/validation.go`, `pkg/api/handlers.go`, `pkg/api/account_lockout.go`

**Status:** ✅ **RESOLVED**

**Implementation:**
- ✅ **Minimum password length: 12 characters** (increased from 8)
- ✅ **Complexity requirements:**
  - At least one uppercase letter
  - At least one lowercase letter
  - At least one number
  - At least one special character
- ✅ **Weak password detection** — common passwords blocked
- ✅ **Argon2id hashing** (memory-hard, constant-time comparison)
- ✅ **Account lockout** — 5 failed attempts within a 15-minute window triggers a 15-minute lockout (`AccountLockoutManager` in `pkg/api/account_lockout.go`)
- ✅ **`qsdm_security_failed_logins_total`** and **`qsdm_security_account_lockouts_total`** counters for SOC alerting on credential-stuffing waves

**Fix Date:** December 2024 (lockout + metrics: May 2026)

**Remaining Work (out-of-scope nice-to-haves):**
- Password history tracking (prevent reuse of the last N passwords) — tracked separately, not part of audit scope.

---

### HIGH-5: Missing Security Headers

**Severity:** 🟠 **HIGH** → ✅ **FIXED**

**Location:** `pkg/api/security.go`

**Status:** ✅ **RESOLVED**

**Implementation — every audit-required header is set with hardened directives:**

| Header | Value | Threat Mitigated |
|--------|-------|------------------|
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains; preload` | TLS downgrade / SSL strip |
| `X-Frame-Options` | `DENY` | Clickjacking (legacy browsers) |
| `X-Content-Type-Options` | `nosniff` | MIME-type confusion |
| `X-XSS-Protection` | `1; mode=block` | Legacy XSS reflection |
| `Content-Security-Policy` | `default-src 'self'; script-src 'self' https://cdn.jsdelivr.net; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' https://cdn.jsdelivr.net; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'` | XSS, base/form hijack, clickjacking |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | Cross-origin URL leakage |
| `Permissions-Policy` | `geolocation=(), microphone=(), camera=(), payment=(), usb=(), accelerometer=(), gyroscope=(), magnetometer=()` | Powerful-API blast radius from XSS |
| `Cross-Origin-Opener-Policy` | `same-origin` | `window.opener` cross-origin sidechannel |
| `Cross-Origin-Resource-Policy` | `same-origin` | Cross-origin embedding of API responses |
| `Server` | *(empty)* | Server fingerprinting |

- ✅ **Regression test** (`pkg/api/security_headers_test.go`) pins every required header so a future refactor cannot silently drop one.
- ✅ Applied as the **outermost** middleware so even early CORS / rate-limit / auth denials carry the full security header set.

**Fix Date:** May 2026 (v0.4.2)

---

## Medium Priority Issues 🟡

### MED-1: Error Messages May Leak Information

**Severity:** 🟡 **MEDIUM** → ✅ **FIXED**

**Location:** `pkg/api/error_sanitize.go`, `pkg/api/handlers.go`

**Status:** ✅ **RESOLVED**

**Implementation:**
- ✅ **`WriteServerError(w, logger, op, err)`** helper logs the full error server-side and returns only a correlation ID to the client
- ✅ **`QSDM_PRODUCTION_MODE=true`** toggles production behaviour: response body becomes `internal server error (id=<6-byte-hex>)`; raw error string never leaks
- ✅ Dev mode preserves raw error for fast iteration
- ✅ Applied at the high-leak paths (token creation, user registration, storage failures)
- ✅ Regression tests in `pkg/api/error_sanitize_test.go`

**Fix Date:** May 2026 (v0.4.2)

---

### MED-2: Missing Input Sanitization

**Severity:** 🟡 **MEDIUM** → ✅ **FIXED**

**Location:** `pkg/api/error_sanitize.go`, `pkg/api/handlers.go`, `pkg/api/middleware.go`, `cmd/qsdm/transaction/transaction.go`

**Status:** ✅ **RESOLVED**

**Implementation:**
- ✅ **`SanitizeForLog(s)` / `SanitizeForLogN(s, n)`** strip CR, LF, NUL, ANSI escape sequences, and other control characters; cap length to `MaxStringLength`
- ✅ **Applied at every user-input log call site:**
  - `Login` / `Register` (request address)
  - `AuthMiddleware` (request path on token-rejection paths)
  - `RequestSigningMiddleware` (request path)
  - `transaction.HandleTransaction` (sender, recipient — defence-in-depth even though already format-validated)
- ✅ Regression tests in `pkg/api/error_sanitize_test.go`

**Fix Date:** May 2026 (v0.4.2)

---

### MED-3: Insufficient Transaction Validation

**Severity:** 🟡 **MEDIUM** → ✅ **FIXED**

**Location:** `pkg/api/validation.go`, `cmd/qsdm/transaction/transaction.go`

**Status:** ✅ **RESOLVED**

**Implementation:**
- ✅ **Address format validation** — hex-encoded, 32–128 chars (`ValidateAddress`)
- ✅ **Amount validation** — min/max bounds, **NaN- and Infinity-safe** (the pre-existing NaN check was bypassed by comparison ordering; +Inf slipped through before the v0.4.2 fix)
- ✅ **Parent-cell validation** — each cell ID validated via `ValidateTransactionID`, max 10 parents
- ✅ **Timestamp validation** — `ValidateTimestamp` accepts RFC3339 / RFC3339Nano, rejects:
  - more than **30 seconds in the future** (clock skew window)
  - more than **24 hours in the past** (replay window)
- ✅ Wired into `ParseTransaction` so both API and P2P transaction flows enforce the same rules
- ✅ Regression tests in `pkg/api/validation_timestamp_test.go`

**Fix Date:** May 2026 (v0.4.2)

---

### MED-4: Missing API Versioning

**Severity:** 🟡 **MEDIUM** → ✅ **FIXED**

**Location:** `pkg/api/versioning.go`, `pkg/api/handlers.go`

**Status:** ✅ **RESOLVED**

**Implementation:**
- ✅ **`APIVersion` registry** (`pkg/api/versioning.go`) with lifecycle states: `active` → `deprecated` → `sunset`
- ✅ **`DeprecationMiddleware`** emits RFC 8594 `Deprecation` and `Sunset` HTTP-date headers plus `Link rel="successor-version"` and `Link rel="deprecation"` (migration guide URL)
- ✅ **`HTTP 410 Gone`** automatically returned for sunset versions
- ✅ **`GET /api/v1/versions`** public catalogue endpoint for SDK consumption
- ✅ Operator can mark a version deprecated at runtime via `RegisterAPIVersion` (no redeploy)
- ✅ Regression tests in `pkg/api/versioning_test.go`

**Fix Date:** May 2026 (v0.4.2)

---

### MED-5: Missing Request Timeout

**Severity:** 🟡 **MEDIUM** → ✅ **FIXED**

**Location:** `pkg/api/request_timeout.go`, `pkg/api/server.go`

**Status:** ✅ **RESOLVED**

**Implementation:**
- ✅ **`RequestTimeoutMiddleware(30s default)`** wraps every request with `context.WithTimeout` plus `http.TimeoutHandler`
- ✅ Slowloris-resistant: a handler that ignores `ctx.Done()` still terminates because `TimeoutHandler` buffers and emits 503
- ✅ Streaming-route bypass list (`/api/v1/contracts/traces/ws`) so the WebSocket trace stream is not killed by the deadline
- ✅ **`qsdm_security_request_timeout_total`** counter
- ✅ Regression tests in `pkg/api/request_timeout_test.go`

**Fix Date:** May 2026 (v0.4.2)

---

### MED-6: Missing CORS Configuration

**Severity:** 🟡 **MEDIUM** → ✅ **FIXED**

**Location:** `pkg/api/cors.go`, `pkg/api/server.go`

**Status:** ✅ **RESOLVED**

**Implementation:**
- ✅ **`CORSMiddleware`** with deny-by-default behaviour
- ✅ Exact-match origin allowlist (case-insensitive, de-duplicated at config load)
- ✅ **`Vary: Origin`** emitted automatically to prevent cache poisoning
- ✅ **Wildcard + credentials combination is rejected** per the Fetch spec
- ✅ Preflight (`OPTIONS`) handled with `204 No Content` + 10-minute `Access-Control-Max-Age`
- ✅ Configurable via env: `QSDM_CORS_ALLOWED_ORIGINS`, `QSDM_CORS_ALLOW_CREDENTIALS`
- ✅ **`qsdm_security_cors_rejections_total`** counter on every denial
- ✅ Regression tests in `pkg/api/cors_test.go`

**Fix Date:** May 2026 (v0.4.2)

---

### MED-7: Missing Session Management

**Severity:** 🟡 **MEDIUM** → ✅ **FIXED**

**Location:** `pkg/api/token_revocation.go`, `pkg/api/auth.go`, `pkg/api/handlers.go`

**Status:** ✅ **RESOLVED**

**Implementation:**
- ✅ **`TokenRevocationStore`** keyed by `Claims.Nonce` (256-bit per-token random, guaranteed unique)
- ✅ Background cleanup goroutine auto-evicts entries past their natural JWT expiry → **bounded memory** regardless of logout volume
- ✅ **`AuthManager.ValidateToken`** consults the revocation store; revoked tokens fail with `"token revoked"`
- ✅ **`POST /api/v1/auth/logout`** authenticated endpoint revokes the caller's current JWT
- ✅ **`qsdm_security_token_revocations_total`** and **`qsdm_security_token_revoked_hits_total`** counters
- ✅ Idempotent `Stop()` released on `Server.Stop` so test suites don't leak goroutines
- ✅ Regression tests in `pkg/api/token_revocation_test.go`

**Fix Date:** May 2026 (v0.4.2)

---

### MED-8: Missing Security Monitoring

**Severity:** 🟡 **MEDIUM** → ✅ **FIXED**

**Location:** `pkg/monitoring/security_metrics.go`, `pkg/monitoring/prometheus_scrape.go`

**Status:** ✅ **RESOLVED**

**Implementation:**
- ✅ **Dedicated `pkg/monitoring/security_metrics.go`** module with 11 atomic counters
- ✅ Registered as the **`qsdm_security`** Prometheus collector
- ✅ Counters exposed under the `qsdm_security_*` prefix:

| Metric | Recorded From |
|--------|---------------|
| `qsdm_security_failed_logins_total` | `handlers.Login` on auth failure |
| `qsdm_security_account_lockouts_total` | `handlers.Login` when remaining attempts reach 0 |
| `qsdm_security_rate_limit_violations_total` | `RateLimitMiddleware` on every 429 |
| `qsdm_security_csrf_failures_total` | `CSRFMiddleware` on every rejection |
| `qsdm_security_auth_invalid_token_total` | `AuthMiddleware` on invalid/expired/revoked JWT |
| `qsdm_security_auth_missing_token_total` | `AuthMiddleware` on missing Authorization header |
| `qsdm_security_token_revocations_total` | `TokenRevocationStore.Revoke` (explicit logout) |
| `qsdm_security_token_revoked_hits_total` | `ValidateToken` on revoked-token reuse attempts |
| `qsdm_security_request_signature_failed_total` | `RequestSigningMiddleware` on bad X-Signature |
| `qsdm_security_request_timeout_total` | `RequestTimeoutMiddleware` on 30s deadline trip |
| `qsdm_security_cors_rejections_total` | `CORSMiddleware` on disallowed origin |

- ✅ **`monitoring.GetSecurityStats()`** returns a JSON snapshot for dashboard panels
- ✅ Counters are monotonic atomics on the hot path — no shared mutex contention
- ✅ **`ResetSecurityMetricsForTest()`** helper for test isolation

**Fix Date:** May 2026 (v0.4.2)

**Operational guidance:** Any non-zero **rate** on these counters represents either a misconfigured client or a probing attacker. Pair with Prometheus alerting on:
- `rate(qsdm_security_failed_logins_total[5m]) > 1` — credential-stuffing
- `rate(qsdm_security_csrf_failures_total[5m]) > 0` — CSRF probing
- `rate(qsdm_security_rate_limit_violations_total[1m]) > 5` — scraping / brute force
- `rate(qsdm_security_cors_rejections_total[5m]) > 0` — unauthorised cross-origin caller

---

## Low Priority Issues 🟢

### LOW-1: Missing Security Documentation

**Severity:** 🟢 **LOW**

**Status:** **PARTIALLY ADDRESSED** — this audit document is now the canonical security overview, plus every fix references its implementation file. A dedicated developer-facing "security best practices" guide is still pending.

**Recommendation:**
- ⏳ Create a security guide for application developers
- ⏳ Document the incident-response runbook (lockout escalation, token revocation, log forensics)

**Fix Priority:** **LOW**

---

### LOW-2: Missing Security Testing

**Severity:** 🟢 **LOW**

**Status:** **PARTIALLY ADDRESSED**

**What is now in place:**
- ✅ **58 dedicated security regression tests** (CSRF 24, CORS 7, request-timeout 3, error-sanitize 5, log-sanitize 4, token-revocation 6, versioning 5, security-headers 1, timestamp 7)
- ✅ Full `go test ./...` runs clean across every package
- ✅ `go vet ./...` clean
- ✅ `gosec`, `staticcheck`, **and** `govulncheck` enforced on every push / PR via [`.github/workflows/security-scan.yml`](../../../.github/workflows/security-scan.yml). All three ceilings are zero after the July 2026 hardening pass, so CI fails on the first new medium-or-higher gosec finding, pkg/api staticcheck finding, or reachable vulnerability.

**Baseline-disposition table** (each ceiling traces to specific tracked-for-follow-up findings that the hardening pass did NOT introduce):

| Tool          | Ceiling | Findings covered (file → rule)                                                                                                                                                                                                                |
|---------------|---------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `gosec`       | 0       | No accepted residual findings. Operator-controlled local file paths carry narrow rule-specific dispositions; treasury URLs are literal-loopback-only and redirects are disabled. |
| `staticcheck` | 0       | No accepted residual findings in `pkg/api/...`; stale helpers and dead compatibility code were removed. |
| `govulncheck` | 0       | No accepted residual findings. Go 1.25.11 closes GO-2026-5037, GO-2026-5038, and GO-2026-5039; Kad-DHT removal previously closed GO-2024-3218 exposure. |

**What is still pending:**
- ⏳ External penetration testing
- ⏳ OWASP ZAP / nuclei in CI/CD

**Fix Priority:** **LOW**

---

### LOW-3: Missing Security Headers Documentation

**Severity:** 🟢 **LOW**

**Status:** **ADDRESSED IN AUDIT DOC** — see [HIGH-5](#high-5-missing-security-headers) above for the full header table and threat mapping. Inline source comments in `pkg/api/security.go` also annotate each header with the threat it mitigates.

**Recommendation:**
- ⏳ Surface the same table in the SDK / operator deployment guide

**Fix Priority:** **LOW**

---

## Security Recommendations Summary

### Immediate Actions

1. ✅ **Implement password hashing** (CRIT-1)
2. ✅ **Add comprehensive input validation** (CRIT-2)
3. ✅ **Implement CSRF protection** (HIGH-1)
4. ✅ **Improve rate limiting** (HIGH-2)
5. ✅ **Add request size limits** (HIGH-3)

### Short-Term Actions

6. ✅ **Strengthen password policy** (HIGH-4)
7. ✅ **Verify security headers** (HIGH-5)
8. ✅ **Sanitize error messages** (MED-1)
9. ✅ **Add input sanitization** (MED-2)
10. ✅ **Enhance transaction validation** (MED-3)

### Long-Term Actions

11. ✅ **Implement API versioning strategy** (MED-4)
12. ✅ **Add request timeouts** (MED-5)
13. ✅ **Configure CORS** (MED-6)
14. ✅ **Implement session management** (MED-7)
15. ✅ **Add security monitoring** (MED-8)

### Open (Low Priority)

16. ⏳ **Developer security guide** (LOW-1)
17. ⏳ **Automated security testing in CI** (LOW-2)
18. ⏳ **SDK / operator-side header documentation** (LOW-3)

---

## Security Checklist

### Authentication & Authorization
- [x] Password hashing (Argon2id)
- [x] Strong password policy (12+ chars, complexity)
- [x] Account lockout after failed attempts (5 attempts → 15-min lockout)
- [x] Token revocation/blacklist
- [x] Session management (`/auth/logout`)
- [x] Role-based access control (RBAC)

### Input Validation & Sanitization
- [x] Comprehensive input validation
- [x] Format validation (addresses, IDs, etc.)
- [x] Length limits on all inputs
- [x] Input sanitization before logging (CRLF/NUL/ANSI stripped)
- [x] Transaction validation (incl. timestamp, NaN/Inf-safe amounts)

### Network Security
- [x] TLS 1.3 configuration
- [x] Security headers (all recommended + COOP/CORP)
- [x] CORS configuration (deny-by-default, env-configurable allowlist)
- [x] CSRF protection (synchronizer-token + double-submit + user binding)
- [x] Request size limits (1 MB)

### Rate Limiting & DoS Protection
- [x] Per-endpoint rate limiting
- [x] IP-based rate limiting
- [x] Account lockout (functional equivalent of exponential backoff)
- [x] Request timeouts (30s context deadline)
- [x] Resource limits

### Monitoring & Logging
- [x] Security metrics (`qsdm_security_*` Prometheus counters)
- [x] Audit logging
- [ ] Intrusion detection *(low priority — future SIEM integration)*
- [ ] Anomaly detection *(low priority — future SIEM integration)*
- [x] Security alerts *(operator wires Prometheus alertmanager rules from the recommended thresholds in [MED-8](#med-8-missing-security-monitoring))*

### Code Security
- [x] SQL injection protection
- [x] XSS prevention (CSP `frame-ancestors 'none'`, `object-src 'none'`, `base-uri 'self'`)
- [x] CSRF protection
- [x] Secure error handling (`QSDM_PRODUCTION_MODE`)
- [x] Secure coding practices (constant-time compares, bounded goroutines, NaN/Inf-safe arithmetic)

---

## Testing Recommendations

### Security Testing
- [ ] **Penetration testing** — External security testing
- [ ] **Vulnerability scanning** — Automated scanning (OWASP ZAP, etc.)
- [x] **Code review** — Security-focused code review (every fix above)
- [ ] **Dependency scanning** — Check for vulnerable dependencies
- [ ] **Static analysis** — Use tools like gosec, staticcheck

### Test Scenarios
- [x] Brute force attack on login *(account-lockout regression tested)*
- [x] CSRF attack attempts *(24 regression tests in `csrf_test.go`)*
- [x] Rate limit bypass attempts *(role + endpoint limiters regression tested)*
- [x] Large request body attacks *(1 MB cap regression tested)*
- [x] Invalid input attacks *(`validation_*_test.go`)*
- [ ] XSS attack attempts *(browser-side; covered by CSP but no automated harness yet)*
- [ ] SQL injection attempts *(parametrised queries verified by review; no automated fuzz harness)*

---

## Compliance Considerations

### Data Protection
- [ ] **GDPR compliance** — If handling EU data
- [x] **Data encryption** — At rest (storage backends) and in transit (TLS 1.3)
- [ ] **Data retention** — Policies and implementation
- [ ] **Right to deletion** — User data deletion

### Security Standards
- [x] **OWASP Top 10** — All applicable OWASP Top 10 categories addressed by fixes above
- [x] **CWE Top 25** — CWE-117 (log injection), CWE-352 (CSRF), CWE-209 (info disclosure), CWE-307 (brute force), CWE-770 (resource consumption), CWE-693 (security headers) all addressed
- [x] **NIST Framework** — Align with NIST cybersecurity framework (FIPS 204 ML-DSA-87 throughout)

---

## Next Steps

1. ✅ Review this audit with development team
2. ✅ Prioritise fixes based on severity
3. ✅ Implement fixes starting with critical issues
4. **Re-audit** by an independent reviewer after this hardening pass
5. **Schedule regular audits** (quarterly recommended)
6. **Wire alertmanager rules** from the `qsdm_security_*` counter thresholds in [MED-8](#med-8-missing-security-monitoring)

---

## References

- [OWASP Top 10](https://owasp.org/www-project-top-ten/)
- [CWE Top 25](https://cwe.mitre.org/top25/)
- [NIST Cybersecurity Framework](https://www.nist.gov/cyberframework)
- [OWASP API Security](https://owasp.org/www-project-api-security/)
- [OWASP Secure Headers Project](https://owasp.org/www-project-secure-headers/)
- [RFC 8594 — The Sunset HTTP Header Field](https://www.rfc-editor.org/rfc/rfc8594)
- [Fetch Living Standard — CORS](https://fetch.spec.whatwg.org/#http-cors-protocol)

---

**Report Status:** Hardening Pass Complete
**Next Review:** Independent re-audit recommended after the v0.4.2 release

*This audit is a living document and is updated as fixes are implemented. The full list of source files touched by the May 2026 hardening pass is tracked in the v0.4.2 release evidence.*
