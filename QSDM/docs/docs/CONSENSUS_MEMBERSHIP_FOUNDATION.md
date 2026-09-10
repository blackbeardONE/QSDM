# Consensus Membership Foundation

QSDM now has a deterministic format for describing a future validator set.
It is a preparation tool, not a switch that changes how the network produces
blocks today.

## What It Does

A membership file records, for each validator:

- the validator's QSDM consensus address;
- the ML-DSA-87 public consensus key that derives that address;
- the validator's libp2p peer ID; and
- integer voting power.

The file has a stable fingerprint. Every operator who validates the same file
will see the same fingerprint, total voting power, and required quorum.
This lets operators review the exact proposed set before it is ever made part
of the chain.

It also removes two unsafe sources of disagreement from future consensus work:

- runtime-only membership registration; and
- floating-point stake arithmetic for voting thresholds.

## What It Does Not Do Yet

This foundation does **not** enroll a validator, authorize a peer, change the
current block producer, change the current validator set, or enable a
multi-validator vote reactor.

The following work remains before QSDM can safely use it for real BFT block
finality:

1. Commit membership snapshots to the chain using an old-set-approved change.
2. Resolve the applicable snapshot by block height during replay and recovery.
3. Bind a snapshot member's peer ID to authenticated vote transport.
4. Require signed votes and a real quorum certificate before accepting a block.
5. Prove proposer rotation, recovery, and failover with a real multi-node
   staging network.

Do not treat a membership JSON file or its fingerprint as proof that those
steps are active.

## Membership File Shape

The following is an illustrative shape. Replace the placeholder values with
full lower-case values; it is not itself a valid membership file.

```json
{
  "schema_version": 1,
  "network_id": "qsdm-mainnet",
  "effective_height": 0,
  "members": [
    {
      "address": "<64-character lower-case SHA-256 public-key hash>",
      "consensus_public_key_hex": "<full lower-case ML-DSA-87 public key>",
      "p2p_peer_id": "<canonical libp2p peer ID>",
      "voting_power": 1
    }
  ]
}
```

Rules enforced by the validator:

- schema version must be `1`;
- network ID must be non-empty and have no surrounding whitespace;
- each address, public key, and peer ID must be unique;
- the address must derive from the listed public key;
- the peer ID must parse to the exact canonical libp2p value;
- voting power must be a non-zero integer; and
- the total voting power must fit in an unsigned 64-bit integer.

The quorum is the smallest whole number strictly greater than two thirds of
the total voting power. For example, voting powers totaling `4` require `3`;
voting powers totaling `17` require `12`.

## Safe Offline Workflow

On each validator, print its public identity without creating a key:

```powershell
qsdm-consensus-membership `
  --identity-key C:\QSDM\validator-consensus-key.json `
  --p2p-peer-id <validator-libp2p-peer-id> `
  --json
```

The command reads an existing key only. If the path is wrong or missing, it
fails instead of creating a different validator identity. Its output contains
only the address, public key, and peer ID. It never prints the private key.

After assembling a proposed membership file, each operator should independently
validate it:

```powershell
qsdm-consensus-membership --in proposed-membership.json --json
```

Compare the reported `membership_fingerprint` through an authenticated operator
channel. Do not accept a new set merely because one node generated the file.

## Security Notes

- Keep the signer JSON private and backed up using the QSDM keystore policy.
- A public consensus key is safe to share; its private key is not.
- A peer ID identifies network transport, not voting permission by itself.
- A valid membership file is a review artifact until a future chain update
  commits and enforces it.

## Height-Indexed Schedules

QSDM also has an immutable in-memory schedule that resolves the membership
snapshot applicable to a given chain height. A schedule sorts snapshots by their
activation height, rejects duplicate activation heights and network mismatches,
and returns defensive copies to readers.

That solves the deterministic lookup rule needed for replay and recovery:
"which set applies at height H?" It does not yet solve the source-of-truth
rule: today no chain transaction creates or persists this schedule. A future
membership change must be approved by the old set, committed to the chain, and
reconstructed from chain history before the schedule may control BFT voting.
## Opt-In BFT Wire Gate

The BFT executor can now be given an immutable membership schedule and an
activation height by an embedding application. At and after that height it:

- requires every outbound vote to carry the active membership fingerprint;
- signs that fingerprint as part of the vote digest; and
- rejects inbound signed votes whose fingerprint, validator address, or public
  key differs from the active snapshot.

Before the configured activation height, the gate emits no membership
fingerprint and continues to verify the existing signed BFT wire format. This
allows a coordinated upgrade without making historical signed messages
unreadable.

This gate is deliberately **not installed by the current QSDM node runtime or
enabled through production configuration**. It is an isolated enforcement
building block, not proof that QSDM currently has multi-validator BFT
finality. It does not replace chain-committed membership, authenticated
peer-to-signer transport binding, integer quorum accounting, or verified
multi-node proposer rotation.
## Staging Integer Voting View

The membership foundation also provides an immutable integer-only voting view
for a selected snapshot, or for the snapshot active at a selected height. It:

- preserves the snapshot fingerprint and canonical address order;
- sums only distinct, exact member addresses;
- rejects unknown and duplicate voters instead of silently treating them as
  zero voting power; and
- calculates the smallest integer strictly greater than two thirds of total
  voting power.

The view has a simple address-order rotation for the multi-node staging
harness. That rotation is deliberately not a production weighted proposer
algorithm and is not connected to the current node runtime. It exists to make
membership, quorum accounting, and failover tests deterministic before any
chain-committed BFT transition is proposed.

## In-Memory Four-Node Staging Test

`TestBFTMembershipFourNodeStagingClusterCommitsAfterFailover` runs four
independent consensus/executor instances against the same immutable membership
snapshot. It relays membership-bound signed messages to every instance,
deliberately retires the first round, and verifies that the rotated proposer
and a full signed quorum commit the same value on every instance.

It is a regression harness, not a network deployment. It does not test real
libp2p transport, peer-to-signer identity binding, process recovery, or a
chain-committed transition. Those require a separate two-or-more-process
staging network before production BFT can be considered.

## Validator Peer Binding Proof

`ConsensusPeerBinding` is a separate public proof that binds a membership
member's ML-DSA consensus signer to its declared libp2p peer identity. Both
private keys sign the same membership-fingerprint-specific challenge, so a
proof cannot be copied to another membership snapshot or substituted with an
uncommitted key.

The proof deliberately does not treat the network peer that relays a message
as the validator that originated it. Gossip relays may differ from the vote
originator. It proves key possession for the identity pair committed in the
snapshot; a future transport handshake must still bind that proof to a live
peer session before it can enforce a production peer policy.

The current runtime does not create, distribute, or enforce these proofs.
They are a testable prerequisite for that later chain-approved transition.

## Authenticated Publisher Policy

QSDM libp2p GossipSub uses `StrictSign`: libp2p verifies the original
publisher before delivering a message to a subscription. `Message.GetFrom`
therefore identifies that authenticated publisher, while `ReceivedFrom`
identifies only the immediate relay hop. The two values may differ and must
not be conflated.

`BFTPeerOriginPolicy` is an opt-in ingress gate. At its activation height it
requires each signed BFT message's `GetFrom` peer ID to equal the peer ID
committed for the message's validator in the active membership snapshot.
Relay identity remains available only for rate limiting and reputation.

The current node runtime does not install this policy. Activating it requires
every validator to share the same membership schedule, use `StrictSign`, and
roll out a common activation height. The live three-host regression test
covers the important case where the publisher and relay are different peers.