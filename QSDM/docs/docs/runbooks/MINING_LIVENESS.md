# v2-Mining Chain Liveness — Operator Runbook

Triage flow for the 2 alerts in the
`qsdm-v2-mining-liveness` group:

| Alert | Severity | Default `for:` | Anchor |
|---|---|---|---|
| `QSDMMiningChainStuck`     | **critical** | 3m  | [§3.1](#31-mode-a--qsdmminingchainstuck) |
| `QSDMMiningMempoolBacklog` | warning      | 10m | [§3.2](#32-mode-b--qsdmminingmempoolbacklog) |

> **Why a liveness-only runbook?** These two alerts are
> *upstream* of every other v2-mining alert. The block
> producer is the consumer of the slash, enrollment, and
> attestation pipelines — when it stalls, the
> `qsdm_slash_*` / `qsdm_enrollment_*` /
> `qsdm_attest_*` counters all flatline simultaneously.
> An on-call paged on `QSDMMiningSlashApplied` or
> `QSDMMiningRegistryEmpty` *during* a producer wedge
> will read those tiles as healthy ("no slashes for 1h" /
> "registry empty") even though nothing is healthy. The
> first job in any v2-mining incident is to confirm the
> chain is advancing; this runbook is that confirmation.

Companion observability surfaces: the **📊 System Metrics**
tile on the dashboard shows `qsdm_chain_height`,
`qsdm_mempool_size`, and `qsdm_validators_active` directly.
The mining-specific tiles (🪪 Enrollment Registry, ⚖️
Slashing Pipeline) cross-reference back to this runbook
when the upstream wedge is the suspected cause of their own
collateral signal.

---

## 1. Glossary (60-second skim)

- **`qsdm_chain_height`** — monotonic gauge, the block
  height of the most recent block this node has applied.
  Healthy node: increments every block-time (production
  block-time is seconds).
- **`qsdm_mempool_size`** — point-in-time gauge of the
  pending-tx queue. Healthy node: oscillates around a
  small number (typically <100 in steady state); spikes
  during burst submission and drains within seconds.
- **`qsdm_validators_active`** — point-in-time gauge of
  validators participating in consensus. The quorum
  threshold is consensus-config-dependent; below quorum,
  no block is sealable.
- **`qsdm_process_uptime_seconds`** — gates both alerts so
  a fresh boot or a Prometheus target bounce doesn't
  page during the warm-up window.
- **`ProduceBlock`** — the block-producer entry point
  (`pkg/blockchain/...`). The "all transactions failed
  state application" path is the silent freeze vector
  this runbook spends most of its triage on.
- **Admission gate** — stateless validation a tx passes
  before entering the mempool (`AdmissionCheckers` in
  `pkg/mining/...`).
- **Applier** — stateful validation at consensus time
  (`ApplySlashTx` / `ApplyEnrollTx` / etc.). Applier and
  admission can disagree because admission is stateless;
  divergence is what the mempool-backlog alert is
  designed to catch.

---

## 2. First-90-seconds checklist

Regardless of which mode fired:

1. **Confirm the alert is real, not a scrape gap.** Both
   alerts are gated on `qsdm_process_uptime_seconds > 300`,
   so a process restart can't fire them; but a Prometheus
   target bounce can briefly read stale metrics. Wait one
   scrape interval before triaging.

2. **Read the dashboard's three core gauges in order.**
   `qsdm_chain_height` first — is it advancing across two
   refreshes? `qsdm_mempool_size` second — what's the trend?
   `qsdm_validators_active` third — quorum?

3. **Suppress the noise.** Every other v2-mining alert is
   collateral signal during a chain-stuck incident. If
   `QSDMMiningChainStuck` is firing, treat
   `QSDMMiningRegistryEmpty` / `QSDMMiningSlashApplied`
   silence as **expected** — the producer can't apply the
   counters that drive those alerts. Don't burn cycles on
   the downstream tiles until liveness is restored.

4. **Tail the producer logs.** `ProduceBlock` errors are
   the most operationally common cause of Mode A; the
   "all transactions failed state application" path is the
   silent-freeze vector — see Mode A §3.1 below.

5. **Do not restart the validator yet.** A restart that
   doesn't address the root cause will silently re-wedge
   on the next block; gather the diagnostic state first.

---

## 3. Modes

### 3.1. Mode A — `QSDMMiningChainStuck`

`delta(qsdm_chain_height[5m]) == 0` for 3m on a node up
>5m. Severity: **critical**.

#### Symptoms (dashboard / Prometheus)

- `qsdm_chain_height` **flat** across two scrape windows.
- `qsdm_mempool_size` likely **growing** (admission is
  still accepting; the producer is the bottleneck).
- All v2-mining tiles (🪪 Enrollment, ⚖️ Slashing,
  📋 Recent Rejections) read as "no recent activity" —
  silence is the signal here, not a healthy state.
- Every `qsdm_*_applied_total` counter has a flat
  derivative; every `*_rate` query returns 0.

#### Triage

```promql
# Step 1: Mempool growing or empty?
delta(qsdm_mempool_size[5m])

# Step 2: Quorum?
qsdm_validators_active

# Step 3: Are admission rejections climbing?
sum(rate(qsdm_slash_rejected_total[5m]))
sum(rate(qsdm_enrollment_rejected_total[5m]))
```

| Mempool trend | Quorum | Probable cause |
|---|---|---|
| Empty (≈ 0) | Yes | Validator never built a block — clock skew, `MaxClockSkewSec` violation, or the producer is idle waiting for a tx that never arrives |
| Empty (≈ 0) | No (below quorum) | Quorum collapse — can't seal a block until validators rejoin; check P2P + consensus alerts |
| Growing | Yes | "All transactions failed state application" — every tx in the candidate block is rejected by the applier and the producer silently doesn't seal. **THIS IS THE COMMON CAUSE.** |
| Growing | No | Quorum AND backpressure — investigate the consensus subsystem first; backlog is a downstream consequence |

#### The "all transactions failed" silent-freeze path

This is the operationally most-frequent cause of Mode A
and the reason Mining tile silence isn't trustworthy as a
liveness signal.

`ProduceBlock` collects N candidate txs from the mempool,
runs them through their respective `Apply*` paths in a
trial-state transaction, and seals a block IFF at least
one tx applied. When *every* candidate fails state
application, the producer:

1. Logs `ProduceBlock: all transactions failed state application`.
2. Returns a non-block result.
3. **Does not seal**.
4. Mempool is unchanged — the same N txs will be
   re-attempted next round, with the same outcome.

The producer is now in a stable freeze: the failing txs
won't drain, and other txs queue behind them (the mempool
is FIFO-ish; producer drains in order).

This is most often triggered by a **release skew** —
admission is stateless and forwards tx versions the new
applier rejects; or by a **state corruption** that makes
the applier reject every tx that touches a poisoned
account.

#### Triage commands

```bash
# Find the freeze-log line:
journalctl -u qsdm --since "10 minutes ago" \
  | grep -i "all transactions failed"

# What versions / contracts are dominating the mempool?
curl -s http://127.0.0.1:8080/api/mempool/peek \
  | jq '.transactions[].type' | sort | uniq -c

# Are slash/enroll rejections climbing? If yes, the
# admission/applier divergence is feeding the freeze:
curl -s http://127.0.0.1:9090/api/v1/query \
  --data-urlencode 'query=sum by (reason) (rate(qsdm_slash_rejected_total[5m]))'
curl -s http://127.0.0.1:9090/api/v1/query \
  --data-urlencode 'query=sum by (reason) (rate(qsdm_enrollment_rejected_total[5m]))'
```

#### Mitigation

| Cause | Action |
|---|---|
| All-failed freeze (release skew) | Roll the producer to the prior validator binary; the failing tx version drains naturally as miners notice rejections and stop submitting |
| All-failed freeze (state corruption) | Identify the poisoned account from logs; if the failing tx pattern points at one specific NodeID/Owner, isolate it via the mempool reject list while ops investigates |
| Mempool empty + quorum | Validator is idle. Confirm the producer is selected for the round (consensus-leader rotation); if not, the round leader is the wedged peer — escalate to that peer's on-call |
| Quorum collapse | This is a consensus-subsystem incident, not a mining incident — escalate to the consensus on-call. v2-mining tiles will recover automatically once quorum is back |
| Prolonged unknown cause | Restart the producer **only after** capturing logs and a mempool snapshot; a blind restart resets the symptom but loses the diagnostic trail |

#### Recovery validation

```promql
# Height advancing again?
delta(qsdm_chain_height[2m]) > 0

# Mempool draining?
delta(qsdm_mempool_size[2m]) < 0

# Slash + enroll counters resuming?
rate(qsdm_slash_applied_total[5m]) > 0  # if there were applied slashes pre-incident
rate(qsdm_enrollment_applied_total[5m]) > 0
```

The downstream alerts (`QSDMMiningSlashApplied`,
`QSDMMiningRegistryEmpty`, etc.) auto-clear within their
own `for:` windows once the producer resumes. Do not
silence them manually unless you've confirmed the
producer is healthy first.

---

### 3.2. Mode B — `QSDMMiningMempoolBacklog`

`qsdm_mempool_size > 10000` for 10m. Severity: warning.

#### Symptoms (dashboard / Prometheus)

- `qsdm_mempool_size` **above 10k** and the cell stays red.
- `qsdm_chain_height` **still advancing** (otherwise this
  would be Mode A).
- One or both of `qsdm_slash_rejected_total` /
  `qsdm_enrollment_rejected_total` rates climbing if the
  cause is admission/applier divergence; flat if the cause
  is pure traffic spike.

#### Why this alert exists

The admission gate is stateless; the applier is stateful.
A tx can pass admission (signature OK, fee OK, encoding OK)
and then fail apply (account doesn't exist, nonce gap,
contract returned ApplyError). When admission is letting
through txs the applier rejects, those txs eat block-space
budget every round but never drain — they sit in the
mempool until they expire or get evicted, which can take a
long time on the default config.

The 10k / 10m threshold is calibrated against the
production block-size + block-time: traffic spikes drain
within seconds, applier-divergence backlogs do not.

#### Triage

```promql
# Step 1: Is the chain still advancing?
delta(qsdm_chain_height[5m])
# If 0 → escalate to Mode A; this is no longer a backlog
# incident, it's a freeze.

# Step 2: Are admission-vs-applier divergence rates high?
sum by (reason) (rate(qsdm_slash_rejected_total[10m]))
sum by (reason) (rate(qsdm_enrollment_rejected_total[10m]))

# Step 3: Mempool growth rate vs producer drain rate?
deriv(qsdm_mempool_size[5m])
```

| `chain_height` advancing | Reject rates | Probable cause |
|---|---|---|
| Yes | Low | Genuine traffic spike — likely a dApp launch, a coordinated mass-enroll, or a probe. Self-resolves once submission rate drops. Monitor for promotion to Mode A |
| Yes | High by reason `not_enrolled` / `wrong_contract` / `decode_failed` | Stateless admission letting through invalid txs. Client-server skew after a release, OR a probe submitting malformed payloads at volume |
| Yes | High by reason `insufficient_balance` / `fee_invalid` | Operator-side: under-funded miners retrying enrolls, or fee-floor mismatch after a fee policy change |
| No | — | Promote to Mode A — chain has wedged |

#### Mitigation

- **Genuine spike:** wait. If sustained beyond 30m,
  consider raising the producer batch size or the
  admission-gate rate-limit (config knobs documented in
  `MINER_QUICKSTART.md`).
- **Admission/applier divergence (high reject-by-reason
  rate):** the long-term fix is to tighten admission to
  cover the divergent path. Short-term, you can:
  - Pin the client minimum version in MINER_QUICKSTART
    (drains the malformed-payload dominant case).
  - Add a temporary mempool eviction policy keyed off
    the dominant reject reason (operator-only knob).
- **Probe / scan:** the per-miner rate-limit on the
  rejection-ring (commit `aa17da6`) already throttles the
  dust counter; for the mempool itself, add the offending
  source IP to the firewall block list.

#### Recovery validation

```promql
qsdm_mempool_size < 1000           # back near steady state
deriv(qsdm_mempool_size[5m]) <= 0  # draining or flat
```

---

## 4. Cross-mode escalation

These two alerts have a **strict promotion path**: if
Mode B is firing AND `qsdm_chain_height` stops advancing,
the incident has promoted from "backlog" to "frozen". Mode
A takes precedence; abandon Mode B triage and switch.

The reverse is not true — Mode A can fire in isolation
(empty mempool + idle producer); Mode B cannot mask a
freeze because Mode A's `delta(chain_height[5m]) == 0`
condition is a hard guard.

If both fire concurrently:

1. Mode A is the operational lead — the producer is
   wedged.
2. Mode B is the diagnostic clue — the cause is almost
   certainly the all-failed-freeze path (Mode A §3.1)
   driven by what the backlog accumulated.
3. Read the top-N tx types from the mempool snapshot;
   the dominant type is the failing tx pattern.

---

## 5. Interaction with downstream v2-mining alerts

When Mode A fires, every alert in these groups goes
quiet because their counter inputs flatline:

- `qsdm-v2-mining-slash` (4 alerts —
  `QSDMMiningSlashApplied`, `SlashedDustBurst`,
  `SlashRejectionsBurst`, `AutoRevokeBurst`).
- `qsdm-v2-mining-enrollment` (5 alerts —
  `QSDMMiningRegistryEmpty`, `RegistryShrinkingFast`,
  `PendingUnbondMajority`, `EnrollmentRejectionsBurst`,
  `BondedDustDropped`).

`QSDMMiningRegistryEmpty` is *especially* misleading
during a wedge: the gauge reads zero because the
applier hasn't run since the wedge began, not because
miners have exited. The runbook for that alert
(`ENROLLMENT_INCIDENT.md` Mode A) explicitly redirects
back here as the first triage step — confirm liveness
before believing the registry-empty signal.

After Mode A clears, expect a **single-tick burst** of
downstream activity as the producer drains the backlog
that accumulated during the wedge. This may briefly
trigger `QSDMMiningSlashedDustBurst` or
`QSDMMiningEnrollmentRejectionsBurst` depending on what
was queued; treat as expected post-recovery noise, not
as a new incident, unless the burst sustains beyond the
alert's `for:` window.

---

## 6. Reference

- Source gauges: `pkg/monitoring/prometheus.go`
  (callback-driven; the producer + mempool + validator-set
  install the closures at boot).
- Alert rules: `deploy/prometheus/alerts_qsdm.example.yml`
  → `qsdm-v2-mining-liveness` group.
- Producer entry point: `pkg/blockchain/...:ProduceBlock`.
- Admission entry point: `pkg/mining/...:AdmissionCheckers`.
- Companion runbooks (downstream collateral):
  - SLASHING_INCIDENT.md
  - ENROLLMENT_INCIDENT.md
  - REJECTION_FLOOD.md
