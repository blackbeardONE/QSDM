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
| Other signatures | The generic peer-to-peer transaction verifier still also accepts Ed25519 |
| Consensus messages | Signed consensus is supported in the node but **not yet enforced** (`signed_consensus_active: false`) |
| Mining | NVIDIA GPUs today, through QSDM Hive 1.4.21 (protocol v2, enrolled miners). See [Mining today](MINING_TODAY.md). |
| Supply | Max supply **90,000,000 CELL**, all from mining. A 10,000,000 CELL genesis treasury is a mainnet proposal and is not part of the current chain. |
| Release | `hardened-legacy-20261002-d7ffcd4-hl2` (see [What is running](#what-is-running)) |
| Independent audit | Not done yet. The [internal security review](SECURITY_AUDIT.md) is a self-maintained checklist. |

## Check it yourself

Everything above can be checked against the public status endpoint. It needs no
key and is served on both hosts:

```bash
curl -s https://api.qsdm.tech/api/v1/status
```

Trimmed response (October 2026):

```json
{
  "version": "hardened-legacy-20261002-d7ffcd4-hl2",
  "git_sha": "d7ffcd4e80f69c3db3f2bbcf4c7f34e08cc49d25",
  "chain_tip": 755950,
  "peers": 0,
  "node_role": "validator",
  "coin": { "name": "Cell", "symbol": "CELL", "decimals": 8, "smallest_unit": "dust" },
  "tokenomics": {
    "cap_cell": "90000000.00000000",
    "emitted_cell": "2694893.61622650",
    "block_reward_cell": "3.56490987",
    "current_epoch": 0,
    "next_halving_height": 12623041,
    "target_block_time_seconds": 10
  },
  "consensus_auth": {
    "signed_consensus_supported": true,
    "signed_consensus_active": false,
    "unsigned_consensus_traffic_accepted": true,
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
| `consensus_auth.signed_consensus_active` | `false`: consensus messages are not required to be signed yet. |
| `consensus_auth.task_action_signatures_active` | `true`: task actions must carry a valid signature. |
| `tokenomics.*` | Supply cap, amount mined so far, current block reward and the next halving height. Amounts are also given in dust (1 CELL = 100,000,000 dust). |
| `mining.protocol_versions_accepted` | Only mining protocol v2 proofs are accepted. |
| `mining.attestation_types_required` | The NVIDIA attestation types a proof may carry. |
| `mining.min_enroll_stake_dust` | Enrollment bond: 1,000,000,000 dust = 10 CELL. With `deferred_bond_from_rewards`, the bond can be paid from mining rewards. |

The [explorer](https://qsdm.tech/explorer.html) and the
[network page](https://qsdm.tech/network.html) show the same data in a browser.

## What is running

- **Core release** `hardened-legacy-20261002-d7ffcd4-hl2`, built from
  `QSDM/source` in this repository: the d7ffcd4 base, the September 2026
  recovery patches and the hardened-legacy mining work (durable proof and
  nonce de-duplication, persistent pending rewards, and a stop-on-failure rule
  for block storage).
- **Timeline.** The chain was paused on 2026-09-22 and restarted on 2026-09-27
  by the same single producer. Public mining reopened on 2026-10-02.
- **QSDM Hive 1.4.21** for Windows and Linux from
  [qsdm.tech/downloads/hive-v2/](https://qsdm.tech/downloads/hive-v2/), signed
  with the rotated v2 release key. See
  [Downloads & verification](DOWNLOADS_AND_VERIFICATION.md).

## What is not active yet

These parts are designed or implemented in the code but are not switched on for
the live network. Documents that describe them are marked **Design** in these
docs.

- Multiple independent validators and BFT finality between them.
- Enforced signing of consensus messages.
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
| Edge relay settlement (Mother Hive pools) | Paused (read-only) |
| Operator dashboard | Private; not offered publicly |

If something on this page disagrees with the status API, the API is right.
Please [open an issue](https://github.com/blackbeardONE/QSDM/issues) so the
page can be fixed.
