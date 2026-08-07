# Integer-dust fork readiness

QSDM currently persists legacy account balances as floating-point CELL. The
integer-dust implementation must not be activated against a running legacy
snapshot until the synthetic system funder has been replaced by a persisted,
capped issuance ledger.

## Read-only assessment

Stop one validator cleanly, copy these four files from the same committed
snapshot, and run the planner against the copies:

```text
qsdm_accounts.json
qsdm_chain.ndjson
qsdm_enrollment.json
qsdm_staking.json
```

```bash
go run ./cmd/qsdm-ledger-fork-plan \
  --accounts /safe-copy/qsdm_accounts.json \
  --chain /safe-copy/qsdm_chain.ndjson \
  --enrollment /safe-copy/qsdm_enrollment.json \
  --staking /safe-copy/qsdm_staking.json \
  --out /safe-copy/ledger-fork-plan.json
```

The command is read-only and refuses to overwrite an existing manifest. It
checks file stability, chain continuity, canonical block hashes, historical
reward limits, funder nonce reconciliation, liquid balances, enrollment bonds,
staking bonds, task stakes, task reward pools, pending task rewards, stream
escrow, and the 100 million CELL supply cap. Task and stream state is replayed
from the chain journal. Wallet, node, GPU, and key identifiers are omitted from
the output.

## Current fail-closed behavior

The node rejects every non-zero `fork_dust_height` during configuration
validation, and the process entrypoint repeats that refusal as defense in
depth. The integer accounting code can be tested, but it cannot be armed on a
validator until the missing capped-issuance transition is implemented and this
guard is deliberately replaced in a reviewed release.

`MigrateToDust` also validates the entire account set before changing any
account. An overflowing legacy balance therefore cannot leave a partially
migrated store.

## Reconciliation and legacy allocations

`accounted_circulating_dust` includes liquid and contract-held CELL. It must not
exceed chain-classified issuance. A positive `accounted_excess_dust` is not a
rounding adjustment and must never be accepted automatically.

Historical development prefunds may explain a stable excess, but explanation
is not authorization. Before activation, governance must choose and publish one
deterministic disposition:

1. Burn the grandfathered allocation in the fork transition; or
2. Classify it against an explicitly approved treasury reserve allocation.

That decision, the exact aggregate dust amount, source snapshot hashes, and
resulting state root must be committed to the reviewed migration manifest.

## What the manifest does not authorize

`activation.ready` remains `false` by design. A generated manifest is evidence
for review, not an activation command. Do not set `fork_dust_height` until all
of the following exist:

1. A deterministic state transition removes the legacy synthetic funder and
   initializes a persisted capped-issuance ledger.
2. Any accounted excess has an explicit burn or treasury classification in the
   approved migration manifest.
3. Every validator runs the same release and independently reproduces the same
   manifest hashes and totals.
4. Validator operators approve one manifest hash and one activation height.
5. A full backup, restore, replay, fork-boundary, and rollback rehearsal passes
   on a disposable copy of production state.

The default notice period used by the planner is 60,480 blocks, approximately
seven days at the ten-second target block time. This is a minimum coordination
window, not permission to activate automatically.
