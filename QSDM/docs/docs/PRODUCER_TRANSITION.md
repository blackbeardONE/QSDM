# Producer transition

The producer transition tells a node which consensus key may sign the block at
each height, and which history it must already hold. It is the
`[consensus.producer_transition]` table in the node's configuration file
(`producer_transition` under `consensus` in YAML). Every node that follows the
chain must use the same table.

Without the table, a node accepts blocks from the producers listed in
`[consensus] authorized_block_producers`. With it, the node checks the table
instead and does not read that list (see the stale-allowlist warning in
[Private P2P mode](PRIVATE_P2P.md#4-the-stale-producer-allowlist-warning)).

There are two versions of the table:

- **Version 1** describes exactly one hand-over: a historical producer, then a
  replacement producer. The public network has had one hand-over so far, at
  height 659,977, and a version 1 table describes it.
- **Version 2** lists every producer the chain has had, in order, as producer
  epochs. It is needed for a second hand-over, for example to a standby
  producer.

A node that runs this code with its existing version 1 table behaves exactly as
before. Version 2 applies only when a table says `version = 2`.

## 1. Version 1

```toml
[consensus.producer_transition]
version = 1
historical_signature_height = <first signed height>
historical_producer = "<64 hex consensus address>"
checkpoint_height = <last height of the historical producer>
checkpoint_hash = "<hash of block checkpoint_height>"
effective_height = <checkpoint_height + 1>
replacement_producer = "<64 hex consensus address>"
historical_prefix_bytes = <bytes>
historical_prefix_sha256 = "<64 hex>"
```

Rules, checked when the configuration loads and again when the node installs
the policy:

- `checkpoint_height` is at least 1 and `effective_height` is
  `checkpoint_height + 1`;
- `historical_signature_height` is between 1 and `checkpoint_height`;
- `checkpoint_hash`, both producers and `historical_prefix_sha256` are 64
  lowercase hexadecimal characters and not all zero;
- the two producers differ, and `historical_prefix_bytes` is positive;
- a version 1 table must not contain `epochs`.

A consensus address is the hex SHA-256 of the producer's ML-DSA-87 consensus
public key.

## 2. Version 2

Version 2 lives in the same table, so a build that only knows version 1 refuses
a version 2 table at startup ("version must be 1") instead of ignoring a key it
does not know:

```toml
[consensus.producer_transition]
version = 2
historical_prefix_bytes = <bytes>          # journal prefix pin, as in version 1
historical_prefix_sha256 = "<64 hex>"

[[consensus.producer_transition.epochs]]   # epoch 0: no checkpoint
from_height = <first signed height>
producer_id = "<64 hex consensus address>"

[[consensus.producer_transition.epochs]]   # one table per hand-over
from_height = <checkpoint height + 1>
producer_id = "<64 hex consensus address>"
checkpoint_hash = "<hash of block from_height - 1>"
```

In YAML, `epochs` is a list under `consensus.producer_transition`, with the same
keys.

Rules:

| Rule | Why |
|---|---|
| `version = 2`, and none of the version 1 fields (`checkpoint_height`, `checkpoint_hash`, `historical_signature_height`, `historical_producer`, `replacement_producer`, `effective_height`) is set | A table that mixes the two forms is refused at startup. |
| 2 to 64 epochs | A schedule exists to describe at least one hand-over. The bound keeps a malformed table from becoming a cost on every block. |
| `epochs[0].from_height` is at least 1, and epoch 0 has no `checkpoint_hash` | Genesis is never signed. Epoch 0 starts where signing started. |
| `from_height` strictly increases | An epoch is defined by its start height only, so this also means no gaps and no overlaps. |
| Every later epoch has a `checkpoint_hash` (64 lowercase hex, not all zero) | Each hand-over is pinned to the last block of the previous producer, so a hand-over can only be written after that producer has stopped and its last block is known. A future hand-over cannot be scheduled in advance. |
| Every `producer_id` is 64 lowercase hex, not zero, and unique in the whole schedule | A key is never reused after a hand-over. Moving back to an earlier host means a new epoch with a new key. |
| `historical_prefix_bytes` is positive and `historical_prefix_sha256` is valid | The same journal prefix pin as version 1. |

## 3. Which blocks are accepted

For a block at height `h`, under either version:

- **From the first epoch on** (`h` at or above `epochs[0].from_height`): the
  block must be signed, and its `producer_id` must be the producer of the epoch
  that contains `h`.
- **Below the first epoch:** the block may be unsigned. A signed block must
  come from the first epoch's producer.
- **Hand-over pins:** for every epoch after the first, block `from_height - 1`
  must have the hash `checkpoint_hash`, and block `from_height` must name it as
  `prev_hash`.
- **Appending:** a node accepts a block in an epoch only once its own chain
  holds every hand-over checkpoint up to that epoch.
- **Producing:** only the **final** epoch's producer may produce blocks, and
  only on top of the final checkpoint. The node refuses to start as a producer
  if its consensus signer is not the final epoch's producer, and a producer of
  an earlier epoch is refused before any transaction leaves the mempool.

Everything else is the same as in version 1: the journal prefix check before
startup, validation of the whole chain from genesis before it is restored, no
automatic repair or truncation of a chain that does not match, and the strict
enrollment snapshot. Replay mode uses the same policy as normal startup.

### Version 1 is a two-epoch schedule

The node enforces a version 1 table as this schedule:

```
epoch 0: from_height = historical_signature_height, producer_id = historical_producer
epoch 1: from_height = effective_height, producer_id = replacement_producer,
         checkpoint_hash = checkpoint_hash
```

Version 1 already requires `effective_height = checkpoint_height + 1`, so the
two forms describe the same rule. Existing version 1 tables need no change. The
tests in section 7 check this against the rule as it was written before
version 2, with the heights of the public network's hand-over.

The legacy state-root boundary stays at the first hand-over (version 1:
`effective_height`); a later hand-over does not move it.

### Startup log

The line `Checkpoint-bound producer transition enforced` shows `version` and
`epochs` (`producer@from_height`, oldest first). Its fields
`checkpoint_height`, `checkpoint_hash`, `effective_height` and
`replacement_producer` describe the **final** hand-over, so for a version 1
table they show the same values as before. The schedule is not published in
`GET /api/v1/status`.

## 4. Writing a version 1 table as version 2

Copy the fields into two epochs and keep the prefix pin:

```toml
[consensus.producer_transition]
version = 2
historical_prefix_bytes = <historical_prefix_bytes>
historical_prefix_sha256 = "<historical_prefix_sha256>"

[[consensus.producer_transition.epochs]]
from_height = <historical_signature_height>      # 482566 on the public network
producer_id = "<historical_producer>"

[[consensus.producer_transition.epochs]]
from_height = <effective_height>                 # 659977 on the public network
producer_id = "<replacement_producer>"
checkpoint_hash = "<checkpoint_hash>"
```

The node behaves identically with either table. There is no need to convert a
table until a second hand-over.

## 5. A second hand-over

At most one producer may produce at any time. The steps:

1. **Upgrade every node first.** Every node (the producer and every follower)
   must run a build that understands version 2 while it keeps its version 1
   table. Nothing changes yet. A build without version 2 refuses a version 2
   table at startup; that is safe, but it stops the node.
2. **Stop the old producer** and make sure it cannot start again as a producer.
   Record its last block `N` and that block's hash `H_N` from its own journal.
   Check on every follower that its tip is at or below `N` and that its block
   `N` has the hash `H_N`.
3. **Write the new table.** Copy the current epochs exactly, then add
   `{from_height = N + 1, producer_id = <new producer>, checkpoint_hash = H_N}`.
   Use the same file on every node. A follower with a different schedule
   rejects the new producer's blocks and stops following.
4. **Optionally re-pin the prefix:** the byte length and SHA-256 of the journal
   up to and including the line of block `N`, which must end on a newline.
   Compute them on a stopped copy of the journal. Keeping the old pin is also
   valid, because the journal only grows.
5. **Restart the followers** with the new table. Each one validates its history
   from genesis, accepts the old blocks up to `N`, and from `N + 1` accepts only
   the new producer's key.
6. **Start the new producer** with its consensus signer key already in place.
   It refuses to start if its signer is not the final epoch's producer or if
   the key file is missing. Create the key in advance, offline, with
   `qsdm-valkey generate --out <file>` (from `QSDM/source`:
   `go run -tags dilithium_circl ./cmd/qsdm-valkey generate --out <file>`);
   the `validator_address` it prints is the new epoch's `producer_id`, and
   `qsdm-valkey show --in <file>` prints it again.
7. **Check** that the followers accept block `N + 1`, that its `prev_hash` is
   `H_N` and its `producer_id` is the new producer, and that a block from the
   old key at or above `N + 1` is refused (`ErrExternalProducerNotAuthorized`).
8. **The old producer's host** must use the new table (as a follower) or stay
   down. With the new table it cannot produce.

**Never remove or edit an earlier epoch.** Dropping epoch 0 makes every node
reject the blocks its producer signed, and changing epoch 1 moves the legacy
state-root boundary and changes the restored state.

## 6. What can go wrong

| Situation | Result |
|---|---|
| A node runs a build without version 2 and gets a `version = 2` table | Startup refused ("version must be 1"). No fork. Upgrade first (step 1). |
| The table mixes version 1 and version 2 fields, or a version 1 table has `epochs` | Startup refused at configuration validation. |
| Malformed schedule: one epoch, unordered or equal heights, a missing or zero checkpoint, uppercase hex, a repeated producer, `from_height = 0` | Startup refused. |
| Wrong `checkpoint_hash` | The restore refuses the history ("checkpoint hash mismatch"), or block `N + 1` is rejected. The node stops following; it does not fork. |
| A follower already holds a block above `N` from the old producer | The restore refuses it, because that producer is wrong for that height. The node never truncates or repairs automatically, so an operator must repair it by hand. Choose `N` as the old producer's real last block and check every node first (step 2). |
| A follower does not yet hold the checkpoint (it is behind) | It catches up through the earlier epochs and accepts the new epoch once block `N` is in its own chain ("checkpoint is not established" until then). |
| Nodes have different schedules | Nodes with the wrong table reject the new producer's blocks and stop following. Distribute one file. |
| The old producer is restarted with the old table after the hand-over | It would produce a competing chain from `N + 1`. Followers with the new table reject it; preventing this is the point of step 2. |
| The new producer has the wrong signer key | Startup refused ("configured local signer is not the approved replacement producer"). |
| The prefix pin is wrong | Startup refused at the journal prefix check. |
| `authorized_block_producers` still lists an old producer | Ignored while the table is configured, as in version 1, but the node logs the stale-allowlist warning. Update it anyway, so that removing the table later fails safe. |

## 7. Code and tests

- `pkg/producerpolicy/transition.go`: `Epoch`, version 2 validation,
  `Schedule`, `ProducerAt`, `FirstHandoverHeight` and `FinalEpoch`.
- `pkg/chain/producer_transition.go`: the block, append and produce rules over
  epochs; `pkg/chain/block.go` keeps the schedule computed once.
- `cmd/qsdm/main.go`: the state-root boundary, the local producer check and
  the startup log.

Tests:

- `pkg/producerpolicy/transition_v2_test.go`: the schedule, version 1 as its
  two-epoch schedule on the public network's heights, and every validation
  rule above.
- `pkg/chain/producer_transition_v2_test.go`:
  `TestProducerTransitionV1V2EquivalentOnLiveShapedHeights` keeps the version 1
  block rule as written before version 2 and checks that version 1, enforced
  as its schedule, and the same table written as version 2 give the same
  verdict and the same error text for heights around 482,566 and 659,977,
  five producer IDs, signed and unsigned blocks and every combination of block
  and parent hash. The other tests run real ML-DSA-87 keys over three epochs:
  each height is accepted only from its epoch's key, every hand-over is pinned,
  a journal crossing a hand-over replays and restores, only the final epoch's
  producer can produce, and the installed schedule cannot be changed.
- `pkg/config/producer_transition_test.go`: loading version 2 from TOML and
  YAML, and refusing a mixed table.
- `pkg/config/live_shape_test.go` and `cmd/qsdm/live_shape_test.go`: a
  configuration shaped like the block producer's keeps its version 1 meaning:
  the same two epochs, the same state-root boundary and the same producer.
- `cmd/qsdm/producer_transition_v2_test.go`: the state-root boundary in both
  versions, and a real state directory going from producer A to B (version 1)
  to C (version 2): B cannot produce, C produces across the boundary, a crash
  is recovered by replay mode, and the version 1 table can no longer start the
  node.
- `cmd/qsdm/private_p2p_test.go`: the stale-allowlist warning names the final
  epoch's producer.

## 8. Not yet known

- How long a follower with a large journal takes to validate its history from
  genesis at startup. Every restart with a transition table already does this,
  so check that the service manager's start timeout allows for it.
- A reviewed tool for computing a new prefix pin on a live journal does not
  exist yet; until it does, keep the existing pin (step 4 is optional).
