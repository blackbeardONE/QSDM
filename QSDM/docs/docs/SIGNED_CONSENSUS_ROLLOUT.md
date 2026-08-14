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

## Task-action signatures

Separate from the signed-vote rollout above, and separately gated.

`/api/v1/tasks/actions/submit-signed` has always verified the ML-DSA envelope
at admission. Until recently the mempool transaction dropped the signature
afterwards, so consensus replay re-checked nothing and a proposer could inject
task actions against any account. The proof is now carried and re-verified at
apply time (`chain.VerifyTaskActionSignature`).

Two behaviours, only one of which is on by default:

- **Always on.** A signature that is PRESENT is verified at every height, and a
  bad one is refused. This carries no replay risk: a historical unsigned action
  has nothing to verify and is unaffected.
- **Off by default.** Whether a signature is REQUIRED is governed by
  `[consensus] task_action_signature_activation_height` (env:
  `QSDM_TASK_ACTION_SIGNATURE_ACTIVATION_HEIGHT`). Zero, the default, requires
  nothing -- an unsigned task action is accepted at any height. The node logs a
  WARNING at startup while this is the case.

### Before enabling it

Setting the height rejects any unsigned task action at or above it. If your
chain already contains one above the value you choose, replay diverges and the
node cannot follow.

So pick a height ABOVE your current tip, and confirm your history first. There
is a command for this:

```
go build -tags dilithium_circl -o qsdm-taskaction-scan ./cmd/qsdm-taskaction-scan
./qsdm-taskaction-scan --chain /opt/qsdm/qsdm_chain.ndjson
```

It is read-only. It reports the tip, how many `qsdm/tasks/v1` transactions the
chain carries, how many of those are unsigned, the greatest height carrying an
unsigned one, and a suggested activation height above both that and the tip.

If it reports `REFUSING TO ADVISE: no blocks were read`, the path is wrong or
the volume is not mounted. It exits non-zero rather than suggesting a height,
because an unread chain and a clean chain otherwise look identical.

Run it on EVERY node. They should agree; if they do not, that disagreement
matters more than the number. Every validator must then be set to the same
value, as with `signed_message_activation_height`.

## Transaction-content root (coordinated fork)

`computeTxRoot` merkleizes transaction IDs only, and the block hash signs over
that root. So any field a transaction carries that the state root does not
independently distinguish -- amount, recipient, contract, payload, the
signature itself -- can be rewritten in flight with the block hash, the
producer signature and the state root all still verifying.

`[consensus] tx_content_root_activation_height` (env:
`QSDM_TX_CONTENT_ROOT_ACTIVATION_HEIGHT`) sets the first height whose tx root
commits contents instead. Zero, the default, keeps the legacy root; the node
logs a WARNING at startup while that is so.

### This one is different from every other gate here

The other activation heights change what a node ACCEPTS. This changes what a
node COMPUTES: block hashes differ from the activation height onward. A node
with a different value derives different hashes for the same blocks and forks
immediately.

So: pick a height comfortably above the current tip, set the identical value on
every node, and restart them all before the chain reaches it. Verify with
`grep tx_content_root_activation_height` across your fleet, not from memory.
A node that misses the change will reject every block from the activation
height onward.

Producer and validator derive the hash through one function
(`computeBlockHash`); `recomputeHash` in the propagation path delegates to it
rather than carrying its own copy, so there is no second derivation to keep in
step. That was not true before commit `bee8da4`, and activating the gate
against a build without it would halt propagation network-wide.

## Current boundary

The current runtime uses one configured block producer with append-only
followers. The signed-message rollout removes identity forgery on that path,
but it does not by itself create a dynamically managed multi-validator
membership protocol. Signed POL artifacts are therefore limited to singleton
certificates from the active producer; multi-validator certificates remain
disabled until they carry an authenticated vote from each validator.
Automatic proposer-equivocation reports that lack two signed vote exhibits
fail closed after activation and do not slash anyone.
