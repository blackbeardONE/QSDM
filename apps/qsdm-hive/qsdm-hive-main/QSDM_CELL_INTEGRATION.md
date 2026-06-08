# QSDM and CELL Integration

QSDM Hive now has a first-class QSDM Core bridge while keeping the existing task runtime in K2-compatible mode.

## Runtime Shape

- Desktop shell: QSDM Hive
- Native token display: CELL
- Protocol compatibility symbol: KOII
- Task runtime mode: `k2-compat`
- QSDM Hive API default: `https://api.qsdm.tech/attest/home-validator/api/v1`
- QSDM Gateway API default: `https://api.qsdm.tech/attest/home-validator/api/v1`
- QSDM Core API default: `http://localhost:8080/api/v1`
- QSDM dashboard default: `http://localhost:8081`
- Optional QSDM wallet address: `QSDM_WALLET_ADDRESS`
- Optional task action signer: `QSDM_TASK_ACTION_SIGNER=cli`
- Optional local proof-loop button: `QSDM_ENABLE_LOCAL_SIGNED_LOOP=1`
- Optional QSDM task registry: `QSDM_TASK_REGISTRY_PATH` on QSDM Core
- Optional QSDM task action log: `QSDM_TASK_ACTION_LOG_PATH` on QSDM Core

The task execution layer still depends on the K2-compatible task node package by default. If `QSDM_TASK_RUNTIME_MODE=qsdm-native` is set, Hive stops calling K2 task discovery and reads task inventory from QSDM Core's `/tasks` registry API. Native stake, submission, round, current-slot, and slot-time reads come from QSDM Core instead of the K2 RPC/cache path. With the CLI signer configured, native start/stop/stake/withdraw/claim flows submit signed QSDM task action intents to Core before updating local task process state. Core v2 wiring also offers accepted actions to the live mempool as `qsdm/tasks/v1` transactions, so block replay can commit deterministic task lifecycle state. Task `stake`, `fund`, `unstake`, `withdraw`, proof `submit`, and reward `claim` actions now mutate live CELL-backed task state when the action lands in a block.

## Environment

Override these values for non-local QSDM nodes:

```text
QSDM_HIVE_API_URL=https://api.qsdm.tech/attest/home-validator/api/v1
QSDM_GATEWAY_API_URL=https://api.qsdm.tech/attest/home-validator/api/v1
QSDM_CORE_API_URL=http://localhost:8080/api/v1
QSDM_DASHBOARD_URL=http://localhost:8081
QSDM_WALLET_ADDRESS=
QSDM_TASK_RUNTIME_MODE=qsdm-native
```

`QSDM_HIVE_API_URL` is the endpoint the desktop app uses for consumer-facing
Core reads and signed submissions. In production it should point at the Home
Gateway, not directly at a validator port. The Home Gateway then proxies only
the explicitly allowed Hive routes to the local Core API.

To let Hive sign native task start/stop intents with a QSDM self-custody keystore:

```text
QSDM_TASK_RUNTIME_MODE=qsdm-native
QSDM_TASK_ACTION_SIGNER=cli
QSDM_TASK_ACTION_CLI_PATH=qsdmcli
QSDM_TASK_ACTION_KEYSTORE_PATH=/path/to/qsdm-wallet.json
QSDM_TASK_ACTION_PASSPHRASE_FILE=/path/to/passphrase.txt
QSDM_TASK_ACTION_SENDER=<hex sha256 public key address>
QSDM_NATIVE_SUBMISSION_METHODS=checkSubmissionAndUpdateRound
QSDM_ENABLE_LOCAL_SIGNED_LOOP=0
```

`QSDM_ENABLE_LOCAL_SIGNED_LOOP=1` is intended for local/operator testing only.
It enables the Hive top-bar proof action that signs and submits the full CELL
task lifecycle against the configured API URL.

For the local home-validator proof loop, the repo includes:

```text
QSDM/scripts/run_hive_signed_cell_loop.ps1
```

That script signs and submits `fund -> start -> stake -> submit -> claim`
against the current validator using the configured QSDM signer. The visible
consumer loop is `start -> stake -> submit -> claim`; `fund` seeds the task
reward pool so `submit` can reserve a reward and `claim` can pay it back after
block inclusion.

On QSDM Core, set `QSDM_TASK_REGISTRY_PATH` to a JSON task registry to expose the native read API consumed by Hive:

```text
QSDM_TASK_REGISTRY_PATH=/opt/qsdm/tasks.json
QSDM_TASK_ACTION_LOG_PATH=/opt/qsdm/task-actions.jsonl
```

## App Bridge

The desktop app exposes a QSDM Core status endpoint through the existing Electron IPC path:

```text
renderer -> preload -> main controller -> QSDM Core API
```

For the public app path this becomes:

```text
renderer -> preload -> main controller -> Home Gateway -> QSDM Core API
```

The bridge checks:

- `/health`
- `/status`
- `/wallet/balance?address=...`
- `/wallet/nonce?sender=...`
- `/mining/account?address=...`
- `/wallet/submit-signed`
- `/tasks`
- `/tasks/{task_id}`
- `/tasks/{task_id}/submissions`
- `/tasks/state`
- `/tasks/{task_id}/state`
- `/tasks/actions/submit-signed`
- `/tasks/actions`
- `/tasks/actions/{action_id}`

If QSDM Core is offline, the app reports the core as offline instead of failing startup.
The same top-bar widget also reports whether the configured task-action signer
is ready and, when explicitly enabled, can run a local signed proof loop through
the Home Gateway/Core path.

## CELL Account Reads

Set `QSDM_WALLET_ADDRESS` to a QSDM ML-DSA self-custody address to show CELL balance in the top bar. The address is not the old K2/Solana public key; QSDM derives account addresses from the ML-DSA public key used by `qsdmcli wallet` and the browser wallet.

Signed CELL sends are exposed through `submitQsdmSignedTransaction`. Hive submits a ready envelope to `/wallet/submit-signed`; signing remains outside the app until the QSDM wallet WASM/keystore flow is embedded.

## QSDM Task Registry

QSDM Core now exposes read-only native task discovery from `QSDM_TASK_REGISTRY_PATH`. The file can be shaped as either `{ "tasks": [...] }` or a raw array. Hive consumes those tasks when `QSDM_TASK_RUNTIME_MODE=qsdm-native`.

Minimal entry:

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

Signed task actions are accepted through `/tasks/actions/submit-signed`. The envelope uses the same self-custody rule as wallet sends: `sender` must equal `hex(sha256(public_key))`, and the ML-DSA signature must verify over the envelope with `signature` and `public_key` cleared. Hive can produce these envelopes through `qsdmcli wallet sign-task-action` when `QSDM_TASK_ACTION_SIGNER=cli`.

Before signing, Hive reads `/mining/account?address=...` and stamps the live AccountStore nonce into the task action envelope when the endpoint is available. That keeps block-time task actions aligned with CELL account replay protection.

QSDM Core exposes deterministic task state through `/tasks/state`. On a fully wired validator that state comes from the chain-backed task store; standalone API processes fall back to action-log projection. `/tasks` overlays task-state stake and submissions into the registry response consumed by Hive. Accepted actions report `mempool_status=submitted` or `duplicate` when a live mempool is wired, and `not_configured` after persisting the action log in standalone mode.

This is the native registry plus signed action layer with CELL-backed task staking and reward settlement. Existing Hive stake and withdraw controls submit signed native `stake` and `withdraw` actions in `qsdm-native` mode. `fund` locks CELL into a task reward pool, `submit` can reserve a `reward_amount` from that pool when the submitting account has task stake, and `claim` pays pending rewards back to the signer after block inclusion. Hive's native claim path now submits a signed `claim` task action instead of calling the old K2 claim routine.

When a running task calls the standard namespace method `checkSubmissionAndUpdateRound(submission, round)` in `qsdm-native` mode, Hive intercepts that local `namespace-wrapper` call, signs a native QSDM `submit` action, sends it to `/tasks/actions/submit-signed`, and merges the submission into the local cache for immediate UI feedback. If a task template uses a different submission method name, add it to `QSDM_NATIVE_SUBMISSION_METHODS` as a comma-separated list.

Example proof payload accepted by Core:

```json
{
  "round": 12,
  "slot": 100,
  "submission_value": "bafy-proof",
  "reward_amount": 1.5
}
```

## Next Native Ports

To move from `k2-compat` to `qsdm-native`, QSDM Core needs API equivalents for:

- task metadata and source retrieval
- task migration execution and richer stake delegation UX
- task-runner proof verification and auditor approval rules
- CELL faucet or funding flow
- task marketplace cache and private task lookup
