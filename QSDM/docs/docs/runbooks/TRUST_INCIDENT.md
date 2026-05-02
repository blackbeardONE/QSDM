# Trust / NGC-Attestation Incident — Operator Runbook

Triage flow for the 6 alerts in the
`qsdm-trust-transparency` + `qsdm-trust-redundancy`
groups:

| Alert | Severity | Default `for:` | Anchor |
|---|---|---|---|
| `QSDMTrustNoAttestationsAccepted`     | warning      | 5m  | [§3.1](#31-mode-a--qsdmtrustnoattestationsaccepted) |
| `QSDMTrustIngestRejectRateElevated`   | warning      | 10m | [§3.2](#32-mode-b--qsdmtrustingestrejectrateelevated) |
| `QSDMTrustAttestationsBelowFloor`     | warning      | 10m | [§3.3](#33-mode-c--qsdmtrustattestationsbelowfloor) |
| `QSDMTrustNGCServiceDegraded`         | warning      | 10m | [§3.4](#34-mode-d--qsdmtrustngcservicedegraded) |
| `QSDMTrustLastAttestedStale`          | warning      | 5m  | [§3.5](#35-mode-e--qsdmtrustlastattestedstale) |
| `QSDMTrustAggregatorStale`            | **critical** | 2m  | [§3.6](#36-mode-f--qsdmtrustaggregatorstale) |

> **Why a trust-only runbook?** The trust pipeline is the
> chain's *reward gate*. Enrollment determines who CAN earn;
> trust determines whose attestations COUNT toward earning.
> A misread on these alerts is the difference between the
> chain paying out fairly and silently under-paying the
> honest fleet — and unlike slashing, the user-facing
> consequence (the qsdm.tech transparency badge flipping
> red) is visible to the entire community within minutes.
> Five of the six alerts fire **before** the badge flips,
> giving the operator a pre-warning window the runbook is
> designed to use.

Companion observability: the dashboard's existing **🛡️
Trust Panel** widget (`updateTrustPanel()` →
`/api/v1/trust/attestations/summary`) shows ratio /
status / last / window cells. The
`TrustMetricsCollector` in `pkg/api/trust_metrics.go`
exposes the same numbers as the seven `qsdm_trust_*`
gauges every alert here evaluates against. External
mirror: `.github/workflows/trustcheck-external.yml` runs
`cmd/trustcheck --min-attested 2` from outside the
cluster on a schedule — Mode C is the internal twin of
that probe.

---

## 1. Glossary (60-second skim)

- **TrustAggregator** — `pkg/api/handlers_trust.go`. Polls
  peer trust endpoints + local NGC ingest store every
  `refresh_interval` (default **10 s**) and caches the
  result. `Summary()` returns the cached snapshot in O(1)
  — every gauge in the trust collector reads through this
  cache.
- **NGC sidecar** — out-of-process attestation poster.
  Reads NVIDIA AT (Attestation Token) from the host,
  HMACs it under the operator's `QSDM_NGC_INGEST_SECRET`,
  and POSTs to `/api/v1/ngc/proof/ingest`. Three canonical
  sidecars per node: Windows scheduled task, VPS systemd
  timer, OCI cron job.
- **`fresh_within`** — aggregator config (default **15
  minutes**). An attestation is "fresh" if its
  `LastAttestedAt` is within this window. Ageing out of
  this window is what flips the source from contributing
  to `attested` to silently dropping.
- **`attested`** — count of distinct attestation sources
  currently fresh. The chain's redundancy floor.
- **`total_public`** — denominator: public-validator-set
  size + 1 (local node). Used by the Ratio gauge.
- **`ngc_service_status`** enum — `healthy` / `degraded`
  / `outage`. Set by the aggregator's classification of
  proof-arrival cadence; flips before `attested` hits 0
  in a slow-death scenario.
- **`qsdm_trust_warm`** — aggregator-level boot flag.
  Goes from 0 to 1 after the first full Refresh()
  completes. Modes C/D/E/F gate on `warm == 1` so a
  redeploy doesn't page during the ~10 s warm-up.
  Modes A/B do *not* gate on warm — they're counter-rate
  based and a brand-new node with no sidecars wired up
  legitimately fires Mode A starting at minute 25.
- **External CI mirror** — `cmd/trustcheck --min-attested
  2` runs from a GitHub-Actions schedule. The internal
  Mode C alert and the external CI assert the same
  condition; CI green + Mode C firing internally is a
  hint that Prometheus, not the trust pipeline, is the
  problem.

---

## 2. First-90-seconds checklist

Regardless of which mode fired:

1. **Open the Trust Panel widget** on the dashboard. The
   four cells (ratio / status / last / window) are the
   exact values the alerts evaluate. If the panel reads
   `disabled` or `warming up…`, treat the alert as a
   redeploy-window false-positive and wait one
   `refresh_interval` (10 s) before triaging further.

2. **Visit qsdm.tech/trust.html (or the local equivalent)**
   if the alert is in `qsdm-trust-redundancy`. The
   transparency pill is what users see; if the pill is
   green while Mode C/D/E is firing internally, the
   alert is catching the slow-death pre-warning window
   the runbook is designed to use — act *before* the
   pill flips.

3. **Cross-reference the external trustcheck CI status.**
   `.github/workflows/trustcheck-external.yml`. If the
   external probe is failing AND Mode C is firing
   internally, the trust pipeline genuinely has <2
   sources. If external is failing but no Mode C
   internally, the local node's metric path is broken
   (collector wedged, scrape config, etc.). Conversely
   if external is green but Mode C is firing, suspect a
   Prometheus-target issue on the affected node.

4. **Tail the NGC ingest logs.** `journalctl -u qsdm
   --since "20 minutes ago" | grep -i "ngc/proof/ingest"`.
   Look for repeated `hmac_mismatch` /
   `invalid_nonce` / `unauthorized` lines — the
   highest-frequency reject reason names the cause for
   Modes A/B almost always.

5. **Suppress noise: Mode F is upstream of all others.**
   If `QSDMTrustAggregatorStale` (critical) is firing,
   the aggregator goroutine is wedged. Modes A/B may
   still fire correctly (they're driven by NGC ingest
   counters, which are independent), but Modes C/D/E
   become collateral signal — gauges read stale because
   nothing is updating them, not because the underlying
   data changed. Triage Mode F first.

---

## 3. Modes

### 3.1. Mode A — `QSDMTrustNoAttestationsAccepted`

`sum(rate(qsdm_ngc_proof_ingest_accepted_total[20m])) == 0`
for 5m. Severity: warning. **No `warm` gate.**

#### Symptoms

- Trust Panel: `last` cell shows a timestamp >20 min
  old; `status` likely `degraded` or `outage` (often
  fires alongside Mode D).
- Prometheus: `qsdm_ngc_proof_ingest_accepted_total` flat
  for 20 min.
- qsdm.tech badge will be amber/red within minutes if
  not already.

#### Triage

```promql
# Confirm the absence is not just an "accepted" gap
# that's actually being rejected:
sum(rate(qsdm_ngc_proof_ingest_rejected_total[20m]))
sum by (reason) (rate(qsdm_ngc_proof_ingest_rejected_total[20m]))

# Is QSDM_NGC_INGEST_SECRET unset on this node?
# (when unset, the ingest route is disabled; the counter
# stays pinned at 0 by design)
curl -s http://127.0.0.1:8080/api/v1/trust/attestations/summary | jq .
```

| Reject rate | Probable cause | Action |
|---|---|---|
| Zero (no rejections either) | No sidecar is posting *anything*. Either the scheduled task / systemd timer / cron stopped, the host is offline, or `QSDM_NGC_INGEST_SECRET` is unset (route disabled — `ingest_disabled` reason) | Check sidecar status; verify the secret is set on the node. See [`NGC_SUBMISSION_INCIDENT.md` §3.2](NGC_SUBMISSION_INCIDENT.md#32-mode-b--qsdmngcproofingestrejectburst) "Class 4 — config" for the silence-vs-fix decision |
| Non-zero, `unauthorized` dominant | Probe / scan; legitimate sidecars are also down | Same as above; the unauthorized rejects are noise |
| Non-zero, `hmac` / `nonce` dominant | Sidecar IS posting but the signed bundle is rejected | Promote to Mode B (the same incident from the rejection-side view); secret rotated on one side. See [`NGC_SUBMISSION_INCIDENT.md`](NGC_SUBMISSION_INCIDENT.md#32-mode-b--qsdmngcproofingestrejectburst) for the per-reason cause table |

#### Mitigation

- **Sidecar stopped:** restart the scheduler/timer/cron
  on the affected host. The sidecar's first POST after
  restart will re-warm `attested` within one
  `fresh_within` cycle.
- **Secret unset (disabled route):** `[trust]` may be
  configured for an environment where ingest isn't
  expected (read-only watcher node). If this is a
  production validator, confirm `QSDM_NGC_INGEST_SECRET`
  is in the env file the systemd unit reads from — a
  common bug is the secret in the operator's interactive
  shell but not the unit env.
- **Host offline:** unrelated to this runbook — fix the
  host via the ops on-call's normal procedure.

---

### 3.2. Mode B — `QSDMTrustIngestRejectRateElevated`

`rejected_rate > 1 + accepted_rate` over 10 min, for 10m.
Severity: warning. **No `warm` gate.**

#### Symptoms

- Sidecars are posting (rejected_total IS climbing —
  this distinguishes from Mode A's silence).
- `accepted_total` is climbing slowly or zero.
- The `+1` constant in the threshold means single
  one-off probes don't fire this alert — sustained
  imbalance does.

#### Triage

```promql
# The dominant reject reason is the cause:
sum by (reason) (rate(qsdm_ngc_proof_ingest_rejected_total[10m]))
```

The closed-enum `reason` set is the same as
[`NGC_SUBMISSION_INCIDENT.md`](NGC_SUBMISSION_INCIDENT.md#32-mode-b--qsdmngcproofingestrejectburst)
Mode B (the per-request gate runbook), which
carries the **authoritative reason → cause + action
table for all nine reasons**. Below is the
trust-side abridged view; deep-link to the gate
runbook for the full triage matrix and mitigation
classes.

| Dominant `reason` | Cause | Action |
|---|---|---|
| `hmac` | `QSDM_NGC_INGEST_SECRET` rotated on one side, mismatch between sidecar config and node config | See [`NGC_SUBMISSION_INCIDENT.md` §3.2](NGC_SUBMISSION_INCIDENT.md#32-mode-b--qsdmngcproofingestrejectburst) "Class 1 — auth/secret drift" mitigation |
| `nonce` | Replay-nonce expired in flight (slow network) OR sidecar's clock has drifted out of the freshness window OR the nonce-pool overflowed | See [`NGC_SUBMISSION_INCIDENT.md` §3.2](NGC_SUBMISSION_INCIDENT.md#32-mode-b--qsdmngcproofingestrejectburst) "Class 3 — replay/timing" mitigation |
| `unauthorized` | Third party probing the endpoint OR a misconfigured sidecar lost its auth header | Add the source IP to firewall block list (see gate runbook §3.2 source-distribution audit) |
| `body_too_large` / `invalid_json` / `missing_cuda_hash` | Sidecar/operator-tooling regression after a release | See [`NGC_SUBMISSION_INCIDENT.md` §3.2](NGC_SUBMISSION_INCIDENT.md#32-mode-b--qsdmngcproofingestrejectburst) "Class 2 — payload shape" mitigation; pin sidecar minimum version in MINER_QUICKSTART |
| `ingest_disabled` | `QSDM_NGC_INGEST_SECRET` is unset on this validator — ingest endpoint returns 404 by design. **Not a sidecar fault.** | If this validator is supposed to be a trust peer, set the env. If not, silence Mode B on this instance |
| `body_read` / `other` | Network flap / unmapped failure | See gate runbook §3.2 |

#### Mitigation

- **Secret rotation:** the operationally most-common
  trigger. Pick one side as the canonical secret source
  (usually the validator's env file in
  `/etc/qsdm/qsdm.env`) and re-key all sidecars from
  there. Rotation should be a coordinated push, not
  side-by-side updates. The gate runbook documents the
  validator-accepts-both transition window pattern
  for safe rotations.
- **Clock skew:** force NTP sync on sidecars; if the
  fleet drifts repeatedly, audit which timer service
  the host runs (Windows Time / `chronyd` / `systemd-
  timesyncd`).
- **For all other reasons:** [`NGC_SUBMISSION_INCIDENT.md`
  §3.2](NGC_SUBMISSION_INCIDENT.md#32-mode-b--qsdmngcproofingestrejectburst)
  is the authoritative gate-side runbook — TRUST is
  the *aggregate response*, NGC_SUBMISSION is the
  *per-request gate*. Fix the gate; trust auto-clears
  5–20 min later as the aggregator re-warms.

---

### 3.3. Mode C — `QSDMTrustAttestationsBelowFloor`

`qsdm_trust_warm == 1 and qsdm_trust_attested < 2`
for 10m. Severity: warning.

#### Symptoms

- Trust Panel: ratio cell shows `0 of N` or `1 of N`.
- The external `cmd/trustcheck --min-attested 2` CI
  probe will fail on its next schedule.
- qsdm.tech transparency pill amber.

#### Triage

The aggregator deduplicates by `LocalDistinctAttestationSource`
— two sidecars sharing a `QSDM_NGC_PROOF_NODE_ID` collapse
to ONE source, so `attested<2` doesn't necessarily mean
"only one sidecar is alive". Check the source list:

```bash
curl -s http://127.0.0.1:8080/api/v1/trust/attestations/summary \
  | jq '.attestations[]'
```

| Observed | Cause | Action |
|---|---|---|
| 1 source listed | One of the sidecars (Windows / VPS / OCI) is offline or its timer stopped | Restart the affected sidecar |
| 2+ sources but `attested<2` | Sources are listed but one or more aged out of `fresh_within` (15 min default) | Promote to Mode E; the sources are alive but stale |
| 2+ sources, all fresh, `attested<2` | `LocalDistinctAttestationSource` collision — two sidecars use the same NodeID | Set distinct `QSDM_NGC_PROOF_NODE_ID` per host; restart sidecars |
| Zero sources | Mode A is upstream — no proofs are arriving at all | Switch to Mode A triage |

#### Mitigation

- **Sidecar restart** is the canonical fix when one side
  is offline. The next post will register; `attested`
  re-counts within one `refresh_interval` (10 s).
- **NodeID collision** is a config-drift bug — usually
  introduced by copy-pasting an env file across hosts
  without editing the NodeID. Audit the sidecar env
  files; a host should never share NodeID with another.

#### Why this alert is gated on `warm == 1`

`Summary()` returns zero values during the first
Refresh() tick after a restart. Without the gate, every
redeploy would fire Mode C for ~10 s. The gate is
**not** a delay — it's a "the aggregator has actually
sampled the trust state at least once" guarantee.

---

### 3.4. Mode D — `QSDMTrustNGCServiceDegraded`

`qsdm_trust_warm == 1 and qsdm_trust_ngc_service_healthy == 0`
for 10m. Severity: warning.

#### Symptoms

- Trust Panel: `status` cell shows `degraded` or
  `outage`.
- Often the **first** alert in a slow-death progression
  — the aggregator classifies `ngc_service_status`
  conservatively, so it flips before `attested` hits 0.

#### Triage

```promql
# What's the underlying drift driving the classification?
rate(qsdm_ngc_proof_ingest_accepted_total[15m])
sum by (reason) (rate(qsdm_ngc_proof_ingest_rejected_total[15m]))
```

The `ngc_service_status` enum is:

- `healthy`: cadence within expected, no significant
  reject rate.
- `degraded`: arrivals slowing or rejection rate
  climbing — pre-warning.
- `outage`: arrivals stopped or fully rejected — the
  user-facing pill flips red.

Mode D fires for both `degraded` and `outage`. Use the
Trust Panel's `status` cell to distinguish.

#### Mitigation

- Mode D rarely needs its own mitigation — it's almost
  always a leading signal for Modes A/B/C/E. Identify
  the upstream cause and triage that mode; Mode D
  auto-clears once the underlying classifier flips back
  to `healthy`.

---

### 3.5. Mode E — `QSDMTrustLastAttestedStale`

`qsdm_trust_warm == 1 and last_attested_seconds > 0
and time() - last_attested_seconds > 1800` for 5m.
Severity: warning.

#### Symptoms

- Trust Panel: `last` cell shows a timestamp 30+
  minutes old.
- The `>0` guard prevents a false fire during
  warm-up (when `last_attested_seconds == 0`).
- `attested` may still be ≥2 — Mode E is the
  *belt-and-braces* alert that catches "sources
  exist on paper but the freshest one is too old to
  trust".

#### Triage

The 30-minute threshold is twice `fresh_within`. The
intent: alert *halfway through* the degradation window
so an operator has 30 minutes before the source ages
out completely and `attested` drops, triggering Mode C.

```promql
# Newest attestation age:
time() - qsdm_trust_last_attested_seconds

# Are NEW attestations arriving but only being rejected?
rate(qsdm_ngc_proof_ingest_accepted_total[15m])
rate(qsdm_ngc_proof_ingest_rejected_total[15m])
```

| Newer attempts arriving | Conclusion |
|---|---|
| Yes, accepts climbing | The newest sample is fresh but the aggregator isn't picking it up — verify sidecar `LocalDistinctAttestationSource` matches what the aggregator expects |
| Yes, rejects climbing | Cross-reference Mode B; reject reason is the cause |
| No | Cross-reference Mode A; sidecars are not posting at all |

#### Mitigation

- Same as upstream mode (A or B). Mode E is downstream
  signal.

---

### 3.6. Mode F — `QSDMTrustAggregatorStale`

`qsdm_trust_warm == 1 and last_checked_seconds > 0
and time() - last_checked_seconds > 120` for 2m.
Severity: **critical**.

#### Symptoms

- `qsdm_trust_last_checked_seconds` not advancing —
  the Refresh() ticker is wedged.
- Trust Panel cells freeze at the values they had at
  the moment of the wedge; subsequent NGC ingest posts
  may still be flowing but they don't reach the
  Summary() cache.
- Modes A/B may continue to behave normally (they're
  driven by `qsdm_ngc_proof_ingest_*` counters, which
  are independent of the aggregator).
- Modes C/D/E become **collateral signal** — they read
  stale gauges; the data underneath them may be
  perfectly fine.

#### Why this is critical-severity

Refresh() runs every `refresh_interval` (default 10 s).
The 120 s threshold = **12+ missed ticks in a row**.
That's not a slow tick or a GC pause; that's a code-
level wedge:

- Aggregator's peer-provider RPC is blocked on a peer
  that never responds AND the timeout knob isn't tight
  enough.
- Storage read on the local NGC ingest store is
  blocking (disk hardware fault, fsync stall).
- A goroutine deadlock in the aggregator's lock
  hierarchy.

The user-facing transparency endpoint (`/api/v1/trust/
attestations/summary`) goes stale from the user's
perspective regardless of how many proofs are arriving
underneath; the qsdm.tech badge can stay green while
the pipeline is silently broken — and that's the worst
class of trust failure (false confidence).

#### Triage

```bash
# Capture goroutine stacks before restarting:
curl -s http://127.0.0.1:8080/debug/pprof/goroutine?debug=1 \
  > /tmp/qsdm-trust-stuck-$(date +%s).txt

# Tail logs for the affected window:
journalctl -u qsdm --since "10 minutes ago" \
  | grep -iE "trust|aggregator|refresh"
```

Look for:
- `Refresh() context deadline exceeded` — the
  peer-provider RPC is blocking; likely a peer is
  unreachable AND the `peer_timeout_sec` in
  `qsdm.toml` is too high.
- `read tcp ... i/o timeout` — same shape.
- *No log lines at all in the trust subsystem during
  the wedge window* — strongly suggests a goroutine
  deadlock; the goroutine dump is the only way to see
  the lock hierarchy.

#### Mitigation

- **Capture state first** (goroutine dump above).
  Restarting without capturing loses the diagnostic
  trail and the wedge will likely recur after the
  next ineligible peer joins the gossip set.
- **Restart the validator.** The aggregator
  reconstructs its state from the local NGC ingest
  store on boot; a clean restart resumes Refresh()
  ticks within `refresh_interval`.
- **If it recurs after restart**, the cause is
  reproducible — file a P1 ticket with the goroutine
  dump. Until the underlying bug is fixed, an
  operator-side mitigation is to tighten
  `peer_timeout_sec` in `qsdm.toml` (default is
  generous; halving it usually surfaces the
  unreachable peer as a logged timeout rather than a
  silent block).

---

## 4. Cascade map

A real trust incident often progresses through 3–5
modes in a known order. Reading 4 simultaneous alerts
as 4 separate incidents wastes triage time; here's
the canonical progression so on-call can identify the
*root cause* immediately:

```
sidecar stops posting (or secret rotated)
        │
        ▼
   ┌────────────────────────────┐    NGC counter side:
   │ qsdm_ngc_proof_ingest_*    │    accepted flatlines
   │ counter rates flip         │    (or rejected climbs)
   └────────────────────────────┘
        │
        │ ~5 min   →  Mode A fires (if accepts gone)
        │ ~10 min  →  Mode B fires (if rejects climbing)
        │
        ▼
   ┌────────────────────────────┐    Aggregator's
   │ ngc_service_status flips   │    classifier flips
   │ from healthy to degraded   │    BEFORE the gauges
   └────────────────────────────┘    stop counting
        │
        │ ~10 min  →  Mode D fires
        │
        ▼
   ┌────────────────────────────┐    Newest sample
   │ last_attested_seconds      │    crosses 30m old
   │ ages past 30 min           │    (twice fresh_within)
   └────────────────────────────┘
        │
        │ ~5 min   →  Mode E fires
        │
        ▼
   ┌────────────────────────────┐    Source ages out
   │ attested drops below 2     │    of fresh_within
   │ (15 min)                   │
   └────────────────────────────┘
        │
        │ ~10 min  →  Mode C fires
        │             (qsdm.tech pill goes amber)
        │
        ▼
   ┌────────────────────────────┐    Aggregator's
   │ ngc_service_status →       │    classifier escalates
   │ "outage"                   │    (already in D)
   └────────────────────────────┘
        │
        │             user-facing badge flips RED
```

If you see all of A/D/E/C firing within ~40 min: this
is one incident with a single root cause (whatever
broke the sidecar). The chain of fires above is the
order; whatever fired first is the entry point.

If Mode F (`AggregatorStale`) fires alongside any
others: **F is the root cause**. The other modes are
reading stale gauges; their conditions may not even
be true on the live data, just on the cached snapshot
from the moment of the wedge. Triage F, then re-check
the others after the aggregator resumes.

---

## 5. Cross-mode escalation matrix

| Concurrent alerts | Most likely root | Action |
|---|---|---|
| A only | Sidecar stopped or `INGEST_SECRET` unset | Mode A triage |
| B only | Secret rotated, clock skew, or release skew | Mode B triage |
| A + D + (E later) + (C later) | Single sidecar / single secret incident progressing through the cascade | Find the failing sidecar; modes auto-clear once posting resumes |
| C alone (no A/B/D/E) | Source-list collision (NodeID dedup) | §3.3 cause-table row 3 |
| C alone, external CI green | Local Prometheus path broken, not a real trust incident | Validate scrape config |
| F alone or F+anything | Aggregator wedged | Mode F triage; **suppress the others as collateral** |
| E + Mode A flat | Older samples decayed; sidecar quiet but not crashed | Restart the sidecar; samples will refresh |
| All 6 | Aggregator wedge happened during a real sidecar incident — both pages legitimate | Triage F first, then triage A/B once the aggregator is alive again |

---

## 6. Recovery validation

After mitigation, confirm trust pipeline health:

```promql
# Accepts climbing again?
rate(qsdm_ngc_proof_ingest_accepted_total[5m]) > 0

# Aggregator ticking?
delta(qsdm_trust_last_checked_seconds[2m]) > 0

# Redundancy back above floor?
qsdm_trust_warm == 1 and qsdm_trust_attested >= 2

# Service status back to healthy?
qsdm_trust_ngc_service_healthy == 1

# Newest sample fresh?
(time() - qsdm_trust_last_attested_seconds) < 900
```

Expect the qsdm.tech transparency badge to flip green
within one `fresh_within` window (15 min) once the
underlying cause is fixed. If the badge stays amber
after metrics confirm recovery, suspect a CDN-cache
miss on the static page; the pill state is computed
client-side from the JSON endpoint, so a hard browser
refresh resolves stale render.

External CI (`trustcheck-external.yml`) runs on a
schedule; the next run after metrics-side recovery
will turn green and confirm the fix from outside.

---

## 7. Reference

- **Source files:**
  - [`pkg/api/handlers_trust.go`](../../../source/pkg/api/handlers_trust.go)
    — TrustAggregator + Refresh() loop
  - [`pkg/api/trust_metrics.go`](../../../source/pkg/api/trust_metrics.go)
    — Prometheus collector for the seven `qsdm_trust_*`
    gauges
  - [`pkg/api/handlers.go`](../../../source/pkg/api/handlers.go)
    — `/api/v1/ngc/proof/ingest` POST and
    `/api/v1/trust/attestations/summary` GET
  - [`cmd/trustcheck/main.go`](../../../source/cmd/trustcheck/main.go)
    — external CI probe (the `--min-attested 2` mirror)
- **API endpoints:**
  - `POST /api/v1/ngc/proof/ingest` — sidecar posts AT bundles
  - `GET  /api/v1/trust/attestations/summary` — public
    transparency JSON (powers qsdm.tech badge AND the
    dashboard's Trust Panel widget)
- **Prometheus series:**
  - `qsdm_ngc_proof_ingest_accepted_total{}` — accepted bundles
  - `qsdm_ngc_proof_ingest_rejected_total{reason=...}` — rejected, by reason
  - `qsdm_trust_attested` — count of fresh sources
  - `qsdm_trust_total_public` — denominator
  - `qsdm_trust_ratio` — attested / total_public
  - `qsdm_trust_ngc_service_healthy` — 1 iff status="healthy"
  - `qsdm_trust_last_attested_seconds` — newest sample timestamp
  - `qsdm_trust_last_checked_seconds` — last Refresh() tick
  - `qsdm_trust_warm` — aggregator boot flag
- **Closed-enum values:**
  - `ngc_service_status`: `healthy`, `degraded`,
    `outage`
  - Reject reasons (9 values; authoritative
    definition in
    [`pkg/monitoring/ngc_ingest_metrics.go`](../../../source/pkg/monitoring/ngc_ingest_metrics.go)):
    `ingest_disabled`, `unauthorized`, `body_read`,
    `body_too_large`, `invalid_json`,
    `missing_cuda_hash`, `nonce`, `hmac`, `other`.
    See
    [`NGC_SUBMISSION_INCIDENT.md`](NGC_SUBMISSION_INCIDENT.md#32-mode-b--qsdmngcproofingestrejectburst)
    for the per-reason cause + action table.
- **Companion runbooks:**
  - [`NGC_SUBMISSION_INCIDENT.md`](NGC_SUBMISSION_INCIDENT.md)
    — the *upstream cause* runbook. NGC submission
    is the per-request gate (challenge issuance +
    proof ingest); trust here is the aggregate
    response. Sustained `QSDMNGCProofIngestRejectBurst`
    is the canonical upstream cause for Modes A/B/D
    here; the gate runbook's §3.2 is the
    authoritative reason → cause + action table for
    all nine reject reasons.
  - [`MINING_LIVENESS.md`](MINING_LIVENESS.md) —
    aggregator wedges and producer wedges share the
    same "downstream silence is collateral signal"
    pattern; the playbooks are sibling shapes.
  - [`SLASHING_INCIDENT.md`](SLASHING_INCIDENT.md) —
    if trust degradation is *caused* by miners
    submitting spoofed AT bundles (a separate attack
    class from operational sidecar failure), the
    forged-attestation slashing path is the
    enforcement counterpart.
- **External CI:**
  [`.github/workflows/trustcheck-external.yml`](../../../../.github/workflows/trustcheck-external.yml)

---

## 8. Alert ↔ Mode quick-reference

| Alert                              | Mode | Severity | Triage section |
| ---------------------------------- | ---- | -------- | -------------- |
| `QSDMTrustNoAttestationsAccepted`  | A    | warning  | §3.1           |
| `QSDMTrustIngestRejectRateElevated`| B    | warning  | §3.2           |
| `QSDMTrustAttestationsBelowFloor`  | C    | warning  | §3.3           |
| `QSDMTrustNGCServiceDegraded`      | D    | warning  | §3.4           |
| `QSDMTrustLastAttestedStale`       | E    | warning  | §3.5           |
| `QSDMTrustAggregatorStale`         | F    | **critical** | §3.6     |

---

*Maintained alongside [`alerts_qsdm.example.yml`](../../../deploy/prometheus/alerts_qsdm.example.yml).
If you change a threshold there, update the "what
triggers" wording here in the same commit.*
