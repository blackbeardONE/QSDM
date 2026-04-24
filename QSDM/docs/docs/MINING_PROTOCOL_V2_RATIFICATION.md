# MINING_PROTOCOL_V2_RATIFICATION.md

> **Purpose.** Per
> [`MINING_PROTOCOL_V2_NVIDIA_LOCKED.md` §11](./MINING_PROTOCOL_V2_NVIDIA_LOCKED.md#11-open_question-summary),
> Phase 2 implementation is blocked until the three OPEN_QUESTIONs
> are resolved with explicit owner sign-off. This file records that
> sign-off so Phase 2 code lands against a committed decision
> record, not an ambient understanding.

## Sign-off

- **Ratified by:** project owner, in-chat decision
- **Ratified on:** 2026-04-24
- **Spec version:** `6826bc4` —
  `QSDM/docs/docs/MINING_PROTOCOL_V2_NVIDIA_LOCKED.md` as authored
  in that commit
- **Ratification channel:** Cursor chat session
  `abae084d-c682-4845-9a7a-255cc20a943a`

## Decisions

### OPEN_QUESTION_1 — Trust-anchor model (§5.6)

> Accept the tiered trust-anchor recommendation? NVIDIA-CC-pinned
> for Hopper / Blackwell Confidential-Computing GPUs, plus
> Registered-operator HMAC for consumer RTX cards.

**Ratified: TIERED (recommended option).**

Rationale:

- Keeps consumer NVIDIA GPUs (Turing / Ampere / Ada) eligible to
  mine. Without a consumer path the chain is effectively a
  datacenter-only product.
- Gives CC-capable datacenter GPUs a strictly stronger crypto
  guarantee than the HMAC path. Operators who have paid for CC
  hardware get the attestation value they paid for.
- Implementation cost is manageable — both paths compose with
  existing code (the HMAC path extends
  `pkg/monitoring/nvidia_hmac.go`; the CC path uses `crypto/x509`
  plus a pinned-root genesis extension).

Two `Attestation.Type` values land in Phase 2:
`"nvidia-cc-v1"` and `"nvidia-hmac-v1"`. Verifier dispatches
on `Attestation.Type`.

### OPEN_QUESTION_2 — `MIN_ENROLL_STAKE` (§5.4)

> Initial enrollment stake required to register a
> `(node_id, gpu_uuid, hmac_key)` tuple in the
> `nvidia-hmac-v1` operator registry.

**Ratified: `10 CELL` (recommended option).**

Encoded as `MinEnrollStake = 10 * 10^8` dust in the v2 genesis
ceremony bundle. Governance may adjust post-launch via the
chain-config delta mechanism defined in `§5.2` of the spec.

Rationale:

- Low enough that a miner with roughly one day of pre-mining can
  self-fund enrollment, which keeps onboarding accessible.
- High enough that thousand-GPU Sybil enrollments cost
  10,000 CELL locked for 30 days — comparable to the cost of
  the GPUs themselves, so not a free attack.

### OPEN_QUESTION_3 — `FRESHNESS_WINDOW` (§6.2)

> Maximum age of an attestation nonce / issued-at timestamp
> before a proof carrying it becomes stale.

**Ratified: `60 seconds` (recommended option).**

Rationale:

- Short enough that a replayed bundle becomes invalid within one
  block-production cycle.
- Long enough that a miner on a slow residential link has time
  to fetch a challenge, compute a proof, and submit it without
  false-positive rejection.
- Symmetric around the validator nonce ring-buffer retention
  (`2 * FRESHNESS_WINDOW = 120 s`) which the spec defines for
  same-challenge double-spend protection.

## What Phase 2 inherits

With the three above resolved, Phase 2 implementation is
unblocked and proceeds against these constants:

```go
// pkg/mining/fork.go (to be added in Phase 2)

// FRESHNESS_WINDOW is the maximum age of an attestation nonce
// in the v2 protocol. Per MINING_PROTOCOL_V2 §6.2 and the
// 2026-04-24 ratification, this is 60 seconds.
const FreshnessWindow = 60 * time.Second

// MinEnrollStakeDust is the minimum stake (in dust; 1 CELL =
// 10^8 dust) an operator must lock to register a
// (node_id, gpu_uuid) tuple in the nvidia-hmac-v1 registry.
// Per ratification this is 10 CELL.
const MinEnrollStakeDust = 10 * 100_000_000

// AttestationTypeCC is the whitelisted Attestation.Type string
// for Hopper / Blackwell Confidential-Computing GPU
// attestations. Dispatched to pkg/mining/attest/cc.
const AttestationTypeCC = "nvidia-cc-v1"

// AttestationTypeHMAC is the whitelisted Attestation.Type
// string for registered-operator consumer-GPU HMAC
// attestations. Dispatched to pkg/mining/attest/hmac.
const AttestationTypeHMAC = "nvidia-hmac-v1"
```

`FORK_V2_HEIGHT` is deliberately deferred to Phase 4 — Phase 2
and 3 implement the v2 code paths behind a not-yet-activated
fork gate. The gate activates at genesis reset (Phase 4), at
which point `FORK_V2_HEIGHT = 0` because we are launching a
fresh chain.

## Revocation

This ratification can be revisited at any time by a new sign-off
recorded as an additional section in this file. Changing a
ratified parameter after Phase 2 code has shipped may require
corresponding code changes and should be coordinated with the
existing activation plan.
