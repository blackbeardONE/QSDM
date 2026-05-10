# qsdm (JavaScript SDK) — Changelog

All notable changes to the published `qsdm` npm package are recorded here.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning: [Semantic Versioning](https://semver.org/).

## [0.3.0] — 2026-05-10

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
