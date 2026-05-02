# Operator Hygiene — Operator Runbook

Final coverage runbook for the 4 alerts that don't
fit into a dedicated subsystem runbook. All four are
**operator-resolvable without cross-team coordination** —
fix your config, fix your client, fix your storage
backend. None are paged-out-of-bed cascade triggers.

| Alert | Severity | Default `for:` | Anchor |
|---|---|---|---|
| `QSDMNvidiaLockHTTPBlocksSpike`     | warning | 10m | [§3.1](#31-mode-a--qsdmnvidialockhttpblocksspike) |
| `QSDMNvidiaLockP2PRejects`          | warning | 5m  | [§3.2](#32-mode-b--qsdmnvidialockp2prejects) |
| `QSDMAttestHashrateOutOfBand`       | warning | 10m | [§3.3](#33-mode-c--qsdmattesthashrateoutofband) |
| `QSDMNoTransactionsStored`          | warning | 30m | [§3.4](#34-mode-d--qsdmnotransactionsstored) |

> **Why a bundled hygiene runbook?** These four alerts
> are the **last 11% of `alerts_qsdm.example.yml`**
> after the per-subsystem runbooks (slashing,
> enrollment, mining-liveness, trust, NGC, quarantine,
> arch-spoof, rejection-flood, submesh-policy,
> governance, REJECTION_FLOOD modes D/E) shipped.
> They share three properties: (1) operationally
> *adjacent* to v1 NVIDIA-lock or v2 throughput
> sentinels, (2) **resolvable by the on-call alone**
> without escalation, and (3) lower-frequency than
> the cascade alerts so a single bundled runbook is
> the right granularity.

> **What this commit closes.** Coverage moves from
> 34/38 (89%) to **38/38 (100%)** — every alert in
> the v2 alerts file now carries an anchored
> `runbook_url`, and every runbook is bidirectionally
> cross-linked to its operationally-related
> companions.

---

## 1. Glossary (60-second skim)

- **NVIDIA-lock** — the operator-policy gate that
  requires a recent qualifying NGC proof bundle
  before allowing state-changing actions on the
  validator. Has two enforcement points:
  - **HTTP gate** (`enforceNvidiaLock` in
    `pkg/api/handlers.go`): every state-changing
    API call returns `403` if the gate is enabled
    and no qualifying proof is in the ring.
  - **P2P gate** (`NvidiaLockP2PGate` in
    `pkg/monitoring/nvidia_p2p_gate.go`): inbound
    libp2p-received transactions are dropped
    AFTER consensus validation if the gate is
    enabled and no qualifying proof is in the
    ring.
  - The two gates are **independent toggles**:
    `QSDM_NVIDIA_LOCK_ENABLED` for HTTP,
    `QSDM_NVIDIA_LOCK_GATE_P2P` for P2P. Both
    consult the same proof ring with `consume=false`
    on the P2P side (so single-use HTTP nonce
    behaviour stays independent).
- **Qualifying NGC proof** — a bundle in the proof
  ring (populated by
  [`NGC_SUBMISSION_INCIDENT.md`](NGC_SUBMISSION_INCIDENT.md)
  ingest) that satisfies the lock's policy:
  - Received within `QSDM_NVIDIA_LOCK_MAX_PROOF_AGE`
    (default 15 min).
  - JSON `architecture` field contains "nvidia"
    case-insensitive.
  - `gpu_fingerprint.available == true`.
  - If `QSDM_NVIDIA_LOCK_EXPECTED_NODE_ID` is set,
    the proof's `qsdm_node_id` (or legacy
    `qsdmplus_node_id`) matches.
  - If `QSDM_NVIDIA_LOCK_PROOF_HMAC_SECRET` is set,
    the proof's `qsdm_proof_hmac` validates.
- **Per-arch hashrate band** — the §4.6.3
  plausibility check on `Attestation.ClaimedHashrateHPS`.
  Five canonical arches each carry a `[Min, Max]`
  band sized to the consumer-product range:
  | Arch | Min | Max |
  |---|---|---|
  | `turing`       |  10 KH/s | 5 MH/s |
  | `ampere`       |  50 KH/s | 50 MH/s |
  | `ada-lovelace` | 100 KH/s | 50 MH/s |
  | `hopper`       |   1 MH/s | 200 MH/s |
  | `blackwell`    |   5 MH/s | 500 MH/s |
  Bands are intentionally wide (~100× range per
  arch). An out-of-band claim is either a units
  typo, a leaderboard-stuffing attempt, or a
  consensus-relevant signal that the proof itself
  is suspect.
- **Throughput sentinels** —
  - `qsdm_transactions_processed_total` — every
    inbound P2P message increments. Bumped at the
    very top of the dispatch path
    (`cmd/qsdm/main.go::SetMessageHandler`).
  - `qsdm_transactions_invalid_total` — bumped
    when any gate (WASM preflight / submesh / consensus
    / NVIDIA-lock P2P) rejects.
  - `qsdm_transactions_stored_total` — bumped
    only after a tx survives every gate AND
    `storage.StoreTransaction` succeeds.
  - **Mode D's signal** is the divergence between
    the first and last counter: processed > 0 but
    stored == 0 means the pipeline is admitting
    traffic but losing 100% of it somewhere
    between admission and storage.

---

## 2. First-90-seconds checklist

1. **Identify the mode.** The alert name maps 1:1
   to a mode anchor.

2. **None of these page through to other teams.**
   All four are operator-side fixes. The cascade
   maps in §4 below cover the rare cases where
   they fire alongside a runbook from another
   subsystem.

3. **Mode D is the one to read first if multiple
   are firing simultaneously.** It's the
   throughput sentinel — if Mode D fires, your
   chain is admitting txs but storing none, and
   the cause is almost always one of the other
   three modes (A/B for NVIDIA-lock policy
   choking the pipeline; C is unlikely but
   possible if every claimed hashrate is
   out-of-band).

4. **For Modes A and B, check the lock toggle
   first.** `QSDM_NVIDIA_LOCK_ENABLED` and
   `QSDM_NVIDIA_LOCK_GATE_P2P` are operator
   choices. If you didn't intend to enable the
   gate but it's blocking traffic, the fix is
   one env-var change and a restart.

---

## 3. Modes

### 3.1. Mode A — `QSDMNvidiaLockHTTPBlocksSpike`

`rate(qsdm_nvidia_lock_http_blocks_total[5m]) > 0.5`
for 10m. Severity: warning.

#### What triggered it

The HTTP NVIDIA-lock gate is returning `403`s on
state-changing API calls at >0.5 per second
sustained over 10 minutes (≈150 failures in 5
min). Each `403` carries a typed detail message
naming which guard tripped — the runbook's triage
table maps the four canonical detail strings to
their causes.

This alert is the **operator-policy enforcement
signal**: the gate is doing its job, but either
the operator's expectations are wrong (gate
enabled but the sidecar isn't posting) or a
client is hammering the gate without realizing
it's enabled.

#### Symptoms

- `qsdm_nvidia_lock_http_blocks_total` is
  incrementing.
- API clients receive `403 Forbidden` on every
  state-changing call (mint, token-create,
  wallet-send, etc.).
- The 403 response body contains one of the four
  canonical error detail messages (see triage
  table below).
- Read-only API calls (`GET /metrics`,
  `GET /api/v1/chain/height`, etc.) are
  unaffected — only state-changing routes are
  gated.

#### Triage — read the detail message

The 403 body names the failure mode in plain
text. Capture one and match against the table:

```bash
curl -sX POST http://127.0.0.1:8080/api/v1/wallet/send \
  -H 'Authorization: Bearer ...' \
  -d '{"to": "...", "amount": 1}' \
  | jq -r .error
```

| Detail substring | Probable cause | Action |
|---|---|---|
| `"no NGC proof bundles ingested"` | Sidecar isn't posting at all OR `QSDM_NGC_INGEST_SECRET` is unset (the ingest endpoint returns 404 silently — see [`NGC_SUBMISSION_INCIDENT.md` §3.2 "Class 4 — config"](NGC_SUBMISSION_INCIDENT.md#32-mode-b--qsdmngcproofingestrejectburst)) | Verify the sidecar is running; verify `QSDM_NGC_INGEST_SECRET` is set on this validator |
| `"no qualifying proof within window"` | Sidecar IS posting but every recent bundle fails the policy. Most often the bundle's `architecture` field doesn't contain "nvidia" (e.g. running an alternate sidecar on non-NVIDIA hardware) OR `gpu_fingerprint.available` is `false` (sidecar can't see the GPU) | Inspect a recent bundle: `curl -s /api/v1/monitoring/ngc-proofs \| jq '.bundles[-1]'`; check the `architecture` and `gpu_fingerprint` fields |
| `"matching qsdm_node_id / qsdmplus_node_id"` | `QSDM_NVIDIA_LOCK_EXPECTED_NODE_ID` on the validator doesn't match `QSDM_NGC_PROOF_NODE_ID` on the sidecar | Pick one canonical NodeID; align both env vars; restart whichever side changed |
| `"valid qsdm_proof_hmac / qsdmplus_proof_hmac"` | `QSDM_NVIDIA_LOCK_PROOF_HMAC_SECRET` (validator) ≠ `QSDM_NGC_PROOF_HMAC_SECRET` (sidecar). Same secret-rotation trap as [`NGC_SUBMISSION_INCIDENT.md` §3.2 Class 1](NGC_SUBMISSION_INCIDENT.md#32-mode-b--qsdmngcproofingestrejectburst) | Verify the secret matches; mid-rotation transition windows are the canonical false-positive |

#### Mitigation

- **Operator unintended-lock case:** if you didn't
  intend to enable the HTTP gate, set
  `QSDM_NVIDIA_LOCK_ENABLED=0` (or unset it) and
  restart. The gate is opt-in.
- **Sidecar fix:** restart the sidecar; rotate the
  appropriate env var; widen
  `QSDM_NVIDIA_LOCK_MAX_PROOF_AGE` if your
  sidecar has a slow cadence.
- **Don't reflexively disable the lock** in
  production. The gate exists to keep
  state-changing API calls behind a fresh-GPU
  attestation; disabling it removes that
  defence-in-depth layer.

#### Recovery validation

```promql
rate(qsdm_nvidia_lock_http_blocks_total[5m]) < 0.1
```

The alert auto-clears once the rate falls below
threshold for one full evaluation window past
`for: 10m`.

---

### 3.2. Mode B — `QSDMNvidiaLockP2PRejects`

`increase(qsdm_nvidia_lock_p2p_rejects_total[15m]) > 0`
for 5m. Severity: warning.

#### What triggered it

The P2P NVIDIA-lock gate dropped at least one
inbound libp2p transaction over the last 15
minutes. The gate runs **AFTER** consensus
validation, so the dropped tx had already passed
WASM preflight, submesh policy, AND the consensus
validator — the lock is the final gate before
storage. A single drop fires this alert.

#### How this differs from Mode A

| | Mode A (HTTP) | Mode B (P2P) |
|---|---|---|
| Endpoint | State-changing API routes | Inbound libp2p transactions |
| Toggle | `QSDM_NVIDIA_LOCK_ENABLED` | `QSDM_NVIDIA_LOCK_GATE_P2P` |
| Threshold | rate > 0.5/s | any drop |
| Visibility | Client gets 403 with detail | Silent drop (libp2p fire-and-forget) |
| Proof consumption | `consume=true` if ingest nonce required | `consume=false` always |
| Failure mode | Operator can see the 403 in their script | Producer has no client-visible signal |

The independence of the two toggles means
operators can run with HTTP gated but P2P open
(common — protect mint/token-create from the
internet but accept P2P txs from peers without
GPU policy), or vice versa.

#### Symptoms

- `qsdm_nvidia_lock_p2p_rejects_total` is
  incrementing.
- `qsdm_transactions_invalid_total` is also
  incrementing in lockstep (the P2P drop also
  bumps `IncrementTransactionsInvalid`).
- Logs contain `"NVIDIA-lock P2P gate not
  satisfied"` warnings with the upstream proof
  policy hint.

#### Triage

```promql
# Confirm the rate:
rate(qsdm_nvidia_lock_p2p_rejects_total[15m])

# Same proof-ring health checks as Mode A apply
# here — the P2P gate uses the same
# NvidiaLockProofOK function. If Mode A is also
# firing, the cause is shared (no qualifying
# proof in the ring).
rate(qsdm_nvidia_lock_http_blocks_total[5m])
```

| Pattern | Probable cause | Action |
|---|---|---|
| Mode A also firing | Shared root: no qualifying proof in the ring. Both gates are reading the same broken state | Triage Mode A first; Mode B clears as a follow-on |
| Mode A flat, Mode B alone | HTTP gate is **disabled** (`QSDM_NVIDIA_LOCK_ENABLED=0`) but P2P gate is **enabled** (`QSDM_NVIDIA_LOCK_GATE_P2P=1`). The validator accepts state-changing API calls without a proof but rejects P2P txs without one — an operator-policy decision | Confirm the asymmetry is intended. If yes, this alert IS the producer's only signal and should not be silenced; treat as operator outreach to peers running pure-relay nodes |
| P2P drops correlated with chain-height jumps | A reorg invalidated the proof ring's freshness; the next sidecar post will rewarm | Wait one sidecar cycle; alert auto-clears |
| P2P drops in a fresh validator with `QSDM_NVIDIA_LOCK_GATE_P2P=1` set unintentionally | The gate was enabled by accident (env-var typo, copy-paste from a config template) | Set `QSDM_NVIDIA_LOCK_GATE_P2P=0` and restart |

#### Mitigation

- **Operator unintended-gate case:** disable the
  P2P gate (`QSDM_NVIDIA_LOCK_GATE_P2P=0`,
  restart). Same opt-in semantics as the HTTP
  gate.
- **Cooperative mitigation:** the dropped peer
  has no client-visible feedback (libp2p drops
  silently). If you intend to keep the gate on
  AND want peers to participate, coordinate
  out-of-band: peers running their own sidecars
  + posting bundles to YOUR `/monitoring/ngc-proof`
  endpoint will satisfy the gate.

---

### 3.3. Mode C — `QSDMAttestHashrateOutOfBand`

`rate(qsdm_attest_hashrate_rejected_total[5m]) > 0.05`
for 10m. Severity: warning.

#### What triggered it

A miner is submitting `Attestation.ClaimedHashrateHPS`
values outside the per-arch `[Min, Max]` band for
the claimed `arch`. The threshold is sustained
0.05/s ≈ 15 rejections in 5m — lower than the
arch-spoof unknown-arch rule because hashrate
fabrication is **economic-cheating-flavoured**
rather than typo-flavoured.

The alert label `arch` carries which product line
is being targeted (the per-arch series gets its
own series in Prometheus, and the alert
description templates `{{ $labels.arch }}` so the
on-call sees which arch was hit).

#### Symptoms

- `qsdm_attest_hashrate_rejected_total{arch="<X>"}`
  is incrementing for one or more arches.
- The rejection appears in the §4.6 forensic ring
  (`/api/v1/attest/recent-rejections`) with the
  out-of-band hashrate value preserved.
- Possibly co-fires with a per-type attestation
  verifier rejection (the hashrate gate is
  upstream of the per-type verifier; if the
  hashrate fails, the per-type verifier never
  runs).

#### Triage — three distinct causes

```promql
# Which arch is hit?
sum by (arch) (rate(qsdm_attest_hashrate_rejected_total[5m]))

# Cross-reference the rejection ring for the
# specific value:
GET /api/v1/attest/recent-rejections?limit=20
# then grep for hashrate values; the band is
# embedded in the rejection detail.
```

| Pattern | Probable cause | Action |
|---|---|---|
| Single miner, value is several orders of magnitude OFF the band (e.g. claiming 1 MH/s on turing) | **Units typo.** Miner client is emitting H/min or kH/s as raw H/s | Reach out to the operator; the quickstart docs may need clarification on the units field. The miner's hardware is fine; only the reporting is wrong |
| Single miner, value is *just above* `Max` for the arch (e.g. claiming 6 MH/s on turing where Max is 5 MH/s) | **Leaderboard-stuffing attempt.** Inflate claimed hashrate to top the leaderboard without actually mining at that rate | If sustained from one NodeID, escalate to the slashing pipeline ([`SLASHING_INCIDENT.md`](SLASHING_INCIDENT.md)); the per-type verifier rejection that follows is the actual evidence |
| Multiple miners, one arch dominant | A new client release shipped with a wrong band table OR a new GPU SKU's hashrate genuinely exceeds the current band | Check release notes; if a legitimate hardware bump deserves a wider band, raise it in `pkg/mining/attest/archcheck` and ship a CHANGELOG entry |
| `arch="unknown"` dominant | Cross-reference [`ARCH_SPOOF_INCIDENT.md` §3.1 Mode A](ARCH_SPOOF_INCIDENT.md#31-mode-a--qsdmattestarchspoofunknownarchburst) — unknown_arch lands in the default switch branch and the hashrate band check returns false trivially | Triage as ARCH_SPOOF Mode A; the hashrate signal is a follow-on |

#### Companion runbook

[`ARCH_SPOOF_INCIDENT.md`](ARCH_SPOOF_INCIDENT.md)
covers the *complementary* arch-spoof family
(unknown_arch, gpu_name_mismatch,
cc_subject_mismatch). The hashrate-band check is
operationally distinct — a miner can spoof their
arch (Mode B in arch-spoof) AND submit a wildly
out-of-band hashrate, but the two failure modes
are evaluated independently. Sustained activity
from one NodeID across both runbooks is the
canonical "this miner is cheating across multiple
axes" pattern; cross-reference to slashing.

#### Mitigation

- **Units typo:** operator-side fix; quickstart
  docs update if the typo is widespread.
- **Leaderboard-stuffing:** slashing pipeline.
- **Band update needed:** raise the band; ship a
  CHANGELOG entry. The bands in the §1 glossary
  are the reference values.

---

### 3.4. Mode D — `QSDMNoTransactionsStored`

`rate(qsdm_transactions_stored_total[30m]) == 0
and rate(qsdm_transactions_processed_total[30m]) > 0`
for 30m. Severity: warning.

#### What triggered it

The validator is **receiving and processing
inbound P2P transactions but storing zero of
them** for 30 minutes. The expression is the
chain's **divergence sentinel** — if the
admission counter is moving but the storage
counter is flat, every tx is being dropped
somewhere in the pipeline between admission and
storage.

This alert exists because the symptom is silent
to operators: the validator looks healthy from
outside (peers connected, mempool churning, /metrics
returning data), but the on-disk ledger isn't
growing. Without this alert the operator could
miss it for hours.

#### Pipeline between admission and storage

The dispatch path
(`cmd/qsdm/transaction/transaction.go::HandleTransaction`)
runs every inbound tx through six gates in
order. A failure at any gate increments
`qsdm_transactions_invalid_total` (or its
subsystem-specific counterpart) and skips
storage:

```
1. ParseTransaction          → silent drop on parse failure
2. WASM preflight            → IncrementTransactionsInvalid
3. submesh policy match      → IncrementTransactionsInvalid +
                                qsdm_submesh_p2p_reject_*_total
4. consensus.ValidateTransaction → IncrementTransactionsInvalid
5. NvidiaLockP2PGate.Allows  → IncrementTransactionsInvalid +
                                qsdm_nvidia_lock_p2p_rejects_total
6. storage.StoreTransaction  → IncrementTransactionsStored on success
                                RecordError on failure
```

If `stored==0` but `processed>0`, exactly one of
these gates is rejecting 100% of traffic, OR
gate 6 (storage) is consistently erroring.

#### Triage — narrow the gate

```promql
# Where are the rejects landing?
rate(qsdm_transactions_invalid_total[30m])
rate(qsdm_submesh_p2p_reject_route_total[30m])
rate(qsdm_submesh_p2p_reject_size_total[30m])
rate(qsdm_nvidia_lock_p2p_rejects_total[30m])

# The dominant rejector names the gate:
#   submesh_p2p_reject_route   → submesh policy (no route match)
#   submesh_p2p_reject_size    → submesh policy (size cap)
#   nvidia_lock_p2p_rejects    → NVIDIA-lock P2P gate
#   transactions_invalid only  → WASM preflight OR consensus validator
#   ALL silent (counters flat) → storage backend erroring
```

| Dominant signal | Gate | Action |
|---|---|---|
| `qsdm_submesh_p2p_reject_*` matches `processed` rate | Submesh policy is rejecting every tx (fee/geotag mismatch fleet-wide OR `max_tx_size` lowered to zero) | [`SUBMESH_POLICY_INCIDENT.md` §3.1](SUBMESH_POLICY_INCIDENT.md#31-mode-a--qsdmsubmeshp2prejects) for the per-counter triage |
| `qsdm_nvidia_lock_p2p_rejects` matches `processed` rate | P2P NVIDIA-lock gate is rejecting every tx (no qualifying proof in the ring) | §3.2 (Mode B above) |
| `qsdm_transactions_invalid` climbing but no submesh / nvidia-lock signal | WASM preflight OR consensus validator is rejecting every tx. Check validator logs for `"WASM preflight failed"` or `"Received invalid transaction"` warnings | Audit the WASM ruleset (a recent rule deployment may have made every tx invalid); audit the consensus parent-cell state (a stale local view may be rejecting valid txs) |
| All counters flat, only `qsdm_transactions_processed_total` climbing | Storage backend is erroring on every `StoreTransaction` call. Check validator logs for `"Failed to store transaction"` errors | Check disk space; check storage backend health (whichever the validator is configured for); restart the storage subsystem if applicable |
| `qsdm_transactions_processed_total` climbing but `qsdm_transactions_invalid_total` flat AND no rejects | All txs are being silently dropped at parse stage (`ParseTransaction` failure with no metric increment) | Check validator logs for transaction-parse warnings; the inbound P2P traffic shape may have changed (fork that introduced a new tx envelope) |

#### Mitigation by gate

- **Submesh policy:** see SUBMESH_POLICY_INCIDENT.md.
- **NVIDIA-lock P2P:** see §3.2 above.
- **WASM preflight regression:** roll back the
  most-recent ruleset deployment; the chain
  shouldn't be running with a ruleset that
  rejects 100% of legitimate txs.
- **Consensus validator:** investigate
  parent-cell state; restart the validator if a
  reorg has corrupted the local view.
- **Storage backend:** disk full / database
  connection lost / permissions broken — fix at
  the OS / database layer.
- **Parse-stage drops:** version mismatch
  between producers and validator; pin minimum
  client version.

#### Why this alert is the canonical "something is broken silently" signal

Most chain-failure modes have a louder signal
(consensus stalls trip
[`MINING_LIVENESS.md`](MINING_LIVENESS.md);
quarantine triggers trip
[`QUARANTINE_INCIDENT.md`](QUARANTINE_INCIDENT.md);
trust degradation trips
[`TRUST_INCIDENT.md`](TRUST_INCIDENT.md)). Mode D
catches the residual case where the chain
**looks healthy from outside** but isn't doing
useful work — the alert exists to make that
silent failure mode loud.

#### Recovery validation

```promql
rate(qsdm_transactions_stored_total[10m]) > 0
```

The alert auto-clears once storage resumes for
one full evaluation window past `for: 30m`.

---

## 4. Cross-mode + cross-runbook escalation

| Concurrent alerts | Most likely root | Action |
|---|---|---|
| Mode A only | NVIDIA-lock HTTP gate is rejecting state-changing API calls | §3.1 — read the 403 detail message |
| Mode B only | NVIDIA-lock P2P gate is rejecting libp2p txs | §3.2 — same proof-ring health checks |
| Mode A + Mode B | Both gates share the same proof ring; the ring is broken | §3.1 first (HTTP detail names the failure mode); Mode B clears as a follow-on |
| Mode A or B + [`NGC_SUBMISSION_INCIDENT.md`](NGC_SUBMISSION_INCIDENT.md) Mode B | The NGC submission gate is rejecting bundles → no fresh proofs in the ring → both NVIDIA-lock gates reject | NGC_SUBMISSION is the **upstream cause**; fix the dominant reject reason there, and the NVIDIA-lock signals clear within one sidecar cycle |
| Mode C only | Hashrate band rejection — units typo, leaderboard-stuffing, or band needs widening | §3.3 |
| Mode C + [`ARCH_SPOOF_INCIDENT.md`](ARCH_SPOOF_INCIDENT.md) Mode B from same NodeID | Miner cheating across multiple axes (arch + hashrate). Canonical sustained-cheating slashing case | [`SLASHING_INCIDENT.md`](SLASHING_INCIDENT.md) escalation |
| Mode D alone | A pipeline gate is rejecting 100% of traffic OR storage is broken | §3.4 — narrow by which counter is climbing |
| Mode D + Mode B | NVIDIA-lock P2P gate rejecting every tx is the cause of zero stored txs | Fix Mode B; Mode D clears |
| Mode D + [`SUBMESH_POLICY_INCIDENT.md`](SUBMESH_POLICY_INCIDENT.md) Mode A | Submesh policy is rejecting every tx | Fix submesh policy; Mode D clears |
| Mode D + chain-stuck (`QSDMMiningChainStuck`) | Storage failures are wedging block production | [`MINING_LIVENESS.md`](MINING_LIVENESS.md) takes precedence; Mode D is downstream symptom |

---

## 5. Reference

- **Source files:**
  - [`pkg/monitoring/nvidia_lock.go`](../../../source/pkg/monitoring/nvidia_lock.go)
    — `NvidiaLockProofOK` policy + four canonical
    error detail strings (the substrings the
    Mode A triage table matches against).
  - [`pkg/monitoring/nvidia_lock_metrics.go`](../../../source/pkg/monitoring/nvidia_lock_metrics.go)
    — `RecordNvidiaLockHTTPBlock` /
    `RecordNvidiaLockP2PReject`.
  - [`pkg/monitoring/nvidia_p2p_gate.go`](../../../source/pkg/monitoring/nvidia_p2p_gate.go)
    — P2P gate config struct.
  - [`pkg/api/handlers.go`](../../../source/pkg/api/handlers.go)
    — `enforceNvidiaLock` HTTP gate enforcement.
  - [`cmd/qsdm/transaction/transaction.go`](../../../source/cmd/qsdm/transaction/transaction.go)
    — `HandleTransaction` six-gate dispatch
    pipeline (the basis of Mode D's gate
    decomposition).
  - `pkg/monitoring/archcheck_metrics.go` —
    per-arch hashrate band counter
    (`qsdm_attest_hashrate_rejected_total{arch}`).
  - `pkg/mining/verifier.go` — per-type
    attestation verifier that drives the
    hashrate-band check.
  - [`pkg/monitoring/metrics.go`](../../../source/pkg/monitoring/metrics.go)
    — `IncrementTransactionsProcessed` /
    `IncrementTransactionsValid` /
    `IncrementTransactionsInvalid` /
    `IncrementTransactionsStored` (the four
    counters Mode D's expression compares).
  - [`cmd/qsdm/main.go`](../../../source/cmd/qsdm/main.go)
    — `SetMessageHandler` is where every inbound
    P2P message bumps `IncrementTransactionsProcessed`
    (the numerator of Mode D's signal).
- **Configuration env vars:**
  - HTTP gate: `QSDM_NVIDIA_LOCK_ENABLED`,
    `QSDM_NVIDIA_LOCK_MAX_PROOF_AGE`,
    `QSDM_NVIDIA_LOCK_EXPECTED_NODE_ID`,
    `QSDM_NVIDIA_LOCK_PROOF_HMAC_SECRET`,
    `QSDM_NVIDIA_LOCK_REQUIRE_INGEST_NONCE`.
  - P2P gate: `QSDM_NVIDIA_LOCK_GATE_P2P` (uses
    same proof-policy env vars as HTTP, but
    `consume=false` always).
  - Sidecar side:
    `QSDM_NGC_PROOF_NODE_ID` (must match
    `QSDM_NVIDIA_LOCK_EXPECTED_NODE_ID`),
    `QSDM_NGC_PROOF_HMAC_SECRET` (must match
    `QSDM_NVIDIA_LOCK_PROOF_HMAC_SECRET`),
    `QSDM_NGC_INGEST_SECRET` (the auth
    secret for the ingest endpoint, see
    [`NGC_SUBMISSION_INCIDENT.md`](NGC_SUBMISSION_INCIDENT.md)).
- **Prometheus series:**
  - `qsdm_nvidia_lock_http_blocks_total` —
    Mode A's source.
  - `qsdm_nvidia_lock_p2p_rejects_total` —
    Mode B's source.
  - `qsdm_attest_hashrate_rejected_total{arch}`
    — Mode C's source.
  - `qsdm_transactions_processed_total` —
    Mode D's denominator.
  - `qsdm_transactions_stored_total` —
    Mode D's numerator (== 0 case).
  - `qsdm_transactions_invalid_total` —
    Mode D's triage decomposition signal.
- **Hashrate band reference table** (§4.6.3 of
  [`MINING_PROTOCOL_V2.md`](../MINING_PROTOCOL_V2.md)):
  - `turing`       [10 KH/s,   5 MH/s]
  - `ampere`       [50 KH/s,  50 MH/s]
  - `ada-lovelace` [100 KH/s, 50 MH/s]
  - `hopper`       [1 MH/s,  200 MH/s]
  - `blackwell`    [5 MH/s,  500 MH/s]
- **Companion runbooks** (cross-mode escalation
  links):
  - [`NGC_SUBMISSION_INCIDENT.md`](NGC_SUBMISSION_INCIDENT.md)
    — upstream cause for Modes A/B (no fresh
    proofs in the ring).
  - [`SUBMESH_POLICY_INCIDENT.md`](SUBMESH_POLICY_INCIDENT.md)
    — alternate gate that can cause Mode D
    (submesh rejecting every tx).
  - [`ARCH_SPOOF_INCIDENT.md`](ARCH_SPOOF_INCIDENT.md)
    — companion to Mode C (miner cheating
    across multiple axes).
  - [`SLASHING_INCIDENT.md`](SLASHING_INCIDENT.md)
    — escalation for sustained Mode C from one
    NodeID.
  - [`MINING_LIVENESS.md`](MINING_LIVENESS.md)
    — when Mode D escalates to chain-stuck.

---

## 6. Coverage closure

This runbook is the **last commit in the runbook
sweep that closes alert-with-`runbook_url`
coverage at 38/38 (100%)**. The progression:

| Commit | Cluster | Coverage delta |
|---|---|---|
| Initial sweep | Slashing, enrollment, mining-liveness, trust | 14/38 → 20/38 |
| Quarantine + arch-spoof + rejection-plumbing | 7 alerts in 3 small clusters | 20/38 → 27/38 |
| Submesh-policy | 2 alerts, companion to quarantine | 27/38 → 29/38 |
| Governance-authority | 3 alerts (last critical-severity gap closed) | 29/38 → 32/38 |
| NGC-submission | 2 alerts, companion to trust | 32/38 → 34/38 |
| **Operator hygiene (this runbook)** | **4 alerts in 4 unrelated clusters** | **34/38 → 38/38 (100%)** |

After this commit, every alert in
[`alerts_qsdm.example.yml`](../../../deploy/prometheus/alerts_qsdm.example.yml)
carries an anchored `runbook_url`, and every
runbook is bidirectionally cross-linked to its
operationally-related companions.

---

## 7. Alert ↔ Mode quick-reference

| Alert                              | Mode | Severity | Triage section |
| ---------------------------------- | ---- | -------- | -------------- |
| `QSDMNvidiaLockHTTPBlocksSpike`    | A    | warning  | §3.1           |
| `QSDMNvidiaLockP2PRejects`         | B    | warning  | §3.2           |
| `QSDMAttestHashrateOutOfBand`      | C    | warning  | §3.3           |
| `QSDMNoTransactionsStored`         | D    | warning  | §3.4           |
