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
