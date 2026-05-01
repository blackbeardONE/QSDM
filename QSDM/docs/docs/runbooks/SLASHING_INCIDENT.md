# Runbook — v2-Mining Slashing Incident

**Audience:** validator operators on call for a single QSDM node or a
fleet thereof, plus protocol engineers responding to a "the slasher
just nuked half the fleet" page.

**Trigger:** one or more of these alerts firing on
[`alerts_qsdm.example.yml`](../../../deploy/prometheus/alerts_qsdm.example.yml):

- `QSDMMiningSlashApplied` (severity warning) — Mode A: any successful slash
- `QSDMMiningSlashedDustBurst` (severity **critical**) — Mode B: ≥50 CELL drained in 15m
- `QSDMMiningSlashRejectionsBurst` (severity warning) — Mode C: ≥10 slashes rejected in 10m
- `QSDMMiningAutoRevokeBurst` (severity **critical**) — Mode D: ≥3 records auto-revoked in 15m

**Estimated time to resolve:** 5–10 minutes for Mode A (single legitimate
slash); 15–60 minutes for Mode B/D when triaging coordinated cheating vs.
verifier regression; 30 minutes to several hours when a verifier rollback
is needed.

---

## 1. What is a "slashing incident"?

QSDM's v2-mining protocol enforces NVIDIA-locked attestation honesty by
debiting an offender's stake when their proofs are forged, double-mined,
or stale. The chain-side path is in
[`pkg/chain/slash_apply.go`](../../../source/pkg/chain/slash_apply.go);
operator-facing receipts are stored in
[`pkg/chain/slash_receipts.go`](../../../source/pkg/chain/slash_receipts.go);
the per-receipt query lives at
`GET /api/v1/mining/slash/{tx_id}` and the dashboard tile at
`GET /api/mining/slash-receipts`.

There are FOUR independent failure modes worth paging on, only some of
which are "incidents" in the malicious-activity sense:

| Mode | Alert                            | Scenario                                                                        | Action class            |
| ---- | -------------------------------- | ------------------------------------------------------------------------------- | ----------------------- |
| **A** | `QSDMMiningSlashApplied`        | One or more honest slashes applied — a real cheater was caught                  | Confirm + ratify        |
| **B** | `QSDMMiningSlashedDustBurst`    | Mass-slash event (≥50 CELL drained in 15m). Coordinated ring **OR** verifier regression | Ratify or rollback     |
| **C** | `QSDMMiningSlashRejectionsBurst` | Outsider probing the slash endpoint, slasher-tool bug, or replay flood          | Identify and rate-limit |
| **D** | `QSDMMiningAutoRevokeBurst`     | Multiple miners pushed under `MIN_ENROLL_STAKE` simultaneously                  | Likely escalates Mode B |

Mode A is the **happy path** for a chain that's working — the protocol
caught a cheater and the operator's job is to confirm, not to roll
anything back. Modes B and D are the truly dangerous ones because the
"verifier regression" branch involves rolling back a release; a wrong
call here loses real money for honest miners. Mode C is rarely an
emergency on its own but is a leading indicator for the other three when
a sophisticated attacker is probing the slasher endpoint to learn its
admission gate behaviour.

---

## 2. Symptoms

### 2.1 Dashboard tile

Open the operator dashboard (default
`http://<validator-host>:8080/`) and locate the **⚖️ Slashing Pipeline**
card. Five counter cells form the at-a-glance signal:

| Cell             | Healthy           | Mode A                       | Mode B (cheat ring)               | Mode B (verifier regression)         | Mode C (probe / spam)         | Mode D (auto-revoke burst)              |
| ---------------- | ----------------- | ---------------------------- | --------------------------------- | ------------------------------------ | ----------------------------- | --------------------------------------- |
| **applied**      | ≤1/day, all kinds | climbing slowly, mixed kinds | **climbing fast, mixed kinds**    | **climbing fast, single kind dominant** | flat                          | climbing fast (multiple kinds, often)   |
| **drained dust** | ≤1 CELL/day       | ≤1 CELL/event                | **≥50 CELL/15m, multiple targets** | **≥50 CELL/15m, multiple targets**   | flat                          | ≥30 CELL/15m, all targets pushed under  |
| **reward / burn**| flat              | small bump                   | large bumps                       | large bumps                          | flat                          | large bumps                             |
| **rejected**     | flat              | flat                         | flat                              | flat                                 | **climbing, often single reason** | flat                                |
| **auto-revoked** | flat              | maybe one fully_drained      | several                           | several                              | flat                          | **3+ in 15m (the alert trigger)**       |

Combine with the receipts table immediately below — the **Top slashed
NodeIDs** strip and the per-row **Evidence** + **Detail** columns answer
the next question every time: "is this concentrated on one rig or
spread across the fleet?"

- **Concentrated on 1–3 NodeIDs** → almost certainly a real cheat
  (Modes A or D), or a single-rig firmware bug.
- **Spread across many NodeIDs in the same EvidenceKind** → almost
  certainly a verifier regression. Treat as Mode B-rollback until
  proven otherwise.

### 2.2 Prometheus series

The dashboard tile is a snapshot view; for time-series triage use the
underlying counters from
[`pkg/monitoring/slashing_metrics.go`](../../../source/pkg/monitoring/slashing_metrics.go):

```promql
# Per-kind applied rate (Mode A/B signal)
sum by (kind) (rate(qsdm_slash_applied_total[5m]))

# Per-kind drained dust (CELL = dust / 1e9)
sum by (kind) (increase(qsdm_slash_drained_dust_total[15m])) / 1e9

# Per-reason reject rate (Mode C signal)
sum by (reason) (rate(qsdm_slash_rejected_total[5m]))

# Auto-revoke pressure (Mode D signal)
sum by (reason) (increase(qsdm_slash_auto_revoked_total[15m]))

# Reward / burn split (sanity check on slash arithmetic)
rate(qsdm_slash_rewarded_dust_total[5m])
rate(qsdm_slash_burned_dust_total[5m])
```

If the rejected-by-reason series is dominated by `verifier_failed` with
no rise in `qsdm_slash_applied_total`, an outsider is probing the
endpoint with bad evidence — Mode C. If `evidence_replayed` dominates,
a slasher tool is retrying without re-encoding, which is benign but
worth fixing client-side.

---

## 3. Triage flow

```
       ┌───────────────────────────────────────────┐
       │ Page received: which alert fired?         │
       └────────────┬──────────────────────────────┘
                    │
         ┌──────────┼──────────┬───────────────────┐
         │          │          │                   │
         ▼          ▼          ▼                   ▼
   QSDMMining   QSDMMining   QSDMMining        QSDMMining
   SlashApplied SlashedDust  SlashRejections   AutoRevoke
   (Mode A)     Burst        Burst             Burst
                (Mode B)     (Mode C)          (Mode D)
                    │          │                   │
                    │          │                   │
                    ▼          ▼                   ▼
              §3.2 Mode B   §3.3 Mode C        §3.4 Mode D
              (verifier      (probe / spam)     (mass auto-
              regression                          revoke)
              vs. real
              cheat ring)
                    │
                    ▼
              §3.2.4 Verifier rollback procedure
              (if regression confirmed)
```

### 3.1 Mode A — `QSDMMiningSlashApplied`

A single slash transaction landed. Walk the receipt:

1. Identify the tx_id from the alert page (or the dashboard tile's most
   recent row). Pull the full receipt:

   ```bash
   curl -s -H "Authorization: Bearer $QSDM_OPERATOR_TOKEN" \
     http://<validator>:8080/api/v1/mining/slash/$TXID | jq .
   ```

   Or via the CLI: `qsdmcli slash-receipt <tx-id>`.

2. Confirm the EvidenceKind:
   - `forged-attestation` — CC-AIK signature did not validate. Real
     forgery, OR (rare) a verifier regression. Distinguish by Mode B
     vs. Mode A: Mode A means it's an isolated rig.
   - `double-mining` — same proof submitted to two parents. Real
     double-spending; almost never a false positive.
   - `freshness-cheat` — proof staler than the freshness window. Real
     replay; almost never a false positive.

3. Confirm the offender's NodeID matches a registered rig you would
   expect to be slashable (i.e. `phase=active` at slash time, not in
   unbond). Cross-check at:

   ```bash
   curl -s http://<validator>:8080/api/v1/mining/enrollment/$NODE_ID | jq .
   ```

4. Confirm `auto_revoked` matches expectations:
   - `false` — offender retains some bond; expect them to retry honestly
     or stake-up.
   - `true` — offender pushed under `MIN_ENROLL_STAKE`; record moved to
     unbond. Expect Mode D to fire if 3+ revokes happen in 15m.

5. **No action required if all four checks pass.** A Mode A page is the
   alert system saying "the chain caught a cheater, here's the receipt"
   — file the tx_id with the ops journal and resolve the alert.

### 3.2 Mode B — `QSDMMiningSlashedDustBurst`

≥50 CELL drained in a 15m window across multiple receipts. This is the
hard one. Two scenarios:

**B1: Coordinated cheat ring.** Several rigs are independently forging
and the protocol is correctly slashing them all. EvidenceKinds will
be **mixed** (forged + double-mining + freshness, with no single kind
dominating). Top-3 slashed NodeIDs will be a small set of clearly-malicious
rigs. **Action: ratify and continue.** File an ops journal entry; the
chain's working as designed.

**B2: Verifier regression.** A recent release of the verifier produced
false-positive rejections, and now the slasher is incorrectly converting
those rejections into slashes. EvidenceKind will be **single-kind dominant**
— almost always `forged-attestation` because the CC-AIK path is the most
sensitive to verifier code changes. Top-3 NodeIDs will be a long tail
because honest-rig forgeries are evenly distributed across the fleet.

#### 3.2.1 Distinguishing B1 from B2

```promql
# Single dominant kind = single-source bug = verifier regression suspect.
sum by (kind) (increase(qsdm_slash_drained_dust_total[15m]))
```

- **One kind has >80% of drained dust** → suspect B2.
- **Two or three kinds each have >15%** → suspect B1.

Cross-check with pre-release baseline:

```promql
# Compare 1d-ago lookback. If today is >5× the historical rate
# in a SINGLE kind, the regression hypothesis dominates.
sum by (kind) (increase(qsdm_slash_drained_dust_total[1h]))
sum by (kind) (increase(qsdm_slash_drained_dust_total[1h] offset 1d))
```

Spot-check 3 receipts on the long tail:

```bash
for txid in $TX1 $TX2 $TX3; do
  curl -s -H "Authorization: Bearer $QSDM_OPERATOR_TOKEN" \
    http://<validator>:8080/api/v1/mining/slash/$txid | jq '{tx_id, node_id, evidence_kind, slasher}'
done
```

If the offender NodeIDs are operators you have direct contact with AND
they swear their rigs were honest, **the regression hypothesis is
confirmed**. Move to §3.2.4.

#### 3.2.2 Mitigation if B1 (real cheating)

- Continue the slash flow — the chain is doing its job.
- File an ops journal entry naming the offender NodeIDs.
- If the cheat ring is large (≥10 rigs), consider tightening the
  evidence-verification thresholds in the next release cycle so the
  margin shrinks; but DO NOT change them mid-incident — this would
  introduce a verifier regression while you're investigating one.

#### 3.2.3 Mitigation if B2 (verifier regression suspected, not yet confirmed)

- **Pause new slash submissions** at the slasher-tool layer (NOT at the
  chain — the chain MUST keep applying pre-existing slashes until the
  rollback ships, otherwise a real attacker can use the pause window to
  cheat). Slasher tooling should be wrapped behind a kill-switch
  precisely for this case.
- Page the protocol-engineering on-call.
- Continue collecting receipts so the post-mortem can quantify the
  blast radius.

#### 3.2.4 Verifier rollback procedure

If §3.2.1 confirms a regression:

1. Identify the last "known good" verifier release tag from the deploy
   journal.
2. Roll the validator binaries back. The chain-state tolerates a verifier
   downgrade because the verifier is admission-gate-only — already-
   applied slashes do not un-apply.
3. **Compensate slashed honest miners off-chain.** The chain has no
   un-slash transaction; rebates go through the operator-controlled
   stake-top-up flow. Track every refund in the ops journal so the
   regression's economic blast radius is auditable.
4. File a post-mortem against the regression-introducing PR. Add a
   regression test reproducing the false-positive evidence so the same
   verifier flaw cannot reach production again.

### 3.3 Mode C — `QSDMMiningSlashRejectionsBurst`

≥10 slashes rejected in 10m. Cheap to triage.

```promql
sum by (reason) (increase(qsdm_slash_rejected_total[10m]))
```

Reason → meaning → action:

| Reason                  | Meaning                                                              | Action |
| ----------------------- | -------------------------------------------------------------------- | ------ |
| `verifier_failed`       | Evidence didn't pass the EvidenceVerifier — malformed or a probe      | If sustained with no `applied` rises, an outsider is probing the slasher endpoint. Consider rate-limiting slash submissions upstream of admission. |
| `evidence_replayed`     | Replay protection working; the slasher tool is retrying              | Fix the slasher tool client-side — it should drop after the first `evidence_replayed` response. |
| `node_not_enrolled`     | Slash submitted against an unknown NodeID                            | Client bug or spam; file a low-severity bug if originated from an operator-controlled tool, otherwise ignore. |
| `decode_failed`         | Slash payload failed to deserialise                                  | Tooling bug; fix the encoder. |
| `fee_invalid`           | Slasher reward exceeded the slashed dust                             | Tooling bug; clamp the reward client-side. |
| `wrong_contract`        | Slash submitted to the wrong contract address                        | Tooling bug; update the slasher's hard-coded address. |
| `state_lookup_failed`   | Validator could not read the offender's enrollment state             | Validator-side problem; check disk + indexer health. |
| `stake_mutation_failed` | Validator could not write the post-slash state                       | Validator-side problem; check disk + db health. |
| `other`                 | Anything not in the closed enum (forward-compat bucket)              | Investigate by tail-grepping the validator's slash-apply logs. |

A sustained `verifier_failed` rate without any `applied` rises is the
canonical Mode C signature — someone outside the trust circle is
probing the slasher endpoint with handcrafted bad evidence. Production
deployments should gate slash submission behind authentication
upstream of the chain (e.g. only the `slasher_address` set is allowed
to POST `/api/v1/mining/slash`); if your deployment doesn't, this
alert is the first reason to add it.

### 3.4 Mode D — `QSDMMiningAutoRevokeBurst`

≥3 records auto-revoked in 15m. This means three independent miners
were slashed below `MIN_ENROLL_STAKE` (10 CELL) within the window.

Almost always **escalates Mode B** (the same cheat-ring or verifier-
regression event that caused the dust burst is now pushing miners off
the active set). Two non-Mode-B scenarios worth checking:

1. **Slash arithmetic bug:** the `ApplySlashTx` path is double-debiting,
   pushing miners under the threshold faster than expected. Verify by
   comparing pre-slash bond + slash amount against post-slash bond on
   each affected receipt:

   ```bash
   # If `slashed_dust + auto_revoke_remaining_dust != pre-slash bond`,
   # ApplySlashTx is debiting twice.
   curl -s ... /api/v1/mining/slash/$TXID | jq '{slashed_dust, auto_revoke_remaining_dust}'
   ```

2. **Stake-stripping attack:** a malicious slasher is intentionally
   submitting maximum-amount slashes to push enrolled miners off the
   active set rather than to recover dust. Verify by checking the
   slasher address — if the same address is responsible for all 3+
   revokes:

   ```bash
   # All revokes should NOT come from one slasher address absent a
   # legitimate stake-stripping campaign.
   for txid in $TX1 $TX2 $TX3; do
     curl -s ... /api/v1/mining/slash/$txid | jq -r '.slasher'
   done | sort | uniq -c
   ```

   If a single slasher is responsible, isolate that address and consider
   blacklisting at the slash-tx admission gate. Slasher addresses are
   not pseudonymous — they are tracked and operator-controlled.

---

## 4. Reference

- **Source files:**
  - [`pkg/chain/slash_apply.go`](../../../source/pkg/chain/slash_apply.go) — slasher pipeline
  - [`pkg/chain/slash_receipts.go`](../../../source/pkg/chain/slash_receipts.go) — bounded receipt store + `List()` for the dashboard tile
  - [`pkg/monitoring/slashing_metrics.go`](../../../source/pkg/monitoring/slashing_metrics.go) — Prometheus counters + `SlashMetricsSnapshot`
  - [`internal/dashboard/slashing.go`](../../../source/internal/dashboard/slashing.go) — dashboard tile data endpoint
- **API endpoints:**
  - `GET /api/v1/mining/slash/{tx_id}` — single-receipt lookup (per receipt)
  - `GET /api/mining/slash-receipts` — paginated list (operator dashboard tile)
- **Prometheus series:**
  - `qsdm_slash_applied_total{kind=...}` — successful slashes by EvidenceKind
  - `qsdm_slash_drained_dust_total{kind=...}` — cumulative drained stake
  - `qsdm_slash_rewarded_dust_total` — total paid to slashers
  - `qsdm_slash_burned_dust_total` — total burned (drained – rewarded)
  - `qsdm_slash_rejected_total{reason=...}` — rejected slash submissions
  - `qsdm_slash_auto_revoked_total{reason=...}` — post-slash auto-revokes
- **Closed-enum values:**
  - EvidenceKinds: `forged-attestation`, `double-mining`, `freshness-cheat`
  - Reject reasons: `verifier_failed`, `evidence_replayed`, `node_not_enrolled`, `decode_failed`, `fee_invalid`, `wrong_contract`, `state_lookup_failed`, `stake_mutation_failed`, `other`
  - Auto-revoke reasons: `fully_drained`, `under_bonded`

## 5. Alert ↔ Mode quick-reference

| Alert                          | Mode | Severity | Triage section |
| ------------------------------ | ---- | -------- | -------------- |
| `QSDMMiningSlashApplied`       | A    | warning  | §3.1           |
| `QSDMMiningSlashedDustBurst`   | B    | critical | §3.2           |
| `QSDMMiningSlashRejectionsBurst` | C  | warning  | §3.3           |
| `QSDMMiningAutoRevokeBurst`    | D    | critical | §3.4           |

---

*Maintained alongside [`alerts_qsdm.example.yml`](../../../deploy/prometheus/alerts_qsdm.example.yml).
If you change a threshold there, update the "what triggers" wording here in the same commit.*
