# Runbook — §4.6 Attestation Rejection Flood

**Audience:** validator operators on call for a single QSDM node or a
fleet thereof.

**Trigger:** one or both of these alerts firing on
[`alerts_qsdm.example.yml`](../../../deploy/prometheus/alerts_qsdm.example.yml):

- `QSDMAttestRejectionPersistCompactionsHigh` (severity warning)
- `QSDMAttestRejectionPersistHardCapDropping` (severity warning)

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

The persister is bounded by **two** caps in series:

| Cap         | Unit    | Default        | Set by                            | Defence depth |
| ----------- | ------- | -------------- | --------------------------------- | ------------- |
| **softCap** | records | 1024 records   | `recentrejects.DefaultPersistSoftCap` | First — triggers compaction (read-all, keep-last-N, atomic-rename rewrite) once per `softCap`-many appends |
| **maxBytes** | bytes  | 0 (= disabled) | `cfg.RecentRejectionsMaxBytes`    | Second — refuses an Append outright when admitting it would breach the byte ceiling AND a salvage compaction failed to free enough headroom |

A "rejection flood" is operator-jargon for the scenario where one or
more miners are submitting forged proofs faster than the validator can
trim the on-disk log. There are two failure modes:

- **Mode A (caught by `QSDMAttestRejectionPersistCompactionsHigh`):**
  the soft-cap rewrite loop is keeping up, but the rate is anomalously
  high — typically `>5 compactions/min` sustained for 30m. The
  validator is healthy; the volume is the signal.
- **Mode B (caught by `QSDMAttestRejectionPersistHardCapDropping`):**
  the soft-cap rewrite loop is NOT keeping up. The hard byte cap has
  refused at least one record over the last 10m. Forensic durability
  is being shed — the in-memory ring still receives every record, but
  the on-disk JSONL log is no longer a complete history.

Mode B is strictly worse than Mode A, and only fires when `maxBytes`
is configured. A node with `cfg.RecentRejectionsMaxBytes == 0` (the
default) will never see Mode B — but it ALSO has no upper bound on
disk consumption, which is why production operators are encouraged to
set the cap.

---

## 2. Symptoms

### 2.1 Dashboard tile

Open the operator dashboard (default
`http://<validator-host>:8080/`) and locate the **🛑 Attestation
Rejections** card. The persistence-lifecycle row carries four cells:

| Cell                  | Healthy        | Mode A (compactions high)  | Mode B (hard-cap dropping)              |
| --------------------- | -------------- | -------------------------- | --------------------------------------- |
| **persist errors**    | 0 (green)      | 0 (green)                  | 0 — possibly non-zero on a contemporaneous I/O flap |
| **compactions**       | low/stable     | climbing fast              | climbing AND records-on-disk plateaued near MaxBytes/recordSize |
| **records on disk**   | ≤ softCap      | hovering near softCap      | hovering near MaxBytes/recordSize       |
| **hard-cap drops**    | 0 (green)      | 0 (green)                  | **non-zero (red)**                      |

If "hard-cap drops" is red, you are in Mode B. Otherwise check the
**compactions** cell against your baseline — anything more than ~1×
the typical rate is worth investigating even before
`QSDMAttestRejectionPersistCompactionsHigh` fires.

### 2.2 Prometheus

The four series operators read during this incident:

```promql
# Compaction rate (Mode A trigger)
rate(qsdm_attest_rejection_persist_compactions_total[5m]) * 60

# Hard-cap drop rate (Mode B trigger)
rate(qsdm_attest_rejection_persist_hardcap_drops_total[5m])

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
| **Apply libp2p / mempool rate-limit** | Same as Mode A — but here it is the immediate-action choice if you cannot restart the validator. |
| **Slash the offender** (§4)         | The flood is sustained, the offender is identified, and you have governance authority to file a slash transaction. |

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

## 7. Cross-references

- Alert source —
  [`QSDM/deploy/prometheus/alerts_qsdm.example.yml`](../../../deploy/prometheus/alerts_qsdm.example.yml)
- Persister implementation —
  `QSDM/source/pkg/mining/attest/recentrejects/persistence.go`
- Wiring config (`RecentRejectionsPath`, `RecentRejectionsMaxBytes`) —
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
