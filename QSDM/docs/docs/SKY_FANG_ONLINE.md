# Sky Fang - MMORPG

Sky Fang Online is a play-to-earn MMORPG integration powered by QSDM and CELL.

## User flow

1. Open Sky Fang at <https://skyfang.xyz/>.
2. Link the active QSDM wallet from QSDM Hive.
3. Return to Hive and run the **QSDM Sky Fang Link** task.

Hive verifies the active wallet against Sky Fang before submitting reward proofs. If the wallet is not linked, the task stays blocked and shows the wallet address that needs linking.

The task is not just a one-time onboarding banner. The link is the eligibility gate, and the reward view should separate:

- **Sky Fang account** - the linked username or account id returned by Sky Fang.
- **Linked QSDM wallet** - the active wallet Hive is using for proofs.
- **In-game CELL stake** - CELL committed inside Sky Fang, reported by Sky Fang.
- **Hive task stake** - CELL committed to the QSDM Sky Fang task in Hive.
- **Reward basis** - the task's current reward formula using the linked-account state, in-game stake, Hive task stake, and funded task pool.

Hive must not grant rewards when Sky Fang link verification is unavailable, returns `503`, or returns a wallet that does not match the active Hive signer.

## What this proves

- A game account can bind to a QSDM wallet.
- A Hive task can verify that binding before every reward submission.
- CELL can be used as the reward asset for integrations.

## Operational notes

Sky Fang link status is served by the Sky Fang site. If the site returns 503, Hive treats the proof as not verifiable instead of granting rewards. Rewards are paid only from a funded QSDM task pool; the integration does not mint CELL by itself.

## Related pages

- [QSDM Hive guide](QSDM_HIVE.md)
- [Sky Fang official website](https://skyfang.xyz/)
- [Sky Fang integration notes](https://skyfang.xyz/docs)
- [CELL tokenomics](CELL_TOKENOMICS.md)
