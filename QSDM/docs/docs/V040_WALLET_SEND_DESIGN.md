# v0.4 — Browser-Wallet "Send transaction" Design

> Status: **Phase A backend SHIPPED in v0.4.0** (session 95) ·
> **Phase B (WASM + browser UI) deferred to next session.**
> Tracking issue: `browser-wallet-send` (was: "deferred to v0.4" in
> sessions 91–93 backlog).
> Scope: server endpoint + WASM signing helper + browser UI for
> end-user-signed transactions. Wallet generation, keystore I/O,
> arbitrary-message signing, and balance read-only views already
> shipped in v0.3.x.
>
> **Session 95 (Phase A) delivered:**
> - `POST /api/v1/wallet/submit-signed` handler in
>   `pkg/api/handlers.go::SubmitSignedTransaction` — 100 % of the
>   server-side semantics described below: sender-binding,
>   signature verification, balance check, idempotency, submesh
>   policy, p2p broadcast.
> - `StorageInterface.GetTransaction(txID)` lifted onto the API
>   storage contract for O(1) idempotency lookup.
> - Four new monitoring result tags on
>   `qsdm_wallet_send_total`: `sender_mismatch`,
>   `signature_invalid`, `insufficient_balance`, `duplicate`.
> - Public-path + per-IP rate-limit (10/min) wiring in
>   `pkg/api/middleware.go` and `pkg/api/security.go`.
> - 8-case test matrix in `pkg/api/handlers_test.go`
>   (`TestSubmitSigned_*`).
> - Audit row `api-06` flipped from "design" to "backend
>   implemented" with the known-gap list inline.
>
> **Session 95 (Phase A) deferred to next session (Phase B):**
> - WASM `wallet.wasm` helper for client-side
>   ML-DSA-87 canonical-payload signing of a
>   `wallet.TransactionData` envelope.
> - Browser-wallet "Send" tab (5th tab) in
>   `QSDM/deploy/landing/wallet.html` + glue JS in `wallet.js`.
> - SRI re-hash of `wallet.js` + `wallet.wasm` after rebuild.
> - OpenAPI `paths./wallet/submit-signed` entry in
>   `QSDM/docs/docs/openapi.yaml`.
> - `MINER_QUICKSTART.md` Appendix B refresh to call out the new
>   self-custody path alongside the existing validator-signed
>   `/wallet/send`.

## TL;DR for impatient reviewers

| What you'll get in v0.4 | What you'll explicitly NOT get in v0.4 |
| --- | --- |
| `POST /api/v1/wallet/submit-signed` server endpoint (new). | A per-account nonce / replay-protected mempool. |
| Server-side ML-DSA-87 signature verification bound to `sender = hex(sha256(public_key))`. | Atomic "debit only if balance ≥ amount" enforcement at storage. |
| Browser "Send" tab in `wallet.html` (5th tab after Generate / Open / Sign / Balance). | A general-purpose mempool with priority / RBF / TTL semantics. |
| WASM helper `qsdm-wallet sign-transaction(envelope_json, pass, keystore_json)`. | Multi-party / multisig flows. |
| OpenAPI spec + miner-quickstart docs. | Cross-chain / wrapped-asset / token-swap surfaces. |
| `api-06` audit row in `pkg/audit/checklist.go`. | Browser-wallet importing hardware-wallet keys (Ledger, etc.). |

Two of the four "NOT" items (replay protection + atomic debit) are
real security gaps that v0.4 inherits from the existing v0.3.3
`/wallet/send` path. v0.4 **does not regress** them — it inherits
the same posture — but **fixing them is a v0.5 prerequisite**
before opening this endpoint to public/incentivised-testnet
operators. See `Future work` at the bottom.

## Why this needs a new endpoint (and isn't just "wire the UI to `/wallet/send`")

Reading `pkg/api/handlers.go::SendTransaction` and
`pkg/wallet/wallet.go::CreateTransaction` together makes the
mismatch obvious:

- `SendTransaction` requires a JWT (`r.Context().Value("claims")`),
  but the very next line is `_ = claims` — the authenticated
  subject is **discarded**. The server then calls
  `walletService.CreateTransaction(...)` which **always uses the
  validator's own ML-DSA-87 keypair as `Sender`**. The function
  signature takes no `sender` argument.
- `WalletService` is a process-singleton with a single hard-coded
  keypair and a starting `balance: 1000` (the "demo" balance in
  the constructor). Its `CreateTransaction` builds, signs, and
  returns a fully-formed `TransactionData{Sender: ws.address,
  ...}` envelope. The signer is the node operator, not the JWT
  subject.
- `Storage.StoreTransaction(envelope_bytes)` parses the JSON and
  calls `UpdateBalance(sender, -amount)` + `UpdateBalance(recipient,
  +amount)`. It does **not** verify the signature or check that
  the JWT subject equals the envelope's `Sender`.

So the v0.3.3 `/wallet/send` semantics are:

> "Any authenticated client can ask the **validator's own wallet**
> to send N CELL to a recipient. The validator signs with its own
> key; the JWT identity is metadata only."

That is genuinely fine for a single-operator node where the
operator IS the validator. It is not what a self-custody browser
wallet wants — the browser wallet's whole point is that the user's
private key never leaves the browser, so the server cannot sign
on their behalf.

For self-custody we need a path where:

1. The browser builds a transaction envelope.
2. The browser signs it inside the WASM module using the
   user-held ML-DSA-87 private key.
3. The browser POSTs the **already-signed** envelope to a
   validator.
4. The validator verifies the signature, verifies
   `sender = hex(sha256(public_key))`, applies the balance change
   if checks pass, and broadcasts via P2P.

The existing endpoint can't do step 4 because its handler builds
the envelope server-side — it never accepts a client-built one. A
new endpoint is the cleanest fix; trying to bolt
"if-Body-has-Signature-skip-CreateTransaction" onto the existing
handler would produce two confusing posture modes for the same
URL.

## Server-side surface

### Route

```
POST /api/v1/wallet/submit-signed
Content-Type: application/json
Authentication: optional (does NOT bind submission to the JWT
  subject because the cryptographic sender identity IS the envelope's
  public_key field. JWT, if present, is used only for rate-limiting
  bucketing / abuse heuristics — same way `/wallet/balance?address=`
  does today).
```

### Request body — exact wire-format

```json
{
  "id":           "<hex16 — first 16 bytes of sha256(sender||recipient||timestamp_ns)>",
  "sender":       "<hex64 — hex(sha256(public_key))>",
  "recipient":    "<hex64 — hex(sha256(recipient_public_key))>",
  "amount":       10.0,
  "fee":          0.01,
  "geotag":       "US",
  "parent_cells": ["<parent_tx_id_1>", "<parent_tx_id_2>"],
  "timestamp":    "2026-05-13T12:34:56Z",
  "public_key":   "<hex5184 — packed FIPS 204 ML-DSA-87 public key, 2592 bytes hex>",
  "signature":    "<hex9254 — ML-DSA-87 signature over the canonical-payload below, 4627 bytes raw → 9254 hex chars; size sourced from cloudflare/circl mldsa87.SignatureSize and confirmed against pkg/crypto/dilithium_circl.go line 127>"
}
```

**The canonical payload that gets signed is the same JSON object
with `signature` and `public_key` removed**, then `json.Marshal`-ed
with Go's default field ordering. This mirrors
`pkg/wallet/wallet.go::CreateTransaction` lines 107–119 where the
existing CLI / validator-wallet build the signing payload by
marshalling `TransactionData` _before_ setting `Signature` and
`PublicKey`. A WASM-built envelope can produce a byte-for-byte
matching canonical payload because the field order is fixed by the
struct definition.

### Response body

Success (HTTP 200):

```json
{
  "transaction_id": "<echoes envelope.id>",
  "status": "accepted",
  "broadcast": "p2p|local-only"
}
```

Errors (with the existing `writeErrorResponse` shape):

| HTTP | When |
| --- | --- |
| 400 | malformed JSON; field length / range / charset invalid; `sender != hex(sha256(public_key))`. |
| 401 | JWT required by rate-limit middleware but absent (only fires if the deployment configures it). |
| 402 (proposed) | `storage.GetBalance(sender) < amount + fee`. |
| 403 | NVIDIA-lock gate active and the call comes from a non-locked context (matches existing `/wallet/send` posture). |
| 409 | `tx_id` already present in storage (idempotent retry — return the stored tx id without applying again). |
| 422 | ML-DSA-87 signature verification fails. |
| 500 | storage / broadcast layer fault. |
| 503 | wallet service uninitialised (e.g. validator boot races). |

### Handler outline (pseudocode)

```go
func (h *Handlers) SubmitSignedTransaction(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
        return
    }

    var env wallet.TransactionData // existing type from pkg/wallet
    if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
        monitoring.RecordWalletSend(monitoring.WalletSendResultInvalidRequest)
        writeErrorResponse(w, http.StatusBadRequest, "invalid envelope")
        return
    }

    // 1. Shape validation (reuse existing ValidateAddress / ValidateAmount).
    if err := ValidateAddress(env.Sender);    err != nil { ... 400 }
    if err := ValidateAddress(env.Recipient); err != nil { ... 400 }
    if err := ValidateAmount(env.Amount);     err != nil { ... 400 }
    if env.Fee < 0 || ValidateAmount(env.Fee) != nil       { ... 400 }
    if err := ValidateGeoTag(env.GeoTag);     err != nil { ... 400 }
    if err := ValidateParentCells(env.ParentCells); err != nil { ... 400 }

    // 2. Cryptographic bind: sender == hex(sha256(pubkey))?
    pubBytes, err := hex.DecodeString(env.PublicKey)
    if err != nil || len(pubBytes) != mldsa87PublicKeyBytes {
        writeErrorResponse(w, http.StatusBadRequest, "malformed public_key")
        return
    }
    derivedAddr := hex.EncodeToString(sha256.Sum256(pubBytes)[:])
    if derivedAddr != env.Sender {
        writeErrorResponse(w, http.StatusBadRequest,
            "sender does not match hex(sha256(public_key))")
        return
    }

    // 3. Verify the signature over the canonical payload.
    sigBytes, err := hex.DecodeString(env.Signature)
    if err != nil || len(sigBytes) != mldsa87SignatureBytes {
        writeErrorResponse(w, http.StatusBadRequest, "malformed signature")
        return
    }
    canonical, _ := json.Marshal(stripSigAndPubkey(env))
    ok, err := h.walletService.VerifySignature(canonical, sigBytes, pubBytes)
    if err != nil || !ok {
        monitoring.RecordWalletSend(monitoring.WalletSendResultUnauthenticated)
        writeErrorResponse(w, http.StatusUnprocessableEntity, "signature invalid")
        return
    }

    // 4. Balance check (advisory in v0.4-MVP; see Future work).
    bal, err := h.storage.GetBalance(env.Sender)
    if err == nil && bal < env.Amount+env.Fee {
        writeErrorResponse(w, http.StatusPaymentRequired,
            fmt.Sprintf("insufficient balance: have %v, need %v", bal, env.Amount+env.Fee))
        return
    }

    // 5. Idempotency on tx_id (existing storage path already
    //    short-circuits on duplicate; we just surface the right
    //    HTTP code).
    if exists, _ := h.storage.HasTransaction(env.ID); exists {
        writeJSONResponse(w, http.StatusConflict, map[string]string{
            "transaction_id": env.ID,
            "status":         "duplicate",
        })
        return
    }

    // 6. Apply + broadcast (reuses the path that /wallet/send already takes).
    raw, _ := json.Marshal(env)
    if !h.enforceSubmeshWalletSend(w, env.Fee, env.GeoTag, raw) { return }
    if err := h.storage.StoreTransaction(raw); err != nil {
        monitoring.RecordWalletSend(monitoring.WalletSendResultStoreFailed)
        writeErrorResponse(w, http.StatusInternalServerError, "store failed")
        return
    }
    monitoring.RecordWalletSend(monitoring.WalletSendResultSuccess)
    if h.p2pTxBroadcast != nil { _ = h.p2pTxBroadcast(raw) }
    writeJSONResponse(w, http.StatusOK, map[string]string{
        "transaction_id": env.ID,
        "status":         "accepted",
    })
}
```

The handler reuses the existing `WalletSendResult*` monitoring
labels; we don't need a new metric family. Public exposure is
recorded under `qsdm_wallet_send_total{result=...}` exactly like
`/wallet/send`.

### Wiring

`mux.HandleFunc("/api/v1/wallet/submit-signed", handlers.SubmitSignedTransaction)`
goes next to the other wallet routes at `handlers.go:209-213`. The
rate-limit table at `security.go:204` gets a new row:
`"/api/v1/wallet/submit-signed": 10  // 10 envelopes / minute / IP`.

## Browser-side surface

### WASM helper

The current `wallet.wasm` exports `walletGenerate`, `walletOpen`,
`walletSign(message, passphrase, keystoreJSON)`. We add **one**
export:

```
walletSignTransaction(envelopeJSON, passphrase, keystoreJSON) →
    { ok: true, envelope: <signedEnvelopeJSON>, address: <hex64> } |
    { ok: false, error: "<message>" }
```

Internally it:

1. Parses `envelopeJSON` into a `TransactionData` struct (with
   `Signature` and `PublicKey` empty).
2. Decrypts the keystore with `passphrase` to recover the private
   key.
3. Marshals the struct (still without sig/pubkey) → canonical
   bytes.
4. Signs with `ML-DSA-87.Sign(canonical, privKey)`.
5. Sets `envelope.PublicKey = hex(pubKey)`, `envelope.Signature =
   hex(sigBytes)`.
6. Returns the re-marshalled signed envelope.

This is ~30 lines of Go in `wasm_modules/wallet/cmd/qsdm-wallet/
main.go`, sharing the keystore-decrypt path that
`walletSign(message, ...)` already uses. The browser doesn't have
to construct the canonical payload — the WASM module does, which
keeps the signing-payload definition co-located with the validator
side.

### UI tab

A new `<button class="tab" data-tab="send">` and `<div
class="tab-pane" data-pane="send">` in `wallet.html`, inserted
between the existing `sign` and `balance` panes. Form fields:

| Field | Validation |
| --- | --- |
| Keystore file (`<input type="file">`) | shape-check on decrypt |
| Passphrase (`<input type="password">`) | required |
| Recipient address (`<input type="text">`) | 64 lowercase hex chars, `[0-9a-f]{64}`, regex check before WASM call |
| Amount (`<input type="number">`) | `> 0`, ≤ 6 decimal places, ≤ 10⁸ (sanity cap matching `min_enroll_stake_dust` units) |
| Fee (`<input type="number">`) | `≥ 0`, ≤ 1.0 CELL default cap (warn ≥ 0.5) |
| Geotag (`<input type="text">`) | ISO 3166-1 alpha-2 (defaults to wallet's last seen geotag, else `XX`) |
| Validator URL (`<input type="text">`, advanced) | defaults to `https://api.qsdm.tech`. Hidden behind a "Submit to a different validator" disclosure. |

On submit, the JS:

1. Reads `parent_cells` from the most recent
   `GET /api/v1/chain/tips?n=2` call (cached in tab memory), falling
   back to `["genesis", "genesis"]` if the API is unreachable. The
   handler reuses the existing `ValidateParentCells` which already
   accepts `["genesis", "genesis"]`.
2. Builds the unsigned envelope JSON.
3. Awaits `walletSignTransaction(env, pass, ksJSON)`.
4. `POST /api/v1/wallet/submit-signed` with the signed envelope.
5. Renders the response in the existing `.result` div pattern
   (transaction_id, status, broadcast link to `chain.html#<tx_id>`
   for the explorer view).

The "use my last address as sender" affordance comes free because
the browser already remembers the most recently
generated/opened wallet address (the `Use my last address` button
on the Balance tab uses the same state).

### CSP / SRI

No new third-party deps. The new code path is pure Go-WASM call +
fetch to the same origin (`api.qsdm.tech` is already in the
balance-tab's CSP allow-list). The SRI for `wallet.js` will rotate
on the build script's next run (`QSDM/scripts/build_wallet_wasm.sh`
already wires that automatically). Manual SRI bump on `wallet.html`
mirrors the session 92 procedure.

## Audit row

Add to `pkg/audit/checklist.go`:

```
{ID: "api-06", Category: CatAPI, Severity: SevHigh,
 Title: "Self-custody signed-transaction submission (browser
 wallet → POST /wallet/submit-signed)",
 Description: "Verify the handler (a) decodes envelope and
 enforces sender == hex(sha256(public_key)), (b) verifies
 ML-DSA-87 signature over the canonical payload using the
 envelope's public_key (never falling back to a validator-side
 key), (c) consults storage.GetBalance(sender) before applying
 the debit, (d) is idempotent on tx_id, (e) bumps
 qsdm_wallet_send_total{result=success|signature_invalid|
 insufficient_balance|duplicate|store_failed|...} for every
 path. Known gaps (tracked separately): no per-account nonce
 (replay possible across distinct tx_ids); UpdateBalance does
 not fail atomically on insufficient funds (warns and proceeds).
 Both must be closed before incentivised testnet (mining-05) or
 mainnet exposure."},
```

Severity is `SevHigh` because a regression on (a) or (b) lets a
caller spend an arbitrary address's balance.

## Phased rollout

| Phase | Trigger | Posture |
| --- | --- | --- |
| **v0.4.0** | This design lands + handler + WASM helper + UI + tests. | Endpoint exists. NVIDIA-lock gate ON by default (matches `/wallet/send`). Balance check enforced. Replay attack possible across distinct tx_ids → documented operator-must-be-aware risk. |
| **v0.4.1** | Per-account nonce field added to envelope + storage column. | Replay closed. Atomic "balance ≥ amount+fee" enforcement at storage layer. |
| **v0.4.2** | Stress test + slasher coverage. | Public testnet readiness. Pre-requisite for `mining-05` (incentivised testnet). |

`v0.4.0` is safe to ship to **operator-controlled** validators
(node operator runs both the validator and the browser wallet,
trusts their own setup). It is **not** safe to expose at
`api.qsdm.tech` until v0.4.1 closes the replay gap — and that's
fine, because `api.qsdm.tech` is currently a single-operator
validator anyway.

## Future work / known gaps

1. **Per-account nonce.** Add `Nonce uint64` to `TransactionData`,
   require it to be strictly monotonic per `Sender`, and reject
   replays with `409`. Storage column + migration script. Closes
   the replay window between distinct `tx_id`s.
2. **Atomic debit.** `pkg/storage/sqlite.go::UpdateBalance` today
   logs a warning and proceeds on negative balance (line ~155).
   Change to fail with `ErrInsufficientFunds` and wrap the
   transaction-apply step in a single SQL transaction so the
   debit/credit pair rolls back atomically.
3. **Mempool semantics.** Currently `StoreTransaction` writes
   directly to the canonical table — there is no "pending vs
   confirmed" notion. v0.4.x is fine without this (every tx is
   immediately confirmed); a real mempool is a v0.5+ topic that
   also unblocks fee-priority replacement.
4. **Network-agnostic submission.** The browser sends to whatever
   validator URL the user types in. We should accept envelopes
   from any peer-relayed-broadcast path too (validator A receives,
   validates, gossips to validators B/C). The `p2pTxBroadcast`
   hook already does this; nothing extra needed at v0.4.0.

## What this design does **not** change

- `pkg/wallet/wallet.go::WalletService` stays the singleton it is
  today (validator-owned wallet). The new endpoint never reaches
  into that wallet's private key — it only uses
  `walletService.VerifySignature(...)` which takes the public key
  externally.
- `/api/v1/wallet/send` keeps its current "send from validator's
  own wallet" semantics. We don't co-opt or deprecate it; it
  remains useful for node-operator self-service flows (Appendix B
  peer transfer). The audit row stays as-is.
- `pkg/storage/sqlite.go::StoreTransaction` keeps its current
  signature. The new handler just gives it a verified envelope.

## Approvers / open questions for human review

1. **Do we want the new endpoint to default-allow unauthenticated
   submission?** I've argued yes (the cryptographic identity IS in
   the envelope; JWT adds nothing for signed-envelope submission).
   But: a JWT-required posture lets the operator rate-limit by
   account, not just by IP. **Default proposal: unauthenticated
   allowed; per-IP rate-limit at 10/min; JWT bumps to 30/min.**
2. **402 Payment Required for insufficient balance** — is the HTTP
   code OK? Some validators won't have a balance ledger
   (file-storage build, see `file_storage.go::UpdateBalance` is a
   no-op); for those the check is skipped. Document this in the
   OpenAPI spec.
3. **Should we ship v0.4.0 with the replay gap, or hold for
   v0.4.1?** Pro of shipping: gives the browser-wallet's Send tab
   a working backend for self-test today. Con: a malicious caller
   on a validator they don't run can drain a sender's balance with
   a replay. **Default proposal: ship v0.4.0 with a big warning in
   the UI and the OpenAPI doc; tag v0.4.1 a week later with the
   nonce closure**.
