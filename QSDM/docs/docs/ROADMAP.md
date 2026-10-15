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
| **Reopen mining enrollment** | New NVIDIA GPUs can enroll through Hive again. Today only enrolled miners can mine, in 7-day windows. |
| **Retire Ed25519** | The generic peer-to-peer transaction-gossip verifier stops accepting Ed25519 signatures. That code path cannot be reached on the live chain today (the producer has no transaction gossip ingress), but it still exists in the code. |
| **Independent validators** | Validators run independently of the project, voting with signed consensus messages. Not started publicly. |
| **Standby producer** | A second, independently run producer that can take over block production. |

## Mainnet gates

Mainnet is declared only after all of these are done (see
[Treasury policy §9](TREASURY_POLICY.md#9-mainnet-release-gates)):

1. Multiple independent validators in production.
2. Signed consensus enforced (done: from block 827,000).
3. The treasury multisig (if the genesis treasury proposal is adopted) and an
   official, published genesis manifest.
4. An **independent external security audit**, with its findings fixed and the
   report published.

Until then, treat balances and history as pilot data.

## How to follow along

- Live state: [Network status](NETWORK_STATUS.md) and `GET https://api.qsdm.tech/api/v1/status`.
- Code and discussion: [github.com/blackbeardONE/QSDM](https://github.com/blackbeardONE/QSDM).
