# qsdm-sdk (JavaScript SDK) — Changelog

All notable changes to the published `qsdm-sdk` npm package are recorded here.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning: [Semantic Versioning](https://semver.org/).

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
