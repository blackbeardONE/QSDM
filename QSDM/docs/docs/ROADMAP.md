# Roadmap

What has to happen before QSDM can leave the pilot stage, in the order the
project plans to do it. Dates are not promised; each step ships when it is
ready and is announced on [Network status](NETWORK_STATUS.md) and in the
[changelog](https://github.com/blackbeardONE/QSDM/blob/main/CHANGELOG.md).

> [!NOTE]
> This page replaces the earlier phase-by-phase roadmap (2024 to September
> 2026). That version is in the repository history.

## Where we are (October 2026)

- Public **pilot network (pre-mainnet)**, single block producer during recovery.
- Chain restarted on 2026-09-27; public NVIDIA mining reopened on 2026-10-02
  with hardened mining admission (durable proof and nonce de-duplication,
  persistent pending rewards, stop-on-storage-failure).
- QSDM Hive 1.4.22 for Windows and Linux, signed with the rotated v2 release key,
  with automatic owner signatures on mining proofs and Proof-of-Entanglement
  parents on every transfer. Enrolled miners mine in 7-day canary windows.
- Mining is open to every enrolled miner since 2026-10-19 (enrollment reopened on
  2026-10-17); every proof carries the miner's operator signature.
- Anyone can run a full node that verifies every block: a signed full-node release,
  a verified chain snapshot and HTTP sync ([Run a full node](RUN_A_FULL_NODE.md)).
- Wallet transactions signed with ML-DSA-87.

## Done in October 2026

Each item is a consensus rule on the live chain. Details and dates are in
[Network upgrades](NETWORK_UPGRADES.md) and [Consensus rules](CONSENSUS_RULES.md).

| Step | Since |
|---|---|
| **Proof-of-Entanglement**: every transfer names committed parent transactions | Block 816,600 (2026-10-11 13:18 UTC) |
| **Signed consensus**: every BFT vote and consensus message must carry a valid ML-DSA signature; unsigned consensus traffic is refused (`signed_consensus_active: true`) | Block 827,000 (2026-10-12 18:11 UTC) |
| **Relay-only paid edge tasks**: paid edge tasks settle only through an authorized settlement relay | Block 833,800 (2026-10-13 13:21 UTC) |
| **Reserved built-in task IDs** and the public signed task-action route | Node release of 2026-10-15 |

## Next

| Step | What it means for you |
|---|---|
| **Retire Ed25519** | The generic peer-to-peer transaction-gossip verifier stops accepting Ed25519 signatures. That code path cannot be reached on the live chain today (the producer has no transaction gossip ingress), but it still exists in the code. |
| **Standby producer** | A second, independently run producer that can take over block production. |
| **Block production by miners** | Miners produce blocks, in stages, instead of a set of independent validators (decision of 2026-10-19). The single producer's role ends with the last stage. Designed, not built; each stage is a published network upgrade with at least 7 days' notice. |

## Mainnet gates

Mainnet is declared only after all of these are done (see
[Treasury policy §9](TREASURY_POLICY.md#9-mainnet-release-gates)):

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
