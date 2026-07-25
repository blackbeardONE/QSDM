# CELL Streams

CELL Streams is QSDM's bounded payment protocol for services billed by active
use. A VPN can charge only while a tunnel is active, for example, while a
paused or disconnected session stops adding billable seconds.

The first protocol version is `qsdm/streams/v1`.

## What this solves

A normal subscription charges a full period even when the service is unused.
A CELL Stream instead combines:

- a maximum CELL budget held in escrow;
- an exact rate expressed as integer CELL dust per period;
- a maximum number of active seconds;
- an expiry time;
- a service and privacy-preserving device identifier;
- a temporary session key that can acknowledge usage but cannot spend the
  wallet outside the stream.

The provider can receive only the amount justified by accepted cumulative
usage receipts. Closing a stream pays any unsettled earned amount and returns
the unused escrow to the payer.

## The billing model

QSDM uses integer dust and a rational rate:

```text
accrued_dust =
  floor(cumulative_active_seconds * price_dust / price_period_seconds)
```

For a price of 2 CELL per 30 active days:

```text
price_dust          = 200000000
price_period_seconds = 2592000
```

The first active second accrues 77 dust. The cumulative calculation avoids
rounding every receipt independently, so exactly 2 CELL has accrued after
2,592,000 accepted active seconds.

This does not mean 2 CELL is charged every second. It means the 2 CELL period
price is prorated over active seconds.

## Why there is not a transaction every second

The service may update an on-screen estimate once per second, but it should
submit a cumulative receipt every 30 to 60 seconds or at a lifecycle boundary.
One receipt says, for example, "this session has now used 900 active seconds."

Cumulative receipts:

- keep chain traffic small;
- preserve exact total billing;
- reject duplicate, reordered, or decreasing counters;
- let settlement happen in practical batches.

## Lifecycle

### 1. Open

The payer signs an `open` action with the QSDM ML-DSA wallet. Opening:

- locks `budget_dust` from the payer;
- fixes the provider, service, rate, duration cap, and expiry;
- authorizes a temporary Ed25519 session public key;
- starts the stream in `active` state.

The private session key stays on the payer's device. It is not a wallet key and
cannot transfer CELL.

### 2. Receipt

While active, the device signs a cumulative usage receipt with the temporary
session key. The provider wraps it in a wallet-signed `receipt` action.

Consensus rejects a receipt when:

- its sequence is not exactly the next sequence;
- cumulative seconds do not increase;
- cumulative seconds exceed the stream cap;
- cumulative seconds advance faster than elapsed active wall time;
- its timestamp is outside the authorized window;
- its session signature is invalid;
- the stream is paused or closed.

### 3. Pause and resume

Only the payer may pause or resume. A paused stream accepts no usage receipt.
Consensus records paused duration and excludes it from the maximum billable
wall time after resume.

Turning off the service should:

1. stop the local active-second counter;
2. submit the last cumulative receipt;
3. submit `pause`.

Reconnecting submits `resume` before counting new active seconds.

### 4. Settle

Only the provider may submit `settle`. Settlement credits:

```text
accrued_dust - already_settled_dust
```

It does not change the rate, budget, or usage counter.

### 5. Close

Only the payer may close. Closing atomically:

- credits any unsettled accrued amount to the provider;
- refunds `budget_dust - accrued_dust` to the payer;
- permanently prevents more receipts.

## Wallet and session signatures

Root actions use the active QSDM ML-DSA-87 wallet. The canonical action JSON is
signed locally and submitted as:

```json
{
  "action": {
    "id": "stream-action-001",
    "sender": "<payer-address>",
    "stream_id": "vpn-device-001",
    "action": "open",
    "provider": "<provider-address>",
    "service_id": "qsdm-vpn",
    "device_id_hash": "<sha256-hex>",
    "session_public_key": "<ed25519-public-key-hex>",
    "price_dust": 200000000,
    "price_period_seconds": 2592000,
    "budget_dust": 200000000,
    "max_active_seconds": 2592000,
    "expires_at": "2026-09-01T00:00:00Z",
    "nonce": 0,
    "timestamp": "2026-08-01T00:00:00Z"
  },
  "signature": "<mldsa-signature-hex>",
  "public_key": "<mldsa-public-key-hex>"
}
```

Use the CLI to sign without exposing the wallet key:

```powershell
qsdmcli wallet sign-stream-action `
  --in "$HOME/.qsdm/wallet.json" `
  --passphrase-file "$HOME/.qsdm/passphrase" `
  --action-file .\open-stream.json
```

The output is ready for the signed submission endpoint.

## HTTP API

```text
POST /api/v1/streams/actions/submit-signed
GET  /api/v1/streams
GET  /api/v1/streams/{stream_id}
```

List filters:

- `payer`
- `provider`
- `service_id`
- `status` (`active`, `paused`, or `closed`)

The read response includes `remaining_budget_dust` and `unsettled_dust`.

## SDKs

The Go and JavaScript SDKs expose:

- list streams;
- get one stream;
- submit a signed stream action envelope.

Wallet creation, session-key custody, receipt cadence, and service start/stop
remain application responsibilities.

## Economic and security boundaries

- CELL comes from the payer's existing balance. Streams do not mint rewards.
- The entire budget is escrowed at open, so the provider is not relying on a
  future unfunded promise.
- The generic transaction `amount` is always zero. The wallet-signed integer
  `budget_dust` is the only escrow amount, avoiding floating-point ambiguity.
- Every root action is signature-checked at admission and again during block
  application.
- Action IDs, wallet nonces, receipt sequences, timestamps, and cumulative
  counters provide replay protection.
- All consensus monetary fields use integer dust.
- Device identifiers should be SHA-256 digests of an application-specific,
  salted identifier, never a raw hardware serial.
- A provider must not receive the QSDM wallet private key, keystore,
  passphrase, recovery words, or the session private key.

## Integration status

The consensus state, replay, API, CLI signing, and Go/JavaScript SDK surfaces
are implemented. A service such as QSDM VPN still needs its own adapter to
create the temporary session key, count only active use, submit periodic
receipts, and connect pause/resume to the actual service lifecycle.
