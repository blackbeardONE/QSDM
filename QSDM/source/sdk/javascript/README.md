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
| `getWalletNonce(address)` | `GET /api/v1/wallet/nonce` | ✓ |
| `getStreamActionNonce(address)` | `GET /api/v1/streams/nonce` | ✓ |
| `sendTransaction(from, to, amount)` | `POST /api/v1/wallet/send` | ✓ |
| `getTransaction(txID)` | `GET /api/v1/transactions/{id}` (plural; fixed in 0.3.1) | ✓ |
| `getStreams(filters)` | `GET /api/v1/streams` | ✓ |
| `getStream(streamID)` | `GET /api/v1/streams/{stream_id}` | ✓ |
| `submitStreamAction(envelope)` | `POST /api/v1/streams/actions/submit-signed` | ✓ |
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

## Active-use CELL billing

Version `0.3.3` adds the crash-safe runtime for `qsdm/streams/v1` and reads
the action nonce directly from consensus before signing:

```js
const {
    QSDMClient,
    CellStreamWallet,
    CellStreamServiceMeter,
} = require('qsdm-sdk');

const client = new QSDMClient('https://api.qsdm.tech');
const wallet = new CellStreamWallet({
    client,
    address: activeWalletAddress,
    // QSDM Hive, a native wallet bridge, or another local signer supplies this.
    // The SDK never receives or stores the wallet private key.
    signAction: (action, bytes) => walletBridge.signStreamAction(action, bytes),
});

const meter = new CellStreamServiceMeter({
    wallet,
    storage: durableNonSecretStorage,
    sessionSigner: secureDeviceSessionSigner,
    // This endpoint verifies the session receipt, wraps it in the provider's
    // wallet-signed receipt action, and resolves { confirmed: true } only
    // after QSDM confirmation.
    receiptSubmitter: (receipt) => serviceAPI.submitUsageReceipt(receipt),
});

await meter.initialize();
await meter.recover(await service.isActuallyActive());

service.on('started', () => meter.onServiceStarted({
    streamId: 'vpn-device-001',
    provider: providerWalletAddress,
    serviceId: 'qsdm-vpn',
    deviceIdHash: saltedDeviceIDHash,
    priceDust: 200000000,
    pricePeriodSeconds: 2592000,
    budgetDust: 200000000,
    maxActiveSeconds: 2592000,
    expiresAt: '2027-09-01T00:00:00Z',
}));
service.on('stopped', () => meter.onServiceStopped());
```

The service must call `onServiceStarted` only after its tunnel, session, or
other paid capability is actually available. It must call `onServiceStopped`
as soon as that capability stops. The meter:

- checkpoints active time to durable storage;
- submits cumulative receipts every 30 seconds and at lifecycle boundaries;
- retries the same signed receipt after uncertain delivery;
- does not count application downtime after a crash;
- serializes wallet actions so one SDK instance cannot race its own nonce; and
- stores public runtime state only, never wallet or session private keys.

The temporary Ed25519 session key must live in the platform secure keystore.
The provider-side `receiptSubmitter` must be idempotent by
`(stream_id, sequence)`, return `{ confirmed: true }` only after chain
confirmation, and must not hold its QSDM wallet key in a public client. A
backend can use `CellStreamWallet.submitAction` with `action: 'receipt'` after
authenticating the service session.

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
npm test
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
