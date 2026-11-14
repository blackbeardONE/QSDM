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
| About 2026-11-21 (scheduled) | 1,160,000 | A transaction ID names one transaction, whatever its contract: a block that repeats a committed ID is invalid ([Consensus rules](CONSENSUS_RULES.md#transaction-id-uniqueness-from-block-1160000)) | **Yes**, full-node operators: run fullnode-rc1111 or newer before this height |
| About 2026-11-19 (scheduled) | 1,150,000 | Only a task's catalog manager may migrate it: a block with any other `migrate` is invalid ([Consensus rules](CONSENSUS_RULES.md#task-migrate-authorization-from-block-1150000)) | **Yes**, full-node operators: run fullnode-rc1110 or newer before this height |
| 2026-11-13 18:41 | node release | Release rc1111: a transaction ID names one transaction, whatever its contract. The API refuses a signed task action whose ID another transaction holds, the producer leaves such a transaction out of its blocks, and a committed receipt is final; blocks enforce the ID rule from 1,160,000. Mining paused 24 minutes. Full nodes: fullnode-rc1111 is the same `qsdm` ([Run a full node](RUN_A_FULL_NODE.md)) | Full-node operators: upgrade before block 1,160,000 |
| 2026-11-13 11:59 to 12:50 | 1,095,602 and 1,095,785 | The first live turn of block production: the project's full node in Japan made blocks 1,095,602 to 1,095,784 with its own key, then the producer again from 1,095,785 with a new key; the block-maker schedule is a version 2 table with four block makers; mining paused 65 minutes ([details](#2026-11-13-the-first-live-turn-of-block-production)) | **Yes**, full-node operators: take the new schedule from qsdm.tech/fullnode.html (`install-full-node.sh --update-schedule --from-website`); a node with the old schedule stops at block 1,095,601 |
| 2026-11-12 21:25 | node release | Release rc1110: a security fix. The API refuses a task `migrate` from anyone but the task's catalog manager, to a target that is not another registered, unpaused task of the same manager, or into a cycle; blocks enforce the same rule from 1,150,000. Mining paused 6 minutes. Full nodes: fullnode-rc1110 is the same `qsdm` ([Run a full node](RUN_A_FULL_NODE.md)) | Full-node operators: upgrade before block 1,150,000 |
| 2026-11-12 07:46 | node release | Release rc1109: built with Go 1.26.11 (GO-2026-6859 in `net/http`) and golang.org/x/net v0.62.0, and a start reads the journal further ahead (no consensus change); mining paused 5 minutes. Full nodes: fullnode-rc1109 is the same `qsdm` ([Run a full node](RUN_A_FULL_NODE.md)) | No (full-node operators can upgrade) |
| 2026-11-11 | full node | Full nodes can follow a change of block maker: `install-full-node.sh` takes the block-maker schedule from qsdm.tech/fullnode.html (or `--producer-schedule FILE`), and `--update-schedule` puts it into a running node ([Run a full node](RUN_A_FULL_NODE.md#when-the-block-maker-changes)) | No (full-node operators act only after a change of block maker) |
| 2026-11-08 22:15 | full node | Full-node release fullnode-rc1108, the same `qsdm` as release rc1108: a running full node's memory no longer grows between restarts ([Run a full node](RUN_A_FULL_NODE.md)) | No (full-node operators can upgrade) |
| 2026-11-08 17:33 | node release | Release rc1108: the blocks the producer appends keep their signatures only in the journal, so its memory stays flat between restarts (no consensus change); mining paused 5 minutes | No |
| 2026-11-08 16:34 | operations | A second node: the project's full node in Japan verifies every block itself, set up with the one-command installer ([Run a full node](RUN_A_FULL_NODE.md)) | No |
| 2026-11-07 23:18 | full node | Full-node release fullnode-rc1107, the same `qsdm` as release rc1107: a full node needs 6 GB of RAM instead of 10 to 12 ([Run a full node](RUN_A_FULL_NODE.md)) | No (full-node operators can upgrade to use less memory) |
| 2026-11-07 22:56 | node release | Release rc1107: the producer starts faster, and the snapshot import needs about a third of the memory (no consensus change); mining paused 5 minutes | No |
| 2026-11-06 08:42 | node release | Release rc1106: the producer keeps checked block signatures on disk and needs less memory, and the public read-only API answers any browser origin (no consensus change); mining paused 13 minutes | No |
| 2026-11-05 17:51 | node release | Release rc1105: blocks share the values that repeat across them in memory, so the producer needs less (no consensus change); mining paused 21 minutes | No |
| 2026-11-04 04:19 | operations | Public peer-to-peer reopened, on release rc1103 | No |
| 2026-11-03 17:15 | node release | Release rc1103: a fix for a crash that one block with a null transaction entry could cause (no consensus change); mining paused 19 minutes | No |
| 2026-11-03 13:39 | operations | Public peer-to-peer closed until the rc1103 fix was live | No |
| 2026-11-02 | operations | The producer accepts peer-to-peer (libp2p) connections on port 4001, with connection, stream and block-request limits; mining paused for one restart | No |
| 2026-11-01 11:17 | node release | Release rc1101: the Go 1.26.10 security rebuild and a response budget for peer-to-peer block requests (no consensus change); mining paused 16 minutes | No |
| 2026-11-01 07:33 | operations | Mining resumed after the 22 October - 1 November pause (the producer's host ran out of disk space); 7-day mining windows now renew automatically | No |
| 2026-10-19 09:05 | mining window | Mining open to every enrolled miner (public mode); every proof must carry the owner's operator signature | **Yes**, miners need QSDM Hive 1.4.21 or newer |
| 2026-10-18 06:38 | node release | Release candidate rc1017; its new features ship switched off (no consensus change) | No |
| 2026-10-17 10:51 | enrollment | Miner enrollment reopened | Only new miners: enroll in Hive |
| 2026-10-16 05:45 | node release | Ed25519 transactions retired: only ML-DSA-87 signed transactions are accepted | No (current wallets sign with ML-DSA-87) |
| 2026-10-15 19:59 | node release | Reserved built-in task IDs; public signed task-action route opened | Only task publishers: built-in IDs are reserved |
| 2026-10-13 13:21 | 833,800 | Relay-only settlement for paid edge tasks | No (paid resource work is an operator-only test) |
| 2026-10-12 18:11 | 827,000 | Signed consensus enforced | No |
| 2026-10-11 13:18 | 816,600 | Proof-of-Entanglement enforced | **Yes**, if you send CELL with old software |
| 2026-10-10 | Hive release | QSDM Hive 1.4.22 | **Yes**, Hive 1.4.20 / 1.4.17 users install by hand |
| Before October 2026 | 625,000 | Signed task actions and transaction content root | No |

## 2026-11-13: the first live turn of block production

- **When:** mining closed at 11:53:37 UTC and the producer stopped at block
  1,095,601 (11:53:53 UTC). The project's full node in Japan sealed block
  1,095,602 at 11:59:57 UTC and made blocks up to 1,095,784. The producer made
  the blocks again from block 1,095,785 (12:50:22 UTC), and mining reopened at
  12:59:10 UTC. Blocks paused about 6 and about 20 minutes.
- **What changed:** the block-maker schedule
  ([Producer transition](PRODUCER_TRANSITION.md#the-public-schedule)) is a
  version 2 table with four block makers: the historical producer from block
  482,566, `1cf60b16…` from 659,977, the Japan node's turn key
  `2c1ff519…` from 1,095,602, and the producer's new key `287ddb76…`
  from 1,095,785. The key `1cf60b16…` never signs again. No consensus rule
  changed. The chain did not fork, and no CELL was paid during the turn
  (mining was closed). The mining audit of the Japan node's records passed,
  and every block of the turn has its receipt on the producer.
- **What you have to do:** nothing as a wallet holder or miner. Full-node
  operators: a node with the schedule of the full-node release stops at block
  1,095,601. Take the new schedule from qsdm.tech/fullnode.html with
  `bash install-full-node.sh --update-schedule --from-website`
  ([Run a full node](RUN_A_FULL_NODE.md#when-the-block-maker-changes)); a new
  install takes it by itself. Its digest is `fd23fbcae12315a4e6d4abce24bd908d0e738d6227b6d3203c03d772238c1345`.

## 2026-11-13: release rc1111, and transaction IDs unique across contracts from block 1,160,000

- **When:** node release `hardened-legacy-20261114-d7ffcd4-hl2-edge-go12611-ids-pqe-rc1111`,
  switched in 2026-11-13 18:41 UTC; mining reopened at 19:02 UTC. The same
  rule becomes a consensus rule at block **1,160,000** (about 21 November 2026
  at 10-second blocks).
- **What changed:** a transaction ID names one transaction, whatever its
  contract. Wallet transfers have been held to this since block 816,600; now
  every contract is. `/api/v1/tasks/actions/submit-signed` refuses a signed
  task action whose ID already belongs to another recorded or pending
  transaction (409, `mempool_status` `id_taken`), block production leaves such
  a transaction out without a receipt, and a committed receipt is final. From
  block 1,160,000 a block that repeats an ID committed in the last 8,640 blocks
  or earlier in the same block is invalid; blocks below that height replay as
  they were produced. Same Go 1.26.11 and modules as rc1110.
- **What you have to do:** nothing as a wallet holder, miner or task
  publisher. Full-node operators: install `fullnode-rc1111` (the same `qsdm`)
  before block 1,160,000. An older full node keeps following, because the
  producer never puts such a transaction in a block, but only rc1111 or newer
  checks the rule itself.

## 2026-11-12: release rc1110 (a security fix), and the migrate rule from block 1,150,000

- **When:** node release `hardened-legacy-20261113-d7ffcd4-hl2-edge-go12611-ids-pqe-rc1110`,
  switched in 2026-11-12 21:25 UTC; mining reopened by itself at 21:30 UTC.
  The same rule becomes a consensus rule at block **1,150,000** (about
  19 November 2026 at 10-second blocks).
- **What changed:** a `migrate` task action points a task at its newer
  version, and QSDM Hive offers that version as an upgrade to everyone running
  the task. Until rc1110 the node accepted a `migrate` from any signed wallet,
  for any task and any target. It now requires that the task is registered in
  the catalog, that the sender is the task's catalog `manager`, and that the
  target is another registered, unpaused task of the same manager whose own
  `migrated_to` chain does not lead back to the task.
  `/api/v1/tasks/actions/submit-signed` answers 403 to anything else, so it
  never reaches a block. From block 1,150,000 a block that carries such a
  migrate is invalid; blocks below that height replay as they were produced.
  The flaw was reported privately on 12 November 2026; no live task had been
  migrated. Same Go 1.26.11 and modules as rc1109.
- **QSDM Hive:** the next Hive release also follows a migration only to a task
  of the same manager, and stops on a cycle.
- **What you have to do:** nothing as a wallet holder, miner or task
  publisher. Full-node operators: install `fullnode-rc1110` (the same `qsdm`)
  before block 1,150,000. An older full node keeps following, because the
  producer never puts a refused migrate in a block, but only rc1110 or newer
  checks the rule itself.

## 2026-11-12: release rc1109

- **When:** node release `hardened-legacy-20261112-d7ffcd4-hl2-edge-go12611-ids-pqe-rc1109`,
  switched in 2026-11-12 07:46 UTC; mining reopened by itself at 07:51 UTC.
  A node release, not an activation height.
- **What changed:** rc1108 built with Go 1.26.11, which fixes GO-2026-6859
  in `net/http` (a server that limits request bodies with `MaxBytesReader`
  could crash when a handler reads the body while another goroutine writes
  the response; the node's API does both), and with golang.org/x/net
  v0.62.0 (CVE-2026-78661), with the x/crypto, x/sys, x/term, x/text and
  x/sync versions `QSDM/source` has. A start reads eight journal lines per
  worker ahead instead of one, the change `QSDM/source` has since
  2026-11-10. The restart paused mining 5 minutes, as for rc1108; the node loaded the chain in about 3 and a half of them. The full-node release
  `fullnode-rc1109` is the same `qsdm` ([Run a full node](RUN_A_FULL_NODE.md)).
  Nothing a block hashes, signs or encodes changed.
- **What you must do:** nothing. Full-node operators can upgrade.

## 2026-11-08: release rc1108

- **When:** node release `hardened-legacy-20261108-d7ffcd4-hl2-edge-go12610-ids-pqe-rc1108`,
  switched in 2026-11-08 17:33 UTC; mining reopened by itself at 17:38 UTC.
  A node release, not an activation height.
- **What changed:** the change that `QSDM/source` has since 2026-11-07: a
  block's ML-DSA-87 signature leaves memory once the next block is written
  and fsynced to the journal right after it, for blocks the node seals and
  blocks it receives, as a start already did for the blocks it loads. The
  API and the peer-to-peer catch-up read it back from the journal and check
  it, as for the blocks of a start. The restart paused mining 5 minutes, as for rc1107; the node loaded the chain in about 3 of them. The full-node release
  `fullnode-rc1108` is the same `qsdm` ([Run a full node](RUN_A_FULL_NODE.md)).
  Nothing a block hashes, signs or encodes changed.
- **What you must do:** nothing.

## 2026-11-07: release rc1107

- **When:** node release `hardened-legacy-20261107-d7ffcd4-hl2-edge-go12610-ids-pqe-rc1107`,
  switched in 2026-11-07 22:56 UTC; mining reopened by itself at 23:01 UTC.
  A node release, not an activation height.
- **What changed:** the start-up changes that `QSDM/source` has since
  2026-11-07: each producer's ML-DSA-87 public key is prepared once per
  start instead of once per signature, and the receipts file is decoded
  on every core. The snapshot import and the offline tail replay read the
  chain the way a start does: once, on every core, keeping verified
  signatures in the journal. The restart took 5 minutes instead of 13. The full-node release
  `fullnode-rc1107` is the same `qsdm` ([Run a full node](RUN_A_FULL_NODE.md)).
  Nothing a block hashes, signs or encodes changed.
- **What you must do:** nothing.

## 2026-11-06: release rc1106

- **When:** node release `hardened-legacy-20261106-d7ffcd4-hl2-edge-go12610-ids-pqe-rc1106`,
  switched in 2026-11-06 08:42 UTC; mining reopened by itself at 08:55 UTC.
  A node release, not an activation height.
- **What changed:** the start-up and memory changes that `QSDM/source` has
  since 2026-11-06: a height index instead of a walk of the whole chain, one
  signature check per block at start, and block signatures that were checked
  kept in the journal on disk instead of in memory. The producer settled at
  about 4.5 GB after its start, against 6.6 GB on rc1105. The public
  read-only endpoints (status, balances, receipts, blocks, parents, mining
  reads) now answer any browser origin with `Access-Control-Allow-Origin: *`,
  never with credentials, so the QSDM Wallet extension shows confirmations
  and received CELL. Nothing a block hashes, signs or encodes changed.
- **What you must do:** nothing.

## 2026-11-05: release rc1105

- **When:** node release `hardened-legacy-20261105-d7ffcd4-hl2-edge-go12610-ids-pqe-rc1105`,
  switched in 2026-11-05 17:51 UTC, serving again from 18:11 UTC; mining
  reopened by itself at 18:12 UTC. A node release, not an activation height.
- **What changed:** blocks share the values that repeat across them in
  memory (the producer's ML-DSA-87 public key, parent hashes, producer IDs
  and transaction addresses), as `QSDM/source` does since 2026-11-05. The
  producer settled at about 6.6 GB after its start, against 8.5 GB on
  rc1103. Nothing a block hashes, signs or encodes changed.
- **What you must do:** nothing.

## 2026-11-03 to 2026-11-04: release rc1103, public peer-to-peer closed and reopened

- **When:** the producer's public peer-to-peer port closed on 2026-11-03 at
  13:39 UTC. Node release
  `hardened-legacy-20261103-d7ffcd4-hl2-edge-go12610-ids-pqe-rc1103` was
  switched in at 17:15 UTC, and mining reopened by itself at 17:35 UTC. The
  port reopened on 2026-11-04 at 04:19 UTC. A node release, not an
  activation height.
- **What changed:** before rc1103, one block with a null transaction entry
  could stop a node before any signature was checked. Fuzzing found it on
  2026-11-03, and the fix is in `QSDM/source`. The port stayed closed until
  the fix was live.
- **What you must do:** nothing. Full nodes follow the chain over HTTPS and
  were not exposed.

## 2026-11-02: public peer-to-peer on the producer

- **When:** 2026-11-02, one restart of the producer (still release rc1101);
  mining paused while it restarted and reopened by itself. An operations
  change, not an activation height.
- **What changed:** the producer's libp2p port is reachable from the internet:
  `/ip4/194.233.65.156/tcp/4001/p2p/12D3KooWHT2APYkMStuaiLZvto7ZmrPmUag4TStT7MryGn69feum`.
  Before, it listened on the host only. Connection and stream limits are on,
  and block requests are answered within the budget of release rc1101 (one per
  peer every 2 seconds, 2 per second in total). The status API's `peers`
  counts the connected peers.
- **What you must do:** nothing. Full nodes keep following the chain over
  HTTPS; miners and wallets are unaffected.

## 2026-11-01: release rc1101

- **When:** node release `hardened-legacy-20261101-d7ffcd4-hl2-edge-go12610-ids-pqe-rc1101`,
  switched in 2026-11-01 11:17 UTC, serving again from 11:31 UTC; mining
  reopened by itself at 11:33 UTC. A node release, not an activation height.
- **What changed:** built with Go 1.26.10, which fixes GO-2026-6809 in
  `crypto/x509` (reachable from the NVIDIA attestation check), and
  klauspost/compress v1.18.7 (CVE-2026-63209). A node now answers at most one
  peer-to-peer `block_request` per peer every 2 seconds and 2 per second in
  total, drops copies of a block it already has before verifying them, and
  keeps a bounded set of seen blocks. That was the last item holding the
  producer's public peer-to-peer port closed (see
  [Network status](NETWORK_STATUS.md)). The two release builds were
  bit-identical; govulncheck reports no reachable vulnerability.
- **What you must do:** nothing. Miners and wallets are unaffected.

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
  key was rotated: install 1.4.22 by hand. Hive 1.4.17 stopped working after
  9 November 2026; 1.4.20 stops after 19 December 2026. Check the download first:
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

- **Ed25519 for stream session keys.** Ed25519 transactions are refused since
  2026-10-16 (`ed25519_tx_accepted: false`), but CELL stream session keys still
  accept Ed25519 until a stream retirement height is set.
- **Block production by miners (proof of work).** The next step (decision D1, 19 October 2026); designed, not built.

See the [Roadmap](ROADMAP.md).
