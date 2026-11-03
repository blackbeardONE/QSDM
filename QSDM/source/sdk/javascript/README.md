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
const { QSDMClient } = require('qsdm-sdk');

const client = new QSDMClient('https://api.qsdm.tech');
const address = '5d28b82565421e3666ef7a493e0d39fc89d3450009a32cbda72aa89c7cb820e7';

const balance = await client.getBalance(address);         // CELL
const { next } = await client.getWalletNonce(address);    // nonce for the next transfer
const status = await client.getNodeStatus();
console.log({ balance, next, tip: status.chainTip });
```

Public routes need no credentials. `setToken(jwt)` is only for operator
routes.

## Sending CELL (signed transfers)

A QSDM node accepts a CELL transfer from a client only as an envelope signed
with **ML-DSA-87**, posted to `POST /api/v1/wallet/submit-signed` (Ed25519
transactions were retired on 2026-10-16). Since block 816,600 each transfer
must also name **Proof-of-Entanglement parents**: 2 to 10 transactions
committed in the last 8,640 blocks (about 24 hours). Without them the node
answers HTTP 422 `proof-of-entanglement: ...`.

The SDK never holds a private key. Use the path that matches where the key
lives.

### In a browser, with the QSDM Wallet extension

The extension injects `window.qsdm`. It does not sign arbitrary transfers for
a website: it asks QSDM Hive to send CELL. Hive shows the recipient and
amount, and after the user approves it reads the nonce and the parents, signs
with the wallet's ML-DSA-87 key and submits to the node Hive is configured
for (fee 0).

```js
const { waitForWalletProvider, sendTransferWithProvider } = require('qsdm-sdk');

const provider = await waitForWalletProvider(); // null when the extension is missing
if (!provider) {
    location.assign('https://qsdm.tech/wallet-start.html?login=new');
} else {
    const result = await sendTransferWithProvider(provider, {
        recipient: '1111111111111111111111111111111111111111111111111111111111111111',
        amount: 1.25, // CELL, at most 8 decimals
    });
    // { transaction_id, status: 'pending', broadcast, sender }
}
```

`sendTransferWithProvider` first calls `qsdm_requestAccounts` (Hive asks the
user to connect the site only the first time), then `qsdm_sendTransaction`.

### With your own signer

`sendTransfer` reads the nonce and the parents from the node, builds the
envelope, hands it with its signing bytes to your `signTransfer`, and submits
the result:

```js
const { QSDMClient } = require('qsdm-sdk');

const client = new QSDMClient('https://api.qsdm.tech');
const out = await client.sendTransfer({
    sender: myAddress, // hex(sha256(ML-DSA-87 public key))
    recipient: '1111111111111111111111111111111111111111111111111111111111111111',
    amount: 1.25,
    fee: 0,
    // An ML-DSA-87 (FIPS 204) signature over signingBytes with an empty
    // context string, made by a key you hold.
    signTransfer: async (envelope, signingBytes) => ({
        signature: await mySigner.sign(signingBytes),
        public_key: mySigner.publicKey,
    }),
});
// out: { transaction_id, status: 'pending', broadcast: 'block-pending' }
const receipt = await client.getReceipt(out.transaction_id); // 404 until it is in a block
```

`signTransfer` returns `{ signature, public_key }` (hex strings or
`Uint8Array`s), or the complete signed envelope: an object, or its JSON as
`qsdmcli wallet sign-tx` and the web wallet's `qsdm_wallet_sign_transaction`
WASM helper print it. The SDK refuses to submit a returned envelope that
differs from the one it prepared.

Step by step the same is `prepareTransfer` → `transferSigningBytes` → your
signer → `submitSignedTransfer`. An envelope signed entirely elsewhere (for
example `qsdmcli wallet sign-tx --auto-nonce --auto-parents`) goes straight to
`submitSignedTransfer`. Parents expire, so sign shortly before you submit.
`prepareTransfer` generates the transfer `id` with Web Crypto
(`globalThis.crypto`, available in browsers and Node 19+); on Node 18 pass
`id` yourself (16-128 characters of `0-9 A-Z a-z _ -`).

`transferSigningBytes(envelope)` returns exactly what the node verifies: the
envelope with `signature: ""` and without `public_key`, encoded as Go's
`encoding/json` encodes it (field order `id, sender, recipient, amount, fee,
geotag, parent_cells, nonce, signature, timestamp`; `<`, `>`, `&`, U+2028 and
U+2029 escaped). The tests pin it to bytes printed by the node's own code.

### Refusals

`submitSignedTransfer` and `sendTransfer` throw `TransferError` (an
`ApiError` with `reason`, `serverMessage` and, for duplicates,
`transactionId`):

| `reason` | HTTP | What to do |
|---|---|---|
| `poe_parents` (`isPoERejection`) | 422 | Parents missing or outside the window: call `sendTransfer` again (fresh parents, same nonce). |
| `signature` | 422 | Sign `transferSigningBytes()` of exactly the envelope you submit. |
| `nonce` | 409 | Nonce replay or gap: prepare the transfer again. |
| `duplicate` | 409 | This ID, or another transfer with this nonce, is already waiting for the next block (one per sender per block). Follow it with `getReceipt`. |
| `insufficient_balance` | 402 | Balance below amount + fee. |
| `invalid` | 400 | See `serverMessage`. |
| `rate_limited` | 429 | Wait, then retry. |
| `unavailable` | 501, 503 | Queue full, read-only follower or writes paused; retry later. |

`sendTransfer` makes one attempt; every call reads a fresh nonce and fresh
parents.

## API

| Method | Endpoint | Public |
|--------|----------|--------|
| `getBalance(address)` | `GET /api/v1/wallet/balance` | ✓ |
| `getWalletNonce(address)` | `GET /api/v1/wallet/nonce` | ✓ |
| `getChainParents({ limit })` | `GET /api/v1/chain/parents` | ✓ |
| `getPoEParents()` | `GET /api/v1/chain/parents` (receipts fallback when the route is missing) | ✓ |
| `getReceipt(txID)` / `getReceipts({ from, to, limit })` | `GET /api/v1/receipts/{tx_id}` / `GET /api/v1/receipts` | ✓ |
| `prepareTransfer(request)` | the nonce and parents reads above | ✓ |
| `submitSignedTransfer(envelope)` / `sendTransfer(request)` | `POST /api/v1/wallet/submit-signed` | ✓ (the signature is the authorization) |
| `getStreamActionNonce(address)` | `GET /api/v1/streams/nonce` | ✓ |
| `getStreams(filters)` / `getStream(streamID)` | `GET /api/v1/streams`, `GET /api/v1/streams/{stream_id}` | ✓ |
| `submitStreamAction(envelope)` | `POST /api/v1/streams/actions/submit-signed` | ✓, but 503 while the pilot pauses stream actions |
| `getLiveness()` / `getReadiness()` / `getHealth()` | `GET /api/v1/health/*` | ✓ |
| `getNodeStatus()` | `GET /api/v1/status` | ✓ |
| `sendTransaction(from, to, amount)` | none | ⚠ deprecated 0.4.0 — rejects without a request; see below |
| `getTransaction(txID)` | `GET /api/v1/transactions/{id}` | ⚠ deprecated 0.4.0 — operator JWT (401 otherwise); use `getReceipt` |
| `getNetworkTopology()` | `GET /api/v1/network/topology` | ⚠ deprecated 0.4.0 — operator JWT (401 otherwise); use `getNodeStatus().peers` |
| `getRecentTransactions(address, limit)` | `GET /api/v1/wallet/transactions` | ⚠ deprecated 0.3.1 — not registered on any node; use `getReceipts` |
| `getPeers()` | `GET /api/v1/network/peers` | ⚠ deprecated 0.3.1 — not registered on any node; use `getNodeStatus().peers` |
| `getMetricsJSON()` / `getMetricsPrometheus()` | `GET /api/metrics`, `/api/metrics/prometheus` | ⚠ deprecated 0.3.1 — operator dashboard only |

Deprecated methods are kept for compatibility; the routes they call fail for
public clients (401 or 404). `sendTransaction` used to post an unsigned
`{from, to, amount}` body to `POST /api/v1/wallet/send`. That route is
operator-only (an operator JWT plus a request signature made with the node's
own key), it spends from the node's own wallet rather than from `from`, and
it reads `{recipient, amount, fee, geotag, parent_cells}`, so the call never
worked against a node. It now rejects with
`err.code === 'QSDM_UNSIGNED_SEND_REMOVED'` and points to the signed path.

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

`setToken(jwt)` sends `Authorization: Bearer` for operator routes.
`setAPIKey(key)` sends `X-API-Key`, which QSDM nodes do not read (kept for
gateways that do).

## Testing

```bash
cd sdk/javascript
npm test
```

The suite needs Node 19+ (built-in `fetch`, `node:test` and
`globalThis.crypto`); the SDK itself runs on Node 18. The same command runs as
`prepublishOnly`, so a broken build cannot reach the registry.

## Releasing

The package is published from CI by `.github/workflows/sdk-javascript-publish.yml`.
Tag the repo with the matching version:

```bash
# bump version field in package.json + CHANGELOG.md, commit, then:
git tag sdk-js-v0.4.0
git push origin sdk-js-v0.4.0
```

The workflow verifies the tag suffix matches `package.json`, runs the test
suite, and publishes with `--provenance` (Sigstore attestation linking the
tarball to the GitHub Actions run). Only the `NPM_TOKEN` repository secret
is external.

## License

MIT — see [`LICENSE`](LICENSE) and [`CHANGELOG.md`](CHANGELOG.md).
