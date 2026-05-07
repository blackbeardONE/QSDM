# QSDM Reference Telemetry Oracle

> _Quickstart for the second role the home GPU plays in the
> QSDM network: the **Reference Telemetry Oracle**.
> Together with the **Public Challenge Issuer** (see
> `ATTESTER_QUICKSTART.md`) this is what turns a humble
> RTX 3050 into a network-relevant piece of hardware
> infrastructure._

## What is this for?

The QSDM v2 protocol locks proof submission to NVIDIA GPUs.
Today, the validator can verify that a proof was signed by
an enrolled HMAC key — but it has **no ground truth** for
what the operator's hardware actually is. A miner can claim
they are running an RTX 3050 (Ampere, CC 8.6, 8 GB GDDR6) and
the validator simply has to trust the claim.

The Reference Telemetry Oracle solves that the way real
networks have always solved it: **operators we trust publish
what their hardware looks like, and other operators use that
data as a sanity-check substrate**. It does not (yet) reject
spoofed claims. It builds the catalog. Once enough operators
have published profiles for enough SKUs over enough time, the
network has a credible reference set to compare incoming
miner claims against — and at that point, validators can
start downgrading or rejecting impossible claims (e.g.
"RTX 3050 with 24 GB" or "H100 with 130W TDP").

> **Active downstream:** The validator-side **Tier-2 advisory
> checker** (`pkg/mining/telemetrycheck`) already consumes
> these profiles. It compares each accepted v2 proof's
> claimed GPU specs against the catalog and surfaces
> mismatches at `/api/v1/mining/spec-anomalies` and the
> `qsdm_spec_check_*` Prometheus counters. **Advisory
> only** — no rejection, no reward effect (yet). See
> [SPEC_ANOMALY_CHECK.md](SPEC_ANOMALY_CHECK.md) for the
> full design.

The oracle runs **inside the same `qsdm-attester` binary**
that issues challenges. One process, two roles, one HMAC key.

## What gets published?

A signed JSON document at:

```
GET https://api.qsdm.tech/attest/<slot>/api/v1/telemetry/reference
```

Locally (no relay):

```
GET http://127.0.0.1:7733/api/v1/telemetry/reference
```

Sample response from `blackbeard-3050`:

```json
{
  "schema_version": 1,
  "signer_id": "attester-12a0d1aa082b7e28",
  "host_note": "blackbeard",
  "issued_at": 1778158495,
  "collector_kind": "nvidia-smi",
  "gpus": [
    {
      "uuid": "GPU-39925fa6-82f0-0e13-dd28-aa4be2048287",
      "name": "NVIDIA GeForce RTX 3050",
      "vendor": "NVIDIA",
      "arch": "ampere",
      "compute_cap": "8.6",
      "memory_total_mb": 8192,
      "pcie_gen": 3,
      "pcie_width": 16,
      "power_max_w": 143,
      "ecc_supported": false,
      "clock_graphics_boost_mhz": 2145,
      "clock_memory_mhz": 7001,
      "driver_versions_seen": ["576.28"],
      "vbios_versions_seen": ["94.06.37.00.c6"],
      "first_observed_at": 1778158428,
      "last_observed_at": 1778158488,
      "observations": 3
    }
  ],
  "signature": "4ebf88a8f057be33afded40a68e2aad76bf87480e7cee14845a51f790a4bdbaf"
}
```

### What the fields mean

| Field | Type | Notes |
|---|---|---|
| `schema_version` | int | Always `1` today. Bumped only on breaking changes; additive changes keep `1`. |
| `signer_id` | string | The attester's HMAC signer ID — same one it uses on `/api/v1/mining/challenge`. |
| `host_note` | string | Operator-supplied free-form tag (`--telemetry-note`). Defaults to the attester's `--note`. |
| `collector_kind` | string | Today always `"nvidia-smi"`. Future: `"nvml-attestation"`, `"rocm-smi"`, `"spdm"`. Verifiers may weight profiles by source. |
| `issued_at` | int64 (unix sec) | Re-stamped on every request — not cached. |
| `gpus[]` | array | One entry per physical GPU the attester has ever observed. |
| `gpus[].uuid` | string | NVIDIA GPU UUID. Identity key inside the profile. |
| `gpus[].arch` | string | Inferred from compute capability. `ampere`, `ada-lovelace`, `hopper`, etc. Empty for unknown future generations. |
| `gpus[].memory_total_mb` | uint64 | What the device reports, in MiB (1024×1024). Marketing GB ≠ MiB. |
| `gpus[].pcie_gen` / `pcie_width` | uint8 | Maximum negotiable PCIe gen + lane width. |
| `gpus[].power_max_w` | float | TDP cap in watts. SKU-stable; operator BIOS overrides can shift this. |
| `gpus[].ecc_supported` | bool | True if the device supports ECC memory (datacenter cards). False for consumer cards. |
| `gpus[].clock_graphics_boost_mhz` | uint32 | Maximum graphics clock (boost) — SKU-stable. NOT current clock. |
| `gpus[].clock_memory_mhz` | uint32 | Maximum memory clock — SKU-stable. |
| `gpus[].driver_versions_seen` | []string | Union of distinct driver versions observed across the attester's lifetime. |
| `gpus[].vbios_versions_seen` | []string | Same idea for VBIOS revisions. |
| `gpus[].first_observed_at` / `last_observed_at` | int64 | Lifetime bounds for this UUID on this attester. |
| `gpus[].observations` | uint64 | Cumulative `Apply()` count. Longevity signal. |
| `signature` | hex | HMAC-SHA256 over the canonical encoding of every other field. See "Verifying" below. |

## Query parameters

```
GET /api/v1/telemetry/reference?gpu=<uuid>
```
Filter to a single GPU. The profile is **re-signed** after
the filter pass, so the signature you get back is over the
slimmed-down body — verifiers always validate against
exactly the bytes they received.

```
GET /api/v1/telemetry/reference?include_observations=N
```
Cap each per-GPU `*VersionsSeen` set to at most `N`
entries. Useful for operators who have accumulated a
long history but want to publish a compact summary.
Re-signed after capping.

Both knobs can be combined.

## Verifying a profile

The signature is `HMAC_SHA256(canonical_bytes, hmac_key)`
where:

1. `canonical_bytes` is `json.Marshal` of the profile with:
   - `Signature` cleared to `""`
   - `gpus[]` sorted by `uuid` ascending
   - `driver_versions_seen` / `cuda_versions_seen` /
     `vbios_versions_seen` sorted lexicographically
2. `hmac_key` is the SAME key the attester uses to sign
   challenges (the operator pasted both into the
   verifier's allowlist).

A reference verifier in Go is in `pkg/telemetry/profile.go`
(`(*ReferenceProfile).Verify(key)`). Operators of other
languages should mirror the canonical encoding rules above.

## How collection works on the attester

- Boot: load existing profile from
  `~/.qsdm/telemetry.json` if it exists. Past observations
  carry forward across restarts.
- Boot+1: run one Collect immediately so a fresh boot
  picks up new driver versions without waiting a full
  tick.
- Every `--telemetry-every` (default 60s): run
  `nvidia-smi --query-gpu=...` and fold the result into
  the registry. Persist atomically to disk.
- Every request to `/api/v1/telemetry/reference`:
  build a fresh snapshot of the registry, sign with the
  signer key, return.

The collector goroutine and the HTTP handler share the
registry through a single `sync.RWMutex`; concurrent
reads (HTTP) and writes (collector) cannot tear.

### What `nvidia-smi` is queried for

```
nvidia-smi --query-gpu=
  uuid,name,compute_cap,memory.total,driver_version,
  pcie.link.gen.max,pcie.link.width.max,power.max_limit,
  vbios_version,ecc.mode.current,clocks.max.gr,clocks.max.mem
  --format=csv,noheader,nounits
```

`clocks.max.gr` / `clocks.max.mem` (NOT `clocks.gr` /
`clocks.mem`) are the maximum boost clocks. Querying the
current clocks would publish an idle state and the profile
would jitter every tick.

## Flags

```
--telemetry-disabled           Disable the oracle entirely.
--telemetry-every 60s          Collector tick interval.
--telemetry-file PATH          Persistence path.
                               '-' = no persistence.
                               '' = ~/.qsdm/telemetry.json.
--telemetry-note STRING        host_note in the published profile.
                               Defaults to the attester --note.
--telemetry-nvidia-smi PATH    Override nvidia-smi binary.
```

## Observability

`/info`:
```json
{ "telemetry_enabled": true, "telemetry_gpus": 1, "telemetry_ticks": 3 }
```

`/metrics` (Prometheus text format):
```
qsdm_attester_telemetry_gpus
qsdm_attester_telemetry_collection_ticks_total
qsdm_attester_telemetry_collection_errors_total
qsdm_attester_telemetry_apply_calls_total
qsdm_attester_telemetry_requests_total
qsdm_attester_telemetry_sign_failures_total
```

When telemetry is disabled, the `qsdm_attester_telemetry_*`
counters are absent (not zero). A scrape that suddenly stops
seeing them is a positive disable signal, not a metric drop.

## Threat model

- **Forging an attester signature**: requires the HMAC key.
  Same key that signs challenges, so the same
  attestation-chain trust applies.
- **Lying about hardware**: an honest attester can publish
  a faked profile if the operator wants to. The profile
  signature only proves "this attester said this", not
  "this hardware exists". Cross-checks with peer
  attesters' profiles for the same SKU are the network's
  defense — anomalies surface as outliers, not as
  cryptographic failures.
- **Replay across attesters**: the signature binds
  `signer_id`, so a profile signed by attester A cannot be
  republished as if it were attester B's catalog without
  A's key.
- **Stale data**: `issued_at` is freshly stamped at request
  time. Verifiers SHOULD reject profiles older than some
  operator-chosen threshold; the catalog itself is content
  with old observations as long as the issuance is recent.

## What the network does NOT do (yet)

- Reject miner submissions for spec mismatches against the
  catalog. That comes once we have ≥10 attesters publishing
  for ≥3 SKUs each, so a spoofing detector can ground its
  decisions in real distributions instead of single-attester
  data.
- Aggregate profiles across attesters into a global
  catalog. The validator currently treats each profile as
  independent. A future `/api/v1/telemetry/profiles`
  catalog endpoint can roll them up.

## Roadmap

| Tier | What | When |
|---|---|---|
| 1 | Static spec catalog (this doc) | Shipped. |
| 2 | Validator-side advisory check (warn on mismatch) | Next. |
| 3 | Validator-side enforcement (downgrade tier on mismatch) | After ≥10 attesters publish. |
| 4 | Live benchmark fingerprints (timing distributions for canonical kernels) | Independent of 2/3. |
| 5 | Aggregator endpoint at `/api/v1/telemetry/profiles` | Independent of 2/3. |

## Wiring it into your own attester

Your attester already publishes the oracle by default — no
extra steps. To verify locally before exposing it:

```powershell
# Check enabled
Invoke-RestMethod http://127.0.0.1:7733/info | ConvertTo-Json
# Get the signed profile
Invoke-RestMethod http://127.0.0.1:7733/api/v1/telemetry/reference | ConvertTo-Json -Depth 6
```

To disable:

```powershell
.\qsdm-attester.exe --telemetry-disabled
```

To verify externally via the tunnel:

```bash
curl https://api.qsdm.tech/attest/<your-slot>/api/v1/telemetry/reference
```

## Programmatic verification (Go)

```go
import "github.com/blackbeardONE/QSDM/pkg/telemetry"

raw, _ := io.ReadAll(resp.Body)
var p telemetry.ReferenceProfile
_ = json.Unmarshal(raw, &p)
if err := p.Validate(); err != nil { /* malformed */ }
if !p.Verify(operatorKey) { /* signature mismatch */ }
// Inspect p.GPUs[*] for static specs.
```
