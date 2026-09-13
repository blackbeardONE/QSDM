# BFT Round Recovery Staging

## Scope

`BFTExecutor.ConfigureRoundRecovery` is an opt-in consensus-library path for a
fixed validator set. It is not called by node startup and has no activation
flag. Existing node behavior without round-recovery files and production
settings are unchanged. Existing round guards or their marker automatically
block consensus and signing until recovery is configured. Node startup cannot
yet resume this experimental state, so keep it in isolated staging directories.

This extends the [signing journal](BFT_SIGNING_JOURNAL_STAGING.md) with durable
retirement floors, local prevote-lock guards, and local-commit reconciliation.
It does not provide a multi-validator reactor, authenticated inbound replay,
membership transitions, journal compaction, or hardware rollback protection.

## Initialization Contract

Library callers must use a new executor and consensus instance, configure its
exclusive signing journal, restore and verify the canonical chain and account
state, then call `ConfigureRoundRecovery` before starting consensus or signing.
The `BFTRecoveryChain` view must be quiescent during configuration. Its block
lookups are trusted only after the caller's normal chain/state verification;
canonical hashes alone do not prove state validity or finality.

`ApplyInbound` refuses all gossip in recovery mode. The separate inbound
round-binding work (PR #129), authenticated ingress/replay, and their recovery
tests must be integrated before this gate is removed. Direct consensus APIs
remain trusted-library interfaces: callers must authenticate and round-bind
votes before applying them. Fixed-set unit tests are not network validation.

Configuration requires the signing key to be an active validator and rejects
membership-policy mode. The active addresses and voting weights are copied
into an immutable local snapshot, with the actual consensus rules fingerprinted
on disk. Mutating the caller's validator set does not change this instance's
voters. A restart with different voters, weights, or rules fails closed.

Files beside the signing journal are:

- `<signing-journal>.rounds`: versioned guards, chain checkpoint, and bindings;
- `<signing-journal>.rounds.binding`: initialization marker;
- `<signing-journal>.rounds.lock`: operating-system file-ownership lock.

A missing initialized file is an error. An existing signing history with no
round-recovery file is also refused; there is no implicit migration from the
signing-only implementation. Signing records must be covered by the stored
round guards. Unknown fields, invalid bounds, wrong bindings, and integrity
errors are rejected. Deleting or rolling back all files is not detectable;
operators must retain the complete state and must not duplicate the key.
Older binaries do not know about this gate and must not be used with these files.

## Transition Ordering

Before making a proposal visible, consensus durably records its crash floor as
`uint64(round) + 1`. A restart abandons the interrupted round, even if its
deadline had not elapsed. This intentionally sacrifices same-round liveness
instead of reconstructing the round with an empty vote slate. Exhausting the
32-bit wire round range retains the floor `4294967296`; it never wraps to zero.

Prevote and precommit candidates are copied, persisted, then applied under the
consensus mutex. Timeout/failure records are saved before retirement becomes
visible. Storage errors latch the instance off. The timeout API still returns
only heights, so its driver must inspect `RoundRecoveryError`; outbound signing
and later consensus transitions also refuse while the error is latched.

Outbound signing holds the consensus mutex through its state check, reservation,
signature creation, and envelope persistence. A timeout cannot invalidate that
check midway through signing. Publisher callbacks run after the lock is released.
Signer implementations must not re-enter consensus or executor configuration.
Recovered retired-round envelopes are not rebroadcast, even when the separate
signing journal retains their exact bytes.

## Recovery Semantics

Recovery restores only retirement floors and lock guards above the verified
tip. It does not restore active rounds, votes, or commit certificates. A new
round carries its previous lock but starts in proposed status with empty vote
sets. A local non-nil prevote cannot contradict a concrete carried lock; a
precommit requires a fresh prevote quorum. An explicit nil polka is retained
as a nil guard, allowing subsequent prevotes for a different concrete value.
The existing lock-transition rules otherwise remain unchanged.

A recovered lock alone cannot produce a portable prevote proof. A restored
chain height cannot be proposed again, but is not inserted into the in-memory
committed-round cache. `IsCommitted` and certificate builders therefore do not
pretend that local guard records supply authenticated quorum evidence.

On restart, the stored chain checkpoint must exist in the restored chain with
the same canonical hash, and the restored tip cannot be behind it. Each locally
recorded consensus commit must also be present with the same state-root vote
value. A commit ahead of the restored tip, or a conflicting value at its height,
blocks recovery. This includes a crash after local consensus commit but before
the block was sealed. No block or certificate is fabricated to bridge that gap.
Integration with the chain commit journal is still required to resolve it.

The separate [account-only commit reconciliation path](BFT_ACCOUNT_COMMIT_STAGING.md)
can resolve this window for isolated plain-transfer staging chains. It requires
`ConfigureAccountCommitRecovery` instead of this configuration API and is not
compatible with the node's composite account/enrollment/governance callbacks.

## Limits and Verification

The journal is bounded at 4 MiB and 4,096 height guards. It retains old guards;
later-height commits never prune an older height's floor or lock. Capacity or
ambiguous strict-write errors stop the instance and require reopening and disk
reconciliation. Writes have no in-place fallback. Closing the signing journal
also closes round recovery and blocks further consensus activity.
Strict Windows writes retry access-denied replacement errors for a bounded
period; persistent denial still fails closed without truncating the old file.

Tests cover active-round abandonment, timeout/failure restart, lock and nil-polka
retention, exhaustion, exact signing order, fresh-quorum requirements, snapshot
isolation, binding/checkpoint failures, incomplete file pairs, storage failure
latching, and concurrent signing/timeouts. Subprocesses exit without cleanup
after proposal acceptance, lock formation, retirement, and local commit.
Additional crash points stop immediately after durable lock/commit replacement,
before the in-memory transition. An injected post-write error verifies that
recovery uses disk state rather than overwriting it from stale memory.
These are process-crash tests, not power-loss or multi-host consensus tests.

Before node activation: independently review the ordering and storage model,
integrate recovery before network/timer startup, reconcile the local-commit
window with the chain journal, add authenticated replay and membership recovery,
design safe compaction, and run the multi-process/multi-host fault matrix.
