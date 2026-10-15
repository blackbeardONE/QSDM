# Network upgrades

> **Status:** live changelog for the public pilot network (pre-mainnet).
> Newest first. Heights and times are block heights and block timestamps in
> UTC. If this page and `GET https://api.qsdm.tech/api/v1/status` disagree, the
> API is right.

Each entry says what changed and what you have to do. Rules that are switched
on at a block height stay on; the full list of rules in force is in
[Consensus rules](CONSENSUS_RULES.md).

QSDM still runs with a **single block producer** and **no independent
validators**, and it has **not had an independent external audit**. These
upgrades harden that pilot network; they do not change its stage. See
[Network status](NETWORK_STATUS.md) and [Security model](SECURITY_MODEL.md).

## Summary

| Date (UTC) | Height | Upgrade | Do you need to act? |
|---|---|---|---|
| 2026-10-15 19:59 | node release | Reserved built-in task IDs; public signed task-action route opened | Only task publishers: built-in IDs are reserved |
| 2026-10-13 13:21 | 833,800 | Relay-only settlement for paid edge tasks | No (paid resource work is an operator-only test) |
| 2026-10-12 18:11 | 827,000 | Signed consensus enforced | No |
| 2026-10-11 13:18 | 816,600 | Proof-of-Entanglement enforced | **Yes**, if you send CELL with old software |
| 2026-10-10 | Hive release | QSDM Hive 1.4.22 | **Yes**, Hive 1.4.20 / 1.4.17 users install by hand |
| Before October 2026 | 625,000 | Signed task actions and transaction content root | No |

## 2026-10-15: reserved built-in task IDs

- **When:** node release `hardened-legacy-20261014-d7ffcd4-hl2-edge-go1269-ids`,
  live since 2026-10-15 19:59 UTC. This is a node release, not a new
  activation height.
- **What changed:** the built-in system task IDs are reserved. Only the
  project's owner key can register them, so nobody else can publish a task
  under a built-in ID. After this release the public task-action route,
  `POST /api/v1/tasks/actions/submit-signed`, was opened.
- **Paid resource work:** the Edge Pool CPU, GPU and RAM tasks are in an
  operator-only test. They are not yet open to other miners.
- **What you must do:** nothing, unless you publish tasks. Pick your own task
  ID; built-in IDs are refused for any key other than the owner key. See
  [Task registry](QSDM_TASK_REGISTRY.md).

The same day a new 7-day mining window started for enrolled Hive miners; see
[Mining today](MINING_TODAY.md).

## Block 833,800: relay-only paid edge tasks

- **When:** block 833,800, 2026-10-13 13:21 UTC.
- **What changed:** the paid edge tasks (`qsdm-edge-worker` and its GPU and
  RAM variants) settle only through an authorized settlement relay, that is a
  Relay listed in the task's `authorized_relay_ids`. Any other settlement is
  rejected.
- **Status field:** `consensus_auth.edge_relay_only_activation_height: 833800`,
  `edge_relay_only_active: true`.
- **What you must do:** nothing. Paid resource work is in an operator-only
  test and is not yet open to other miners. See [Edge pool](EDGE_POOL.md).

## Block 827,000: signed consensus

- **When:** block 827,000, 2026-10-12 18:11 UTC.
- **What changed:** every BFT vote and consensus message must carry a valid
  ML-DSA signature. Unsigned consensus traffic is refused.
- **Status fields:** `consensus_auth.require_signed_votes: true`,
  `signed_message_activation_height: 827000`,
  `signed_consensus_active: true`,
  `unsigned_consensus_traffic_accepted: false`.
- **What it does not do:** it does not add validators. The network still has
  one producer, which signs every consensus message.
- **What you must do:** nothing. Wallets and miners are not affected.
  Operators of additional validators follow
  [Signed consensus rollout](SIGNED_CONSENSUS_ROLLOUT.md).

## Block 816,600: Proof-of-Entanglement

- **When:** enforced by consensus from block 816,600, 2026-10-11 13:18 UTC.
  The public API began refusing transfers without valid parents (HTTP 422) a
  little earlier, at block 816,240, so no such transfer could still be waiting
  when enforcement started.
- **What changed:** every signed CELL transfer must name 2 to 10 committed
  parent transactions from the last 8,640 blocks. A block holding a transfer
  without valid parents is invalid.
- **Status fields:** `consensus_auth.poe_activation_height: 816600`,
  `poe_active: true`. Wallets read parents from `GET /api/v1/chain/parents`.
- **What you must do:** send CELL with QSDM Hive 1.4.22 or newer, the
  [web wallet](WEB_WALLET.md), or `qsdmcli wallet sign-tx --auto-parents`.
  Older software gets HTTP 422 `proof-of-entanglement: …`. Details:
  [Proof-of-Entanglement](PROOF_OF_ENTANGLEMENT.md).

## 2026-10-10: QSDM Hive 1.4.22

- **What changed:** Hive 1.4.22 for Windows x64 and Linux x86-64 is the
  current release. It adds Proof-of-Entanglement parents to every transfer.
  Edge Control ships with Hive.
- **What you must do:** Hive 1.4.21 users get the update in Hive. Hive 1.4.20
  (Windows) and 1.4.17 (Linux) cannot update themselves because the release
  key was rotated: install 1.4.22 by hand. Hive 1.4.17 stops working after
  9 November 2026 and 1.4.20 after 19 December 2026. Check the download first:
  [Downloads & verification](DOWNLOADS_AND_VERIFICATION.md).

## Block 625,000: signed task actions and transaction content root

- **When:** block 625,000, before October 2026. Both rules were already active
  when the public node was checked on 2026-08-29.
- **What changed:**
  - **Signed task actions.** A task action at or above this height must carry
    a valid ML-DSA signature. Unsigned task actions are rejected.
  - **Transaction content root.** From this height the block's transaction
    root commits the contents of each transaction, not only its ID, so a
    transaction cannot be changed in flight without changing the block hash.
- **Status fields:** `consensus_auth.task_action_signatures_active: true`,
  `tx_content_root_active: true`.
- **What you must do:** nothing. Hive and `qsdmcli` sign task actions.
  Background: [Signed consensus rollout](SIGNED_CONSENSUS_ROLLOUT.md#task-action-signatures).

## Earlier milestones

- **2026-10-02:** public NVIDIA mining reopened with hardened mining admission
  (durable proof and nonce de-duplication, persistent pending rewards,
  stop-on-storage-failure).
- **2026-09-27:** the chain restarted with the same single producer after the
  pause on 2026-09-22.

## Not yet

- **Ed25519 retirement.** The generic peer-to-peer transaction-gossip verifier
  code still accepts Ed25519. The producer runs without transaction gossip
  ingress, so no Ed25519 transaction can reach the live chain today.
  Retirement is planned.
- **Independent validators.** The next step; not started publicly.

See the [Roadmap](ROADMAP.md).
