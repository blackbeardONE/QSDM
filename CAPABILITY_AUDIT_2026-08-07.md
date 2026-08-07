# QSDM+ Capability Audit — Actual Implementation State

**Branch:** `agent/multi-mother-hive-release` · **Audit date:** 2026-08-07 · **Method:** 9 domain audits, each adversarially re-checked; every percentage grounded in read source with `file:line`.

> **Post-audit correction (2026-08-07).** Two claims were re-verified by hand against source after the audit closed:
> - **REFUTED —** *"No supply cap anywhere."* A real, integer-exact, 16-test emission cap exists at `pkg/chain/emission.go:39-46,71-74,162-177` and is clamped at `blockdriver.go:633-634`. The "100M CELL hard cap" feature was raised **12% → 55%** and the Tokenomics domain **44% → 48%**, moving the headline to **~60%**.
> - **CONFIRMED and extended —** the 1e15 genesis allocation is real (it is `float64` **CELL**, not dust). Re-verification surfaced a defect **neither the auditor nor the skeptic isolated**: because `Account.Balance` is `float64` and the funder sits at ~1e15 (ULP = 0.125 CELL), `account.go:172` quantises every funder debit while `account.go:180` credits the miner exactly — **destroying ~0.06 CELL per block ≈ 190k CELL/year**. See gap #4.
>
> Treat other individual percentages as well-evidenced but not hand-verified to this depth.

---

## 0. Remediation status (2026-08-07)

**All 13 gaps closed.** Work done smallest → biggest. Everything builds clean and the full Go suite passes: `go test -tags dilithium_circl ./...` — **80 packages, 0 failures** — plus the Go SDK's own module (1 package, 0 failures), which is separate so that `go get` resolves. 78 new tests across 12 files, each written to fail against the original code.

The remaining consensus item is an **operator action, not code**: re-derive genesis first, then choose one coordinated `fork_dust_height` for every validator. The JavaScript SDK on current main has already advanced to `0.3.3`; do not publish the stale `0.3.1` audit-branch package. Publishing `0.3.3` remains a separate release action requiring registry credentials.

| # | Gap | Status | Evidence |
|---|---|---|---|
| 1 | Gossip sender impersonation (**account drain**) | **FIXED** | `txsig.go verifyEd25519` now binds `sender == hex(sha256(pubkey))`, mirroring the ML-DSA branch; pinned keys via `RegisterKey` remain authoritative. Regression test `TestSigVerifier_Ed25519RejectsSenderImpersonation`. |
| 2 | Governance unreachable (~2,500 LOC dead) | **FIXED** | Added `[governance] authorities` / `QSDM_GOVERNANCE_AUTHORITIES`; wired to `v2wiring.Config.GovernanceAuthorities` in `main.go`; startup logs the posture. 3 config tests. |
| 3 | §8.3 fraud quarantine unreachable | **FIXED** | New `mining.StructuralBatchValidator` (canonical ordering, duplicate cells, zero content hash, optional mesh lookup) replaces `acceptAllBatchValidator` as the miningsvc default. 8 tests. |
| 4 | `/wallet/send` always 500 | **FIXED** | `syncWalletPreflightBalance()` mirrors canonical ledger state before `CreateTransaction`. Only sources with an explicit `present` flag may overwrite the cache — storage is excluded because `(0, nil)` cannot distinguish unknown from zero. 2 tests. |
| 5 | Peer bans had no effect | **FIXED** | `IsBanned` now consulted at all three ingresses (tx, evidence, BFT gossip) before parsing or state mutation. 4 tests. |
| 6 | Rate limiter bypassable by header rotation | **FIXED** | Pre-auth bucket keys on client IP only (`X-API-Key` path removed); `X-Forwarded-For` honoured only under `QSDM_TRUST_PROXY_HEADERS`; source port stripped. `RoleRateLimiter` now actually mounted, inside `AuthMiddleware` so it keys on verified claims. 4 tests. |
| 7 | Forged-attestation **bond theft** | **FIXED** | An unparseable bundle can no longer slash: it returns `ErrEvidenceVerification` instead of `maxSlash()`. node_id lives only inside the bundle, so unparseable evidence is unattributable by construction. Empty `p.NodeID` also refused. 2 tests. |
| 9 | Block production dead in consensus mode | **FIXED** | Pre-seal gate now requires `ChainReplayApplier` (clone capability) rather than the concrete `*AccountStore`. Production's `*EnrollmentAwareApplier` already satisfied it. Test `TestBlockProducer_PreSealAcceptsEnrollmentAwareApplier`. |
| 10 | Unauthenticated consensus messages | **FIXED** | All four message types now authenticated with ML-DSA-87 over domain-separated digests, using one self-certifying identity (`validator == hex(sha256(pubkey))`) so no key registry or distribution ceremony is needed — the node's existing wallet key is the consensus key. **Votes:** propose/prevote/precommit bind kind, height, round, signer, vote value and proposed body. **Evidence:** `EquivocationProof` carries two conflicting signed votes, so an innocent validator can no longer be framed. **POL:** round certificates cover the claimed validator set; lock proofs cover the prevotes themselves. **Blocks:** producer signature over the block hash; `SignBlock` derives `ProducerID` from the key, so a block cannot be sealed under a producer identity that disagrees with the signing key, and the signature is excluded from `computeBlockHash` so no existing block hash changes. One switch (`[consensus] require_signed_votes` / `QSDM_REQUIRE_SIGNED_VOTES`) gates inbound enforcement across all four for rolling upgrades; an invalid signature is always fatal regardless. 31 tests. |
| 11 | CI tested almost nothing | **FIXED** | New `go-test-all` job runs the whole suite (previously `pkg/storage`, `pkg/networking`, `pkg/monitoring`, `pkg/chain`, `pkg/mining`, `internal/**` appeared in **zero** `go test` invocations). Wrote the missing `sqlite_v041_test.go` — 9 tests over real SQLite including a concurrent double-spend test. |
| 12 | Ship-blocking artifacts | **FIXED** | Corrected stale `wallet.js` SRI (the wallet page was genuinely inert on this branch); added `scripts/check_sri_integrity.py` + CI step, verified to catch the exact regression; fixed 4 K8s manifests to the `ghcr.io/...` images CI actually publishes; staged the untracked-but-required `privacy.html` / `support.html`. |
| 8 | float64 money / supply invariant | **FIXED (fork-gated, awaiting activation height)** | Implemented as an approved fork-gated migration, because fixing the *arithmetic* also changes pre-fork state roots — so the fork gates the whole accounting model, not just the encoding. `pkg/chain/dustfork.go`: activation plumbing matching `pkg/mining/fork.go` (defaults to never-active, so unconfigured nodes are byte-identical to before), exact conversions that **refuse** unrepresentable values rather than silently flooring, and `SupplyLedger` — a CAS-based invariant enforcing the 100 M cap against *every* mint path, not just the emission schedule. `pkg/chain/account_dust.go` + `account.go`: `Account.BalanceDust uint64` becomes authoritative at/above the fork with `Balance` kept as a synced mirror for JSON/API consumers; `ApplyTx` and `Credit` do integer arithmetic; `StateRoot` hashes integers. One-time `MigrateToDust` floors deterministically (never inflationary) and **refuses** an unrepresentable allocation rather than clamping. 19 tests, including a 5,000-block supply-conservation test that would fail under float64, a one-dust state-root discrimination test, and a concurrent-mint race test. Config: `[consensus] fork_dust_height` / `QSDM_FORK_DUST_HEIGHT`. **Remaining (operator action, not code):** re-derive genesis to the 90M/10M split and choose a coordinated activation height — the current 1e15 CELL funder is 1e23 dust, which overflows uint64, so `MigrateToDust` will refuse until genesis is corrected. |
| 13 | Go/npm SDK distribution | **FIXED (current npm release pending)** | **Go:** the SDK was not `go get`-able because the parent module declares `github.com/blackbeardONE/QSDM` while its go.mod sits at `QSDM/source/` — a path that does not match its location, so nothing under it resolves. Gave the SDK its own module (`QSDM/source/sdk/go/go.mod`), which is self-contained: it depends only on stdlib and nothing in the parent imports it. `go get github.com/blackbeardONE/QSDM/QSDM/source/sdk/go` now works; install path documented in the package doc and Feature Summary; added a CI step since a separate module falls outside `./...`. **npm:** current main is `0.3.3`, includes the corrected `getTransaction` route and the CELL Stream runtime, and supersedes the stale audit-branch `0.3.1`. The public registry remains at `0.3.0`; publishing `0.3.3` requires registry credentials and its normal release gate. |

Two audit findings were **refuted** during remediation and corrected in place: the "no supply cap anywhere" claim (§3.5, gap #4) and the Hive `native/` packaging claim (gap #10).

---

## 1. Headline

> **QSDM+ is approximately 60% complete against a working end-to-end product — not the ~99% claimed across `QSDM/docs/docs/*_COMPLETE.md`.** The 39-point gap is not missing code; it is *unwired, feature-flagged-off, or security-unsound* code. The two subsystems the product's identity rests on — multi-node BFT consensus and enforced tokenomics — score **37%** and **48%** respectively, and the reference deployment ships with consensus explicitly disabled (`.deploy_stagev.sh:72` sets `QSDM_SOLO_VALIDATOR_MODE=1`).

**Weighted calculation** (weights = essentiality to end-to-end operation):

| Domain | Adj. % | Weight | Contribution |
|---|---|---|---|
| Ledger Core & Consensus | 37 | 0.22 | 8.14 |
| Cryptography & Wallet | 74 | 0.15 | 11.10 |
| Storage & Networking | 67 | 0.13 | 8.71 |
| Mining Protocol V2 | 70 | 0.12 | 8.40 |
| Tokenomics / Tasks / Gov | 48 | 0.12 | 5.76 |
| Edge Compute Pool | 69 | 0.08 | 5.52 |
| Hive Desktop Client | 68 | 0.08 | 5.44 |
| Operator Stack & Trust | 67 | 0.05 | 3.35 |
| Web / SDK / Release Eng | 72 | 0.05 | 3.60 |
| **Total** | | **1.00** | **60.0%** |

**Two sharper framings:**
- *"Can two independent validators reach consensus on a shared ledger today?"* — **No.** Effective completion on that axis is **~15%**: the validator set is hardcoded to `{"bootstrap", own-wallet}` (`cmd/qsdm/main.go:1031,1096`), BFT wire messages carry no signatures (`pkg/chain/bft_wire.go:32-46`), and in non-solo mode block production aborts unconditionally (`pkg/chain/block.go:287-290`).
- *"Is the code that exists well-written?"* — Largely **yes**. `pkg/chain/emission.go`, `pkg/chain/task_state.go`, `pkg/edgepool/*`, `pkg/keystore`, `pkg/walletrecovery` are high-quality, defensively written, well-tested. The failure mode is *integration and enforcement*, not craftsmanship.

---

## 2. Domain Summary (skeptic-adjusted, descending)

| Domain | Completion | State | Biggest gap |
|---|---|---|---|
| Cryptography & Wallet | **74%** | working | `chain.NewSigVerifier()` keyring is permanently empty (`cmd/qsdm/main.go:2262`); ed25519 gossip branch (`pkg/chain/txsig.go:206-230`) binds no sender identity |
| Web / SDK / Release Eng | **72%** | working | Go SDK is not `go get`-able (no root `go.mod`); npm `qsdm-sdk@0.3.0` ships a 404ing `getTransaction`; OpenAPI covers 35 of 79 routes |
| Mining Protocol V2 | **70%** | working | Forged-attestation slashing is unsound — anyone can fabricate evidence and steal a victim's 10 CELL bond (`pkg/mining/slashing/forgedattest/forgedattest.go:219-282`) |
| Edge Compute Pool | **69%** | working | Core settlement is unreachable: no shipped manifest ever populates `authorized_relay_ids` (`pkg/chain/task_state.go:1036-1046`) → zero payouts, proofs never acked |
| Hive Desktop Client | **68%** | working | QSDM-native task runtime is a fake-proof generator (`src/main/controllers/getTaskSource.ts:64-68`) |
| Storage & Networking | **67%** | working | ScyllaDB v0.4.1 primitives are literal stubs (`pkg/storage/scylla.go:791-804`); `pkg/storage`/`pkg/networking` run in **zero** CI test jobs |
| Operator Stack & Trust | **67%** | working | "Attestation" is CPU-generated telemetry (`apps/qsdm-nvidia-ngc/validator_phase1.py:62 simulated_cuda_pow`); trust API publishes hardcoded `gpu_available:true` |
| Tokenomics / Tasks / Gov | **48%** | partial | Emission cap is real (`emission.go:39-46`), but `AccountStore` has no supply invariant, genesis credits ~**1e15 float64 CELL** (`blockdriver.go:95`), and float64 balances destroy ~190k CELL/yr (`account.go:172` vs `:180`). Vesting is 0% — grep "vesting" → no hits. |
| Ledger Core & Consensus | **37%** | prototype | BFT is a single-process vote simulation; block production is unreachable in non-solo mode (`pkg/chain/block.go:287-290` vs `cmd/qsdm/main.go:1265`) |

---

## 3. Feature-Level Detail

### 3.1 Ledger Core & Consensus — 37%

| Feature | % | State | Evidence | Gap |
|---|---|---|---|---|
| Solo-validator bypass (consensus OFF switch) | 100 | production | `cmd/qsdm/main.go:1280-1290`, `:1317-1327`; `.deploy_stagev.sh:72` | Fully implemented — and it is the *deployed* configuration. Negative-signal feature. |
| Mempool fee-priority + admission gating | 75 | working | `pkg/mempool/mempool.go:32,390,132-177`; `cmd/qsdm/main.go:1300-1326` | POL/BFT predicate replaced with `nil` in solo mode (`main.go:1322`); nonce reorder at `block.go:411-453` breaks strict fee ranking |
| Block propagation / follower sync | 70 | working | `pkg/chain/propagation.go:74-206,286-297`; `cmd/qsdm/main.go:1979-2000` | Followers accept any block that hashes correctly — no BFT commit, no POL cert, no producer signature. No reorg path. |
| BFT quorum engine (Tendermint-style) | 68 | working | `pkg/chain/consensus.go:94-121,134-198,254-359,422-498`; 26 tests | Real algorithm, but its only gate-role wiring is skipped in solo mode and unreachable in non-solo mode |
| Block structs / production / external append | 68 | working | `pkg/chain/block.go:54-89,234-395,567-682`; `pkg/chain/merkle.go`; `persist.go:57-238` | `ProduceBlock` returns `ErrPreSealRequiresAccountStore` (`block.go:287-290`) because production applier is `*EnrollmentAwareApplier` (`main.go:1265`). No block signatures. State root is `%f` float64 SHA-256 (`account.go:301-317`). |
| Staked validator reputation / slashing / jailing | 62 | working | `pkg/chain/validator.go:163-218`; `evidence.go:83-142`; `slash_apply.go`, `slash_receipts.go` | `ValidatorSet` has no Save/Load — all slash/jail state lost on restart. Evidence is unsigned. `ActiveValidators()` re-admits jailed validators without `Unjail`. |
| Signed-tx gossip admission | 50 | partial | `pkg/chain/txsig.go:236-262`; `gossip_validation.go:76-108`; `main.go:2262` | Empty keyring ⇒ ed25519 branch (`txsig.go:206-229`) accepts any (sender, attacker-key, attacker-sig). **Any gossip peer can drain any funded account.** |
| POL proofs / round certificates / finality | 42 | partial | `pkg/chain/consensus_certificate.go:38-113`; `pol_follower.go:81-268` | Gate is fail-open: `markPublished()` fires on every failure branch in `pkg/networking/pol_publish.go:27-78`, so `CanExtendFromTip` always returns true |
| 3D mesh validation (parent cells) | 35 | prototype | `pkg/mesh3d/mesh3d.go:94-220` | `validateTransactionSignature` returns nil (`:244-259`); entanglement failures downgraded to warnings (`:186-188`); never touches ledger |
| Mining fraud quarantine (§8.3) | 35 | prototype | `pkg/mining/verifier.go:171-203,457-460,536-541` | `internal/miningsvc/miningsvc.go:258-261,500-502` defaults `Batches` to `acceptAllBatchValidator{}` returning nil ⇒ `Add` is **never** called in production |
| Rule-based submesh quarantine | 30 | prototype | `pkg/quarantine/quarantine_manager.go:39-63` | `IsQuarantined` never consulted on ingest (`cmd/qsdm/transaction/transaction.go:205-266`). `auto_recovery.go` (186 LOC) has zero non-test constructors. |
| Proof-of-Entanglement validation | 18 | stub | `pkg/consensus/poe.go:84-112`; `cmd/qsdm/transaction/transaction.go:236-246` | Self-documented no-op: node signs with its own process-local key then verifies its own signature. Parent cells counted, never checked. |
| BFT gossip vote authentication | 10 | stub | `pkg/chain/bft_wire.go:32-46`; `bft_executor.go:388-412` | No signature field at all. Any peer on `qsdm-bft` can forge prevotes/precommits for any validator. |
| Multi-node BFT quorum in running product | 8 | scaffolding | `pkg/chain/bft_presynthetic.go:31-54`; `main.go:1031,1096` | Local node casts prevotes AND precommits for every validator itself; validator set can never contain a peer; hook unreachable anyway |

*(Dynamic submeshes 45% — `pkg/submesh/dynamic.go:68-88`, `policy.go:9-13` no-ops without a profile file; `PriorityLevel` affects nothing.)*

---

### 3.2 Cryptography & Wallet — 74%

| Feature | % | State | Evidence | Gap |
|---|---|---|---|---|
| ML-DSA-87 (FIPS 204) sign/verify | 95 | production | `go.mod:22` circl v1.6.3; `pkg/crypto/dilithium_circl.go:56,120,167`; release tags `validator_only,dilithium_circl` (`qsdm-go.yml:143`) | No liboqs↔circl parity test runnable in checkout |
| 24-word recovery → deterministic key | 95 | production | `pkg/walletrecovery/recovery.go:49-133` (BIP-39 → HKDF-SHA256 → `mldsa87.NewKeyFromSeed`) | No golden-vector KAT pinning words→address across releases |
| Encrypted keystore v1 (PBKDF2-600k + AES-GCM) | 95 | production | `pkg/keystore/keystore.go:186-533`; 17 tests | Metadata unauthenticated by design (`:204-207`) |
| POST /wallet/submit-signed | 92 | production | `pkg/api/handlers.go:276,1336-1408`; `pkg/wallet/wallet.go:70-114`; re-verified at consensus `pkg/chain/wallet_transfer.go:41` | Nonce-0 fallthrough survives only in no-mempool test config (`handlers.go:1425-1429` hard-rejects on validators) |
| qsdmcli wallet (11 subcommands) | 92 | production | `cmd/qsdmcli/wallet.go:78-107,212-598` | Top-level help lists only 4 of 11 (`main.go:376-392`); no rollback test for `enable-recovery` timeout |
| Legacy wallet → on-chain recovery capsule | 90 | production | `pkg/walletrecovery/legacy_capsule.go:74-330`; `pkg/chain/recovery_capsule_state.go:110-153`; wired `v2wiring.go:666-667` | No capsule expiry/GC; no revoke/rotate action |
| Consensus-side txsig (ML-DSA path) | 68 | partial | `pkg/chain/txsig.go:16-30,98-116` | `RegisterKey`/`RegisterMLDSAKey`/`RegisterMLDSAKeyHex` have **zero** non-test callers; nothing in-tree emits `SigMLDSA`; `TxSigner.Sign` hardcodes ed25519 (`:48-57`) |
| Browser WASM wallet module | 65 | working | `wasm_modules/wallet/cmd/qsdm-wallet/main.go:66-319`; shipped `wallet.wasm` sha384 matches `wallet.js:223` | `crypto_test.go:1` is `//go:build ignore`; `walletcrypto/` has no test file; `wallet_test.go` tests unimported `walletcore` and self-skips; zero CI coverage |
| Browser wallet WebCrypto keystore + Send | 60 | partial | `deploy/landing/wallet.js:50-223`; `wallet.html:871-1268` | **Branch-local SRI regression** — see §4 conflict note. On `origin/main` and in production the hash is correct. |
| Batch ML-DSA signing | 50 | partial | `pkg/crypto/dilithium_circl.go:290-319`; `pkg/consensus/poe.go:60-66` | Zero production callers; claimed "10-100× speedup" never realised; unbounded goroutine spawn if wired |
| Server-custody /wallet/send | 40 | partial | `pkg/api/handlers.go:267,1128`; `pkg/wallet/wallet.go:175-177` | `ws.balance` is structurally 0 in production ⇒ any real send returns 500 |
| Zstd signature compression | 40 | partial | `pkg/crypto/signature_compression.go:11-39` | Provably grows ML-DSA sigs ~+0.3% (`pkg/crypto/benchmark_test.go:127-181`); no wire path uses it |
| Wallet balance sync from ledger | 25 | prototype | `pkg/wallet/wallet.go:152-160` | `SyncBalanceFromLedger` has zero non-test callers — test-only code path |
| Browser wallet 24-word recovery | 0 | absent | grep "recovery" in `deploy/landing/wallet.js` → 0 hits | Web users who clear site data lose keys irrecoverably |
| Cross-device account / vault sync | 0 | doc-only | `docs/docs/WALLET_ACCOUNT_SYNC.md` (self-labelled undelivered) | Entire service |

---

### 3.3 Storage & Networking — 67%

| Feature | % | State | Evidence | Gap |
|---|---|---|---|---|
| libp2p host + GossipSub (StrictSign) | 82 | production | `pkg/networking/libp2p.go:113-198,314-340`; `hostkey.go:1-128`; fatal-on-error `main.go:720-726` | TCP-only listen addrs (`:200-219`), no QUIC/relay/AutoNAT; no connmgr limits; **pkg/networking appears in zero CI `go test` invocations** |
| P2P wallet tx propagation (ingress/relay/dedupe) | 82 | production | `pkg/networking/tx_gossip.go:32-124`, `tx_gossip_relay.go:26-127`; `main.go:2261-2268,2486-2491` | `pkg/walletp2p` has **no test files**; relay dedupe is per-process only |
| TLS 1.3 / mTLS / ACME / cert-expiry gauge | 82 | production | `pkg/api/server.go:203-364`; `pkg/api/cert.go:17` | Final `else` starts plain HTTP with a warning (`:365-376`); `ConfigureMTLS` error discarded at `:280` |
| Ready() readiness probes | 80 | working | `pkg/api/handlers.go:611-644`; backends `sqlite.go:466`, `scylla.go:807`, `file_storage.go:98` | FileStorage `Ready()` is `os.Stat` while `GetBalance` returns `0,nil` (`file_storage.go:116-118`) — k8s sees "ready" on a node that can serve nothing |
| Secondary gossip relays (BFT/PoL/evidence/block/bridge) | 78 | working | `bft_relay.go:22-77`, `pol_relay.go:28-111`, `evidence_relay.go:34-96`; wired `main.go:1020,1119,1144,1166,1979` | JoinTopic failure leaves `bftRelay` nil ⇒ node runs as a mute validator, still reports healthy |
| Storage op metrics `qsdm_storage_op_total` | 76 | working | `pkg/monitoring/storage_op_metrics.go:31-139`; registered `prometheus_scrape.go:47` | `V041MigrationClampedRows` (`sqlite_v041.go:91`) has no caller; `sqlite_v041.go:84-85` names a nonexistent collector file |
| SQLite engine (WAL, Zstd, tx/balance tables) | 72 | working | `pkg/storage/sqlite.go:25-202,400-478` | AES key is compile-time literal `"0123456789abcdef…"` (`:206,:410`); balance updates outside the tx with failures downgraded to `log.Printf` (`:156-170`) → silent ledger divergence under `CHECK(balance>=0)` |
| API versioning + deprecation lifecycle | 72 | working | `pkg/api/versioning.go:33-187`; mounted `server.go:504` | `RegisterAPIVersion` has zero non-test callers; registry is a one-entry compile-time literal — entire sunset/410 machinery is test-only |
| v0.4.1 replay protection (ApplyTransferAtomic/GetNonce) | 70 | working | `pkg/storage/sqlite_v041.go:112-444`; handler `handlers.go:1493-1543` | **No `sqlite_v041_test.go` exists.** The load-bearing replay primitive has zero executing coverage; only a mock reimplementation at `handlers_test.go:181-193` |
| Bootstrap peer discovery | 68 | working | `pkg/networking/bootstrap.go:82-259`; wired `main.go:737-756` | `AllowedPeers` never populated in production; no config field; default `BootstrapPeers` is `[]` ⇒ out-of-box node runs isolated |
| SQLite→Scylla migration tool | 65 | working | `cmd/migrate/main.go:49-189`; `migrate_test.go:13` (runs in CI at `qsdm-go.yml:341`) | Does not migrate nonces — replay-protection state silently dropped |
| API rate limiting | 58 | partial | `pkg/api/security.go:100-300`; mounted `server.go:495` | `X-API-Key` is never validated but keys the bucket (`security.go:288-292`), and RateLimit runs *before* Auth ⇒ rotate the header, bypass the limit. `RoleRateLimiter` (157 LOC + 10 tests) never mounted despite `security.go:187-190` claiming it is. |
| ScyllaDB backend (schema/CRUD/MV/LWT) | 55 | partial | `pkg/storage/scylla.go:153-413,655-782` | `UPDATE balances SET balance = balance + ?` on a non-counter DOUBLE (`:472-481`) — CQL rejects it, failure swallowed at `:404-409`. CI never executes one write. |
| Peer reputation + PEX | 40 | partial | `pkg/networking/reputation.go:102-321`; `pex.go:47-352` | `IsBanned` has zero production callers; `NewPEXManager` called only from `pex_test.go` — 352 LOC + 18 tests of unreachable code |
| ScyllaDB v0.4.1 parity | 3 | stub | `pkg/storage/scylla.go:791-804` — literal `"not yet implemented"` ×2 | `balances` table has no `nonce` column; Scylla-backed nodes 500 on every signed transfer |

---

### 3.4 Mining Protocol V2 — 70%

| Feature | % | State | Evidence | Gap |
|---|---|---|---|---|
| On-chain enrollment + 10 CELL bond | 95 | production | `pkg/mining/fork.go:64`; `enrollment/validate.go:57-60`; `pkg/chain/enrollment_apply.go` (531 LOC); HTTP `handlers.go:399-419` | Registry is a JSON snapshot, not chain state — not covered by block StateRoot |
| Hashcash deferred-bond postage | 95 | production | `enrollment/deferred_bond.go:13-70`; enforced `validate.go:65-72` | 22-bit difficulty is a compile-time constant, not governance-tunable |
| Mining API surface (16 routes) | 90 | working | `pkg/api/handlers.go:334-443`; probes wired `main.go:1465,1610,1636-1642`; `v2wiring.go:662-678` | `/mining/work` serves a hardcoded fixture |
| Turing-or-newer GPU gate | 88 | working | `pkg/mining/attest/archcheck/archcheck.go:108-260`; `verifier.go:374-393`; `main.cu:321-322` | Operates on operator-supplied strings; real 7.5 floor lives in the miner's own binary |
| CUDA mining solver (real GPU kernel) | 88 | working | `cmd/qsdm-miner-cuda-solver/main.cu:207-247` (`__global__ solve_kernel`, 64-step SHA3 DAG walk, atomicCAS); nvcc sm_75/86/89/90 `build_miner_cuda.ps1:71-74`; host re-verify `cuda_solver.go:212-215` | No prebuilt binary committed; hard-errors on post-TC heights (`:142-144`) |
| qsdmminer-console | 88 | working | `cmd/qsdmminer-console/main.go` (74 KB), `v2.go`, `cuda_solver.go`, `enrollment_poller.go`; 8 test files | GPU identity entered by operator, never auto-detected from NVML |
| v2 proof verifier + FORK_V2 gate | 78 | working | `pkg/mining/verifier.go:323-553` (all 11 spec steps) | `forkV2Height` defaults `MaxUint64`; only non-main.go setter is a dev script (`start_local_validator.ps1:849`); release smoke log records `fork_v2_active:false`. Accepted proofs never enter a block. |
| nvidia-hmac-v1 attestation | 78 | working | `pkg/mining/attest/hmac/verifier.go:128-315`; wired `main.go:1470-1548` | Never invoked in shipped builds (v2 dormant). No hardware root of trust. NonceStore in-memory only. |
| On-chain slashing enforcement | 65 | partial | `pkg/chain/slash_apply.go:291-475`; routed `enrollment_aware_applier.go:167-176`; applier → `NewBlockProducer` `main.go:1264` | What it enforces is exploitable (see next row). Slash txs unauthenticated: `slashing/admit.go:72-106` checks shape only; empty keyring at `main.go:2262` lets gossip name an arbitrary Sender. |
| nvidia-cc-v1 (datacenter CC) | 55 | partial | Real verifier `attest/cc/verifier.go:140-481` + `roots.go` | Production wiring installs `cc.NewStubVerifier()` (`attest/production.go:198`), hard-rejecting everything unless `QSDM_CC_ROOTS_DIR` is set. No NVIDIA roots pinned anywhere. |
| Tensor-Core PoW fork | 55 | partial | `pkg/mining/pow/v2/*` byte-exact Go reference; dispatched `verifier.go:504-508` | No GPU implementation; CUDA solver refuses post-TC heights. `forkV2TCHeight` = MaxUint64, never set. |
| Deferred bond funded from earnings | 55 | partial | `enrollment/registry.go:433-465`; `pkg/chain/mining_reward.go:21-57` | `miningSvcCfg.RewardSink` set only when `soloDriver != nil` (`main.go:1599-1601`); sole minter is `blockdriver.go:717-726`. **On a multi-validator chain there is no funding source at all.** |
| Slash evidence verifiers | 45 | partial | `forgedattest/`, `doublemining/`, `freshnesscheat/`; registered `v2wiring.go:410-431` | 1-of-3 sound. freshnesscheat always rejects (`freshnesscheat.go:194-196` + `witness.go:112-129`). **forgedattest is unsound** — `forgedattest.go:219-227` returns `maxSlash()` on an unparseable bundle *before* the NodeID binding check at `:231`. |
| Live work generation + difficulty retargeting | 35 | prototype | `internal/miningsvc/miningsvc.go:413-425`; `cmd/qsdm/main.go:3415-3436` | Three hardcoded batches of `0xC0FFEE01`/`0xDEADBEEF`; `difficulty.Retarget` has only test callers |
| mesh3d CUDA kernels | 35 | prototype | `pkg/mesh3d/kernels/sha256_validate.cu:113-197` | `//go:build cgo && cuda`; no release script passes `-tags cuda`. Committed `mesh3d_kernels.dll` exports only 2 of 3 symbols — `mesh3d_runtime_version` (`cuda.go:277`) is absent ⇒ a tagged build cannot link. |

---

### 3.5 Tokenomics, Tasks, Staking, Governance & Bridge — 44%

| Feature | % | State | Evidence | Gap |
|---|---|---|---|---|
| Task action lifecycle (`qsdm/tasks/v1`, 8 actions) | 88 | production | `pkg/chain/task_state.go:20,256-321,376-447`; folded into state root `enrollment_aware_applier.go:311-330`; 15 tests | "unstake"/"withdraw" are aliases with no unbonding delay; escrow straddles two state trees with no cross-tree invariant |
| Edge-pool 70/15/15 settlement | 88 | production | `task_state.go:941-1085`; `pkg/edgepool/settlement.go:246-324`; tests assert 0.035/0.0075/0.0075 exactly | Legacy non-signed proofs fail closed; per-round caps hardcoded for 3 task IDs (`task_state.go:176-183`) |
| Consensus task catalog | 87 | production | `pkg/chain/task_catalog.go:111-359`; 8 tests | Capability kind accepts any matching string — no executable-capability allow-list |
| CELL emission schedule | 70 | working | `pkg/chain/emission.go:42-295`; 16 tests | Only paying consumer is `blockdriver.go:641`, gated behind `QSDM_SOLO_VALIDATOR_MODE` (`main.go:1554`) which **no deploy artifact sets**. `NextHalvingHeight` off-by-one (`:245-251`). |
| /mining/emission probe | 66 | working | `pkg/api/handlers_mining.go:167-246`; probe `main.go:3110-3141` | Pure schedule arithmetic, never reads AccountStore — reports emission that did not occur |
| Treasury payout signers (Tier 2) | 62 | working | `pkg/api/treasury_payout.go:80-201` (loopback-only, credential/redirect refusal); `cmd/qsdm/treasury_payout.go:23-59` | 1 of 3 documented tiers; both programs default OFF; `cmd/qsdm/treasury_payout.go` has no test file |
| /status tokenomics block | 58 | partial | `pkg/api/handlers_status.go:156-257` | Publishes a fabricated figure on default nodes (minting is solo-gated); **zero tests** reference `tokenomics`/`cap_dust`; surfaces only the 90M sub-cap, never 100M |
| On-chain governance param tuning | 50 | partial | `pkg/governance/chainparams/*` (~2,500 LOC, ~20+25 tests); `pkg/chain/gov_apply.go`; wired `v2wiring.go:458-524` | `GovernanceAuthorities` **never assigned outside tests** — `main.go:1210-1248` omits it ⇒ every gov tx rejects (`gov_apply.go:441-444`). Entire write path unreachable. |
| Validator delegation staking ledger | 24 | scaffolding | `pkg/chain/staking_ledger.go:78-191`; persisted `staking_persist.go` | `Delegate()`/`BeginUnbond()` have zero production callers; no tx type, no route ⇒ `ProcessCommittedHeight` (`main.go:1335`) iterates a permanently empty queue and `SlashDelegated` always slashes zero |
| Bridge (HTLC lock/redeem/refund + atomic swap) | 22 | prototype | `pkg/bridge/protocol.go:64-180`; wired `main.go:940-1024`; 28 tests | Four literal "In a real implementation, this would…" comments (`protocol.go:95,137,173`; `atomic_swap.go:93,128,156,183`). **No value ever moves** — no AccountStore reference anywhere. Secret is server-generated and echoed to the caller (`handlers_bridge.go:101`) ⇒ not a hashlock. |
| Snapshot token-weighted voting | 18 | prototype | `pkg/governance/snapshotvoting.go:88-107`; `voting.go:52-56` | `Vote(…, weight int, …)` takes weight as an untrusted caller argument. No signatures, no ledger stake, no chain anchoring. Reachable only from an interactive stdin CLI (`main.go:679-685`). |
| 100M CELL hard cap | 55 | partial | **Corrected.** Cap is real and enforced on the emission path: `pkg/chain/emission.go:39-46,71-74,162-177` (integer-exact, 16 tests); clamped at `blockdriver.go:633-634`. Genesis ceremony guard at `cmd/genesis-ceremony/main.go:77-87` is dry-run only. | No total-supply invariant in `AccountStore`; opening allocation is ~1e15 **CELL** (float64, `blockdriver.go:95` + `account.go:19`) pinned into the canonical state root at `main.go:2935-2951`; float64 balances destroy ~0.06 CELL/block. See gap #4. |
| Validators earn transaction fees | 8 | absent | `pkg/chain/account.go:230-245` debits `Amount+Fee`, credits only `Amount` | Fees are **burned**. No proposer credit, no fee pool, no consumer of `Block.TotalFees`. |
| 0% founder allocation | 5 | doc-only | `cmd/genesis-ceremony/main.go:8-33` self-disclaims as non-production; `VerifyBundle` refuses non-dry-run (`:348-353`) | The one real genesis guardrail attests to 1e15 CELL to a single account — the *wrong* invariant |
| 10% treasury / 48-month vesting | 0 | doc-only | grep "vesting" across `QSDM/source/**/*.go` → **0 hits** | Entire feature: no vault, no multisig, no cliff, no release schedule |

---

### 3.6 QSDM Hive Desktop Client — 68%

| Feature | % | State | Evidence | Gap |
|---|---|---|---|---|
| QSDM signer wallet UI | 90 | production | `renderer/features/settings/components/Accounts/QsdmWalletPanel.tsx:55-380` (1,173 LOC); 13 controllers each with `.spec.ts` | Zero renderer tests for the 1,173-line panel |
| Chromium wallet extension bridge | 90 | production | `main/services/qsdmWalletProviderBroker.ts:119-383` (9 RPC methods, exact-origin, per-action approvals); wired `main/main.ts:607,631-640` | Approvals use native `dialog.showMessageBox` — no in-app tx preview or approval history |
| Signed task actions (ML-DSA envelope) | 90 | production | `main/services/qsdmTaskActions.ts:456-600`; `qsdmTaskActionSigner.ts:200-340` | Shells out to bundled `qsdmcli`; fails closed with no in-app fallback. Linux `qsdmcli` not checked in. |
| Mother Hive relay pairing / federation | 88 | production | `main/services/qsdmMotherHiveRelayConfig.ts:382-697` (allowlist decode, 3 modes, atomic 0600 writes) | No test for the renderer pairing form |
| Updater w/ ML-DSA-87 signed manifest | 88 | production | `main/AppUpdater.ts:41-90`; `qsdmReleaseManifest.ts:102-493`; `hiveVersionPolicy.ts:92-112` | Authenticode opt-in only (`verifyUpdateCodeSignature:false`) |
| Miner system task + bundled CUDA solver | 86 | production | `qsdmSystemTasks.ts:2816-2898,4213-4285`; `qsdmMinerEnrollment.ts:141-357` (real nvidia-smi CSV parse) | Linux miner binaries absent from tree; fails closed with no CPU fallback |
| Edge worker system tasks | 74 | working | `qsdmSystemTasks.ts:947-1275` (HMAC canonicalization, settlement binding, real sha256/buffer/GPU proofs) | Targets `127.0.0.1:7740` but the bundled `qsdm-edge-control` is never launched by the app |
| Packaging & release pipeline | 74 | production | `electron-builder.json` extraResources; real artifact `release/build/qsdm-hive-1.4.3-win-x64.exe` (123,412,234 bytes) + evidence JSONs | `.gitignore:30` is `native/` — **zero native binaries tracked**, so no platform packages from a clean clone. macOS config is dead (`package-host.js` rejects darwin). |
| Mother Hive workspace panel | 72 | working | `renderer/features/mother-hive/components/MotherHiveView.tsx` (980 LOC, 9 live sections) | No renderer test; renders the all-zero base object (`qsdmSystemTasks.ts:3750-3796`) because the relay is never started |
| Sky Fang wallet-link task | 70 | working | `main/services/skyFangWalletLink.ts` (145 LOC, 3 exports); deep link `handleDeepLinks.ts:230` | No `.spec.ts`; counterparty exists only as a reference `.java` file in `apps/game-integration` |
| Application Compute Gateway (127.0.0.1:7742) | 62 | working | `qsdmSystemTasks.ts:1277-1625` (~390 LOC template string: Bearer + timingSafeEqual, 120/min, 16 KiB cap) | Is an authenticating reverse proxy, not a compute gateway (`:1537-1560` forwards verbatim). No direct test. Proxies to a relay the app never starts. |
| IPC payload validation | 60 | working | `main/initHandlers.ts:239-252` universal wrapper; `main/security/ipcValidation.ts` (623 LOC) | 32 `case` arms vs **172** Endpoints enum members; `default: break` (`:620-621`) ⇒ ~82% of the IPC surface unvalidated |
| Notifications center | 55 | partial | `renderer/features/notifications/*`; routed `AppRoutes.tsx:60-62` | 10 CTAs are `label="implement me"` + `throw new Error('Function not implemented.')` (`useAppNotifications.tsx:196,204,212,220,228,236,244,253,261,269`) |
| Task Studio | 50 | partial | `TaskStudio.tsx:20-115`; controller `manageQsdmTaskCatalog.ts:30-92` | Runtime hardcoded (`:91-96`), selector is a disabled one-option `<select>`; published tasks then execute the fake-proof stub |
| Third-party integrations (IPFS/Arweave/S3) | 50 | partial | `services/ipfs.ts` (6-gateway fallback, spec passes); `arweave.ts`; `aws-config.ts` | `main/services/analytics.ts:1-4` `trackEvent` is an empty no-op; add-ons list is static "intentionally disabled" text |
| QSDM-native generic task runtime | 20 | stub | `main/controllers/getTaskSource.ts:12-81` — `const proof = 'qsdm-native-proof:' + taskId + ':' + Date.now()` every 60s (`:64-72`); returned for all capability tasks (`:123`) and manifest-less tasks (`:130`) | **Every non-system catalog task submits a fabricated timestamp as its proof.** WASM runtime explicitly unimplemented (`qsdmTaskCatalogRuntime.ts:41-42`). |
| Legacy Koii/K2 chain surface | 20 | stub | `vendor/qsdm-chain/web3.ts:261,310,321,336` (getBalance→0, blockhash literal, send throws); `fetchKPLList.ts:8,14` → `[]` | Deliberately deprecated but still rendered for legacy profiles |
| 3D dashboard / topology panels | 0 | absent | No three/d3/deck.gl/cytoscape/vis in `package.json`; zero component hits | Entire feature |
| Dead routes (/rewards, /my-tokens, /history, security/notifications settings) | 0 | stub | `AppRoutes.tsx:50-56` literal `<div>Rewards</div>`; `settingsRoutesConfig.tsx:48-54,82` | 5 declared routes with no implementation |

---

### 3.7 Edge Compute Pool & Federation — 69%

| Feature | % | State | Evidence | Gap |
|---|---|---|---|---|
| Agent→Relay pool protocol | 95 | production | `pkg/edgepool/coordinator.go:237-521`; `agent.go:162-363`; wired `cmd/qsdm-edge-agent/main.go:265-405` | Lease TTL Relay-local; no cross-Relay failover |
| Walletless agents / fixed-algorithm execution | 95 | production | `pkg/edgepool/work.go:19-83,143-178`; GPU helper argv fixed + SHA-256 pinned (`:264-287`) | None for the stated claim |
| Separate Agent/Mother HMAC creds + replay protection | 95 | production | `coordinator.go:303-877`; `protocol.go:361-382`; nonce map `:784-818` | Nonce cache in-memory ⇒ narrow replay slot after restart |
| Durable receipts journal | 88 | production | `coordinator.go:999-1118` (JSONL + fsync, boot revalidation, atomic temp+rename) | Identity hash is **unkeyed** SHA-256 over public fields (`:1104-1118`) — detects corruption, not tampering. Journal never rotated. |
| Multi-Mother tenancy | 86 | production | `pkg/edgepool/mother_context.go:92-354`; isolation across jobs/receipts/settlement | Federation tenants bypass the registry entirely — never registered, invisible to `ListMotherTenants`, and **cannot be revoked** (`:310-313` pattern rejects `federation-*`) |
| Application Compute Gateway (durable job queue) | 85 | working | `pkg/edgepool/compute_gateway.go:42-742`; proxied by Hive; consumed by `qsdmVirtualComputeRuntime.ts:90-143` | Only 3 fixed deterministic benchmarks — a capacity-proof harness, not general compute |
| Resource caps / Relay policy | 82 | production | `work.go:23-31`; `coordinator.go:142-172,722-777,895-901` | Zero OS-level throttle on any platform: `service_linux.go` sets no CPUQuota/MemoryMax; `background_windows.go:17-24` sets no priority class |
| Edge Control operator app | 82 | production | `cmd/qsdm-edge-control/controller.go` (754 LOC); loopback+CSRF `server.go:50-286`; 19 tests | `service_other.go:6-28` — all six service functions return hardcoded errors on Windows/macOS, and **Windows is the primary shipped platform** |
| Relay ML-DSA-87 settlement proofs | 92 | production | `settlement.go:224-684` (deterministic proof ID, write-once binding, ack/consume, self-verify) | One ContributorWallet per binding |
| Core consensus settlement | 72 | working | `pkg/chain/task_state.go:942-1085`; 3 tests | **Unreachable in the shipped product**: gated on 3 hardcoded task IDs (`:176-183`) AND `authorized_relay_ids` (`:1036-1046`), which appears exactly once tree-wide — the struct field at `task_catalog.go:72`. No genesis/config populates it ⇒ zero payouts, proofs never acked, receipts re-served forever. |
| Edge federation | 65 | working | `pkg/edgepool/federation.go:52-190`; `pairing.go:63-113` | Mandatory 24h re-pairing mints a *new* tenant ID (`mother_context.go:356-359`), orphaning bindings/proofs/receipts with no migration. No revocation path. |
| Quotas | 45 | partial | Relay-local caps `compute_gateway.go:22-24`; `coordinator.go:161-172` | Zero hits for `quota` across `pkg/edgepool`, `pkg/chain`, `cmd/qsdm-edge-control`. No per-wallet/per-IP/per-workload/Core-enforced quota exists. |
| Per-Agent payout attribution | 20 | partial | `protocol.go:236-242`; Hive default `qsdmSystemTasks.ts:970` `contributorWallet = env || sender` | Agents have no on-chain payout identity; 70% goes to one operator wallet regardless of who did the work |
| Core lease / escrow / reservation | 0 | absent | No `ComputeOffer`/`ComputeLeaseIntent`/reservation/escrow type for edge compute anywhere in `QSDM/source` | No pre-dispatch funding, no dispute state |
| Marketplace settlement / provider discovery | 0 | absent | Zero `marketplace` hits in `QSDM/source` or Hive `src/` | Manual QSDM-EDGE-2 invitation paste only |

---

### 3.8 Operator Stack, Trust & Attestation — 67%

| Feature | % | State | Evidence | Gap |
|---|---|---|---|---|
| trustcheck black-box verifier | 93 | production | `cmd/trustcheck/main.go:1-204`; cross-compiled 5-cell matrix `release-container.yml:178`, stripped `:225`, signed; 30-min live probe `trustcheck-external.yml:44` | Shape/consistency checks only — cannot detect that `gpu_available` is a server-side constant |
| Prometheus metrics + alert rules | 88 | production | `pkg/monitoring/prometheus_scrape.go:143-150`; `nvidia_lock_metrics.go:5-48`; `deploy/prometheus/alerts_qsdm.example.yml:25-121` + promtool fixtures | No persist-error gauge exposed; no gate-enabled/disabled metric |
| NGC proof ingest pipeline | 84 | production | `pkg/api/handlers.go:2063-2161`; `pkg/monitoring/ngc_proofs.go:39-79`; `ngc_nonce.go:45-72` | Both nonce+HMAC (`ngc_proofs.go:58-60`) and JSONL persistence (`main.go:612`, key absent from all configs) are inert in the shipped posture |
| qsdm-home-gateway + relay tunnel | 78 | working | `cmd/qsdm-home-gateway/gateway.go:49-121`; `pkg/tunnel/client.go:115`, `server.go:227-358`; 19 tunnel tests | Only 3 tests on the internet-facing allowlist; `smoke_release_windows.ps1:168-201` asserts on a binary `build_release.ps1` never builds |
| qsdm-local-gui | 78 | working | `cmd/qsdm-local-gui/main.go:39-260,985`; e2e-exercised by `smoke_release_windows.ps1:95-150` | Smoke is release-time manual, not CI; 8 process-control routes uncovered |
| watch_local_stack.ps1 | 72 | working | `QSDM/scripts/watch_local_stack.ps1:345-491` | Supervises only validator + gateway; signers/attester/GUI/sidecar are never restarted. 511 LOC untested. |
| tray-monitor | 70 | working | `apps/qsdm-tray-monitor/Program.cs:185-267,915,1109-1131` | Zero tests; `status.json` has **no consumer** repo-wide — write-only artifact |
| NVIDIA-lock HTTP gate | 70 | working | `pkg/monitoring/nvidia_lock.go:21`; `pkg/api/handlers.go:110` | Only 7 call sites; **misses `/wallet/submit-signed`, `/mining/submit`, `/mining/enroll`, `/faucet/claim`, `/referrals/*`, `/tasks/actions/submit-signed`** — i.e. every production write path. Defaults off. |
| start_local_validator.ps1 | 70 | working | `QSDM/scripts/start_local_validator.ps1` (974 LOC) | Sets no `QSDM_NGC_*`/`QSDM_NVIDIA_LOCK_*` at all — the operator's own launcher never enables the attestation gate. No tests. |
| Trust transparency API | 68 | working | `pkg/api/handlers_trust.go:179-477`; wired `main.go:2385-2412`; 29 tests | `GPUAvailable:true` and `NGCHMACOK:true` are literals (`:530-531`, `:586-587`) — and on the default ingest path no HMAC is checked at all. Local sidecars inflate both `attested` and `total_public` (`:205-220,393-403`). |
| Treasury/referral/faucet signer health | 65 | partial | `start_treasury_signer.ps1:1-53`; `test_treasury_readiness.ps1:183` | Binary `/healthz` liveness only; nothing restarts a dead signer |
| NVIDIA-lock P2P gate | 65 | working | `pkg/monitoring/nvidia_p2p_gate.go:8-22`; enforced `transaction/transaction.go:137-139,251-253` | Not a peer gate: `Allows()` takes no peer identity and reads *this* node's own proof ring. Runs *after* consensus validation. Doubly conditional, never enabled. |
| NGC attestation sidecar | 55 | partial | `apps/qsdm-nvidia-ngc/validator_phase1.py:31-345` | `cuda_proof_hash` comes from `simulated_cuda_pow` — a 50k-iteration **CPU** SHA-256 chain (`:62`); `architecture` is a hardcoded literal (`:371`) that `nvidia_lock.go:45` substring-matches. No device cert chain, no CC quote. |
| Audit transparency API | 50 | partial | `pkg/api/handlers_audit.go:201-314`; `handlers_audit_badge.go` | Backed by `pkg/audit/checklist.go:208` — 88 hand-written literals, 85 `StatusPassed`. Live API returns exactly `{passed:84,pending:4,total:88,score:95.45}`. `UpdateStatus` has no caller in the running product. |
| Cross-peer attestation aggregation | 25 | prototype | `pkg/api/trust_peer_provider.go:13-106` | Returns zero-valued rows; no protocol message carries a peer's attestation. Numerator structurally capped at local sources. |

---

### 3.9 Website, SDKs, Tooling & Release Engineering — 72%

| Feature | % | State | Evidence | Gap |
|---|---|---|---|---|
| Sigstore/cosign signing + SPDX SBOM | 92 | production | `release-container.yml:460-602`; last 3 releases carry 29 `.sig` + 29 `.pem` pairs + `qsdm-source-sbom.spdx.json` + per-image attestations | No self-verifying `cosign verify` step; download page never mentions cosign |
| Docs SPA (102 entries) | 85 | working | `deploy/landing/docs/docs.js:35-628`; all 102 `repoPath` entries verified on disk and tracked; markdown-it SRI verified | Bodies fetched at runtime from `raw.githubusercontent.com/.../main/` — hard dependency on GitHub + repo publicness; branch hardcoded to `main`; not SEO-indexable |
| Explorer + chain status page | 85 | working | `explorer.html:293-457` (Promise.allSettled, escapeHTML, 15s refresh); all 4 endpoints verified registered | No tests; depends on `/mining/emission` + `/mining/blocks`, both absent from `openapi.yaml` |
| Download page + artifact hosting | 85 | working | `download.html:263-338`; all advertised artifacts return HTTP 206 on live probe; `publish_hive_dual_platform_release.sh` treats 12 artifacts as immutable | No CI check binding `download.html` versions to published artifacts; `downloads/` untracked |
| Trust + public-audit pages | 82 | working | `audit.html:809-877` (URL-persisted filters, `#row=` permalinks); `trust.html:314-336` | Audit data is a compile-time constant (see §3.8); no front-end tests |
| CI workflows | 80 | production | 15 workflows, all SHA-pinned (`check_workflow_action_pins.py` passes); `validate-deploy.yml` runs kubeconform/promtool/amtool/runbook/sitemap/strip gates | **`validate-deploy.yml` has been red on `main` since 2026-08-05** (run 31039485744, sitemap-freshness); `trustcheck-external.yml` failed 3 of last 6. No coverage gate, no OpenAPI lint, no SRI check, no link checker. `QSDM/tests/` invoked by zero workflows. |
| Marketing site / sitemap / security.txt | 80 | working | 13 pages + `assets/site.css`; RFC 9116 `security.txt`; `check_sitemap_freshness.py` in CI | `privacy.html` and `support.html` are **untracked in git** yet required by `_install_docs_site.sh:53-60`; `index.html:490-502` ships two wrong code samples (`status.height` vs `chain_tip`; `qsdm.New`/`client.Status` don't exist) |
| Browser wallet (site side) | 75 | working | `wallet.js:222-231` wasm SRI verified; production serves the correct `wallet.js` sha384 | **Branch-local regression**: HEAD's `wallet.html:872` pins a stale hash (introduced by `b2fe934`, which also deleted the `wallet-provider.js` tag). Ship risk, not current prod state. No CI SRI guard. |
| Go test-suite breadth | 70 | working | 435 `*_test.go`, 3,083 `func Test`, 33 benchmarks | 0 fuzz targets in a crypto/consensus codebase; no coverage measurement anywhere; `QSDM/tests/` is dead code outside the module; zero tests for any landing-site JS |
| Docker + Kubernetes manifests | 65 | partial | 3 Dockerfiles, 12 kubeconform-validated manifests incl. 8 NetworkPolicies | Image refs are dead: `deployment.yaml:23`/`statefulset.yaml:24` `qsdm:latest`, `miner-daemonset.yaml:53`, `validator-statefulset.yaml:58` — CI publishes `ghcr.io/<owner>/qsdm{,-validator,-miner}`. Production actually deploys via `.deploy_stagev.sh` SSH binary swap. |
| OpenAPI accuracy vs routes | 60 | partial | 2,053 lines, 35 paths; **0 phantom routes**; correctly documents the 410 Gone on `/wallet/mint` | 44 of 79 registered routes undocumented, including `/mining/emission` and `/mining/blocks` that the site's own explorer depends on |
| Code signing (SignPath/Authenticode) | 50 | partial | `.signpath/*.xml`; `qsdm-hive-windows.yml:262-315` complete job | Gated on `vars.SIGNPATH_ENABLED` (`:265`) which can never be true — SignPath Foundation application **declined** (`CODE_SIGNING_POLICY.md:8-14`). ML-DSA substitute needs a DPAPI key on one workstation (`new_hive_release_manifest.ps1:36`). |
| Go SDK | 45 | partial | `sdk/go/client.go` (14.7 KB, 9 tests) | Not `go get`-able: module is `github.com/blackbeardONE/QSDM` but `go.mod` lives at `QSDM/source/`; no root `go.mod`, no vanity meta tag. Covers ~7 of 79 routes. |
| JavaScript SDK | 45 | partial | `sdk/javascript/qsdm.js` (10.1 KB, 17 tests); npm provenance genuine | npm has **only 0.3.0**; the `sdk-js-v0.3.1` publish run FAILED (run 27145442319) and was never retried. Published tarball calls `/api/v1/transaction/{id}` (singular) — a permanently-404 route. |
| OpenAPI publication surface | 45 | partial | `docs/docs/API_REFERENCE.md`; `api.html` (accurate where it speaks) | `openapi.yaml` is served by nothing — zero `openapi|swagger` hits in `pkg/api`, zero in the Caddyfile, zero in landing pages. It is a repo file, not an API artifact. |

---

## 4. Documentation Overclaims

Every entry: doc claim → contradicting source.

| # | Doc file | Claim | Contradicting source |
|---|---|---|---|
| 1 | `docs/docs/CELL_TOKENOMICS.md:43,189` | "Treasury vesting: linear over 48 months, **enforced on-chain, locked at genesis**" | grep `vesting` across `QSDM/source/**/*.go` → **0 hits**. Directly contradicted by `docs/docs/TREASURY_POLICY.md:42` ("QSDM does not yet ship the Tier 0 multisig and vesting contract"). Two shipped docs contradict each other on a SevCritical claim. |
| 2 | `docs/docs/CELL_TOKENOMICS.md:41-44` | 100M total supply, hard cap | No cap enforcement anywhere; `internal/blockdriver/blockdriver.go:95` `DefaultFunderBalance = 1e15` pinned into the genesis state root at `cmd/qsdm/main.go:2935` — 10,000,000× the claimed total |
| 3 | `docs/docs/CELL_TOKENOMICS.md` + scope | "Validators earn fees only" | `pkg/chain/account.go:230-245` debits `Amount+Fee` and credits only `Amount`. Fees are burned; validators earn nothing. |
| 4 | `docs/docs/ARCHITECTURE_EXPLAINED.md:94-104` | PoE validates by "checking parent cells, verifying signatures, ensuring mesh connectivity" | `pkg/consensus/poe.go:84-112` — parent cells counted not checked (self-documented as "only a plausibility guard"); signature is self-verified against the node's own key; no connectivity check |
| 5 | `docs/docs/PHASE3_IMPLEMENTATION.md:6,15-17` | "3D Mesh Validation using **Rust and CUDA**", "ensures cryptographic rules are met" | No Rust in `pkg/mesh3d`; CUDA behind `//go:build cuda` that no release script sets (`cuda_stub.go:14-16`); `mesh3d.go:244-259` signature validation is a placeholder returning nil |
| 6 | `docs/docs/PHASE3_IMPLEMENTATION.md:7,21-22,9,44` | Quarantine "isolates submeshes"; "manual voting mechanisms for quarantines" via governance CLI | `IsQuarantined` never consulted before accept/store (`cmd/qsdm/transaction/transaction.go:205-266`); zero quarantine references in `pkg/governance` |
| 7 | `docs/docs/MINING_PROTOCOL_V2.md:973,1084` | freshness-cheat slashing "Shipped" | `freshnesscheat.go:194-196` + `witness.go:112-129` — `RejectAllWitness` is the production default (`v2wiring.go:426`); every such slash is rejected by design |
| 8 | `docs/docs/MINING_PROTOCOL_V2.md §3.2` | nvidia-cc-v1 is a live attestation path | `pkg/mining/attest/production.go:198` installs `cc.NewStubVerifier()`; no NVIDIA roots pinned in the repo |
| 9 | `docs/docs/MINING_PROTOCOL` (WS_e / retargeting) | Per-epoch mesh3d work set + retargeting engine | `internal/miningsvc/miningsvc.go:413-425` static WorkSet & difficulty; `cmd/qsdm/main.go:3415-3436` literal `0xC0FFEE01`/`0xDEADBEEF`; `difficulty.Retarget` has only test callers |
| 10 | `docs/docs/archive/ALL_PHASES_COMPLETE.md:65` | "ScyllaDB Support — 100%" | `pkg/storage/scylla.go:791-804` two literal "not yet implemented" stubs; `balances` table has no `nonce` column |
| 11 | `docs/docs/SCYLLA_MIGRATION.md:24-35` | `PRIMARY KEY (id)`, MV `PRIMARY KEY (tx_id, id)` | Code creates `PRIMARY KEY (id, timestamp)` (`scylla.go:223`) and `(tx_id, id, timestamp)` (`:281`) — an operator following the doc builds a keyspace the DDL cannot match |
| 12 | `docs/docs/ADDITIONAL_TOOLS_COMPLETE.md:12`, `FEATURE_ENHANCEMENTS_COMPLETE.md:103` | "Network Topology Visualization — Interactive frontend visualization ✔" | Zero 3D/graph dependencies in Hive `package.json`; no such component anywhere |
| 13 | `docs/docs/API_REFERENCE.md:351-362` | `import "github.com/blackbeardONE/QSDM/sdk/go"`, "a single `go get` brings both" | No root `go.mod`; module path unresolvable. `deploy/landing/index.html:496-502` compounds with non-existent `qsdm.New` / `client.Status`. |
| 14 | `docs/docs/API_REFERENCE.md:407-411` | "refer to `openapi.yaml` for the machine-readable spec" | Served by nothing (0 `openapi` hits in `pkg/api`, Caddyfile, or landing); documents 35 of 79 routes |
| 15 | `docs/docs/WEB_WALLET.md` | Working self-custody browser wallet | Silent on the SRI rotation requirement; the branch under audit ships a stale `wallet.html:872` hash that blocks `wallet.js` entirely |
| 16 | `pkg/api/middleware.go:425` + `handlers_audit.go:227` | "the **runtime-verified** audit score" | `pkg/audit/checklist.go:208` — 88 hand-written literals, 85 `StatusPassed`; only mutator's sole caller is the offline `cmd/auditreport` |
| 17 | `pkg/audit/checklist.go:246` (bridge-03, `StatusPassed`) | "FeeBasisPoints=30 = 0.3%, distribution=70/20/10 validators/treasury/burn" | `pkg/bridge/fees.go:19-26` has no `FeeBasisPoints` field and no distribution; values are BaseFee 0.01 / PercentageFee 0.001 |
| 18 | `pkg/api/security.go:187-190` | "both middlewares are mounted in series" (RoleRateLimiter) | `setupMiddleware` (`server.go:482-521`) mounts only `s.rateLimiter` |
| 19 | `pkg/storage/file_storage.go:172` | "the SQLite v0.4.1 and **Scylla** backends do the real CAS + atomic debit" | Contradicted 620 lines away at `scylla.go:795-804`. Same at `cmd/qsdm/main.go:290-297`. |
| 20 | `pkg/storage/sqlite_v041.go:84-85` | Clamped gauge collected by `pkg/monitoring/storage_metrics.go` | That file does not exist; `V041MigrationClampedRows` has no caller |
| 21 | `pkg/chain/enrollment_aware_applier.go:53-56` | "Pre-seal BFT … works end-to-end" | `pkg/chain/block.go:287-290` rejects any applier that is not `*AccountStore`; production applier *is* `*EnrollmentAwareApplier` |
| 22 | `pkg/crypto/dilithium.go:446`, `dilithium_circl.go:240-241`, `pkg/wallet/wallet.go:256` | Zstd gives "~50% reduction (4.6 KB → 2.3 KB)" | `pkg/crypto/benchmark_test.go:127-181` states output is **larger** (~+0.3%); assertion relaxed to ≤110% |
| 23 | `pkg/crypto/dilithium_circl.go:279-283`, `pkg/consensus/poe.go:60` | "measured 10-100× speedup" from batch signing | Zero production callers of `SignBatchOptimized` |
| 24 | `docs/docs/EDGE_FEDERATION.md` "Abuse And Failure Controls" | "Per-wallet, per-Relay, per-IP, and per-workload quotas limit floods" | Zero `quota` hits across `pkg/edgepool`, `pkg/chain`, `cmd/qsdm-edge-control` |
| 25 | `deploy/landing/wallet.html:862-870` | SRI values "rotated automatically whenever the Go toolchain rebuilds wallet.wasm" | `scripts/build_wallet_wasm.sh:148-171` is manual-invocation-only; no workflow greps for `integrity`/`sha384` |
| 26 | `deploy/README.md:3` | "Kubernetes (Production)" | Production is systemd + `.deploy_stagev.sh` manual SSH binary swap on one VPS; k8s image refs are unpublishable |
| 27 | `QSDM/tests/README.md` | Presents the directory as the test suite | Outside the `go.mod`, hardcoded `localhost:8080-8085`, referenced by zero workflows |
| 28 | `deploy/landing/security.txt:3-8` | "identical byte-for-byte" to `.well-known/security.txt` | `diff` shows two differing regions |
| 29 | `deploy/landing/download.html:306` | Wallet extension "Chrome Web Store review in progress" | No store listing exists; live download button on an unverifiable claim |
| 30 | `sdk/javascript/package.json:4` | "Feature parity with sdk/go" | True only trivially — both expose ~10% of the API surface |

**Honest docs worth crediting:** `docs/docs/TREASURY_POLICY.md:42,268`, `docs/docs/NVIDIA_LOCK_CONSENSUS_SCOPE.md:24-28`, `docs/docs/CODE_SIGNING_POLICY.md:8-14`, `docs/docs/EDGE_FEDERATION.md:3`, `docs/docs/WALLET_RECOVERY.md`, `docs/docs/P2P_WALLET_TX_INGRESS.md`, `docs/docs/openapi.yaml:737-798`, and `internal/blockdriver/blockdriver.go:12-42`. These accurately disclose their own gaps.

---

## 5. Genuinely Production-Ready

Implemented, wired into the running product, tested, and sound:

1. **ML-DSA-87 crypto core** — `pkg/crypto/dilithium_circl.go` (circl v1.6.3, FIPS 204), `pkg/keystore` (PBKDF2-600k + AES-256-GCM, 17 tests), `pkg/walletrecovery` (BIP-39 → HKDF → `NewKeyFromSeed`, 9 tests). All executed and passing.
2. **`POST /api/v1/wallet/submit-signed` self-custody path** — `pkg/api/handlers.go:1246-1408` + re-verification at consensus `pkg/chain/wallet_transfer.go:41`. The only working transfer path, and it is correct.
3. **`qsdmcli wallet`** — 11 real subcommands, passphrase-file-not-argv, `.bak` on rewrite, cross-checked address derivation.
4. **CELL emission arithmetic** — `pkg/chain/emission.go` (integer-only, 16 tests, cap convergence, halving edges). The *calculator* is correct; only its wiring is gated.
5. **`qsdm/tasks/v1` engine + catalog + edge-pool settlement math** — `pkg/chain/task_state.go`, `task_catalog.go`, `pkg/edgepool/settlement.go`. Atomic cross-store rollback, global replay ledgers, fail-closed on unverifiable proofs, dust-exact splits, folded into the state root.
6. **`pkg/edgepool` Agent→Relay→Mother protocol** — HMAC domain separation, single-use nonces, tamper-resistant receipt journal with boot revalidation, per-tenant isolation, restart recovery. 39 passing tests.
7. **CUDA mining solver** — `cmd/qsdm-miner-cuda-solver/main.cu` real `__global__` SHA3 DAG-walk kernel, nvcc sm_75/86/89/90, `--self-test` gates the build, host re-verifies every GPU answer.
8. **Enrollment + Hashcash postage** — `pkg/mining/enrollment/*`, bit-exact difficulty check, real stake escrow/refund via `DebitAndBumpNonce`/`Credit`.
9. **Release supply chain** — cosign keyless signing of every binary + SHA256SUMS + SBOM, SPDX attestations on 3 images, all GitHub Actions SHA-pinned, npm provenance. Verified against live GitHub releases.
10. **`trustcheck`** — cross-compiled, stripped, signed, and probed against production every 30 minutes. Best-shipped artifact in the repo.
11. **Hive signer wallet + extension bridge + signed task actions + ML-DSA-verified updater** — 441 passing Jest tests, exact-origin permissions, per-action approvals, atomic 0600 writes.

---

## 6. Prototype / Stub Despite Being Documented

1. **Multi-node BFT consensus** — single-process vote simulation (`pkg/chain/bft_presynthetic.go:31-54`), unsigned wire messages (`bft_wire.go:32-46`), validator set of 2 hardcoded entries. **8%**.
2. **Proof-of-Entanglement** — node verifies its own signature (`cmd/qsdm/transaction/transaction.go:236-246`). **18%**.
3. **100M supply cap / 10% vesting / 0% founder / validator fee revenue** — 55% *(corrected from 12%)* / **0%** / 5% / 8%. The emission cap is genuine; what is missing is an `AccountStore` supply invariant, a sane genesis allocation (currently ~1e15 float64 CELL), and integer money units. Vesting has **zero** source hits.
4. **Cross-chain bridge** — four "In a real implementation…" comments; no value ever moves; server-generated "hashlock" echoed to the caller. **22%**.
5. **Token-weighted governance voting** — `Vote(…, weight int, …)` takes weight as an untrusted argument (`snapshotvoting.go:88-107`). **18%**.
6. **Validator delegation staking** — `Delegate()`/`BeginUnbond()` have zero production callers. **24%**.
7. **ScyllaDB v0.4.1 parity** — two literal `"not yet implemented"` returns. **3%**.
8. **Hive QSDM-native task runtime** — every non-system task submits `'qsdm-native-proof:' + taskId + ':' + Date.now()`. **20%**.
9. **NGC "GPU attestation"** — `simulated_cuda_pow` is a CPU SHA-256 chain; `architecture` is a hardcoded literal. **55%** (the transport is real; the attestation is not).
10. **Audit transparency API** — an 88-item Go source literal served as a "runtime-verified" score. **50%**.
11. **Live mining work + difficulty retargeting** — three hardcoded `0xDEADBEEF` batches at a difficulty that never moves. **35%**.
12. **Core lease/escrow + marketplace discovery + 3D topology panels + browser 24-word recovery + cross-device vault sync** — **0%** each.
13. **Dead-but-tested code** (compiles, has tests, unreachable): `pkg/api/ratelimit_roles.go` (10 tests), `pkg/networking/pex.go` (18 tests), `pkg/networking/optimizer.go`, `pkg/storage/query_optimizer.go`, `pkg/quarantine/auto_recovery.go`, `pkg/reputation/*`, `pkg/governance/{executor,multisig}.go` (13 tests).

---

## 7. Top 10 Gaps to Close (ordered by impact on shipping)

### 1. Anyone on the tx-gossip topic can drain any funded account
`cmd/qsdm/main.go:2262` builds `chain.NewSigVerifier()` with an empty keyring; `RegisterKey`/`RegisterMLDSAKey` have **zero** non-test callers. `SigVerifier.Verify` dispatches on the attacker-controlled `stx.Algorithm` (`pkg/chain/txsig.go:196-203`) and the ed25519 branch (`:206-229`) does `if hasKey { compare }` — with an empty keyring it returns nil for any (sender, attacker-key, attacker-sig) triple. Downstream `poolvalidator.go:103-112` and `account.go:216-247` check only nonce and balance.
**Fix:** make the ed25519 branch bind `sender == derived(publicKey)` exactly as the ML-DSA branch does at `txsig.go:249-252`, and reject unknown algorithms. ~10 lines. **Do this first.**

### 2. Forged-attestation slashing lets an attacker steal any miner's bond
`pkg/mining/slashing/forgedattest/forgedattest.go:194-282`: the "offence" is "the HMAC does not verify", but nothing binds the evidence proof to the accused — proofs never enter a block and carry no victim signature. `:219-227` returns `maxSlash()` on an unparseable bundle *before* the `bundle.NodeID == p.NodeID` check at `:231`. `pkg/chain/slash_apply.go:361-425` then debits the full bond and credits the attacker up to 50%. The happy-path test `slash_forgedattest_e2e_test.go:120-175` literally constructs the attack.
**Fix:** require a victim-signed proof or an on-chain proof record before any forged-attestation slash; move the NodeID binding check above the parse-failure return; add signature authentication to `slashing/admit.go:72-106`.

### 3. Block production is unreachable in consensus-enabled mode
`pkg/chain/block.go:287-290` returns `ErrPreSealRequiresAccountStore` unless the applier is exactly `*AccountStore`; production passes `*chain.EnrollmentAwareApplier` (`cmd/qsdm/main.go:1265`). `main.go:2058-2066` documents the workaround for genesis only. Consequence: with `QSDM_SOLO_VALIDATOR_MODE=0`, the node produces no blocks after genesis.
**Fix:** define a `SpeculativeApplier` interface (`Clone() / ApplyTx / StateRoot`) and have `EnrollmentAwareApplier` satisfy it. This single change unblocks the entire BFT/POL path.

### 4. Supply invariant is missing at the account layer, and float64 balances silently destroy CELL
*(Corrected 2026-08-07 after direct re-verification. The original finding said "no supply cap anywhere" — that is **wrong**. See below.)*

**What is actually true.** A real, integer-exact emission cap **does** exist and is enforced on the emission path: `pkg/chain/emission.go:39-46` defines `CellMiningCapWhole = 90_000_000` and `CellMiningCapDust = 9e15` with a package-`init` invariant check (`:71-74`), halving math that provably converges below cap in integer arithmetic (`:162-177`), `MaxHalvings = 64`, and 16 tests. `internal/blockdriver/blockdriver.go:633-634` clamps to `MiningCapDust`. **Do not "add a supply cap" — it exists.**

**The three real defects:**

1. **No total-supply invariant at the `AccountStore` layer.** The cap governs *emission*; nothing prevents a mint outside that path. `mining_reward.go:21-57` never checks cumulative emission.

2. **Genesis credits ~1e15 CELL to `qsdm-system-funder`.** `blockdriver.go:95 DefaultFunderBalance = 1e15` is `float64` (`blockdriver.go:152 FunderInitialBalance float64`), and `Account.Balance` is `float64` (`account.go:19`) — so this is **1e15 CELL, not dust**. It is pinned into the canonical genesis state root at `cmd/qsdm/main.go:2935-2951`. The in-code comment (`blockdriver.go:87-94`) defends this as harmless because the schedule caps emission — true for emission, but it makes the *opening allocation* 10,000,000× the advertised 100M, and it causes defect 3.

3. **Float64 balance arithmetic destroys CELL on every reward block.** `account.go:172` does `sender.Balance -= total` on the funder (balance ≈ 1e15, **float64 ULP = 0.125 CELL**), while `:180` does `recipient.Balance += liquidAmount` on the miner (small balance, exact). Debit and credit are performed at incomparable magnitudes:

   | Quantity | Value |
   |---|---|
   | Funder balance | ~1.0e15 CELL |
   | float64 ULP at that magnitude | 0.125 CELL |
   | Epoch-0 reward/block (90M cap, 4y epoch, 10s blocks) | 3.564909879 CELL |
   | Debit actually applied | 3.625 CELL |
   | Credit actually applied | 3.564909879 CELL |
   | **Supply destroyed per block** | **0.0601 CELL** |
   | **Drift** | **~189,630 CELL/year** |

   The 1e-8 dust unit is **not representable at all** at 1e15 magnitude (`math.ulp(1e15) = 0.125 > 1e-8`). The comment at `blockdriver.go:645-651` reasons that float64 is safe because the error is "at most 1 ULP per block, 2^-52 ≈ 2.2e-16" — that is the ULP of the **reward value**, not of the **funder balance it is subtracted from**. That is the precise analytical error.

   `AccountStore.StateRoot()` (`account.go:315`) compounds this by hashing balances with `%f` — 6 decimal places — so any balance below 1e-6 CELL is invisible to the state root, and two nodes disagreeing below that threshold still produce identical roots.

**Fix:** convert `Account.Balance` from `float64` to `uint64` dust (the emission layer already speaks dust — this makes the two layers agree), change `StateRoot` to hash the integer, add a total-supply invariant to `AccountStore` that rejects any mint exceeding `CellMiningCapDust + treasury`, and re-derive genesis to the documented 90M/10M split. This is the highest-value single refactor in the repo: it fixes the money bug, the state-root truncation, and the unit mismatch at once. Until it lands, do not publish tokenomics claims.

### 5. Sign BFT votes, POL certificates, evidence, and blocks
`pkg/chain/bft_wire.go:32-46` carries no signature; `bft_executor.go:388-412` applies unauthenticated; `consensus_certificate.go:38-83` emits an unsigned digest; blocks have no producer signature (no `SignBlock` anywhere); `evidence.go` is unsigned. Every one of these is trivially forgeable today.
**Fix:** add ML-DSA-87 (or ed25519 with keyring binding) signatures to all four message types plus block headers, verified before application.

### 6. Edge-pool settlement pays nothing — and never will
`pkg/chain/task_state.go:1036-1046` requires the manifest to list the Relay key; `authorized_relay_ids` has exactly **one** occurrence tree-wide — the struct field at `task_catalog.go:72`. No genesis, config, or UI populates it (`TaskStudio.tsx:93-94` publishes a `generic-proof-v1` task ID that is never in `systemResourceTaskPolicies`). Downstream, `qsdmSystemTasks.ts:2048-2062` only acks after on-chain commit, so proofs are re-served forever and receipts never consumed.
**Fix:** seed the three system task manifests with authorized relay IDs at genesis, and expose relay-ID authorization in Task Studio.

### 7. Wire what already exists — five one-to-ten-line gaps
- `GovernanceAuthorities` omitted from `v2wiring.Config` at `cmd/qsdm/main.go:1210-1248` ⇒ ~2,500 LOC of tested governance rejects every tx (`gov_apply.go:441-444`).
- `miningSvcCfg.Batches` never set (`main.go:1592-1606`) ⇒ `acceptAllBatchValidator{}` (`miningsvc.go:258-261,500-502`) makes §8.3 fraud quarantine unreachable.
- `SyncBalanceFromLedger` (`pkg/wallet/wallet.go:152-160`) has no production caller ⇒ `/wallet/send` always 500s.
- `ReputationTracker.IsBanned` (`pkg/networking/reputation.go:169`) never consulted ⇒ bans have no effect.
- `RoleRateLimiter` never mounted despite `security.go:187-190` claiming it is.

### 8. Rate limiter is bypassable with one header; NVIDIA-lock misses every write path
`pkg/api/security.go:288-292` keys the bucket on an unvalidated `X-API-Key`, and `setupMiddleware` (`server.go:482-521`) runs RateLimit *before* Auth — rotate the header per request, get an unlimited window on `/wallet/submit-signed` (nominally 10/min). Separately, `enforceNvidiaLock` covers 7 call sites and misses `/wallet/submit-signed`, `/mining/submit`, `/mining/enroll`, `/faucet/claim`, `/referrals/*`, `/tasks/actions/submit-signed`.
**Fix:** validate the API key before it can key a bucket (or drop the header path); extend the lock to the actual write paths or document the exact covered set.

### 9. CI does not test the storage or networking layer
Exhaustive grep of `.github/workflows/` for `go test`: `pkg/networking`, `pkg/storage`, `pkg/monitoring` appear in **zero** invocations. That means 82 networking tests, both SQLite tests, the mTLS suite, the versioning suite, and the rate-limit tests are non-gating. `pkg/api` runs with a single `-run` filter (`qsdm-go.yml:355`). There is **no `sqlite_v041_test.go` at all** — the load-bearing replay-protection SQL has zero executing coverage. `validate-deploy.yml` has been red on `main` since 2026-08-05.
**Fix:** add these packages to the CI test matrix, write `sqlite_v041_test.go` against real SQLite, add a coverage floor, and fix the red sitemap lint.

### 10. Ship-blocking artifact and consumer defects
- `deploy/landing/wallet.html:872` stale SRI on this branch (introduced by `b2fe934`, which also deleted the `wallet-provider.js` tag) — the wallet page is inert if merged as-is. Production and `origin/main` are correct. Add a CI SRI check (`scripts/check_sitemap_freshness.py` is the right template).
- `privacy.html` / `support.html` are **untracked** yet required by `_install_docs_site.sh:53-60`. `git add` them.
- At audit time, npm `qsdm-sdk` was stuck at 0.3.0 with a permanently-404ing `getTransaction`, and the 0.3.1 publish run had failed (27145442319). Current main has since advanced to 0.3.3; 0.3.1 must not be published over it.
- Go SDK is not `go get`-able — no root `go.mod`, no vanity meta tag.
- ~~`native/` is gitignored in Hive ⇒ **no platform** can be packaged from a clean clone.~~ **REFUTED (verified 2026-08-07).** `native/` is correctly gitignored: it holds 63 MB of compiled Go binaries that the packaging scripts build from source. `package:windows` runs `native:windows` → `QSDM/deploy/scripts/build_hive_windows_native.ps1`, and `package:linux` → `build_hive_linux.sh` populates `native/linux/x64`. A clean clone packages fine; committing these artifacts would be the actual defect.
- K8s manifests reference `qsdm:latest` / `qsdm/miner:latest` / `qsdm/validator:latest`; CI publishes `ghcr.io/<owner>/qsdm{,-validator,-miner}` ⇒ guaranteed `ImagePullBackOff`.

---

## 8. Bottom Line

The engineering quality of the *written* code is high and the in-source comments are unusually self-critical — the SDK deprecation banners name the exact handler file that does not register the route, `CODE_SIGNING_POLICY.md` admits the SignPath rejection, `blockdriver.go:12-42` disclaims its own tokenomics. **The overclaim lives almost entirely in the `docs/docs/*_COMPLETE.md` layer and the marketing surface, not in the engineering comments.**

What separates 59% from 99% is not 40% more code. It is: **four one-line wirings** (governance authorities, batch validator, balance sync, applier interface), **five signature additions** (BFT votes, POL certs, evidence, blocks, ed25519 sender binding), **one supply invariant**, **one manifest seed**, and **deleting or correcting 30 documentation claims**. That is a focused quarter of work, not a rewrite — but until it lands, the honest statement is: *QSDM+ is a well-built single-node ledger with a strong post-quantum wallet, a real GPU mining protocol, and a genuine edge-compute pool, none of which currently form a trustless multi-validator network or an enforced token economy.*
