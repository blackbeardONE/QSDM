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

- **Ed25519 retirement.** The generic peer-to-peer transaction-gossip verifier
  code still accepts Ed25519 signatures. The producer runs without transaction
  gossip ingress, so no Ed25519 transaction can reach the live chain today.
  Wallets, task actions and consensus use ML-DSA. Removing the Ed25519 path is
  planned and is not a consensus rule yet.
- **Independent validators.** There is one block producer and no independent
  validators. Multi-validator BFT voting is the next step and has not started
  publicly.
- **Independent external audit.** None has been completed. The
  [security review](SECURITY_AUDIT.md) is a self-assessment.

See the [Roadmap](ROADMAP.md) and [Security model](SECURITY_MODEL.md).
