# BFT Signing Journal Integration

## Scope

This opt-in integration protects the local proposal, prevote, and precommit
signing path. It does not enable multi-validator consensus, restore prevote
locks or retired rounds, authorize membership transitions, or enforce quorum
certificates on external blocks. It must remain staging-only until those
separate consensus and recovery gates are complete.

The default is unchanged: `consensus.signing_journal = false`. No production
deployment or shared activation setting is changed by this implementation.

An additional [fixed-set round-recovery library path](BFT_ROUND_RECOVERY_STAGING.md)
is available for isolated tests. Node startup does not invoke it, and enabling
the signing-journal setting alone does not enable round or lock recovery.

## Startup Gate

On an existing staging chain, set the following in the node's TOML or YAML
configuration, or use `QSDM_CONSENSUS_SIGNING_JOURNAL=1`:

```toml
[consensus]
signing_journal = true
```

Startup blocks BFT signing before chain restoration. It opens the journal only
after the chain and account state have been restored, using the verified
genesis hash, local signer fingerprint, and consensus configuration fingerprint
as bindings. This first integration refuses a fresh chain with no restored
genesis. Bootstrap an isolated staging chain before opting in.

The journal resides beside the configured SQLite database, in the already
locked validator state directory:

- `qsdm_bft_signing_journal.json`: versioned intentions and signed envelopes;
- `qsdm_bft_signing_journal.json.binding`: initialization/binding marker;
- `qsdm_bft_signing_journal.json.lock`: OS-owned process lock.

An existing journal or marker prevents restarting with the setting disabled.
A marker without its journal is an error, not a fresh validator. Corruption,
wrong identity, changed genesis/configuration, or another process holding the
lock blocks signing. Do not delete these files to clear an error, revert to an
older binary that ignores them, or restore an older journal while retaining
the same signing key. An operator with filesystem access can roll back or
delete all local records; this design is not rollback-resistant hardware.
The same private key must never run in multiple state directories or hosts.

## Signing Order

The executor checks configured membership authority, durably reserves the
exact signing tuple, signs it, and persists the full authenticated envelope
before calling the publisher. A saved envelope is re-used byte-for-byte on
retry, including after process restart. A reservation interrupted before
envelope persistence permits only the same intention to be signed again.

The reservation key remains `(kind, height, round, validator)`. Changing the
block value, proposal body hash, or membership root for that key is a conflict.
Runtime compatibility votes retain their actual empty membership root through
an explicit journal binding flag. This does not create a membership snapshot.
Membership-aware executor callers must match the journal's network identity
and the effective policy before the signing key is invoked.

Storage errors and conflicting reservations latch signing off for the current
executor. A transport publication error can be retried without re-signing.
The singleton synthetic path propagates journaled broadcast errors and will
not locally commit a vote whose envelope failed persistence. Broadcast APIs
still do not apply local votes; the synthetic helper remains the local driver.
This is not the proposed multi-validator reactor or verified inbound replay.

## Persistence Limits

Schema version 2 stores complete signed envelopes and verifies their signatures
on open. Version 1's hash-only records are rejected; there is no silent migration
or deletion of historical reservations. The foundation was inactive, but any
existing staging journal must be retained for an explicitly reviewed migration.

Writes use strict temp-file, flush, and atomic replacement. They never use the
legacy Windows in-place-overwrite fallback. Unix replacements also sync the
parent directory; Windows replacements request write-through. An ambiguous
write failure requires reopening and validating disk state before signing.

The journal retains the foundation's 4 MiB and 4,096-record bounds. Full signed
envelopes usually reach the byte limit first. There is no pruning yet, so this
is a bounded staging run, not an indefinitely running production journal.
Chain-tip-aware compaction and full round/lock recovery remain required.

## Verification

The integration tests cover write-before-sign and write-before-publish order,
exact-byte retry after transport failure and restart, membership changes,
wrong/nil signer, corrupt or missing journals, lock contention, old-schema
refusal, capacity exhaustion, and failure propagation before singleton commit.

Subprocess tests terminate without cleanup after reservation, after signature
creation, and on entry to publication. They reopen with the same key, verify
the OS lock was released, and refuse a conflicting vote. These are process
crash tests, not power-loss tests or a four-host consensus rehearsal.

Before activation, independently review the storage and signing order, finish
durable round/lock recovery and chain-tip reconciliation, and run the full
multi-process/multi-host fault matrix. No public-network activation is implied
by these tests passing.
