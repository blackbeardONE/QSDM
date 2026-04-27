# MINING_PROTOCOL_V2_TIER3_SCOPE.md — Superseded

> **Status:** SUPERSEDED. This file used to track the rolling
> "shipped vs deferred" status of the v2 mining surface. That
> status now lives **inline** in the canonical spec at
> [`MINING_PROTOCOL_V2.md`](./MINING_PROTOCOL_V2.md) — every
> §§5–9 table cross-references the concrete Go file that ships
> the feature, and §12 is the canonical deferred-work register.
>
> This stub is retained so old links keep resolving. New links
> should target the new canonical doc directly.

## Section mapping (old → new)

| Old section | New location |
|---|---|
| §1  Why these items are deferred                       | [`§12`](./MINING_PROTOCOL_V2.md#12-deferred-work-register) (preamble). |
| §2  `nvidia-cc-v1` verifier (datacenter CC GPUs)       | [`§3.2` Wire format](./MINING_PROTOCOL_V2.md#32-attestationbundle-payload--nvidia-cc-v1) for shipped status; [`§12.1`](./MINING_PROTOCOL_V2.md#121-real-world-nvtrust-bundle-framing-for-nvidia-cc-v1) for the deferred `nvtrust` bundle framing. |
| §3  Tensor-Core PoW kernel                             | [`§4`](./MINING_PROTOCOL_V2.md#4-tensor-core-pow-mixin-deferred) (spec) and [`§12.2`](./MINING_PROTOCOL_V2.md#122-tensor-core-pow-kernel) (deferred-work register). |
| §4  Concrete `EvidenceVerifier` implementations        | [`§8.2`](./MINING_PROTOCOL_V2.md#82-slashing) (shipped: `forged-attestation`, `double-mining`); [`§12.3`](./MINING_PROTOCOL_V2.md#123-freshness-cheat-slasher) (deferred: `freshness-cheat`). |
| §5  Suggested ordering                                 | Folded into [`§12`](./MINING_PROTOCOL_V2.md#12-deferred-work-register). |
| §5a Observability for slashing + enrollment            | [`§9.6`](./MINING_PROTOCOL_V2.md#96-observability). |
| §5b Production boot wiring (`internal/v2wiring`)       | [`§9.4`](./MINING_PROTOCOL_V2.md#94-production-boot-wiring-internalv2wiring). |
| §6  Cross-references                                   | [`§14`](./MINING_PROTOCOL_V2.md#14-cross-references). |

## Quick re-references

- **What's shipped today:** the §§5–9 tables of
  [`MINING_PROTOCOL_V2.md`](./MINING_PROTOCOL_V2.md) — every
  cell that says "Shipped" links to the Go file under
  `QSDM/source/`.
- **What's deferred:**
  [`§12`](./MINING_PROTOCOL_V2.md#12-deferred-work-register) —
  four registered items: `nvtrust` framing, Tensor-Core PoW
  kernel, `freshness-cheat` slasher, and `qsdm/gov/v1` runtime
  tuning.
- **CLI surface:**
  [`§9.2`](./MINING_PROTOCOL_V2.md#92-cli--qsdmcli) and
  [`§9.3`](./MINING_PROTOCOL_V2.md#93-slash-helper--offline-evidence-bundle-assembly).
