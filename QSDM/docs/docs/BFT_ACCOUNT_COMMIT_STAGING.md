# BFT Account Commit Reconciliation Staging

## Scope

`BFTExecutor.ConfigureAccountCommitRecovery` adds a bounded, account-only commit
transaction to the fixed-set [round recovery](BFT_ROUND_RECOVERY_STAGING.md)
library. This is not node activation, a `BlockProducer` hook, or a production
replacement for the node's persistence callbacks. It uses the existing chain
NDJSON and account JSON formats in exclusively owned staging directories.

The path supports signed blocks containing plain `AccountStore` transfers only.
Dust accounting must remain disabled. Payloads and contract calls are rejected.
Enrollment, governance promotion, staking, task/stream/capsule state, receipts,
and other post-seal effects are not included. Do not point this API at a live
node's state directory or run the legacy `ChainJournal`/account writers beside
it. The library owns stable chain/account locks and its own open chain handle;
legacy writers are not participants in this lock protocol.

## Initialization and Driving

1. Create a fresh consensus instance and executor with a fixed validator set.
2. Configure its exclusive signing journal.
3. Restore and verify the canonical chain prefix. Pass that quiescent view and
   the existing chain/account paths to `ConfigureAccountCommitRecovery` instead
   of `ConfigureRoundRecovery`. The library validates account recovery itself.
4. Read `AccountCommitSnapshot` for detached chain and account copies. Propose
   only the next height, then call `PrepareAccountCommit` with the exact signed
   block before signing a concrete precommit or applying the commit quorum.
5. Drive authenticated, round-bound votes through the trusted consensus API.
   `ApplyInbound` remains disabled. A successful quorum-completing `PreCommit`
   returns only after the block, accounts, and completion checkpoint are durable.

The caller must authenticate/admit transactions and blocks before preparation.
The producer signature and account transition are checked again locally, but
the API is not an ingress validator. BFT still votes on state-root values, not
the complete block hash. The exact block recorded here is the locally admitted
body; these files do not prove that a network quorum agreed on that entire body.

Only legacy float `AccountStore` state is supported, with finite non-negative
balances, no dust fields, and sorted unique accounts. Fixed local snapshots
prevent caller mutations from changing the prepared block or applied accounts.
Changing the accounting fork configuration blocks this mode.

## Commit Ordering

Before the quorum-completing vote becomes visible, the engine writes:

1. `<signing-journal>.rounds.commit`: exact block, round, current chain byte
   offset, and complete before/after account snapshots.
2. The existing round guard, including its committed state-root value. This is
   the local decision point; the pending block alone is not a commit decision.
3. The exact block line to the chain journal, followed by `fsync`.
4. The account snapshot via the strict atomic writer, with no in-place fallback.
5. A cleared commit record containing the completed canonical checkpoint and
   an exact account-snapshot digest.

Only then does the in-memory committed round and account snapshot become visible.
Any ambiguous write error blocks the entire instance until it is reopened from
disk. Timeouts and signing cannot run through the commit persistence window.
Closing the signing journal releases commit-file ownership as well.

The commit record is versioned, integrity checked, and bound to the signing key,
chain, network, fixed consensus rules, and absolute chain/account paths. A
`.rounds.commit.binding` marker detects a missing initialized commit file.
The round file is materialized before the commit companion is created.
Existing commit files require this reconciliation API; reopening only ordinary
round recovery or signing-only mode cannot bypass them in this version.

## Restart Cases

- Pending record without a committed round guard: discard only when chain bytes
  and accounts are still exactly the recorded pre-commit state. Restore the
  retired-round floor and lock, not the lost votes.
- Committed pending record: complete the exact block and install its exact
  after snapshot. An already installed after snapshot is not replayed.
- Interrupted chain append: complete only an exact byte prefix of the pending
  block line following the verified chain prefix. Unrelated bytes, altered
  prefix blocks, extra blocks, truncation before the recorded offset, and forks
  are refused without rewriting canonical data.
- Completed checkpoint: verify the exact canonical tip and account digest.
  The additional digest matters because legacy state roots round balances and
  can miss small balance changes.
- Locally committed guard without its block or a valid matching pending record:
  remain blocked. No block or certificate is fabricated.

For a torn final line, `LoadChainNDJSON` may return a parsed prefix plus an error.
The caller must verify that prefix before supplying it here. Reconciliation
does not generically ignore parse errors; only the recorded pending append can
be completed. Retain all files together. Deleting or rolling back the entire
state set, cloning the signing key, and noncooperating writers are outside the
protection model. Older binaries must not open these experimental directories.

## Limits and Verification

The commit record is bounded to 16 MiB, each account snapshot to 4 MiB, and each
block line to less than 4 MiB. Existing round/signing journal capacity limits
still apply. This staging path retains the full chain in memory, validates its
prefix when finishing a pending commit, and writes full account snapshots; it
is a correctness path, not a scalable storage design.

Tests cover normal consecutive commits, exact-once account recovery, process
exit at each of the five ordering boundaries, ambiguous replacement errors,
known partial append completion, conflicting tail/prefix/accounts, precision
loss in the legacy root, missing files, changed paths, ownership contention,
unsupported blocks, snapshot isolation, and downgrade refusal. These are
process-crash tests, not power-loss or multi-host finality tests.

Before node integration, define a complete transaction over all composite state
and post-seal effects, integrate authenticated round-bound ingress, and test
multi-validator crashes and partitions. Keep production activation off.
