# QSDM Tier-2 GPU Spec Anomaly Checker

> **Status:** Active on `qsdm.tech` (BLR1 validator) since `2026-05-07`.
> Advisory only — does NOT cause proof rejection.

## What it is

The Tier-2 anomaly checker is the validator-side companion of the
[Reference Telemetry Oracle](TELEMETRY_ORACLE.md). On every accepted v2 proof,
it compares the bundle's claimed GPU specs (`gpu_name`, `gpu_arch`,
`compute_cap`, `driver_ver`) against a catalog of *known-good* GPU
fingerprints, and emits a `Verdict` of one of:

| Kind | Meaning | Operator response |
| --- | --- | --- |
| `match` | Claim is consistent with at least one catalog reference. | (none — quiet path) |
| `mismatch` | At least one rule fired (e.g. impossible `gpu_arch` + `compute_cap`). | Investigate. Proof was still accepted. |
| `unknown_sku` | Catalog has no entry for the claimed `gpu_name`. | Publish a peer-attester profile for that SKU. |
| `skipped` | Catalog empty or claim degenerate. | (no action — pre-Tier-2 posture) |

The Tier-2 checker is **strictly non-consensus**: every advisory verdict
fires *after* the v2 proof has already been accepted. The checker can
never reject a proof or change a reward. Its outputs surface in three
places:

1. Structured logs (`spec-check: ...`).
2. Prometheus `/metrics` (`qsdm_spec_check_*`).
3. The public read-only HTTP endpoint
   `GET /api/v1/mining/spec-anomalies`.

## Why "advisory only"

A Tier-2 reject would couple the checker into consensus, which is
unsafe for a few reasons:

- **Catalog freshness.** A real attester can publish a reference
  profile any time, and the catalog converges over the subsequent
  poll cycle (default 5 min). A reject during the convergence
  window would punish honest miners for being early.
- **Bug surface.** The checker is a young system. Until the rule
  set has burned in over a few months of production traffic, a
  buggy rule could mass-reject otherwise-valid proofs.
- **Forward compatibility.** New SKUs ship faster than baseline
  updates. An "unknown_sku" reject would break every honest miner
  on a new RTX-series card the day NVIDIA released it.

Tier-3 (reward downgrade on persistent mismatch) is the
enforcement layer; **it shipped on `2026-05-07`** and is
[documented below](#tier-3-reward-downgrade).

## Catalog sources

Two source kinds compose:

1. **Static baseline** — vendor-known specs hard-coded into the
   binary at `pkg/mining/telemetrycheck/baseline.go`. Today this
   covers **23 SKUs** spanning RTX 30-series (Ampere CC 8.6),
   RTX 40-series (Ada Lovelace CC 8.9), and datacenter Hopper
   (A100/H100, CC 8.0/9.0). Always present; gives the validator
   something to compare against on a brand-new chain with zero
   connected attesters.

2. **Peer attester profiles** — signed
   `pkg/telemetry.ReferenceProfile` documents fetched from
   attester URLs listed in `QSDM_PEER_ATTESTER_URLS`. Each profile
   is associated with the attester's `SignerID` so a future Tier-3
   reputation system can weight profiles by trust.

Each source can list multiple GPUs per SKU (e.g. "I have observed
this SKU on three different physical cards with these exact driver
versions"). On lookup, the checker considers all entries
collectively — a CC value is acceptable if *any* catalog entry
for that SKU lists it.

## Rules implemented

The current rule set ships with three checks. All three are
deliberate, conservative — false-positives are disruptive, but
true-positives are valuable forensic signals.

### Rule 1: `arch` (always-on, severity `major`)

The bundle's `gpu_arch` must be consistent with the architecture
the `compute_cap` value implies. The mapping is:

| compute_cap | architecture |
| --- | --- |
| 5.x | maxwell |
| 6.x | pascal |
| 7.0 / 7.2 | volta |
| 7.5 | turing |
| 8.0 / 8.6 / 8.7 | ampere |
| 8.9 | ada-lovelace |
| 9.x | hopper |
| 10.x / 12.x | blackwell |

Fires only when both fields are present and the inferred
architecture is non-empty. Does NOT need a catalog match — a
hopper-CC bundle claiming `gpu_arch=ampere` is impossible
regardless of whether the catalog has the SKU on file.

### Rule 2: `compute_cap` (catalog-driven, severity `major`)

When the catalog has at least one entry for the bundle's
`gpu_name`, the bundle's `compute_cap` MUST appear in the union
of `compute_cap` values that catalog entries report for that SKU.

The catalog is built from real attester observations + the static
baseline, so a "RTX 3050 with CC 9.0" claim flags because every
real RTX 3050 reports CC 8.6.

### Rule 3: `driver_ver_format` (always-on, severity `minor`)

The bundle's `driver_ver` must look like an NVIDIA driver version
string — digits and at most three dots, e.g. `576.28` or
`535.104.05`. A `driver_ver` of `576.28-RC` or `foo` flags
because no real NVIDIA driver ships in that format.

This is intentionally weaker than a "must match an observed
driver version" check, because NVIDIA ships drivers faster than
any baseline catalog can track them. Operators who legitimately
downgrade drivers also produce values that "no catalog has
seen yet"; flagging those would be noisy.

## Wire format: `/api/v1/mining/spec-anomalies`

```text
GET https://api.qsdm.tech/api/v1/mining/spec-anomalies?limit=10
```

Returns `200 OK` with a JSON object:

```json
{
  "snapshot": {
    "catalog_total_entries": 24,
    "catalog_signers": 2,
    "catalog_skus": 23,
    "checked_total": 1142,
    "matched_total": 920,
    "mismatched_total": 222,
    "unknown_sku_total": 0,
    "skipped_total": 0,
    "ring_cap": 256,
    "ring_size": 222,
    "mismatches_by_field": {
      "arch": 222,
      "compute_cap": 222
    }
  },
  "anomalies": [
    {
      "observed_at": 1778160907,
      "attestation_type": "nvidia-hmac-v1",
      "node_id": "rtx3050-real-001",
      "gpu_uuid": "GPU-39925fa6-...",
      "gpu_name": "NVIDIA GeForce RTX 3050",
      "gpu_arch": "ampere",
      "compute_cap": "9.0",
      "driver_ver": "576.28",
      "miner_addr": "qsdm1miner-rtx3050",
      "height": 5015,
      "verdict": "mismatch",
      "mismatched_fields": ["arch", "compute_cap"],
      "has_major": true,
      "matched_references": ["attester-12a0d1aa082b7e28", "baseline"]
    }
  ]
}
```

Returns `503 Service Unavailable` when the validator did not opt
into Tier-2 (`QSDM_SPEC_CHECK_ENABLED=1`). Returns `400 Bad
Request` when `?limit=` is malformed or non-positive.

The endpoint is publicly readable (no auth) — it is part of the
trust-transparency surface alongside `/api/v1/mining/blocks` and
`/api/v1/receipts`.

## Prometheus metrics

| Metric | Type | Labels | Description |
| --- | --- | --- | --- |
| `qsdm_spec_check_catalog_entries` | gauge | — | Total observations across all signers. |
| `qsdm_spec_check_catalog_signers` | gauge | — | Distinct signer IDs (peer attesters + `baseline`). |
| `qsdm_spec_check_catalog_skus` | gauge | — | Distinct GPU SKU names. |
| `qsdm_spec_check_checked_total` | counter | — | Cumulative accepted v2 proofs that ran the check. |
| `qsdm_spec_check_match_total` | counter | — | Verdicts of kind `match`. |
| `qsdm_spec_check_mismatch_total` | counter | — | Verdicts of kind `mismatch`. |
| `qsdm_spec_check_unknown_sku_total` | counter | — | Verdicts of kind `unknown_sku`. |
| `qsdm_spec_check_skipped_total` | counter | — | Verdicts of kind `skipped`. |
| `qsdm_spec_check_mismatch_field_total` | counter | `field` | Per-rule firing count. |

Recommended alert (rough cut, tune for your traffic):

```yaml
- alert: QSDMSpecCheckMismatchSpike
  expr: rate(qsdm_spec_check_mismatch_total[5m]) > 0.05
  for: 10m
  annotations:
    summary: ">5% mismatch rate sustained 10m — investigate spec-anomalies"
```

## Validator configuration

Wired by `cmd/qsdm/spec_check.go`. Knobs are **all opt-in** so
pre-Tier-2 deployments keep bit-for-bit behaviour unchanged
unless the operator turns telemetry checking on.

| Env var | Default | Effect |
| --- | --- | --- |
| `QSDM_SPEC_CHECK_ENABLED` | (unset) | When set to `1` / `true`, enables Tier-2. |
| `QSDM_PEER_ATTESTER_URLS` | (unset) | Comma-separated `…/api/v1/telemetry/reference` URLs. |
| `QSDM_PEER_ATTESTER_REFRESH` | `5m` | Catalog poll interval. |
| `QSDM_SPEC_CHECK_RING_CAP` | `256` | In-memory anomaly ring buffer size. |

Recommended systemd drop-in (BLR1 reference deploy):

```ini
[Service]
Environment="QSDM_SPEC_CHECK_ENABLED=1"
Environment="QSDM_PEER_ATTESTER_URLS=https://api.qsdm.tech/attest/blackbeard-3050/api/v1/telemetry/reference"
Environment="QSDM_PEER_ATTESTER_REFRESH=5m"
Environment="QSDM_SPEC_CHECK_RING_CAP=256"
```

## Hot path safety

The advisory check runs synchronously inside the v2 verifier
hot path. Three properties make it safe:

1. **It cannot fail the proof.** The hook is invoked AFTER
   the verifier has already returned `nil` (acceptance). No
   path inside the hook can revoke that decision.

2. **It cannot panic the validator.** A `defer recover()`
   inside `safeOnAccept` (in `pkg/mining/attest/hmac/verifier.go`)
   contains any panic the observer raises. The proof remains
   accepted, the validator stays running, the buggy observer
   gets one chance to misbehave per proof and we move on.

3. **It cannot block.** The checker uses lock-free atomic
   counters for /metrics and an `RWMutex` on the catalog
   only for reads. Writes (catalog refresh from peer
   attesters) happen on a separate goroutine.

## End-to-end verification (BLR1, 2026-05-07)

Reference scenario performed at deployment time:

| Step | Action | Result |
| --- | --- | --- |
| 1 | Deploy validator with `QSDM_SPEC_CHECK_ENABLED=1`. | `spec-check: Tier-2 advisory checker active` in log. Catalog has 24 entries (23 baseline + 1 peer profile from `attester-12a0d1aa082b7e28`). |
| 2 | Run real RTX 3050 miner with claim `compute_cap=8.6`. | 28/28 proofs match. `mismatched_total=0`. |
| 3 | Spoof miner config to claim `compute_cap=9.0`. Restart miner. | After ~25s: `mismatched_total=132`. Both `arch` and `compute_cap` rules fire. `has_major: true` on every record. Anomalies show in `/api/v1/mining/spec-anomalies`. |
| 4 | Revert config, restart miner. | `mismatched_total` freezes. `matched_total` resumes climbing. |

This is the canonical demo proving the rule set is sensitive
enough to catch real spoofing while quiet on the happy path.

## Tier-3 reward downgrade

**Status:** Active on `qsdm.tech` (BLR1 validator) since
`2026-05-07`. Sits one layer above Tier-2 — consumes the
verdict stream and applies a per-miner sliding-window
penalty to the blockdriver's per-block share. Strictly
**off the consensus path** (the proofs that earn rewards
have already been accepted).

### Wire layout

```
v2 proof
    ↓
hmac.Verifier.VerifyAttestation (consensus)
    ↓ (proof accepted, returns nil)
HMACAdapter.OnHMACAccept
    ├── Checker.Check(claim) → Verdict       (Tier-2)
    └── PerMinerStats.Update(addr, verdict)  (Tier-3, sliding window)
        ⋮
miningsvc.RewardSink.OnAcceptedProof(addr)
    ↓
blockdriver.Driver.tick()
    ↓
buildTxs(queue, total, rewardCell):
    for addr, count in queue:
        share = rewardCell * count / total
        share *= rewardPenalty.MultiplierFor(addr)  ← Tier-3 hook
        emit tx(addr, share)
```

The "lost" share is **unminted, NOT redistributed** to
other miners. That keeps the supply cap monotonically
respected and makes the tokenomic effect of Tier-3
strictly subtractive.

### Threshold logic

For each miner address, the engine maintains a sliding
window of the last `WindowSize` verdicts (default 1000).
Every accepted proof's verdict folds into the window:

| Verdict | Counts toward threshold? |
| --- | --- |
| `match` | No (denominator only) |
| `unknown_sku` | No (catalog will catch up) |
| `mismatch` with `has_major: false` (minor only) | No |
| `mismatch` with `has_major: true` | **Yes** |
| `skipped` | No |

Penalty fires when:

```
window_filled  >= MinObservations           AND
mismatch_pct   >= MismatchThresholdPct
```

When fired, every per-block share for that miner is
multiplied by `PenaltyMultiplier`. Otherwise the
multiplier is `1.0`.

### Governance constants (defaults)

| Env var | Default | Meaning |
| --- | --- | --- |
| `QSDM_SPEC_PENALTY_ENABLED` | unset (off) | Master switch. Requires `QSDM_SPEC_CHECK_ENABLED=1` to do anything. |
| `QSDM_SPEC_PENALTY_WINDOW` | 1000 | Sliding-window length in proofs. |
| `QSDM_SPEC_PENALTY_THRESHOLD` | 10.0 | Mismatch percentage at or above which the multiplier fires. |
| `QSDM_SPEC_PENALTY_MULTIPLIER` | 0.75 | Reward share multiplier when over threshold. |
| `QSDM_SPEC_PENALTY_MIN_OBS` | 50 | Warmup floor — penalty cannot fire until window has at least this many proofs. |

Setting `MULTIPLIER=0` would zero out a flagged miner's
reward, which is functionally a hard slash and not what
this layer represents. Values `<= 0` resolve to the
default. Values `> 1` likewise resolve to the default —
Tier-3 is strictly subtractive.

### Public endpoint

```
GET /api/v1/mining/penalty             → all tracked miners + config
GET /api/v1/mining/penalty?address=…   → one miner
```

503 when Tier-3 is disabled. Otherwise:

```json
{
  "config": {
    "window_size": 200,
    "threshold_pct": 5,
    "multiplier": 0.5,
    "min_observations": 20
  },
  "penalised_count": 1,
  "tracked_miners": 1,
  "miners": [
    {
      "miner_addr": "qsdm1miner-rtx3050",
      "window_size": 200,
      "window_filled": 200,
      "mismatch_count": 200,
      "match_count": 0,
      "mismatch_pct": 100,
      "threshold_pct": 5,
      "over_threshold": true,
      "below_min_observations": false,
      "multiplier": 0.5,
      "last_observed_at": 1778162804
    }
  ]
}
```

### Prometheus metrics

| Metric | Type | Meaning |
| --- | --- | --- |
| `qsdm_spec_penalty_active` | gauge | 1 = Tier-3 wired, else 0 |
| `qsdm_spec_penalty_window_size` | gauge | Resolved window length |
| `qsdm_spec_penalty_threshold_pct` | gauge | Resolved threshold |
| `qsdm_spec_penalty_multiplier` | gauge | Resolved multiplier |
| `qsdm_spec_penalty_min_observations` | gauge | Resolved warmup floor |
| `qsdm_spec_penalty_tracked_miners` | gauge | Distinct addresses observed |
| `qsdm_spec_penalty_penalised_miners` | gauge | Miners currently with multiplier < 1.0 |
| `qsdm_spec_penalty_blockdriver_payouts_total` | counter | Per-block shares the blockdriver multiplied by < 1.0 |
| `qsdm_spec_penalty_blockdriver_withheld_dust` | counter | Cumulative dust unminted by Tier-3 |

Per-miner cardinality is intentionally NOT exported as a
Prometheus label. The `/api/v1/mining/penalty` endpoint
is the per-miner-detail surface; Prometheus only sees
aggregates so synthetic-address spam can't inflate the
metrics surface.

### Tier-3 end-to-end verification (BLR1, 2026-05-07)

| Step | Action | Result |
| --- | --- | --- |
| 1 | Deploy with `QSDM_SPEC_PENALTY_ENABLED=1`, window=200, threshold=5%, multiplier=0.5, min_obs=20. | `spec-check: Tier-3 reward downgrade active` in log. `qsdm_spec_penalty_active=1`. |
| 2 | Real RTX 3050 mines with `compute_cap=8.6`. | After ~30s: 1 tracked miner, 0 penalised. Multiplier `1.0`. |
| 3 | Spoof config to `compute_cap=9.0` (Hopper claim on Ampere GPU). | Within ~60s the window fills with 200 mismatches → `over_threshold=true`, `multiplier=0.5`. |
| 4 | Confirm blockdriver applied the penalty. | `qsdm_spec_penalty_blockdriver_payouts_total=10`, `withheld_dust=1.78×10⁹` (≈17.82 CELL not minted). |
| 5 | Restore config, restart miner. | After ~3 minutes the window evicts the bad proofs → `match_count=200`, `mismatch_count=0`, `multiplier=1.0`. The penalty self-clears. |

This proves the round trip: bad proofs ⇒ penalty fires
⇒ blockdriver mints fewer CELL ⇒ honest behaviour
restores the multiplier ⇒ next block credits the full
share. **Persistent good behaviour heals the penalty
without operator intervention.**

## Roadmap

- **Per-attester signing-key pinning.** Today the validator
  accepts profile content over HTTPS but does not verify the
  HMAC signature against a pinned per-attester key. A future
  config file (`peer_attesters.toml`) will list `(URL,
  signer_id, key_path)` triples so a malicious relay cannot
  serve forged catalog entries.
- **More rules.** Memory-size, PCIe-gen, and TDP checks once
  the v1 hmac bundle wire format is extended to carry those
  fields. The bundle extension is forward-compatible — old
  miners simply omit the new fields and the rules become
  no-ops for them.
- **Live driver-version observation.** Once the catalog has
  observed enough drivers per SKU, swap the soft format check
  for a hard "must be in the observed set within ±1 minor
  version" check.

## Source layout

```
pkg/mining/telemetrycheck/
├── claim.go              -- the checker's input shape
├── verdict.go            -- Verdict + FieldMismatch + counters
├── catalog.go            -- thread-safe catalog of references
├── baseline.go           -- built-in static SKU table
├── checker.go            -- entry point: Check(claim) -> Verdict
├── rules.go              -- one function per rule
├── hmac_adapter.go       -- bridge into hmac.Verifier.OnAccept
├── penalty.go            -- Tier-3 sliding-window + multiplier
└── penalty_test.go       -- Tier-3 unit tests (window math)

pkg/api/handlers_spec_anomalies.go        -- public HTTP endpoint (Tier-2 + Tier-3)
pkg/monitoring/spec_check_metrics.go      -- Tier-2 Prometheus collector
pkg/monitoring/spec_penalty_metrics.go    -- Tier-3 Prometheus collector
internal/blockdriver/blockdriver.go       -- RewardPenalty hook in buildTxs
cmd/qsdm/spec_check.go                    -- validator-side wiring (Tier-2 + Tier-3)
```
