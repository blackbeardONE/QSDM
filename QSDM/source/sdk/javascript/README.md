# qsdm-sdk — JavaScript / Node.js SDK

Official JavaScript client for the QSDM HTTP API. Mirrors `sdk/go` feature-for-feature.

> Published on npm as **`qsdm-sdk`** (the bare name `qsdm` was rejected by npm's
> name-similarity heuristic against `qs` / `esm` / `tsdx` etc.; the on-chain
> brand, repo, and binaries are still QSDM — only the npm package id is suffixed).

## Install

```bash
npm install qsdm-sdk
```

(Or vendor `qsdm.js` + `qsdm.d.ts` directly — the SDK has no runtime dependencies.)

## Quick start

```js
const { QSDMClient, isUnauthorized } = require('qsdm-sdk');

const client = new QSDMClient('http://node.example.com:8080');
client.setToken(process.env.QSDM_JWT); // or client.setAPIKey(...)

try {
    const balance = await client.getBalance('qsdm1addr...');
    const txId = await client.sendTransaction('from', 'to', 10.5);
    const topology = await client.getNetworkTopology();
    console.log({ balance, txId, topology });
} catch (err) {
    if (isUnauthorized(err)) {
        console.error('JWT expired — refresh and retry');
    } else {
        throw err;
    }
}
```

## API

| Method | Endpoint | Status |
|--------|----------|--------|
| `getBalance(address)` | `GET /api/v1/wallet/balance` | ✓ |
| `sendTransaction(from, to, amount)` | `POST /api/v1/wallet/send` | ✓ |
| `getTransaction(txID)` | `GET /api/v1/transactions/{id}` (plural; fixed in 0.3.1) | ✓ |
| `getRecentTransactions(address, limit)` | `GET /api/v1/wallet/transactions` | ⚠ deprecated 0.3.1 — endpoint not registered on the public API; use `GET /api/v1/receipts` for a recent-tx feed instead |
| `getLiveness()` / `getReadiness()` / `getHealth()` | `GET /api/v1/health/*` | ✓ |
| `getNodeStatus()` | `GET /api/v1/status` | ✓ |
| `getPeers()` | `GET /api/v1/network/peers` | ⚠ deprecated 0.3.1 — endpoint not registered on the public API; use `getNetworkTopology()` instead |
| `getNetworkTopology()` | `GET /api/v1/network/topology` | ✓ |
| `getMetricsJSON()` | `GET /api/metrics` | ⚠ deprecated 0.3.1 — registered only on the operator dashboard server, not the public API |
| `getMetricsPrometheus()` | `GET /api/metrics/prometheus` (raw text) | ⚠ deprecated 0.3.1 — see `getMetricsJSON` |

Methods marked ⚠ deprecated will be removed in 0.4.0. They currently
throw `ApiError` with `status: 404` against any production
`pkg/api` server. See `qsdm.js` for per-method JSDoc explaining the
endpoint mismatch each one suffers from.

All methods return `Promise<T>`. Errors on non-2xx responses are thrown as `ApiError`
with `status`, `url`, and `body` fields — use the `isNotFound` / `isUnauthorized`
helpers for common cases.

## Options

```js
new QSDMClient('http://node:8080', {
    fetch: myFetchImpl,     // override global fetch (useful for Node < 18)
    timeoutMs: 10_000,      // per-request timeout; 0 disables
});
```

## Testing

```bash
cd sdk/javascript
node --test qsdm.test.js
```

Requires Node 18+ (built-in `fetch` and `node:test`). The same command runs as
`prepublishOnly`, so a broken build cannot reach the registry.

## Releasing

The package is published from CI by `.github/workflows/sdk-javascript-publish.yml`.
Tag the repo with the matching version:

```bash
# bump version field in package.json + CHANGELOG.md, commit, then:
git tag sdk-js-v0.3.0
git push origin sdk-js-v0.3.0
```

The workflow verifies the tag suffix matches `package.json`, runs the test
suite, and publishes with `--provenance` (Sigstore attestation linking the
tarball to the GitHub Actions run). Only the `NPM_TOKEN` repository secret
is external.

## License

MIT — see [`LICENSE`](LICENSE) and [`CHANGELOG.md`](CHANGELOG.md).
