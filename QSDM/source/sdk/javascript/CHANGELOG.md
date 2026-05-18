# qsdm-sdk (JavaScript SDK) — Changelog

All notable changes to the published `qsdm-sdk` npm package are recorded here.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning: [Semantic Versioning](https://semver.org/).

## [0.3.1] — 2026-05-18

### Fixed

- **`getTransaction(txID)` now hits the correct path.** Earlier
  builds (≤0.3.0) called `GET /api/v1/transaction/{id}` (singular),
  which returns 404 in production — the typo dated back to the
  pre-rebrand scaffolding window. The actual handler is registered
  at `GET /api/v1/transactions/{id}` (plural) per
  `pkg/api/handlers.go:269-270` and the `openapi.yaml` spec entry
  for `/transactions/{txId}`. The bug was not caught earlier
  because `qsdm.test.js` starts a fake `httptest`-style server that
  accepts any URL and asserts only on query parameters; the test
  is now path-pinned (`assert.equal(req.url, '/api/v1/transactions/tx-7')`)
  so a future regression of this kind fails CI.

### Deprecated

Four methods continue to call endpoints that are not registered on
the public `pkg/api` server. They have always returned `ApiError`
with `status: 404` against any production node; the JSDoc on each
now states this explicitly and gives the migration path. Pending
removal in **0.4.0**:

- `getRecentTransactions(address, limit)` — calls
  `/api/v1/wallet/transactions`, which has no handler. The public
  surface has no per-address recent-tx endpoint today; callers
  wanting a recent-tx feed should use
  `GET /api/v1/receipts` (paginated chain transparency feed) and
  filter client-side, or maintain their own off-chain index.
- `getPeers()` — calls `/api/v1/network/peers`. Closest analogues
  are `/api/admin/peers` (admin-only, mTLS-required;
  `pkg/api/handlers_admin.go:54`) and the dashboard's
  `/api/topology` (`internal/dashboard/dashboard.go:261`); neither
  is reachable from a JWT-bearer SDK client. Use
  `getNetworkTopology()` for the same data instead.
- `getMetricsJSON()` — calls `/api/metrics`, which is registered
  only on the operator dashboard server
  (`internal/dashboard/dashboard.go:258`, `requireAuth`-gated),
  not on the public API.
- `getMetricsPrometheus()` — same dashboard-only mismatch; calls
  `/api/metrics/prometheus`.

No public method or constructor signature changes; this is a
patch-level release. All 17 existing tests still pass; the
`getTransaction` test is the only one whose assertions were
strengthened.

## [0.3.0] — 2026-05-11

### Changed (publish-time rename)

- npm package id renamed from `qsdm` → `qsdm-sdk`. The original bare name was
  rejected by the registry's typo-squatting heuristic (similarity to `qs`,
  `esm`, `tsdx`, etc.) on first-publish, so the package was rebranded under
  the conventional `<project>-sdk` suffix. No other identifiers change: the
  on-chain brand is still QSDM, the GitHub repo is still `blackbeardONE/QSDM`,
  the binaries are still `qsdm` / `qsdmminer-gui`, the import-time class is
  still `QSDMClient`. Only the `npm install <name>` and `require()` strings
  pick up the `-sdk` suffix. The provenance attestation from the rejected
  publish attempt is preserved on Rekor at logIndex `1506312160` for audit.

## [0.3.0-attempt1] — 2026-05-10 (unpublished; see above)

Publish-ready release. No runtime API changes from `0.2.0`; this release adds
the metadata, packaging, and provenance machinery required for a clean npm
publish via the `sdk-javascript-publish` workflow.

### Added

- `repository`, `bugs`, `homepage` fields in `package.json` so the npm
  registry page links back to the canonical source.
- `exports` field with explicit `types` / `require` / `default` conditions so
  bundlers and modern Node resolvers pick up `qsdm.d.ts` automatically.
- `publishConfig.provenance: true` so each release on npm carries a signed
  Sigstore attestation linking the published tarball to the GitHub Actions
  run that produced it.
- `prepublishOnly` script — `node --test qsdm.test.js` runs as a pre-publish
  gate so a broken build cannot reach the registry.
- `LICENSE` (MIT) and `CHANGELOG.md` are now packaged in the tarball
  (`files` allowlist) so downstream consumers see attribution and history
  without needing to clone the monorepo.
- Expanded test suite (`qsdm.test.js`): now exercises every public method —
  `getNodeStatus` (typed mapping), `getPeers`, `getNetworkTopology`,
  `getMetricsJSON`, `getMetricsPrometheus` (raw text), `getRecentTransactions`,
  `getTransaction`, `sendTransaction`, plus error paths (`isNotFound`,
  `isUnauthorized`), `setToken` / `setAPIKey` header injection, baseURL
  trailing-slash trim, and the per-request timeout.

### Changed

- License field corrected to `MIT` to match the monorepo `LICENSE` file
  (the previous `Apache-2.0` value was a copy-paste error from the Go SDK
  scaffolding window; no published release ever shipped with it).

## [0.2.0] — earlier (in-tree, unpublished)

Initial feature-parity rewrite covering every endpoint exposed by `sdk/go`:
context-style options (`fetch`, `timeoutMs`), `ApiError` class with
`isNotFound` / `isUnauthorized` helpers, baseURL trailing-slash trim, typed
`getNodeStatus` projection, and all wallet / health / network / metrics
endpoints.
