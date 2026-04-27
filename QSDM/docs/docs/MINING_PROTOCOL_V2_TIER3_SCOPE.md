# MINING_PROTOCOL_V2 — Tier-3 Deferred Scope

**Status:** Out-of-session. Explicit deferral. Not blocking v2 activation.

**Audience:** Future implementer / owner. This doc captures the
"deferred but planned" surface of the v2 protocol so the chain can
ship Phase 2 (consumer-GPU HMAC + stake bonding + slashing
scaffolding) without those Tier-3 items, and so the next implementer
inherits a precise scope rather than a TODO comment.

This doc is a sibling to
[`MINING_PROTOCOL_V2_NVIDIA_LOCKED.md`](./MINING_PROTOCOL_V2_NVIDIA_LOCKED.md)
and is referenced from §10 (Implementation phase map).

---

## 1. Why these items are deferred

The v2 hard fork is decomposed into **what miners need on day 0** vs.
**what validators can grow into post-genesis**. The deferred items
share three properties:

1. They require **physical hardware that the project does not own
   yet** (Hopper / Blackwell datacenter GPUs for CC, RTX 4090-class
   for Tensor-Core kernel calibration).
2. They require **non-trivial dependencies on NVIDIA's release
   cadence** (NGC attestation service contracts, CUDA Toolkit 12.x
   APIs that move quarterly).
3. They are **upgradable behind feature gates** — the v2 wire format
   already reserves the `nvidia-cc-v1` attestation type, the Tensor-Core
   kernel slot, and the `EvidenceVerifier` registry, so flipping them
   on later is a soft fork at most, not a wire break.

Shipping the consumer-GPU path first reduces time-to-mainnet from
~8 weeks to ~2 weeks while preserving the door to datacenter-grade
trust later.

---

## 2. Tier-3 item: `nvidia-cc-v1` verifier (datacenter CC GPUs)

### 2.1 Current state in repo

| Component | Status |
|---|---|
| `pkg/mining/attest/cc/StubVerifier` | **Now a fall-through.** Used only when no pinned NVIDIA root is configured. Still always returns `ErrNotYetAvailable`. |
| `pkg/mining/attest/cc/Verifier` | **Phase 2c-iv landed.** Full eight-step acceptance flow: cert-chain-rooted-in-pinned-CA → ECDSA AIK quote verify → nonce/issued_at binding → optional `challenge_sig` cross-check → freshness window → replay cache → PCR firmware/driver floor. Reference: [`pkg/mining/attest/cc/verifier.go`](../../QSDM/source/pkg/mining/attest/cc/verifier.go). |
| Bundle wire format | Defined in [`pkg/mining/attest/cc/bundle.go`](../../QSDM/source/pkg/mining/attest/cc/bundle.go). Length-prefixed binary preimage covering `(device_uuid, challenge_nonce, issued_at, miner_addr, batch_root, mix_digest, challenge_signer_id, challenge_sig)` — same anti-replay strength as the §3.2.2 HMAC path. |
| Test vectors | [`pkg/mining/attest/cc/testvectors.go`](../../QSDM/source/pkg/mining/attest/cc/testvectors.go) — deterministic in-process generator (seeded PRNG, no testdata files). Verifier test suite covers happy + 13 negative cases (tampered signature, wrong root, expired leaf, nonce mismatch, issued_at mismatch, miner_addr/mix_digest tamper, stale, future-dated, below firmware floor, below driver floor, replay, malformed base64, unknown JSON field, over-length cert chain). |
| Production wiring | `attest.ProductionConfig.CCConfig` builds the real verifier from a `cc.VerifierConfig` (pinned roots + min firmware + replay store + optional `ChallengeVerifier`). `attest.ProductionConfig.CCVerifier` remains the escape hatch for HSM-backed builds. Both nil → fall back to the stub. |
| Dispatcher routing | `pkg/mining/attest/dispatcher.go` accepts the type key (unchanged). |

### 2.2 What's still deferred

The Phase 2c-iv verifier is consensus-complete against the
spec's verifier flow. What's NOT yet shipped:

1. **NVIDIA-issued real-world test vectors.** Determinism in
   `cc/testvectors.go` is enough for CI and protocol regression
   testing, but a real H100 / B100 produces an AIK quote whose
   on-the-wire framing is NVIDIA-proprietary. The current
   `cc.Bundle` shape is a Go-native canonicalisation that
   mirrors what an `nvtrust` quote would contain (cert chain +
   AIK signature over the spec preimage + PCR-equivalent
   versions). The seam to swap in real `nvtrust` framing is a
   single `ParseBundle` reimplementation; the verifier code does
   NOT change.
2. **Genesis-pinned NVIDIA root rotation.** `VerifierConfig.PinnedRoots`
   is plumbed end-to-end; ratifying the actual NVIDIA-issued
   Hopper/Blackwell root cert at v2 fork-time is a separate
   governance decision and is tracked in the genesis-state
   schedule, not in code.
3. **CUDA-side miner integration.** Once `cmd/qsdm-miner-cuda`
   ships (see §3 below), it will produce live CC bundles using
   the on-host nvtrust SDK; today only `cmd/qsdmminer-console`
   produces v2 attestations and it produces `nvidia-hmac-v1`
   only.

### 2.3 Hard external dependencies

- **NGC Attestation Service contract.** NVIDIA gates programmatic
  AIK chain verification behind a paid datacenter relationship.
  The current verifier sidesteps this by accepting any
  ECDSA-keyed leaf cert that chains to a pinned root —
  governance can pin NVIDIA's published Hopper/Blackwell roots
  the day the contract is signed, no code change needed.
- **A physical Hopper or Blackwell GPU** for swap-in real-world
  test vectors (see §2.2 item 1). The deterministic in-process
  vectors handle every consensus-relevant code path without
  hardware.

### 2.4 Gating

Validators that do NOT configure a CC trust anchor continue to
reject every `nvidia-cc-v1` proof via `StubVerifier`'s
`ErrNotYetAvailable`. Operators opt in by populating
`attest.ProductionConfig.CCConfig.PinnedRoots`. The v2 hard fork
remains safe to activate with the stub posture; flipping to the
real verifier is a per-validator opt-in until the genesis
operator-registry consensus on a trust anchor lands.

### 2.5 Remaining scope-of-work estimate (post-hardware)

- Real-world `nvtrust` bundle framing in `ParseBundle`: **2 days**
- Genesis trust-anchor rotation policy: **1 day**
- E2E vectors against a real H100 / B100: **2 days**
- Total: **~5 days** to swap from in-process test vectors to
  hardware-issued vectors. Down from the original **~8 days**
  estimate because the cert-chain + signature + freshness +
  replay + PCR-floor pipeline is already done and tested.

---

## 3. Tier-3 item: Tensor-Core PoW kernel (`cmd/qsdm-miner-cuda`)

### 3.1 Current state in repo

| Component | Status |
|---|---|
| `pkg/mining/fork.go` | Defines `ProtocolVersionV2` constant. |
| Wire format `Proof.Version` | v2-aware. v1 proofs continue to validate pre-fork. |
| PoW mixin spec §4 | Documented. Not yet implemented. |
| `cmd/qsdm-miner-cuda` | Does NOT exist yet. |
| `cmd/qsdmminer-console` | v1 default; opt-in v2 attestation **fully wired end-to-end** (challenge fetch → HMAC bundle → v2 submit), built-in `--gen-hmac-key`, v2 sub-wizard, live "v2 NVIDIA" panel row, **background `EnrollmentPoller`** (30 s default, `--enrollment-poll`) that paints `phase` / `stake` / `slashable` and emits phase-transition events. No Tensor-Core mixin. |

### 3.2 What "done" looks like

Ship `cmd/qsdm-miner-cuda` containing:

1. **A CUDA kernel** that performs the §4.2 mixin: per nonce attempt,
   16 dependent `mma.m16n8k16.f16` Tensor-Core ops over a deterministic
   matrix derived from `(prev_block_hash || nonce_high)`, then folds
   the FP16 accumulator into the standard double-SHA256 outer hash.
2. **A non-CUDA fallback** that computes the same mixin in software
   (slow, ~1000× slower than RTX 4090). Validators MUST use this
   path; miners using it earn nothing.
3. **A validator-side reference impl** in pure Go inside
   `pkg/mining/pow/v2/` so block validation does not require CUDA
   on validator nodes.
4. **A calibration suite** that pins the difficulty target so an
   RTX 4090 hits ~1 block / 30s on a ~1000-validator testnet (
   numbers TBD against real hardware).

### 3.3 Hard external dependencies

- **A working CUDA Toolkit 12.x toolchain in CI.** The repo's
  current CI does not provision NVIDIA hardware for tests. We
  either: (a) add a self-hosted GPU runner, (b) cross-compile and
  smoke-test the kernel offline, or (c) gate Tensor-Core CI to a
  manual workflow.
- **At least one RTX 4090** for difficulty calibration. PoW
  difficulty cannot be set without measuring real `mma` throughput.
- **Stable mma instruction selection.** `mma.m16n8k16.f16` is
  Ampere+ only. Turing miners (RTX 20-series) cannot mine v2 even
  with a CUDA build. This is **intentional** per the §1 NVIDIA-only
  hard lock — but it means we owe miners a deprecation notice for
  pre-Ampere cards before the fork.

### 3.4 Gating

The v2 fork ships **without** the Tensor-Core mixin if §3.2 is not
ready. In that case:

- `Proof.Version = 2` proofs are accepted with the legacy
  double-SHA256 PoW only.
- The mixin is enabled via a **second** fork height
  (`FORK_V2_TC_HEIGHT`) at a future block. This is a soft-rejection
  fork (validators get stricter), so it does not require a chain
  reset — only a coordinated upgrade.

### 3.5 Migration path for existing CPU/v1 miners

`cmd/qsdmminer-console` is **frozen at the v1 PoW algorithm** for
the duration of Tier-3 development. It will not be re-shipped
against the v2 Tensor-Core PoW. Per the Phase-0 retirement
decision (commit `19e756a`), the CPU miner remains in the repo
only as the reference v1 PoW implementation; new miners are
directed to wait for `cmd/qsdm-miner-cuda` for production v2 PoW.

**Important nuance** — "frozen at v1 PoW" does *not* mean
"frozen at v1 protocol". The console binary now ships a fully
wired v2 attestation path (`v2.go`, `v2_integration_test.go`):

- `--gen-hmac-key=PATH` produces a 0o600 hex key file and prints
  the matching `qsdmcli enroll …` snippet.
- `--setup` offers an opt-in v2 sub-wizard that drives operators
  end-to-end through key generation → field collection → bond
  command emission.
- With `--protocol=v2` set, the runLoop fetches a fresh
  `/api/v1/mining/challenge`, builds an `nvidia-hmac-v1` bundle
  via `pkg/mining/v2client`, and submits a `Version=2` proof —
  with the live console panel showing a `v2 NVIDIA` row carrying
  `node`, `arch`, `attestations`, and `challenge=Ns ago` so
  operators can spot freshness-window drift.
- A challenge-endpoint outage produces a clear `EvError` and
  refuses to fall back to v1, preventing accidental v1
  submissions to a forked validator.

This means the console miner is suitable today for
**testnet v2 attestation participation** (post-fork, pre-
Tensor-Core-mixin), even though it remains unsuitable for v2
*PoW* mining once the second fork height (`FORK_V2_TC_HEIGHT`)
activates. Coverage is gated by `TestIntegration_RunLoop_v2_EndToEnd`.

### 3.6 Scope-of-work estimate (post-hardware)

- CUDA kernel + Go FFI shim: **3 days**
- Pure-Go validator reference impl: **2 days**
- Cross-impl differential test vectors: **2 days**
- Difficulty calibration on RTX 4090: **2 days**
- `cmd/qsdm-miner-cuda` UX (config, telemetry, Docker image): **3 days**
- CI integration (self-hosted GPU runner OR offline smoke): **2 days**
- Total: **~14 days** assuming hardware on day 1.

---

## 4. Tier-3 item: Concrete `EvidenceVerifier` implementations

### 4.1 Current state in repo

`pkg/mining/slashing/` ships:

- `SlashPayload` wire format (`qsdm/slash/v1`).
- Canonical encoder/decoder.
- Stateless field validation.
- `EvidenceVerifier` interface + `Dispatcher` registry.
- `StubVerifier` (always rejects).
- **`NewProductionDispatcher`** (production.go) — assembles a
  fully-wired dispatcher with the forged-attestation AND
  double-mining verifiers registered against the on-chain
  registry, and a stub for the one remaining deferred kind.

Three `EvidenceKind`s are reserved:

| Kind | Detects | Verifier status |
|---|---|---|
| `forged-attestation` | An HMAC bundle whose MAC fails verification, whose `gpu_uuid` mismatches the enrolled record, whose `challenge_bind` mismatches the proof, or whose `gpu_name` matches the governance deny-list. | **Landed** (`pkg/mining/slashing/forgedattest`). |
| `double-mining` | Two distinct accepted proofs from the same `(node_id, epoch, height)`, both crypto-valid under the registered HMAC key. | **Landed** (`pkg/mining/slashing/doublemining`). |
| `freshness-cheat` | A proof whose `challenge.issued_at` is older than `FRESHNESS_WINDOW` and was nonetheless accepted (i.e. retroactive evidence of validator collusion or clock skew). | Stubbed. |

### 4.2 What "done" looks like

For each kind, ship a verifier that:

1. **Decodes the `EvidenceBlob`** into a kind-specific struct.
2. **Re-runs the original validity check** that the chain *should*
   have rejected the proof under, using only data that is provably
   available at slashing time (post-fork, on-chain).
3. **Returns `maxSlashDust`** — the slasher cannot slash more than
   the offender's currently-bonded stake; the dispatcher already
   enforces this cap downstream.

### 4.3 Hard external dependencies

- **A chain-side applier for `qsdm/slash/v1` transactions.** This
  is the half of slashing that is NOT in `pkg/mining/slashing/`.
  It must:
  - Look up the offender's `EnrollmentRecord` in the
    `EnrollmentState`.
  - Debit `min(SlashAmountDust, record.StakeDust)` from the bonded
    stake.
  - Mark the record as slashed (so it cannot be `Unenroll`-swept
    out from under a pending slasher).
  - Reward the slasher with a configurable fraction of the slashed
    amount, burn the rest. Open question: reward fraction.
- **A consensus rule for evidence freshness.** Slashing evidence
  is itself replay-attackable — if an attacker submits the same
  forged-attestation evidence ten times, the offender gets slashed
  ten times. Mitigation: the chain MUST track `(node_id, evidence_hash)`
  pairs and reject duplicates.

### 4.4 Gating

As of the most recent commit, both `forged-attestation` AND
`double-mining` ship real verifiers; only `freshness-cheat`
remains stubbed. This means slashing is **active** for:

- HMAC forgery / GPU-UUID forgery / challenge-bind forgery /
  deny-listed-GPU offences (`forged-attestation`).
- Equivocation: any operator that signs two distinct, valid v2
  proofs at the same `(epoch, height)` is slashable
  (`double-mining`). The encoder canonicalises pair order so the
  per-fingerprint replay protection in `pkg/chain/slash_apply.go`
  treats `(a, b)` and `(b, a)` as the same offence.

Slashing now also enforces a **post-slash auto-revoke** at the
chain layer: if a successful slash leaves the offender with
strictly less than `MinEnrollStakeDust` (default 10 CELL),
`pkg/chain.SlashApplier` calls
`enrollment.InMemoryState.RevokeIfUnderBonded`, which sets the
record's `RevokedAtHeight + UnbondMaturesAtHeight` and releases
the `gpu_uuid` binding so a fresh `node_id` can re-enroll the
physical card without waiting. This closes the
"slash-to-zero, keep mining for free" loophole: an operator
whose bond drops below the original enrollment minimum is
automatically retired with the remaining stake locked through
the standard unbond window. The threshold is configurable via
`SlashApplier.AutoRevokeMinStakeDust` (set to 0 to disable;
default is the protocol's `MinEnrollStakeDust`).

The remaining inert kind, `freshness-cheat`, still depends on
BFT finality; see §5 for ordering. The wire format and applier
infrastructure are otherwise complete and exercised end-to-end
in `pkg/chain/slash_forgedattest_e2e_test.go` and
`pkg/chain/slash_doublemining_e2e_test.go`.

### 4.5 Scope-of-work estimate

- ~~`forged-attestation` verifier (HMAC re-verification): **2 days**~~ **Landed** in `pkg/mining/slashing/forgedattest` with table-driven verifier tests, a production dispatcher (`pkg/mining/slashing/production.go`), and an end-to-end stake-drain test (`pkg/chain/slash_forgedattest_e2e_test.go`).
- ~~`double-mining` verifier (epoch-indexed seen-proofs cache): **3 days**~~ **Landed** in `pkg/mining/slashing/doublemining` with table-driven verifier tests, an `OPTIONAL` slot in `slashing.ProductionConfig.DoubleMining`, a convenience factory (`doublemining.NewProductionSlashingDispatcher`), and an end-to-end stake-drain test (`pkg/chain/slash_doublemining_e2e_test.go`). Note: an *epoch-indexed seen-proofs cache* is intentionally NOT used — the verifier is purely transactional, accepting any pair of distinct, valid proofs at the same `(epoch, height)` regardless of whether the chain previously saw them. Replay-protection lives one level up in `pkg/chain/slash_apply.go` and keys on the canonical evidence fingerprint.
- `freshness-cheat` verifier (validator-set quorum proof): **4 days,
  plus design review** — this one assumes the chain has BFT
  finality, which we do not yet ship.
- ~~Chain-side `qsdm/slash/v1` applier + replay protection: **3 days**~~ **Landed** in `pkg/chain/slash_apply.go` (commit `5f5fce7`).
- ~~Slasher reward economics + governance hook: **2 days**~~ **Reward economics landed** (RewardBPS, `SlashRewardCap=5000`); on-chain governance hook for tuning RewardBPS at runtime is still future work.
- ~~Post-slash auto-revoke for under-bonded records: **1 day**~~ **Landed** in `pkg/mining/enrollment/registry.go` (`InMemoryState.RevokeIfUnderBonded`) and `pkg/chain/slash_apply.go` (`SlashApplier.AutoRevokeMinStakeDust`, default = `mining.MinEnrollStakeDust`). Unit tests in `pkg/mining/enrollment/revoke_underbonded_test.go` and `pkg/chain/slash_apply_autorevoke_test.go`; e2e auto-revoke assertions added to `pkg/chain/slash_forgedattest_e2e_test.go` and `pkg/chain/slash_doublemining_e2e_test.go`.
- Remaining work: **~4 days** for `freshness-cheat`, plus the BFT-finality dependency it inherits, and **~2 days** for the `qsdm/gov/v1` runtime tuning hook for `RewardBPS` / `AutoRevokeMinStakeDust`.

---

## 5. Suggested ordering

If/when Tier-3 work resumes, the recommended order is:

1. **Tensor-Core PoW kernel (§3)** — unblocks miner UX and is the
   main reason consumer GPUs have any reason to exist on this
   chain. Highest market value, lowest external blocker (only
   needs hardware).
2. ~~**`forged-attestation` slasher (§4)**~~ — **Landed.** Lives
   in `pkg/mining/slashing/forgedattest`; wired into the
   production dispatcher; covered end-to-end by
   `pkg/chain/slash_forgedattest_e2e_test.go`.
3. ~~**`double-mining` slasher (§4)**~~ — **Landed.** Lives in
   `pkg/mining/slashing/doublemining`; injectable through
   `slashing.ProductionConfig.DoubleMining`; covered end-to-end
   by `pkg/chain/slash_doublemining_e2e_test.go`. Note that
   double-mining is purely a *transactional* check (pair of
   crypto-valid distinct proofs at the same `(epoch, height)`)
   and therefore does NOT depend on BFT finality — that
   dependency was an over-cautious read of the original spec.
4. ~~**`nvidia-cc-v1` verifier (§2)**~~ — **Landed (Phase 2c-iv).**
   Real `cc.Verifier` lives in `pkg/mining/attest/cc/verifier.go`;
   buildable via `attest.ProductionConfig.CCConfig`; covered by
   28 unit tests including 13 negative cases (tampered sig, wrong
   root, expired leaf, nonce/issued_at mismatch, preimage tamper,
   stale, future-dated, PCR floor, replay, malformed bundle,
   unknown JSON field, over-length chain). Only consensus-
   speculative work that remains is swapping the in-process
   bundle framing for real `nvtrust` bytes — every other line is
   now hardened.
5. **`freshness-cheat` slasher (§4)** — depends on BFT finality
   and on a sufficient validator set to detect retroactive
   acceptance of stale-nonce proofs. Last item in the slashing
   trilogy; gated on BFT-finality landing first.

---

## 5a. Observability for slashing + enrollment

> **Status:** Landed (this section is descriptive, not deferred).
> Lives at the same Tier-3 scope level as the verifiers because it
> directly observes the §4 surface area.

The slashing applier and the enrollment applier emit two
parallel observability streams. **Both are wired through a
dependency-inverted seam (`chain.MetricsRecorder`,
`chain.ChainEventPublisher`) so `pkg/chain` does not import
`pkg/monitoring`** — that import cycle is what historically kept
slashing observability under-instrumented. The seam is
populated automatically when `pkg/monitoring` is loaded into a
binary (`init()` in `pkg/monitoring/chain_recorder.go`); a
binary that does not import `pkg/monitoring` falls back to
`noopRecorder{}` and `NoopEventPublisher{}` and pays no
overhead.

### 5a.1 Prometheus metrics

Exposed by `pkg/monitoring/prometheus_scrape.go` on the
node's `/api/metrics/prometheus` endpoint. All counters reset
to zero on process restart (single-counter convention; the
delta-over-time semantics are the operator's responsibility).

| Metric | Type | Labels | Source |
|---|---|---|---|
| `qsdm_slash_applied_total` | counter | `kind` (`forged-attestation`, `double-mining`, `freshness-cheat`, `unknown`) | Successful slash transitions, per evidence kind. |
| `qsdm_slash_drained_dust_total` | counter | `kind` | Dust drained from offenders, per evidence kind. Equals `min(SlashAmountDust, record.StakeDust)`. |
| `qsdm_slash_rewarded_dust_total` | counter | — | Dust paid to slashers across all kinds. |
| `qsdm_slash_burned_dust_total` | counter | — | Dust burned (not rewarded) across all kinds. |
| `qsdm_slash_rejected_total` | counter | `reason` (`verifier_failed`, `evidence_replayed`, `node_not_enrolled`, `decode_failed`, `fee_invalid`, `wrong_contract`, `state_lookup_failed`, `stake_mutation_failed`, `other`) | Per-reason rejection of slash transactions. |
| `qsdm_slash_auto_revoked_total` | counter | `reason` (`fully_drained`, `under_bonded`) | Auto-revokes triggered by §4.4 post-slash logic. |
| `qsdm_enrollment_applied_total` | counter | — | Successful enroll transactions. |
| `qsdm_unenrollment_applied_total` | counter | — | Successful unenroll transactions. |
| `qsdm_enrollment_rejected_total` | counter | `reason` (`stake_mismatch`, `gpu_bound`, `key_invalid`, `payload_invalid`, `decode_failed`, `wrong_contract`, `nonce_invalid`, `fee_invalid`, `account_lookup_failed`, `debit_failed`, `state_apply_failed`, `other`) | Per-reason rejection of enroll txs. |
| `qsdm_unenrollment_rejected_total` | counter | `reason` (subset of enrollment, plus `not_enrolled`) | Per-reason rejection of unenroll txs. |
| `qsdm_enrollment_unbond_swept_total` | counter | — | Unbond windows that matured and released stake. |
| `qsdm_enrollment_active_count` | gauge | — | Records where `Active() == true`. Snapshot per scrape. |
| `qsdm_enrollment_bonded_dust` | gauge | — | Sum of `StakeDust` across active records. Snapshot per scrape. |
| `qsdm_enrollment_pending_unbond_count` | gauge | — | Records that have been revoked but whose unbond window has not yet matured. |
| `qsdm_enrollment_pending_unbond_dust` | gauge | — | Dust still locked in pending-unbond records (zero-dust pending records are still counted in `pending_unbond_count`). |

Gauges are **callback-driven**: `pkg/monitoring/enrollment_metrics.go`
holds an `EnrollmentStateProvider` set at boot via
`SetEnrollmentStateProvider(...)`. The default provider in
`pkg/monitoring/enrollment_state_provider.go` wraps an
`enrollment.InMemoryState` and walks the record map under a
single mutex acquisition per scrape (O(n) in active miners).
A node operator that runs without enrollment (e.g. legacy v1
binary) leaves the provider unset and the gauges read as 0.

### 5a.2 Structured events

`pkg/chain/events.go` defines two event types:

- `MiningSlashEvent{Kind, Outcome, Offender, Slasher, EvidenceFingerprint, RejectReason, AppliedHeight, BondedDust, SlashedDust, DrainedDust, RewardedDust, BurnedDust, AutoRevoked}`
- `EnrollmentEvent{Kind, NodeID, Owner, GPUUUID, RejectReason, StakeDelta, AppliedHeight}`

Outcomes:
- `MiningSlashOutcomeApplied` — emitted exactly once per
  successful slash, AFTER the offender's stake has been
  mutated, AFTER any auto-revoke, and AFTER the metric
  counters have been incremented.
- `MiningSlashOutcomeRejected` — emitted exactly once per
  rejection path with the canonical `reason` label.
- `EnrollmentEventEnrollApplied` / `EnrollmentEventEnrollRejected`
  / `EnrollmentEventUnenrollApplied` / `EnrollmentEventUnenrollRejected`
  / `EnrollmentEventSweep` — symmetric coverage of the four
  enrollment transitions.

Events are published via `ChainEventPublisher`. Default
implementation is `NoopEventPublisher{}`. Production deployments
attach a publisher (Kafka, NATS, on-chain log emitter, audit
sink) by setting the `Publisher` field on `EnrollmentApplier` /
`SlashApplier` at construction. `CompositePublisher` lets
multiple sinks subscribe.

### 5a.3 Why both metrics AND events

Metrics are sufficient for SLO dashboards but discard per-event
detail (which `node_id` was slashed, which evidence
fingerprint, etc.). Events are the audit trail, but expensive
to store and slow to query in aggregate. Both layers share the
same canonical reason-tag string set
(`SlashRejectReason*`, `EnrollRejectReason*`) so a metric spike
on `qsdm_slash_rejected_total{reason="evidence_replayed"}`
maps 1:1 onto the corresponding `MiningSlashEvent` records in
the audit sink.

### 5a.4 Test coverage

- `pkg/chain/events_test.go` — unit tests with fake
  `MetricsRecorder` and `ChainEventPublisher` that verify
  every applied / rejected path emits exactly one metric
  observation and one event.
- `pkg/mining/enrollment/stats_test.go` — covers the gauge
  source-of-truth (`InMemoryState.Stats()`) across enroll,
  unenroll, slash-to-zero, and sweep transitions.
- Existing e2e tests
  (`slash_forgedattest_e2e_test.go`,
  `slash_doublemining_e2e_test.go`) continue to pass — the
  metrics and event seams default to noop and impose zero
  observable change in those tests.

---

## 5b. Production boot wiring (`internal/v2wiring`)

The v2 mining surface is now wired into `cmd/qsdm/main.go` at
boot through a single helper, `internal/v2wiring.Wire(...)`.
Before this commit, every collaborator built in §§2–5 (the
on-chain enrollment state, the `EnrollmentApplier`, the
`SlashApplier`, the production slash dispatcher, the mempool
admission gate, the HTTP enroll/unenroll handlers, the four
Prometheus enrollment gauges) was code without a caller in any
production binary — `grep NewEnrollmentApplier cmd/qsdm/main.go`
returned zero matches. The Tier-3 milestone is meaningless if
none of it runs outside `go test`, so this seam closes the gap.

### 5b.1 What `Wire` constructs

In a single call ordered before `chain.NewBlockProducer`:

- `enrollment.NewInMemoryState()` — the on-chain registry.
- `chain.NewEnrollmentApplier(accounts, state)` — the
  applier consumed by `EnrollmentAwareApplier`.
- `chain.NewEnrollmentAwareApplier(accounts, enroll)` — the
  `chain.StateApplier` shim that drops directly into
  `NewBlockProducer` in place of the bare `*AccountStore`.
- `doublemining.NewProductionSlashingDispatcher(...)` +
  `chain.NewSlashApplier(...)` attached via
  `aware.SetSlashApplier(...)`. Slashing wiring failure is a
  hard boot error rather than a silent degrade — the operator
  cannot accidentally run a node that drops slash txs.
- `monitoring.SetEnrollmentStateProvider(...)` — populates
  the four `qsdm_enrollment_*` gauges.
- `pool.SetAdmissionChecker(slashing.AdmissionChecker(
  enrollment.AdmissionChecker(prev)))` — the stacked mempool
  admission gate. `prev` is supplied by the caller (POL/BFT
  extension predicate in `cmd/qsdm/main.go`) and runs last,
  for non-enrollment, non-slash txs. Slash- and enroll-tagged
  txs go through their respective stateless validators
  before delegating to `prev`. Layer order is structurally
  safe (each layer only intercepts its own ContractID) but
  fixed at slashing > enrollment > base for readability and
  blast-radius ordering.
- `api.SetEnrollmentMempool(pool)` — HTTP handler hookup
  for `/api/v1/mining/enroll` and `/api/v1/mining/unenroll`.
- `api.SetSlashMempool(pool)` — HTTP handler hookup for
  `/api/v1/mining/slash`.
- `api.SetEnrollmentRegistry(state)` — HTTP handler hookup
  for the read endpoint
  `GET /api/v1/mining/enrollment/{node_id}`. Returns a
  sanitized `EnrollmentRecordView` (HMACKey omitted by
  design — least-privilege on a hot operator value) with a
  derived `phase ∈ {active, pending_unbond, revoked}` and a
  `slashable` boolean so slash-evidence submitters can confirm
  a target carries real stake before posting evidence. All
  four endpoints return 503 until their respective `Set*` is
  called; `Wire` installs all of them atomically off the same
  `*InMemoryState` (one source of truth — no read replica or
  cache).

### 5b.2 Post-construction `AttachToProducer`

`SetHeightFn` and `OnSealedBlock = SealedBlockHook(...)` close
back over the producer, which is constructed *after* the
applier. `Wired.AttachToProducer(bp)` does both in one call,
keeping the knot tying explicit and idempotent.

### 5b.3 Test coverage

`internal/v2wiring/v2wiring_test.go` exercises:

- input validation (missing accounts, missing pool,
  reward-over-cap),
- end-to-end enroll flow (admission → pool → producer →
  applier → registry + gauges),
- enrollment admission gate (rejects malformed enroll,
  accepts ordinary transfer, supports
  `ReinstallAdmissionGate` for late-bound POL/BFT
  predicates),
- slashing admission gate (rejects malformed slash, accepts
  well-formed slash into the pool — confirms the stacked
  gate has the slashing layer present),
- `OnSealedBlock` auto-sweep at unbond maturity,
- monitoring state provider replacement on a second `Wire`
  call (no aliasing across boots),
- slasher routability through the aware applier, and
- enrollment query round-trip
  (`GET /api/v1/mining/enrollment/{node_id}` returns a
  fresh `active` view immediately after the enroll lands;
  503 is returned when the registry is not wired).

These fourteen tests are the contract `cmd/qsdm/main.go`
must honour. Any drift between `Wire` and the production
boot sequence is caught here, not on mainnet.

---

## 6. Cross-references

- v2 spec: [`MINING_PROTOCOL_V2_NVIDIA_LOCKED.md`](./MINING_PROTOCOL_V2_NVIDIA_LOCKED.md)
- Phase 0 retirement decision: commit `19e756a`
- Stub verifiers: `pkg/mining/attest/cc/stub.go` (now a fall-through used only when no NVIDIA root is pinned — superseded by `pkg/mining/attest/cc/verifier.go` whenever `attest.ProductionConfig.CCConfig` is populated), `pkg/mining/slashing/verifier.go` (StubVerifier remains in use for the one remaining deferred kind, `freshness-cheat`)
- Concrete `forged-attestation` verifier: `pkg/mining/slashing/forgedattest/forgedattest.go`
- Concrete `double-mining` verifier: `pkg/mining/slashing/doublemining/doublemining.go`
- Production slashing wiring: `pkg/mining/slashing/production.go` + `pkg/mining/slashing/forgedattest/production.go` + `pkg/mining/slashing/doublemining/production.go`
- End-to-end slash tests: `pkg/chain/slash_forgedattest_e2e_test.go`, `pkg/chain/slash_doublemining_e2e_test.go`
- Post-slash auto-revoke: `pkg/mining/enrollment/registry.go` (`RevokeIfUnderBonded`), `pkg/chain/slash_apply.go` (`SlashApplier.AutoRevokeMinStakeDust`); tests in `pkg/mining/enrollment/revoke_underbonded_test.go` and `pkg/chain/slash_apply_autorevoke_test.go`
- Slashing + enrollment Prometheus metrics: `pkg/monitoring/slashing_metrics.go`, `pkg/monitoring/enrollment_metrics.go`, `pkg/monitoring/prometheus_scrape.go`, `pkg/monitoring/enrollment_state_provider.go`
- Slashing + enrollment structured events: `pkg/chain/events.go`, `pkg/monitoring/chain_recorder.go`; tests in `pkg/chain/events_test.go` and `pkg/mining/enrollment/stats_test.go`
- Production boot wiring: `internal/v2wiring/v2wiring.go` + tests in `internal/v2wiring/v2wiring_test.go`; consumed by `cmd/qsdm/main.go`
- Slashing admission + HTTP submission: `pkg/mining/slashing/admit.go`, `pkg/mining/slashing/admit_test.go`, `pkg/api/handlers_slashing.go`, `pkg/api/handlers_slashing_test.go` (`POST /api/v1/mining/slash`)
- Enrollment read endpoint: `pkg/api/handlers_enrollment_query.go` + `pkg/api/handlers_enrollment_query_test.go` (`GET /api/v1/mining/enrollment/{node_id}`)
- Enrollment list endpoint (paginated): `pkg/mining/enrollment/registry.go` exposes `(*InMemoryState).List(ListOptions) ListPage` with cursor-based pagination, lexicographic node_id ordering, optional `Phase` filter (active | pending_unbond | revoked), `DefaultListLimit=50` / `MaxListLimit=500` clamps, and copy-on-return record values; tests in `pkg/mining/enrollment/registry_list_test.go` cover phase filters, cursor exclusivity, full walks, and registry-isolation guarantees. HTTP surface in `pkg/api/handlers_enrollment_list.go` + `pkg/api/handlers_enrollment_list_test.go` (`GET /api/v1/mining/enrollments?cursor=&limit=&phase=`) returns an `EnrollmentListPageView` with `records`, `next_cursor`, `has_more`, `total_matches`, `phase` and reuses `EnrollmentRecordView` for per-record shape so clients only learn one record format. Wired through a narrow `EnrollmentLister` interface (`api.SetEnrollmentLister`) installed alongside the existing query registry by `internal/v2wiring/v2wiring.go`. Integration round-trip in `internal/v2wiring/v2wiring_test.go`.
- Slash receipt read endpoint: `pkg/chain/slash_receipts.go` + `pkg/chain/slash_receipts_test.go` (bounded in-memory store, FIFO eviction, `ChainEventPublisher`-driven), `pkg/api/handlers_slash_query.go` + `pkg/api/handlers_slash_query_test.go` (`GET /api/v1/mining/slash/{tx_id}` with sanitised `SlashReceiptView`). Wired into the boot path via `internal/v2wiring/v2wiring.go`'s `slashReceiptAdapter` (composes the receipt store onto the existing `SlashApplier.Publisher` chain via `chain.NewCompositePublisher`, registers a chain→api adapter via `api.SetSlashReceiptStore`). Round-trip integration coverage in `internal/v2wiring/v2wiring_test.go`.
- v2 mining CLI: `cmd/qsdmcli/mining.go` + `cmd/qsdmcli/mining_test.go`. Subcommands `enroll`, `unenroll`, `slash`, `enrollment-status`, `enrollments` (paginated list with `--phase`, `--limit`, `--cursor`, `--all` flags), `slash-receipt` cover the full v2 HTTP surface. Builds canonical payloads through `pkg/mining/{enrollment,slashing}` so the CLI shares the exact codec the mempool admission gate validates against — no parallel hand-rolled JSON path. Unit tests assert envelope shape, base64 round-trip, missing-flag rejection, hex-decode failure surfacing, HTTP path escaping, and 4xx propagation across all five subcommands.
- Reserved wire keys: §3.1, §3.2, §3.3 of the v2 spec.

---

**Owner action:** None required. This document records *deferred*
scope. It is referenced from §10 of the v2 spec; future contributors
should treat it as the authoritative starting point if/when any of
§§2-4 are picked up.
