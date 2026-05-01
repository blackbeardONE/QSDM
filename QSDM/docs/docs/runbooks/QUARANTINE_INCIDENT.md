# Quarantine Subsystem — Operator Runbook

Triage flow for the 2 alerts in the `qsdm-quarantine`
group:

| Alert | Severity | Default `for:` | Anchor |
|---|---|---|---|
| `QSDMQuarantineAnySubmesh`        | warning      | 10m | [§3.1](#31-mode-a--qsdmquarantineanysubmesh) |
| `QSDMQuarantineMajorityIsolated`  | **critical** | 15m | [§3.2](#32-mode-b--qsdmquarantinemajorityisolated) |

> **Why a quarantine-only runbook?** Quarantine is the
> mesh's **immune system** — when a submesh starts
> emitting policy-violating traffic (invalid txs, repeated
> P2P rejects, malformed gossip), `QuarantineManager`
> isolates it from consensus until either an operator
> calls `RemoveQuarantine` or the auto-recovery hook
> clears the condition. Mode A is "the immune system
> activated" (informational-but-actionable, because human
> review is the default policy). Mode B is "the immune
> system has flipped from local-policy enforcement to
> systemic-event signal" — when more than half of the
> tracked submeshes are quarantined, the cause is almost
> never local; it's a bad rollout, a rotated key, or a
> chain-wide clock-skew event.

Companion observability: `qsdm_quarantine_*` gauges
emitted by `pkg/quarantine/metrics.go`. The
`qsdm-submesh` alert group is the per-policy-rejection
counterpart — **submesh** alerts answer "is the policy
ever kicking in?" while **quarantine** alerts answer
"how many submeshes are currently isolated?".

---

## 1. Glossary (60-second skim)

- **Submesh** — a logical partition of the gossip mesh
  identified by a key (typically a network/region/
  validator-set hash). Quarantine state is per-submesh
  and per-node.
- **Quarantined** — `quarantined[k]=true` on a node.
  P2P traffic from that submesh is dropped before
  consensus admission; the node still observes it for
  diagnostic counters but does not act on it.
- **Tracked** — a submesh that has been observed at
  least once. The denominator for the ratio gauge.
  `qsdm_quarantine_submeshes_tracked` increases over
  time and never decreases (no GC of historical
  submeshes).
- **`qsdm_quarantine_submeshes`** — gauge: number of
  submeshes with `quarantined[k]=true` *right now*. Goes
  up on `AddQuarantine`, down on `RemoveQuarantine` or
  auto-recovery.
- **`qsdm_quarantine_submeshes_ratio`** — gauge:
  `submeshes / submeshes_tracked`. The collector returns
  0 when `tracked==0` (natural-zero denominator); no
  alert can fire on an empty fleet by construction.
- **Auto-recovery** — `pkg/quarantine/auto_recovery.go`,
  optional and off by default. When wired, it monitors
  the underlying counter trend; if a quarantined
  submesh's reject rate drops to zero for a sustained
  window, it auto-clears. **Production deployments
  typically do NOT wire auto-recovery** — operator
  review is the safer default.
- **`RemoveQuarantine(k)`** — manual clear. The right
  primitive after the underlying cause is fixed; calling
  it without fixing the cause re-quarantines on the next
  policy hit.

---

## 2. First-90-seconds checklist

1. **Read both gauges.** `qsdm_quarantine_submeshes` is
   the count; `qsdm_quarantine_submeshes_tracked` is
   the denominator. The ratio is the systemic signal.

2. **Don't just call `RemoveQuarantine` to clear the
   alert.** A bare `RemoveQuarantine` re-quarantines on
   the next hit; the alert returns within minutes.
   Identify the underlying cause first.

3. **Cross-reference the submesh-policy counters.**
   `qsdm_submesh_p2p_reject_*` and
   `qsdm_submesh_api_reject_*` (from the `qsdm-submesh`
   alert group) name the offending policy. The
   dominant `reason` label there is the cause for the
   quarantine here.

4. **For Mode B: stop, capture, decide.** Critical-
   severity, ratio > 50% — don't reflexively clear half
   the mesh. Find the systemic cause (release rollout
   timing, key rotation window, NTP skew event) before
   touching `RemoveQuarantine`.

---

## 3. Modes

### 3.1. Mode A — `QSDMQuarantineAnySubmesh`

`qsdm_quarantine_submeshes > 0` for 10m. Severity: warning.

#### Symptoms

- One or more submeshes have `quarantined[k]=true` for
  10+ minutes.
- The corresponding submesh's traffic is being dropped
  at the P2P / API gates; downstream consensus does not
  see it.
- `qsdm_submesh_p2p_reject_*` or `*_api_reject_*`
  counters were almost certainly elevated in the
  10–30 min before this alert fired.

#### Triage

```promql
# Which submeshes are currently quarantined? The
# QuarantineManager exposes the keys via /api endpoint;
# the counter does not (it's just a count). Inspect
# logs for "AddQuarantine" lines.
```

```bash
# Find the AddQuarantine log lines:
journalctl -u qsdm --since "30 minutes ago" \
  | grep -iE "addquarantine|quarantine.*key="

# Cross-reference the upstream policy that fired:
journalctl -u qsdm --since "30 minutes ago" \
  | grep -iE "submesh.*reject|policy.*violat"
```

| Observed reject reason | Probable cause | Action |
|---|---|---|
| `invalid_signature` / `bad_proof` dominant | A peer in that submesh has a corrupted key bundle, or is running an old version that signs with a deprecated scheme | Reach out to the submesh operator; request key rotation or version bump |
| `clock_skew` / `replay_window` dominant | Single-host time drift on the offending peer | Force NTP sync on the peer; quarantine clears on next clean window |
| `bad_tx_payload` / `decode_failed` dominant | Client-server skew after a release in that submesh | Pin minimum client version; coordinate the rollout |
| `unknown` / `other` dominant | Spec drift or a new failure path that hasn't been mapped to a closed-enum reason yet | File a P2 ticket; the closed-enum must be tightened so future occurrences have a labeled reason |
| Counter quiet now, but quarantine sticky | Auto-recovery is not wired (the default); the manual `RemoveQuarantine` is the only way to clear | After confirming the cause is resolved, call `RemoveQuarantine(k)` via the operator API |

#### Mitigation

- **Operator-side fix** (the offending peer's key /
  clock / version): clear after the underlying fix
  ships; quarantine state isn't retained across the
  fix because the policy counter has stopped
  incrementing.
- **`RemoveQuarantine` without a fix:** anti-pattern.
  The alert will refire within one observation window
  (typically <5 min) once the offending peer pings the
  policy gate again.
- **Wire auto-recovery only for non-production
  environments.** In production the human-review
  default is intentional — auto-recovery can clear a
  quarantine that's masking a real attack.

---

### 3.2. Mode B — `QSDMQuarantineMajorityIsolated`

`qsdm_quarantine_submeshes_tracked >= 4 and
qsdm_quarantine_submeshes_ratio > 0.5` for 15m.
Severity: **critical**.

#### Why this is critical-severity

A *majority* of tracked submeshes simultaneously
isolated is **never** a local-policy hit — single-rig
issues affect one submesh at a time. When >50% are
quarantined together, the cause is necessarily
something they share: a recently-shipped binary, a
rotated key, a clock-source rollover, a config-drift
push that hit the whole fleet.

The `tracked >= 4` guard exists so a 1-of-2 outage
doesn't cross 50% on a tiny fleet. With four or more
tracked submeshes, the ratio is meaningful.

#### Symptoms

- `qsdm_quarantine_submeshes` is large.
- `qsdm_quarantine_submeshes_ratio` > 0.5.
- Mode A is firing for many submeshes simultaneously
  (the same single page in alert manager, but the
  underlying log lines name multiple submesh keys).
- Consensus liveness alerts may follow within minutes
  if the quarantine has isolated enough of the
  validator set — cross-reference
  [`MINING_LIVENESS.md`](MINING_LIVENESS.md#31-mode-a--qsdmminingchainstuck)
  if `qsdm_chain_height` stops advancing.

#### Triage

```bash
# Did a release ship in the last hour?
git log --since "60 minutes ago" --oneline -- 'pkg/' 'cmd/'

# Did a config push happen?
journalctl -u qsdm --since "60 minutes ago" \
  | grep -iE "config.*reload|env.*chang"

# What's the dominant reject reason across the
# quarantined population?
sum by (reason) (rate(qsdm_submesh_p2p_reject_total[15m]))
sum by (reason) (rate(qsdm_submesh_api_reject_total[15m]))
```

| Recent change | Likely cause | Action |
|---|---|---|
| Validator binary released < 1h ago | Release regression — the new binary's policy gate rejects payloads from peers still on the old version | Roll back the validator binary; the quarantines clear on the next clean policy window |
| Config push in last hour | Config drift — a new `cfg.SubmeshPolicy*` value pushed to one side but not the other | Audit the config rollout; reconcile sides |
| NTP rollover / leap-second event | Fleet-wide clock skew | Wait for NTP to re-stabilise; do NOT mass `RemoveQuarantine` (the gate will catch the next skewed payload anyway) |
| Key rotation in the last 24h | Old peers haven't picked up the new public key yet | Coordinate the key-rotation rollout; legacy key window may need extending |
| No recent change | **Suspect attack surface.** Coordinated invalid-tx submission from a botnet hits every submesh roughly evenly | Capture mempool snapshots + P2P peer logs for forensics; consider raising the per-miner rate-limit on the rejection-ring (see [`REJECTION_FLOOD.md`](REJECTION_FLOOD.md)) |

#### Mitigation

- **Do NOT mass-RemoveQuarantine.** A bulk clear without
  fixing the upstream cause re-fires within 15 min and
  loses the diagnostic state of which submeshes
  triggered when.
- **Roll back the most-recent change first** (binary
  /config / key) and observe whether the ratio drops
  on its own. If it does, the change WAS the cause and
  no manual quarantine clearing is needed.
- **If the ratio doesn't drop after rollback**, the
  cause is upstream of the change (likely external —
  attack, NTP, network split). Capture state and
  escalate to the network-ops on-call.

#### Recovery validation

```promql
qsdm_quarantine_submeshes_ratio < 0.25       # back below alert threshold
delta(qsdm_quarantine_submeshes[10m]) < 0    # count actually decreasing
```

The alert auto-clears when the ratio falls below 0.5
for one full evaluation window past the `for: 15m`
holdoff.

---

## 4. Cross-mode escalation

| Concurrent alerts | Most likely root | Action |
|---|---|---|
| Mode A on 1 submesh | Local-policy hit on a single peer | Mode A triage |
| Mode A on N <50% submeshes | Several independent local hits, OR a partial systemic event hitting some submeshes harder than others | Triage each Mode A independently; watch for ratio to cross 50% (promotion to Mode B) |
| Mode B alone | Systemic event (release / config / key / clock) | Mode B triage; do NOT clear individual quarantines until root cause identified |
| Mode B + chain-liveness alert | Quarantine has isolated enough validators that consensus is wedging | [`MINING_LIVENESS.md`](MINING_LIVENESS.md) takes precedence; quarantine is collateral but not the root cause unless the alert pre-dates the chain-stuck event |

---

## 5. Reference

- **Source files:**
  - [`pkg/quarantine/manager.go`](../../../source/pkg/quarantine/manager.go)
    — QuarantineManager + Add/RemoveQuarantine
  - [`pkg/quarantine/metrics.go`](../../../source/pkg/quarantine/metrics.go)
    — `qsdm_quarantine_*` gauges
  - [`pkg/quarantine/auto_recovery.go`](../../../source/pkg/quarantine/auto_recovery.go)
    — optional auto-clear hook
- **Prometheus series:**
  - `qsdm_quarantine_submeshes` — current count of isolated submeshes
  - `qsdm_quarantine_submeshes_tracked` — total observed (denominator)
  - `qsdm_quarantine_submeshes_ratio` — quarantined / tracked
- **Companion runbooks:**
  - [`MINING_LIVENESS.md`](MINING_LIVENESS.md) — when
    quarantine has isolated enough of the validator
    set that consensus stalls
  - [`REJECTION_FLOOD.md`](REJECTION_FLOOD.md) — the
    rejection-ring is the per-rejection forensic store;
    a sustained quarantine usually has a corresponding
    rejection-flood signal in that subsystem

---

## 6. Alert ↔ Mode quick-reference

| Alert                            | Mode | Severity     | Triage section |
| -------------------------------- | ---- | ------------ | -------------- |
| `QSDMQuarantineAnySubmesh`       | A    | warning      | §3.1           |
| `QSDMQuarantineMajorityIsolated` | B    | **critical** | §3.2           |
