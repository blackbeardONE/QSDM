# NGC Submission Incident — Operator Runbook

Triage flow for the 2 alerts in the NGC-submission
sub-group of `qsdm-nvidia-lock`:

| Alert | Severity | Default `for:` | Anchor |
|---|---|---|---|
| `QSDMNGCChallengeRateLimited`     | warning | 5m | [§3.1](#31-mode-a--qsdmngcchallengeratelimited) |
| `QSDMNGCProofIngestRejectBurst`   | warning | 5m | [§3.2](#32-mode-b--qsdmngcproofingestrejectburst) |

> **Why a dedicated NGC-submission runbook?** The
> NGC submission subsystem is the **per-request gate**
> for the qsdm.tech transparency pipeline — a sidecar
> calls `GET /monitoring/ngc-challenge` to obtain a
> nonce and then `POST /monitoring/ngc-proof` to
> submit the signed CUDA-proof bundle. These two
> alerts catch failures at that submission gate,
> distinct from the **aggregate-response** failure
> mode that
> [`TRUST_INCIDENT.md`](TRUST_INCIDENT.md) covers.
> When this runbook's alerts fire alone, it's a
> client / sidecar config problem. When they fire
> alongside `QSDMTrust*` alerts, the submission gate
> is the **upstream cause** of the aggregate trust
> degradation — see §4.

Companion observability:

- `qsdm_ngc_challenge_issued_total` — successful
  GETs on `/monitoring/ngc-challenge`.
- `qsdm_ngc_challenge_rate_limited_total` — 429
  responses (Mode A's source).
- `qsdm_ngc_proof_ingest_accepted_total` —
  successful POSTs on `/monitoring/ngc-proof`.
- `qsdm_ngc_proof_ingest_rejected_total{reason}` —
  failed POSTs by closed-enum reason (Mode B's
  source). Nine reasons in the closed-enum set:
  `ingest_disabled`, `unauthorized`, `body_read`,
  `body_too_large`, `invalid_json`,
  `missing_cuda_hash`, `nonce`, `hmac`, `other`.
- `qsdm_ngc_ingest_nonce_pool_size` — gauge of
  tracked ingest nonces (the replay-protection
  pool).

---

## 1. Glossary (60-second skim)

- **Sidecar** — the NGC-attestation client that
  runs alongside (or on a separate host from) a
  GPU and submits CUDA-proof bundles to a QSDM
  validator. Reference impls live in
  `apps/qsdm-nvidia-ngc/scripts/` (PowerShell for
  Windows hosts) and `docker compose --profile gpu`
  (Linux containers).
- **`/monitoring/ngc-challenge`** — `GET` endpoint
  that issues a fresh `(nonce, issued_at,
  signature)` triple. Per-IP rate-limit: **15
  requests/minute**. Returns `429 Too Many
  Requests` with a `Retry-After` header (in
  seconds) when exhausted.
- **`/monitoring/ngc-proof`** — `POST` endpoint that
  ingests a CUDA-proof bundle. Per-IP rate-limit:
  **30 requests/minute**. The body is HMAC-signed
  with `QSDM_NGC_INGEST_SECRET` (operator-pinned;
  rotates via secure ops channel).
- **`QSDM_NGC_INGEST_SECRET`** — shared HMAC secret
  between sidecar and validator. **Required**. The
  validator returns 404 (not 401/403) on the
  ingest endpoint when this env is unset — see
  `ingest_disabled` reason below.
- **Replay-nonce pool** — the validator tracks
  recently-seen ingest nonces in
  `pkg/monitoring/ngc_nonce.go` and rejects
  duplicates. The pool size is bounded;
  pre-expiry duplicates fall into the `nonce`
  reject reason.
- **Nine closed-enum reject reasons** for
  `/monitoring/ngc-proof`:
  - `ingest_disabled` — `QSDM_NGC_INGEST_SECRET`
    unset on this validator. Returns 404. **Not
    a sidecar fault** — the validator is
    deliberately running with ingest disabled.
  - `unauthorized` — caller is not authorised
    (auth middleware rejected before
    HMAC check).
  - `body_read` — server couldn't read the
    request body (truncation mid-POST, network
    flap).
  - `body_too_large` — body exceeds
    `cfg.HTTPBodyMaxBytes`.
  - `invalid_json` — body doesn't parse as JSON.
  - `missing_cuda_hash` — JSON parsed but
    `cuda_proof_hash` field is empty / absent.
  - `nonce` — replay-nonce check failed (nonce
    already seen, expired, or outside the
    accepted window).
  - `hmac` — HMAC verification against
    `QSDM_NGC_INGEST_SECRET` failed (most often a
    secret rotation that hit one side but not the
    other).
  - `other` — unmapped failure path. A non-zero
    rate here is itself a P3 bug — the closed
    enum should cover every observed failure
    mode.

---

## 2. First-90-seconds checklist

1. **Identify which alert fired.**
   - Mode A = challenge issuance (`429`s).
   - Mode B = proof ingest (rejected POSTs).

2. **For Mode B, decompose by `reason`.**
   ```promql
   sum by (reason) (rate(qsdm_ngc_proof_ingest_rejected_total[10m]))
   ```
   The dominant reason is the **only thing that
   matters** — the rest of the runbook is the
   reason → cause mapping.

3. **Check whether ingest is even enabled.**
   ```promql
   # If ingest_disabled is the dominant reason,
   # this validator has QSDM_NGC_INGEST_SECRET
   # unset by design. Mode B is then expected; the
   # alert is reading the natural failure mode of
   # an ingest-disabled validator.
   rate(qsdm_ngc_proof_ingest_rejected_total{reason="ingest_disabled"}[10m])
   ```
   If your validator is *supposed* to be a trust
   peer, set the env. If it isn't, silence Mode B
   on this validator's instance label.

4. **Cross-reference trust-aggregator state.**
   Mode B firing alongside
   [`TRUST_INCIDENT.md`](TRUST_INCIDENT.md)
   alerts means the submission gate is the
   upstream cause of trust degradation — the
   rejected counters carry the reason; trust
   alerts only carry the symptom.

5. **Don't reflexively raise rate limits.** The
   per-IP caps (15/min for challenge, 30/min for
   proof) are intentionally tight. A well-behaved
   sidecar with a one-minute attestation cadence
   plus jitter never approaches them.

---

## 3. Modes

### 3.1. Mode A — `QSDMNGCChallengeRateLimited`

`increase(qsdm_ngc_challenge_rate_limited_total[10m]) > 5`
for 5m. Severity: warning.

#### What triggered it

A sidecar (or a third party probing the endpoint)
has hit the 15-req/IP/min rate limit on `GET
/monitoring/ngc-challenge` and received `429 Too
Many Requests` at least 6 times in the last 10
minutes. The validator returns the
`Retry-After` header (in seconds) on every 429,
so a well-behaved client should back off
immediately; sustained hits mean the client is
ignoring `Retry-After` OR retrying without
jitter.

#### Symptoms

- `qsdm_ngc_challenge_rate_limited_total` is
  incrementing.
- Sidecars may report attestation gaps in their
  logs ("got 429, will retry"); if they're
  retrying tight, the gap may not propagate to
  Mode B (the proof step needs a successful
  challenge first).
- `qsdm_ngc_challenge_issued_total` may be
  *higher* than expected if the same sidecar is
  successfully fetching a fresh nonce on every
  retry instead of reusing one — the
  challenge-then-proof pair is supposed to be
  one-shot.

#### Triage

```promql
# Confirm the rate:
rate(qsdm_ngc_challenge_rate_limited_total[10m])

# Compare to issued — a healthy ratio is
# rate_limited << issued.
rate(qsdm_ngc_challenge_issued_total[10m])

# Identify the offending IPs from access logs:
# (the rate limit is per-IP, so the dominant IP
# in the 429 log lines is the offender.)
```

```bash
# Validator nginx / reverse-proxy access log:
journalctl -u qsdm --since "30 minutes ago" \
  | grep -E "POST|GET.*monitoring/ngc-challenge.*429" \
  | awk '{print $3}' | sort | uniq -c | sort -rn | head
```

| Pattern | Probable cause | Action |
|---|---|---|
| Single IP dominates 429s, owner is a known sidecar | Sidecar's polling cadence is too tight, OR the script is ignoring `Retry-After`, OR retry-without-jitter is causing herd convergence | Reach out to the sidecar operator; the reference scripts in `apps/qsdm-nvidia-ngc/scripts/` jitter randomly within a 30s window — match that behaviour |
| Single IP dominates, owner unknown | Third-party probe scanning the endpoint | Add the IP to the upstream firewall block list |
| Many IPs each contribute a few 429s | Fleet-wide sidecar release with bad polling logic, OR a coordinated probe | Audit recent sidecar releases for polling-cadence regressions; if no release in flight, treat as a probe and tighten upstream |
| Bursts correlated with a chain-height jump | Sidecars may be racing to refresh their nonce after a chain event invalidated the previous one | Cross-check chain-event timing; consider extending the nonce TTL slightly so legitimate fresh-after-reorg refreshes don't compete |

#### Mitigation

- **Sidecar-side fix:** the reference
  `attest-from-env-file.ps1` (Windows) and the
  Linux compose profile both jitter the
  challenge-then-proof cycle. Pin your sidecars
  to versions that match the reference cadence
  (typically 60–120s with ±30s jitter).
- **Validator-side mitigation:** raising the
  per-IP cap on `/monitoring/ngc-challenge` is an
  available knob in `pkg/api/security.go`
  (`getEndpointLimit`), but doing so to silence
  the alert without identifying the offender
  expands the attack surface — a probe can now
  scan harder before the rate-limiter trips.
  Don't raise the cap reflexively.
- **Don't disable the rate limit.** The challenge
  endpoint mints fresh randomness on every call;
  unbounded querying is itself a small DoS
  vector against the validator's PRNG.

#### Recovery validation

```promql
rate(qsdm_ngc_challenge_rate_limited_total[5m]) == 0
```

The alert auto-clears once 429s stop for one full
evaluation window past `for: 5m`.

---

### 3.2. Mode B — `QSDMNGCProofIngestRejectBurst`

`sum(increase(qsdm_ngc_proof_ingest_rejected_total[10m])) > 10`
for 5m. Severity: warning.

#### What triggered it

Across **all reject reasons combined**, more
than 10 POSTs to `/monitoring/ngc-proof` were
rejected in the last 10 minutes. The summed-rate
expression means a multi-reason failure trips
the alert just as easily as a single dominant
reason — this is intentional, because most
real incidents (a rotated secret, a sidecar
shipping a malformed bundle, a clock-skew
event) cause two or three reasons to spike
together.

#### Why this is the **canonical upstream cause** for trust degradation

Sustained Mode B is the most common upstream
cause of `QSDMTrustNoAttestationsAccepted`
(TRUST_INCIDENT.md Mode A) and
`QSDMTrustIngestRejectRateElevated`
(TRUST_INCIDENT.md Mode B). The path is:

```
sidecar posts proof bundle
  → gate rejects (this runbook's Mode B)
    → accepted_total flatlines OR rejects outpace accepts
      → trust aggregator sees no fresh attestations
        → /api/v1/trust/attestations/summary "attested" drops
          → qsdm.tech transparency badge flips amber/red
```

Every step downstream of "gate rejects" is
diagnosed by trust alerts; this runbook covers
the upstream "gate rejects" decision itself.

#### Symptoms

- `qsdm_ngc_proof_ingest_rejected_total` is
  incrementing across one or more reasons.
- `qsdm_ngc_proof_ingest_accepted_total` may be
  flat or lagging.
- Sidecars receive non-2xx responses on POST;
  their logs name the HTTP status and
  (sometimes) the error body.
- The TRUST_INCIDENT.md cascade may already be
  in flight.

#### Triage — the `reason` is everything

```promql
# Ranked list — the dominant reason names the
# cause:
topk(3, sum by (reason) (rate(qsdm_ngc_proof_ingest_rejected_total[10m])))
```

| Reason | Probable cause | Action |
|---|---|---|
| `hmac` | **Most common.** `QSDM_NGC_INGEST_SECRET` rotated on one side but not the other. Rotation is a manual two-step that is easy to half-finish | Verify the secret matches between sidecar and validator. The reference rotation procedure: stage the new secret on validators first (validators accept BOTH old and new during a transition window), then roll sidecars, then drop the old secret on validators. If you're mid-rotation, the rejects are expected; if you're not, find the asymmetric host |
| `nonce` | Replay-nonce check failed. Three sub-causes: (1) sidecar clock skew pushes the nonce outside the accepted window; (2) the sidecar is retrying with the same bundle (which retains the same nonce); (3) `qsdm_ngc_ingest_nonce_pool_size` overflowed and the validator GC'd the still-in-flight nonce | Force NTP sync on the sidecar host; check sidecar logs for retry loops on the same proof; cross-reference the nonce-pool gauge — a high size combined with sustained `nonce` rejects means the pool eviction is too aggressive for your traffic shape |
| `unauthorized` | Caller doesn't have the required auth credentials at the middleware layer (separate from HMAC). A third-party probe is the dominant case; a misconfigured sidecar that lost its auth header is the operator-side case | Audit the source IPs in the access log; confirm legitimate sidecars carry the right auth header |
| `body_too_large` | Bundle exceeds `cfg.HTTPBodyMaxBytes`. Most often a sidecar shipping a regression that includes extra debug payload | Confirm sidecar version against the reference; if a release shipped a payload-size regression, roll back |
| `missing_cuda_hash` | JSON parsed but the `cuda_proof_hash` field is empty / absent. Sidecar bug — the proof generator never populated the field | Sidecar release regression; pin minimum sidecar version |
| `invalid_json` | Body isn't valid JSON. A sidecar shipping malformed bodies, OR a third-party probe POSTing garbage | Audit source IPs; if the source is a known sidecar, the sidecar's serializer is broken |
| `body_read` | Server couldn't read the body (mid-POST truncation). Network flap between sidecar and validator | Cross-check `qsdm_p2p_*` and ingress error rates; transient unless the network is actively degraded |
| `ingest_disabled` | `QSDM_NGC_INGEST_SECRET` is unset on this validator. **Not a sidecar fault.** The validator is deliberately running with ingest disabled and returns 404 on every POST | If this validator is *supposed* to be a trust peer, set the env and restart. If it isn't, silence Mode B on this validator's instance label permanently |
| `other` | Unmapped failure path. The closed enum should cover every observed mode; a non-zero rate here is itself a P3 bug | File a P3 to add the missing reason key to `pkg/monitoring/ngc_ingest_metrics.go::NGCProofIngestRejectReason` |

#### Mitigation by reason class

**Class 1 — auth/secret drift** (`hmac`,
`unauthorized`):

- The fix is on the **sidecar side or the
  validator side, never both**. Pick the side
  that diverged most recently.
- Mid-rotation false-positive is the canonical
  trap. Plan rotations during low-traffic
  windows; document the validator-accepts-both
  transition window in the secure ops channel.

**Class 2 — payload shape** (`body_too_large`,
`missing_cuda_hash`, `invalid_json`):

- Sidecar regression. Roll back to the last known
  good sidecar release; the validator is
  correct.
- Pin minimum sidecar version in the operator
  guide so downstream operators don't deploy the
  bad release.

**Class 3 — replay/timing** (`nonce`,
`body_read`):

- `nonce`: NTP, retry loops, or the nonce-pool
  size. The pool size is operator-tunable;
  raising it costs memory and slightly weakens
  the replay-protection window guarantee.
- `body_read`: usually transient; persistent
  signal indicates a network problem upstream
  of the validator.

**Class 4 — config** (`ingest_disabled`):

- Either set `QSDM_NGC_INGEST_SECRET` and
  restart, OR silence the alert on this
  instance.

**Class 5 — bug** (`other`):

- File the P3; close the loop in the closed-enum
  set.

#### Recovery validation

```promql
sum(rate(qsdm_ngc_proof_ingest_rejected_total[5m])) == 0
sum(rate(qsdm_ngc_proof_ingest_accepted_total[5m])) > 0
```

Sustained accepted-with-no-rejects is the
healthy state.

---

## 4. Cross-mode + cross-runbook escalation

| Concurrent alerts | Most likely root | Action |
|---|---|---|
| Mode A only | Sidecar polling-cadence drift, or a third-party probe | §3.1 |
| Mode B only | One of the §3.2 nine reject reasons | §3.2 — start with the dominant `reason` label |
| Mode A + Mode B (same sidecar IPs) | A sidecar mass-fork shipped with bad polling AND a malformed bundle | Roll back the sidecar release; both modes clear once the IPs stop bouncing |
| Mode B + [`TRUST_INCIDENT.md`](TRUST_INCIDENT.md) Mode A | Sustained ingest rejects → no accepted proofs → trust attested drops | Mode B is the **upstream cause**; the trust alert is the symptom. Fix the dominant reject reason here, and the trust alert clears 5–20 min later as the aggregator re-warms |
| Mode B + [`TRUST_INCIDENT.md`](TRUST_INCIDENT.md) Mode B | Rejects are outpacing accepts (the gate is letting *some* through but rejecting more) | Same mitigation; this branches when the operator is mid-secret-rotation and only some sidecars have the new secret |
| Mode B + [`TRUST_INCIDENT.md`](TRUST_INCIDENT.md) Mode D | The trust aggregator's `ngc_service_status` classifier flipped to degraded *because* the gate is rejecting. Mode D is the leading-edge cascade signal; Mode B here is the cause | Fix Mode B; Mode D clears as a follow-on |
| Mode B `reason="ingest_disabled"` dominant + no companion trust alerts | This validator isn't supposed to be a trust peer; the alert is reading the natural failure mode | Silence Mode B on this instance |
| Mode B `reason="hmac"` dominant + no other alerts | Mid-rotation false-positive | Confirm with the secure ops channel that a rotation is in flight; if so, ignore until rotation completes |

---

## 5. Reference

- **Source files:**
  - [`pkg/api/handlers_challenge.go`](../../../source/pkg/api/handlers_challenge.go)
    — `GET /monitoring/ngc-challenge` handler
    (issues nonce + signature, NOT rate-limited
    here — the limiter is upstream).
  - [`pkg/api/security.go`](../../../source/pkg/api/security.go)
    — per-endpoint rate limiter
    (`getEndpointLimit`, the 15 req/IP/min
    cap on challenge and 30 req/IP/min on
    proof). Records the `RecordNGCChallengeRateLimited`
    on 429.
  - [`pkg/monitoring/ngc_ingest_metrics.go`](../../../source/pkg/monitoring/ngc_ingest_metrics.go)
    — nine-reason closed-enum + counter
    machinery + `NGCProofIngestRejectReason`
    error → reason mapper.
  - [`pkg/monitoring/ngc_proofs.go`](../../../source/pkg/monitoring/ngc_proofs.go)
    — proof-bundle storage and the
    `RecordNGCProofBundle*` validation that
    feeds the reject-reason mapper.
  - [`pkg/monitoring/ngc_nonce.go`](../../../source/pkg/monitoring/ngc_nonce.go)
    — replay-nonce pool implementation.
  - [`pkg/monitoring/nvidia_lock_metrics.go`](../../../source/pkg/monitoring/nvidia_lock_metrics.go)
    — `RecordNGCChallengeIssued` /
    `RecordNGCChallengeRateLimited` recorders.
- **Reference sidecars:**
  - `apps/qsdm-nvidia-ngc/scripts/attest-from-env-file.ps1`
    (Windows Scheduled Task `QSDM-NGC-Attest`)
  - `docker compose --profile gpu` (Linux)
- **Configuration:**
  - `QSDM_NGC_INGEST_SECRET` — required to enable
    `/monitoring/ngc-proof`. Unset = ingest
    disabled (404 on every POST, the
    `ingest_disabled` reason).
  - `cfg.HTTPBodyMaxBytes` — body-size cap.
  - Per-endpoint rate limits in `pkg/api/security.go`.
- **Prometheus series:**
  - `qsdm_ngc_challenge_issued_total` — success
  - `qsdm_ngc_challenge_rate_limited_total` —
    Mode A's source
  - `qsdm_ngc_proof_ingest_accepted_total` —
    success (also TRUST_INCIDENT.md Mode A's
    source)
  - `qsdm_ngc_proof_ingest_rejected_total{reason}`
    — Mode B's source
  - `qsdm_ngc_ingest_nonce_pool_size` —
    replay-protection pool gauge
- **Closed-enum reject reasons** (9 values):
  `ingest_disabled`, `unauthorized`, `body_read`,
  `body_too_large`, `invalid_json`,
  `missing_cuda_hash`, `nonce`, `hmac`, `other`.
- **Companion runbooks:**
  - [`TRUST_INCIDENT.md`](TRUST_INCIDENT.md) —
    *aggregate response* runbook. Trust alerts
    fire on the *consequences* of NGC submission
    failures (no fresh attestations, low fresh
    source count, aggregator status degraded).
    NGC_SUBMISSION here is the *per-request
    gate*; trust there is the *aggregate
    response*. Bidirectional cross-links from
    Modes A/B/D in TRUST_INCIDENT.md.
  - [`OPERATOR_HYGIENE_INCIDENT.md`](OPERATOR_HYGIENE_INCIDENT.md)
    — *downstream consumer* runbook. NGC
    submission populates the proof ring that
    NVIDIA-lock (Modes A/B in the hygiene
    runbook) reads from. Sustained NGC ingest
    rejects (Mode B here) are the canonical
    upstream cause of NVIDIA-lock alerts: if
    the gate rejects bundles, the ring stays
    empty and both NVIDIA-lock gates start
    rejecting. Fix Mode B here and the
    NVIDIA-lock signals clear within one
    sidecar cycle.

---

## 6. Alert ↔ Mode quick-reference

| Alert                              | Mode | Severity | Triage section |
| ---------------------------------- | ---- | -------- | -------------- |
| `QSDMNGCChallengeRateLimited`      | A    | warning  | §3.1           |
| `QSDMNGCProofIngestRejectBurst`    | B    | warning  | §3.2           |
