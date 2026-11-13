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
  replacement producer. Until 13 November 2026 the public network had had one
  hand-over, at height 659,977, and a version 1 table described it.
- **Version 2** lists every producer the chain has had, in order, as producer
  epochs. It is needed for a second hand-over, for example to a standby
  producer. The public network uses it since the live turn of 13 November
  2026 ([The public schedule](#the-public-schedule)).

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
table until a second hand-over. `qsdm-handover` (section 5) writes this
conversion and the new epoch in one step.

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
   rejects the new producer's blocks and stops following. `qsdm-handover`
   writes the file (see "A full node as the new producer" below).
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

### A full node as the new producer (a stand-in)

A full node ([Run a full node](RUN_A_FULL_NODE.md)) already holds what a
producer needs: the whole journal from genesis, the state, and a consensus key
of its own, which it creates on its first start (`qsdm_consensus_signer.json`
in its state directory, unless `[consensus] signer_key_path` names another
file). The startup line `Validator consensus signer ready` prints the key's
address. When the producer is gone for good, such a node can take over block
production with the steps above. Four things differ:

1. **Its own key is the new epoch's producer.** The key holds no funds and
   gains no power until the nodes install a table that names it.
   `qsdm-valkey show --in <key file>` prints its address again.
2. **`qsdm-handover` writes the table.** It reads the node's current table
   (version 1 or version 2) with the node's own loader, adds the new epoch,
   checks the schedule with the node's own rules and writes a new file; it
   never overwrites one. Then it loads that file as the node will, and removes
   it again unless the schedule is exactly the new one and every other setting
   is unchanged. From `QSDM/source`:

   ```bash
   go build -tags dilithium_circl ./cmd/qsdm-handover
   ./qsdm-handover --config /etc/qsdm-follower/core.toml --out core-handover.toml \
     --from-height <N+1> --producer <the node's address> --checkpoint-hash <H_N>
   ```

3. **Seed its served watermark.** The producer role requires the served
   watermark `hl1-served-watermark.json`, and a full node never writes one, so
   the producer role refuses a full node's directory ("watermark missing: run
   R-H (or R-B)"). With the node stopped and the new file installed, seed it
   with the `hl1-tail` of the node's release, as the node's service user:

   ```bash
   sudo runuser -u qsdm-follower -- env CONFIG_FILE=/etc/qsdm-follower/core.toml \
     <release folder>/hl1-tail watermark seed --served-tip <N> --follower-height <N> --follower-hash <H_N>
   ```

   `--served-tip` is the old producer's last block `N`. The follower height and
   hash come from a second node that holds block `N`, so the seed refuses a
   journal that ends below `N` (the old producer served a block this node never
   received), one whose block `N` differs, and one with a torn last line.
4. **Start it in the producer role.** Set `QSDM_NETWORK_BLOCK_PRODUCER=1` and
   remove `QSDM_CHAIN_SYNC_URLS` (the producer role does not sync), then start
   it. Its first block is `N + 1`, on top of `H_N`. The other nodes install the
   same file and set `QSDM_CHAIN_SYNC_URLS` to the new producer's API, which
   must be reachable from them.

What does not move with the chain: the producer's mining records live in
their own directory (`QSDM_LEGACY_MINING_*`), not in the journal, and clients
that use `api.qsdm.tech` reach the new producer only once that name points at
it. Without the records a stand-in starts with mining off (no
`QSDM_LEGACY_MINING_*` settings) or with new records, which the node creates
at its first start from block `N + 1`; either way the old producer's accepted
but unpaid proofs are not paid by the new one. The next section moves the
records too.

`TestStandInPromotesAFollower` (section 7) runs these steps on real state
directories.

### Carrying the mining records in a planned hand-over

When the old producer stops on purpose, its mining records can move with the
chain, so that mining continues on the new producer with nothing lost:

1. **Stop the old producer** (step 2). Its served watermark must equal its
   journal's last block `N`: the records then hold exactly the payments in the
   chain up to `N`.
2. **Copy the records byte for byte**: `legacy-mining.db` and, if it exists,
   `legacy-mining.db-wal` from its legacy-mining directory. Never copy
   `legacy-mining.db-shm`. Compare the SHA-256 of each copy with the original.
3. **Install them** in the new producer's legacy-mining directory (mode 0700,
   owned by the node's service user), with the old producer's mining
   configuration file (the same SHA-256) and the same four
   `QSDM_LEGACY_MINING_*` settings, pointing at the new paths.
4. **Close mining for the first start**: create an empty `KILL` file in that
   directory. The node then reconciles the records with the chain
   (`hl1: legacy-mining reconciliation clean`, with `"created":false` and
   `"tip"` equal to `N`) and seals blocks, but admits no proofs and pays
   nothing (`legacymining: KILL present: KILLED`).
5. **Open mining**: when the new producer has sealed its first blocks, remove
   `KILL` and restart it. Pending proofs from the old producer are paid in
   order from then on.

The old producer's records must never be used by two producers: after the
copy, its host stays stopped (step 8), and its records stay where they are as
evidence.

### Handing production back

Handing production back, for example from a stand-in to the host that
produced before, is one more hand-over with the same steps:

- **A new key.** A key is never used twice: the hand-back is a new epoch with a
  new key, pinned to the last block of the producer it takes over from. A
  schedule that names an earlier producer again is refused at startup.
- **A state directory that holds that producer's chain and no watermark**: a
  full node that followed it with its schedule, or a copy of its stopped chain
  files without its watermark. Seed the watermark at its last block (step 3 of
  the stand-in steps) and install the new schedule on every node.
- **The mining records come back the same way**: copied after it stops, with
  `KILL` for the first start.

A follower that keeps the previous schedule refuses the first block of the
new epoch and stops following, so every node needs the new file.

`TestStandInHandsBack` (section 7) runs a hand-over and a hand-back on real
state directories.

The steps were also rehearsed on copies of the project's full node in Japan,
the standby block maker, sealed off from the network. On 10 November 2026 a
copy took over block production. On 11 November 2026 a copy rehearsed a full
takeover with the producer's own mining records (the reconciliation was clean;
mining was closed, then reopened) and the hand-back to a new key, and the
mining audit passed: every proof was paid exactly once. On 13 November 2026
the turn followed on the live network: the producer stopped at block
1,095,601, the node made blocks 1,095,602 to 1,095,784 with its own key, and
the producer made the blocks again from block 1,095,785 with a new key. The
mining audit of the node's records passed and the chain did not fork.

### Telling the full nodes

Full nodes set up from a release get their schedule from the release's
`fullnode-core.toml` ([Run a full node](RUN_A_FULL_NODE.md)). After each
hand-over, and before the new producer's first block if it can be, the project
publishes the new schedule on https://qsdm.tech/fullnode.html, in a
"Block makers" section: the exact `[consensus.producer_transition]` text
(`<pre class="producer-schedule">`) and its digest. That page is where full
nodes already take their other trust anchors from.

`install-full-node.sh --update-schedule --from-website` puts it into a running
node: it checks that the new schedule continues the node's (the same prefix
pin, the same earlier epochs from the same heights, new epochs only at the
end), keeps the old `core.toml`, sets `authorized_block_producers` to the last
producer, restarts the node, puts the old file back if the node does not start,
and compares the node with the network. A new install with `--from-website`
takes the schedule from the page. Because the installer refuses a changed
prefix pin, the project keeps the existing pin in every published schedule
(step 4 stays optional, and is not used for the public network).

The digest is the SHA-256 of the schedule's canonical JSON:
`{"epochs":[{"checkpoint_hash":...,"from_height":...,"producer_id":...},...],
"prefix_bytes":...,"prefix_sha256":...,"version":...}` with sorted keys, no
spaces, and `checkpoint_hash` `""` for epoch 0. Two nodes with the same digest
enforce the same schedule. A version 1 table and its version 2 spelling have
different digests (the version is part of it). The release's version 1 table
(`fullnode-rc1108` to `fullnode-rc1111`) has the digest
`bd666048acae4de96b637e79852a764f516b7d7bd9b086f09bdd504d3bcf0da5`.

### The public schedule

Since the live turn of 13 November 2026 every node must carry this table.
qsdm.tech/fullnode.html shows the same text; a node with the version 1 table
of the full-node releases stops at block 1,095,601 until it has it
([Run a full node](RUN_A_FULL_NODE.md#when-the-block-maker-changes)).

```toml
[consensus.producer_transition]
version = 2
historical_prefix_bytes = 2728741562
historical_prefix_sha256 = "3c4db4879b82544ccef7e2abe0c0e2ef7741864e13307fc95978abc393f7057b"

[[consensus.producer_transition.epochs]]
from_height = 482566
producer_id = "42d05e653f004d0aa71ae9cebc7b2a5b5d0dc7bb41b219a8e99d7c29229c438b"

[[consensus.producer_transition.epochs]]
from_height = 659977
producer_id = "1cf60b16cf9aee5e8eaf67a8518a2b2ba7bc54f2dd607f05bc5a861b7a94c859"
checkpoint_hash = "16884d35cee8b805fab51f1ecad740022fdb991e0fece15394774e1c5b7cdd0b"

[[consensus.producer_transition.epochs]]
from_height = 1095602
producer_id = "2c1ff51975c523a36be9803025a02f069cc3dc1194bbc9174d557e76f30b5425"
checkpoint_hash = "1155a34f94ec5aebfdfd703a8da020a647f9831482633e496a8304e931ead50e"

[[consensus.producer_transition.epochs]]
from_height = 1095785
producer_id = "287ddb7643997cdb3651091e1fc67c360d472349e5b6d674c6b68a05fed54a4a"
checkpoint_hash = "735eb4a6b69b989a5282e30209dac323f7b0e520ff908c4edb21daec4bf7fb5e"
```

Its digest is `fd23fbcae12315a4e6d4abce24bd908d0e738d6227b6d3203c03d772238c1345`. The
producer since block 1,095,785 is the last epoch's key; the key of the epoch
from 659,977 never signs again.

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
| A state directory without `qsdm_enrollment.json`, for example one that holds only the journal, receipts and accounts | Startup refused under a transition table ("required enrollment snapshot"). A full node's state directory holds it. |
| A hand-back names an earlier producer's key again | Startup refused (a repeated producer). Use a new key. |
| A full node set up from a release keeps the release's schedule after a hand-over | It stops at block `N` ("external block producer is not authorized" for `N + 1`) and loses nothing. `install-full-node.sh --update-schedule --from-website` puts the published schedule in, and it carries on. |
| A published schedule changes an earlier epoch or the prefix pin | `install-full-node.sh` refuses it and changes nothing. |
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
- `cmd/qsdm/standin_promotion_test.go`: `TestStandInPromotesAFollower` builds a
  full node as one is built (a snapshot of the producer's journal, accounts
  and enrollment, then blocks appended as HTTP chain sync appends them) and
  makes it the producer: the producer role refuses it without a watermark, the
  seed refuses a served tip above its journal and a hash it does not hold, and
  after the seed and a version 2 schedule that names its own key it seals
  blocks on top of the old producer's last block. The old producer, restarted
  with the schedule, cannot seal; a second full node with the schedule refuses
  a block the old producer sealed under the old table and follows the new
  producer, and with the old table it does the opposite.
  `TestStandInHandsBack` goes on from there: after the stand-in has sealed,
  production moves to a node that followed it, with a new key pinned to the
  stand-in's last block. A schedule that names the first producer's key again
  is refused; the stand-in, restarted with the final schedule, cannot seal; a
  follower that kept the stand-in's schedule refuses the hand-back block; and
  a follower with the final schedule holds every block from the producer of
  its epoch, across a restart.
- `cmd/qsdm-handover/main_test.go`: the public full node's version 1 table
  becomes a three-epoch schedule in the same place with every other line
  unchanged, a version 2 table gets a fourth epoch, Windows line ends stay,
  and every invalid schedule, an inline table, a YAML file and an existing
  output file are refused without writing anything.
- Fuzz targets, whose seeds run with the other tests and which the nightly
  fuzz workflow fuzzes for five minutes each:
  - `FuzzTransitionValidate` (`pkg/producerpolicy`) builds tables with any
    version, any mix of version 1 and version 2 fields and up to 66 epochs.
    `Validate` must accept exactly the tables that an independent restatement
    of the rules in sections 1 and 2 accepts, and every accepted table must
    give a usable schedule.
  - `FuzzProducerTransitionBlockRule` (`pkg/chain`) gives the block rule
    blocks around every boundary, signed by each producer, forged, unsigned
    or changed after signing. The rule as written before version 2, version 1
    as its schedule and the same table written as version 2 must agree, error
    text included, and both a version 1 policy and a three-epoch schedule must
    accept exactly the blocks that section 3 allows.

## 8. Not yet known

- How long a follower with a large journal takes to validate its history from
  genesis at startup. Every restart with a transition table already does this,
  so check that the service manager's start timeout allows for it. One figure:
  the project's full node on a 12 GB server answered again 4 minutes 26
  seconds after a restart at block 1,058,193 (release fullnode-rc1108, 9
  November 2026). On 12 November 2026 it answered again 2 minutes 51 seconds
  after its switch to fullnode-rc1109, at block 1,087,781.
- A reviewed tool for computing a new prefix pin on a live journal does not
  exist yet; until it does, keep the existing pin (step 4 is optional).
