# Real multi-validator consensus

> **Status: design and rollout plan. This is not active on the public QSDM
> network.**

QSDM can authenticate consensus messages with ML-DSA-87 and can report the
size and fingerprint of the validator set each node has loaded. Those are
important building blocks, but they are not the same thing as a live
multi-validator consensus network.

Today, a normal QSDM node starts with its own local consensus signer as the
only member of its in-memory validator set. The local synthetic round is
deliberately restricted to that one signer. It refuses to create votes for
other validators. This prevents fabricated votes, but it also means that QSDM
must not claim Byzantine-fault-tolerant finality until the work below exists
and has passed the activation tests.

## What already protects the network

- Every node can use a dedicated ML-DSA consensus signing key. It is separate
  from a CELL wallet.
- A present BFT signature is always checked. A bad signature is rejected.
- The synthetic proposer path refuses to run unless exactly one active
  validator exists and it matches the local signing key.
- Unprovable `invalid_vote` accusations are rejected. Unsigned conflicting
  proposals cannot create slashable evidence.
- The validator status API can expose an active validator count and a
  deterministic fingerprint. The rollout preflight compares those values
  between nodes before anyone considers activation.

These controls are safety rails for the current single-validator posture.
They do **not** turn a peer allowlist, a static configuration file, or a
two-node connectivity rehearsal into a validator membership system.

## What must be built

### 1. Chain-committed validator membership

Validator membership must live in replayable chain state, not in each
operator's local configuration. Each membership record needs at least:

- validator address derived from the ML-DSA public key;
- consensus public key and its stable fingerprint;
- libp2p peer ID used to deliver messages;
- bonded stake and lifecycle state;
- the first block height at which the record is effective.

The active-set root must be committed to every block and be reconstructable
from chain history. Adding, removing, jailing, or replacing a validator must
be an authenticated state transition. A local file can bootstrap a fresh
network, but it must never be able to silently rewrite the live set.

### 2. Membership changes with the old set's approval

Once a network has more than one validator, the current active set must
approve a membership change before the new set becomes effective. The change
needs a future effective height and a certificate from the old effective set.
This prevents a new node, a compromised operator machine, or a stale config
file from appointing itself a validator.

The initial multi-validator migration needs a separately reviewed genesis or
migration manifest. It must name every initial validator, public key, peer ID,
stake, membership root, and activation height. Each operator must verify the
same manifest hash before startup.

### 3. One validator, one local signature

For a multi-validator height:

1. The selected proposer builds and signs one proposal.
2. Each other active validator verifies the proposal, its membership snapshot,
   and the block body.
3. Each validator signs only its own prevote and precommit.
4. Gossip carries the individual signed messages.
5. A quorum certificate contains the actual signed votes that reached the
   threshold.

No code may loop through validator names and call `PreVote` or `PreCommit` as
if it held their private keys. The existing synthetic path already refuses
this, and that refusal must remain permanent.

### 4. Certificates that a follower can verify

A committed block at or above the activation height must carry a certificate
that binds:

- block height, round, block hash, and membership-root hash;
- every voter address and its exact signed message;
- the quorum calculation against the effective validator set.

Followers must reject a block whose certificate is incomplete, has an
unknown signer, uses a key that does not match membership, mixes membership
snapshots, or falls short of the configured quorum. A trusted block-producer
allowlist remains useful as transport protection; it is not a replacement for
a quorum certificate.

### 5. Network vote reactor and recovery

The node needs a real round reactor that:

- schedules proposals deterministically from the effective set;
- stores a validator's signed vote before publishing it, preventing accidental
  double-signing after restart;
- retries and advances rounds after a timeout without reopening an expired
  round;
- accepts only messages from the effective membership snapshot;
- rate-limits and deduplicates gossip by authenticated validator identity;
- persists enough state to recover after a restart without re-voting.

Equivocation and future invalid-vote evidence must be derived from signed
exhibits and processed only through the same replayable state transition as
other slashing. A network packet alone must never move stake.

## Delivery order

1. **Membership state and replay:** add immutable membership records,
   deterministic active-set roots, persistence, and replay tests.
2. **Membership transactions:** add join, leave, replace-key, jail, and
   unjail transitions with old-set quorum approval and future effective
   heights.
3. **Peer/key binding:** load remote BFT public keys only from the effective
   membership snapshot. The attester `peer_signers.toml` file is unrelated and
   must not become validator membership.
4. **Vote reactor:** replace synthetic multi-member behavior with local
   signing plus authenticated peer gossip.
5. **Quorum certificates:** verify them during block creation, propagation,
   catch-up, and replay.
6. **Migration tooling:** provide a manifest generator, a read-only
   preflight, and a safe rollback before the activation height.
7. **Activation:** enable signed consensus only after the full staged test
   matrix passes and every validator reports the same set root and activation
   height.

## Required test evidence before activation

QSDM must pass these tests on real, separately operated nodes before enabling
multi-validator consensus on a public chain:

- four-validator network with deterministic proposer rotation;
- catch-up from an empty follower and from a restarted validator;
- loss of one validator without violating the quorum rule;
- conflicting proposals and conflicting votes from a real signing key;
- partition and rejoin behavior with no double commit at one height;
- membership join, leave, key replacement, and jail at future heights;
- certificate replay, tampering, and wrong-membership-root rejection;
- independent cross-node comparison of tip, block hash, active-set count, and
  fingerprint after every test.

Two nodes are useful for transport and follower testing, but they are not a
Byzantine-fault-tolerant validator set. Four nodes are the minimum meaningful
test shape for one-fault BFT behavior.

## Operator gates

Until the implementation and test evidence above exist:

- leave `require_signed_votes` in its compatibility posture;
- do not set a signed-consensus activation height for public production;
- treat the validator count and fingerprint as transparency data, not a quorum
  guarantee;
- use the rollout preflight as a refusal gate, not as permission to skip the
  missing reactor and membership work.

The preflight should refuse activation when any node reports fewer than two
active validators, a missing fingerprint, a count mismatch, or a fingerprint
mismatch. That prevents an operator from accidentally declaring a
single-validator node to be a multi-validator network. It is intentionally a
guardrail, not the consensus implementation itself.

## Related documents

- [Signed consensus rollout](./SIGNED_CONSENSUS_ROLLOUT.md)
- [Validator quickstart](./VALIDATOR_QUICKSTART.md)
- [Production readiness](./PRODUCTION_READINESS.md)
- [QSDM network](./QSDM_NETWORK.md)