# Consensus Durability Staging Design

## Status

This document is a design and rollout gate. It does not make QSDM a
multi-validator BFT chain, enable a local BFT state file, or change block
acceptance. It defines the minimum crash-safety boundary that must exist before
production BFT can be considered.

## Current Boundary

QSDM already has useful consensus foundations:

- ML-DSA-87 validator signing identities;
- signed proposal, prevote, and precommit envelopes;
- membership snapshots that bind a validator address, signer public key, and
  libp2p peer ID;
- StrictSign GossipSub ingress with peer-origin checks; and
- staging coverage for restart-and-replay and four-validator signed gossip.

The live `BFTConsensus` engine is still in-memory. Its active rounds, vote
sets, committed-round cache, timeout floor, and carried prevote lock disappear
on restart. The current local pre-seal helper is deliberately restricted to a
single validator and must not be mistaken for multi-validator finality.

Therefore, a node restart can currently lose the information that prevents its
local signer from issuing a conflicting vote. Recreating an empty BFT engine
after restart is not acceptable once a validator originates real votes.

## Safety Invariant

For one validator key, QSDM must never sign two different values for the same
tuple:

1. network ID;
2. chain identity and height;
3. consensus round;
4. vote kind; and
5. committed membership root.

The invariant applies to proposals, prevotes, and precommits. A restart,
partial disk write, corrupted state file, local clock change, or configuration
change must not weaken it.

## Required Durable State

The durable record is not a generic serialization of Go maps. It is a
versioned, canonical record with a bounded active-height window and all of the
following bindings:

- schema version and QSDM network ID;
- last durable chain tip height and block hash;
- active membership root and membership schedule fingerprint;
- local signer address and ML-DSA public-key fingerprint;
- consensus configuration fingerprint, including quorum fraction and round
  limits;
- active round data, including the canonical signed inbound envelopes needed to
  rebuild it;
- retired-round floors and carried prevote locks for unresolved heights; and
- a local signing journal mapping every signed tuple to its block hash and the
  canonical signed envelope.

Committed blocks are authoritative in the persisted chain, not in a separate
BFT cache. On restore, the durable BFT record must be reconciled with the
chain tip. Entries at or below the committed chain tip are removed only after
the corresponding durable chain record is verified.

## Write-Ahead Signing Journal

The signing journal is the critical part of this design.

Before a validator creates or publishes a proposal, prevote, or precommit, it
must atomically record the exact tuple and intended block hash. It then follows
one of two safe outcomes:

- the same tuple and hash may be signed or re-published after a restart; or
- a different hash for that tuple is refused without exception.

A crash after writing the intent but before network publication may cost
liveness for that one vote. That is acceptable. A crash that lets a validator
sign a conflicting vote is not.

The journal must be written before the signature is exposed to the network,
and journal persistence failure must stop local vote origination. It must never
fall back to an empty in-memory signer state.

## Restore Rules

Restore is allowed only when all binding values match the local, replayed
chain. A mismatch in network, chain tip, membership root, signer identity, or
consensus configuration is a fail-closed condition for validator signing.

The restore path must:

1. verify the durable file size, version, checksum, and canonical encoding;
2. verify every saved inbound envelope through the normal signature and
   membership checks;
3. rebuild unresolved rounds by replaying verified envelopes through the same
   executor used for live gossip;
4. restore only monotonic round floors and locks that are consistent with that
   replay; and
5. quarantine corrupted or mismatched state rather than silently replacing it.

The node may run in observer or catch-up mode after a durable-state failure,
but it must not originate consensus messages until an operator completes an
explicit recovery procedure.

Persisted deadlines are not trusted as wall-clock authority after restart.
Restore must use a bounded restart grace period, then progress according to
the normal round-timeout logic. This prevents stale timestamps from causing an
instant timeout storm while avoiding indefinite retention of a stalled round.

## File and Atomicity Rules

The state file contains no private key, but its integrity is safety-critical.
Use the project atomic-write helper, restrictive file permissions, a
write-to-temp plus flush plus replace sequence, and a parent-directory sync
where the platform supports it. Keep at most one validated recovery copy.

The file format must reject unknown fields by default. A future format migration
must be explicit, testable, and reversible before activation. A partial or
unparseable file cannot be treated as a fresh validator.

## Integration Boundary

Persistence belongs around the consensus executor, not in a relay callback and
not in a dashboard process. The order for a locally originated vote is:

1. resolve the chain-rooted membership and expected proposer;
2. construct the canonical message and signing tuple;
3. durably reserve the tuple in the signing journal;
4. sign the exact reserved payload;
5. durably record the signed envelope;
6. apply it to the local executor; and
7. publish it through GossipSub.

Inbound messages are first authenticated and membership-checked, then added to
the bounded replay record only after the executor accepts them. Duplicate
messages may be retained only once by canonical payload hash.

An external block must still require a verified quorum certificate rooted in
the active, chain-derived membership. Durable local state improves signer
safety; it does not replace quorum-certificate enforcement.

## Staged Rollout

1. Add the state format and restore validator behind a disabled feature gate.
   Do not write it in production yet.
2. Add deterministic unit tests for corruption, torn write recovery, signer
   mismatch, membership mismatch, stale chain tip, and conflicting vote
   refusal.
3. Run shadow persistence on a staging network. Write and validate state while
   keeping the current compatibility producer path authoritative.
4. Run a four-validator, multi-process staging network. Kill and restart each
   validator at proposal, prevote, precommit, timeout, and post-commit points.
   Confirm no signer emits conflicting evidence and all survivors converge.
5. Make a chain-rooted membership transition and quorum-certificate block gate
   available, still disabled by a shared activation height.
6. Independently review the state format, signing order, recovery procedure,
   and multi-process test evidence.
7. Upgrade every validator, confirm identical capabilities and activation
   settings, then enable the shared future height.

## Explicit Non-Goals

This work does not:

- make local configuration authoritative for membership;
- let a relay or a dashboard repair consensus state;
- recover a lost validator signing key;
- turn a single-process test into multi-host BFT proof; or
- permit external block acceptance without a verified quorum certificate.

## Implementation Checklist

- [ ] Define a canonical, bounded durable BFT record and schema version.
- [ ] Add a write-ahead local signing journal with conflict refusal.
- [ ] Rebuild active rounds only through normal envelope verification.
- [ ] Fail closed for signing on corruption or binding mismatch.
- [ ] Add atomic-write, crash, and migration tests on Windows and Linux.
- [ ] Add a multi-process four-validator crash-restart staging harness.
- [ ] Add chain-rooted membership transition state and quorum-certificate
      external block enforcement.
- [ ] Obtain independent security review before any production activation.
