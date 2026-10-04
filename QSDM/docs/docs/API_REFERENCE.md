# API reference

The public HTTP API of the QSDM pilot network. Every endpoint on this page can
be called from the internet today. Examples are real responses from the live
network (October 2026), trimmed for length.

> [!NOTE]
> QSDM is a pilot network (pre-mainnet) with a single block producer during
> recovery. While the recovery lasts, the API accepts reads, signed wallet
> transfers and mining submissions; other writes are paused. See
> [Network status](NETWORK_STATUS.md).

## Base URL

```text
https://api.qsdm.tech/api/v1
```

`https://qsdm.tech/api/v1` serves the same read endpoints for pages on
qsdm.tech. **Mining endpoints are served only on `api.qsdm.tech`.**

| Convention | Detail |
|---|---|
| Format | JSON (`Content-Type: application/json`) unless noted. Request bodies are limited to 64 KiB on the public write routes. |
| Authentication | None for the endpoints on this page. Writes are authorised by an ML-DSA-87 signature inside the request body, not by a login. |
| Addresses | 64 lowercase hex characters: `hex(sha256(ML-DSA-87 public key))`. |
| Amounts | Fields named `*_cell` or `balance`/`amount` are CELL (8 decimals). Fields named `*_dust` are integers; 1 CELL = 100,000,000 dust. |
| Heights and times | Block heights are integers. Timestamps are RFC 3339; `/chain/blocks` uses the server's local offset, the other endpoints use UTC (`Z`). |
| Versioning | All routes live under `/api/v1`; see `GET /versions` and [API versioning](API_VERSIONING.md). |

### Rate limits

Limits are per client IP address. When you exceed one you get **HTTP 429**,
sometimes with a `Retry-After` header; wait and retry with backoff.

| Group | Limit |
|---|---|
| `/status`, `/versions`, `/chain/blocks`, `/tasks*` | 600 requests per minute |
| Most other read endpoints | 100 requests per minute |
| `POST /wallet/submit-signed` | 10 requests per minute |
| Mining endpoints on `api.qsdm.tech` | Limited per IP at the edge (bursts allowed); `POST /mining/submit` about 60 per minute |

Health endpoints are not rate-limited. Do not poll faster than you need: a new
block arrives about every 10 seconds.

### Errors

Most errors use this JSON shape:

```json
{ "error": "Bad Request", "message": "recipient must be a lowercase 64-character wallet address", "status": 400 }
```

Some endpoints answer differently, so check the HTTP status first:

- mining, receipt and block endpoints may return a **plain-text** body;
- a paused mining service returns `503 {"error":"mining_unavailable","detail":"…"}` with `Retry-After`;
- routes paused during recovery return `503 {"status":"read_only","message":"…"}`;
- unknown or blocked paths return an HTML 404 or 405 page.

| Status | Meaning |
|---|---|
| 200 / 202 | OK / accepted for the next block |
| 400 | Malformed request or failed validation |
| 401 | The route needs operator credentials and is not public |
| 402 | Not enough CELL for amount + fee |
| 404 | Not found |
| 405 | Wrong HTTP method |
| 409 | Duplicate transaction, or a nonce replay or gap |
| 422 | Signature does not verify |
| 429 | Rate limit |
| 503 | Paused during recovery, or the service is busy; retry later |

## Endpoint summary

| Method | Path | Purpose |
|---|---|---|
| GET | `/status` | Node, chain, supply, consensus and mining flags |
| GET | `/health`, `/health/live`, `/health/ready` | Health checks |
| GET | `/versions` | API version catalogue |
| GET | `/mining/blocks` | Recent block headers |
| GET | `/chain/blocks` | Full blocks with transactions |
| GET | `/receipts`, `/receipts/{tx_id}` | Transaction receipts |
| GET | `/mining/emission` | Supply and reward schedule |
| GET | `/wallet/balance` | CELL balance of an address |
| GET | `/wallet/nonce` | Last used nonce of an address |
| GET | `/mining/account` | Balance and nonce in one call |
| POST | `/wallet/submit-signed` | Submit an ML-DSA-87 signed transfer |
| GET | `/wallet/recovery/capsules`, `/wallet/recovery/capsules/{locator}`, `/wallet/recovery/nonce` | Encrypted recovery capsules (read) |
| GET | `/mining/work` | Current mining work (`api.qsdm.tech` only) |
| GET | `/mining/challenge` | Fresh signed challenge for attestation |
| POST | `/mining/submit` | Submit a mining proof (`api.qsdm.tech` only) |
| GET | `/mining/enrollments`, `/mining/enrollment/{node_id}` | Miner enrollment registry |
| GET | `/tasks`, `/tasks/state`, `/tasks/{task_id}`, `/tasks/actions` | Task registry (read) |
| GET | `/streams`, `/streams/{stream_id}`, `/streams/nonce` | CELL Streams (read) |
| GET | `/trust/attestations/summary`, `/trust/attestations/recent` | Attestation transparency |
| GET | `/audit/summary`, `/audit/items`, `/audit/badge.svg` | Internal checklist |
| GET | `/referrals/reward-pool`, `/referrals/status` | Referral pool status |

## Status and health

### `GET /status`

The single best endpoint for "what is the network doing". Also described field by
field on [Network status](NETWORK_STATUS.md).

```bash
curl -s https://api.qsdm.tech/api/v1/status
```

```json
{
  "node_id": "12D3KooWHT2APYkMStuaiLZvto7ZmrPmUag4TStT7MryGn69feum",
  "version": "hardened-legacy-20261002-d7ffcd4-hl2",
  "git_sha": "d7ffcd4e80f69c3db3f2bbcf4c7f34e08cc49d25",
  "build_date": "2026-10-02T05:23:22Z",
  "uptime": "4h43m36s",
  "chain_tip": 755940,
  "peers": 0,
  "node_role": "validator",
  "network": "QSDM · CELL",
  "coin": { "name": "Cell", "symbol": "CELL", "decimals": 8, "smallest_unit": "dust" },
  "tokenomics": {
    "cap_dust": 9000000000000000,
    "cap_cell": "90000000.00000000",
    "emitted_cell": "2694857.96712780",
    "block_reward_cell": "3.56490987",
    "current_epoch": 0,
    "next_halving_height": 12623041,
    "target_block_time_seconds": 10,
    "blocks_per_epoch": 12623040
  },
  "task_actions_ready": true,
  "consensus_auth": {
    "signed_consensus_supported": true,
    "require_signed_votes": false,
    "signed_consensus_active": false,
    "unsigned_consensus_traffic_accepted": true,
    "task_action_signatures_active": true,
    "tx_content_root_active": true
  },
  "mining": {
    "protocol_versions_accepted": [2],
    "fork_v2_active": true,
    "fork_v2_tc_active": false,
    "attestation_types_required": ["nvidia-cc-v1", "nvidia-hmac-v1"],
    "min_enroll_stake_dust": 1000000000,
    "enrollment_contract": "qsdm/enroll/v2",
    "signed_enrollment_required": true,
    "deferred_bond_from_rewards": true
  }
}
```

`tokenomics.blocks_per_epoch` is the **halving** epoch (12,623,040 blocks, about
four years). It is not the same as the mining epoch in `/mining/work`.

### `GET /health`, `GET /health/live`, `GET /health/ready`

```json
{"status":"healthy","product":"QSDM","version":"hardened-legacy-20261002-d7ffcd4-hl2","git_sha":"d7ffcd4e80f69c3db3f2bbcf4c7f34e08cc49d25","build_date":"2026-10-02T05:23:22Z","timestamp":1791115518}
```

`/health/live` returns `{"status":"alive",…}`. `/health/ready` returns
`{"status":"ready","checks":{"storage":"ok","wallet_service":"ok"}}`, or HTTP 503
when storage is not ready.

### `GET /versions`

```json
{"current":"v1","versions":[{"name":"v1","prefix":"/api/v1","status":"active"}]}
```

## Blocks and transactions

### `GET /mining/blocks`

Recent block headers, newest last. Query: `from`, `to` (heights), `limit`
(default 20, max 200).

```bash
curl -s "https://api.qsdm.tech/api/v1/mining/blocks?limit=3"
```

```json
{
  "tip": 755940, "from": 755938, "to": 755940,
  "headers": [
    {
      "height": 755940,
      "hash": "841ad935f29bca0a84232b875b2576583a672a5d24a172194efd895c2a84eb4c",
      "prev_hash": "112f9d90145acd60a25a4e2d8ce5094516f46f5863cffebae645366b9ff8f30d",
      "state_root": "595e6f74257d225f39155d1b4617764f77396527ec79052aea914d3db80345a6",
      "tx_root": "670cfebaab21130148ad8c414fca03b4afee957603c5dcac30c6b2532f9386eb",
      "tx_count": 1,
      "timestamp": "2026-10-04T12:05:11Z",
      "producer_id": "1cf60b16cf9aee5e8eaf67a8518a2b2ba7bc54f2dd607f05bc5a861b7a94c859"
    }
  ]
}
```

### `GET /chain/blocks`

Full blocks including transactions and the producer's block signature. Query:
`from`, `to`, `limit` (default 16, max 64). Responses can be large.

```json
{
  "tip": 755941, "from": 755941, "to": 755941,
  "blocks": [{
    "height": 755941,
    "hash": "15b625ee5f5b2d87d11633da20af83bda630a52980fceb4d40f63180f4a4754b",
    "prev_hash": "841ad935…eb4c",
    "timestamp": "2026-10-04T14:05:22.091217912+02:00",
    "transactions": [{
      "ID": "solo-heartbeat-2203456-1791115521997698505",
      "Sender": "qsdm-system-funder", "Recipient": "qsdm-system-funder",
      "Amount": 0, "Fee": 0, "Nonce": 2203456
    }],
    "state_root": "bfce3feb…916f",
    "total_fees": 0, "gas_used": 0,
    "producer_id": "1cf60b16…c859",
    "producer_auth": { "public_key": "…" }
  }]
}
```

Blocks without user activity carry a zero-value producer heartbeat transaction,
as above.

### `GET /receipts` and `GET /receipts/{tx_id}`

Receipts for recent transactions (query `from`, `to`, `limit`), or for one
transaction id. An unknown id returns a plain-text 404.

```json
{
  "tx_id": "solo-heartbeat-2203456-1791115521997698505",
  "block_height": 755941,
  "block_hash": "15b625ee5f5b2d87d11633da20af83bda630a52980fceb4d40f63180f4a4754b",
  "status": 1,
  "gas_used": 0,
  "fee": 0,
  "logs": [{ "topic": "TxApplied", "data": { "amount": 0, "recipient": "qsdm-system-funder", "sender": "qsdm-system-funder" }, "index": 0 }],
  "timestamp": "2026-10-04T12:05:22.093619088Z",
  "index_in_block": 0
}
```

Use `/receipts/{tx_id}` to follow a transfer you submitted. (`/transactions` and
`/transactions/{id}` exist in the code but need operator credentials.)

### `GET /mining/emission`

```json
{
  "chain_tip": 755941,
  "mining_cap_dust": 9000000000000000,
  "blocks_per_epoch": 12623040,
  "target_block_time_seconds": 10,
  "current_epoch": 0,
  "block_reward_dust": 356490987,
  "block_reward_cell": "3.56490987",
  "emitted_dust": 269486153203767,
  "emitted_cell": "2694861.53203767",
  "remaining_dust": 8730513846796233,
  "next_halving_height": 12623041,
  "next_halving_eta_seconds": 118671000
}
```

## Wallet

### `GET /wallet/balance?address={address}`

```json
{"address":"5d28b82565421e3666ef7a493e0d39fc89d3450009a32cbda72aa89c7cb820e7","balance":20.494531481334427,"source":"mining-ledger"}
```

`balance` is in CELL. `source` says which ledger answered (`mining-ledger` is the
chain account store).

### `GET /wallet/nonce?sender={address}`

The query parameter is **`sender`** (not `address`). `nonce` is the last nonce
already used; sign your next transfer with `next`.

```json
{"sender":"5d28b82565421e3666ef7a493e0d39fc89d3450009a32cbda72aa89c7cb820e7","nonce":1,"next":2}
```

### `GET /mining/account?address={address}`

Balance and nonce in one call:

```json
{"address":"5d28b825…20e7","balance":20.494531481334427,"nonce":1,"present":true}
```

### `POST /wallet/submit-signed`

Submits a CELL transfer that you signed yourself with ML-DSA-87. No login is
needed: the signature authorises the transfer. QSDM Hive, the
[web wallet](WEB_WALLET.md) and `qsdmcli` build this envelope for you.

Request body:

| Field | Type | Notes |
|---|---|---|
| `id` | string | Unique transaction id chosen by the client |
| `sender` | string | Must equal `hex(sha256(public_key))` |
| `recipient` | string | 64 lowercase hex characters |
| `amount` | number | CELL, greater than 0 |
| `fee` | number | CELL, 0 or more |
| `geotag` | string | Optional, may be empty |
| `parent_cells` | array of strings | Optional, may be empty |
| `nonce` | integer | Required, must be `next` from `/wallet/nonce` |
| `timestamp` | string | RFC 3339 |
| `public_key` | string | Hex ML-DSA-87 public key (not signed) |
| `signature` | string | Hex ML-DSA-87 signature |

What is signed: the JSON encoding of the envelope with `signature` set to `""`
and `public_key` removed, fields in the order of the table above (Go
`encoding/json` output). Third-party signers must reproduce it byte for byte;
see [Signed transfers](V040_WALLET_SEND_DESIGN.md) and
[Replay protection](V041_REPLAY_PROTECTION_DESIGN.md).

Responses:

| Status | Body | Meaning |
|---|---|---|
| 202 | `{"transaction_id":"…","status":"pending","broadcast":"…"}` | Queued for the next block. Follow it with `/receipts/{tx_id}`. |
| 409 | `{"transaction_id":"…","status":"duplicate",…}` | Same transaction already submitted |
| 409 | error JSON, `nonce replay` / `nonce gap` | Fetch `/wallet/nonce` again and re-sign |
| 400 | error JSON | Validation failed (for example nonce 0, bad address or hex) |
| 402 | error JSON | Balance below amount + fee |
| 422 | error JSON | Signature does not verify |
| 503 | error JSON | Transaction queue full; retry after the next block |

### Wallet recovery capsules

`GET /wallet/recovery/capsules?owner={address}`,
`GET /wallet/recovery/capsules/{locator}` and
`GET /wallet/recovery/nonce?sender={address}` read the encrypted recovery
capsules used by Hive's recovery-phrase feature
(see [Wallet recovery](WALLET_RECOVERY.md)). Registering new capsules is
paused during recovery.

## Mining

These endpoints are used by QSDM Hive and the command-line miner. Most users do
not need to call them directly; see [Mining today](MINING_TODAY.md). The proof
format is defined in [Mining protocol v2](MINING_PROTOCOL_V2.md). Base URL:
`https://api.qsdm.tech/api/v1`; every mining route is rate-limited per IP.

| Method | Path | Status today |
|---|---|---|
| GET | `/mining/work` | Live on `api.qsdm.tech` |
| GET | `/mining/challenge` | Live |
| POST | `/mining/submit` | Live on `api.qsdm.tech` for enrolled miners |
| GET | `/mining/enrollments`, `/mining/enrollment/{node_id}` | Live |
| POST | `/mining/enroll`, `/mining/unenroll` | **Paused** during recovery |

### `GET /mining/work`

The current work package. Optional query `height`. The work-set fields are
described in Mining protocol v2 and are left out below.

```json
{"epoch":12,"height":755942,"header_hash":"a1cedad2…d080","difficulty":"16777216","blocks_per_epoch":60480,"…":"…"}
```

Here `blocks_per_epoch` is the **mining** epoch (60,480 blocks), not the halving
epoch in `/status`. When mining is closed the endpoint returns
`503 {"error":"mining_unavailable",…}`.

### `GET /mining/challenge`

A fresh, signed, single-use challenge that the miner binds into its attestation.
Not cacheable (`Cache-Control: no-store`); it expires after about 60 seconds.

```json
{"nonce":"b144c302a35ee8e28f88d09203255f9de150c771552eb5f77fc8a5d89a34986b","issued_at":1791115543,"signer_id":"validator-483c814995ccf802","signature":"eab7de98d392e3a869fa06b744e77f6c9184d294a03c1dc6448ced7cfe92d093"}
```

### `POST /mining/submit`

Body: one proof as canonical JSON per Mining protocol v2, including its
`attestation` object (`type` is `nvidia-hmac-v1` or `nvidia-cc-v1`). The
`miner_addr` must be the owner of an active enrollment. Hive 1.4.21 also adds
the owner's ML-DSA-87 signature.

| Status | Body |
|---|---|
| 200 | `{"accepted":true,"proof_id":"…"}` |
| 400 | `{"accepted":false,"reject_reason":"…","detail":"…"}` |
| 429 | Rate limit |
| 503 | `{"error":"mining_unavailable",…}`: mining window closed |

### `GET /mining/enrollments` and `GET /mining/enrollment/{node_id}`

The enrollment registry. List query: `cursor`, `limit`, `phase`
(`active`, `pending_unbond`, `revoked`). Private enrollment keys are never
returned.

```json
{
  "records": [{
    "node_id": "clore-3090-01",
    "owner": "5d28b82565421e3666ef7a493e0d39fc89d3450009a32cbda72aa89c7cb820e7",
    "gpu_uuid": "GPU-0856d89a-074f-adbd-781d-1f5e7673d569",
    "stake_dust": 1000000000,
    "bond_mode": "mining_rewards",
    "required_stake_dust": 1000000000,
    "bond_remaining_dust": 0,
    "fully_bonded": true,
    "enrolled_at_height": 326831,
    "phase": "active",
    "slashable": true
  }],
  "next_cursor": "hive-2696v3-0b65af9f0525",
  "has_more": true,
  "total_matches": 55
}
```

## Tasks and streams

Read-only during recovery; signed task and stream actions are paused.

| Endpoint | Returns |
|---|---|
| `GET /tasks` | Task catalogue from the chain: `runtime`, `catalog_source`, `catalog_state_root`, `tasks[]` |
| `GET /tasks/state` | On-chain task state (stakes, participants, submissions). Can be large. |
| `GET /tasks/{task_id}` | One task (`/state` and `/submissions` sub-paths are also available) |
| `GET /tasks/actions` | Task action log (query `limit`, `task_id`, `sender`) |
| `GET /streams` | CELL Streams (query `payer`, `provider`, `status`, `service_id`) |
| `GET /streams/{stream_id}` | One stream |
| `GET /streams/nonce?sender={address}` | Next stream action nonce |

See [Task registry](QSDM_TASK_REGISTRY.md) and [CELL Streams](CELL_STREAMS.md).

## Transparency

| Endpoint | Returns |
|---|---|
| `GET /trust/attestations/summary` | How many public nodes have a fresh NVIDIA attestation, with a scope note |
| `GET /trust/attestations/recent` | Recent attestations (query `limit`) |
| `GET /audit/summary`, `GET /audit/items`, `GET /audit/badge.svg` | The project's **internal, self-maintained checklist**. It is not an independent audit. |
| `GET /referrals/reward-pool` | Referral pool state (currently `enabled: false`) |

## Not public

These exist in the source code but are not reachable for public clients during
the pilot. They are listed so nobody builds on them by mistake:

- Login and accounts (`/auth/*`): new registration is disabled during
  recovery and the API has no public login. The optional QSDM Account at
  `https://qsdm.tech/api/account/` uses wallet sign-in; see [QSDM Account](QSDM_ACCOUNT.md).
- Operator routes that need credentials: `/transactions*`, `/network/topology`,
  `/governance/*`, `/attest/recent-rejections`, `/mining/slash*`, `/tokens/*`,
  `/contracts/*`, `/bridge/*`, `/wallet/send`, `/validator/*`, `/monitoring/*`.
- Administrative routes (`/api/admin/*`) and certificate issuance are not exposed.
- No public metrics endpoint.

## Machine-readable spec

[`openapi.yaml`](openapi.yaml) is being brought in line with this page. Where
they differ, this page and the live responses are correct.
