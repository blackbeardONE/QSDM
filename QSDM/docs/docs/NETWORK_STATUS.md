# Network status

QSDM is a **public pilot network (pre-mainnet)**. This page explains what that
means today, which parts of the design are switched on, and how to check the
live state yourself. It is reviewed with every release; the numbers below are
examples from October 2026, and the live values are always in the status API.

> [!IMPORTANT]
> Treat balances and history as pilot data. Mainnet needs the release gates in
> [Treasury policy §9](TREASURY_POLICY.md#9-mainnet-release-gates) to close
> first, including independent validators and an independent external audit.

## At a glance

| Topic | Today |
|---|---|
| Stage | Pilot network (pre-mainnet) |
| Block production | A **single block producer during recovery**, plus a backup node that follows the chain. The status API reports `peers: 0`. |
| Block time | 10 second target |
| Wallet transactions | Signed with **ML-DSA-87** (NIST FIPS 204, Cloudflare CIRCL) by Hive, the web wallet and `qsdmcli` |
| Other signatures | The generic peer-to-peer transaction-gossip verifier code still also accepts Ed25519, but the producer runs without transaction gossip ingress, so no Ed25519 transaction can reach the live chain today. Retiring that code path is planned. |
| Consensus messages | **Signed consensus enforced from block 827,000** (12 October 2026, 18:11 UTC): every BFT vote and consensus message must carry a valid ML-DSA signature, and unsigned consensus traffic is refused (`signed_consensus_active: true`). See [Consensus rules](CONSENSUS_RULES.md). |
| Transfer parents (Proof-of-Entanglement) | **Enforced from block 816,600** (11 October 2026): every CELL transfer names committed parents. Send with Hive 1.4.22 or newer, the web wallet, or `qsdmcli --auto-parents`. See [Proof-of-Entanglement](PROOF_OF_ENTANGLEMENT.md). |
| Mining | NVIDIA GPUs today, through QSDM Hive 1.4.22 (protocol v2, enrolled miners). See [Mining today](MINING_TODAY.md). |
| Supply | Max supply **90,000,000 CELL**, all from mining. A 10,000,000 CELL genesis treasury is a mainnet proposal and is not part of the current chain. |
| Release | `hardened-legacy-20261014-d7ffcd4-hl2-edge-go1269-ids`, live since 15 October 2026, 19:59 UTC (see [What is running](#what-is-running)) |
| Independent audit | Not done yet. The [internal security review](SECURITY_AUDIT.md) is a self-maintained checklist. |

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
| `peers` | Connected peer nodes. `0` is expected while a single producer runs the chain. |
| `consensus_auth.signed_consensus_active`, `signed_message_activation_height` | `true` from block 827,000: every consensus message must carry a valid ML-DSA signature. |
| `consensus_auth.unsigned_consensus_traffic_accepted` | `false`: unsigned consensus traffic is refused. |
| `consensus_auth.poe_activation_height`, `poe_active` | The block from which transfers must name committed parents, and whether that height has been reached. |
| `consensus_auth.edge_relay_only_activation_height`, `edge_relay_only_active` | From block 833,800, paid edge tasks settle only through an authorized settlement relay. |
| `consensus_auth.task_action_signatures_active`, `tx_content_root_active` | `true`: task actions must carry a valid signature, and the block's transaction root commits transaction contents (both from block 625,000). |
| `tokenomics.*` | Supply cap, amount mined so far, current block reward and the next halving height. Amounts are also given in dust (1 CELL = 100,000,000 dust). |
| `mining.protocol_versions_accepted` | Only mining protocol v2 proofs are accepted. |
| `mining.attestation_types_required` | The NVIDIA attestation types a proof may carry. |
| `mining.min_enroll_stake_dust` | Enrollment bond: 1,000,000,000 dust = 10 CELL. With `deferred_bond_from_rewards`, the bond can be paid from mining rewards. |

The [explorer](https://qsdm.tech/explorer.html) and the
[network page](https://qsdm.tech/network.html) show the same data in a browser.

## What is running

- **Core release** `hardened-legacy-20261014-d7ffcd4-hl2-edge-go1269-ids`
  (live since 2026-10-15 19:59 UTC), built from `QSDM/source` in this
  repository: the d7ffcd4 base, the September 2026 recovery patches, the
  hardened-legacy mining work (durable proof and nonce de-duplication,
  persistent pending rewards, and a stop-on-failure rule for block storage),
  the Proof-of-Entanglement parent rules (enforced from block 816,600),
  signed consensus (enforced from block 827,000), the relay-only rule for paid
  edge tasks (from block 833,800) and reserved built-in task IDs.
- **Upgrade history.** Every network upgrade, with its height, time and what
  users had to do, is listed in [Network upgrades](NETWORK_UPGRADES.md).
- **Timeline.** The chain was paused on 2026-09-22 and restarted on 2026-09-27
  by the same single producer. Public mining reopened on 2026-10-02.
- **QSDM Hive 1.4.22** for Windows and Linux from
  [qsdm.tech/downloads/hive-v2/](https://qsdm.tech/downloads/hive-v2/), signed
  with the rotated v2 release key. See
  [Downloads & verification](DOWNLOADS_AND_VERIFICATION.md).

## What is not active yet

These parts are designed or implemented in the code but are not switched on for
the live network. Documents that describe them are marked **Design** in these
docs.

- Multiple independent validators and BFT finality between them. Today one
  producer signs every consensus message; there are no independent
  validators yet.
- Retirement of the Ed25519 code path in the generic peer-to-peer
  transaction-gossip verifier. It cannot be reached on the live chain today
  (the producer has no transaction gossip ingress), but the code still exists.
- The genesis treasury multisig and an official genesis manifest.
- An independent external security audit.

See the [Roadmap](ROADMAP.md) for the order of work and
[Security model](SECURITY_MODEL.md) for what this means for your funds.

## Service availability during recovery

Some public services are limited while the network finishes its recovery:

| Service | State |
|---|---|
| Status, blocks, transactions, balances (read) | Available |
| Signed wallet transfers (`POST /api/v1/wallet/submit-signed`) | Available |
| Mining (work, challenge, submit) on `api.qsdm.tech` | Open to GPUs enrolled before the recovery, in public windows with per-IP rate limits |
| New mining enrollments | Paused during recovery |
| New account registration | Disabled during recovery (HTTP 503) |
| Signed task actions (`POST /api/v1/tasks/actions/submit-signed`) | Open since 2026-10-15. Built-in system task IDs are reserved: only the project's owner key can register them. |
| Paid resource work (Edge Pool CPU/GPU/RAM tasks) | Operator-only test; not yet open to other miners. From block 833,800 these tasks settle only through an authorized settlement relay. |
| Operator dashboard | Private; not offered publicly |

If something on this page disagrees with the status API, the API is right.
Please [open an issue](https://github.com/blackbeardONE/QSDM/issues) so the
page can be fixed.
