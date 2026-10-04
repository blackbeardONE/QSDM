# NVIDIA-lock: HTTP, P2P, and consensus scope

> [!IMPORTANT]
> **Historical scope note.** Since mining protocol v2 activated, every mining
> proof must carry an NVIDIA attestation (`nvidia-cc-v1` or `nvidia-hmac-v1`);
> see [Mining protocol v2](MINING_PROTOCOL_V2.md). The NGC sidecar API gate
> described below remains optional and per-operator.

> **Referenced by** the verbatim scope-note string in every
> `/api/v1/trust/attestations/summary` response and in the "Attestation
> transparency" widget on `qsdm.tech`. If you follow the "Details →"
> link from either of those surfaces you landed here.

This document clarifies what the current implementation enforces versus
what would require consensus or network-wide agreement. It is the
normative reference for the anti-claim language used across the trust
endpoints (`pkg/api/handlers_trust.go`) and the landing-page transparency
widget (`deploy/landing/index.html`, `deploy/landing/trust.html`).

## The one sentence that matters

> **NVIDIA-lock is an opt-in, per-operator API policy — not a consensus rule.**

This sentence describes the `nvidia_lock` API gate (NGC proof bundles on
selected HTTP routes and optional P2P storage policy). It does not describe
mining: mining on the QSDM pilot network requires a valid NVIDIA attestation
(`nvidia-cc-v1` or `nvidia-hmac-v1`) at consensus. Separately, the optional
`nvidia_lock` API setting is a per-node policy for selected HTTP routes and is
not a consensus rule.

## What the QSDM code actually does

- **HTTP API (per-node).** When the operator enables `nvidia_lock`,
  selected state-changing routes require a fresh NGC proof bundle
  before the request is admitted. Proof freshness, optional HMAC,
  ingest nonce tracking, and rate limits apply at the **local node's
  API boundary**. Neighbouring nodes are unaffected.
- **Dashboard (per-node, read-only, operator only).** Summarises NGC
  proof counters and Prometheus metrics for the operator. Scrape auth is
  JWT or `metrics_scrape_secret`.
- **Optional P2P storage policy (per-node).** When
  `nvidia_lock_gate_p2p` is set, transactions received over libp2p may
  be **rejected before local storage** if they do not carry a
  qualifying proof. This is **not** a consensus rule: other peers are
  not required to agree, and the tx may still be accepted elsewhere on
  the network.
- **Trust transparency endpoints (per-node, public, read-only).**
  Introduced in Major Update Phase 5 (§8.5). Expose aggregate counts —
  never raw attestations — under `/api/v1/trust/attestations/*`. The
  scope note that points to this document is served verbatim and also
  embedded in the landing page so third parties can independently
  observe "X of Y validators publish fresh attestations" without
  credentials. See `docs/docs/history/MAJOR_UPDATE_EXECUTED.md` §8.5.2
  for the guardrails.

## What the API gate does **not** do

- **Block validity / consensus.** Validators do **not** require a GPU
  attestation in block headers, transaction inclusion rules, view
  change votes, or finality signatures. (Mining proofs are different:
  see the note at the top of this page.) A block accepted under current
  consensus rules could contain txs that would have been blocked on a
  strict API node — that's working as intended.
- **Cross-node enforcement.** There is no protocol message that proves
  "this tx was NVIDIA-lock-checked on peer X." Each operator
  configures their node independently.

## Why this matters

The project has a standing commitment not to turn the API gate's
attestation posture into a silent gating rule. One failure mode is
explicitly excluded:

1. **"Attested validators stat."** The widget renders `X of Y`,
   **never** `X` alone. A number without a denominator is an over-claim.
   The `total_public` field is always present and always ≥ `attested`.

## If block-level attestation were ever desired (design sketch, NOT a commitment)

A future design would need at minimum:

- a normative **proof or commitment** in the block or tx merkle
  metadata (canonical encoding mandatory),
- **validator rules** that reject blocks violating those rules,
- **fork choice** implications for a network that partly upgrades,
- and a distinct hard-fork label — this is **not** an extension of
  NVIDIA-lock; it is a separate project.

Such a design is explicitly out of scope for the Major Update and
would require its own spec, independent review, governance vote, and
migration window. Nothing in the current codebase should be read as
foreshadowing it.

## References

- [`history/MAJOR_UPDATE_EXECUTED.md`](./history/MAJOR_UPDATE_EXECUTED.md) — Major Update, especially §5.4, §8.4, §8.5.
- [`MINING_PROTOCOL_V2.md`](./MINING_PROTOCOL_V2.md) — current mining protocol; NVIDIA attestation required on every proof.
- [`NODE_ROLES.md`](./NODE_ROLES.md) — validator / miner split.
- [`REBRAND_NOTES.md`](./REBRAND_NOTES.md) §4 — Phase-0 working values pinned in-repo.
- [`ROADMAP.md`](./ROADMAP.md) — operational detail and forward-looking pointers.
- `pkg/api/handlers_trust.go` — the Go source that emits the scope-note string this document is linked from.
- `cmd/trustcheck/` — third-party scraper that asserts the scope-note string matches, byte-for-byte, what this document reinforces.
