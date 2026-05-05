# Peer Reputation — Operator Runbook

Two-mode runbook for the peer-reputation trackers
(`pkg/networking.ReputationTracker`). Mode A catches a
**high banned-ratio** (50%+ peers banned for ≥10m on a
tracker with ≥4 peers) — either a coordinated attack or
a penalty-config regression. Mode B is a softer info-
level drift signal: **min-score sliding toward the
ban threshold** sustained for ≥30m, often a precursor to
Mode A.

| Alert | Severity | Default `for:` | Anchor |
|---|---|---|---|
| `QSDMReputationBanRatioHigh` | warning | 10m | [§3.1](#31-mode-a--qsdmreputationbanratiohigh) |
| `QSDMReputationScoreCollapse`| info    | 30m | [§3.2](#32-mode-b--qsdmreputationscorecollapse) |

> **What this runbook closes.** `pkg/networking.ReputationTracker`
> existed prior to this commit and was wired into the BFT
> ingress and evidence ingress, but had **two large
> operational gaps**:
>
> 1. **Decay never ran.** The trackers were created in
>    `cmd/qsdm/main.go` but `Start()` was never called.
>    Penalties accumulated forever without the configured
>    decay pulling scores back toward zero.
> 2. **Zero observability.** Tracker state was visible
>    only via the admin API. No Prometheus exposition,
>    no alerting, no dashboards.
>
> Both gaps are now closed: `Start()` is invoked at
> validator boot (with matching `defer Stop()`), and the
> new `qsdm_reputation_*{tracker}` gauges expose tracker
> state via the `pkg/monitoring/repmetrics` leaf
> (zero-dependency, mirrors the `netmetrics` pattern to
> avoid import cycles).

---

## 1. Glossary (60-second skim)

- **`ReputationTracker`** — per-peer score store with
  configurable per-event weights. Two trackers are wired
  in production:
  - **`tracker="tx"`** — uses `DefaultReputationConfig`
    (lenient: `InvalidTxWeight=-10`, `ProtocolViolWeight=-100`,
    `BanThreshold=-200`).
  - **`tracker="evidence"`** — uses
    `ReputationConfigForEvidence` (strict:
    `InvalidTxWeight=-15`, `ProtocolViolWeight=-150`).
- **`PeerEvent`** — one of `EventValidBlock`,
  `EventInvalidBlock`, `EventValidTx`, `EventInvalidTx`,
  `EventTimeout`, `EventLatencyReport`, `EventDisconnect`,
  `EventProtocolViolation`. Weights from the active
  config map each event to a score delta.
- **Banned** — a peer crosses `Banned=true` when its
  score drops below `BanThreshold`. Bans are sticky
  until either a manual `Unban(peerID)` or a process
  restart (state is in-memory).
- **Decay** — a periodic background loop multiplies
  every peer's score by `DecayFactor` (0.95 default)
  every `DecayInterval` (5m default), pulling scores
  toward zero. Without decay, a single bad burst would
  permanently dominate.
- **`qsdm_reputation_*{tracker}`** — five gauges per
  registered tracker: `peers_total`, `peers_banned`,
  `score_min`, `score_max`, `score_avg`. All pulled at
  scrape time from `ReputationTracker.Snapshot()`.

---

## 2. Pre-flight: which tracker, which signal?

```promql
sum by (tracker) (qsdm_reputation_peers_banned)
  /
sum by (tracker) (qsdm_reputation_peers_total)
```

The dominant tracker tells you whether the failure is
in the **transaction-gossip** or **consensus-evidence**
ingress. If both show > 0.5, the cause is probably
systemic (penalty-config regression, deploy-side bug);
if only one, the cause is per-tracker (per-config or
per-traffic-shape).

---

## 3. Per-mode triage

### 3.1 Mode A — `QSDMReputationBanRatioHigh`

**Severity:** warning. **Default `for:`** 10m.

**Fires when**: `peers_banned / peers_total > 0.5`
sustained for ≥10m AND `peers_total >= 4` (filters out
`1-of-2-peers-banned` noise on tiny dev clusters).

**Why this matters**: a majority of tracked peers are
banned. Either a coordinated attack against this node,
or — far more likely — a penalty-config regression
that's mass-banning honest peers.

**Triage**:

1. **Inspect the banned set** via the admin API or
   directly via `ReputationTracker.BannedPeers()`. For
   each banned record, look at:
   - `BannedAt` timestamp clustering (recent burst →
     systemic cause; historical drift → individual
     misbehaviour).
   - `InvalidBlks` / `InvalidTxs` / `Violations` counts.
     High counts = real misbehaviour. Low counts +
     recent bans = the penalty math has shifted under
     them (config regression).
2. **Per-tracker disambiguation**:
   - Both `tracker="tx"` AND `tracker="evidence"` firing
     concurrently → cause is systemic. Check recent
     deploys / config pushes.
   - Only `tracker="evidence"` firing → consensus
     evidence is producing protocol violations
     systematically. Cross-check
     [`SLASHING_INCIDENT.md`](SLASHING_INCIDENT.md) and
     [`MINING_LIVENESS.md`](MINING_LIVENESS.md) — if
     evidence-relay is degraded, peers will be flagged
     with `EventProtocolViolation` for relaying
     malformed evidence even when they're honest.
   - Only `tracker="tx"` firing → transaction gossip
     specifically is producing invalid-tx penalties.
     Check the gossip path's validator logic (a recent
     change to tx-validation criteria can mass-flag
     historic behaviour as invalid).
3. **Cross-fleet check**: if a majority of validators
   are firing this alert concurrently, the cause is
   100% deploy-side (config regression or
   consensus-shape change). Roll back.
4. **Mitigation**: in the rare case of a real attack:
   - The bans themselves are doing their job — banned
     peers are dropped from the tx/evidence path, so
     the validator is protected.
   - Check that `quarantine` is also kicking in
     (`qsdm_quarantine_*`). Reputation alone doesn't
     isolate misbehaving peers from non-tx topics;
     quarantine does. If they're not co-firing, the
     ingress paths are protected but other paths
     (PEX, BFT) may not be — investigate.

**Companions:**
[`QUARANTINE_INCIDENT.md`](QUARANTINE_INCIDENT.md)
(`QSDMQuarantineMajorityIsolated` is the quarantine-
side companion when a majority of peers misbehave),
[`SLASHING_INCIDENT.md`](SLASHING_INCIDENT.md)
(`tracker="evidence"` Mode A often co-fires with
slashing alerts when evidence-gossip is degraded),
[`NETWORKING_INCIDENT.md`](NETWORKING_INCIDENT.md)
(`QSDMP2PGossipIngressStalled` co-firing with Mode A
suggests peers are protocol-violating because they
can't actually parse our gossip — unusual but
possible).

---

### 3.2 Mode B — `QSDMReputationScoreCollapse`

**Severity:** info. **Default `for:`** 30m.

**Fires when**: `qsdm_reputation_score_min < -100`
(halfway to the default `BanThreshold` of -200)
sustained for ≥30m AND `peers_total >= 4`.

**Why this matters**: drift signal. Penalties are
accumulating faster than the configured decay can pull
them back. Often a precursor to Mode A — bans usually
follow within the next 30m if the trend continues.

**Triage**:

1. **Read the trend**:
   ```promql
   deriv(qsdm_reputation_score_min[1h])
   ```
   - Strongly negative → score is actively collapsing,
     Mode A is imminent. Begin Mode A triage now to
     get ahead of the page.
   - Flat near -100 → peers are on a stable
     low-but-not-banned floor, likely a known-noisy
     peer set. May not need immediate action.
   - Positive → decay is winning, the tracker is
     recovering from a transient burst. Wait it out.
2. **Check decay config**: `DecayInterval` (5m default)
   and `DecayFactor` (0.95 default). If decay was
   recently tuned tighter (smaller factor or longer
   interval), the system is now less forgiving and
   Mode B firing is the expected new normal.
3. **Volume check**: if `peers_total` is climbing while
   `score_min` collapses, new peers are coming in
   already-misbehaving. Often a sign of bootstrap-list
   contamination — check
   `pkg/networking/bootstrap.go` /
   `pkg/networking/pex.go` for recent additions.

**Companions:**
[Mode A above](#31-mode-a--qsdmreputationbanratiohigh)
(this is its precursor signal),
[`NETWORKING_INCIDENT.md`](NETWORKING_INCIDENT.md)
(`QSDMP2PNoPeers` is the polar opposite — there are
no peers to score; if Mode B was firing and is now
silent because peers dropped to zero, that's the
real failure to investigate).

---

## 4. Cross-references

- `pkg/monitoring/repmetrics/repmetrics.go` — leaf
  package with `ReputationProvider` interface,
  `RegisterReputationProvider`, `Providers()`. Zero
  non-stdlib imports.
- `pkg/monitoring/reputation_metrics.go` — Prometheus
  exposition wrapper. Re-exports the leaf primitives
  at `monitoring.RegisterReputationProvider` for
  backwards compat.
- `pkg/networking/reputation.go` —
  `ReputationTracker.Snapshot()` implements the
  provider interface. The legacy
  `RecordEvent` / `DecayAll` / `Start` / `Stop`
  methods are unchanged.
- `cmd/qsdm/main.go` —
  `monitoring.RegisterReputationProvider("tx", ...)`
  and `("evidence", ...)` happen at boot, with
  matching `Start()` / `defer Stop()`.
- `QSDM/deploy/prometheus/alerts_qsdm.example.yml` —
  `qsdm-reputation` group.
- `QSDM/deploy/grafana/dashboards/qsdm-runbook-reputation-incident.json`
  — auto-generated panel.
- [`QUARANTINE_INCIDENT.md`](QUARANTINE_INCIDENT.md)
  (the policy-layer counterpart — bans isolate per
  topic, quarantine isolates per submesh).
- [`SLASHING_INCIDENT.md`](SLASHING_INCIDENT.md)
  (`tracker="evidence"` Mode A often co-fires when
  evidence gossip is degraded).
- [`NETWORKING_INCIDENT.md`](NETWORKING_INCIDENT.md)
  (`QSDMP2PNoPeers` is the polar opposite —
  Mode B / Mode A both require non-zero
  `peers_total`).
