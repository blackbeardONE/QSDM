# Release Evidence — v0.4.1

> Independent supply-chain verification of the v0.4.1 release line.
> v0.4.1 closes the two v0.4.0 known gaps documented in
> [`V040_WALLET_SEND_DESIGN.md`](V040_WALLET_SEND_DESIGN.md)
> "Future work": (1) cross-`tx_id` replay against
> `/api/v1/wallet/submit-signed`, (2) non-atomic balance debit in
> `pkg/storage/sqlite.go::UpdateBalance`. Design + closure status
> in [`V041_REPLAY_PROTECTION_DESIGN.md`](V041_REPLAY_PROTECTION_DESIGN.md).
>
> **Status of this document**: PRE-DEPLOY skeleton committed
> alongside the v0.4.1 tag cut. The cosign / Rekor / GHCR / BLR1
> verification sections marked **PENDING** below will be filled in
> by the operator once `release-container.yml` finishes the build
> and the BLR1 binary swap completes. Once those sections turn
> green, this header note is removed and the doc becomes the
> canonical v0.4.1 supply-chain evidence.
>
> Companion documents:
> [`RELEASE_EVIDENCE_v0.4.0.md`](RELEASE_EVIDENCE_v0.4.0.md) (v0.4.0
> self-custody Send tab), [`RELEASE_EVIDENCE_v0.3.3.md`](RELEASE_EVIDENCE_v0.3.3.md)
> (v0.3.3 mint deprecation), [`RELEASE_EVIDENCE.md`](RELEASE_EVIDENCE.md)
> (v0.3.0 baseline + CI methodology).

## What v0.4.1 ships

v0.4.1 is the **replay-protection + atomic-debit** release. It
takes the v0.4.0 self-custody Send pipeline and removes the two
known unsafe behaviors:

1. **Cross-`tx_id` replay** — every envelope now carries a
   per-account `nonce uint64`. The validator atomically bumps it
   inside the same SQL transaction as the balance debit, so a
   replayed envelope (same nonce, different `tx_id`) is rejected
   with HTTP 409 `nonce_replay` rather than double-spending.

2. **Non-atomic balance debit** — the v0.4.0 trio of
   `storageHasTransaction` + `GetBalance` + `StoreTransaction`
   (which had a race window between the balance check and the
   debit write) is replaced with a single
   `storage.ApplyTransferAtomic(sender, recipient, amount, fee,
   envelopeNonce, txID, rawEnvelope)` call that performs tx_id
   uniqueness, nonce CAS, balance gate, debit, credit, nonce
   bump, and transaction insert all inside one SQL transaction.

The wire format adds one field (`nonce`, `omitempty`); legacy
v0.4.0 envelopes (nonce field absent or 0) still verify and apply.
The browser wallet auto-fetches the next nonce from a new public
helper endpoint `GET /api/v1/wallet/nonce?sender=…`, and the
`qsdmcli wallet sign-tx` subcommand wraps the same logic for
non-browser callers.

## Commit anchors

| Anchor | Commit | Date | Summary |
|--------|--------|------|---------|
| v0.4.1 foundation (Session 99) | `ecfa121` | 2026-05-13 | Design doc + nonce wire-format + atomic-debit storage interface + SQLite v0.4.1 schema migration + 3 new monitoring result tags |
| v0.4.1 handler (Session 100)   | `8659b04` | 2026-05-14 | `SubmitSignedTransaction` calls `GetNonce` + `ApplyTransferAtomic`; `StorageInterface` extended in lockstep across `pkg/api/server.go` + `cmd/qsdm/main.go`; 5 new handler tests |
| v0.4.1 client+tooling (Session 100) | `2bdacb8` | 2026-05-14 | `GET /api/v1/wallet/nonce` endpoint + 6 tests; `qsdmcli wallet sign-tx` + 5 tests with hard signature-verification guarantee; browser Send tab nonce input + WASM rebuild + SRI refresh; `cmd/v041smoke` 5-probe super-set |
| v0.4.1 release-cut (Session 100, this doc) | _tag commit_ | 2026-05-14 | Landing pill v0.4.0 → v0.4.1; `RELEASE_EVIDENCE_v0.4.1.md` skeleton; `git tag v0.4.1` annotated |

## Test posture at tag-time (CGO_ENABLED=0)

```
go build ./...                     exit 0
go vet   ./...                     exit 0
go test  ./pkg/api/...             ok  (19/19  — 8 v0.4.0 + 5 v0.4.1 handler
                                                  + 6 nonce-endpoint)
go test  ./cmd/qsdmcli/...         ok  (5/5 sign-tx  — incl. hard
                                                       verifySignature() against
                                                       server canonicalisation)
go test  ./pkg/audit/...           ok
```

The hard guarantee inside the CLI test suite
(`cmd/qsdmcli/wallet_signtx_test.go::verifySignature`) is the
strongest property of this release: it runs the exact
parse-→-clear-sig-and-pubkey-→-re-marshal canonicalisation
algorithm `pkg/api/handlers.go::SubmitSignedTransaction` uses on
the server side, then calls `mldsa87.Verify` against the
envelope's own public_key. Any byte-level drift between the CLI
canonicalisation and the server canonicalisation would surface as
a unit-test failure. Same property is exercised through the
browser path via the WASM signer's `qsdm_wallet_sign_transaction`
helper, whose canonicalisation algorithm is the same Go
`json.Marshal` over the same `txEnvelope` field-shape mirror.

## At a glance

| Verification | Subject | Result |
|--------------|---------|--------|
| SHA256SUMS (root of binary integrity tree) | `release-container.yml@refs/tags/v0.4.1` | _PENDING — needs release-container.yml workflow run_ |
| Individual binary signature (`qsdmminer-console-linux-amd64`) | same | _PENDING_ |
| Source SBOM (`qsdm-source-sbom.spdx.json`) | same | _PENDING_ |
| Container `ghcr.io/blackbeardone/qsdm:0.4.1` | same | _PENDING_ |
| Container `ghcr.io/blackbeardone/qsdm-validator:0.4.1` | same | _PENDING_ |
| Container `ghcr.io/blackbeardone/qsdm-miner:0.4.1` | same | _PENDING_ |
| Binary content hash vs SHA256SUMS row | `qsdmminer-console-linux-amd64` | _PENDING_ |
| BLR1 binary swap (validator runs v0.4.1) | `/api/v1/status` reports `v0.4.1` | _PENDING — operator step_ |
| Public POST `/wallet/submit-signed` v0.4.1 wire | nonce field accepted; `nonce_replay` 409 surfaces | _PENDING_ |
| `cmd/v041smoke` 5/5 against production | nonce endpoint + nonce-conflict CAS visible | _PENDING_ |
| Landing pill | v0.4.0 → v0.4.1 confirmed over HTTPS | _PENDING_ |
| `wallet.wasm` SRI match over HTTPS | `sha384-HOd3kgcQwL/Gb+ujOF5phQeYLv73om7peCWQkN/mif3mQmBSefaCP1q1V8q0AE04` | _PENDING — operator deploys updated landing_ |

## Browser-wallet WASM anchor (refreshed in v0.4.1)

The browser-wallet WASM is rebuilt against the Session 99 source
that added the `Nonce` field to `txEnvelope`. The size delta vs
v0.4.0 is 63 bytes (the extra struct field's emit-machinery).
Subresource Integrity (SRI) for the deployed asset:

| File | sha384 (SRI form) | Size |
|------|-------------------|------|
| `wallet.wasm` | `sha384-HOd3kgcQwL/Gb+ujOF5phQeYLv73om7peCWQkN/mif3mQmBSefaCP1q1V8q0AE04` | 3 884 194 B |
| `wallet.js`   | `sha384-8BO6kH4J1WSt3LmqWNeT4LpuLReHbVTWJ1YH8GCtikE9hPnw5QBGDUyfIYj3gYpC` | 42 119 B |
| `wasm_exec.js` (unchanged from v0.4.0 — Go toolchain pin) | `sha384-PWCs+V4BDf9yY1yjkD/p+9xNEs4iEbuvq+HezAOJiY3XL5GI6VyJXMsvnjiwNbce` | (toolchain default) |

Operator self-check (post-deploy):

```bash
# wallet.wasm hash matches the SRI pinned in wallet.js + wallet.html
curl -sSL https://qsdm.tech/wallet.wasm | openssl dgst -sha384 -binary | base64 -w0
# Expected: HOd3kgcQwL/Gb+ujOF5phQeYLv73om7peCWQkN/mif3mQmBSefaCP1q1V8q0AE04

# wallet.js hash matches wallet.html's <script integrity> attribute
curl -sSL https://qsdm.tech/wallet.js   | openssl dgst -sha384 -binary | base64 -w0
# Expected: 8BO6kH4J1WSt3LmqWNeT4LpuLReHbVTWJ1YH8GCtikE9hPnw5QBGDUyfIYj3gYpC
```

## Provenance fingerprint

_PENDING — fill in after `release-container.yml@refs/tags/v0.4.1`
completes._ Same Sigstore OID extraction process as
[`RELEASE_EVIDENCE_v0.4.0.md`](RELEASE_EVIDENCE_v0.4.0.md#provenance-fingerprint),
but the workflow ref + run URL will rebind to `refs/tags/v0.4.1`
and the next workflow run number.

## Container image digests

_PENDING — fill in after `release-container.yml@refs/tags/v0.4.1`
completes._

## Live post-deploy probes

_PENDING — fill in after the BLR1 binary swap._ The minimum set
the operator must verify (per the v0.4.0 release-cut checklist):

1. `GET https://api.qsdm.tech/api/v1/status` reports
   `"version":"v0.4.1"`.
2. `GET https://api.qsdm.tech/api/v1/wallet/nonce?sender=<fresh
   hex64>` returns HTTP 200 with `{sender, nonce:0, next:1}` (was
   HTTP 404 on v0.4.0).
3. `POST https://api.qsdm.tech/api/v1/wallet/submit-signed` with a
   v0.4.1 envelope carrying `nonce: 1` over a fresh keypair
   (signature-only check, no funding) returns HTTP 402
   `insufficient_balance` — confirms the new
   `ApplyTransferAtomic` path is reachable.
4. `cd QSDM/source && CGO_ENABLED=0 go run ./cmd/v041smoke` reports
   `PASS=5 FAIL=0` from this workstation against
   `https://api.qsdm.tech`.
5. `https://qsdm.tech/` ver-pill text reads `v0.4.1` and the
   anchor target is `https://github.com/blackbeardONE/QSDM/releases/tag/v0.4.1`.
6. `https://qsdm.tech/wallet.wasm` sha384 matches
   `sha384-HOd3kgcQwL/Gb+ujOF5phQeYLv73om7peCWQkN/mif3mQmBSefaCP1q1V8q0AE04`.

Once all six green, this skeleton header is removed and the
"PENDING" rows in the At-a-glance table are flipped to ✓ Verified.
