# Cell (CELL) — Tokenomics

> **Status:** [Genesis Policy v1.2](GENESIS_POLICY_v1.2.md) (2026-11-20): from block 1,210,000 the reward is 4.56 CELL, split 85 % miner, 8 % Operations, 5 % Play Rewards, 2 % Security (§3). Earlier:
> [Genesis Policy v1.1](GENESIS_POLICY_v1.1.md): **ratified by
> owner 2026-10-16 and approved in writing 2026-10-19; v1.1 (2026-11-01) drops the counsel review.** Maximum supply
> 90,000,000 CELL, all from mining; no genesis treasury (the earlier
> 10,000,000 CELL treasury proposal is withdrawn); transaction fees are burned
> (credited to no account) while one producer, run by the project, makes the blocks;
> mainnet continues the current chain through a published, deterministic
> migration. Policy SHA-256, signature, migration manifest hash and fork
> height: not yet published (they will be recorded here). Audit row `tok-01`
> is **closed by the owner's decision** of 2026-11-01 (no counsel engagement).
>
> The code outside the emission schedule follows the policy too: the protocol
> supply cap (`chain.CellSupplyCapDust`) equals the 90,000,000 CELL mining cap,
> the `genesis-ceremony` dry-run allocates no treasury and refuses a bundle that
> does, and `qsdm-ledger-fork-plan` allows no genesis issuance, so the pilot
> chain's 100 CELL genesis seed stays flagged until the migration burns or
> classifies it (clause 5(b)).
>
> The values in this document are the
> authoritative in-repo numbers used by `pkg/branding` constants,
> `pkg/chain/emission` (Phase 3.3), and the tokenomics dashboard panel. They
> MUST NOT be changed in isolation; any change requires a corresponding
> update to `QSDM/docs/docs/REBRAND_NOTES.md` and a new entry in
> `NEXT_STEPS.md` (not yet published).
>
> The chain running today is a pilot network (pre-mainnet), with one block
> producer run by the project. Live values are reported by
> `GET https://api.qsdm.tech/api/v1/status`.

This document is the normative reference for the Cell coin's supply,
emission, and fee model. It is **not** a legal offering document, it is not
an investment prospectus, and it is not a commitment until the owner declares
mainnet in writing. See §8 ("Legal posture") below.

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
| Transaction fees | **Burned:** 100% of fees are credited to no account while one producer, run by the project, makes the blocks (see §5) |

### 2.1 No genesis treasury

There is no genesis treasury allocation and no treasury address in genesis.
The earlier proposal (10,000,000 CELL minted at genesis and released over 48
months to a multisig) is withdrawn by
[Genesis Policy v1.0](GENESIS_POLICY_v1.0.md). Any future proposal to add an
allocation needs a new published policy version, the owner's written decision and the
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

Target block time **10 seconds**. The cap is **90,000,000 CELL**, and the epoch allocations do not change: 45,000,000
CELL for epoch 0, then half of the previous epoch's each time. All amounts are computed in integer dust (1 CELL =
10^8 dust) by `pkg/chain/emission.go` and `pkg/chain/network_rewards.go`. The schedule changed at block 1,210,000
([Genesis Policy v1.2](GENESIS_POLICY_v1.2.md), published 20 November 2026).

### 3.1 Before block 1,210,000

Epochs last 4 years: 12,623,040 blocks. The reward is `floor(epoch_allocation / 12,623,040)`, which is
**3.56490987 CELL** in epoch 0 (`EmissionSchedule.BlockRewardDust`). Blocks below 1,210,000 always keep this schedule,
including when they are replayed. About 4,313,537 CELL are issued below that block.

### 3.2 From block 1,210,000: 4.56 CELL, halving by allocation

- The reward is **4.56 CELL** (456,000,000 dust) from block 1,210,000.
- It halves when the current epoch's allocation has been issued, counting the blocks before 1,210,000.
- The block that completes an allocation pays exactly the remainder. Each epoch therefore issues **exactly** its
  allocation, and nothing is lost to rounding (`EmissionSchedule.RewardDustAt`, `CumulativeDustAt`).
- When the halved reward reaches 0 dust (after 29 halvings), issuance ends, about 0.17 CELL short of the cap.

At 10-second blocks:

| Epoch | Allocation (CELL) | Reward (CELL) | Heights | Lasts | Ends (years from genesis) | Issued (CELL) | % of cap |
|---|---:|---:|---|---:|---:|---:|---:|
| 0 | 45,000,000 | 4.56 | 1,210,000 – 10,132,469 | 2.83 y | 3.21 | 45,000,000 | 50 % |
| 1 | 22,500,000 | 2.28 | 10,132,470 – 20,000,891 | 3.13 y | 6.34 | 67,500,000 | 75 % |
| 2 | 11,250,000 | 1.14 | 20,000,892 – 29,869,313 | 3.13 y | 9.47 | 78,750,000 | 87.5 % |
| 3 | 5,625,000 | 0.57 | 29,869,314 – 39,737,735 | 3.13 y | 12.59 | 84,375,000 | 93.75 % |
| 4 | 2,812,500 | 0.285 | 39,737,736 – 49,606,157 | 3.13 y | 15.72 | 87,187,500 | 96.875 % |
| 5 | 1,406,250 | 0.1425 | 49,606,158 – 59,474,579 | 3.13 y | 18.85 | 88,593,750 | 98.44 % |
| 6 | 703,125 | 0.07125 | 59,474,580 – 69,343,001 | 3.13 y | 21.97 | 89,296,875 | 99.22 % |
| 7+ | halves | halves | … | 3.13 y each | … | → 90,000,000 | → 100 % |

(Epoch 0 starts at genesis; its row shows the part from block 1,210,000.)

### 3.3 Who receives each reward (from block 1,210,000)

| Share | Receiver | Notes |
|---|---|---|
| 85 % | the miner | Also receives the rounding remainder (each fund share is rounded down to the dust). Deferred bonds fill from this share only. For a PULSE pool member it goes into the member's pool escrow, as before. |
| 8 % | Operations fund `13d786706accfbe77c5ddf6fc6757e1cca07bd01aff0cad3dcf9411d92cf11c9` | Infrastructure, servers, audits, running the network. |
| 5 % | Play Rewards fund `b8fcf6a1f352aacd5b6abf03b77c499ca9b110905809a86f631c6c72eb525dff` | For all QSDM games, one chest per game. Bannerfall is first. |
| 2 % | Security fund `4146b9a5cdb287c46c1ce67f6e377af7d5bc0c5f83cbef7403f0346b5433e301` (the bug bounty wallet) | Goes to the miner while the fund holds 100,000 CELL or more. |

- These are **shares of the scheduled emission**, not extra issuance. The sum of what miners and funds receive is the
  reward of the block, and never more.
- What is actually issued is often less than the schedule. Each owner may earn at most 2,000 CELL per 8,640 blocks.
  That cap counts the miner's 85 % share only.
- The fund addresses and balances are public. A monthly report lists every payment
  ([TREASURY_POLICY](TREASURY_POLICY.md) §7).

### 3.4 Invariants (tests in `pkg/chain/network_rewards_test.go`)

- Every epoch from block 1,210,000 issues exactly its allocation. The reward halves at each boundary, and the total
  never exceeds 90,000,000 CELL. This is checked at full scale for seven activation heights, and block by block on a
  scaled schedule for eight.
- In 200,000 random splits, and at the edge amounts, the miner's share plus the three fund shares equals the reward to
  the dust. Over all of them, the parts add up to the rewards with no drift.
- With the rules off, or below block 1,210,000, the schedule, the payments and the replayed state are identical to
  before.

---

## 4. What Cell is used for

| Use | Notes |
|---|---|
| Transaction fees | Denominated in dust. Burned today (credited to no account) while one producer, run by the project, makes the blocks; see §5. |
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
| Halving period | every 210,000 blocks (~4 years at 10-min blocks) | every 12,623,040 blocks (~4 years at 10-sec blocks) until block 1,210,000; from then when each epoch's allocation is issued (about 3.13 years) |
| Block time | ~10 min | 10 sec (target) |
| Initial reward | 50 BTC | 3.56490987 CELL; 4.56 CELL from block 1,210,000 (85 % to the miner) |
| Founder / insider allocation | 0 | 0 |
| Treasury | none | none (no genesis treasury) |
| Consensus | pure PoW | PoE + BFT **for consensus** (design target; today a single block producer), additive PoW **for emission only** |

The structural difference from Bitcoin is that Cell's PoW layer is
explicitly *additive* — it exists solely to meter coin emission. Consensus
does not depend on PoW (design target; today one block producer run by the
project makes the pilot network's blocks). If all miners went offline tomorrow, the
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
  network). Fees are burned while one producer, run by the project, makes the blocks;
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

From block 1,190,000 an active deferred enrollment whose withheld bond is still
zero 60,480 blocks (7 days) after it was made is removed, and its node ID and
GPU are free again; the miner can enroll again for free. An enrollment that has
earned anything is kept ([Consensus rules](CONSENSUS_RULES.md#the-rc1112-rules-from-block-1190000)).

The project does not sell Cell. Exchanges that choose to list Cell after
launch operate independently; the QSDM project does not negotiate listing
allocations and does not provide market-making inventory.

---

## 8. Legal posture

This document describes the intended fair-launch utility-token design: zero
founder allocation, no genesis treasury, mining emission of the whole
supply, and no project token sale. It is a technical and economic specification, not a
legal conclusion. The owner reviews the implemented genesis, custody,
distribution, marketing, and jurisdiction-specific obligations before a
production launch (Genesis Policy v1.1: no counsel engagement, the owner's decision of 2026-11-01).

This is a posture, not a guarantee. The owner's written review is required before
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
| 2026-10-19 | [Genesis Policy v1.0](GENESIS_POLICY_v1.0.md) approved by the owner in writing; signature block filled in with the policy text's SHA-256 (`b6c18897...`). Counsel review is still pending, so `tok-01` stays `pending`. |
| 2026-11-01 | [Genesis Policy v1.1](GENESIS_POLICY_v1.1.md): the owner decided not to engage counsel; the counsel opinion in clauses 1, 7 and 8 is replaced by the owner's written decision and `tok-01` is closed by it. Burn rule of the funder-removal migration decided: the 74 seed-faucet credits of 1,000 CELL each (74,000 CELL written straight into state in the faucet era and never moved) are burned; balances from mining rewards and signed transfers are kept. |
| 2026-11-20 | [Genesis Policy v1.2](GENESIS_POLICY_v1.2.md): from block 1,210,000 the block reward is 4.56 CELL, halving by allocation used, and each reward is split 85 % miner, 8 % Operations, 5 % Play Rewards, 2 % Security (the Security share goes to the miner while that fund holds 100,000 CELL); the 90,000,000 CELL cap is unchanged. §3 rewritten. |

