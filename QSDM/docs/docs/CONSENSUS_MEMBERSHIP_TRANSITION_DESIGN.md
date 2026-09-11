# Consensus Membership Transition Design

## Status

This document is a design and rollout gate. It does not activate multi-validator
BFT, change the current block producer, or permit a local configuration file to
change validator membership.

## Non-Negotiable Rule

The validator set that decides a block at height H must be derived from chain
state that every honest node replays identically. A local YAML setting, a local
JSON schedule, an off-chain vote tally, or an operator dashboard action cannot
be an authority for production validator membership.

The existing membership-file and schedule tools are review artifacts only. The
existing `qsdm/gov/v1` parameter mechanism remains a separate governance
feature; it is not a validator-set transition mechanism and must not be used to
activate one.

## Required Chain State

Before production activation, QSDM needs a dedicated membership-transition
state machine with all of the following values committed into the consensus
state root:

- the active membership snapshot and its fingerprint;
- a bounded queue of pending transition proposals;
- the full proposed replacement snapshot, not only a fingerprint;
- the activation height for each accepted proposal;
- approvals from members of the active, pre-transition set; and
- enough replay metadata to reject duplicate, stale, and conflicting proposals.

The state machine must implement `ChainReplayClone` and
`RestoreFromChainReplay` so speculative BFT execution and external block
replay use independent state. Its state root contribution must be enabled at a
coordinated network height. A transition that is not in the state root is not a
consensus transition.

## Current Foundation Implementation

`ConsensusMembershipState` now models the required transition data in an
isolated chain package: an active snapshot, a bounded queue of complete
replacement proposals, signed approvals from the active old set, an integer
more-than-two-thirds quorum, exact-height resolution, bounded audit history,
and deterministic state-root bytes. It also has typed clone and restore helpers
so the model can be exercised without shared mutable state.

It is deliberately **not active in the live chain**. It does not accept a
transaction, contribute to the current block state root, alter the runtime
validator set, or permit `qsdm/gov/v1` to change membership. Those links must
be introduced together behind a coordinated activation plan; connecting only
one of them would create a fork risk rather than BFT safety.

## Approval Rule

A proposed replacement set must be approved by the currently active set, not
by the proposed set and not by a separate locally configured authority list.

Each approval must bind this exact tuple:

1. network ID;
2. active membership fingerprint;
3. proposed membership fingerprint and complete canonical member list;
4. activation height; and
5. a domain-separated proposal ID.

The approval threshold is the existing integer quorum: strictly more than two
thirds of the active set's voting power. Duplicate, unknown, and stale votes
must be rejected. A vote cannot be carried forward to a proposal with a
different snapshot or activation height.

At the activation height, block acceptance must resolve the membership from
the committed schedule. Messages for the prior set are valid only below the
activation height; messages for the new set are valid at and above it.

## Required Safety Gates

Do not activate the state machine until all of these conditions are true:

1. `require_signed_votes` is enabled on every validator.
2. Votes carry and verify the committed membership fingerprint.
3. StrictSign publisher identity is checked against the committed member peer
   ID, not the relay hop.
4. A block is accepted only with a verified quorum certificate from the
   active set for that height.
5. The transition state and its scheduled activation are included in the state
   root and survive snapshot, recovery, local production, and external block
   replay.
6. A multi-process staging network proves proposer rotation, failover,
   equivocation handling, and a membership change under packet delay and node
   restart.
7. Every participating node runs the same released capability before the
   transition proposal is admitted. Nodes that cannot verify the new rule must
   stop rather than silently follow a different chain.

## Rollout Sequence

1. Ship the state-machine code disabled behind a network activation height.
2. Establish a fixed genesis membership snapshot and independently verify its
   fingerprint on every staging validator.
3. Run a multi-process staging network with at least four validators and
   confirm the same committed tip and state root after normal rounds, a failed
   proposer, relay hops, and restart recovery.
4. Submit a test transition from the old set, collect a real old-set quorum,
   and verify every node switches at the same height.
5. Repeat the test with an invalid, duplicate, stale, wrong-peer, and
   insufficient-quorum transition; each must leave the active set unchanged.
6. Publish the version and activation plan, upgrade all production validators,
   and verify readiness before allowing a production proposal.
7. Only after the production transition is independently observed should the
   current single-producer compatibility path be retired.

## What This Does Not Solve

This design does not claim that QSDM is BFT-safe today. Until the gates above
are implemented and demonstrated, the production chain must keep its current
single-producer compatibility posture. Local schedule validation, peer-binding
proofs, and staging harnesses are prerequisites, not finality.

## Implementation Checklist

- [ ] Add a chain-rooted `ConsensusMembershipState` with deterministic
      serialization and bounded proposal storage.
- [ ] Add signed, old-set-quorum membership-transition transactions.
- [ ] Include that state in clone, restore, state-root, snapshot, and recovery
      paths.
- [ ] Require a quorum certificate for external block acceptance.
- [ ] Enable membership and publisher policies only from committed state.
- [ ] Add a real multi-process validator test harness and failure-injection
      scenarios.
- [ ] Conduct an independent security review before a production activation.