# Roadmap

What has to happen before QSDM can leave the pilot stage, in the order the
project plans to do it. Dates are not promised; each step ships when it is
ready and is announced on [Network status](NETWORK_STATUS.md) and in the
[changelog](https://github.com/blackbeardONE/QSDM/blob/main/CHANGELOG.md).

> [!NOTE]
> This page replaces the earlier phase-by-phase roadmap (May 2025 to September
> 2026). That version is in the repository history.

## Where we are (November 2026)

- Public **pilot network (pre-mainnet)**, one block producer run by the project,
  running release rc1109 since 2026-11-12. Since 2026-11-02 the producer also
  accepts peer-to-peer (libp2p) connections on port 4001, with connection limits.
- Chain restarted on 2026-09-27; public NVIDIA mining reopened on 2026-10-02
  with hardened mining admission (durable proof and nonce de-duplication,
  persistent pending rewards, stop-on-storage-failure).
- QSDM Hive 1.4.22 for Windows and Linux, signed with the rotated v2 release key,
  with automatic owner signatures on mining proofs and Proof-of-Entanglement
  parents on every transfer.
- Mining is open to every enrolled miner since 2026-10-19 (enrollment reopened on
  2026-10-17); every proof carries the miner's operator signature. Mining runs
  in 7-day windows that renew automatically (since 2026-11-01, after the pause
  from 2026-10-22).
- Anyone can run a full node that verifies every block: a signed full-node release,
  a verified chain snapshot and HTTP sync ([Run a full node](RUN_A_FULL_NODE.md)).
- The project's full node in Japan (since 2026-11-08, on fullnode-rc1109 since
  2026-11-12) is the standby block maker. A full takeover with the producer's
  mining records and the hand-back to a new key were rehearsed on copies of it
  on 2026-11-10 and 2026-11-11, sealed off from the network; block production
  has not moved to it yet. Since 2026-11-11 full nodes can follow a change of
  block maker.
- Wallet transactions signed with ML-DSA-87.
- The [web wallet](WEB_WALLET.md) installs as a phone app (since 2026-11-10).

## Done in October 2026

Each item is a consensus rule on the live chain. Details and dates are in
[Network upgrades](NETWORK_UPGRADES.md) and [Consensus rules](CONSENSUS_RULES.md).

| Step | Since |
|---|---|
| **Proof-of-Entanglement**: every transfer names committed parent transactions | Block 816,600 (2026-10-11 13:18 UTC) |
| **Signed consensus**: every BFT vote and consensus message must carry a valid ML-DSA signature; unsigned consensus traffic is refused (`signed_consensus_active: true`) | Block 827,000 (2026-10-12 18:11 UTC) |
| **Relay-only paid edge tasks**: paid edge tasks settle only through an authorized settlement relay | Block 833,800 (2026-10-13 13:21 UTC) |
| **Reserved built-in task IDs** and the public signed task-action route | Node release of 2026-10-15 |

## Done in November 2026

These are releases and operations, not consensus rules. Details and dates are
in [Network upgrades](NETWORK_UPGRADES.md) and [Network status](NETWORK_STATUS.md).

| Step | Since |
|---|---|
| **Public peer-to-peer**: the producer accepts libp2p peers on port 4001, with limits | 2026-11-02 |
| **6 GB full nodes**: a full node needs 6 GB of RAM | Full-node release of 2026-11-07 |
| **A second node**: the project's full node in Japan verifies every block, set up with the one-command installer | 2026-11-08 |
| **Phone wallet**: the [web wallet](WEB_WALLET.md) installs as a phone app | 2026-11-10 |
| **Hand-over rehearsals**: copies of the project's full node in Japan, sealed off from the network, took over block production, with the producer's mining records and the hand-back to a new key | 2026-11-10 and 2026-11-11 |
| **Full nodes follow a change of block maker**: the installer takes the block-maker schedule from qsdm.tech/fullnode.html and puts it into a running node | 2026-11-11 |

## Next

| Step | What it means for you |
|---|---|
| **Retire Ed25519 stream session keys** | Ed25519 transactions are already refused (since 2026-10-16, `ed25519_tx_accepted: false`). CELL stream session keys, which sign stream usage receipts, are still Ed25519. From a published height (`stream_ed25519_retirement_height`) a stream open, which registers such a key, is refused; streams opened before it can still take receipts, settle and close. The rule is in the code; no height is set yet. |
| **Live turn to the standby block maker** | Block production moves for real to the project's full node in Japan, as rehearsed on copies of it on 2026-11-10 and 2026-11-11. This is the next step. Full nodes then take the new block-maker schedule from qsdm.tech/fullnode.html ([Run a full node](RUN_A_FULL_NODE.md#when-the-block-maker-changes)). |
| **Block production by miners** | Miners produce blocks, in stages, instead of a set of independent validators (decision of 2026-10-19). The single producer's role ends with the last stage. Designed, not built; each stage is a published network upgrade with at least 7 days' notice. |

## Mainnet gates

Mainnet is declared only after all of these are done, together with the
release gates of [Treasury policy §9](TREASURY_POLICY.md#9-mainnet-release-gates):

1. Decentralised block production: the miners-produce-blocks plan in production. (Before the
   decision of 2026-10-19 this gate read "multiple independent validators in production".)
2. Signed consensus enforced (done: from block 827,000).
3. The published mainnet migration manifest and fork height
   ([Genesis Policy v1.0](GENESIS_POLICY_v1.0.md): mainnet continues the
   current chain; there is no genesis treasury).
4. A **published security review** of consensus, economics and custody, with
   its findings fixed. The current path is the open
   [community review and bug bounty](COMMUNITY_REVIEW.md); a professional
   audit remains welcome. No independent audit has been completed.

Until then, treat balances and history as pilot data.

## How to follow along

- Live state: [Network status](NETWORK_STATUS.md) and `GET https://api.qsdm.tech/api/v1/status`.
- Code and discussion: [github.com/blackbeardONE/QSDM](https://github.com/blackbeardONE/QSDM).
