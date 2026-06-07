# QSDM API Reference

> **Canonical source.** This document is a curated, tutorial-style
> reference designed to onboard new SDK and integration authors. The
> complete machine-readable specification — every endpoint, every
> request and response shape, every error code — is
> [`openapi.yaml`](openapi.yaml) (currently v1.1.0). When the two
> ever disagree, `openapi.yaml` is authoritative; please open an
> issue so the drift can be fixed here.

## Overview

QSDM exposes a path-versioned REST API mounted under `/api/v1`. Every
public endpoint is documented in `openapi.yaml`; this file walks
through the most common ones and explains the cross-cutting concerns
(authentication, rate limiting, transparency surface, deprecation
flow, SDKs, WebSocket).

**Local-dev base URLs:**

- HTTPS (TLS 1.3, default): `https://localhost:8443/api/v1`
- HTTP  (insecure):          `http://localhost:8080/api/v1`

The HTTP listener is intended for local development only. Production
traffic terminates TLS at the operator's reverse proxy.

---

## Authentication

All write operations and most reads require a JWT Bearer token.
Acquire one via `POST /api/v1/auth/login`:

```
POST /api/v1/auth/login
Content-Type: application/json

{ "address": "<wallet-address>", "password": "<your-strong-password>" }
```

The response carries the access/refresh pair (no `csrf_token` field —
CSRF tokens are issued by a separate endpoint, see below):

```json
{
  "access_token":  "<jwt>",
  "refresh_token": "<jwt>",
  "expires_in":    900
}
```

Include the access token on subsequent requests:

```
Authorization: Bearer <access_token>
```

Tokens are quantum-safe (CRYSTALS-Dilithium / ML-DSA-87 signatures)
and short-lived (15 minutes). Use `refresh_token` to obtain a new
access token. Revoke the current token at any time via
**`POST /api/v1/auth/logout`** — subsequent requests presenting the
revoked token are rejected with `401`.

### Public-read endpoints

A growing set of read-only routes is intentionally unauthenticated so
SDK clients, third-party aggregators, and the landing-page widgets
at `https://qsdm.tech/trust` can scrape verifiable signals without
an operator-granted session:

- `/api/v1/status`
- `/api/v1/versions`
- `/api/v1/wallet/balance`
- `/api/v1/wallet/nonce`
- `/api/v1/audit/summary`
- `/api/v1/audit/items`
- `/api/v1/audit/badge.svg`
- `/api/v1/trust/attestations/summary`, `.../recent`
- `/api/v1/attest/recent-rejections`
- `/api/v1/receipts`, `/api/v1/receipts/{tx_id}`

All remain rate-limited per-IP by the same limiter that protects
authenticated routes.

### Request signing

`POST` / `PUT` / `DELETE` requests carry three additional headers
over a canonical payload:

```
X-Timestamp: <RFC 3339 UTC>
X-Nonce:     <opaque>
X-Signature: <base64url ML-DSA-87 signature, or HMAC fallback>
```

Replay protection is enforced by the storage layer on submit; see
`pkg/api/security.go` and `openapi.yaml` for the full rules.

### CSRF tokens

Browser-originated state-changing requests pass a CSRF token via
double-submit (cookie + header). Issue one via:

```
GET /api/v1/csrf-token
```

The response carries the token in JSON and also sets the
`qsdm_csrf` cookie. Echo the token on state-changing requests via
the `X-CSRF-Token` header. Server-to-server callers using JWT
bearer auth do not need this — the CSRF middleware applies only to
cookie-authenticated browser flows.

### `X-API-Key` (rate-limit identifier — not authentication)

The optional `X-API-Key` header is **not** an authentication
credential — it's an opaque per-client identifier the rate limiter
uses to group requests under a stable key (instead of the source
IP). Authentication is JWT Bearer; granting access never relies on
the key value. Source: `pkg/api/security.go::getClientIdentifier`.

---

## Endpoints (curated quick reference)

Exhaustive list: [`openapi.yaml`](openapi.yaml). The selection below
covers the most common integration paths.

### Wallet

#### Get balance (public read)

**GET** `/api/v1/wallet/balance?address=<address>`

```json
{
  "balance": 1000.0,
  "address": "wallet_address_123"
}
```

#### Read next nonce (public read)

**GET** `/api/v1/wallet/nonce?address=<address>`

Returns the next acceptable transaction nonce for the address.
Symmetric with `/wallet/balance`: read-only, no JWT, no signing.

#### Send transaction (authenticated)

**POST** `/api/v1/wallet/send`

Submits a transaction via the operator-managed wallet path. Returns
the canonical `transaction_id` and an initial `pending` status.
See `openapi.yaml` for the submesh-`422` and NVIDIA-lock-`403`
response shapes.

#### Self-custody signed submission (authenticated)

**POST** `/api/v1/wallet/submit-signed`

The v0.4.0+ self-custody path: client signs a `wallet.TransactionData`
envelope locally and submits it. The server verifies the
ML-DSA-87 signature over the canonical payload and never falls
back to a validator-side keypair. Replay protection comes from the
per-address monotonic nonce — fetch the next acceptable value via
`GET /api/v1/wallet/nonce` first.

#### Native CELL coin metadata

QSDM's public ecosystem is currently **CELL-only**. Treat Cell
(`CELL`) as the network's native coin, not as a secondary token. The
public wallet/account surfaces are `/wallet/balance`,
`/wallet/nonce`, `/receipts`, `/mining/blocks`, and
`/status`.

The codebase still contains early secondary-token scaffolding under
`/api/v1/tokens/*`, but those routes are not part of the public
explorer or Sky Fang integration surface. Do not build product flows
or user messaging around secondary tokens until QSDM ships a real token
standard, token balances, transfers, and explorer support.

### Transactions

#### Get transaction by id

**GET** `/api/v1/transactions/{tx_id}`

Note plural `transactions`; the path uses the brace-syntax form in
the spec. Returns the full record including settlement status,
block reference, and attestation metadata.

#### Recent transactions (public read)

**GET** `/api/v1/receipts`

Paginated recent transactions feed used by the chain dashboard.
Per-tx outcome probes are available at
`GET /api/v1/receipts/{tx_id}`.

### Authentication

#### Login

See **Authentication** above for the request and response shapes.

#### Logout

**POST** `/api/v1/auth/logout`

Revokes the caller's current access token. Subsequent requests
presenting the same token are rejected with `401` by the auth
middleware.

### Transparency

- `GET /api/v1/audit/summary` — checklist score + bucket breakdown
  (filterable to evidence provenance; pinned by
  `TestAuditAPI_WireParity_DashboardAndAPI`).
- `GET /api/v1/audit/items` — full filterable item list with
  closed-enum query validation.
- `GET /api/v1/audit/badge.svg` — server-rendered shields.io-style
  SVG status pill (suitable for embedding as
  `<img src="https://api.qsdm.tech/api/v1/audit/badge.svg">`);
  cached 60 s.
- `GET /api/v1/trust/attestations/summary`, `.../recent` — NGC
  attestation transparency surface (Major Update §8.5).
- `GET /api/v1/attest/recent-rejections` — v2 mining attestation
  rejection ring.

### Network

#### Get network topology (authenticated)

**GET** `/api/v1/network/topology`

Live JSON projection of the current peer set, suitable for the
dashboard's WebGL renderer. Returns an empty topology (`200`) on
cold-start when no `TopologyProvider` is wired.

### Health

- `GET /api/v1/health`        — full health snapshot.
- `GET /api/v1/health/live`   — liveness probe (always `200` if
                                 the process is alive).
- `GET /api/v1/health/ready`  — readiness probe; non-`200` means
                                 the node is not ready to serve.

These are **exempt from rate limiting** so probes are not throttled.

### Versioning catalogue

#### List API versions (public read)

**GET** `/api/v1/versions`

```json
{
  "current": "v1",
  "versions": [
    { "name": "v1", "prefix": "/api/v1", "status": "active" }
  ]
}
```

The `status` enum is `active` / `deprecated` / `sunset`. SDKs use
this to render deprecation banners without scraping every endpoint
response for `Deprecation` / `Sunset` headers.

The deprecation flow itself is implemented in `DeprecationMiddleware`
(see `pkg/api/versioning.go`):

- **Active** — pass through unchanged.
- **Deprecated** — responses carry `Deprecation: true|<RFC1123>` and
  (when set) `Sunset: <RFC1123>`, plus
  `Link rel="successor-version"` and `Link rel="deprecation"`
  pointing at the migration guide.
- **Sunset** — middleware short-circuits with **410 Gone** plus a
  JSON body pointing at the migration guide.

---

## Error responses

Per `pkg/api/error_sanitize.go` (audit row `api-04`), error responses
are sanitized — they never leak stack traces, file paths, or
internal state. Wire shape (from `pkg/api/middleware.go::writeErrorResponse`):

```json
{
  "error":   "Unauthorized",
  "message": "missing authentication",
  "status":  401
}
```

`error` is the standard `http.StatusText` string for the status
code; `message` is a sanitized human-readable detail; `status` is
the numeric HTTP status (mirrors the response status line for
clients that lose it).

### Common HTTP status codes

| Code | Meaning                                                                |
|------|------------------------------------------------------------------------|
| 200, 201 | Success                                                            |
| 400  | Invalid request parameters or malformed body                           |
| 401  | Authentication required or invalid                                     |
| 403  | Insufficient permissions (or NVIDIA-lock blocked)                      |
| 404  | Resource not found                                                     |
| 405  | Method not allowed                                                     |
| 410  | Sunset API version (see `/versions`)                                   |
| 422  | Submesh policy violation (when submesh profiles are loaded)            |
| 429  | Rate limit exceeded                                                    |
| 503  | Service not configured (e.g. wallet service did not initialize)        |

---

## Rate limiting

Default: **100 requests per client per minute**. Specific routes are
pinned tighter in `pkg/api/security.go` — for example
`/monitoring/ngc-challenge` is pinned at 15/min. `GET /api/v1/health`
and `/api/v1/health/*`
are exempt so probes are not throttled.

Every response carries the standard limit headers:

```
X-RateLimit-Limit:     100
X-RateLimit-Remaining: 95
X-RateLimit-Reset:     <unix-timestamp>
```

The client identifier is `X-API-Key` if present, else the source IP
(via the first hop of `X-Forwarded-For` if proxied, else
`r.RemoteAddr`).

Operator override: `[api] rate_limit_max_requests` /
`rate_limit_window` in the config file, or
`QSDM_API_RATE_LIMIT_MAX` / `QSDM_API_RATE_LIMIT_WINDOW` env vars.
The legacy `QSDM_*` env vars continue to be accepted during the
deprecation window — see `REBRAND_NOTES.md`.

---

## SDKs

### Go

```go
import "github.com/blackbeardONE/QSDM/sdk/go"

client := qsdm.NewClient("https://localhost:8443")
client.SetToken("<jwt>")

balance, err := client.GetBalance("wallet_address_123")
```

Module: `github.com/blackbeardONE/QSDM`. The Go SDK package lives
under `QSDM/source/sdk/go/`; the SDK ships in the same module as
the server so a single `go get` brings both. Package name: `qsdm`.

### JavaScript

NPM package: **`qsdm-sdk`**.

```javascript
import QSDMClient from 'qsdm-sdk';

const client = new QSDMClient('https://localhost:8443');
client.setToken('<jwt>');

const balance = await client.getBalance('wallet_address_123');
```

See `QSDM/source/sdk/javascript/README.md` for the full method
catalogue. Publish workflow:
`.github/workflows/sdk-javascript-publish.yml`.

---

## WebSocket

`GET /api/v1/contracts/traces/ws` streams recent contract traces
over a WebSocket connection (used by the dashboard and dev tools).
The endpoint is exempt from request-timeout middleware so a
long-running stream can outlive any HTTP deadline (see
`pkg/api/request_timeout.go`). Future real-time streaming endpoints
will follow the same `/api/v1/.../ws` convention.

---

## Versioning

The API is versioned by URL path (`/api/v1/...`). New majors get a
sibling prefix (`/api/v2`) and the old prefix stays live until its
sunset date. Minor changes are strictly additive — new fields, new
endpoints, looser validation — and never break a v1 client.

Current major: `v1`. See **Versioning catalogue** above for the live
catalogue and the deprecation-header contract.

---

## Support

For issues, documentation gaps, or canonical-spec drift, open an
issue on [GitHub](https://github.com/blackbeardONE/QSDM) or refer to
the project documentation under `QSDM/docs/docs/` — particularly
[`openapi.yaml`](openapi.yaml) for the machine-readable spec.

For security disclosure, see the RFC 9116 `security.txt` file
deployed at <https://qsdm.tech/.well-known/security.txt> (and the
matching legacy-compatibility location at
<https://qsdm.tech/security.txt>), source-of-truth in
`QSDM/deploy/landing/.well-known/security.txt`.

---

*Last verified: 2026-05-18 against `pkg/api/handlers.go`,
`pkg/api/middleware.go`, `pkg/api/security.go`,
`pkg/api/versioning.go`, and `openapi.yaml` v1.1.0.*
