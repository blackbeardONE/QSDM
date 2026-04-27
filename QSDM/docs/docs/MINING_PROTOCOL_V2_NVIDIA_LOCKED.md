# MINING_PROTOCOL_V2_NVIDIA_LOCKED.md — Superseded

> **Status:** SUPERSEDED. This file was the original Phase-1
> design draft for the v2 mining protocol. The normative spec
> now lives at
> [`MINING_PROTOCOL_V2.md`](./MINING_PROTOCOL_V2.md).
>
> This stub is retained so old links (PRs, issues, the landing
> page, code comments referencing
> `MINING_PROTOCOL_V2_NVIDIA_LOCKED.md`) keep resolving. New
> links should target `MINING_PROTOCOL_V2.md` directly.
>
> **Where did the content go?** Section-by-section mapping below.
> The verbatim original is preserved in git history at commit
> `6826bc4` of this path.

## Section mapping (old → new)

| Old section | New location |
|---|---|
| §0  Executive summary               | [`MINING_PROTOCOL_V2.md §0`](./MINING_PROTOCOL_V2.md#0-executive-summary) |
| §1  What changes relative to v1     | [`MINING_PROTOCOL_V2.md §1`](./MINING_PROTOCOL_V2.md#1-what-changes-relative-to-v1) |
| §2  What does NOT change            | [`MINING_PROTOCOL_V2.md §2`](./MINING_PROTOCOL_V2.md#2-what-does-not-change) |
| §3  Wire format                     | [`MINING_PROTOCOL_V2.md §3`](./MINING_PROTOCOL_V2.md#3-wire-format) |
| §4  Tensor-Core PoW mixin           | [`MINING_PROTOCOL_V2.md §4`](./MINING_PROTOCOL_V2.md#4-tensor-core-pow-mixin-deferred) |
| §5  Trust anchors (recommendation)  | [`MINING_PROTOCOL_V2.md §5`](./MINING_PROTOCOL_V2.md#5-trust-anchors) (the recommendation is now ratified — §13.1) |
| §6  Freshness window                | [`MINING_PROTOCOL_V2.md §6`](./MINING_PROTOCOL_V2.md#6-freshness-window--nonce-issuance) |
| §7  Verifier state                  | [`MINING_PROTOCOL_V2.md §7`](./MINING_PROTOCOL_V2.md#7-verifier) |
| §8  Activation mechanics            | [`MINING_PROTOCOL_V2.md §10`](./MINING_PROTOCOL_V2.md#10-activation-mechanics--hard-fork) |
| §9  Attacker model                  | [`MINING_PROTOCOL_V2.md §11`](./MINING_PROTOCOL_V2.md#11-attacker-model) |
| §10 Implementation phase map        | [`MINING_PROTOCOL_V2.md §12`](./MINING_PROTOCOL_V2.md#12-deferred-work-register) (status, what shipped) and [`§13`](./MINING_PROTOCOL_V2.md#13-historical-decision-record) (decisions). |
| §11 OPEN_QUESTION summary           | [`MINING_PROTOCOL_V2.md §13`](./MINING_PROTOCOL_V2.md#13-historical-decision-record) (all three questions ratified 2026-04-24). |

## What about the on-chain enrollment / slashing surface?

That body of work was added during Phase 2c+ and is documented
in the canonical doc at
[`MINING_PROTOCOL_V2.md §8`](./MINING_PROTOCOL_V2.md#8-on-chain-enrollment--slashing).
It is not present in this superseded original.
