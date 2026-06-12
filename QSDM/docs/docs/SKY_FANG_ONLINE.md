# Sky Fang - MMORPG

Sky Fang Online is a play-to-earn MMORPG integration powered by QSDM and CELL.

## User flow

1. Open Sky Fang at <https://skyfang.xyz/>.
2. Link the active QSDM wallet from QSDM Hive.
3. Return to Hive and run the **QSDM Sky Fang Link** task.

Hive should verify the active wallet against Sky Fang before submitting the one-time reward proof. If the wallet is not linked, the task must stay blocked and show the wallet address that needs linking.

## What this proves

- A game account can bind to a QSDM wallet.
- A Hive task can verify that binding before reward submission.
- CELL can be used as the reward asset for integrations.

## Operational notes

Sky Fang link status is served by the Sky Fang site. If the site returns 503, Hive should treat the proof as not verifiable instead of granting rewards.

## Related pages

- [QSDM Hive guide](QSDM_HIVE.md)
- [Sky Fang official website](https://skyfang.xyz/)
- [Sky Fang integration notes](https://skyfang.xyz/docs)
- [CELL tokenomics](CELL_TOKENOMICS.md)
