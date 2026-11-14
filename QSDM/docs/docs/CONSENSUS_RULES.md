# Consensus rules

> **Status:** the rules in force on the public pilot network (pre-mainnet) as
> of October 2026. QSDM runs with a single block producer and no independent
> validators yet. The producer applies these rules when it builds a block, and
> every follower applies them again when it replays the block.

This page lists the block-validity rules in one place. Each rule switches on
at an activation height and stays on. `GET https://api.qsdm.tech/api/v1/status`
reports every rule in its `consensus_auth` block; if this page and the API
disagree, the API is right. The history of each upgrade is in
[Network upgrades](NETWORK_UPGRADES.md).

"MUST" and "MUST NOT" below describe what a node refuses. A block that breaks
a rule at or above its activation height is invalid; a follower refuses the
whole block. Blocks below an activation height keep replaying under the rules
they were produced with.

## Rules in force

| Rule | Activation height | Live since (UTC) | Status field in `/api/v1/status` | What is rejected |
|---|---|---|---|---|
| Signed task actions | 625,000 | Before October 2026 | `consensus_auth.task_action_signatures_active` | A task action without a valid ML-DSA signature |
| Transaction content root | 625,000 | Before October 2026 | `consensus_auth.tx_content_root_active` | A block whose transaction root does not commit the transactions' contents |
| Proof-of-Entanglement | 816,600 | 2026-10-11 13:18 | `consensus_auth.poe_activation_height`, `poe_active` | A transfer that does not name 2 to 10 valid committed parents |
| Signed consensus | 827,000 | 2026-10-12 18:11 | `consensus_auth.require_signed_votes`, `signed_message_activation_height`, `signed_consensus_active`, `unsigned_consensus_traffic_accepted` | Any BFT vote or consensus message without a valid ML-DSA signature |
| Relay-only paid edge tasks | 833,800 | 2026-10-13 13:21 | `consensus_auth.edge_relay_only_activation_height`, `edge_relay_only_active` | A paid edge task settlement that does not come through an authorized settlement relay |
| Transaction ID uniqueness | 1,160,000 | Scheduled, about 2026-11-21 (the producer's API refuses a taken ID since 2026-11-13 18:41) | none (a code constant, `TxIDUniqueActivationHeight`) | A transaction whose ID is committed in the last 8,640 blocks or earlier in the same block, whatever its contract |
| Task migrate authorization | 1,150,000 | Scheduled, about 2026-11-19 (the producer's API enforces it since 2026-11-12 21:25) | none (a code constant, `TaskMigrateAuthActivationHeight`) | A task `migrate` not sent by the task's catalog manager, or to a target that is not another registered, unpaused task of the same manager, or that would make a cycle |

A node built from `QSDM/source` must use the same activation heights as the network it follows. The
relay-only rule is the one set in the node configuration: `[consensus] edge_relay_only_activation_height = 833800`
(or `QSDM_EDGE_RELAY_ONLY_ACTIVATION_HEIGHT=833800`) for the public pilot network; zero, the default, never
activates it. The reserved built-in task IDs have no activation height: every catalog action for them on the
public network was sent by the QSDM task manager wallet.

Live values on the public node:

```json
"consensus_auth": {
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
}
```

## The rules in detail

### Signed task actions (from block 625,000)

- A `qsdm/tasks/v1` task action in a block at or above height 625,000 MUST
  carry a valid ML-DSA signature from its sender, where
  `sender == hex(sha256(public_key))`.
- A signature that is present MUST verify at every height, also below the
  activation height.
- Since the node release of 15 October 2026, built-in system task IDs are
  reserved: only the project's owner key can register them.

Background: [Signed consensus rollout](SIGNED_CONSENSUS_ROLLOUT.md#task-action-signatures),
[Task registry](QSDM_TASK_REGISTRY.md).

### Transaction content root (from block 625,000)

- From height 625,000 a block's transaction root MUST commit the contents of
  its transactions, not only their IDs. Amount, recipient, payload and
  signature therefore cannot be changed without changing the block hash.
- Unlike the other rules, this one changes what a node computes. A node with a
  different activation height derives different block hashes and stops
  following the chain.

Background: [Signed consensus rollout](SIGNED_CONSENSUS_ROLLOUT.md#transaction-content-root-coordinated-fork).

### Proof-of-Entanglement (from block 816,600)

A signed wallet transfer (`qsdm/wallet-transfer/v1`) at height `h` MUST:

1. name 2 to 10 parents in `parent_cells`;
2. not repeat a parent, and not name its own ID as a parent;
3. use an ID that is not already used by a committed transaction in the
   reference window or earlier in the same block;
4. name only parents committed at heights `h-8640` to `h-1`, or earlier in the
   same block.

The public API started refusing transfers without valid parents (HTTP 422) at
block 816,240, shortly before the consensus rule applied at 816,600. Wallets
get parents from `GET /api/v1/chain/parents`. Details:
[Proof-of-Entanglement](PROOF_OF_ENTANGLEMENT.md).

### Signed consensus (from block 827,000)

- Every BFT vote and consensus message MUST carry a valid ML-DSA signature.
- Unsigned consensus traffic MUST be refused
  (`unsigned_consensus_traffic_accepted: false`).
- A signature that is present but invalid is refused at every height.

This rule stops forged consensus messages. It does not add validators: the
single producer signs every consensus message today. Details:
[Signed consensus rollout](SIGNED_CONSENSUS_ROLLOUT.md).

### Relay-only paid edge tasks (from block 833,800)

- A paid edge task (`qsdm-edge-worker` and its GPU and RAM variants) MUST
  settle only through an authorized settlement relay: a Relay whose
  key-derived ID is listed in the task's `authorized_relay_ids`, with a valid
  ML-DSA-87 Relay signature.
- Any other settlement of these tasks is rejected.

Paid resource work is in an operator-only test and is not yet open to other
miners. Details: [Edge pool](EDGE_POOL.md),
[Task registry](QSDM_TASK_REGISTRY.md).

### Task migrate authorization (from block 1,150,000)

- A `migrate` task action (payload `{"migrated_to":"<task id>"}`) MUST come
  from the task's catalog manager: the task is registered in the catalog and
  the sender equals its manifest's `manager`.
- The target MUST be another registered task of the same manager that is not
  paused, and following `migrated_to` from the target MUST NOT lead back to
  the task (a chain of more than 64 steps is refused too).
- A block that carries any other `migrate` is rejected. Blocks below
  1,150,000 replay as they were produced.

The producer's API (`/api/v1/tasks/actions/submit-signed`) has refused such
a migrate with 403 since node release rc1110 (2026-11-12 21:25 UTC), and
the producer never puts a refused action in a block, so full nodes on older
releases keep following until they upgrade. Code:
`QSDM/source/pkg/chain/task_migrate_auth.go`. Details:
[Task registry](QSDM_TASK_REGISTRY.md),
[Network upgrades](NETWORK_UPGRADES.md).

### Transaction ID uniqueness (from block 1,160,000)

- A transaction ID names one transaction, whatever its contract. From block
  1,160,000 a block MUST NOT carry a transaction whose ID is committed in the
  Proof-of-Entanglement reference window (the last 8,640 blocks) or earlier in
  the same block. Wallet transfers follow this rule since block 816,600; it
  now covers every contract: task actions, enrollments, mining and system
  transactions.
- A block that breaks it is rejected. Blocks below 1,160,000 replay as they
  were produced.
- Receipts are final: once an ID has a committed receipt, no later
  transaction changes it.

The producer's API (`/api/v1/tasks/actions/submit-signed`) has refused a
signed task action whose ID another transaction holds since node release
rc1111 (2026-11-13 18:41 UTC): 409, `status` `duplicate`, `mempool_status`
`id_taken`. The producer leaves such a transaction out of its blocks, so full
nodes on older releases keep following until they upgrade. Code:
`QSDM/source/pkg/chain/txid_unique.go`. Details:
[Network upgrades](NETWORK_UPGRADES.md).

## Other checks on every transfer

These have no activation height; they apply to every signed transfer:

- the ML-DSA-87 signature MUST verify under the sender's public key, and the
  sender address MUST equal `hex(sha256(public_key))`;
- the nonce MUST be the sender's next nonce (no replays, no gaps);
- the balance MUST cover amount plus fee.

See [Signed transfers](V040_WALLET_SEND_DESIGN.md) and
[Replay protection](V041_REPLAY_PROTECTION_DESIGN.md). Mining admission rules
(protocol v2 only, NVIDIA attestation, signed enrollment, 10 CELL bond) are in
[Mining protocol v2](MINING_PROTOCOL_V2.md).

## Not yet

- **Ed25519 stream session keys.** Ed25519 transactions are refused since
  16 October 2026 (`ed25519_tx_accepted: false`). That is an admission rule,
  not a block rule: a block does not record a transaction's signature
  algorithm. CELL stream session keys are still Ed25519. The block rule that
  retires them is in the code but has no height yet: in a block at or above
  `[consensus] stream_ed25519_retirement_height` (env
  `QSDM_STREAM_ED25519_RETIREMENT_HEIGHT`), a stream open, which registers an
  Ed25519 session key, MUST be refused; streams opened below it can still take
  receipts, settle and close. Today `stream_ed25519_retirement_height` is `0`
  and `stream_ed25519_session_keys_retired` is `false`. Metrics:
  `qsdm_tx_gossip_ed25519_rejected_total` (Ed25519 transactions refused) and
  `qsdm_stream_ed25519_retirement_height`.
- **QSDM-20 tokens.** Native tokens (contract `qsdm/token/v1`) are in the code
  but switched off: their rules are in [QSDM-20 tokens](QSDM_20.md). Below
  `[consensus] qsdm20_activation_height` (env `QSDM_QSDM20_ACTIVATION_HEIGHT`),
  and always while it is `0`, nodes refuse token transactions at admission, and
  a block is judged exactly as before QSDM-20: a `qsdm/token/v1` transaction
  that reaches a block anyway is a plain CELL transfer, so no block that is
  valid today becomes invalid. The token state stays empty and no state root
  changes. A release that reports the field shows
  `consensus_auth.qsdm20_activation_height` `0` and `qsdm20_active` `false`.
- **Decentralised block production.** There is one block producer at a time,
  run by the project, and no independent validators. Since 19 October 2026 the
  plan is for miners to produce blocks, in stages, instead of a set of
  independent validators; it is designed, not built, and it is the next step.
  On 13 November 2026 block production moved to the project's full node in
  Japan, the standby block maker, for a live turn (blocks 1,095,602 to
  1,095,784) and back to the producer with a new key; there is still one
  block producer at a time.
- **Independent external audit.** None has been completed. The
  [security review](SECURITY_AUDIT.md) is a self-assessment. Review now runs as
  an open [community review with a bug bounty](COMMUNITY_REVIEW.md), which is
  not an audit; a professional audit remains welcome.

See the [Roadmap](ROADMAP.md) and [Security model](SECURITY_MODEL.md).
