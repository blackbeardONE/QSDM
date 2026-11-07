# Verified snapshot bootstrap

A new full node can start from a snapshot of the chain at a checkpoint height N
instead of downloading and re-executing every block from genesis. The node does
not trust the snapshot. It checks it against a checkpoint that you pin in the
node's own configuration: the hash chain back to genesis, the producer's
signature on the checkpoint block, the producer transition and the state root.
It installs nothing unless every check passes. Then the node starts normally
and catches up from block N+1.

[Run a full node](RUN_A_FULL_NODE.md) uses this to set up a full node on the
public network. This page explains how the bootstrap works, what it trusts, and
how to use it by hand.

The bootstrap is off by default. With `[snapshot_bootstrap]` unset the node
boots exactly as before, and `qsdm --snapshot-import` refuses to run.

## 1. What it adds

| Piece | Where | Default |
|---|---|---|
| Config keys `enabled`, `checkpoint_height`, `checkpoint_block_hash`, `checkpoint_state_root` and `checkpoint_producer_id` under `[snapshot_bootstrap]`; environment `QSDM_SNAPSHOT_BOOTSTRAP` and `QSDM_SNAPSHOT_CHECKPOINT_*` | `pkg/config/snapshot_bootstrap.go` | off, no change |
| Offline export: `qsdm --snapshot-export --state-dir <stopped state dir> --out <new dir>` | `cmd/qsdm/snapshot_bootstrap.go` | always available; only reads the source |
| Offline import: `qsdm --snapshot-import --from <snapshot dir>` | `cmd/qsdm/snapshot_bootstrap.go` | refused unless enabled |
| Boot guard: a restored chain that reaches `checkpoint_height` must hold the checkpoint block there, or the node exits 78 | `snapshotCheckpointGuard`, called in `cmd/qsdm/main.go` after the restore | a no-op unless enabled |
| Strict manifest reader, hash-verified staging copy, advisory checkpoint file | `pkg/ledgersnapshot/bootstrap.go` | none |
| Measurement harness | `cmd/qsdm/snapshot_bench_test.go` | skipped unless asked for |

No consensus rule, block production, block acceptance, gossip or catch-up code
changed. Both modes are offline: each takes the validator state lock, prints a
JSON report and exits 0, or exits 78 with the reason. Neither starts the node.
The import refuses to run in the producer role (`QSDM_SOLO_VALIDATOR_MODE` or
`QSDM_NETWORK_BLOCK_PRODUCER` set).

## 2. Design

### The snapshot format

The bootstrap reuses the snapshot format of `pkg/ledgersnapshot`, the one
`qsdm-ledger-snapshot` also writes: the four validator state files, copied
under the validator state lock, and a manifest with their sizes and SHA-256
hashes.

| File | Holds |
|---|---|
| `qsdm_chain.ndjson` | the block journal, blocks 0 to N |
| `qsdm_accounts.json` | the accounts at N |
| `qsdm_enrollment.json` | the enrollment registry at N |
| `qsdm_staking.json` | the local staking ledger (it must be empty, see section 3) |
| `ledger-snapshot.json` | the manifest: schema 1, the four names above in this order, their sizes and SHA-256 |

An export adds `snapshot-checkpoint.json`: the height, block hash, state root
and producer of the tip, whether the tip is signed, the number of blocks and the
journal size. This file is advisory. An import never takes its trust anchor
from it; it only refuses a snapshot whose checkpoint file names another
checkpoint than the one you pinned. Other files in the snapshot directory, such
as checksum lists added for distribution, are ignored.

### Why the snapshot keeps the whole journal

The snapshot holds the whole journal from genesis, not only the state at N. A
journal that starts at N would need consensus-adjacent changes that this
feature does not make:

- The producer transition is validated over the chain from genesis, and its
  pinned history prefix must be present before a node starts at all. A node
  with a producer transition configured and an empty state directory refuses to
  start. That is why a new node on such a network starts from a snapshot, and
  why the import runs before that check.
- Task, CELL stream and wallet-recovery state is not persisted. Every start
  rebuilds it from all blocks.
- The restore and receipt paths assume the full journal.

What a snapshot saves is the genesis sync. Without one, a new node fetches
every block and fully re-executes it through `TryAppendExternalBlock` (hash,
signature, transactions, state root). A verified snapshot replaces that with a
copy plus the checks below. It does not shrink the journal that every start
loads, so it saves time but not memory (section 6).

## 3. Trust model

### What you trust

- **The checkpoint you pin**: the height N, the hash and state root of block N
  and, optionally, its producer. The import accepts only a snapshot whose tip
  is exactly this block, so the pinned block hash is the trust anchor for all
  of the history below it. Take it from a source that is independent of the
  snapshot download, and confirm it with a second one. For the public network,
  [Run a full node](RUN_A_FULL_NODE.md) step 3 compares the values on qsdm.tech,
  the public API and the node configuration.
- **Your node's consensus configuration.** The `[consensus]` settings decide
  which producer may sign block N (`producer_transition`,
  `authorized_block_producers`) and how block hashes are computed (the
  activation heights). Use the network's settings. For the public network they
  come with the full-node release (`fullnode-core.toml`).
- **The `qsdm` binary** you run. Verify the release.

The snapshot itself can come from anyone, over any transport.

### What the import checks

`qsdm --snapshot-import` holds the state lock and refuses (exit 78, nothing
installed) unless every check below passes:

1. `[snapshot_bootstrap] enabled = true` with a complete, well-formed
   checkpoint, and the node is in the follower role.
2. The state directory holds no journal, accounts, enrollment, staking,
   receipts (`qsdm_receipts.ndjson` or `qsdm_receipts.json`), governance
   (`qsdm_governance.json`) or earlier import record. Neither the snapshot
   directory nor the state directory may contain the other.
3. Staging: the snapshot is copied into a new private directory inside the
   state directory (`.snapshot-import-<time>`), and every byte written is
   hashed. Every file must have the manifest's size and SHA-256. The manifest
   itself must be strict: schema 1, the four names in order, the
   stopped-validator flag, no unknown field and nothing after it. All later
   checks read the staged copy, so the source cannot change after it was
   checked. If the snapshot has a `snapshot-checkpoint.json`, it must name the
   pinned checkpoint.
4. The journal ends with a newline, loads, holds no forked duplicate block and
   starts at genesis (height 0, empty `prev_hash`). Every block's hash is
   recomputed (`chain.ComputeBlockHash`) and must link to its parent, so the
   pinned tip hash commits to the whole history.
5. The tip is exactly the checkpoint: height, block hash and state root.
6. The tip is signed by an authorized producer. It must carry
   `producer_auth`, and `chain.VerifyBlockSignature` must pass: ML-DSA-87, with
   a public key that derives the block's `producer_id`, the same check that
   external blocks get. Then:
   - if `checkpoint_producer_id` is set, the producer must be that address;
   - if a producer transition is configured, the producer must also be the
     one the transition names at N: the producer of the epoch that contains N
     (for a version 1 table, the replacement producer from `effective_height`
     on and the historical producer below it), or the first epoch's producer
     for a signed block below the first epoch (see
     [Producer transition](PRODUCER_TRANSITION.md));
   - without a transition, the producer must be listed in
     `authorized_block_producers` or be the pinned `checkpoint_producer_id`.

   If nothing names the producer, the import refuses. An unsigned tip is always
   refused, so N must be a block that the producer signed.
7. With a producer transition configured, the staged journal starts with the
   pinned history prefix (`ValidateJournalPrefix`) and the whole chain passes
   the transition rules (`ValidateProducerTransitionChain`'s checks, run on
   every block once as the journal is read: `chain.CheckJournal`).
8. The unmodified boot restore (`hl1ReplayStack.restore`, the code that a
   normal start and the replay mode use) must reproduce the tip's state root
   from the accounts and enrollment files and the task, stream and recovery
   state it replays from the journal. Once the enrollment root is active at N,
   the enrollment registry is part of that check; with a producer transition
   that is from the first hand-over on (version 1: `effective_height`). The
   report then says
   `"enrollment_root_checked": true`.
9. Nothing that the state root does not commit is imported:
   - The journal must hold no governance parameter transaction
     (`qsdm/gov/v1`). The parameter store is a local file that the state root
     does not commit and that is not rebuilt from blocks.
   - The staking ledger must be empty: no bond and no unbonding entry. It is
     local, the state root does not commit it, and a maturing unbonding entry
     credits balances. It is not installed. It must also be a file that the
     node's own staking decoder reads as an empty ledger. For a file that
     decoder refuses (data after the ledger, another version, a malformed
     entry) the node falls back to a backup file, which a snapshot does not
     carry, so the source's staking state would be unknown.
   - Receipts are not imported. The new node has no receipt history below N.
     Receipts are API history, not consensus state.

Only then are `qsdm_enrollment.json`, `qsdm_accounts.json` and
`qsdm_chain.ndjson` renamed into place, the journal last. The directory is
fsynced, and `qsdm_snapshot_import.json` records the import. The next normal
start runs the ordinary restore and catches up from N+1 through the normal
block path.

### The boot guard

While `[snapshot_bootstrap]` is enabled, every start checks the restored chain.
If the chain reaches `checkpoint_height`, the block there must have the pinned
hash and state root, or the node refuses to start: exit 78 with
`snapshot bootstrap checkpoint: restored block <N> (...) is not the trusted
checkpoint (...)`. A chain below the checkpoint height passes, because it is
still syncing. Keep the bootstrap enabled after the import so that the pin
stays enforced.

## 4. Configuration

The keys go under `[snapshot_bootstrap]` (TOML) or `snapshot_bootstrap:`
(YAML). An environment variable that is set to a non-blank value replaces the
file value.

| Key | Env | Default | Meaning |
|---|---|---|---|
| `enabled` | `QSDM_SNAPSHOT_BOOTSTRAP` (`1`, `true`, `yes` or `on` mean true; any other value means false) | `false` | allows `--snapshot-import` and switches the boot guard on |
| `checkpoint_height` | `QSDM_SNAPSHOT_CHECKPOINT_HEIGHT` | `0` (not set) | the checkpoint height N; at least 1 when enabled |
| `checkpoint_block_hash` | `QSDM_SNAPSHOT_CHECKPOINT_BLOCK_HASH` | empty | the hash of block N: 64 lower-case hexadecimal characters |
| `checkpoint_state_root` | `QSDM_SNAPSHOT_CHECKPOINT_STATE_ROOT` | empty | the state root of block N: 64 lower-case hexadecimal characters |
| `checkpoint_producer_id` | `QSDM_SNAPSHOT_CHECKPOINT_PRODUCER_ID` | empty (optional) | the ML-DSA-87 producer address that must have signed block N: 64 lower-case hexadecimal characters |

With placeholders for the values:

```toml
[snapshot_bootstrap]
enabled = true
checkpoint_height = <N>
checkpoint_block_hash = "<64 lower-case hex>"
checkpoint_state_root = "<64 lower-case hex>"
checkpoint_producer_id = "<64 lower-case hex>"   # recommended
```

The node refuses to load a configuration (`Failed to load configuration:
invalid configuration: [snapshot_bootstrap] ...`, before any mode runs) when:

- a checkpoint key is set while `enabled` is false, so that nobody believes a
  checkpoint is enforced when it is not;
- `enabled` is true and `checkpoint_height` is 0, or the block hash or the
  state root is not 64 lower-case hexadecimal characters;
- `checkpoint_producer_id` is set and is not 64 lower-case hexadecimal
  characters. A libp2p peer ID (`12D3KooW...`) is refused: block signatures
  name the ML-DSA-87 address;
- `QSDM_SNAPSHOT_CHECKPOINT_HEIGHT` is set and is not a whole number.

`authorized_block_producers` authorizes the checkpoint block only if it lists
the block's `producer_id`. For a signed block that is the producer's ML-DSA-87
address, so a list of libp2p peer IDs never matches. Without a producer
transition, set `checkpoint_producer_id`.

## 5. Using it

### Make a snapshot

1. Stop a synced node, or copy its stopped state directory. The export takes
   the directory's state lock (`qsdm-validator.state.lock`) and refuses a
   directory whose node is still running.
2. Run the export with that node's configuration. It verifies the snapshot
   under the consensus settings it finds there.

   ```bash
   CONFIG_FILE=<the node's config> qsdm --snapshot-export --state-dir <stopped state dir> --out <new dir>
   ```

   The output directory must not exist yet and must be outside the state
   directory. The source is only read. The export refuses a copy whose accounts
   do not reproduce the journal tip, as after a crash between a journal append
   and the accounts save; a normal start of that node reconciles it first. On
   success it prints a JSON report (`out_dir`, `manifest`, `checkpoint`,
   `journal_load_seconds`, `restore_seconds`, `enrollment_root_checked`) and
   writes `snapshot-checkpoint.json`. An import needs a signed tip, so check
   that `checkpoint.tip_signed` is `true`.
3. Publish the checkpoint (height, block hash, state root and producer)
   through a channel separate from the snapshot bytes, and ship the snapshot
   directory, for example as a tar archive. Its integrity comes from the
   pinned checkpoint and the manifest, not from the transport.

A directory written by `qsdm-ledger-snapshot` has the same format without
`snapshot-checkpoint.json`. The import accepts it too, since it verifies
everything itself.

### Import it on a new node

1. Write the node's configuration: the network's consensus settings and the
   `[snapshot_bootstrap]` table with the checkpoint. Keep the node in the
   follower role (neither `QSDM_SOLO_VALIDATOR_MODE` nor
   `QSDM_NETWORK_BLOCK_PRODUCER` set). The state directory is the directory of
   `[storage] sqlite_path` (env `SQLITE_PATH`; the default `qsdm.db` means the
   working directory). It must hold no chain files.
2. With the node stopped, run:

   ```bash
   CONFIG_FILE=<config> qsdm --snapshot-import --from <snapshot dir>
   ```

   The snapshot directory must be outside the state directory, and the state
   directory needs room for a staged copy of the snapshot.

   - **Exit 0** prints the report, which is also kept in the state directory
     as `qsdm_snapshot_import.json`: `state_dir`; `checkpoint` (`height`,
     `block_hash`, `state_root`, `producer_id`, `tip_signed`, `blocks`,
     `journal_bytes`); `authorized_by` (`producer_transition`,
     `authorized_block_producers` or `checkpoint_producer_id`: what authorized
     the producer); `enrollment_root_checked`; `installed` (the name, size and
     SHA-256 of each installed file); `imported_at`; and timings.
   - **Exit 78** prints `FATAL (exit 78): snapshot bootstrap: <reason>` on
     standard error. Nothing is installed and the staged copy is removed.
3. Start the node normally. It restores the state at N, then catches up from
   N+1. Keep `[snapshot_bootstrap]` enabled so that the boot guard keeps
   checking the pin.

On the public network, [Run a full node](RUN_A_FULL_NODE.md) and
`QSDM/scripts/fullnode/install-full-node.sh` carry out these steps with the
published release, snapshot and checkpoint, and check the import record
afterwards.

### If an install is interrupted

The three files are renamed into place one at a time, the journal last. If the
import is interrupted between two renames, the state directory is no longer
empty, and the next import refuses to run. Remove the files that were installed
(`qsdm_enrollment.json`, `qsdm_accounts.json`, `qsdm_chain.ndjson`) and any
leftover `.snapshot-import-*` directory, then retry. Nothing repairs this
automatically.

## 6. Measurements

These figures come from the harness in `cmd/qsdm/snapshot_bench_test.go` (its
header shows how to run it). It builds synthetic chains of plain transfers:
each block holds one transfer from each of the "tx/blk" senders to rotating
recipients among "accounts" accounts. Every block is ML-DSA-87 signed, so a
block is about 10.8 KB, of which about 9.6 KB is `producer_auth`. Each phase
ran in its own process on Linux x86-64 (a 16-thread laptop CPU, a virtual
machine with 15 GB of RAM). "RSS" is that process's peak resident memory.

- **replay** is the path of a new node without a snapshot: append every block
  from genesis through `TryAppendExternalBlock` (hash, signature, full
  re-execution, state root). "persist" writes and fsyncs the journal, accounts,
  enrollment and receipts after every block, as a running node does; "mem"
  keeps everything in memory.
- **export** is `qsdm --snapshot-export`.
- **import** is `qsdm --snapshot-import`: the staged copy, every check in
  section 3, and the install.
- **boot** is the first start after the import (the normal restore).

| blocks | tx/blk | accounts | journal | replay mem | replay persist | replay RSS | export | import | import RSS | boot | boot RSS |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 5 000 | 4 | 20 000 | 54 MB | 83.5 s | 133.1 s | 181 MB | 0.55 s | 0.55 s | 114 MB | 0.50 s | 115 MB |
| 20 000 | 4 | 2 000 | 216 MB | 64.7 s | 112.1 s | 495 MB | 2.08 s | 2.09 s | 310 MB | 1.63 s | 264 MB |
| 20 000 | 16 | 2 000 | 260 MB | 122.2 s | 170.6 s | 899 MB | 3.08 s | 3.32 s | 510 MB | 2.66 s | 375 MB |
| 80 000 | 4 | 2 000 | 866 MB | 291.8 s | 507.3 s | 1 835 MB | 9.01 s | 9.35 s | 1 125 MB | 7.11 s | 944 MB |

What the numbers show:

- **Import and boot scale with the journal's size**: about 10.8 s per GB for
  the import and 8.2 s per GB for a start, with a peak RSS of about 1.3 times
  the journal size for the import and 1.1 times for a start. Decoding the
  journal's JSON is about 75% of the time; the state check itself took 0.44 s
  at 80 000 blocks.
- **A replay from genesis grows faster than the chain.**
  `TryAppendExternalBlock` scans the in-memory chain for a duplicate height on
  every append, so the cost per block rises with the chain's length. With
  persistence on, the mean append over the last tenth of the chain took 5.7 ms
  at 20 000 blocks and 7.3 ms at 80 000. Fitted to the two rows with 4
  transactions per block and 2 000 accounts, a replay of n blocks takes about
  5.35e-3·n + 1.23e-8·n² seconds with persistence on (3.1e-3·n + 6.9e-9·n² in
  memory): about 1.6 hours at 500 000 blocks, before any network transfer.
- **The replay cost per block grows with the number of accounts**, because
  each append clones the state and hashes every account for the state root.
  With 20 000 accounts the mean append over the last tenth took 33 ms in memory
  and 52 ms with persistence, against 3.2 ms and 5.7 ms with 2 000 accounts.
- **Replay RSS is about 2.1 times the journal size**: it holds the whole chain
  and the in-memory receipt store.

These synthetic ratios understate a real chain, whose journal carries other
transaction types and whose start also replays task, stream and recovery
state. They were also measured before blocks shared their repeated values in
memory (`pkg/chain/block_share.go`): every block then held its own copy of the
producer's 2,592-byte public key.

`cmd/qsdm/restore_bench_test.go` measures the restore every start runs, and
the import's checks, on an existing state directory (its header shows how).
On a copy of the public chain at height 880 376 (880 377 blocks, a 5.0 GB
journal), on Windows x86-64 with a 16-thread laptop CPU, each took about 2.5
minutes with fullnode-rc1019, where a start peaked at 8.2 GiB and the import at
8.3 GiB. fullnode-rc1107 (7 November 2026) shares the values repeated across
blocks, and both a start and the import read the journal
once, verify each block once on every core and keep every verified signature
but the tip's only in the journal.
[Run a full node](RUN_A_FULL_NODE.md#memory) has the figures and how they grow
with the chain. A snapshot saves the genesis replay, not memory: every restart
costs the "boot" time and memory, whether or not the node came from a
snapshot.

## 7. Tests

From `QSDM/source`, with the same settings as CI:

```
CGO_ENABLED=1 go test -count=1 -tags dilithium_circl ./pkg/ledgersnapshot/ ./pkg/config/ ./cmd/qsdm/
```

- `cmd/qsdm/snapshot_bootstrap_test.go`:
  - `TestSnapshotBootstrapExportImportEndToEnd`: a signed synthetic chain is
    built with main()'s wiring and persistence hook. The export leaves the
    source files unchanged, the import installs the exact source bytes, and
    the imported node's restore gives the source's state root and byte-identical
    accounts. The boot guard passes. The producer then seals three more blocks,
    and the imported node appends them through `TryAppendExternalBlock` with
    equal state roots.
  - `TestSnapshotImportRefusals`: import disabled; the producer role; a wrong
    checkpoint height, hash or state root; a wrong pinned producer; no producer
    identity; a producer that is not in the allowlist; a file that differs from
    the manifest; tampered accounts with a rehashed manifest; a forged tip
    signature; a tampered history block; a dropped history block; a journal
    without a final newline; a staking ledger that is not empty; an advisory
    checkpoint that disagrees; a state directory that is not empty; a snapshot
    inside the state directory; and a second import. Each refusal leaves no
    chain state behind. It also checks that the allowlist path accepts the
    producer.
  - `TestSnapshotImportUnderProducerTransition`: with a version 1 producer
    transition, the replacement producer is authorized from `effective_height`
    on and the historical producer below it, and a transition that names
    another producer is refused. With a version 2 schedule after a second
    hand-over, the final epoch's producer is authorized, and the version 1
    table refuses the same snapshot.
  - `TestSnapshotVerifyAppliesConsensusSettingsFirst`: a chain sealed with the
    content root active from block 2 exports and imports only when the
    consensus settings apply before the hash checks (see section 8).
  - `TestSnapshotImportRefusesUnsignedCheckpoint`,
    `TestSnapshotExportRefusesUnreconciledState`,
    `TestSnapshotVerifyRefusesGovernanceHistory`, `TestSnapshotCheckpointGuard`,
    `TestSnapshotModeRequested` and `TestSnapshotDispatchPlacement` (the
    dispatch comes after the config load and the replay-mode dispatch, and
    before the transition preflight and the state lock; the boot guard comes
    after `RestoreChain` and before the served watermark and block
    propagation).
- `pkg/ledgersnapshot/bootstrap_test.go`: the staged copy has the exact bytes,
  a mismatch is refused and the staging directory removed, the manifest reader
  is strict, and the checkpoint file round-trips and is never overwritten.
- `pkg/config` `TestSnapshotBootstrap*`: validation, environment overrides,
  TOML and YAML loading and the `Config.Validate` wiring. `live_shape_test.go`
  checks that a producer-shaped config leaves the bootstrap off.
- `pkg/chain` `TestStakingLedgerEntries`: the staking decoder that the import
  shares with the node refuses exactly the files for which the node's loader
  falls back to its backup.

Fuzz targets cover each parser that the bootstrap adds for untrusted or
operator input (the journal is read by the node's existing block decoder).
Their seeds run with the other tests, and the nightly fuzz workflow fuzzes
each for five minutes:

- `FuzzDecodeManifest` (`pkg/ledgersnapshot`): any manifest bytes. An accepted
  manifest names exactly the four validator files in order, with sizes that
  are not negative and lower-case hex digests, reads the same with the lenient
  JSON decoder, and is accepted again unchanged when written back.
- `FuzzDecodeCheckpoint` (`pkg/ledgersnapshot`): any checkpoint file. An
  accepted checkpoint reads back unchanged when written back.
- `FuzzSnapshotBootstrapConfig` (`pkg/config`): any file values and any
  environment overrides. The result is the trimmed file value or the
  non-blank variable, a malformed height variable is refused, and `Validate`
  accepts exactly the configurations the import and the boot guard can
  enforce (checked against an independent statement of the rules).
- `FuzzSnapshotArgs` (`cmd/qsdm`): any command line. Every refusal is the usage
  error; an accepted one is dispatched by `main()`, names one mode and has
  exactly the paths that mode needs, each given on the command line.
- `FuzzSnapshotStakingLedger` (`cmd/qsdm`): any staking ledger. A file the
  check accepts loads with the node's own loader from its own bytes (not from
  the loader's backup) and holds no bond and no unbonding entry.

## 8. Remaining risks and limits

- **The pinned checkpoint is the trust anchor.** A wrong but self-consistent
  checkpoint from a malicious source would install a fork. Publish checkpoints
  signed or through two independent channels, and confirm one with the public
  API before you pin it.
- **Memory is not reduced.** Every start holds the whole journal and replays
  task, stream and recovery state over it. Cutting the journal at N would need
  persisted task, stream and recovery state, transition validation from a
  checkpoint instead of genesis, and height-indexed chain access: a
  consensus-adjacent change that needs its own review.
- **Governance history.** A chain that holds a governance parameter
  transaction cannot be imported: every snapshot of it is refused. Supporting
  it would need the parameter store to be rebuilt from blocks at every start,
  or committed in the state root, which is a consensus change.
- **The checkpoint must be a signed block.** Older blocks may carry no
  `producer_auth` (see `pkg/chain/block_external_authz.go`). Pick N at a recent
  block signed by the current producer.
- **Verify under the network's consensus settings.** The export and the import
  apply the node's consensus settings, as a normal start does, before they
  recompute any block hash: the content-root activation height
  (`tx_content_root_activation_height`) changes block hashes from that height
  on. `TestSnapshotVerifyAppliesConsensusSettingsFirst` guards that order. A
  node configured with other activation heights than the network's refuses a
  real snapshot with `journal block <height> hash does not match its contents`.
- **An interrupted install needs manual cleanup** (section 5).
- **The measurements are synthetic.** They use plain transfers. A genesis
  replay of a real chain was not measured.
