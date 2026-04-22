# Cell (CELL) — Tokenomics

> **Status:** Ratified per the Major Update Phase 0 recommendation, awaiting
> counsel review before mainnet genesis. The values in this document are the
> authoritative in-repo numbers used by `pkg/branding` constants,
> `pkg/chain/emission` (Phase 3.3), and the tokenomics dashboard panel. They
> MUST NOT be changed in isolation; any change requires a corresponding
> update to `QSDM/docs/docs/REBRAND_NOTES.md` and a new entry in
> `NEXT_STEPS.md`.

This document is the normative reference for the Cell coin's supply,
emission, and fee model. It is **not** a legal offering document, it is not
an investment prospectus, and it is not a commitment until counsel has
signed off. See §8 ("Legal posture") below.

---

## 1. Coin identity

| Field | Value | Defined in |
|---|---|---|
| Name | `Cell` | `pkg/branding.CoinName` |
| Symbol | `CELL` | `pkg/branding.CoinSymbol` |
| Decimals | `8` | `pkg/branding.CoinDecimals` |
| Smallest unit | `dust` (1 CELL = 10^8 dust) | `pkg/branding.SmallestUnitName` |
| Issued by | QSDM mainnet, PoW emission layer | see §3 |
| Non-issuance paths | validator fees (never mint new supply) | see §5 |

The 8-decimal choice intentionally mirrors Bitcoin UX: wallet developers,
block explorers, and exchanges already know how to display 8-decimal coins
correctly, and the smallest-unit name `dust` is short and phonetic for CLI
output (`amount: 12345 dust`).

---

## 2. Supply model

| Parameter | Value |
|---|---|
| Total cap | **100,000,000 CELL** (100 M) |
| Pre-mine | **0%** |
| Genesis treasury allocation | **10%** (10,000,000 CELL) |
| Treasury vesting | linear over 48 months, enforced on-chain, locked at genesis |
| Mining emission | **90%** (90,000,000 CELL) over ~20 years |
| Emission curve | halvings every 4 years (see §3) |
| Validator block subsidy | **0** — validators earn only transaction fees |
| Base-fee burn | **undecided** — EIP-1559-style burn is *optional*; decision due before genesis (Phase 0 follow-up item) |

### 2.1 Treasury address

The treasury address is committed in the genesis block. Its balance is
public and its spend policy is enforced by a WASM contract (bound at
genesis) that releases (total_alloc / 48) CELL per month to a
multisig-controlled spend address. The contract address, the multisig
membership, and the spend policy are published in
`QSDM/docs/docs/GENESIS.md` (Phase 0 deliverable, pending counsel review).

### 2.2 Pre-mine: why zero

The project commits to a **fair launch**: no public sale, no private sale,
no ICO, no presale, no founder allocation. All 90 M mining-emission CELL
must be earned by a miner producing a valid PoW proof. This is the cleanest
posture against US/UK/EU securities classification; it is the single most
important decision on this page and it is **not** negotiable in the current
design window without revisiting Phase 0.

---

## 3. Emission schedule

Assumes a target block time of **10 seconds** (matches the current
`pkg/chain` default; see §3.2 for the sensitivity analysis if this changes).
Blocks per 4-year epoch = 12,623,040 (= 4 × 365.25 × 86400 / 10, using the
Julian year so leap years are absorbed uniformly).

The per-block reward is computed as `floor(epoch_allocation_dust /
blocks_per_epoch)` using integer math — these are the EXACT values
returned by `pkg/chain.EmissionSchedule.BlockRewardDust` at each epoch
boundary, not rounded display values. See `pkg/chain/emission.go` and its
unit tests for the canonical definition.

| Epoch | Years | Epoch allocation (CELL) | Block reward (dust) | Block reward (CELL) | Cumulative (CELL, approx) | % of mining cap |
|---|---|---:|---:|---:|---:|---:|
| 0 | 0–4 | 45,000,000 | 356,490,987 | 3.56490987 | 44,999,999.88 | 50.00% |
| 1 | 4–8 | 22,500,000 | 178,245,493 | 1.78245493 | 67,499,999.80 | 75.00% |
| 2 | 8–12 | 11,250,000 | 89,122,746 | 0.89122746 | 78,749,999.74 | 87.50% |
| 3 | 12–16 | 5,625,000 | 44,561,373 | 0.44561373 | 84,374,999.68 | 93.75% |
| 4 | 16–20 | 2,812,500 | 22,280,686 | 0.22280686 | 87,187,499.62 | 96.875% |
| 5 | 20–24 | 1,406,250 | 11,140,343 | 0.11140343 | 88,593,749.56 | 98.4375% |
| 6 | 24–28 | 703,125 | 5,570,171 | 0.05570171 | 89,296,874.50 | 99.2188% |
| 7+ | 28+ | halves every 4y | halves | halves | → 90,000,000 asymptotically | → 100% |

The small (~0.12 CELL per epoch) cumulative shortfall versus the nominal
"50% / 75% / 87.5% / ..." percentages is the integer-division truncation
residue described in §3.1. It is deterministic, unavoidable in exact
integer arithmetic, and bounded — `ConvergenceCheck` verifies the total
shortfall across all epochs stays under 0.00001 % of the 90 M CELL cap.

### 3.1 Invariants enforced by `pkg/chain/emission` (Phase 3.3)

The emission calculator in `pkg/chain/emission.go` is the single source of
truth for runtime. It is pure Go (no CGO), deterministic, and exercised by
the Phase 3.3 unit tests that verify:

1. The sum of per-block rewards over epochs 1..N, extended to N → ∞,
   converges to exactly **9,000,000,000,000,000 dust** (= 90 M CELL).
2. The per-block reward is computed from integer math only (no floating
   point), so two validators on different architectures always agree.
3. The transition block between epochs is well-defined and matches the
   schedule above.
4. If the target block time is re-configured before genesis, the schedule
   re-derives but the total cap stays exactly 90 M CELL.

### 3.2 Sensitivity to block-time changes

If target block time changes, the per-block reward changes but the total
cap does not: the calculator computes per-block reward as
`epoch_allocation_dust / blocks_per_epoch` where `blocks_per_epoch` is
derived from the target block time. Operators changing block time
post-genesis are effectively changing inflation per unit time; the hard-cap
invariant is preserved.

### 3.3 First-halving verification

The first halving is a one-way, non-reversible event. Before mainnet, the
incentivized testnet (Major Update Phase 4, wall-clock bound) MUST run a
compressed-time simulation of the first halving and verify:

- No validator disagreement on the exact halving block.
- No miner accepts a stale reward (old epoch reward) for the first block
  of the new epoch.
- The cumulative-emission invariant holds to the dust across the boundary.

---

## 4. What Cell is used for

| Use | Notes |
|---|---|
| Transaction fees | Paid to validators. Denominated in dust. |
| WASM contract gas | Gas price in dust/gas; already wired in `pkg/wasm`. |
| Validator bonds | Future: validators stake CELL to participate; slashable. |
| Bridge collateral | Future: denominate bond in CELL via `pkg/bridge`. |
| Governance weight | Future: one CELL = one vote, in proposal-weighted voting. |

Cell is **not** used to pay mining rewards out of a pre-minted pool; mining
rewards are newly minted at block-confirmation time (a `mint` transaction
embedded in the block by the validator who proposes, crediting the winning
miner). This is the only path that creates new supply.

---

## 5. Fee model

Validators never dilute holders. All validator revenue comes from transaction
fees on real user transactions. This split is enforced at the protocol
level: a mining-reward transaction with `role = miner` is the **only**
transaction allowed to mint new supply, and a block producer can include at
most one such transaction. A validator that attempts to self-pay via a
mining-reward transaction is producing an invalid block.

### 5.1 Optional EIP-1559-style base-fee burn

The project leans toward adopting an EIP-1559-style split (base fee burned,
priority fee to the validator) because it adds a deflationary pressure
component that offsets emission during epochs 1–4, and because it is the
mechanism users and exchanges already understand. The decision is **open**
and must be made before genesis. It is tracked in `NEXT_STEPS.md` as a
Phase-0 follow-up.

---

## 6. Comparison to Bitcoin

| Dimension | Bitcoin | Cell |
|---|---|---|
| Total cap | 21,000,000 BTC | 100,000,000 CELL |
| Decimals | 8 | 8 |
| Halving period | every 210,000 blocks (~4 years at 10-min blocks) | every 12,614,400 blocks (~4 years at 10-sec blocks) |
| Block time | ~10 min | 10 sec (target) |
| Initial reward | 50 BTC | 1.4280 CELL |
| Pre-mine | 0 | 0 |
| Treasury | none | 10% vested linearly over 48 months |
| Consensus | pure PoW | PoE + BFT **for consensus**, additive PoW **for emission only** |

The structural difference from Bitcoin is that Cell's PoW layer is
explicitly *additive* — it exists solely to meter coin emission. Consensus
does not depend on PoW. If all miners went offline tomorrow, the validators
would continue producing blocks; the only thing that would stop is new
supply creation.

---

## 7. Distribution philosophy

**Fair launch, utility-first.** There is no presale, no ICO, no public sale,
no private sale, no airdrop of founder tokens. The treasury allocation is
published on-chain at genesis, time-locked by a WASM contract, and spent
only on development per a public policy document
(`QSDM/docs/docs/TREASURY_POLICY.md`, Phase 0 deliverable).

Earning paths at launch are:
- **Mining**: buy a GPU, run `qsdm-miner`, earn Cell by producing valid
  proofs.
- **Validating**: operate a VPS validator node, earn transaction fees in
  Cell.
- **Using**: use Cell to pay transaction fees on a service you care about.

The project does not sell Cell. Exchanges that choose to list Cell after
launch operate independently; the QSDM project does not negotiate listing
allocations and does not provide market-making inventory.

---

## 8. Legal posture

This document describes a fair-launch utility token. None of the parameters
above are structured to accommodate a securities offering. The combination
of (a) zero pre-mine, (b) zero founder allocation, (c) public on-chain
treasury with published vesting, (d) coin is minted only in exchange for
work (PoW) or consumed in exchange for network service (fees), and (e) no
sale by the project at any time, is the strongest posture available to a
non-custodial blockchain project against the US Howey test and the EU MiCA
"asset-referenced token" / "e-money token" categories.

This is a posture, not a guarantee. Counsel review is required before
mainnet genesis and before any promotional language is published on
`qsdm.tech` or elsewhere. The phrases "investment", "returns", "profit",
and "yield" are forbidden in all project communications (see
`QSDM/docs/docs/COPY_FILTERS.md`, Phase 5.4 deliverable).

---

## 9. Changelog

| Date | Change |
|---|---|
| Major Update Phase 3.1 | Document created; numbers adopted from Major Update §4.1–§4.4 with Phase 0 "ratified per recommendation, awaiting counsel review" status. |

