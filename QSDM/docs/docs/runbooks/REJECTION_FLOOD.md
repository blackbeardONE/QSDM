# Runbook — §4.6 Attestation Rejection Flood

**Audience:** validator operators on call for a single QSDM node or a
fleet thereof.

**Trigger:** one or more of these alerts firing on
[`alerts_qsdm.example.yml`](../../../deploy/prometheus/alerts_qsdm.example.yml):

- `QSDMAttestRejectionPersistCompactionsHigh` (severity warning) — Mode A
- `QSDMAttestRejectionPersistHardCapDropping` (severity warning) — Mode B
- `QSDMAttestRejectionPerMinerRateLimited` (severity warning) — Mode C

**Estimated time to resolve:** 5–30 minutes for a typical single-miner
flood; longer if a coordinated multi-miner spam campaign is under way
or if the operator chooses to push the offender through the §4.6
slashing pipeline.

---

## 1. What is a "rejection flood"?

QSDM validators record every §4.6 attestation rejection — archspoof
mismatches and out-of-band hashrate claims — into a bounded in-memory
ring (`pkg/mining/attest/recentrejects`). The ring buffer is volatile
by design: it is forensic telemetry, not consensus state. When the
operator configures `cfg.RecentRejectionsPath`, the same records are
ALSO append-only persisted to a JSONL log so a restart does not wipe
forensic continuity.

The rejection ring is bounded by **three** defences in series. The
first fires at ring entry; the next two fire on the persister:

| Defence                | Unit       | Default        | Set by                                  | Where it fires |
| ---------------------- | ---------- | -------------- | --------------------------------------- | -------------- |
| **per-miner limiter**  | rec/s/miner | 0 (= disabled) | `cfg.RecentRejectionsRateLimitPerSec`   | `Store.Record()` entry — drops BEFORE the ring or the persister see the record. Per-miner token bucket; the dashboard's "top offenders" strip identifies the source. |
| **softCap**            | records    | 1024 records   | `recentrejects.DefaultPersistSoftCap`   | Persister — triggers compaction (read-all, keep-last-N, atomic-rename rewrite) once per `softCap`-many appends. |
| **maxBytes**           | bytes      | 0 (= disabled) | `cfg.RecentRejectionsMaxBytes`          | Persister — refuses an Append outright when admitting it would breach the byte ceiling AND a salvage compaction failed to free enough headroom. |

A "rejection flood" is operator-jargon for the scenario where one or
more miners are submitting forged proofs faster than the validator can
absorb. There are three failure modes:

- **Mode A (caught by `QSDMAttestRejectionPersistCompactionsHigh`):**
  the soft-cap rewrite loop is keeping up, but the rate is anomalously
  high — typically `>5 compactions/min` sustained for 30m. The
  validator is healthy; the volume is the signal.
- **Mode B (caught by `QSDMAttestRejectionPersistHardCapDropping`):**
  the soft-cap rewrite loop is NOT keeping up. The hard byte cap has
  refused at least one record over the last 10m. Forensic durability
  is being shed — the in-memory ring still receives every record, but
  the on-disk JSONL log is no longer a complete history.
- **Mode C (caught by `QSDMAttestRejectionPerMinerRateLimited`):**
  the per-miner token bucket is exhausted for at least one
  `miner_addr`. Records from that source are dropped at
  `Store.Record()` entry; neither ring nor persister sees them. This
  is the EARLIEST defence — when it fires alone (Modes A and B both
  flat) it is the cleanest "single bad actor" signal in the stack.

Mode B is strictly worse than Mode A, and only fires when `maxBytes`
is configured. Mode C only fires when `cfg.RecentRejectionsRateLimitPerSec`
is configured. A node with both caps disabled (the defaults)
will never see Modes B or C — but it ALSO has no upper bound on disk
consumption AND no per-miner fairness guarantee, which is why
production operators are encouraged to set both.

---

## 2. Symptoms

### 2.1 Dashboard tile

Open the operator dashboard (default
`http://<validator-host>:8080/`) and locate the **🛑 Attestation
Rejections** card. The persistence-lifecycle row carries five cells:

| Cell                  | Healthy        | Mode A (compactions high)  | Mode B (hard-cap dropping)              | Mode C (rate-limit dropping) |
| --------------------- | -------------- | -------------------------- | --------------------------------------- | ---------------------------- |
| **persist errors**    | 0 (green)      | 0 (green)                  | 0 — possibly non-zero on a contemporaneous I/O flap | 0 (green)        |
| **compactions**       | low/stable     | climbing fast              | climbing AND records-on-disk plateaued near MaxBytes/recordSize | low/stable — the limiter is sparing the ring |
| **records on disk**   | ≤ softCap      | hovering near softCap      | hovering near MaxBytes/recordSize       | ≤ softCap                    |
| **hard-cap drops**    | 0 (green)      | 0 (green)                  | **non-zero (red)**                      | 0 (green)                    |
| **rate-limit drops**  | 0 (green)      | 0 — limiter not exhausted  | possibly non-zero — the flood is hitting both gates | **non-zero (red)** |

Read top-to-bottom. The first red cell tells you the mode:

- **rate-limit drops red, others green** ⇒ Mode C — single bad actor,
  the limiter is doing its job. Triage at §3.3.
- **hard-cap drops red** ⇒ Mode B — escalate; the limiter (if
  configured) was not enough.
- **compactions climbing, hard-cap drops green** ⇒ Mode A — the
  soft-cap loop is keeping up, but the volume is anomalous.

If multiple cells are red the modes are not mutually exclusive: a
sustained single-miner flood will trip Mode C first, then if their
ALLOWED rate still saturates the persister, Modes A and B as well.

### 2.2 Prometheus

The five series operators read during this incident:

```promql
# Compaction rate (Mode A trigger)
rate(qsdm_attest_rejection_persist_compactions_total[5m]) * 60

# Hard-cap drop rate (Mode B trigger)
rate(qsdm_attest_rejection_persist_hardcap_drops_total[5m])

# Per-miner rate-limit drop rate (Mode C trigger)
rate(qsdm_attest_rejection_per_miner_rate_limited_total[5m])

# Current on-disk record count (gauge)
qsdm_attest_rejection_persist_records_on_disk

# Underlying §4.6 rejection rate by kind
rate(qsdm_attestation_rejected_total[5m]) by (kind)
```

### 2.3 Logs

Per-record `Append` failures are intentionally NOT logged (they fire
too frequently under filesystem flap to log per-event); they only bump
`qsdm_attest_rejection_persist_errors_total`. The only log-channel
signals you will see are:

- Boot-time: `v2wiring: recent-rejections persister: <err>` if the
  filesystem path was unreachable at startup.
- Boot-time: `v2wiring: recent-rejections restore: <err>` if replay
  of an existing JSONL log failed.

If you see neither, the persister is operating normally and the
flood signal lives entirely in the metrics.

---

## 3. Triage

Work top-to-bottom; each step is independent so you can stop as soon
as the picture is clear.

### 3.1 Confirm a flood is in progress

```promql
rate(qsdm_attestation_rejected_total[5m])
```

A healthy validator's baseline is highly site-specific but typically
< 1 rejection/s. A sustained ≥ 10 rejection/s is anomalous; ≥ 100
rejection/s is a flood by any operator's definition.

### 3.2 Identify the dominant rejection kind

```promql
topk(5, rate(qsdm_attestation_rejected_total{kind!=""}[5m]))
```

The four §4.6 kinds and what they imply:

| Kind                              | Implication                                                                            |
| --------------------------------- | -------------------------------------------------------------------------------------- |
| `archspoof_unknown_arch`          | Miner is claiming a GPU architecture string the validator does not recognise. Either a fresh NVIDIA arch the validator does not yet know about (deploy a software update) OR a hostile spammer trying values at random. |
| `archspoof_gpu_name_mismatch`     | Miner's claimed `gpu_name` does not match the canonical name for the claimed `gpu_arch`. Deliberate forgery. |
| `archspoof_cc_subject_mismatch`   | Confidential Computing leaf-cert subject does not match the claimed GPU. Deliberate forgery via stolen / replayed CC cert. |
| `hashrate_out_of_band`            | Claimed hashrate is outside the verifier's allowed band for the claimed arch. Deliberate forgery OR a miner running unsanctioned firmware. |

A flood that is overwhelmingly ONE kind is almost always a single
hostile miner; a balanced spread across kinds is more often a
coordinated attack.

### 3.3 Identify the offending miner(s)

The dashboard's **Top offenders (this page)** strip is computed
client-side over the most-recent 50 rejections. For incidents that
have been in progress for more than ~5 minutes, paginate further back
via the v1 endpoint:

```bash
curl -s 'http://<validator-host>:8080/api/v1/attest/recent-rejections?limit=500' \
  | jq -r '.records[] | .miner_addr' | sort | uniq -c | sort -rn | head
```

For incidents older than the in-memory ring (default 1024 records),
inspect the on-disk JSONL log directly:

```bash
jq -s 'group_by(.MinerAddr) | map({addr: .[0].MinerAddr, count: length}) | sort_by(-.count) | .[0:10]' \
  "$RECENT_REJECTIONS_PATH"
```

If a single `miner_addr` accounts for ≥ 80% of the flood, you have a
clean target for the §3.4 mitigations. If it is spread across ≥ 5
addresses, treat it as a coordinated campaign and escalate to §4.

### 3.4 Decide on mitigation

The choice depends on whether you are seeing Mode A or Mode B AND on
your operational policy.

#### 3.4.1 Mode A (compactions high) — three options

| Option                                  | Effect                                                       | Trade-off                                          |
| --------------------------------------- | ------------------------------------------------------------ | -------------------------------------------------- |
| **Wait** (no action)                    | The compaction loop continues to absorb the volume. Disk usage stays ~bounded; the alert auto-clears once the rate drops. | None — the validator is healthy. Use when the flood is short-lived (e.g. < 1h). |
| **Tighten softCap**                     | More aggressive trimming, smaller per-rewrite cost.          | More frequent rewrites, slightly higher background I/O. |
| **Apply libp2p / mempool rate-limit**   | Throttle the offending miner upstream of the verifier.       | Affects ALL traffic from that peer, not just rejections. Acceptable if §3.3 identified a single hostile miner; problematic for a coordinated campaign of legitimate-looking peers. |

#### 3.4.2 Mode B (hard-cap dropping) — escalate

Mode B means the on-disk ceiling is actively shedding records. The
in-memory ring is unaffected, so live operator surfaces are accurate;
but a forensic post-mortem will be missing data for the duration of
the drop. Pick ONE of:

| Option                              | When to choose                                                                |
| ----------------------------------- | ----------------------------------------------------------------------------- |
| **Raise `cfg.RecentRejectionsMaxBytes`** | You have headroom in your disk budget. Restart the validator to apply (config-reload is not yet supported for this field as of 2026-04-30). |
| **Raise softCap**                   | The soft-cap loop is running but each rewrite is too small. Larger softCap means each rewrite trims more, amortising the I/O cost. Same restart caveat. |
| **Tighten `cfg.RecentRejectionsRateLimitPerSec`** | The hard-cap is breached because per-miner traffic is too high; cut admission upstream rather than chasing the disk. Restart caveat applies. |
| **Apply libp2p / mempool rate-limit** | Same as Mode A — but here it is the immediate-action choice if you cannot restart the validator. |
| **Slash the offender** (§4)         | The flood is sustained, the offender is identified, and you have governance authority to file a slash transaction. |

#### 3.4.3 Mode C (rate-limit dropping) — usually self-resolving

Mode C means the per-miner token-bucket limiter is actively dropping
records — the validator's earliest defence is doing its job. The ring
and the persister are both unaffected, so live operator surfaces are
accurate AND the on-disk forensic record is complete (modulo the
records the limiter shed, which by definition were redundant
volume from a single offender).

The expected operator response is light:

| Option                              | When to choose                                                                |
| ----------------------------------- | ----------------------------------------------------------------------------- |
| **Wait** (no action)                | The limiter is shedding the offender's surplus; ring/persister load stays at baseline. The alert auto-clears once the offender backs off or is otherwise mitigated. Use when §3.3 identifies a single offender and Modes A and B are both flat. |
| **Apply libp2p / mempool rate-limit** | The drops have persisted across multiple alert cycles AND the offending `miner_addr` is identified — cut at the network gate so even the limiter does not need to keep counting. |
| **Slash the offender** (§4)         | Same triggers as Mode A/B: sustained, identified, you have authority. |
| **Loosen `cfg.RecentRejectionsRateLimitPerSec`** | The drops are spread across MANY `miner_addr`s — the limiter is too tight for legitimate fleet behaviour (e.g. a CI run, a shared staging cluster). Restart caveat applies. |

If Mode C fires alongside Mode A or Mode B the limiter is sparing the
ring/persister but not enough — the offender's ALLOWED rate is still
saturating downstream defences. Tighten the rate-limit AND apply the
relevant Mode-A or Mode-B mitigation.

---

## 4. Escalation: §4.6 slashing

If §3.3 identifies a single sustained offender and you have authority
to file slash evidence, the v2 mining stack already supports this
end-to-end:

1. Inspect the offender's recent records (client-side `miner_addr`
   filter — the v1 endpoint's server-side filters are `kind` /
   `reason` / `arch` / `since` only, deliberately keeping the wire
   shape narrow):

   ```bash
   curl -s 'http://<validator-host>:8080/api/v1/attest/recent-rejections?limit=500' \
     | jq --arg addr "<addr>" '.records | map(select(.miner_addr == $addr))'
   ```

2. Pick a representative record and submit a slash transaction
   referencing it. See [`MINING_PROTOCOL_V2.md`](../MINING_PROTOCOL_V2.md)
   §5 for the slash-evidence schema and authority list.

3. Verify the slash receipt landed:

   ```bash
   curl -s 'http://<validator-host>:8080/api/v1/mining/slash/<tx-id>'
   ```

4. Coordinate with at least one peer validator before submission —
   slash evidence is consensus state, and a single-validator slash
   without peer review is operationally rude even when correct.

---

## 5. Worked example

A coordinated `archspoof_gpu_name_mismatch` flood from a single peer.

**14:02 UTC** — `QSDMAttestRejectionPersistCompactionsHigh` fires.
PagerDuty pages on-call.

**14:03 UTC** — Operator opens the dashboard. Compactions cell is
climbing (~ 8/min); records-on-disk is at 1024 (== softCap). Top
offenders strip: `qsdm1xyz...` with 47 of the last 50 rejections.
Hard-cap drops: 0.

**14:04 UTC** — Operator confirms with PromQL:
```promql
rate(qsdm_attestation_rejected_total{kind="archspoof_gpu_name_mismatch"}[5m]) by (kind)
```
Returns ~ 130/s for that kind, baseline being < 1/s.

**14:05 UTC** — Operator pages a peer validator via Slack. Peer
confirms they see the same flood from the same miner address. Both
agree this is sustained-and-clear-cut.

**14:08 UTC** — Operator files a slash transaction referencing one
of the rejection records (`Seq=8423`).

**14:11 UTC** — Slash receipt visible on both validators. The
offending miner's enrollment is marked slashed; subsequent proof
submissions from that address are rejected at the enrollment layer
before they ever reach §4.6 verification.

**14:14 UTC** — Compaction rate decays to baseline. Alert
auto-resolves. Total operator-time: ~12 minutes.

---

## 6. After the incident

- Capture the dashboard tile screenshot for the post-mortem doc. The
  attestation-rejections card includes an **⬇ export CSV** link that
  emits the full record set for the on-screen page; pair it with the
  full JSONL from `cfg.RecentRejectionsPath` for an exhaustive record.
- File the slash receipt and the rejection-record CSV together as the
  evidence bundle. Both are reproducible from chain state + the
  on-disk log; the bundle is for human review during a governance
  audit, not for chain replay.
- If `cfg.RecentRejectionsMaxBytes` was set too tight (i.e. Mode B
  fired during the incident), re-tune. The recommended starting point
  is `MaxBytes = 16 * softCap * average_record_size` ≈ 16x the
  soft-cap working set. At the default `softCap=1024` and ~512 bytes
  per record that is 8 MiB.
- If a fresh GPU architecture string is responsible for an
  `archspoof_unknown_arch` flood that turns out to be legitimate
  (i.e. a miner running a real GPU your validator does not yet know
  about), file a doc/code update against `pkg/api`'s
  `recentRejectionArches` allowlist. Treat that as a software-bug
  triage path, not a security incident.

---

## 7. Mode anchors

The five alerts in the
`qsdm-v2-attest-recent-rejections` group each have a
dedicated `runbook_url` deep-link into the section
below. Sections collect the mode-specific entry
points so an operator paged on a single alert lands
on the relevant text rather than scrolling the
whole runbook.

### 7.1. Mode A — `QSDMAttestRejectionPersistCompactionsHigh`

The soft-cap rewrite loop is keeping up but the rate
is anomalously high (>5 compactions/min sustained
for 30m). The validator is healthy; the **volume**
is the signal. Expect the
`qsdm_attest_rejection_persist_records_on_disk`
gauge to hover near `softCap` during the spike.

- **Triage entry point:** §3.1 (confirm flood),
  §3.2 (dominant kind), §3.3 (offending miner).
- **Mitigation:** §3.4 — operator policy. Either
  tighten the §4.6 cap, raise softCap, or apply a
  P2P / mempool rate-limit on the dominant
  miner_addr.
- **Promotion path:** if compactions stay high AND
  the persister starts dropping records, the
  incident has promoted to Mode B — switch to §7.2
  below.

### 7.2. Mode B — `QSDMAttestRejectionPersistHardCapDropping`

The persister is **shedding records** at the
hard byte cap. The soft-cap rewrite loop is no
longer freeing enough headroom on each pass; the
on-disk JSONL log is no longer a complete forensic
record. Mode B is strictly worse than Mode A.

- **In-memory ring is unaffected.** The dashboard
  tile and `/api/v1/attest/recent-rejections`
  continue to surface every record. Only the
  durable forensic-record persistence is being
  dropped.
- **Triage entry point:** §3 — same flow as Mode A,
  but the `hard-cap drops` cell on the dashboard
  is red.
- **Mitigation:** §3.4 row "hard-cap dropping" —
  raise `cfg.RecentRejectionsMaxBytes` if disk
  budget allows, OR raise `softCap` so each rewrite
  trims more, OR apply the libp2p / mempool
  rate-limit. Pick one based on the dominant
  source distribution.

### 7.3. Mode C — `QSDMAttestRejectionPerMinerRateLimited`

The per-miner token-bucket limiter
(`recentrejects.SetRateLimit`, wired via
`cfg.RecentRejectionsRateLimitPerSec`) is dropping
records at `Store.Record()` entry. Neither the ring
nor the persister sees those records — the limiter
is the **earliest defence** in the stack.

When Mode C fires alone (Modes A and B both flat),
this is the **cleanest "single bad actor" signal in
the stack** — the limiter has identified one
saturating source and is sparing the rest of the
pipeline.

- **Triage entry point:** §3.3 — the dashboard's
  "top offenders" strip names the saturating
  `miner_addr` directly.
- **Mitigation:**
  - Limiter doing its job ⇒ apply policy at the
    libp2p / mempool gate so the offender's traffic
    drops before it even reaches the limiter.
  - Limiter rate appears too tight (broad
    distribution of drops, no obvious top
    offender) ⇒ relax `cfg.RecentRejectionsRateLimitPerSec`,
    OR cross-check `qsdm_p2p_peers_connected` for
    botnet-shaped peer churn.

### 7.4. Mode D — `QSDMAttestRejectionFieldTruncationSustained`

The rejection ring's per-field rune-cap is
truncating records at >25% of observed rejections
for 15m. This is **not** a rejection-volume alert
(Modes A/B/C cover that); it's a rejection-payload-
shape alert.

The ring's per-field caps are pinned in code
(`maxDetailRunes=200` / `maxGPUNameRunes=256` /
`maxCertSubjectRunes=256`) and the truncation
machinery silently shortens any field that
overflows. Mode D fires when truncations are no
longer occasional one-offs — a sustained 25%+ rate
means something has changed about the rejection
payload distribution.

#### Triage

```promql
# Which field is dominant?
topk(3, rate(qsdm_attest_rejection_field_truncated_total[10m]))

# How close to the cap are observations?
qsdm_attest_rejection_field_runes_max
```

| Dominant `field` | Probable cause |
|---|---|
| `detail` (cap 200) | A recent verifier release emits longer `RejectError.Detail` strings; OR a miner is intentionally stuffing the proof envelope with oversized payloads to drown debug signal |
| `gpu_name` (cap 256) | Unlikely to be operator-side — gpu_name is short by NVIDIA convention; sustained truncation here is suspicious. Cross-check Modes B/C in [`ARCH_SPOOF_INCIDENT.md`](ARCH_SPOOF_INCIDENT.md) |
| `cert_subject` (cap 256) | Most often: multi-byte unicode in CN/SAN of a real NVIDIA cert pushes byte count past rune count. Operationally fine but worth verifying via `qsdm_attest_rejection_field_runes_max` |

#### Mitigation

- **Verifier release skew:** raise the cap in
  `pkg/mining/attest/recentrejects/recentrejects.go`
  (one-line change + CHANGELOG entry) and re-roll.
- **Miner stuffing:** identify the offender via
  the rejection-ring tile; the dominant `miner_addr`
  with truncation events is the source. Escalate
  via the slashing pipeline if sustained from one
  NodeID.
- **Multi-byte unicode (cert_subject):** no chain-
  side action. Document the operator's cert chain
  for future reference.

### 7.5. Mode E — `QSDMAttestRejectionFieldRunesMaxNearCap`

`qsdm_attest_rejection_field_runes_max` is sitting
within 10% of the in-store cap for 30m+. **Severity:
info — should NOT page**; wire to a passive channel
(chat ping, dashboard cell) so operators see the
ramp before Mode D paints.

This is the leading-indicator alert — the
process-lifetime max-runes-observed gauge has
crossed the 90% threshold for the relevant cap
(`detail≥180`, `gpu_name≥230`, `cert_subject≥230`).

#### Action

- **No incident.** Mode E is informational; the ring
  continues to truncate harmlessly.
- **Operator decision:** if observed proofs are
  consistently approaching the cap, consider
  whether the cap should be raised in the
  recentrejects source. Mode D is the page that
  fires if you don't act.
- **Cross-reference Mode D's cause table** for the
  same `field` label — the leading-indicator and
  the page share the same root causes.

---

## 8. Cross-references

- Alert source —
  [`QSDM/deploy/prometheus/alerts_qsdm.example.yml`](../../../deploy/prometheus/alerts_qsdm.example.yml)
- Persister implementation —
  `QSDM/source/pkg/mining/attest/recentrejects/persistence.go`
- Wiring config (`RecentRejectionsPath`, `RecentRejectionsMaxBytes`,
  `RecentRejectionsRateLimitPerSec`, `RecentRejectionsRateLimitBurst`,
  `RecentRejectionsRateLimitIdleTTL`) —
  `QSDM/source/internal/v2wiring/v2wiring.go`
- Dashboard tile —
  `QSDM/source/internal/dashboard/static/dashboard.js`
  (function `updateAttestRejections`)
- §4.6 kind allowlist —
  `QSDM/source/pkg/api/handlers_recent_rejections.go`
  (`recentRejectionKinds`)
- Slash transaction schema —
  [`MINING_PROTOCOL_V2.md`](../MINING_PROTOCOL_V2.md) §5
- Operator entry point —
  [`OPERATOR_GUIDE.md`](../OPERATOR_GUIDE.md)
- Companion runbooks:
  - [`ARCH_SPOOF_INCIDENT.md`](ARCH_SPOOF_INCIDENT.md)
    — when the rejection-ring volume comes from
    arch-spoof rejections specifically
  - [`SLASHING_INCIDENT.md`](SLASHING_INCIDENT.md) —
    the slash-evidence escalation path for sustained
    single-miner activity
