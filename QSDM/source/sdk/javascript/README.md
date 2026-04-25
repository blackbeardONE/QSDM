# qsdm — JavaScript / Node.js SDK

Official JavaScript client for the QSDM HTTP API. Mirrors `sdk/go` feature-for-feature.

## Install

```bash
npm install qsdm
```

(Or vendor `qsdm.js` + `qsdm.d.ts` directly — the SDK has no runtime dependencies.)

## Quick start

```js
const { QSDMClient, isUnauthorized } = require('qsdm');

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

| Method | Endpoint |
|--------|----------|
| `getBalance(address)` | `GET /api/v1/wallet/balance` |
| `sendTransaction(from, to, amount)` | `POST /api/v1/wallet/send` |
| `getTransaction(txID)` | `GET /api/v1/transaction/{id}` |
| `getRecentTransactions(address, limit)` | `GET /api/v1/wallet/transactions` |
| `getLiveness()` / `getReadiness()` / `getHealth()` | `GET /api/v1/health/*` |
| `getNodeStatus()` | `GET /api/v1/status` |
| `getPeers()` | `GET /api/v1/network/peers` |
| `getNetworkTopology()` | `GET /api/v1/network/topology` |
| `getMetricsJSON()` | `GET /api/metrics` |
| `getMetricsPrometheus()` | `GET /api/metrics/prometheus` (raw text) |

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

Requires Node 18+ (built-in `fetch` and `node:test`).
