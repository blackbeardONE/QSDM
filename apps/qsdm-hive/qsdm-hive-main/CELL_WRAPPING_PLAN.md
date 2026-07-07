# CELL Wrapping Plan

Status date: 2026-06-08

## Core Decision

CELL remains the native coin of QSDM Core.

If CELL is exposed on another chain, it should be represented as a wrapped token:

- Name: Wrapped CELL
- Symbol: wCELL
- First target chain: Base
- Initial environment: Base Sepolia testnet
- Mainnet only after real QSDM utility and bridge testing
- Ratio: 1 native CELL locked on QSDM = 1 wCELL minted on Base
- Decimals: 9, matching native CELL

## Mental Model

Native CELL is the real asset.

wCELL is a receipt token on Base. It only has value if users trust that it can be redeemed back into native CELL.

Example:

```text
QSDM Core:
100 CELL locked

Base:
100 wCELL minted
```

When the user wants native CELL back:

```text
Base:
100 wCELL burned

QSDM Core:
100 CELL unlocked
```

## Why Base First

Base is the preferred first wrapping chain because it is:

- EVM-compatible
- cheaper than Ethereum mainnet
- supported by common wallets, explorers, and Uniswap
- easier to explain and integrate than building a custom DEX immediately

Ethereum mainnet is too expensive for the current budget. A custom QSDM-native DEX is not the first priority because it would require liquidity, audits, indexing, market UI, and user trust.

## Bridge Requirements

A minimal bridge must support:

1. Lock native CELL on QSDM Core.
2. Mint matching wCELL on Base.
3. Burn wCELL on Base.
4. Unlock matching native CELL on QSDM Core.
5. Publish transparent reserves.
6. Prevent double minting, replay, and fake unlock events.

The bridge is the risky part. A weak bridge can destroy trust quickly, so the first version should be testnet-only, simple, and auditable.

## Current Priority

Do not rush a public liquidity pool.

The better order is:

1. Keep CELL native inside QSDM.
2. Grow real QSDM Hive utility: task running, staking, rewards, validator work.
3. Publish a CELL supply/account/task explorer.
4. Test wCELL on Base Sepolia.
5. Open small community testing for wrapping and redemption.
6. Deploy wCELL on Base mainnet only when there is actual demand.
7. Add a small Uniswap pool only after there is community liquidity and a clear reason to trade.

## Value Principle

CELL should earn value from work and utility first:

- stake CELL to run QSDM tasks
- earn CELL for useful validation or compute work
- use CELL to fund task reward pools
- use CELL for QSDM gateway, validator, or network services
- use CELL as the native settlement unit for QSDM Core

DEX access should come after utility, not before it.
