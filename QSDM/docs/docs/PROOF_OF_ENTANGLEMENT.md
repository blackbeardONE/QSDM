# Proof-of-Entanglement (PoE)

Proof-of-Entanglement ties every signed CELL transfer to transactions the
network has already committed. Each transfer names *parent cells*: the IDs of
earlier transactions, carried inside the envelope the sender signs.

## Status (October 2026)

- **Running today:** every signed wallet transfer is verified with ML-DSA-87
  under the sender's own public key, and the sender address must equal
  `hex(sha256(public_key))`. The producer checks this before a transfer is
  queued, and every follower checks it again when it replays a block.
  `parent_cells` are part of the signed bytes, so nobody can change them
  after signing.
- **Implemented, not yet active:** the parent rules below. Validators enforce
  them only from a configured activation height
  (`[consensus] poe_activation_height`, default `0` = never). Until the
  network sets that height, parents are not checked. Every transfer on the
  chain so far carries an empty `parent_cells` list, and blocks below the
  activation height keep replaying with the rules they were produced under.
- **Network shape:** QSDM runs as a single-producer pilot. Follower nodes
  replay and verify every block. Multi-validator BFT voting is not active
  yet (see [Relation to multi-validator BFT](#relation-to-multi-validator-bft)).

## The rules

From the activation height on, a signed wallet transfer (`qsdm/wallet-transfer/v1`)
at block height `h` is valid only if:

1. it names **2 to 10 parents**, each 16–128 characters of `0-9 A-Z a-z _ -`;
2. no parent repeats, and no parent equals the transfer's own ID;
3. its own ID is not already used by a transaction committed in the reference
   window or earlier in the same block;
4. every parent is a transaction **committed at a height from `h-8640` to
   `h-1`**, or a transaction **earlier in the same block**.

The window is 8,640 blocks, about 24 hours at the 10-second block target.
Rule 4 makes every reference point strictly backwards in the chain, so the
parent graph cannot contain a cycle.

Each node works out which parents are valid from its own copy of the chain.
It never uses receipts or the mempool for this, so every node gets the same
answer. Producer heartbeats, mining rewards and other transaction families
are not affected.

If a transfer breaks a rule:

| Where | What happens |
|---|---|
| `POST /api/v1/wallet/submit-signed` | HTTP **422** `proof-of-entanglement: …; sign again with parents from GET /api/v1/chain/parents` |
| Block production | the transfer is dropped with a failed receipt; account state is untouched |
| A follower replaying a block | the whole block is refused (same error on every node) |
| Peer-to-peer gossip ingest | the message is dropped |

## Getting parents (wallets and integrations)

Call the node just before signing and put `parents` into `parent_cells`:

```
GET /api/v1/chain/parents
```

```json
{
  "tip": 805400,
  "poe_activation_height": 0,
  "poe_active": false,
  "min_parents": 2,
  "max_parents": 10,
  "window_blocks": 8640,
  "parents": ["solo-heartbeat-…-…", "solo-heartbeat-…-…"],
  "recent": [{"id": "solo-heartbeat-…-…", "height": 805400}]
}
```

The route is public and read-only. `parents` holds the node's two newest
committed transaction IDs, read up to the node's durable tip. These parents
are valid **both before and after** activation, so a wallet that always uses
them needs no change at the activation height. Nodes released before this
route existed still serve `GET /api/v1/receipts`. The newest successful
receipts (`status: 1`) are an equivalent fallback.

First-party clients that sign transfers:

- **qsdmcli:** `qsdmcli wallet sign-tx --auto-parents` (the funding scripts
  pass it).
- **Hive:** fetches parents for every transfer. On a 422
  `proof-of-entanglement` it signs again at the same nonce with fresh
  parents.
- **Web wallet:** fetches parents when the parent-cells field is empty.
- **qsdm-game-signer:** journals the parents with each signed payout. If a
  journaled envelope's parents have expired, it signs the same transaction ID
  and nonce again over fresh parents. At most one envelope per nonce can
  ever be applied, so this cannot pay twice.

A signed transfer stays valid for about a day after its parents were
committed. Sign shortly before you submit.

## Operators

| Setting | Default | Meaning |
|---|---|---|
| `[consensus] poe_activation_height` / `QSDM_POE_ACTIVATION_HEIGHT` | `0` | First height at which the rules are consensus rules. Every validator must use the same value. |

- While a height is configured but not yet reached, `submit-signed` rejects
  nothing. It counts the transfers enforcement *would* reject in
  `qsdm_poe_shadow_would_reject_total{reason}`. Use this to see when clients
  are ready.
- Admission starts enforcing 360 blocks (about an hour) before the
  activation height. A transfer without valid parents therefore cannot still
  be waiting in a mempool when production starts enforcing.
- Metrics: `qsdm_poe_activation_height`,
  `qsdm_poe_rejected_total{reason}`,
  `qsdm_poe_external_blocks_refused_total` and `qsdm_poe_history_ids`.
  `GET /api/v1/status` reports `consensus_auth.poe_activation_height` and
  `consensus_auth.poe_active`.
- Roll out in this order:
  1. upgrade every node with the height unset;
  2. release the clients;
  3. watch the shadow counters;
  4. set the same future height on the producer first, then on every
     follower.

  A follower that enforces when the producer does not would stop at the
  first block that breaks a rule.
- To turn enforcement off again, set the height back to `0` on the followers
  first, then on the producer. Blocks produced while it was on stay valid
  under both settings.

## Relation to multi-validator BFT

The parent rules are deterministic block-validity rules derived only from the
committed chain. Every validator that replays the same blocks reaches the
same verdict, and tests check this with several in-process validators
replaying honest and adversarial blocks. That makes them safe to keep when
several validators vote on blocks. They do not replace BFT voting, which
remains the mechanism for agreeing on which block comes next.
