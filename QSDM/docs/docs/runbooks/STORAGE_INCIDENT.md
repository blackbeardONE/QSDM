# Storage Backend — Operator Runbook

Two-mode runbook for the storage layer (SQLite / FileStorage /
Scylla). Mode A catches sustained write-error bursts (storage
rejecting transaction writes); Mode B is the lowest-level
health-probe signal — `Ready()` itself failing — which is
critical because the validator cannot meaningfully participate
in consensus without a working storage backend.

| Alert | Severity | Default `for:` | Anchor |
|---|---|---|---|
| `QSDMStorageWriteErrorBurst` | warning      | 5m | [§3.1](#31-mode-a--qsdmstoragewriteerrorburst) |
| `QSDMStorageReadyFailing`    | **critical** | 2m | [§3.2](#32-mode-b--qsdmstoragereadyfailing)    |

> **What this runbook closes.** Before this commit, the SQLite
> backend's `StoreTransaction` had no Prometheus instrumentation
> at all — a write failure was log-only. The legacy
> `monitoring.RecordStorageOperation` hook covered GetBalance /
> UpdateBalance / SetBalance but was exposed only in the
> `/api/metrics` JSON map, not in the OpenMetrics scrape used
> for alerting. The new `qsdm_storage_op_total{op,result}`
> counter (`pkg/monitoring/storage_op_metrics.go`) plus
> instrumentation in `sqlite.go`, `file_storage.go`, and
> `scylla.go` close that gap.

---

## 1. Glossary (60-second skim)

- **Storage backend** — one of three implementations of the
  storage interface used by the validator:
  - `pkg/storage/sqlite.go` (CGO, default). Per-row encrypted
    + zstd-compressed transaction blobs in a single SQLite
    file with WAL mode.
  - `pkg/storage/file_storage.go` (no-CGO fallback). One file
    per transaction, no balance tracking.
  - `pkg/storage/scylla.go` (production). Scylla cluster with
    LWT-based dedupe and per-keyspace partition layout.
- **`qsdm_storage_op_total{op, result}`** — per-(operation,
  result) counter emitted at the storage call sites. `op` ∈
  `{store_transaction, get_balance, update_balance,
    set_balance, ready}`. `result` ∈ `{success, error}`. All
  10 (op, result) pairs are pre-populated at value 0 so the
  alert query never has missing-data on cold-start nodes.
- **`Ready()`** — the storage interface's health probe. Called
  by `/api/v1/health`, by the wallet handler at every send,
  and by the metrics-check on scrape. A `Ready()` failure
  means the backend is fully offline.
- **Companion alerts** — storage failures show up at multiple
  layers in the operational stack:
  - `QSDMNoTransactionsStored` (in `qsdm-throughput`) fires
    when ZERO transactions complete on the entire node — the
    aggregate-throughput sentinel.
  - `QSDMWalletStorageErrorBurst` (in `qsdm-wallet`) fires
    when the *wallet API surface* sees storage failures
    end-to-end.
  - The two alerts in this runbook fire from the storage
    layer itself, regardless of which API surface caused
    the call.

---

## 2. Pre-flight: confirm which op is failing

```promql
topk(3, sum by (op) (rate(qsdm_storage_op_total{result="error"}[5m])))
```

The dominant `op` tag tells you which call site is failing
and forks the runbook into the right mode below.

---

## 3. Per-mode triage

### 3.1 Mode A — `QSDMStorageWriteErrorBurst`

**Severity:** warning. **Default `for:`** 5m.

**Fires when**: `qsdm_storage_op_total{op="store_transaction",result="error"}`
rate exceeds 1/min sustained for ≥5m.

**Why this matters**: write failures mean the chain can't
accept new state. If sustained, follows into
`QSDMNoTransactionsStored` and `QSDMMiningChainStuck`.

**Triage**:

1. **Confirm the magnitude**:
   ```promql
   rate(qsdm_storage_op_total{op="store_transaction",result="error"}[5m])
   /
   rate(qsdm_storage_op_total{op="store_transaction"}[5m])
   ```
   - Ratio close to 1: the backend is fully wedged on writes.
     Mode B (`QSDMStorageReadyFailing`) likely co-fires.
   - Ratio between 0.05 and 0.5: partial wedge — lock
     contention, intermittent disk pressure, transient Scylla
     quorum loss. Investigate but the chain is still
     making forward progress.
2. **Cross-check the throughput sentinel**:
   - `QSDMNoTransactionsStored` firing concurrently → full
     storage wedge; the chain has stopped accepting state.
     Page level escalates: see
     [`OPERATOR_HYGIENE_INCIDENT.md` §3.4](OPERATOR_HYGIENE_INCIDENT.md#34-mode-d--qsdmnotransactionsstored)
     and [`MINING_LIVENESS.md` §3.1](MINING_LIVENESS.md#31-mode-a--qsdmminingchainstuck).
   - Not firing → some writes are still succeeding; this is
     a partial-degradation signal, not a stall.
3. **Cross-check the wallet surface**:
   - `QSDMWalletStorageErrorBurst` firing concurrently → the
     wallet API surface is the source of the failed writes
     (consistent with end-to-end visibility).
   - Not firing but Mode A is → the failed writes are coming
     from p2p ingress (libp2p accepting txs that storage
     then rejects). Inspect
     `qsdm_p2p_wallet_ingress_dedupe_skip_total` and
     submesh-policy reject counters to characterize the
     traffic.
4. **Inspect logs**: search the validator's stdout for
   storage error patterns:
   - SQLite: `"database is locked"`, `"disk I/O error"`,
     `"no space left"`.
   - FileStorage: `"failed to write transaction file"`,
     `"no space left on device"`.
   - Scylla: `"WriteTimeoutException"`, `"NoHostAvailable"`,
     `"OperationTimedOut"`.

**Companions:**
[`OPERATOR_HYGIENE_INCIDENT.md`](OPERATOR_HYGIENE_INCIDENT.md)
(when Mode A escalates to full storage wedge),
[`MINING_LIVENESS.md`](MINING_LIVENESS.md)
(downstream chain-stall risk),
[`WALLET_INCIDENT.md`](WALLET_INCIDENT.md)
(wallet API surface symptom of the same failure class),
[`STUB_DEPLOYMENT_INCIDENT.md`](STUB_DEPLOYMENT_INCIDENT.md)
(in extreme cases — `kind="poe"` accepting unsigned txs
that the chain can't validate, indirectly stressing
storage).

---

### 3.2 Mode B — `QSDMStorageReadyFailing`

**Severity:** critical. **Default `for:`** 2m.

**Fires when**: `qsdm_storage_op_total{op="ready",result="error"}`
rate is non-zero for ≥2m.

**Why this matters**: `Ready()` is the lowest-level health
probe. A failure means the backend is reporting itself
fully offline. The validator cannot meaningfully
participate in consensus without a working storage
backend, so this is the storage equivalent of "hard down."

**Triage**:

1. **Confirm the underlying backend is reachable**:
   - SQLite: SSH to the node, `ls -la <db-path>` (the path
     is in the validator's startup config). Check disk
     space, file permissions, and FS mount status.
   - FileStorage: `ls -la <storage-dir>`. Verify the
     directory exists, is writable, and has free space.
   - Scylla: from the node, run `cqlsh <host>` (or your
     cluster's preferred client) — failure here means the
     validator can't reach the cluster, success means the
     cluster is fine but the validator-side session is
     broken.
2. **Identify the proximate cause**:
   - **Backend alive but unreachable from this node**: DNS
     resolution failure, TLS cert expired, auth
     credentials rotated, firewall change, network
     partition. Restart the validator with corrected
     config (or fix the network).
   - **Backend itself dead**: file deleted, FS unmounted,
     Scylla cluster down. Restore from backup or wait for
     the cluster to recover.
3. **Mitigate downstream load-balancer behaviour**:
   - `/api/v1/health` returns 503 while `Ready()` fails,
     so a properly-configured LB will (correctly) take
     this node out of rotation. If the LB is NOT taking
     it out, fix the LB's healthcheck wiring — running
     a partially-broken validator behind a healthy LB is
     worse than letting traffic spread to healthy peers.
4. **Anticipate the cascade**:
   - `QSDMNoTransactionsStored` will fire within ~30m if
     this stays unresolved.
   - `QSDMMiningChainStuck` will fire if a majority of
     validators hit Mode B concurrently (e.g. shared
     Scylla cluster outage).

**Companions:**
[`MINING_LIVENESS.md`](MINING_LIVENESS.md)
(downstream consensus stall),
[`OPERATOR_HYGIENE_INCIDENT.md`](OPERATOR_HYGIENE_INCIDENT.md)
(`QSDMNoTransactionsStored` follows within 30m),
[`WALLET_INCIDENT.md`](WALLET_INCIDENT.md)
(API surface symptoms once `Ready()` flips to error),
[`QUARANTINE_INCIDENT.md`](QUARANTINE_INCIDENT.md)
(if a majority hit this — submesh isolation behaviour
will follow).

---

## 4. Cross-references

- `pkg/monitoring/storage_op_metrics.go` — per-(op, result)
  counter definitions and the `qsdm_storage_op_total`
  exposition.
- `pkg/storage/sqlite.go`, `pkg/storage/file_storage.go`,
  `pkg/storage/scylla.go` — storage backend implementations
  with `monitoring.RecordStorageOp(...)` instrumentation at
  every terminal point.
- `QSDM/deploy/prometheus/alerts_qsdm.example.yml` —
  `qsdm-storage` group with the two alerts.
- `QSDM/deploy/grafana/dashboards/qsdm-runbook-storage-incident.json`
  — auto-generated panel.
- [`OPERATOR_HYGIENE_INCIDENT.md`](OPERATOR_HYGIENE_INCIDENT.md)
  (`QSDMNoTransactionsStored` aggregate-throughput
  sentinel; cross-fires with both modes when storage
  is fully wedged).
- [`MINING_LIVENESS.md`](MINING_LIVENESS.md)
  (`QSDMMiningChainStuck` is the downstream consensus-stall
  signal when storage stays broken).
- [`WALLET_INCIDENT.md`](WALLET_INCIDENT.md)
  (`QSDMWalletStorageErrorBurst` is the wallet-API-surface
  symptom of the same failure class).
- [`QUARANTINE_INCIDENT.md`](QUARANTINE_INCIDENT.md)
  (when a majority of validators hit Mode B together,
  submesh isolation follows).
