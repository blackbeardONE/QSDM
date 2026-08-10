# Signed consensus rollout

QSDM validators authenticate BFT votes, block producer messages, prevote-lock
proofs, round certificates, and equivocation evidence with ML-DSA-87. Signature
enforcement is height-gated so validators can be upgraded before the network
switches policy at one shared block height.

This is a validator-set operation. Never enable it on only one validator.

## Validator identity

Each node loads or creates a consensus-only hot key. By default it is stored
next to the SQLite state as `qsdm_consensus_signer.json`. It is not a CELL
wallet and must never receive user or treasury funds.

The file is mode `0600` and contains private signing material. Back it up with
the validator state. Losing or replacing it changes the node's validator
address. Never copy one validator's key to another validator.

An explicit path is optional:

```toml
[consensus]
signer_key_path = "qsdm_consensus_signer.json"
require_signed_votes = false
signed_message_activation_height = 0
```

Environment equivalents are `QSDM_CONSENSUS_SIGNER_KEY_PATH`,
`QSDM_REQUIRE_SIGNED_VOTES`, and
`QSDM_SIGNED_MESSAGE_ACTIVATION_HEIGHT`.

## Phase 1: upgrade and observe

1. Back up every validator's state directory.
2. Upgrade every validator to the same signing-capable release.
3. Keep `require_signed_votes = false` and
   `signed_message_activation_height = 0`.
4. Restart each validator twice and confirm the same address appears in the
   `Validator consensus signer ready` log entry after both starts.
5. Confirm each node emits signed BFT traffic and remains synchronized.

Present but invalid signatures are rejected even during this compatibility
phase. Only missing signatures remain accepted.

## Phase 2: choose one height

After every validator is upgraded, operators approve one future activation
height with enough time to update and restart every node. Configure the exact
same values everywhere:

```toml
[consensus]
require_signed_votes = true
signed_message_activation_height = 600000
```

The node refuses to start when only one of these settings is active. Blocks and
other consensus artifacts below the activation height remain readable for
historical replay. At and above the height, unsigned artifacts are rejected.

## Phase 3: verify activation

Before the chosen height, verify every node logs the same activation height.
At the boundary, monitor chain height, peer count, BFT commits, follower
append errors, POL conflicts, and signature rejection metrics. Do not
unilaterally disable enforcement after activation; a validator with different
settings can diverge from the network.

## Current boundary

The current runtime uses one configured block producer with append-only
followers. The signed-message rollout removes identity forgery on that path,
but it does not by itself create a dynamically managed multi-validator
membership protocol. Signed POL artifacts are therefore limited to singleton
certificates from the active producer; multi-validator certificates remain
disabled until they carry an authenticated vote from each validator.
Automatic proposer-equivocation reports that lack two signed vote exhibits
fail closed after activation and do not slash anyone.
