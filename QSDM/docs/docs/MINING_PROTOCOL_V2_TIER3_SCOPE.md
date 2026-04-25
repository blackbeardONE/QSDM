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
| `pkg/mining/attest/cc/StubVerifier` | Stub. Always returns `ErrNotYetAvailable`. |
| Wire format `Attestation.Type = "nvidia-cc-v1"` | Reserved in spec §3.2.1. |
| Dispatcher routing | `pkg/mining/attest/dispatcher.go` accepts the type key. |
| Production wiring | `pkg/mining/attest/production.go` — CC path is a no-op factory hook. |

### 2.2 What "done" looks like

Replace `cc.StubVerifier` with `cc.RemoteVerifier` that:

1. **Parses the NVIDIA AIK quote** from `Attestation.Bundle` per
   spec §3.2.1 (CBOR-tagged COSE_Sign1 over a TPM-shape quote).
2. **Verifies the AIK certificate chain** terminates in NVIDIA's
   public Hopper/Blackwell root CA (pinned, fetched out-of-band, not
   from the proof itself).
3. **Verifies PCR / RIM measurements** match the
   `policy.allowed_rim_digests` list shipped in `genesis.json`
   under `v2.cc_policy`.
4. **Verifies challenge-nonce binding** — same `Challenge` mechanism
   already used by `nvidia-hmac-v1`, just with the CC-side signing
   key being the AIK instead of an enrolled HMAC secret.
5. **Verifies freshness** against the same `FRESHNESS_WINDOW = 60s`.

### 2.3 Hard external dependencies

- **NGC Attestation Service contract.** NVIDIA gates programmatic
  AIK chain verification behind a paid datacenter relationship.
  Until that contract exists, `cc.RemoteVerifier` cannot validate
  the chain root. Workaround: ship with the chain root pinned to
  the public NVIDIA cert published with each driver release, accept
  some staleness risk.
- **A physical Hopper or Blackwell GPU** for end-to-end test
  vectors. Mock vectors are insufficient because the AIK quote
  format has changed at least twice between H100 driver branches.

### 2.4 Gating

Until §2.2 is delivered, the chain MUST reject `nvidia-cc-v1`
proofs at the verifier layer. The current `StubVerifier` already
does this. The v2 hard fork is therefore safe to activate without
this item.

### 2.5 Scope-of-work estimate (post-hardware)

- AIK quote parser + COSE_Sign1 verify: **3 days**
- Cert chain pinning + rotation policy: **2 days**
- RIM digest policy genesis extension + tests: **1 day**
- E2E vectors against a real H100 / B100: **2 days**
- Total: **~8 days** assuming hardware + NVIDIA contract are in hand
  on day 1.

---

## 3. Tier-3 item: Tensor-Core PoW kernel (`cmd/qsdm-miner-cuda`)

### 3.1 Current state in repo

| Component | Status |
|---|---|
| `pkg/mining/fork.go` | Defines `ProtocolVersionV2` constant. |
| Wire format `Proof.Version` | v2-aware. v1 proofs continue to validate pre-fork. |
| PoW mixin spec §4 | Documented. Not yet implemented. |
| `cmd/qsdm-miner-cuda` | Does NOT exist yet. |
| `cmd/qsdmminer-console` | v1 default; opt-in v2 attestation only (no Tensor-Core mixin). |

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

`cmd/qsdmminer-console` is **frozen at v1** for the duration of
Tier-3 development. It will not be re-shipped against v2 PoW. Per
the Phase-0 retirement decision (commit `19e756a`), the CPU miner
remains in the repo only as the reference v1 implementation; new
miners are directed to wait for `cmd/qsdm-miner-cuda`.

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

Three `EvidenceKind`s are reserved:

| Kind | Detects |
|---|---|
| `forged-attestation` | An HMAC bundle whose MAC verifies but whose `gpu_uuid` is in the deny-list, OR whose enrolled key was never bonded. |
| `double-mining` | Two distinct accepted proofs from the same `(node_id, height)` within the same epoch. |
| `freshness-cheat` | A proof whose `challenge.issued_at` is older than `FRESHNESS_WINDOW` and was nonetheless accepted (i.e. retroactive evidence of validator collusion or clock skew). |

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

`StubVerifier` is registered for all three kinds at v2 genesis,
which means **no slashing is possible** until concrete verifiers
land. This is intentional: it lets the wire format and applier
infrastructure mature on testnet before any real economic
penalty is dispensed.

### 4.5 Scope-of-work estimate

- `forged-attestation` verifier (HMAC re-verification): **2 days**
- `double-mining` verifier (epoch-indexed seen-proofs cache): **3 days**
- `freshness-cheat` verifier (validator-set quorum proof): **4 days,
  plus design review** — this one assumes the chain has BFT
  finality, which we do not yet ship.
- Chain-side `qsdm/slash/v1` applier + replay protection: **3 days**
- Slasher reward economics + governance hook: **2 days**
- Total: **~14 days** assuming the BFT finality layer is in place.

---

## 5. Suggested ordering

If/when Tier-3 work resumes, the recommended order is:

1. **Tensor-Core PoW kernel (§3)** — unblocks miner UX and is the
   main reason consumer GPUs have any reason to exist on this
   chain. Highest market value, lowest external blocker (only
   needs hardware).
2. **`forged-attestation` slasher (§4)** — the cheapest of the
   three slashing verifiers, and the one most likely to be
   exercised in practice (attack surface = full consumer-GPU
   population).
3. **`nvidia-cc-v1` verifier (§2)** — only valuable once we have a
   real datacenter customer. Until then, every line of code here is
   speculative.
4. **`double-mining` + `freshness-cheat` slashers (§4)** — depend
   on BFT finality and on a sufficient validator set to detect
   collusion, neither of which is testnet-day-1.

---

## 6. Cross-references

- v2 spec: [`MINING_PROTOCOL_V2_NVIDIA_LOCKED.md`](./MINING_PROTOCOL_V2_NVIDIA_LOCKED.md)
- Phase 0 retirement decision: commit `19e756a`
- Stub verifiers: `pkg/mining/attest/cc/stub.go`, `pkg/mining/slashing/verifier.go`
- Reserved wire keys: §3.1, §3.2, §3.3 of the v2 spec.

---

**Owner action:** None required. This document records *deferred*
scope. It is referenced from §10 of the v2 spec; future contributors
should treat it as the authoritative starting point if/when any of
§§2-4 are picked up.
