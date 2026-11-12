# Network status

QSDM is a **public pilot network (pre-mainnet)**. This page explains what that
means today, which parts of the design are switched on, and how to check the
live state yourself. It is reviewed with every release; the numbers below are
examples from October 2026, and the live values are always in the status API.

> [!IMPORTANT]
> Treat balances and history as pilot data. Mainnet needs the
> [mainnet gates](ROADMAP.md#mainnet-gates) and the release gates in
> [Treasury policy §9](TREASURY_POLICY.md#9-mainnet-release-gates) to close
> first, including decentralised block production and a published security review
> (today an open [community review with a bug bounty](COMMUNITY_REVIEW.md);
> no independent audit has been completed).

## At a glance

| Topic | Today |
|---|---|
| Stage | Pilot network (pre-mainnet) |
| Block production | A **single block producer**. Anyone can run a full node that verifies every block itself ([Run a full node](RUN_A_FULL_NODE.md)); since 8 November 2026 the project runs one on a second server, in Japan. It is the standby block maker: on 10 and 11 November 2026 copies of it, sealed off from the network, rehearsed taking over block production, with the producer's mining records and a hand-back. Block production has not moved to it yet, and since 11 November 2026 full nodes can follow a change of block maker. Since 2 November 2026 the producer also accepts peer-to-peer (libp2p) connections on port 4001, with connection, stream and block-request limits (see [What is running](#what-is-running)); the status API's `peers` counts the peers connected to it. |
| Block time | 10 second target |
| Wallet transactions | Signed with **ML-DSA-87** (NIST FIPS 204, Cloudflare CIRCL) by Hive, the web wallet and `qsdmcli` |
| Other signatures | **Ed25519 transactions are refused since 16 October 2026** (status API `ed25519_tx_accepted: false`): only ML-DSA-87 signed transactions reach the chain. CELL stream session keys still accept Ed25519 until a stream retirement height is set (`stream_ed25519_session_keys_retired: false`). |
| Consensus messages | **Signed consensus enforced from block 827,000** (12 October 2026, 18:11 UTC): every BFT vote and consensus message must carry a valid ML-DSA signature, and unsigned consensus traffic is refused (`signed_consensus_active: true`). See [Consensus rules](CONSENSUS_RULES.md). |
| Transfer parents (Proof-of-Entanglement) | **Enforced from block 816,600** (11 October 2026): every CELL transfer names committed parents. Send with Hive 1.4.22 or newer, the web wallet, or `qsdmcli --auto-parents`. See [Proof-of-Entanglement](PROOF_OF_ENTANGLEMENT.md). |
| Mining | NVIDIA GPUs through QSDM Hive 1.4.21 or newer, **open to every enrolled miner** since 19 October 2026 (protocol v2; every proof carries the enrollment owner's operator signature), in 7-day windows that renew automatically. Mining was paused from 22 October to 1 November 2026 (see [What is running](#what-is-running)). See [Mining today](MINING_TODAY.md). |
| Supply | Max supply **90,000,000 CELL**, all from mining. No genesis treasury and no premine ([Genesis Policy v1.1](GENESIS_POLICY_v1.1.md)). Transaction fees are burned (credited to no account). |
| Release | `hardened-legacy-20261112-d7ffcd4-hl2-edge-go12611-ids-pqe-rc1109`, live since 12 November 2026, 07:51 UTC (see [What is running](#what-is-running)) |
| Independent audit | None completed. Security review is an open [community review](COMMUNITY_REVIEW.md) with a [bug bounty](BUG_BOUNTY.md) (rewards confirmed by the owner on 17 October 2026; 50,000 CELL pool); a professional audit remains welcome. The [internal security review](SECURITY_AUDIT.md) is a self-maintained checklist. |

## Check it yourself

Everything above can be checked against the public status endpoint. It needs no
key and is served on both hosts:

```bash
curl -s https://api.qsdm.tech/api/v1/status
```

Trimmed response (mid-October 2026; `chain_tip` and the exact amounts change every block, so read them live):

```json
{
  "version": "hardened-legacy-20261014-d7ffcd4-hl2-edge-go1269-ids",
  "git_sha": "d7ffcd4e80f69c3db3f2bbcf4c7f34e08cc49d25",
  "peers": 0,
  "node_role": "validator",
  "coin": { "name": "Cell", "symbol": "CELL", "decimals": 8, "smallest_unit": "dust" },
  "tokenomics": {
    "cap_cell": "90000000.00000000",
    "emitted_cell": "3045139.…",
    "block_reward_cell": "3.56490987",
    "current_epoch": 0,
    "next_halving_height": 12623041,
    "target_block_time_seconds": 10
  },
  "consensus_auth": {
    "signed_consensus_supported": true,
    "require_signed_votes": true,
    "signed_message_activation_height": 827000,
    "signed_consensus_active": true,
    "unsigned_consensus_traffic_accepted": false,
    "poe_activation_height": 816600,
    "poe_active": true,
    "edge_relay_only_activation_height": 833800,
    "edge_relay_only_active": true,
    "task_action_signatures_active": true,
    "tx_content_root_active": true
  },
  "mining": {
    "protocol_versions_accepted": [2],
    "fork_v2_active": true,
    "attestation_types_required": ["nvidia-cc-v1", "nvidia-hmac-v1"],
    "min_enroll_stake_dust": 1000000000,
    "signed_enrollment_required": true,
    "deferred_bond_from_rewards": true
  }
}
```

How to read the fields that matter most:

| Field | Meaning |
|---|---|
| `version`, `git_sha` | The release the public node runs. `git_sha` is the base commit; the release adds the recovery and mining patches listed in the changelog. |
| `chain_tip` | Current block height. It should rise by about 6 per minute. |
| `peers` | Peer-to-peer (libp2p) nodes connected to this node. Since 2 November 2026 other nodes can connect to the producer on port 4001, so the value can be above `0`. A full node that follows the chain over HTTPS is not counted. |
| `consensus_auth.signed_consensus_active`, `signed_message_activation_height` | `true` from block 827,000: every consensus message must carry a valid ML-DSA signature. |
| `consensus_auth.unsigned_consensus_traffic_accepted` | `false`: unsigned consensus traffic is refused. |
| `consensus_auth.poe_activation_height`, `poe_active` | The block from which transfers must name committed parents, and whether that height has been reached. |
| `consensus_auth.edge_relay_only_activation_height`, `edge_relay_only_active` | From block 833,800, paid edge tasks settle only through an authorized settlement relay. |
| `consensus_auth.task_action_signatures_active`, `tx_content_root_active` | `true`: task actions must carry a valid signature, and the block's transaction root commits transaction contents (both from block 625,000). |
| `consensus_auth.ed25519_tx_accepted` | `false`: transactions signed with Ed25519 are refused; ML-DSA-87 is the only accepted transaction signature. |
| `consensus_auth.stream_ed25519_retirement_height`, `stream_ed25519_session_keys_retired` | The block from which a CELL stream open (which registers an Ed25519 session key) is refused, and whether the next block is at or above it. `0` and `false`: no height is set. |
| `tokenomics.*` | Supply cap, amount mined so far, current block reward and the next halving height. Amounts are also given in dust (1 CELL = 100,000,000 dust). |
| `mining.protocol_versions_accepted` | Only mining protocol v2 proofs are accepted. |
| `mining.attestation_types_required` | The NVIDIA attestation types a proof may carry. |
| `mining.min_enroll_stake_dust` | Enrollment bond: 1,000,000,000 dust = 10 CELL. With `deferred_bond_from_rewards`, the bond can be paid from mining rewards. |

The [explorer](https://qsdm.tech/explorer.html) and the
[network page](https://qsdm.tech/network.html) show the same data in a browser.

## What is running

- **Core release** `hardened-legacy-20261112-d7ffcd4-hl2-edge-go12611-ids-pqe-rc1109`
  (live since 2026-11-12 07:51 UTC). It is rc1108 (live from 2026-11-08 17:38
  UTC) built with Go 1.26.11, which fixes GO-2026-6859 in Go's web server
  (`net/http`), and golang.org/x/net v0.62.0 (CVE-2026-78661), with the
  further journal read-ahead that `QSDM/source` has since 2026-11-10 (a
  shorter load at start). The restart paused mining 5 minutes, as for rc1108; the node loaded the chain in about 3 and a half of them. The full-node release `fullnode-rc1109`
  is the same `qsdm`. rc1108 was rc1107 (live from 2026-11-07 23:01 UTC) with
  the change that `QSDM/source` has since 2026-11-07: the blocks the
  producer appends keep their signatures only in the journal, as the blocks
  loaded at a start already did, so its memory stays flat between restarts.
  rc1107 was rc1106 (live from 2026-11-06 08:55
  UTC) with the faster start that `QSDM/source` has since 2026-11-07 (each
  producer key prepared once per start, the receipts file decoded on every
  core) and the lighter snapshot import. The full-node release
  `fullnode-rc1107` is the same `qsdm`. The restart took 5 minutes instead of 13. rc1106 was rc1105 (live from 2026-11-05
  18:13 UTC) with the start-up and memory changes that `QSDM/source` in this
  repository has since 2026-11-06 (a height index, one signature check per
  block at start, checked signatures kept in the journal on disk) and the
  public read-only API for any browser origin. The producer settled at about
  4.5 GB after its start instead of 6.6 GB, and the QSDM Wallet extension's
  History now shows confirmations and received CELL. rc1105 was rc1103 (live
  from 2026-11-03 17:37 UTC) with lighter memory: blocks share the values that
  repeat across them. Nothing a block hashes, signs or encodes changed in
  either release. rc1103 was rc1101 (live from
  2026-11-01 11:31 UTC) with one fix: a block with a null transaction entry
  could stop a node before any signature was checked. Fuzzing found it on
  2026-11-03, and the fix is in this repository. The release has the d7ffcd4 base, the
  September 2026 recovery patches, the hardened-legacy mining work (durable
  proof and nonce de-duplication, persistent pending rewards, and a
  stop-on-failure rule for block storage), the Proof-of-Entanglement parent
  rules (enforced from block 816,600), signed consensus (enforced from block
  827,000), the relay-only rule for paid edge tasks (from block 833,800),
  reserved built-in task IDs, the retirement of Ed25519 transactions
  (2026-10-16) and reopened miner enrollment (2026-10-17); since rc1101 also
  a response budget for peer-to-peer block requests (one answer per peer
  every 2 s, 2 per second in total, duplicate blocks dropped before
  verification) and klauspost/compress v1.18.7 (CVE-2026-63209). rc1101 was
  built with Go 1.26.10 (GO-2026-6809), and rc1109 with Go 1.26.11
  (GO-2026-6859). The base of this code is
  `QSDM/source` in this repository, which also has the peer-to-peer limits
  (opt-in with `p2p_limits_enabled`, switched on at the producer), the
  block-request budget ([P2P limits](P2P_LIMITS.md)), the Ed25519 retirement,
  the private peer-to-peer mode ([Private P2P mode](PRIVATE_P2P.md)), the
  supply code of the Genesis Policy (a 90 million CELL cap and no genesis
  treasury), the version 2 producer transition for a second hand-over
  ([Producer transition](PRODUCER_TRANSITION.md)) and the verified snapshot
  bootstrap for new full nodes
  ([Verified snapshot bootstrap](SNAPSHOT_BOOTSTRAP.md)). The other new
  features of the release candidates ship switched off.
- **Second node.** The project's own full node on a second server in Japan
  (12 GB of memory) was installed on 2026-11-08 with the one-command
  installer. Since 2026-11-10 it checks itself against the network every 5
  minutes and alerts the operators. It runs `fullnode-rc1109` since
  2026-11-12: it answered again 2 minutes 51 seconds after the switch, at
  block 1,087,781 (on 2026-11-09, with `fullnode-rc1108`, 4 minutes 23
  seconds). It is the standby block maker. Copies of it, sealed off from the
  network, rehearsed the hand-over: on 2026-11-10 a copy took over block
  production, and on 2026-11-11 a copy rehearsed a full takeover with the
  producer's own mining records (reconciled clean; mining closed, then
  reopened) and the hand-back to a new key. The mining audit passed: every
  proof was paid exactly once. A live turn of block production to it has not
  happened yet; it is the next step. Since 2026-11-13 its status is public:
  the producer's server reads it from the node every 15 seconds and serves it
  as `https://qsdm.tech/second-node/status.json` (its tip, version, uptime and
  role, when it was read, and whether the read worked), and qsdm.tech's
  [network page](https://qsdm.tech/network.html) shows it next to the
  producer.
- **Peer-to-peer.** Since 2026-11-02 the producer accepts libp2p peers from
  the internet on port 4001
  (`/ip4/194.233.65.156/tcp/4001/p2p/12D3KooWHT2APYkMStuaiLZvto7ZmrPmUag4TStT7MryGn69feum`,
  the host behind `api.qsdm.tech`), with connection and stream limits and the
  block-request budget above; before that the port listened on the host only.
  It was closed from 2026-11-03 13:39 UTC while the null-transaction fix went
  in, and reopened on 2026-11-04 04:19 UTC. A full node does not need it: it
  follows the chain over HTTPS ([Run a full node](RUN_A_FULL_NODE.md)).
- **Upgrade history.** Every network upgrade, with its height, time and what
  users had to do, is listed in [Network upgrades](NETWORK_UPGRADES.md).
- **Timeline.** QSDM's first code is from 10 May 2025, and block 0 of this
  pilot chain from 6 May 2026 (see [History](HISTORY.md)). The chain was
  paused on 2026-09-22 and restarted on 2026-09-27
  by the same single producer. Public mining reopened on 2026-10-02 and has
  been open to every enrolled miner since 2026-10-19. On 2026-10-22 the
  producer's host ran out of disk space: the node stopped sealing blocks for
  9 hours to protect its data, and mining froze as designed. The chain resumed
  by itself; mining reopened on 2026-11-01 07:33 UTC after an audit of the
  window (PASS, no findings). Since then each 7-day mining window renews
  automatically.
- **QSDM Hive 1.4.22** for Windows and Linux from
  [qsdm.tech/downloads/hive-v2/](https://qsdm.tech/downloads/hive-v2/), signed
  with the rotated v2 release key. See
  [Downloads & verification](DOWNLOADS_AND_VERIFICATION.md).

## What is not active yet

These parts are designed or implemented in the code but are not switched on for
the live network. Documents that describe them are marked **Design** in these
docs.

- A live turn of block production to the standby block maker, the project's
  full node in Japan. It was rehearsed on copies of that node on 2026-11-10
  and 2026-11-11, sealed off from the network. It is the next step.
- Multiple independent validators and BFT finality between them. This is in
  the code but no longer planned: since 2026-10-19 the plan is for miners to
  produce blocks, in stages (see the [Roadmap](ROADMAP.md)). Today one
  producer signs every consensus message.
- Retirement of Ed25519 CELL stream session keys. The rule is in the code
  (`stream_ed25519_retirement_height`), but no height is set
  (`stream_ed25519_session_keys_retired: false`). Ed25519 transactions are
  already refused (`ed25519_tx_accepted: false`).
- The published mainnet migration manifest and fork height
  ([Genesis Policy v1.0](GENESIS_POLICY_v1.0.md) §5).
- A published security review with its findings fixed. The current path is
  the [community review and bug bounty](COMMUNITY_REVIEW.md); no independent
  audit has been completed.

See the [Roadmap](ROADMAP.md) for the order of work and
[Security model](SECURITY_MODEL.md) for what this means for your funds.

## Service availability

Some public services are limited during the pilot. State
on 2026-11-12:

| Service | State |
|---|---|
| Status, blocks, transactions, balances (read) | Available |
| Signed wallet transfers (`POST /api/v1/wallet/submit-signed`) | Available |
| Mining (work, challenge, submit) on `api.qsdm.tech` | Open to every enrolled miner (public mode since 2026-10-19) in 7-day (168-hour) windows that renew automatically, with caps per owner per 24-hour epoch and per-IP rate limits. Each window starts the next one by itself. |
| New mining enrollments | Open since 2026-10-17. QSDM Hive submits the signed enrollment; the 10 CELL bond is paid up front or from the first rewards. |
| New account registration | Disabled during the pilot (HTTP 503) |
| Signed task actions (`POST /api/v1/tasks/actions/submit-signed`) | Open since 2026-10-15. Built-in system task IDs are reserved: only the project's owner key can register them. |
| Paid resource work (Edge Pool CPU/GPU/RAM tasks) | Operator-only test; not yet open to other miners. From block 833,800 these tasks settle only through an authorized settlement relay. |
| Operator dashboard | Private; not offered publicly |

If something on this page disagrees with the status API, the API is right.
Please [open an issue](https://github.com/blackbeardONE/QSDM/issues) so the
page can be fixed.
