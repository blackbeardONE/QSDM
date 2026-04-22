# AUDIT_PACKET_MINING — external review packet for `mining-01`

> **Status:** Draft for external auditor consumption. Updates are appended via
> PR with the `mining-audit` checklist label. **Not** a substitute for the
> normative spec in [`MINING_PROTOCOL.md`](./MINING_PROTOCOL.md) — this
> document is a *reading guide* + *test coverage matrix* so an auditor can
> establish confidence quickly.
>
> Tracked in [`pkg/audit/checklist.go`](../../source/pkg/audit/checklist.go)
> under the `mining_audit` category, item `mining-01`.

## 0. Scope

This packet covers the QSDM mining sub-protocol (Candidate C: mesh3D-tied
useful PoW) in its **pre-mainnet, pre-Phase-6 state**. It **does not**
cover:

- The CUDA miner kernel — that is a separate packet (`mining-02`) and is
  gated behind Phase 6 of the Major Update.
- The PoE+BFT consensus layer — consensus is deliberately out of scope;
  mining is additive.
- The NVIDIA NGC attestation transparency surface — covered by the
  trust-API guardrail items (`trust-01` through `trust-03` in the audit
  checklist) and by the `trustcheck` binary (`cmd/trustcheck`).

The auditor's remit is to confirm that the **pure-Go reference
implementation** of the mining protocol matches the spec, is bit-for-bit
deterministic across implementations, and admits no economic or DoS
vector that would let an attacker halt or fork PoE+BFT consensus through
the mining surface.

## 1. Reading order

Auditors should read in this exact order:

1. [`MINING_PROTOCOL.md`](./MINING_PROTOCOL.md) — normative spec. Sections
   **§2 (algorithm)**, **§5 (proof function)**, **§6 (attestation)**,
   **§7 (validator pipeline)**, **§9 (consensus safety)**, and
   **§11 (fork rules)** are the load-bearing chapters.
2. [`CELL_TOKENOMICS.md`](./CELL_TOKENOMICS.md) — reward schedule
   assumptions the mining package relies on. Interacts with mining only
   via the deterministic [`pkg/chain/emission.go`](../../source/pkg/chain/emission.go)
   calculator.
3. [`NODE_ROLES.md`](./NODE_ROLES.md) — validator / miner split and
   `pkg/mining/roleguard`.
4. [`REBRAND_NOTES.md`](./REBRAND_NOTES.md) §4 — the Phase-0 values
   adopted as working hypotheses in-repo.
5. Package doc comments:
   - `pkg/mining/doc.go`
   - `pkg/mining/roleguard/doc.go`

Then the source files, in decreasing load-bearing order:

| # | File | What lives there |
|---|------|-------------------|
| 1 | `pkg/mining/proof.go` | Canonical-JSON `Proof` codec, `Attestation`, `ID()` derivation. Any serialization drift here **forks the network**. |
| 2 | `pkg/mining/pow.go` | The proof-of-work hash function and target comparison. Auditor's primary focus for soundness. |
| 3 | `pkg/mining/verifier.go` | Step-by-step validator pipeline mirroring spec §7. |
| 4 | `pkg/mining/difficulty.go` | Retargeting maths (§8). Integer-only, no floats. |
| 5 | `pkg/mining/epoch.go` | Emission epoch, mining epoch, and work-set rotation (§3). |
| 6 | `pkg/mining/dag.go` | Parent-cell batch DAG construction that ties §2.1. |
| 7 | `pkg/mining/solver.go` | Reference CPU solver used by `cmd/qsdmminer`. Not on the critical path for consensus safety but important for test symmetry. |
| 8 | `pkg/mining/roleguard/*` | Startup guard that prevents a validator binary from mining or a miner binary from signing blocks. Small, but high-leverage. |
| 9 | `pkg/chain/emission.go` | Pure-integer halving-schedule calculator. |

Everything in this package is stdlib-only + `golang.org/x/crypto/sha3`.
There are **no third-party math, crypto, or GPU dependencies** reachable
from the consensus-safety surface under audit.

## 2. Threat model

The auditor should evaluate the implementation against at least the
following adversary classes.

### 2.1 A_rogue_miner — a single mining party with unlimited GPU

Capabilities: arbitrary hashrate, arbitrary number of miner identities,
full knowledge of the spec and codebase, can publish proofs on any
topic.

Invariants the protocol MUST uphold:

- **I-1 Consensus liveness.** No stream of valid or invalid proofs can
  stall block production. Verification is bounded time (§1.1 goal 4);
  invalid proofs are cheaply rejected and do not enter state.
- **I-2 Emission cap.** No sequence of proofs can mint more than the
  schedule in `CELL_TOKENOMICS.md` specifies for the current block
  height, regardless of timing, fork selection, or reorg depth.
- **I-3 No validator capture.** A miner that accumulates 51%+ hashrate
  MUST NOT thereby gain any BFT vote weight, signing key material, or
  ability to finalize blocks.

### 2.2 A_rogue_validator — a BFT validator trying to steal rewards

Capabilities: signs blocks, can drop, reorder, or include arbitrary
mining proofs in blocks they produce.

Invariants:

- **I-4 Non-discriminatory inclusion.** A valid proof with a later
  `attested_at` than a block's parent MUST be includable; censorship
  requires collusion at the BFT quorum level, not at the individual
  validator level.
- **I-5 Address binding.** A validator MUST NOT be able to rewrite
  `miner_addr` on a proof they relay: the PoW hash commits to the
  address.
- **I-6 No self-mining loophole.** A validator acting as their own
  miner earns the same reward path as any other miner; there is no
  code path that favors miner≡validator.

### 2.3 A_network_adversary — a partitioner or stale-view propagator

Capabilities: can delay, drop, duplicate, or reorder `pkg/mining` P2P
topic messages. Cannot decrypt traffic or forge signatures.

Invariants:

- **I-7 Idempotent ingestion.** Replays of a previously-seen proof MUST
  be dropped before any expensive validation work.
- **I-8 Bounded state.** Unbounded inbound proof streams MUST not grow
  in-memory state without limit; cap is expressed per-epoch.

### 2.4 A_determinism_tripper — two honest validators trying to agree

Not an adversary per se. The pair of validators is honest, well-
connected, and running the same binary. The scenario is: given the
same block header and the same candidate proof, both validators must
return bit-for-bit identical `(accepted, reward_dust)` tuples.

Invariants:

- **I-9 No float math in consensus path.** `verifier.go`, `difficulty.go`,
  and `emission.go` MUST use `math/big` or fixed-width integer types
  for every quantity that enters a consensus decision.
- **I-10 Stable JSON codec.** Field order, map iteration, and numeric
  representation in `proof.go`'s canonical codec MUST be stable across
  Go versions and host architectures.

## 3. Invariants → source-location map

An auditor's-eye index: every invariant above should have a clearly
identifiable implementation site and at least one test.

| Invariant | Primary site | Secondary sites | Tests |
|-----------|--------------|-----------------|-------|
| I-1 | `pkg/mining/verifier.go` (bounded-time checks) | `pkg/mining/pow.go` (early-reject on target miss) | `verifier_test.go/TestVerifier_RejectsMalformed*`, `pow_test.go` (benchmarks to be added) |
| I-2 | `pkg/chain/emission.go` | `pkg/mining/verifier.go` (reward assignment) | `pkg/chain/emission_test.go`, `pkg/mining/verifier_test.go/TestVerifier_RewardMatchesSchedule` |
| I-3 | `pkg/mining/roleguard/roleguard.go` | `cmd/qsdmplus/main.go` startup checks | `roleguard_test.go`, `roleguard_validator_test.go` |
| I-4 | `pkg/mining/verifier.go` (no `miner_addr` allow-list) | n/a | `verifier_test.go/TestVerifier_AcceptsAnyAddress` |
| I-5 | `pkg/mining/proof.go` (`ID()` commits to every field) | `pkg/mining/pow.go` (hash over canonical bytes) | `proof_test.go/TestProof_IDStable`, `TestProof_IDChangesOnFieldChange` |
| I-6 | `pkg/mining/verifier.go` | n/a | `verifier_test.go/TestVerifier_IdempotentValidatorIsMiner` |
| I-7 | `pkg/mining/verifier.go` (seen-set cache) | `pkg/mining/proof.go/ID()` | `verifier_test.go/TestVerifier_DuplicateProofRejected` |
| I-8 | `pkg/mining/epoch.go` (work-set bounds) | `pkg/mining/verifier.go` (cache bounds) | `epoch_test.go` |
| I-9 | `pkg/mining/pow.go`, `difficulty.go`, `pkg/chain/emission.go` | n/a | `difficulty_test.go` asserts integer-only paths |
| I-10 | `pkg/mining/proof.go` (`canonicalJSON`) | n/a | `proof_test.go/TestCanonicalJSON_*` |

## 4. Test coverage matrix (current snapshot)

Run this locally to reproduce:

```powershell
cd QSDM/source
$env:CGO_ENABLED = "0"
$env:QSDM_METRICS_REGISTER_STRICT = "1"
go test -tags=validator_only -count=1 -short -timeout 10m `
  ./pkg/mining/... `
  ./pkg/mining/roleguard/... `
  ./pkg/chain/... `
  ./pkg/config/... `
  ./pkg/branding/... `
  ./cmd/trustcheck/...
```

Expected tail: every package `ok`, zero `--- FAIL`. Matching Linux
invocation lives in `.github/workflows/qsdm-split-profile.yml` (the
validator matrix cell).

Packages and what they exercise:

| Package | Lines of test | Focus |
|---------|----------------|-------|
| `pkg/mining` | proof codec round-trips, PoW hash determinism, epoch rotation, difficulty retarget behaviour, verifier pipeline | pure-Go, stdlib + `x/crypto/sha3` |
| `pkg/mining/roleguard` | every combination of `(NodeRole, MiningEnabled, BuildProfile)` that startup might see | startup fast-fail |
| `pkg/chain` (emission) | schedule boundary cases: height 0, height right before halving, right after halving, past the cap | integer-only |

## 5. Parameters under audit (not yet locked)

The following **values** are declared in the repo but are explicitly
listed as audit-gated in `MINING_PROTOCOL.md`:

| Parameter | Current in-repo value | Locked by |
|-----------|-----------------------|-----------|
| Proof function family | SHA3-256 over canonical proof bytes | §5 |
| Memory-hardness knob | TBD (Candidate C default) | §5 |
| Difficulty retarget window | documented in §8 | §8 |
| Block reward curve | halvings every 4 emission epochs | `CELL_TOKENOMICS.md` |
| Mining epoch duration | 1 week wall-clock | §3 |

Any change to these values after this audit concludes must go through a
fresh PR with both the spec update and the matching test update in the
same commit, labelled `mining-audit-rerun-required`.

## 6. Reproducible build

Go version, modules, and tags used for every CI artifact:

```text
go version            # as pinned by QSDM/source/go.mod
go env GOMOD          # must resolve to QSDM/source/go.mod
CGO_ENABLED=0         # validator profile has zero C deps
build tag             # -tags=validator_only for the consensus-safety build
```

Deterministic-build recipe:

```bash
cd QSDM/source
GOFLAGS="-trimpath -buildvcs=false" \
CGO_ENABLED=0 \
go build -tags=validator_only -ldflags="-buildid=" \
  -o qsdm-validator ./cmd/qsdmplus
sha256sum qsdm-validator
```

The SHA-256 of the artifact MUST match between two independent
machines running the same Go toolchain and the same commit hash. Drift
is a bug; please file.

## 7. How to file findings

The preferred channel during the audit window is a private, encrypted
email to the address published in the audit engagement letter. Each
finding should quote:

- commit hash the finding was discovered on,
- invariant label (`I-1` … `I-10`, or a proposed new label),
- affected file(s) and line range,
- reproduction recipe (shell commands, inputs, expected vs observed),
- suggested severity (`critical` / `high` / `medium` / `low` /
  `informational`) — final severity is assigned by the QSDM foundation
  after triage.

Findings that touch the consensus safety surface (invariants I-1, I-2,
I-3) are handled under the QSDM security disclosure policy; **please
do not open a public GitHub issue** until a fix has shipped.

## 8. Out-of-scope-but-related

These items are adjacent to mining but have their own audit packets or
review channels and should **not** be included in the `mining-01`
report:

- CUDA kernel correctness → `mining-02` (Phase 6).
- NGC bundle verification internals → `trust-02`.
- BFT quorum maths → separate consensus review already carried out by
  the Phase 0 cryptography consultant.
- Tokenomics sign-off → `tok-01` (counsel & foundation, not an auditor
  deliverable).

## 9. Changelog

| Date | Revision |
|------|----------|
| 2026-04-22 | Initial packet, aligned to Major Update Phase 5 post-execution state. |
