# Smart Contracts + Atomic-Swap Bridge — Operator Runbook

Two-mode runbook covering both the WASM contract execution
path (`pkg/contracts.ContractEngine`) and the atomic-swap
bridge protocol (`pkg/bridge.BridgeProtocol`). Both
subsystems carry user-driven error volume that's not
necessarily a system fault, so thresholds are conservative
and tuned to catch *sustained dominant-error patterns*
rather than absolute error counts.

| Alert | Severity | Default `for:` | Anchor |
|---|---|---|---|
| `QSDMContractExecuteErrorRate` | warning | 15m | [§3.1](#31-mode-a--qsdmcontractexecuteerrorrate) |
| `QSDMBridgeOpErrorBurst`       | warning | 10m | [§3.2](#32-mode-b--qsdmbridgeoperrorburst)       |

> **What this runbook closes.** Both `pkg/contracts` and
> `pkg/bridge` had **zero** Prometheus instrumentation
> before this commit. Contract gas-exhaustion failures, a
> WASM runtime regression, a stuck bridge lock, or a flood
> of invalid-secret redemption attempts were all log-only.
> The new `qsdm_contract_executions_total{result}` and
> `qsdm_bridge_op_total{op,result}` counters
> (`pkg/monitoring/contracts_bridge_metrics.go`) close that
> gap with a 2-row + 6-row exposition surface, all rows
> pre-populated at 0.

---

## 1. Glossary (60-second skim)

- **`ContractEngine.ExecuteContract`** — the single entry
  point for WASM contract calls. Routes to (in priority
  order): per-contract wazero runtime → shared wazero
  runtime → wasmSDK adapter → simulation fallback. A
  `result="error"` outcome can come from any of those
  layers.
- **`BridgeProtocol`** — three state-changing methods:
  - `LockAsset(...)` — generates a secret + hash, locks
    funds on the source chain, advances the lock to
    `LockStatusLocked`.
  - `RedeemAsset(lockID, secret)` — accepts the secret,
    verifies its hash, advances the lock to
    `LockStatusRedeemed`.
  - `RefundAsset(lockID)` — accepts a post-expiry
    recovery, advances the lock to `LockStatusRefunded`.
- **`qsdm_contract_executions_total{result}`** — counter,
  `result ∈ {success, error}`. Both rows pre-populated at
  0 so cold-start nodes don't have missing-data on alert
  evaluation.
- **`qsdm_bridge_op_total{op, result}`** — counter,
  `op ∈ {lock, redeem, refund}`, `result ∈ {success, error}`.
  All 6 (op, result) rows pre-populated at 0.

---

## 2. Pre-flight: which subsystem is dominating the errors?

```promql
sum(rate(qsdm_contract_executions_total{result="error"}[15m]))
sum(rate(qsdm_bridge_op_total{result="error"}[10m]))
```

The dominant subsystem forks the runbook into the right
mode below.

---

## 3. Per-mode triage

### 3.1 Mode A — `QSDMContractExecuteErrorRate`

**Severity:** warning. **Default `for:`** 15m.

**Fires when**: contract execution **error ratio > 50%**
sustained for ≥15m AND the call rate is at least 1/min
(filters out the "rarely-used contract that errored once"
case).

**Why this matters**: a sustained dominant-error contract
execution rate indicates a systematic failure rather than
isolated user errors:
- WASM runtime regression (wazero per-contract or shared
  runtime broken).
- Gas envelope misconfigured — the majority of calls
  exhausting their limit.
- ABI / codec drift after a deploy: callers passing args
  that the contract no longer accepts.
- Simulation-fallback path producing errors that the
  real WASM runtime would not have produced.

**Triage**:

1. **Inspect node logs** for `"function not found"`,
   `"gas exhausted"`, or runtime panic stacktraces. The
   dominant error class points at the failing layer.
2. **Cross-check the WASM-SDK stub sentinel**:
   ```promql
   qsdm_stub_active{kind="wasm_sdk"} == 1
   ```
   - Firing → the WASM SDK stub is active, which can
     produce systematic errors when the wazero priority
     chain falls through. See
     [`STUB_DEPLOYMENT_INCIDENT.md` §kind-wasm-sdk](STUB_DEPLOYMENT_INCIDENT.md#kind-wasm-sdk).
   - Not firing → the failure is in real WASM execution,
     gas, or ABI.
3. **Concentration check**: if the failure is concentrated
   to a single contract (drill via app-layer logs or
   ContractEngine.executions map), that contract has
   shipped a broken update. Consider rolling it back via
   the upgrade path in `pkg/contracts/upgrade.go`.
4. **Gas envelope check**: inspect
   `ContractEngine.gasConfig.DefaultLimit` and
   `MaxLimit` against the contracts in question. A
   default-limit shrink is a common deploy footgun.

**Companions:**
[`STUB_DEPLOYMENT_INCIDENT.md`](STUB_DEPLOYMENT_INCIDENT.md)
(`kind="wasm_sdk"` is the upstream sentinel when the
fallback path is producing the errors).

---

### 3.2 Mode B — `QSDMBridgeOpErrorBurst`

**Severity:** warning. **Default `for:`** 10m.

**Fires when**: any bridge op (lock / redeem / refund) is
producing errors at > 0.2/min sustained for ≥10m.

**Why this matters**: each bridge op represents a real
cross-chain action and a sustained error rate has direct
economic impact — most concerningly, locked funds that
won't refund.

**Triage**:

1. **Drill by op tag**:
   ```promql
   topk(3, sum by (op) (rate(qsdm_bridge_op_total{result="error"}[10m])))
   ```
   Each op has a distinct interpretation:

2. **Per-op interpretation**:
   - **`op="lock"`**: secret generation or lock-state
     setup is failing. Inspect `generateSecret()` and
     `generateLockID()` (both in
     `pkg/bridge/protocol.go`). Common causes: source-
     chain RPC unreachable (in a real deploy), or the
     `bp.locks` map state is wedged (concurrent map race —
     check the mutex usage if you're seeing a flap
     pattern).
   - **`op="redeem"`**: invalid-secret submissions OR
     cross-chain proof failures. **Cross-reference**:
     - `qsdm_p2p_messages_total` rate is *low* + redeem
       errors are *high* → likely adversarial spam from
       a small set of attackers.
     - `qsdm_p2p_messages_total` rate is *normal* + redeem
       errors are *high* → cross-chain proof verification
       is failing systematically (check the target-chain
       client config).
   - **`op="refund"`** (highest stakes): post-expiry
     recovery is failing — locks aren't transitioning to
     `LockStatusRefunded`. **Funds are stuck.**
     Investigate immediately:
     - Check that `time.Now().Before(lock.ExpiresAt)` is
       evaluating correctly — clock drift between the
       node and the rest of the network can keep refunds
       blocked indefinitely.
     - Inspect the lock map for stuck rows (lock_id with
       `LockStatusLocked` and ExpiresAt long past).
     - If the issue is widespread (multiple instances
       firing), there's a bug in the refund eligibility
       gate. Roll back the most recent bridge protocol
       change.

3. **Cross-network check**: this alert is per-instance.
   If a majority of validators are firing concurrently,
   the cause is a deploy-side regression rather than a
   single-host issue. Roll back.

**Companions:**
[`MINING_LIVENESS.md`](MINING_LIVENESS.md)
(downstream chain-stall risk if bridge ops are the
dominant transaction class on a chain that's relying
on bridge volume),
[`OPERATOR_HYGIENE_INCIDENT.md`](OPERATOR_HYGIENE_INCIDENT.md)
(`QSDMNoTransactionsStored` will follow if bridge
errors are accompanied by a storage wedge — though
that's a coincidence rather than a causal chain).

---

## 4. Cross-references

- `pkg/monitoring/contracts_bridge_metrics.go` — counter
  definitions, `RecordContractExecution`,
  `RecordBridgeOp`, and the
  `contractsBridgePrometheusMetrics` collector.
- `pkg/contracts/engine.go` —
  `ContractEngine.ExecuteContract` is instrumented via a
  named-return `defer` that flips
  `qsdm_contract_executions_total{result=...}` at every
  termination point.
- `pkg/bridge/protocol.go` — `LockAsset`, `RedeemAsset`,
  `RefundAsset` are each instrumented via a named-return
  `defer` that flips
  `qsdm_bridge_op_total{op=..., result=...}`.
- `QSDM/deploy/prometheus/alerts_qsdm.example.yml` —
  `qsdm-contracts-bridge` group.
- `QSDM/deploy/grafana/dashboards/qsdm-runbook-contracts-bridge-incident.json`
  — auto-generated panel.
- [`STUB_DEPLOYMENT_INCIDENT.md`](STUB_DEPLOYMENT_INCIDENT.md)
  (`kind="wasm_sdk"` is the upstream sentinel when the
  fallback path is producing contract execution errors).
- [`MINING_LIVENESS.md`](MINING_LIVENESS.md)
  (downstream chain-stall risk).
- [`OPERATOR_HYGIENE_INCIDENT.md`](OPERATOR_HYGIENE_INCIDENT.md)
  (`QSDMNoTransactionsStored` may co-fire if bridge
  errors are accompanied by a storage-layer issue).
