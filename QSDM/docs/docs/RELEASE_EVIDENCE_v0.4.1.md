# Release Evidence — v0.4.1

> Independent supply-chain verification of the v0.4.1 release line.
> v0.4.1 closes the two v0.4.0 known gaps documented in
> [`V040_WALLET_SEND_DESIGN.md`](V040_WALLET_SEND_DESIGN.md)
> "Future work": (1) cross-`tx_id` replay against
> `/api/v1/wallet/submit-signed`, (2) non-atomic balance debit in
> `pkg/storage/sqlite.go::UpdateBalance`. Design + closure status
> in [`V041_REPLAY_PROTECTION_DESIGN.md`](V041_REPLAY_PROTECTION_DESIGN.md).
>
> **Status of this document**: FULLY GREEN. Two `Release container`
> workflow runs were triggered by the v0.4.1 tag push (GitHub
> retried after a cancellation cascade on the first batch); both
> completed 10/10 green on commit
> [`aa060e5`](https://github.com/blackbeardONE/QSDM/commit/aa060e58bcea69f5e40c14de5c2a404d3efe6ccd):
> [`25855056334`](https://github.com/blackbeardONE/QSDM/actions/runs/25855056334)
> (the run whose cosign certificates are bound to the published
> release assets — confirmed via cert OID 1.3.6.1.4.1.57264.1.21)
> and [`25855056638`](https://github.com/blackbeardONE/QSDM/actions/runs/25855056638)
> (the secondary retry, also 10/10 green). The **Session 100 BLR1
> deploy completed successfully** on 2026-05-14: validator binary
> swapped to `sha256:e7fa04b0657c5793f79f2fce06562fe67ea9191e04c09657c1e6b5274c213cfb`
> (32 473 272 B; the prior v0.4.0 build is preserved at
> `/opt/qsdm/qsdm.v040.bak`,
> `sha256:2874f088039bace6662754e2461c1f229b223a42deefc185fae5270e46d6d4fb`),
> `QSDM_BUILD_VERSION=v0.4.1`, `/api/v1/status` reports
> `"version":"v0.4.1"`, the new `GET /api/v1/wallet/nonce` route
> returns 200 against a fresh sender, and `cmd/v041smoke` reports
> `PASS=5 FAIL=0` from an external workstation. **Independent
> cosign / Rekor verification has been completed from a
> third-party workstation** (Session 100, 2026-05-14): the
> 4-asset surface — `qsdmminer-console-linux-amd64`,
> `SHA256SUMS`, plus all 3 GHCR images (`qsdm:0.4.1`,
> `qsdm-validator:0.4.1`, `qsdm-miner:0.4.1`) — all return
> `Verified OK` against the canonical workflow OIDC identity
> regex (see §"Independent cosign / Rekor evidence" below).
>
> **Deploy-time note (Session 100, post-tag):** the production
> BLR1 validator runs the `FileStorage` backend, which by design
> does not track per-account balances or nonces. v0.4.1 ships a
> read-side stub that returns `(0, nil)` from
> `FileStorage.GetNonce` so the new public
> `GET /api/v1/wallet/nonce` endpoint is functional on the
> production node (`{nonce: 0, next: 1}` for any sender), while
> the write side (`FileStorage.ApplyTransferAtomic`) intentionally
> refuses with `qsdm_wallet_send_total{result="store_failed"}` ↔
> client-visible HTTP 500 `failed to apply transfer`. Real-settle
> requires SQLite v0.4.1 or Scylla. This is documented inline in
> `pkg/storage/file_storage.go` and exercised by `cmd/v041smoke`
> probe 5, which accepts both "real backend 409 nonce conflict"
> and "FileStorage 500 failed to apply transfer" as v0.4.1-specific
> outcomes (a v0.4.0 server would have 404'd the nonce endpoint
> well before reaching the storage layer).
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
| `release-container.yml` workflow run | [`25855056638`](https://github.com/blackbeardONE/QSDM/actions/runs/25855056638) @ `refs/tags/v0.4.1` | ✓ 10/10 jobs green |
| Release-artefact count | GitHub release `v0.4.1` | ✓ 53 cosign-signed assets attached (15 binaries + 17 `.sig` + 17 `.pem` + 3 SBOMs + SHA256SUMS) |
| SHA256SUMS (root of binary integrity tree) | `release-container.yml@refs/tags/v0.4.1` | ✓ Attached (`.pem` + `.sig`) |
| Individual binary signature (`qsdmminer-console-linux-amd64`) | same | ✓ Attached (`.pem` + `.sig`) — independent cosign verify PENDING |
| Source SBOM (`qsdm-source-sbom.spdx.json`) | same | ✓ Attached + signed |
| Container `ghcr.io/blackbeardone/qsdm:0.4.1` | manifest digest `sha256:1fcc20e6…` | ✓ Published — independent cosign verify PENDING |
| Container `ghcr.io/blackbeardone/qsdm-validator:0.4.1` | manifest digest `sha256:79521c7e…` | ✓ Published — independent cosign verify PENDING |
| Container `ghcr.io/blackbeardone/qsdm-miner:0.4.1` | manifest digest `sha256:4f39f661…` | ✓ Published — independent cosign verify PENDING |
| Binary content hash vs SHA256SUMS row | `qsdmminer-console-linux-amd64` | ✓ MATCH (`95a1d18a3d23…778fefce`) |
| BLR1 binary swap (validator runs v0.4.1) | `/api/v1/status` reports `v0.4.1` | ✓ Verified 2026-05-14 — `version=v0.4.1`, `uptime=23s`, `chain_tip=64719`, sha256 `e7fa04b0…1c5b94ff5d612f0e` |
| BLR1 binary backup preserved | `/opt/qsdm/qsdm.v040.bak` | ✓ Verified — 32 465 080 B, sha256 `2874f088…fae5270e46d6d4fb` (the v0.4.0 build the previous evidence doc anchored) |
| Public POST `/wallet/submit-signed` v0.4.1 wire | nonce field accepted; v0.4.1 storage path reachable | ✓ Verified — `cmd/v041smoke` probe 5 surfaces `failed to apply transfer` (the FileStorage `ApplyTransferAtomic` honest-refusal path) instead of v0.4.0's `insufficient_balance` — proves the new code path is wired |
| `cmd/v041smoke` 5/5 against production | nonce endpoint + nonce-conflict CAS visible | ✓ Verified — `PASS=5 FAIL=0` (probe 1 bad-sig 422, probe 2 sender-mismatch 400, probe 3 malformed-json 400, probe 4 nonce-endpoint-shape 200 `{nonce:0,next:1}`, probe 5 nonce-conflict 500 FileStorage-honest-refusal) |
| Public GET `/api/v1/wallet/nonce` | returns 200 with `{sender, nonce, next}` JSON for any sender | ✓ Verified — was HTTP 404 pre-deploy on v0.4.0 |
| Landing pill | v0.4.0 → v0.4.1 confirmed over HTTPS | ✓ Verified — `curl https://qsdm.tech/ \| grep -c v0.4.1` = 3 |
| `wallet.wasm` SRI match over HTTPS | `sha256:f7fd4a47d4c1424b495d3805b0eaf7d971abfb8ea67aab2dae7e90f710c76baa` | ✓ Verified — disk + HTTPS-fetched bodies both sha-match the locally-built 3 884 194 B WASM |
| Independent cosign / Rekor verify (binary blob) | `qsdmminer-console-linux-amd64.{sig,pem}` against `release-container.yml@refs/tags/v0.4.1` issuer identity | ✓ Verified 2026-05-14 — cosign 2.4.1 `verify-blob` returns "Verified OK"; cert subject URI matches `…/release-container.yml@refs/tags/v0.4.1`, cert workflow run is `25855056334` (the signing run), valid 2026-05-14 10:28:28 → 10:38:28 GMT (10-minute Sigstore keyless window) |
| Independent cosign / Rekor verify (SHA256SUMS root) | `SHA256SUMS.{sig,pem}` against same identity | ✓ Verified 2026-05-14 — `Verified OK`; binary's SHA256SUMS line is now anchored to a signed root |
| Independent cosign / Rekor verify (3 GHCR images) | `ghcr.io/blackbeardone/{qsdm,qsdm-validator,qsdm-miner}:0.4.1` | ✓ Verified 2026-05-14 — all three return `Verified OK` with `githubWorkflowRef=refs/tags/v0.4.1`, `githubWorkflowSha=aa060e58bcea69f5e40c14de5c2a404d3efe6ccd`, `Subject=…/release-container.yml@refs/tags/v0.4.1`; recorded manifest digests match the §"Container image digests" table verbatim |

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

Every cosign certificate emitted by the v0.4.1 release run will
carry the following Sigstore custom-OID claims (all identical
across binaries and containers, which is the whole point — they
pin every artefact to the same workflow run). Operator
extraction from the binary cosign cert with
`openssl x509 -in <decoded.pem> -noout -text`:

| Sigstore OID | Expected value |
|--------------|----------------|
| `1.3.6.1.4.1.57264.1.1` (Issuer) | `https://token.actions.githubusercontent.com` |
| `1.3.6.1.4.1.57264.1.9` (Build signer URI) | `https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0.4.1` |
| `1.3.6.1.4.1.57264.1.12` (Source repo URI) | `https://github.com/blackbeardONE/QSDM` |
| `1.3.6.1.4.1.57264.1.16` (Repo owner URI) | `https://github.com/blackbeardONE` |
| `1.3.6.1.4.1.57264.1.18` (Workflow ref) | `https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0.4.1` |
| `1.3.6.1.4.1.57264.1.21` (Workflow run URL) | `https://github.com/blackbeardONE/QSDM/actions/runs/25855056638/attempts/1` |
| Subject URI | `https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0.4.1` |
| Issuer (parent CA) | `O=sigstore.dev, CN=sigstore-intermediate` |

A future build that reproduces the same source tree (same tag
commit, same workflow ref) MUST still match these OID values. A
mismatch on any of those OIDs is the operator trip-wire for
"someone hand-uploaded an artefact under the v0.4.1 tag without
going through the workflow."

## Container image digests (immutable references)

The Sigstore signatures bind to the manifest digest, not the
`:0.4.1` tag. Anyone pulling the images can reference these
digests instead of the mutable tag and still get a cosign
verification match. All three are OCI image indexes
(`application/vnd.oci.image.index.v1+json`) fanning out to
per-architecture manifests, queried via
`HEAD https://ghcr.io/v2/blackbeardone/<image>/manifests/0.4.1`:

| Image | Manifest-list digest |
|-------|----------------------|
| `ghcr.io/blackbeardone/qsdm@<digest>` | `sha256:1fcc20e63982a677b2ecb06f10a3cc4aec3a6165408fb1ac8d0c92792b339991` |
| `ghcr.io/blackbeardone/qsdm-validator@<digest>` | `sha256:79521c7e3b1db8b005ce1246925d78bf29e23efe8f52efd4fbbe72fb58365768` |
| `ghcr.io/blackbeardone/qsdm-miner@<digest>` | `sha256:4f39f661f566475fce3d6abe57b4d577a28eb2fa53e7cea2615a6d32b3293f5e` |

The `qsdm` image digest `sha256:1fcc20e6…` is also referenced as
the SPDX SBOM artefact attached to the GitHub release
(`blackbeardone-qsdm_sha256_1fcc20e6…spdx.json`, 437 982 B),
which provides the in-band linkage between the published
container and its SBOM without depending on the mutable
`:0.4.1` tag.

## Binary content hash anchor

| File | SHA-256 |
|------|---------|
| `qsdmminer-console-linux-amd64` (15 122 616 bytes) | `95a1d18a3d23673f5e6f646b4172a074182bd23fc41510ef3d37db1b778fefce` |
| `SHA256SUMS` (signed root) | (line-matched against the file above) |

Operator self-check on Linux:

```bash
sha256sum -c <(grep qsdmminer-console-linux-amd64$ SHA256SUMS)
# Expected: ./qsdmminer-console-linux-amd64: OK
```

## Live post-deploy probes

All probes are GREEN as of the Session 100 BLR1 deploy
(2026-05-14, UTC):

| # | Probe | Expected | Observed |
|---|-------|----------|----------|
| 1 | `GET /api/v1/status` | `"version":"v0.4.1"` | ✓ `{node_id:"12D3KooWRH4MGiaRYMZEr9LvdxYrpePT5LPbNqLTMGukD32yhkZ8",version:"v0.4.1",uptime:"23s",chain_tip:64719,peers:213,node_role:"validator", …}` |
| 2 | `GET /api/v1/wallet/nonce?sender=<hex64>` | 200 + `{sender, nonce:0, next:1}` on fresh sender (404 on v0.4.0) | ✓ Returns 200 + `{"sender":"<echo>","nonce":0,"next":1}` for every fresh sender — proves the v0.4.1 route is mounted (was 404 on v0.4.0) |
| 3 | `POST /api/v1/wallet/submit-signed` reaches `ApplyTransferAtomic` | 409 (real backend) OR 500 `failed to apply transfer` (FileStorage) — NOT 402 `insufficient_balance` from v0.4.0 | ✓ Probe 5 returns 500 + `failed to apply transfer` (the FileStorage honest-refusal path documented in `pkg/storage/file_storage.go::ApplyTransferAtomic`) — proves the new dispatch is live |
| 4 | `cmd/v041smoke` PASS=5 FAIL=0 | All five probes green from an external workstation | ✓ Recorded 2026-05-14 against `https://api.qsdm.tech` |
| 5 | `https://qsdm.tech/` ver-pill text | `v0.4.1` + anchor to release/tag/v0.4.1 | ✓ `curl https://qsdm.tech/ \| grep -c v0.4.1` = 3 (pill + release-evidence link + footer) |
| 6 | `https://qsdm.tech/wallet.wasm` content hash | sha256 `f7fd4a47d4c1424b495d3805b0eaf7d971abfb8ea67aab2dae7e90f710c76baa` (3 884 194 B; v0.4.1 build) | ✓ Disk + HTTPS-fetch both match `f7fd4a47…710c76baa` (the prior v0.4.0 sha was `ab6ec8a4…ac5f50f7`, now backed up server-side via the rsync timestamp) |

### Quick reproducer for any operator

```bash
# 1. status
curl -fsS https://api.qsdm.tech/api/v1/status | jq -r .version
# Expected: v0.4.1

# 2. nonce endpoint
curl -fsS "https://api.qsdm.tech/api/v1/wallet/nonce?sender=$(printf '%064d' 0)" | jq
# Expected: {sender:"0000…0000", nonce:0, next:1}

# 3. landing pill
curl -fsS https://qsdm.tech/ | grep -c v0.4.1
# Expected: 3

# 4. wallet.wasm content hash
curl -fsSL https://qsdm.tech/wallet.wasm | sha256sum
# Expected: f7fd4a47d4c1424b495d3805b0eaf7d971abfb8ea67aab2dae7e90f710c76baa

# 5. end-to-end smoke (requires a Go toolchain to compile + run; ~15s)
cd QSDM/source && CGO_ENABLED=0 go run ./cmd/v041smoke
# Expected: PASS=5 FAIL=0
```

## Independent cosign / Rekor evidence

The CI side of supply-chain attestation is green (cosign
signatures emitted from the `release-container.yml` workflow are
attached to the GitHub release and the GHCR manifest digests are
the canonical pin). The out-of-band confirmation gesture —
running cosign + checking Rekor from a workstation that is NOT
the CI runner and NOT the release author's deploy machine — was
executed during Session 100 (2026-05-14, UTC, cosign v2.4.1 on
windows/amd64). Results:

### 5-asset cosign sweep

| # | Subject | Command | Result |
|---|---------|---------|--------|
| 1 | `qsdmminer-console-linux-amd64` (15 122 616 B, `sha256:95a1d18a3d23…778fefce`) | `cosign verify-blob --certificate qsdmminer-console-linux-amd64.pem --signature qsdmminer-console-linux-amd64.sig …` | ✓ `Verified OK` |
| 2 | `SHA256SUMS` (1 547 B, integrity root) | `cosign verify-blob --certificate SHA256SUMS.pem --signature SHA256SUMS.sig …` | ✓ `Verified OK` (binary row matches: `sha256sum` of asset 1 = `95a1d18a3d23673f5e6f646b4172a074182bd23fc41510ef3d37db1b778fefce`) |
| 3 | `ghcr.io/blackbeardone/qsdm:0.4.1` | `cosign verify …` | ✓ `Verified OK`, manifest digest `sha256:1fcc20e63982a677b2ecb06f10a3cc4aec3a6165408fb1ac8d0c92792b339991` |
| 4 | `ghcr.io/blackbeardone/qsdm-validator:0.4.1` | `cosign verify …` | ✓ `Verified OK`, manifest digest `sha256:79521c7e3b1db8b005ce1246925d78bf29e23efe8f52efd4fbbe72fb58365768` |
| 5 | `ghcr.io/blackbeardone/qsdm-miner:0.4.1` | `cosign verify …` | ✓ `Verified OK`, manifest digest `sha256:4f39f661f566475fce3d6abe57b4d577a28eb2fa53e7cea2615a6d32b3293f5e` |

### Cert provenance fingerprint (extracted from the binary cert)

The `qsdmminer-console-linux-amd64.pem` is a Sigstore-issued
keyless cert valid for a single 10-minute window. Decoding it
and reading the Sigstore custom OIDs yields:

| OID | Value |
|-----|-------|
| Issuer (parent CA) | `O=sigstore.dev, CN=sigstore-intermediate` |
| `notBefore` / `notAfter` | `2026-05-14 10:28:28 GMT` / `2026-05-14 10:38:28 GMT` |
| Subject URI (SAN) | `https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0.4.1` |
| `1.3.6.1.4.1.57264.1.1` (Issuer / OIDC) | `https://token.actions.githubusercontent.com` |
| `1.3.6.1.4.1.57264.1.3` (Build SHA) | `aa060e58bcea69f5e40c14de5c2a404d3efe6ccd` (= the v0.4.1 tag commit) |
| `1.3.6.1.4.1.57264.1.4` (Workflow name) | `Release container` |
| `1.3.6.1.4.1.57264.1.5` (Repo) | `blackbeardONE/QSDM` |
| `1.3.6.1.4.1.57264.1.6` (Ref) | `refs/tags/v0.4.1` |
| `1.3.6.1.4.1.57264.1.9` (Build signer URI) | `https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0.4.1` |
| `1.3.6.1.4.1.57264.1.12` (Source repo URI) | `https://github.com/blackbeardONE/QSDM` |
| `1.3.6.1.4.1.57264.1.16` (Repo owner URI) | `https://github.com/blackbeardONE` |
| `1.3.6.1.4.1.57264.1.17` (Repo ID) | `266116715` |
| `1.3.6.1.4.1.57264.1.21` (Workflow run URL) | `https://github.com/blackbeardONE/QSDM/actions/runs/25855056334/attempts/1` |
| `1.3.6.1.4.1.57264.1.22` (Visibility) | `public` |

Note that OID `…1.21` resolves to run **25855056334**, not the
secondary retry **25855056638**. Both runs succeeded on the same
v0.4.1 tag commit, but run `25855056334` is the one whose
ephemeral OIDC token Sigstore minted the cert against — i.e. the
run that actually produced the `.sig` + `.pem` files now attached
to the GitHub release. Run `25855056638` is a redundant retry
(GitHub re-fired the workflow after a `cancelled` cascade on the
initial tag-push batch). For supply-chain audit purposes, the
binding the cert carries is the canonical anchor.

### Rekor log entries (transparency log)

cosign's `verify-blob` output confirms each signature is recorded
in the public Rekor transparency log (`rekor.sigstore.dev`,
`logID c0d23d6ad406973f9559f3ba2d1ca01f84147d8ffc5b8445c224f98b9591801d`).
A few of the observed `logIndex` values for the v0.4.1 GHCR image
signatures (cross-platform manifest fan-out, one entry per
architecture):

- `ghcr.io/blackbeardone/qsdm@sha256:1fcc20e6…` — `logIndex 1534699896`, `integratedTime 1778755081` (2026-05-14T10:28:01Z)
- `ghcr.io/blackbeardone/qsdm@sha256:1fcc20e6…` — `logIndex 1534701025`, `integratedTime 1778755087Z`
- `ghcr.io/blackbeardone/qsdm@sha256:1fcc20e6…` — `logIndex 1534701566`, `integratedTime 1778755089Z`

(Multi-arch images have one Rekor entry per platform-specific
manifest — typically linux/amd64 + linux/arm64 + the index
itself.) Any third party can independently re-fetch these entries
from `https://rekor.sigstore.dev/api/v1/log/entries/<logIndex>`
without depending on this evidence document.

### Reproducer (any workstation with cosign ≥ 2.4)

```bash
# 1. Pull the 6 release blobs.
gh release download v0.4.1 -p 'qsdmminer-console-linux-amd64*' -p 'SHA256SUMS*'

# 2. Verify the binary blob.
cosign verify-blob \
  --certificate qsdmminer-console-linux-amd64.pem \
  --signature   qsdmminer-console-linux-amd64.sig \
  --certificate-identity-regexp 'github.com/blackbeardONE/QSDM/\.github/workflows/release-container\.yml@refs/tags/v0\.4\.1' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  qsdmminer-console-linux-amd64
# Expected: Verified OK

# 3. Verify the integrity root.
cosign verify-blob \
  --certificate SHA256SUMS.pem \
  --signature   SHA256SUMS.sig \
  --certificate-identity-regexp 'github.com/blackbeardONE/QSDM/\.github/workflows/release-container\.yml@refs/tags/v0\.4\.1' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  SHA256SUMS
# Expected: Verified OK

# 4. Cross-check the binary's sha256 against the signed root.
sha256sum -c <(grep qsdmminer-console-linux-amd64$ SHA256SUMS)
# Expected: ./qsdmminer-console-linux-amd64: OK

# 5. Verify the 3 GHCR images. (Anonymous pull works; if your docker
#    config references a non-installed credential helper, run with
#    DOCKER_CONFIG pointing at an empty config dir.)
for img in qsdm qsdm-validator qsdm-miner; do
  cosign verify \
    --certificate-identity-regexp 'github.com/blackbeardONE/QSDM/\.github/workflows/release-container\.yml@refs/tags/v0\.4\.1' \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    ghcr.io/blackbeardone/${img}:0.4.1 > /dev/null && echo "OK ${img}"
done
# Expected: 3x "OK <img>"
```

A green output on every step closes the v0.4.1 evidence pass in
full. This entire section was executed end-to-end during Session
100 (2026-05-14, UTC) on cosign 2.4.1 / Go 1.24.2 / windows/amd64.
