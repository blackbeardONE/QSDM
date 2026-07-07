# QSDM Task Catalog and Registry API

QSDM Core exposes a synchronized, versioned task catalog for QSDM Hive and SDK clients. Catalog manifests are `qsdm/tasks/v1` consensus state, so validators replay the same signed publications and derive the same catalog state root.

The local JSON registry remains a bootstrap and recovery input. It can mark built-in tasks as trusted, but it is not the network source of truth for catalog versions, manager ownership, stake, rewards, or task activity.

## How Task Distribution Works

1. A publisher opens **Add Task > Task Studio** in QSDM Hive.
2. Hive creates a bounded manifest, inserts the active QSDM signer as manager, signs a catalog action with the wallet keystore, and submits it to QSDM Core.
3. A validator includes the `qsdm/tasks/v1` action in a block.
4. Every synchronized validator replays that block and exposes the same catalog manifest through `GET /api/v1/tasks`.
5. Hive refreshes its catalog every 15 seconds. New compatible tasks appear without reinstalling Hive.

A Hive update is required only when a manifest requires a newer Hive version or a new executable capability. Updating text, economics, URLs, tags, timing, or activity state does not require a client release.

There is no required centralized task provider. A public API or home gateway is a convenient transport for clients, while catalog authority comes from replicated QSDM chain state.

## Configuration

Set the registry path before starting QSDM Core:

```text
QSDM_TASK_REGISTRY_PATH=/opt/qsdm/tasks.json
```

If the variable is unset, chain-published catalog tasks are still returned. If neither bootstrap data nor chain state is available, `GET /api/v1/tasks` returns an empty, unconfigured response instead of failing startup.

Set the signed task-action log path to enable native task write intents:

```text
QSDM_TASK_ACTION_LOG_PATH=/opt/qsdm/task-actions.jsonl
```

## Endpoints

- `GET /api/v1/tasks`
- `GET /api/v1/tasks/{task_id}`
- `GET /api/v1/tasks/{task_id}/submissions`
- `GET /api/v1/tasks/state`
- `GET /api/v1/tasks/{task_id}/state`
- `POST /api/v1/tasks/actions/submit-signed`
- `GET /api/v1/tasks/actions`
- `GET /api/v1/tasks/actions/{action_id}`

The catalog read endpoints are public and read-only. The action submit endpoint is public but requires an ML-DSA signed self-custody envelope where `sender == hex(sha256(public_key))`. QSDM Core records accepted actions in the configured JSONL log, rejects duplicate IDs, and rejects opted-in nonce replays where `nonce <= last_nonce(sender)`. When the node has live v2 wiring, accepted envelopes are also submitted to the mempool as `qsdm/tasks/v1` transactions for deterministic block replay.

`/tasks/state` and `/tasks/{task_id}/state` expose deterministic task state. On a live validator they read the chain-backed task state store. In standalone/API-only mode they fall back to the JSONL action log projection. The normal `/tasks` and `/tasks/{task_id}` registry responses are also overlaid with task state when a chain store or action log is configured.

## Registry Shape

The file may be either `{ "tasks": [...] }` or a raw task array.

```json
{
  "tasks": [
    {
      "task_id": "qsdm-demo-task",
      "task_name": "QSDM Demo Task",
      "is_allowlisted": true,
      "is_active": true,
      "task_audit_program": "bafy...",
      "task_metadata": "bafy...",
      "minimum_stake_amount": 0,
      "round_time": 600,
      "submission_window": 60,
      "audit_window": 60
    }
  ]
}
```

QSDM Core fills compatibility defaults for empty task manager, stake pot, map fields, and task type so current Hive builds can parse bootstrap entries without legacy chain account data. Windows UTF-8 BOM input is accepted, although launchers write new registries atomically as UTF-8 without BOM.

## Consensus Catalog Manifest

Registration is permissionless. The registering signer becomes the immutable manager. Only that manager can publish the next exact version, pause the task, or resume it.

```json
{
  "schema_version": 1,
  "task_id": "qsdm-shared-edge",
  "version": 1,
  "name": "QSDM Shared Edge",
  "description": "Contribute bounded CPU work to the QSDM edge pool.",
  "manager": "hex-sha256-public-key",
  "active": true,
  "runtime": {
    "kind": "capability",
    "capability": "generic-proof-v1",
    "min_hive_version": "1.3.60",
    "max_memory_mb": 256,
    "max_runtime_seconds": 30
  },
  "minimum_stake_amount": 1,
  "reward_per_round": 0.05,
  "round_time": 60,
  "submission_window": 30,
  "audit_window": 15,
  "metadata_url": "https://qsdm.tech/tasks/qsdm-shared-edge",
  "source_url": "https://qsdm.tech/docs/#/qsdm-shared-edge",
  "authorized_relay_ids": [
    "64-character-key-derived-relay-id"
  ],
  "tags": ["cell", "cpu", "qsdm"]
}
```

Catalog actions are:

- `catalog-register`: version must be `1`; task ID must not already have a manifest.
- `catalog-update`: signer must be the manager; version must equal current version plus one.
- `catalog-pause`: manager-only emergency stop without deleting history.
- `catalog-resume`: manager-only reactivation.

Bootstrap trust and catalog publication are separate. A permissionless task is synchronized but unverified. Hive lists it under **Show non-verified tasks** until an operator or governance process places that task ID in the trusted bootstrap registry.

`authorized_relay_ids` is optional for ordinary tasks and required for pooled CPU, GPU, or RAM settlement. Each value is the 32-byte hexadecimal ID shown by Edge Control and derived from the Relay's persistent ML-DSA-87 public key. The task manager can rotate or revoke Relay authorization only through the next signed catalog version. Core still verifies the full public key and signature in every proof; the manifest stores only its compact key-derived ID.

## Runtime Safety

Catalog publishers cannot inject native JavaScript into Hive.

- `capability` selects bounded code already shipped and reviewed in Hive. The initial supported capability is `generic-proof-v1`.
- `wasm` requires an absolute HTTPS module URL, a 64-character SHA-256 digest, and an ABI identifier. Manifests are accepted by Core, but Hive blocks execution until its sandboxed WASM runtime is available.
- Unknown capabilities, unsupported manifest schemas, and tasks requiring a newer Hive version remain non-executable.

The manifest is the task definition and compatibility contract. Participant execution, stake, reward pools, submissions, claims, and manager actions are separate consensus state. The task row in Hive is therefore a view over real manifest and ledger state, not merely a banner.

## Signed Task Actions

Task actions use the same self-custody identity rule as `/wallet/submit-signed`.

```json
{
  "id": "action-id",
  "sender": "hex-sha256-public-key",
  "task_id": "qsdm-demo-task",
  "action": "start",
  "payload": "{\"mode\":\"service\"}",
  "nonce": 1,
  "timestamp": "2026-05-28T00:00:00Z",
  "signature": "hex-mldsa-signature",
  "public_key": "hex-mldsa-public-key"
}
```

Supported action names are `start`, `stop`, `stake`, `fund`, `unstake`, `submit`, `claim`, `withdraw`, `migrate`, `catalog-register`, `catalog-update`, `catalog-pause`, and `catalog-resume`.

Successful submissions return `mempool_status`. `submitted` means the action was added to the live mempool, `duplicate` means the tx was already pending, and `not_configured` means the action was persisted to the JSONL log but no live mempool submitter is installed in this process.

When `qsdm/tasks/v1` actions are included in a block, they consume the sender's live AccountStore nonce.

- `stake` debits CELL from the sender and locks it in task state.
- `unstake` and `withdraw` release only already-locked task stake back to the sender; over-withdraw attempts are rejected without mutating account or task state.
- `fund` debits CELL from the sender and locks it in the task reward pool.
- `submit` records proof metadata. If the payload includes `reward_amount`, the sender must have task stake and the reward is reserved from the funded reward pool into the sender's pending reward balance.
- `claim` pays claimable pending rewards back to the sender's AccountStore balance. Double-claims are rejected without mutating account or task state.
- A `qsdm-edge-relay-v2` resource submission is settled in the `submit` transaction itself. Core requires an authorized Relay ID, rejects proof or receipt reuse globally, and atomically pays 70% to the bound contributor owner, 15% to Mother Hive, and 15% to the fixed ecosystem reserve. There is no second `claim` action for that batch.

The `/tasks` overlay exposes pending rewards through `available_balances`, includes `reward_pool_amount`, `pending_reward_amount`, and `total_reward_paid_amount`, and annotates submissions with `reward_amount`, `claimed`, and `claimed_at`.

Example funded proof flow:

```json
{"action":"fund","amount":25}
{"action":"stake","amount":5}
{"action":"submit","payload":"{\"round\":12,\"slot\":100,\"submission_value\":\"bafy-proof\",\"reward_amount\":1.5}"}
{"action":"claim","payload":"{\"round\":12}"}
```

Unsigned action envelopes can be signed with a self-custody keystore:

```bash
qsdmcli wallet sign-task-action \
  --in /path/to/qsdm-wallet.json \
  --passphrase-file /path/to/passphrase.txt \
  --envelope-file task-action.json \
  | curl -fsS -H 'Content-Type: application/json' --data-binary @- \
      http://localhost:8080/api/v1/tasks/actions/submit-signed
```
