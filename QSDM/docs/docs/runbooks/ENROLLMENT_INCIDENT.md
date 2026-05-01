# v2-Mining Enrollment Incident — Operator Runbook

Triage flow for the 5 alerts in the
`qsdm-v2-mining-enrollment` group:

| Alert | Severity | Default `for:` | Anchor |
|---|---|---|---|
| `QSDMMiningRegistryEmpty`              | warning | 15m | [§3.1](#31-mode-a--qsdmminingregistryempty) |
| `QSDMMiningRegistryShrinkingFast`      | warning | 10m | [§3.2](#32-mode-b--qsdmminingregistryshrinkingfast) |
| `QSDMMiningPendingUnbondMajority`      | warning | 30m | [§3.3](#33-mode-c--qsdmminingpendingunbondmajority) |
| `QSDMMiningEnrollmentRejectionsBurst`  | warning | 5m  | [§3.4](#34-mode-d--qsdmminingenrollmentrejectionsburst) |
| `QSDMMiningBondedDustDropped`          | warning | 10m | [§3.5](#35-mode-e--qsdmminingbondeddustdropped) |

Companion dashboard tile: **🪪 Enrollment Registry** at
`/dashboard` → `/api/mining/enrollment-overview`. The tile's
counter strip (`active miners` / `bonded dust` / `pending unbond` /
`enroll / unenroll` / `enroll rejected` / `unenroll rejected`) is
the same series the alerts evaluate, so an operator looking at
the dashboard at the moment an alert fires should see the
matching cell turn red or amber.

> **Why a registry-only runbook?** The on-chain enrollment
> registry is the chain's economic-safety thermometer — it
> answers "who is currently slashable, and how much stake is
> at risk?". A misread here can mean the cluster pages on a
> false-empty registry (operator panic) or, conversely, that
> a cluster *should* be paging on a coordinated exit and
> isn't. Triage steps below assume the alert manager is
> alive; if Prometheus itself is silent, escalate to the
> liveness runbook (`QSDMMiningChainStuck`) first.

---

## 1. Glossary (60-second skim)

- **Active record** — `EnrollmentRecord` with `RevokedAtHeight == 0`.
  Owner is staked and slashable. Surfaced as `qsdm_enrollment_active_count`.
- **Pending unbond** — record with `RevokedAtHeight != 0` AND
  `StakeDust > 0`. The 7-day cooldown is in flight; the bond is
  *still* slashable and the stake is *still* locked, but no new
  attestations from this NodeID will count. Surfaced as
  `qsdm_enrollment_pending_unbond_count`.
- **Revoked** — record with `StakeDust == 0`. Either fully
  slashed or already swept; no longer slashable, no longer
  bonded. Stays in the registry for audit purposes; not
  counted in either gauge.
- **Sweep** — `SweepMaturedUnbonds` reads matured pending-unbond
  records and credits the owner balance, dropping the record's
  `StakeDust` to 0. Surfaced as
  `qsdm_enrollment_unbond_swept_total` (monotonic counter).
- **Bonded dust** — sum of `StakeDust` across all *active*
  records. The economic backbone of the slasher's collateral
  pool. Surfaced as `qsdm_enrollment_bonded_dust`.
- **dust → CELL** — divide by 1e9. The dashboard tile shows
  CELL; PromQL queries below show dust.

---

## 2. First-90-seconds checklist

Regardless of which mode fired:

1. **Open the dashboard tile.** `/dashboard` → 🪪 Enrollment
   Registry. Read the counter strip top-to-bottom; the cell
   colours pre-classify the incident.

2. **Confirm the alert is real, not a scrape gap.** Check the
   `qsdm_process_uptime_seconds` cell — if the Prometheus
   target was just bouncing (process restart), gauges briefly
   read zero before the `EnrollmentStateProvider` reattaches
   in `internal/v2wiring`. Wait one scrape interval before
   triaging.

3. **Cross-reference the chain liveness alerts.** If
   `QSDMMiningChainStuck` is firing in the same window, the
   block producer is wedged and the registry simply isn't
   advancing. Triage the producer first; the registry will
   recover on its own.

4. **Identify the dominant signal.** The mode tables below
   call out the cells that should change first; if the
   "expected first-mover" cell is calm, you're probably
   looking at a false alert (drift between Prometheus
   recording rules and the live gauges, or a label
   mismatch).

5. **Pause the dashboard tile** with the ⏸ button if you need
   to read a row mid-incident. Other tiles keep ticking.

---

## 3. Modes

### 3.1. Mode A — `QSDMMiningRegistryEmpty`

`qsdm_enrollment_active_count == 0` for 15m on a node with
`qsdm_process_uptime_seconds > 600`.

#### Symptoms (dashboard)

- 🪪 Enrollment Registry → `active miners` cell **red, value 0**.
- `bonded dust` cell **0 CELL**.
- Records table empty (or shows only `revoked` / `pending_unbond` rows).

#### Symptoms (Prometheus)

```promql
qsdm_enrollment_active_count == 0
qsdm_enrollment_bonded_dust  == 0
```

#### Triage

```promql
# Did everyone unenroll legitimately in the last hour?
rate(qsdm_unenrollment_applied_total[1h])

# Or did the admission gate start rejecting every enroll?
sum by (reason) (rate(qsdm_enrollment_rejected_total[1h]))

# Or has the producer stopped sealing entirely?
delta(qsdm_chain_height[5m])
```

| Signal | Probable cause | Action |
|---|---|---|
| `unenrollment_applied` rate >> 0 in last hour | Coordinated exit (release regression, driver rollout, governance unrest) | See Mode B below — the same coordinated-exit pattern but caught earlier in time |
| `enrollment_rejected` rate >> 0 by `malformed_payload` / `wrong_contract` | Client-server skew after a release | Pin minimum client version in MINER_QUICKSTART; revert the contract change if mid-deploy |
| `enrollment_rejected` rate >> 0 by `gpu_uuid_collision` / `node_id_bound` | Re-enrollment churn (fleet wipe + re-onboard) | Confirm with fleet operators; usually self-resolves once the unbond window closes |
| `delta(chain_height[5m]) == 0` | Producer wedged | Switch to `QSDMMiningChainStuck` runbook; gauges will recover when block production resumes |

#### Mitigation

- **Testnet:** acceptable during quiet windows. Suppress with a
  longer `for:` (30m–1h) or scope the alert to mainnet
  instances only.
- **Mainnet:** production v2 mainnet must never sit at
  `active=0`. Treat as a P2 incident; if the chain is
  advancing AND admission isn't rejecting, the alert is
  catching a real coordinated exit and operations should
  reach out to the miner community.

---

### 3.2. Mode B — `QSDMMiningRegistryShrinkingFast`

`qsdm_enrollment_active_count` fell by >25% in 1h on a node
that started with ≥4 active records.

#### Symptoms (dashboard)

- `active miners` cell value drops sharply across two refreshes
  (2 s polling makes the slope visible).
- `pending unbond` cell *climbs* if the exits are voluntary
  (operator unenrolls go through the unbond window).
- `pending unbond` cell stays flat if the exits are forced
  (slasher auto-revokes drain bonds directly without parking
  them in pending_unbond).

#### Symptoms (Prometheus)

```promql
(
  (qsdm_enrollment_active_count offset 1h) - qsdm_enrollment_active_count
) / (qsdm_enrollment_active_count offset 1h) > 0.25
and (qsdm_enrollment_active_count offset 1h) >= 4
```

#### Triage

```promql
# Voluntary vs. forced decomposition.
rate(qsdm_unenrollment_applied_total[1h])      # voluntary
rate(qsdm_slash_applied_total[1h])             # forced
rate(qsdm_enrollment_unbond_swept_total[1h])   # final-stage

# Reject reasons rising at the same time as exits = release regression.
sum by (reason) (rate(qsdm_enrollment_rejected_total[1h]))
```

| Driver dominant | Probable cause | Action |
|---|---|---|
| Voluntary (unenroll rate up) | Release regression / driver rollout / governance unrest | Confirm with fleet operators; freeze the release if a regression; coordinate with miner community |
| Forced (slash rate up) | Coordinated cheat-ring detected by slasher; **switch to SLASHING_INCIDENT.md** Mode B | Triage on the slashing runbook — this alert is collateral signal |
| Sweep (unbond_swept rate up) | Old unbond windows maturing in batch — usually after an exit-burst from earlier | Backwards-look at the prior week's `unenrollment_applied` rate to confirm |

#### Mitigation

- **Voluntary exit:** publish the release-regression status
  page; if the regression is real, push a rollback build with
  v2-mining still enabled and ask miners to re-enroll. A new
  enrollment carries fresh bond — owners that already exited
  must wait for their unbond window to close before
  re-enrolling unless they want to bond fresh dust.
- **Forced exit:** **page the slashing on-call**. A
  coordinated cheat-ring at this scale is a P1; do not let
  the slasher continue draining without a verifier rollback
  decision (see SLASHING_INCIDENT.md §4).

---

### 3.3. Mode C — `QSDMMiningPendingUnbondMajority`

More than half of the bonded population is in
pending_unbond for 30m, and the population has at least 4
records.

#### Symptoms (dashboard)

- `pending unbond` cell **red** (>50%) or amber (>25%).
- `bonded dust` cell drops alongside as records hit the
  unbond window (active stake exits the bonded pool the
  moment `RevokedAtHeight` is set).

#### Symptoms (Prometheus)

```promql
(
  qsdm_enrollment_pending_unbond_count
  /
  (qsdm_enrollment_active_count + qsdm_enrollment_pending_unbond_count)
) > 0.5
```

#### Triage

```promql
# How much CELL is locked in unbond windows right now?
qsdm_enrollment_pending_unbond_dust / 1e9
qsdm_enrollment_bonded_dust         / 1e9

# When did each record enter pending_unbond? Page by NodeID:
GET /api/mining/enrollment-overview?phase=pending_unbond&limit=200
```

This mode is almost always a **leading indicator** of a
larger incident — operators don't randomly all click unenroll
within 30m. The triage decision is *which* incident is
upstream:

| Concurrent signal | Probable cause |
|---|---|
| Slashing rate >> 0 | Coordinated cheat-ring (forced exits parked in pending_unbond before sweep). Switch to SLASHING_INCIDENT.md |
| `qsdm_consensus_*` byzantine alerts | Pre-fork-vote staging; miners exit ahead of governance lock |
| Release announcement in last 24h | Driver/binary regression |
| External incident (NVIDIA driver rollout, AWS/GCP outage) | Hardware unreachability triggers an exit cascade |

#### Mitigation

- **Communicate before the 7-day window closes.** Once
  matured, the unbond sweeps and the bond returns to owner
  balance. Re-enrollment after that requires fresh dust —
  miners who hesitate on rollback may not be able to
  re-bond at the same scale.
- If the upstream cause is a release regression, push a
  rollback build with v2-mining still active.

---

### 3.4. Mode D — `QSDMMiningEnrollmentRejectionsBurst`

`sum(increase(qsdm_enrollment_rejected_total[10m])) >= 20`
for 5m.

#### Symptoms (dashboard)

- `enroll rejected` cell **amber** with a non-trivial
  per-reason breakdown in the detail line (e.g.
  `malformed_payload:14 · stake_mismatch:6`).

#### Symptoms (Prometheus)

```promql
sum(increase(qsdm_enrollment_rejected_total[10m])) >= 20
```

#### Triage

The alert collapses by-reason; first thing to do is **break
out the dominant rejection** so the cause is unambiguous.

```promql
sum by (reason) (increase(qsdm_enrollment_rejected_total[10m]))
```

| Dominant `reason` | Cause | Action |
|---|---|---|
| `insufficient_balance` | Client offered <10 CELL bond | Operator-side: top up; chain-side no action |
| `gpu_uuid_collision` | Two NodeIDs claimed the same GPU UUID | Almost always a misconfigured fleet — same GPU re-imaged with a different NodeID without unenrolling first; instruct operator to unenroll the old NodeID and wait for the unbond window |
| `node_id_bound` | Re-enroll without unbond first | Same as above — old record still active |
| `decode_failed` | Old client / canonicaljson drift | Pin minimum client version in MINER_QUICKSTART |
| `wrong_contract` | Client targeting a stale contract address (chain ID mismatch on multi-net deployments) | Confirm chain-id pinning in client config |
| `fee_invalid` | Client offered fee below floor / wrong currency | Usually a custom-tooling issue; cross-reference the fee-floor recording rule |
| `admission_failed` | Catch-all admission gate rejection (mempool quota, signature failure, etc.) | Read `admission_failed` log lines for the specific reject path |
| `other` | Unmapped reason — should never dominate | If `other` >> 0, suspect a NEW rejection path was added without a tag; file a ticket |

#### Mitigation

- A burst of `decode_failed` after a release is the canonical
  client-server-skew signature. Roll the client minimum-version
  pin in `MINER_QUICKSTART.md`; the client maintainers will
  push.
- Sustained `node_id_bound` / `gpu_uuid_collision` is usually
  *operator confusion* about the unenroll → unbond → re-enroll
  flow. Reach out individually rather than escalating.

---

### 3.5. Mode E — `QSDMMiningBondedDustDropped`

`(qsdm_enrollment_bonded_dust offset 30m) - qsdm_enrollment_bonded_dust > 5e10`
(>50 CELL drop in 30m, requires the prior reading to be non-zero).

#### Symptoms (dashboard)

- `bonded dust` cell drops by >50 CELL across the two
  scrape windows visible in the cell's detail line.
- `active miners` may *or may not* drop alongside —
  partial-stake slashes drain bond without removing the
  record, so a slash burst can drop bond without dropping
  the active count.

#### Symptoms (Prometheus)

```promql
(qsdm_enrollment_bonded_dust offset 30m) - qsdm_enrollment_bonded_dust > 5e10
```

#### Triage

```promql
# Voluntary exit drains bond AS unbond pressure climbs.
rate(qsdm_unenrollment_applied_total[30m])

# Slash drains bond and the dust ends up in reward + burn pools.
rate(qsdm_slash_drained_dust_total[30m])

# Sweeps return bond to owner balances; the gauge drop here
# is INTENDED behaviour (mature unbonds).
rate(qsdm_enrollment_unbond_swept_total[30m])
```

| Sum of the three | Conclusion |
|---|---|
| ≈ size of the gauge drop | Drop is fully accounted for; pick the dominant driver and triage from the sibling mode (B for unenroll, slashing runbook for slash, no action for sweep) |
| << size of the gauge drop | **Suspect a metric-callback regression.** The bonded-dust gauge reads through the live `EnrollmentStateProvider` callback installed by `internal/v2wiring`. A stale or detached provider will surface the wrong number. Verify by inspecting the callback's source (chain state) directly via `/api/v1/mining/enrollments?phase=active&limit=500` and summing `stake_dust`; if the API total differs from the gauge, file a P2 ticket — the alert is real but the cause is in the metrics path, not on-chain |

#### Mitigation

- Gauge regression: restart the affected node. The
  `EnrollmentStateProvider` is reattached at boot; a stale
  closure that survives the restart would be a far more
  serious bug worth a code-level investigation.
- Real drop driven by slashing: see SLASHING_INCIDENT.md §4
  for the verifier-rollback path including off-chain
  compensation if the bond loss was caused by a verifier
  regression rather than legitimate cheating.

---

## 4. Cross-mode escalation

If two or more enrollment alerts fire within the same 30m
window, escalate to a P2 incident. Common multi-fire patterns:

- **B + C + E (shrinking + pending-majority + bonded-drop):**
  textbook coordinated voluntary exit. Check release notes
  in the last 24h.
- **D + Mode B's voluntary branch:** client-server skew is
  rejecting incoming enrolls AND existing miners are leaving.
  Roll the release.
- **B's forced branch + E:** active slashing incident;
  switch to SLASHING_INCIDENT.md and treat enrollment alerts
  as collateral signal.
- **A alone after a quiet period:** probably a producer
  wedge masquerading as a registry incident. Cross-check
  `QSDMMiningChainStuck` first.

---

## 5. Recovery validation

After mitigation, confirm the registry is healthy:

```promql
# Active count climbing back?
delta(qsdm_enrollment_active_count[15m]) > 0

# Bonded dust climbing back?
delta(qsdm_enrollment_bonded_dust[15m]) > 0

# Reject rate back to baseline (typically <1/min in steady state)?
sum(rate(qsdm_enrollment_rejected_total[15m])) < 0.02
```

Dashboard cell colours back to green is the operator-facing
confirmation. The 5 alerts auto-clear once their `for:`
windows pass without the trigger condition holding.

---

## 6. Reference

- Counters / gauges: `pkg/monitoring/enrollment_metrics.go`.
- Snapshot view: `monitoring.EnrollmentMetricsSnapshot`.
- Dashboard handler: `internal/dashboard/enrollment.go`
  (`handleEnrollmentOverview`).
- Wire-shape contract:
  `internal/dashboard.dashboardEnrollmentOverviewView`.
- Alert rules: `deploy/prometheus/alerts_qsdm.example.yml` →
  `qsdm-v2-mining-enrollment` group.
- Companion runbooks:
  - SLASHING_INCIDENT.md (when forced-exit is the driver)
  - REJECTION_FLOOD.md (when admission-gate metrics overlap)
