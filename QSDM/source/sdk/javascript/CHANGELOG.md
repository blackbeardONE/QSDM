# qsdm-sdk (JavaScript SDK) — Changelog

All notable changes to the published `qsdm-sdk` npm package are recorded here.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning: [Semantic Versioning](https://semver.org/).

## [0.5.0] - 2026-11-07

### Added

- QSDM-20 tokens (`QSDM/docs/docs/QSDM_20.md`): `sendQSDM20` (prepare, have
  a key the caller holds sign the 32-byte digest, submit), `prepareQSDM20`,
  `submitQSDM20`, `getAccount` (`GET /api/v1/mining/account`, the account
  nonce a token transaction carries) and the reads `getQSDM20Tokens`,
  `getQSDM20Token`, `getQSDM20Holders` and `getQSDM20Balances`.
- `validateQSDM20Action`, `qsdm20TokenId`, `qsdm20SigningPreimage`,
  `qsdm20SigningDigest`, `qsdm20TxId` and `walletAddress`. Digests and ids
  are pinned to vectors the node's own code computes
  (`sdk/go/testdata/qsdm20_vectors.json`); nonces and fees above 2^53 stay
  exact as bigints or decimal strings.

Nodes refuse token transactions until the network sets an activation height.
The QSDM Wallet extension cannot sign them yet.

## [0.4.0] - 2026-11-03

Public QSDM nodes accept a CELL transfer from a client only as an envelope
signed with ML-DSA-87 (`POST /api/v1/wallet/submit-signed`), and since block
816,600 the envelope must name Proof-of-Entanglement parents; without them the
node answers HTTP 422. Earlier releases could not build or submit such a
transfer.

### Added

- Signed CELL transfers: `prepareTransfer` (reads `next` from
  `GET /api/v1/wallet/nonce` and the parents), `transferSigningBytes` (the
  exact bytes the node verifies, pinned to vectors printed by the node's own
  `pkg/wallet` code), `submitSignedTransfer`, and
  `sendTransfer({ ..., signTransfer })`, which signs with a key the caller
  holds and also accepts a full envelope from `qsdmcli wallet sign-tx` or the
  web wallet's WASM signer.
- `getChainParents` and `getPoEParents` (`GET /api/v1/chain/parents`, falling
  back to successful receipts only when that route is missing, the rule
  qsdmcli and QSDM Hive use), `getReceipt` and `getReceipts`.
- `TransferError` (an `ApiError` with `reason`, `serverMessage` and
  `transactionId`) and `isPoERejection`.
- QSDM Wallet extension glue: `getWalletProvider`, `waitForWalletProvider`
  and `sendTransferWithProvider`. The extension's `window.qsdm` cannot sign a
  website's envelope; it asks QSDM Hive to build, sign and submit a transfer
  after the user approves it.
- TypeScript declarations for all of the above.

### Changed

- `sendTransaction()` now rejects with `code: 'QSDM_UNSIGNED_SEND_REMOVED'`
  and sends no request. It posted an unsigned `{from, to, amount}` body to
  `POST /api/v1/wallet/send`, an operator-only route (operator JWT plus a
  request signature made with the node's own key) that spends from the
  node's own wallet and reads `{recipient, amount, fee, geotag,
  parent_cells}`; it never worked against a node.
- `CellStreamWallet` confirms actions through `GET /api/v1/receipts/{tx_id}`
  (public) instead of `GET /api/v1/transactions/{id}` (operator JWT), and
  reports an action whose receipt failed on chain.

### Deprecated

- `getTransaction` (operator JWT; use `getReceipt`) and `getNetworkTopology`
  (operator JWT).
- `getPeers`, `getRecentTransactions`, `getMetricsJSON` and
  `getMetricsPrometheus` stay deprecated with corrected reasons: the first
  two call routes no node registers, the metrics calls exist only on the
  operator dashboard. They were announced for removal in 0.4.0 and are kept
  in this release for compatibility.
- `setAPIKey` is documented as a no-op for QSDM nodes, which do not read
  `X-API-Key`.

## [0.3.3] - 2026-07-28

### Fixed

- CELL Stream actions now use the live consensus `action_nonce` instead of
  the one-based wallet-transfer `next` nonce.
- First stream actions correctly accept nonce `0`.
- Older clients can fall back to `/wallet/nonce.nonce`; the transfer-only
  `.next` field is never used for stream actions.

### Added

- `QSDMClient.getStreamActionNonce(address)` for the explicit
  `GET /api/v1/streams/nonce` contract.

## [0.3.2] - 2026-07-26

### Added

- `CellStreamWallet` for serialized nonce lookup, canonical action signing,
  signed-envelope submission, and chain confirmation.
- `CellStreamServiceMeter` for active-service metering, 30-second cumulative
  receipts, lifecycle pause/resume/close, durable checkpoints, crash recovery,
  and idempotent retry of uncertain signed receipts.
- Canonical CELL Stream action and receipt byte helpers shared by service
  clients and provider backends.
- `QSDMClient.getWalletNonce(address)` for self-custody action construction.
- A dedicated `qsdm-sdk/cell-stream-runtime` package export.

### Security

- Runtime persistence contains only public state and already-signed retry
  payloads. Wallet keys and Ed25519 session private keys remain behind caller
  supplied signing interfaces.
- Restart recovery never bills elapsed process downtime automatically. The
  application must report whether the paid service is actually active.

## [0.3.1] — 2026-05-18

### Typings

- **`@deprecated` JSDoc tags added to `qsdm.d.ts` for the four
  removed-in-0.4.0 methods** (`getRecentTransactions`, `getPeers`,
  `getMetricsJSON`, `getMetricsPrometheus`). TypeScript compilers
  and IDEs that honor JSDoc deprecation tags (VS Code, Cursor,
  WebStorm, etc.) will now show strikethrough on usages plus the
  migration path inline. Without this, runtime callers reading
  the JS file see the deprecation banners but TypeScript users
  importing only the type surface would not. `getTransaction` also
  carries a JSDoc note about the 0.3.1 plural-path fix so a future
  reader can correlate the spec, the runtime SDK, and the typings
  without three-file triangulation. No declared interface or class
  signature changed; this is type-system metadata only.

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
  the binaries are still `qsdm` / `qsdmminer-console`, the import-time class is
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
