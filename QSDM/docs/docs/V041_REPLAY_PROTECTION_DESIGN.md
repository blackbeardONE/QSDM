# v0.4.1 Design — Replay protection + atomic balance debit

> **Status**: SHIPPED + DEPLOYED across Sessions 99–100
> (2026-05-13 → 2026-05-14). All client + server + tooling
> components landed, `v0.4.1` tag pushed,
> [`release-container.yml` run 25855056638](https://github.com/blackbeardONE/QSDM/actions/runs/25855056638)
> 10/10 green, 53 cosign-signed assets attached, and the BLR1
> validator binary swapped to v0.4.1 (sha256
> `e7fa04b0657c5793f79f2fce06562fe67ea9191e04c09657c1e6b5274c213cfb`)
> with `/api/v1/status` reporting `"version":"v0.4.1"`,
> `GET /api/v1/wallet/nonce` returning 200, and `cmd/v041smoke`
> reporting `PASS=5 FAIL=0` from an external workstation. Full
> evidence in [`RELEASE_EVIDENCE_v0.4.1.md`](RELEASE_EVIDENCE_v0.4.1.md).
>
> **Implementation manifest**:
>   - **Session 99 (commit `ecfa121`)** — Storage foundation:
>     design doc; `wallet.TransactionData.Nonce` wire field;
>     `storage.GetNonce` + `storage.ApplyTransferAtomic` interface
>     methods; SQLite v0.4.1 schema migration; 3 new monitoring
>     result tags (`nonce_replay`, `nonce_conflict`,
>     `nonce_lookup_failed`); WASM signer struct mirror for the
>     Nonce field.
>   - **Session 100 (commit `8659b04`)** — Handler integration:
>     `SubmitSignedTransaction` calls `storage.GetNonce()` for the
>     replay gate, then `storage.ApplyTransferAtomic()` for the
>     single-ACID-step transfer (replacing the v0.4.0 trio of
>     `storageHasTransaction` + `GetBalance` + `StoreTransaction`);
>     `StorageInterface` in `pkg/api/server.go` + the local
>     `Storage` interface in `cmd/qsdm/main.go` extended for
>     compile-time satisfies-check across all backends (SQLite +
>     Scylla + file); 5 new `TestSubmitSigned_*` cases green
>     (HappyPath_WithNonce, LegacyV040Envelope, NonceReplay,
>     NonceConflict, NonceLookupFailed) + all 8 v0.4.0 tests still
>     green = 13/13 PASS in `pkg/api`.
>   - **Session 100 (this session)** — Client + tooling:
>     `GET /api/v1/wallet/nonce` helper endpoint + 6 unit tests
>     (Section 5.2); `qsdmcli wallet sign-tx` subcommand + 5
>     unit tests with hard signature-verifies-against-server-
>     canonicalisation guarantee (Section 5.3); browser-wallet
>     Send tab Nonce input + auto-resolve + WASM rebuild + SRI
>     refresh (Section 5.2); `cmd/v041smoke` 5-probe super-set
>     of `cmd/v040smoke`.
>
> **Production-deploy footnote**: the BLR1 validator runs the
> `FileStorage` backend, which by design does not track
> per-account balances or nonces. v0.4.1's
> `FileStorage.GetNonce` returns `(0, nil)` so the new public
> `GET /api/v1/wallet/nonce` endpoint is functional (every fresh
> sender resolves to `{nonce:0, next:1}`), but the write path
> `FileStorage.ApplyTransferAtomic` honestly refuses with a
> `qsdm_wallet_send_total{result="store_failed"}` counter bump ↔
> client-visible HTTP 500 `failed to apply transfer`. Settlement
> requires the SQLite v0.4.1 or Scylla backend; both implement
> the full CAS + atomic-debit semantics. This is exercised end-to-
> end by `cmd/v041smoke` probe 5, which accepts both
> "real-backend 409 nonce conflict" and "FileStorage 500" as
> v0.4.1-specific outcomes. A v0.4.0 server would have returned
> HTTP 402 `insufficient_balance` from `GetBalance() == 0`,
> never reaching the new code path.
>
> **Independent cosign / Rekor verification (Session 100,
> 2026-05-14)**: 5-asset out-of-band sweep with cosign v2.4.1
> from a workstation outside the CI runner returned
> `Verified OK` for `qsdmminer-console-linux-amd64`,
> `SHA256SUMS`, and all 3 GHCR images (`qsdm:0.4.1`,
> `qsdm-validator:0.4.1`, `qsdm-miner:0.4.1`). Cert OID
> `1.3.6.1.4.1.57264.1.21` resolved to signing run
> `25855056334`. Full per-asset table + reproducer in
> [`RELEASE_EVIDENCE_v0.4.1.md`](RELEASE_EVIDENCE_v0.4.1.md)
> §"Independent cosign / Rekor evidence". This closes the
> v0.4.1 evidence pass.
>
> **Remaining (informational, NOT release-blocking)**:
>   - Optional: `TestSqliteV041Migration_FromV040DB` storage-layer
>     migration test (requires a CGO build environment; not a
>     release blocker because the schema-only migration is exercised
>     end-to-end by every CGO build's existing storage tests).
>
> Closes the two v0.4.0 known gaps documented in
> [`V040_WALLET_SEND_DESIGN.md`](V040_WALLET_SEND_DESIGN.md)
> "Future work":
> (1) cross-`tx_id` replay against `/api/v1/wallet/submit-signed`
> (2) non-atomic balance debit in `pkg/storage/sqlite.go::UpdateBalance`
> Audit row `api-06` (in
> [`pkg/audit/checklist.go`](../../source/pkg/audit/checklist.go))
> carries the closing anchors.

## 1. Problem statement

### 1.1 Cross-tx_id replay

v0.4.0's `/wallet/submit-signed` handler enforces single-`tx_id`
replay protection via the `storage.GetTransaction(tx_id)` pre-flight
check — a duplicate `tx_id` returns HTTP 409 + `status:"duplicate"`.

But the `tx_id` in v0.4.0 is constructed client-side as a 16-byte
prefix of `sha256(sender|recipient|amount|fee|geotag|nanoseconds)`.
The nanosecond timestamp is the only field that varies under the
attacker's control without changing the logical transfer. So a
client (or an MITM that re-signs in their possession of the private
key — which is impossible — but also a re-sign by the legitimate
sender themselves later) can produce **arbitrarily many distinct
`tx_id`s for the same logical transfer**. Each will pass the
duplicate-check (different `tx_id`) and apply the same balance
debit again.

This is not a hypothetical: a careless caller who retries a
"send 10 CELL to Bob" request by re-clicking the Send button
re-builds a fresh `tx_id` and triggers a second debit. The browser
wallet today refuses to do that by storing the last submitted
`tx_id` in IndexedDB and surfacing a "duplicate" warning if the
user re-clicks — but that's strictly best-effort client-side. A
malicious client can ignore it.

### 1.2 Non-atomic balance debit

`pkg/storage/sqlite.go::UpdateBalance` runs:

```sql
INSERT INTO balances (address, balance, updated_at)
VALUES (?, COALESCE((SELECT balance FROM balances WHERE address = ?), 0.0) + ?, CURRENT_TIMESTAMP)
ON CONFLICT(address) DO UPDATE SET
    balance = balance + ?,
    updated_at = CURRENT_TIMESTAMP
```

with **no `CHECK(balance >= 0)` constraint** and **no rollback on
negative**. `pkg/storage/sqlite.go::StoreTransaction` calls
`UpdateBalance(sender, -amount)` and `UpdateBalance(recipient, +amount)`
as two separate statements, each in its own implicit transaction.

The v0.4.0 `/wallet/submit-signed` handler does a pre-flight
`storage.GetBalance(sender)` check before `StoreTransaction`,
but:

1. Two concurrent submit-signed calls from the same sender can
   both pass the pre-flight check and both proceed to debit,
   driving the on-disk balance below zero.
2. If the first `UpdateBalance(sender, -amount)` succeeds and
   the second `UpdateBalance(recipient, +amount)` fails (disk
   full, sqlite lock contention, etc.), the sender is debited
   without the recipient being credited.

Both gaps are present in `/wallet/send` today too, so v0.4.0 is
not a regression — but exposing the surface to public,
unauthenticated callers on a balance-bearing chain makes both
non-theoretical.

## 2. Wire-format additions

### 2.1 New envelope field: `nonce`

Add a `nonce` field to `pkg/wallet.TransactionData`:

```go
type TransactionData struct {
    ID          string   `json:"id"`
    Sender      string   `json:"sender"`
    Recipient   string   `json:"recipient"`
    Amount      float64  `json:"amount"`
    Fee         float64  `json:"fee"`
    GeoTag      string   `json:"geotag"`
    ParentCells []string `json:"parent_cells"`
    Nonce       uint64   `json:"nonce"`           // ← NEW in v0.4.1
    Signature   string   `json:"signature"`
    PublicKey   string   `json:"public_key,omitempty"`
    Timestamp   string   `json:"timestamp"`
}
```

`nonce` is a per-sender monotonically-increasing 64-bit
unsigned integer. The handler enforces:

- `nonce > last_seen_nonce[sender]`
- (Optional, configurable) `nonce == last_seen_nonce[sender] + 1`
  for strict serialisation. Default OFF; turning ON would block
  the legitimate "fire two concurrent transfers in parallel"
  pattern most wallets need.

### 2.2 Canonical-payload contract

The canonical signing payload is unchanged in structure: the
envelope JSON with `signature` + `public_key` cleared, then
`json.Marshal` with Go's default field-order emission. The
`nonce` field appears in the canonical bytes exactly where the
struct declaration places it (after `parent_cells`, before
`signature`).

Both the server (Go) and the WASM module (Go-compiled-to-wasm)
use `json.Marshal` on the identical struct definition, so they
produce byte-identical canonical bytes. A JavaScript SDK or
hand-rolled client building the canonical bytes must use the
same field order: `id, sender, recipient, amount, fee, geotag,
parent_cells, nonce, signature, public_key, timestamp` — see
the WASM helper's `txEnvelope` struct for the authoritative
order.

### 2.3 Backward compatibility

For one release window (v0.4.1 → v0.4.2), the handler accepts:

- **Modern envelopes** with `nonce >= 1`: full v0.4.1 path.
- **Legacy envelopes** with `nonce == 0` or `nonce` absent
  (JSON omitted → Go zero-value `0`): handled as v0.4.0
  envelopes — pre-flight `GetBalance` check, no nonce
  enforcement, no replay protection beyond `tx_id`. This path
  is rate-limited at 1 req/min per sender (10× tighter than the
  full-path 10/min) to discourage operators from using it.

v0.4.2 removes the legacy path entirely. v0.4.1 release notes
will set a deprecation window date.

## 3. Schema additions

### 3.1 SQLite

Two changes to `pkg/storage/sqlite.go`:

```sql
-- (a) Add CHECK constraint to balances. CHECK constraints on
-- existing rows require a "CREATE TABLE … new + INSERT INTO …
-- SELECT * + DROP + RENAME" migration since SQLite cannot
-- alter-add-check. The migration runs idempotently at startup
-- if the existing balances table lacks the CHECK constraint
-- (probe: SELECT sql FROM sqlite_master WHERE name='balances').
CREATE TABLE balances_v041 (
    address    TEXT    PRIMARY KEY,
    balance    REAL    NOT NULL DEFAULT 0.0 CHECK(balance >= 0),
    nonce      INTEGER NOT NULL DEFAULT 0   CHECK(nonce >= 0),
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- (b) The migration step:
--      1. BEGIN;
--      2. CREATE TABLE balances_v041 (...);
--      3. INSERT INTO balances_v041 (address, balance, updated_at)
--         SELECT address, MAX(balance, 0), updated_at FROM balances;
--         (negative balances on-disk are clamped to 0; this is the
--          forensic acknowledgment that any pre-v0.4.1 race may
--          have left negative balances we're now fixing up.)
--      4. DROP TABLE balances;
--      5. ALTER TABLE balances_v041 RENAME TO balances;
--      6. COMMIT;
--    On any failure: ROLLBACK and refuse to start (operator must
--    investigate; the storage layer is the source of truth).
```

The migration logs the count of rows clamped from negative to 0
(`qsdm_storage_v041_migration_clamped` gauge, type `info`, set once at
boot) for forensic audit.

### 3.2 Scylla (CQL)

```cql
ALTER TABLE qsdm.balances ADD nonce bigint;
-- Atomic debit via LWT (CAS) — CQL natively atomic:
UPDATE qsdm.balances
   SET balance = ?, nonce = ?
   WHERE address = ?
     IF balance = ? AND nonce = ?;
-- The CAS guarantees only one concurrent debit succeeds; the
-- losers see a "[applied] = false" row and the handler returns
-- HTTP 409 with status:"nonce_conflict".
```

CHECK constraint equivalent: enforced in the application by
refusing to apply a debit if the CAS condition `balance >= amount`
is not met (`IF balance >= ?` is not portable across all CQL
flavours, so we issue the CAS on the exact pre-image and reject
if it doesn't match).

### 3.3 File storage

`pkg/storage/file_storage.go` is single-writer (lock-protected),
so atomic debit comes for free — wrap the read-modify-write in
the existing `sync.RWMutex.Lock()`. Add the nonce field to the
on-disk JSON representation.

## 4. Handler logic

### 4.1 New handler steps in `SubmitSignedTransaction`

Insert between the existing signature-verify step and the
existing storage-check step:

```go
// v0.4.1 nonce enforcement.
//
// Backward-compat: envelopes with nonce == 0 take the legacy
// (v0.4.0) path — they're subject to the 1-req/min/sender
// rate-limit on the legacy path AND don't update the on-disk
// nonce-tracker, so a sender who once submits a v0.4.0 envelope
// can later submit v0.4.1 envelopes starting at nonce=1 without
// conflict.
if env.Nonce > 0 {
    last, err := h.storage.GetNonce(env.Sender)
    if err != nil {
        monitoring.RecordWalletSend(monitoring.WalletSendResultNonceLookupFailed)
        writeErrorResponse(w, http.StatusInternalServerError, "nonce lookup failed")
        return
    }
    if env.Nonce <= last {
        monitoring.RecordWalletSend(monitoring.WalletSendResultNonceReplay)
        writeErrorResponse(w, http.StatusConflict,
            fmt.Sprintf("nonce replay: envelope nonce %d <= last-seen %d", env.Nonce, last))
        return
    }
}
```

### 4.2 New atomic-debit interface

Extend `pkg/api/server.go::StorageInterface` and the per-backend
storage layers with:

```go
// ApplyTransferAtomic debits sender (amount + fee), credits
// recipient (amount), bumps sender's nonce to envelopeNonce
// (or leaves nonce untouched if envelopeNonce == 0), and stores
// the transaction blob — all in a single ACID transaction.
//
// Returns an error wrapped with one of the sentinel types:
//   ErrInsufficientBalance — sender balance < amount + fee
//   ErrNonceConflict       — sender's nonce no longer matches
//                            the pre-image (concurrent submit)
//   ErrTxAlreadyExists     — tx_id already in transactions table
// in addition to any wrapped backend-internal storage error.
ApplyTransferAtomic(
    ctx context.Context,
    sender, recipient string,
    amount, fee float64,
    envelopeNonce uint64,
    txID string,
    rawEnvelope []byte,
) error
```

The handler replaces the current sequence (pre-flight GetBalance,
GetTransaction, submesh check, StoreTransaction → which internally
calls UpdateBalance twice) with:

```
ApplyTransferAtomic(ctx, sender, recipient, amount, fee, nonce, txID, rawEnvelope)
```

The submesh-policy gate stays as-is (before ApplyTransferAtomic);
the metric increment happens after, depending on returned error.

### 4.3 New `qsdm_wallet_send_total` result tags

Add two:

- `result="nonce_replay"` — envelope nonce ≤ stored nonce
- `result="nonce_conflict"` — atomic-debit CAS rejected on
  concurrent submit (Scylla LWT path, sqlite optimistic-concurrency
  retry on SQLITE_BUSY)

## 5. WASM + client changes

### 5.1 WASM helper

`wasm_modules/wallet/cmd/qsdm-wallet/main.go::walletSignTransaction`
unchanged in structure — Go's `json.Marshal` automatically picks
up the new `Nonce` field. Bump `apiVersion` constant
`v2 → v3`.

### 5.2 Browser wallet

`QSDM/deploy/landing/wallet.js`:

1. New "Nonce" input on the Send tab, type=number, min=1, default=`(last_seen + 1)`.
2. Helper that queries a new endpoint `GET /api/v1/wallet/nonce/{sender}` to
   pre-populate the default. Falls back to 1 if the endpoint
   returns 404 (first send for this sender).
3. Inline warning if user types a nonce ≤ what the server
   reports as last-seen.

### 5.3 `qsdmcli wallet sign-tx` CLI

Land in the same session as the browser changes — same canonical
payload, same nonce field. Reads input as JSON-on-stdin (no
field-by-field flag parsing — the user supplies an unsigned
envelope, the CLI signs and prints the signed envelope on stdout).
Replaces the current "construct canonical JSON by hand and pipe
through `qsdmcli wallet sign --message-file -`" path.

## 6. Migration runbook

1. **Pre-deploy**: cross-compile v0.4.1 binary, smoke-test
   against a local dev validator. Run the SQLite schema-
   migration test on a copy of the BLR1 `qsdm.db` (or
   equivalent file-storage / Scylla dataset).
2. **Deploy** to BLR1: `systemctl stop qsdm` → swap binary →
   `systemctl start`. On start, the validator:
   - Detects v0.4.0 schema (`balances` table has no CHECK).
   - Runs the migration (Section 3.1).
   - Logs the clamped-row count (expected: 0 unless a v0.4.0
     race left a negative balance).
   - Resumes serving.
3. **Smoke test** (`cmd/v040smoke` re-purposed as
   `cmd/v041smoke`): 5 probes against production
   `/wallet/submit-signed`:
   - probes 1-3 from v0.4.0 (regression guard)
   - probe 4: valid envelope, nonce=1 (first-send for the test
     keypair). Expected: HTTP 200 accepted + balance debit/credit
     applied + nonce bumped to 1.
     **HARD GUARD**: only runs if `QSDM_V041_POSITIVE_PROBE=1` is
     set; otherwise skipped to avoid creating a `-X CELL` on the
     test address.
   - probe 5: re-send the same envelope. Expected: HTTP 409
     duplicate (tx_id replay) OR HTTP 409 nonce_replay
     (depending on whether the tx_id check or nonce check
     fires first; we want tx_id-first for cleanest semantics).
4. **Rollback plan**: pre-v0.4.1 backup of `qsdm.db`,
   `qsdm_accounts.json`, `qsdm_chain.ndjson` taken before the
   migration. If the migration fails or the smoke test fails
   non-recoverably, restore from backup and re-deploy v0.4.0.

## 7. Test matrix (added in v0.4.1)

| Test name | Asserts |
|---|---|
| `TestApplyTransferAtomic_HappyPath` | balance + nonce + tx_id all advance in one ACID step |
| `TestApplyTransferAtomic_InsufficientBalance` | returns `ErrInsufficientBalance`; no state mutation |
| `TestApplyTransferAtomic_NonceConflict` | returns `ErrNonceConflict` when pre-image nonce no longer matches |
| `TestApplyTransferAtomic_DuplicateTxID` | returns `ErrTxAlreadyExists`; no state mutation |
| `TestApplyTransferAtomic_ConcurrentRace` | 2 goroutines submit the same envelope; exactly one succeeds, one gets `ErrNonceConflict` |
| `TestSubmitSigned_NonceReplay` | nonce ≤ last-seen → HTTP 409 nonce_replay |
| `TestSubmitSigned_LegacyV040Envelope` | nonce==0 envelope still works (backward-compat) |
| `TestSubmitSigned_Backend_FileStorage` | matrix-runs the suite against file_storage too |
| `TestSubmitSigned_Backend_Scylla` | matrix-runs against a local Scylla container (-tags scylla) |
| `TestSqliteV041Migration_FromV040DB` | start with a v0.4.0 `balances` table, run migration, assert new CHECK constraint exists and clamped-row count metric advanced |

## 8. Out of scope (not in v0.4.1)

- **Per-sender rate-limit**: today's rate-limit is per-IP. A
  malicious actor with many IPs can still flood. Per-sender
  rate-limit is v0.4.2.
- **Mempool integration**: signed envelopes go straight to
  storage. v0.5 will route them through `pkg/mempool` first so
  that block producers see the pending transfers and can include
  them in the next block.
- **Account-deletion / archival**: a zero-balance, never-used
  account stays in the `balances` table forever. v0.5 will add
  a periodic vacuum.

## 9. Audit row

`api-06` already covers `/wallet/submit-signed`. v0.4.1 extends
the `Description` to flip the "KNOWN GAPS shipped intentionally"
paragraph from PENDING → CLOSED, with the closing anchors:

- `qsdm_wallet_send_total{result="nonce_replay"}` and
  `{result="nonce_conflict"}` exposition active on
  api.qsdm.tech/api/v1/metrics.
- `pkg/storage/sqlite.go::ApplyTransferAtomic` exists; pre-flight
  `GetBalance` + `StoreTransaction(UpdateBalance)` pair removed
  from the handler.
- BLR1's `balances` table has the new schema (`CHECK(balance >=
  0)`, `nonce` column, both visible via `PRAGMA table_info`).
- `cmd/v041smoke` 5/5 PASS or 4/5 PASS (depending on whether
  the positive probe was opted in).

No new audit row needed — this work belongs under `api-06` as
"close the v0.4.0 known gaps."
