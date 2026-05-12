# Wallet API — Operator Runbook

Three-mode runbook for the `/api/v1/wallet/*` HTTP surface.
Catches the operationally-relevant failure modes on the wallet
endpoints: handler-side error rate, storage-backend wedge,
and admin mint burst (supply-inflation tripwire).

| Alert | Severity | Default `for:` | Anchor |
|---|---|---|---|
| `QSDMWalletSendErrorRate`        | warning | 10m | [§3.1](#31-mode-a--qsdmwalletsenderrorrate)        |
| `QSDMWalletStorageErrorBurst`    | warning | 5m  | [§3.2](#32-mode-b--qsdmwalletstorageerrorburst)    |
| `QSDMWalletMintBurst`            | warning | 30m | [§3.3](#33-mode-c--qsdmwalletmintburst)            |

> **What this runbook closes.** Before this commit the wallet
> HTTP surface had only *gate-side* observability —
> `qsdm_submesh_api_wallet_reject_*_total` (submesh policy
> rejects) and `qsdm_p2p_wallet_ingress_dedupe_skip_total`
> (p2p dedupe). The handler-side failure paths
> (validation errors, `tx_create_failed`, `store_failed`,
> `no_wallet_service`, `nvidia_lock_blocked`) were
> log-only — an operator could not see a wedged storage
> backend, a missing wallet service init, or a perpetually-
> blocking NVIDIA-lock from Prometheus alone. The new
> `qsdm_wallet_send_total{result=...}` /
> `qsdm_wallet_balance_query_total{result=...}` /
> `qsdm_wallet_mint_total{result=...}` /
> `qsdm_wallet_create_total{result=...}` counters
> (`pkg/monitoring/wallet_metrics.go`) plus the three alerts
> in this runbook close that gap.

---

## 1. Glossary (60-second skim)

- **Handler-side** vs **gate-side**: a wallet request fails
  on the *handler* side when the wallet code's own logic
  rejects (validation, tx-create, store, missing wallet
  service, NVIDIA-lock), and on the *gate* side when an
  upstream policy gate (submesh-policy enforcement, p2p
  dedupe) rejects before the handler even sees the body.
  This runbook covers handler-side; gate-side lives in
  [`SUBMESH_POLICY_INCIDENT.md`](SUBMESH_POLICY_INCIDENT.md)
  and the dedupe is currently log-only (no dedicated alert).
- **Per-result counters**: every state-changing wallet
  endpoint emits a counter labelled by the terminal outcome
  it took. The label values are stable strings (defined as
  Go constants in `pkg/monitoring/wallet_metrics.go`):
  - `qsdm_wallet_send_total{result=...}`: success /
    invalid_request / unauthenticated / nvidia_lock_blocked /
    no_wallet_service / tx_create_failed / store_failed.
  - `qsdm_wallet_balance_query_total{result=...}`: success /
    storage_error / no_wallet_service.
  - `qsdm_wallet_mint_total{result=...}`: success /
    admin_rejected / invalid_request / store_failed /
    no_wallet_service.
  - `qsdm_wallet_create_total{result=...}`: success / failed.
- **`$CELL`** — the main coin minted by `/api/v1/wallet/mint`.
  Mint is admin-only and gated by NVIDIA-lock; sustained
  successful mint volume regardless of policy is the
  supply-inflation signal Mode C catches.

---

## 2. Pre-flight: identify the failing endpoint

```promql
topk(3, sum by (result) (rate(qsdm_wallet_send_total{result!="success"}[5m])))
topk(3, sum by (result) (rate(qsdm_wallet_balance_query_total{result!="success"}[5m])))
topk(3, sum by (result) (rate(qsdm_wallet_mint_total{result!="success"}[5m])))
```

The dominant `result` tag tells you the failure mode and
forks the runbook into the right mode below.

---

## 3. Per-mode triage

### 3.1 Mode A — `QSDMWalletSendErrorRate`

**Severity:** warning. **Default `for:`** 10m.

**Fires when**: handler-side failure ratio (i.e.
`tx_create_failed` + `store_failed` + `no_wallet_service` +
`nvidia_lock_blocked`) divided by total `qsdm_wallet_send_total`
exceeds 10% for ≥10m.

**Does NOT include** submesh-policy or dedupe rejects —
those have their own counters and runbooks.

**Triage**:

1. **Drill into the dominant failure tag**:
   ```promql
   topk(3, sum by (result) (rate(qsdm_wallet_send_total{result!="success",instance="$instance"}[5m])))
   ```
2. **Tag → root cause → fork**:
   - `tx_create_failed`: wallet service init failed at
     boot. Cross-check `qsdm_stub_active{kind="wallet"} == 1`
     (non-CGO build → wallet stub) and
     `qsdm_stub_active{kind="dilithium"} == 1` (no liboqs).
     Fix: rebuild with `CGO_ENABLED=1` and liboqs available.
     See [`STUB_DEPLOYMENT_INCIDENT.md` § kind-wallet](STUB_DEPLOYMENT_INCIDENT.md#kind-wallet)
     and [§ kind-dilithium](STUB_DEPLOYMENT_INCIDENT.md#kind-dilithium).
   - `store_failed`: storage backend is wedged on writes.
     Mode B ([§3.2](#32-mode-b--qsdmwalletstorageerrorburst))
     is the dedicated alert; check whether
     `QSDMNoTransactionsStored` is also firing (storage
     wedge is system-wide, not wallet-specific). See
     [`OPERATOR_HYGIENE_INCIDENT.md` §3.4](OPERATOR_HYGIENE_INCIDENT.md#34-mode-d--qsdmnotransactionsstored).
   - `no_wallet_service`: the wallet didn't initialize at
     node boot. Check startup logs for liboqs / OpenSSL /
     CGO errors. Same root cause as `tx_create_failed`
     above, just observable on a different code path.
   - `nvidia_lock_blocked`: NVIDIA-lock proof ring is empty
     or the gate is misconfigured. Cross-check
     `qsdm_nvidia_lock_http_blocks_total` rate (rapid
     increase = ring empty) and the NGC submission gate
     in [`NGC_SUBMISSION_INCIDENT.md`](NGC_SUBMISSION_INCIDENT.md).
3. **Persistent vs transient**: the alert is `for:10m` so
   the burst must be sustained — a single bad client
   spamming `/send` for 8 minutes won't page. If the alert
   clears on its own within 10m of trigger, the cause is
   likely a single bad client (apply rate-limiting at the
   API gate or the libp2p layer).

**Companions:**
[`STUB_DEPLOYMENT_INCIDENT.md`](STUB_DEPLOYMENT_INCIDENT.md)
(if root cause is wallet-stub or dilithium-stub),
[`OPERATOR_HYGIENE_INCIDENT.md`](OPERATOR_HYGIENE_INCIDENT.md)
(if root cause is storage wedge),
[`NGC_SUBMISSION_INCIDENT.md`](NGC_SUBMISSION_INCIDENT.md)
(if root cause is empty NVIDIA-lock proof ring),
[`SUBMESH_POLICY_INCIDENT.md`](SUBMESH_POLICY_INCIDENT.md)
(gate-side rejects, complementary).

---

### 3.2 Mode B — `QSDMWalletStorageErrorBurst`

**Severity:** warning. **Default `for:`** 5m.

**Fires when**: combined rate of
`qsdm_wallet_balance_query_total{result="storage_error"}` +
`qsdm_wallet_send_total{result="store_failed"}` exceeds
1 per minute for ≥5m on a single instance.

**Triage**:

1. **Confirm system-wide vs wallet-only**:
   - System-wide storage wedge: `QSDMNoTransactionsStored`
     fires concurrently. Fix is at the storage-backend
     layer (BoltDB / Badger), not in the wallet.
   - Wallet-only: only the wallet-side counters increment.
     Indicates the wallet's `storage` interface is
     mis-wired or the per-endpoint code path is bugged.
     Check the validator's startup wiring of
     `NewHandlers(...)` in `pkg/api/server.go` —
     specifically the `storage` argument.
2. **Block-production followthrough**:
   - If storage stays wedged, `QSDMMiningChainStuck` will
     fire next ([`MINING_LIVENESS.md` §3.1](MINING_LIVENESS.md#31-mode-a--qsdmminingchainstuck)).
     Fix the storage wedge before chain liveness degrades.
3. **Inspect logs**: search for
   `"Failed to get balance"` and `"Failed to store transaction"`
   on the validator's stdout. The underlying error
   (filesystem, lock, corruption) is in the slog `error`
   field.

**Companions:**
[`OPERATOR_HYGIENE_INCIDENT.md`](OPERATOR_HYGIENE_INCIDENT.md)
(system-wide storage wedge),
[`MINING_LIVENESS.md`](MINING_LIVENESS.md)
(downstream chain-stall risk).

---

### 3.3 Mode C — `QSDMWalletMintBurst`

> **v0.3.3 status (Session 91): this alert is now a
> regression tripwire, not an active-incident detector.**
> `/api/v1/wallet/mint` was removed in v0.3.3 and always
> returns HTTP 410 Gone; the `result="success"` tag no
> longer increments on a v0.3.3+ node. The alert is kept
> in `alerts_qsdm.example.yml` so that a future code
> regression (or a manual revert) which restores the
> never-credited mint stub gets caught at the 30m
> threshold. Operators tracking misconfigured callers
> that still target the removed endpoint should watch
> `rate(qsdm_wallet_mint_total{result="gone"}[5m])`
> instead. The triage steps below apply to the legacy
> pre-v0.3.3 posture.

**Severity:** warning. **Default `for:`** 30m.

**Fires when**: `qsdm_wallet_mint_total{result="success"}`
rate exceeds 5/min sustained for ≥30m (≥150 successful
mints in the window).

**Why this is a tripwire**:
`/api/v1/wallet/mint` mints `$CELL` (the main coin) and is
admin-only. It's gated by NVIDIA-lock and submesh
privileged-payload policy, so unauthorized callers can't
even reach the storage step. But sustained *successful*
mint volume by an authorized caller is itself suspicious —
either:

- **Legitimate cause**: an admin running a test campaign
  without notifying ops. Confirmable out-of-band by
  asking the operator who owns the API credentials.
- **Adversarial cause**: compromised admin credentials.
  Rotate, restrict, audit recipient addresses against the
  legitimate-recipient list.

**Triage**:

1. **List the mint amounts and recipients**:
   - Validator stdout: `grep '"coin":"CELL"'` filters the
     mint log lines. Fields: `amount`, `recipient`, `tx_id`.
2. **Cross-check governance**:
   - `QSDMGovAuthorityVoteRecorded` (info) — if a
     governance vote authorized the campaign, the vote
     payload is in the chain.
3. **Recipient audit**:
   - Compare each `recipient` against the legitimate-
     recipient list. Any address NOT on the list is a
     credential-compromise red flag.
4. **Remediate**:
   - **Legitimate**: silence the alert per-deployment
     for the campaign window with an Alertmanager
     silence covering the right `instance` label.
   - **Compromise**: rotate the admin credential, restrict
     the IP ACL on `/api/v1/wallet/mint`, file an incident
     report. Subsequent unauthorized mints will register
     as `result="admin_rejected"` rather than `success`.

**Companions:**
[`GOVERNANCE_AUTHORITY_INCIDENT.md`](GOVERNANCE_AUTHORITY_INCIDENT.md)
(if a legitimate authority vote drove the mint),
[`OPERATOR_HYGIENE_INCIDENT.md`](OPERATOR_HYGIENE_INCIDENT.md)
(if NVIDIA-lock is mis-configured and admin path is open).

---

## 4. Cross-references

- `pkg/monitoring/wallet_metrics.go` — per-result counter
  definitions and the `qsdm_wallet_*_total` exposition.
- `pkg/api/handlers.go` — wallet endpoint handlers
  instrumented with `monitoring.RecordWalletXxx(...)` at
  every terminal point.
- `QSDM/deploy/prometheus/alerts_qsdm.example.yml` —
  `qsdm-wallet` group with the three alerts.
- `QSDM/deploy/grafana/dashboards/qsdm-runbook-wallet-incident.json`
  — auto-generated panel.
- [`STUB_DEPLOYMENT_INCIDENT.md`](STUB_DEPLOYMENT_INCIDENT.md)
  (`kind="wallet"`, `kind="dilithium"`),
  [`OPERATOR_HYGIENE_INCIDENT.md`](OPERATOR_HYGIENE_INCIDENT.md)
  (storage wedge),
  [`NGC_SUBMISSION_INCIDENT.md`](NGC_SUBMISSION_INCIDENT.md)
  (NVIDIA-lock proof ring),
  [`SUBMESH_POLICY_INCIDENT.md`](SUBMESH_POLICY_INCIDENT.md)
  (gate-side rejects),
  [`GOVERNANCE_AUTHORITY_INCIDENT.md`](GOVERNANCE_AUTHORITY_INCIDENT.md)
  (mint authorization audit trail),
  [`MINING_LIVENESS.md`](MINING_LIVENESS.md)
  (downstream chain-stall risk after storage wedge).
