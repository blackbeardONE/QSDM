# Cell (CELL) — Tokenomics

> **Status:** [Genesis Policy v1.0](GENESIS_POLICY_v1.0.md): **ratified by
> owner 2026-10-16, awaiting signature and counsel review.** Maximum supply
> 90,000,000 CELL, all from mining; no genesis treasury (the earlier
> 10,000,000 CELL treasury proposal is withdrawn); transaction fees are burned
> (credited to no account) until independent validators produce blocks;
> mainnet continues the current chain through a published, deterministic
> migration. Policy SHA-256, signature, migration manifest hash and fork
> height: not yet published (they will be recorded here). Audit row `tok-01`
> stays **pending** until counsel's written opinion is received.
>
> A few code constants and tools outside the emission schedule still carry the
> withdrawn treasury figure; aligning them is a separate, reviewed code change.
>
> The values in this document are the
> authoritative in-repo numbers used by `pkg/branding` constants,
> `pkg/chain/emission` (Phase 3.3), and the tokenomics dashboard panel. They
> MUST NOT be changed in isolation; any change requires a corresponding
> update to `QSDM/docs/docs/REBRAND_NOTES.md` and a new entry in
> `NEXT_STEPS.md` (not yet published).
>
> The chain running today is a pilot network (pre-mainnet), run by a single
> block producer during recovery. Live values are reported by
> `GET https://api.qsdm.tech/api/v1/status`.

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
| Issued by | QSDM pilot network (pre-mainnet), PoW emission layer | see §3 |
| Non-issuance paths | transaction fees (never mint new supply; burned today) | see §5 |

The 8-decimal choice intentionally mirrors Bitcoin UX: wallet developers,
block explorers, and exchanges already know how to display 8-decimal coins
correctly, and the smallest-unit name `dust` is short and phonetic for CLI
output (`amount: 12345 dust`).

---

## 2. Supply model

| Parameter | Value |
|---|---|
| Max supply | **90,000,000 CELL** (9,000,000,000,000,000 dust), all from mining |
| Founder / insider allocation | **0%** |
| Genesis protocol treasury | **None.** The earlier 10,000,000 CELL treasury proposal is withdrawn ([Genesis Policy v1.0](GENESIS_POLICY_v1.0.md) §1). |
| Mining emission | **90,000,000 CELL** over ~20 years |
| Emission curve | halvings every 4 years (see §3) |
| Validator block subsidy | **0** |
| Transaction fees | **Burned:** 100% of fees are credited to no account until independent validators produce blocks (see §5) |

### 2.1 No genesis treasury

There is no genesis treasury allocation and no treasury address in genesis.
The earlier proposal (10,000,000 CELL minted at genesis and released over 48
months to a multisig) is withdrawn by
[Genesis Policy v1.0](GENESIS_POLICY_v1.0.md). Any future proposal to add an
allocation needs a new published policy version, a counsel opinion and the
governance process, and it may never mint CELL on a chain that has already
launched without it. Program budgets (referral, onboarding, grants) are funded
only from existing CELL; see the [Treasury Policy](TREASURY_POLICY.md).

### 2.2 No founder or insider premine

The project commits to a **fair launch**: no public sale, no private sale,
no ICO, no presale, no founder allocation. All 90 M mining-emission CELL
must be earned by a miner producing a valid PoW proof. This is the cleanest
distribution posture in the current design and is not negotiable without a
public tokenomics revision and governance approval.

With no genesis treasury, no CELL is allocated at genesis to anyone: the
whole 90,000,000 CELL supply is issued by mining. Historic development
credits on the pilot chain are not an allocation; the mainnet migration in
§2.3 burns or explicitly classifies them.

### 2.3 Genesis path

Mainnet continues the current chain. A deterministic, network-wide migration
at a published fork height, announced at least 60,480 blocks (7 days) ahead,
removes the synthetic funder balance, burns or explicitly classifies every
historic development credit and every balance that classified issuance does
not explain (with aggregate figures published), and keeps balances explained
by mining rewards and ordinary signed transfers. The aggregate-only migration
manifest and its SHA-256 are published, and every validator applies the same
manifest hash. If the manifest cannot reach a "ready" state, the fallback is a
clean genesis under a new policy version. Until then, treat balances and
history as pilot data.

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
unit tests for the exact token definition.

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
| Transaction fees | Denominated in dust. Burned today (credited to no account) until independent validators produce blocks; see §5. |
| WASM contract gas | Gas price in dust/gas; already wired in `pkg/wasm`. |
| Validator bonds | Future: validators stake CELL to participate; slashable. |
| Miner enrollment bonds | 10 CELL, slashable. May be prepaid or accumulated from protocol mining rewards starting from a zero CELL balance. |
| Bridge collateral | Future: denominate bond in CELL via `pkg/bridge`. |
| Governance weight | Future: one CELL = one vote, in proposal-weighted voting. |

Cell is **not** used to pay mining rewards out of a pre-minted pool; mining
rewards are newly minted at block-confirmation time (a `mint` transaction
embedded in the block by the validator who proposes, crediting the winning
miner). This is the only path that creates new supply; there is no genesis
treasury. On the pilot chain today, rewards are paid from the synthetic
`qsdm-system-funder` reserve; before mainnet it is replaced by capped,
consensus-native issuance that cannot exceed the schedule in §3 or the cap
in §2 ([Genesis Policy v1.0](GENESIS_POLICY_v1.0.md) §4).

---

## 5. Fee model

Validators never dilute holders: there is no validator block subsidy. This
split is enforced at the protocol
level: a mining-reward transaction with `role = miner` is the **only**
transaction allowed to mint new supply, and a block producer can include at
most one such transaction. A validator that attempts to self-pay via a
mining-reward transaction is producing an invalid block.

**Fees are burned today.** A transfer debits `amount + fee` from the sender
and credits only `amount` to the recipient, so 100% of the fee is credited to
no account. Under [Genesis Policy v1.0](GENESIS_POLICY_v1.0.md) §3 this stays
so until independent validators produce blocks. Validators earn no fees on
the pilot network today.

### 5.1 Fee destination after independent validators (open)

Once independent validators produce blocks, the project may propose paying
part of each fee to the block producer, for example an EIP-1559-style split
(base fee burned, priority fee to the validator). Any change to the fee
destination is a consensus change: it needs a new policy version and a
published activation height.

---

## 6. Comparison to Bitcoin

| Dimension | Bitcoin | Cell |
|---|---|---|
| Max supply | 21,000,000 BTC | 90,000,000 CELL (all from mining) |
| Decimals | 8 | 8 |
| Halving period | every 210,000 blocks (~4 years at 10-min blocks) | every 12,623,040 blocks (~4 years at 10-sec blocks) |
| Block time | ~10 min | 10 sec (target) |
| Initial reward | 50 BTC | 3.56490987 CELL |
| Founder / insider allocation | 0 | 0 |
| Treasury | none | none (no genesis treasury) |
| Consensus | pure PoW | PoE + BFT **for consensus** (design target; today a single block producer), additive PoW **for emission only** |

The structural difference from Bitcoin is that Cell's PoW layer is
explicitly *additive* — it exists solely to meter coin emission. Consensus
does not depend on PoW (design target; today a single block producer runs
the pilot network during recovery). If all miners went offline tomorrow, the
validators would continue producing blocks; the only thing that would stop is new
supply creation.

---

## 7. Distribution philosophy

**Fair launch, utility-first.** There is no presale, no ICO, no public sale,
no private sale, no airdrop of founder tokens, and no genesis treasury. Program budgets are funded only from existing CELL under the public [Treasury Policy](TREASURY_POLICY.md).

Earning paths at launch are:
- **Mining**: run Hive (NVIDIA GPU), earn Cell by producing valid
  proofs.
- **Validating**: operate a VPS validator node (not yet open on the pilot
  network). Fees are burned until independent validators produce blocks;
  any validator fee revenue after that needs a new policy version (§5.1).
- **Using**: use Cell to pay transaction fees on a service you care about.

### 7.1 Bond from mining earnings

A new NVIDIA miner does not need pre-existing CELL. The miner may submit a
signed `mining_rewards` enrollment with a zero
starting bond. Accepted protocol rewards are then withheld into the on-chain
enrollment record until its 10 CELL target is reached. Only the remainder of
the reward is credited to the spendable wallet balance. This changes reward
destination accounting, not the emission schedule or total reward amount.

The mode is opt-in. Prepaid enrollment remains available, deferred enrollments
are still slashable for invalid proofs, and zero-balance enrollment requires
one-time computational postage to limit persistent-state spam.

The project does not sell Cell. Exchanges that choose to list Cell after
launch operate independently; the QSDM project does not negotiate listing
allocations and does not provide market-making inventory.

---

## 8. Legal posture

This document describes the intended fair-launch utility-token design: zero
founder allocation, no genesis treasury, mining emission of the whole
supply, and no project token sale. It is a technical and economic specification, not a
legal conclusion. Counsel must review the implemented genesis, custody,
distribution, marketing, and jurisdiction-specific obligations before a
production launch.

This is a posture, not a guarantee. Counsel review is required before
mainnet genesis and before any promotional language is published on
`qsdm.tech` or elsewhere. The phrases "investment", "returns", "profit",
and "yield" are forbidden in all project communications (see
`QSDM/docs/docs/COPY_FILTERS.md`, not yet published).

---

## 9. Changelog

| Date | Change |
|---|---|
| Major Update Phase 3.1 | Document created; numbers adopted from Major Update §4.1–§4.4 with Phase 0 "ratified per recommendation, awaiting counsel review" status. |
| 2026-10-16 | [Genesis Policy v1.0](GENESIS_POLICY_v1.0.md) ratified by owner, awaiting signature and counsel review: 90,000,000 CELL hard cap, all from mining; 10,000,000 CELL treasury proposal withdrawn; fees burned until independent validators; mainnet continues the current chain via a published migration. |

