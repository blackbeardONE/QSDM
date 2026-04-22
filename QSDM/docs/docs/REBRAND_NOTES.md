# Rebrand notes — QSDM+ → QSDM, with the native coin **Cell (CELL)**

> Audience: node operators, SDK consumers, downstream maintainers. This document is
> normative for the in-repo rebrand executed in Phase 1 of `Major Update.md`. It is
> **not** a marketing document — product copy lives on `qsdm.tech` and in the
> landing / dashboard pages.

## 1. Scope

The platform is migrating from the transitional name **QSDM+** back to **QSDM** and
introducing a native coin, **Cell (CELL)**. No on-chain data, address format, wire
protocol, consensus rule, or cryptographic primitive is changed by the rebrand
itself; this is a **naming and identity** migration plus the first ratchet of the
Cell identity (branding constants, tokenomics documents, trust endpoints). Actual
coin emission and mining protocol work lands in later phases (see `CELL_TOKENOMICS.md`
and `MINING_PROTOCOL.md` once those land).

Anything that is **not** changed by this document:

- The Go module path remains `github.com/blackbeardONE/QSDM`.
- Address formats, signature schemes (ML-DSA-87 via liboqs), GossipSub topics, and
  the `/api/v1/*` REST surface remain unchanged.
- Existing databases, config files, and systemd units keep working without renaming.
- Proof-of-Entanglement + BFT consensus semantics are unchanged and remain CPU-only.
- NVIDIA NGC attestation remains **optional** and **not a consensus rule** — it is a
  transparency signal, re-exposed under the `/api/v1/trust/*` endpoints in Phase 5.

## 2. Migration window

| Date (relative) | Milestone |
|---|---|
| T + 0 (Phase 1 landing) | Preferred names accepted everywhere. Legacy names continue to work. A one-shot deprecation warning is logged the first time a legacy env var or HTTP header is observed. |
| T + 6 months | Legacy names still accepted. Documentation stops referring to them except in migration tables. |
| T + 12 months | Legacy env vars / headers / config file names removed from the deprecation shim. SDK legacy class aliases removed in the next major version bump. |
| T + ? (governance decision) | Repository root directory renamed from `QSDM+/` to `QSDM/` and any folder whose literal name contains `qsdmplus` renamed. This is a **one-way door** change that waits on a coordinated release and is not performed silently in a patch release. |

## 3. Deprecation table — symbol / file / protocol

### 3.1 Environment variables

All `QSDMPLUS_*` environment variables have a preferred `QSDM_*` equivalent. The
shim in `pkg/envcompat` reads the preferred name first, falls back to the legacy
name, and logs a one-shot deprecation warning when only the legacy name is present.

| Preferred | Legacy | Notes |
|---|---|---|
| `QSDM_STRICT_PRODUCTION_SECRETS` | `QSDMPLUS_STRICT_PRODUCTION_SECRETS` | Boolean. Enforces strict handling of admin / HMAC secrets in production. |
| `QSDM_NGC_INGEST_SECRET` | `QSDMPLUS_NGC_INGEST_SECRET` | HMAC key for NGC proof ingest. |
| `QSDM_METRICS_SCRAPE_SECRET` | `QSDMPLUS_METRICS_SCRAPE_SECRET` | Shared secret for the internal Prometheus scrape endpoint. |
| `QSDM_PUBLISH_MESH_COMPANION` | `QSDMPLUS_PUBLISH_MESH_COMPANION` | Boolean truthy gate for publishing the mesh companion feed. |
| `QSDM_WASM_PREFLIGHT_MODULE` | `QSDMPLUS_WASM_PREFLIGHT_MODULE` | Path to the WASM preflight module. |
| `QSDM_METRICS_REGISTER_STRICT` | `QSDMPLUS_METRICS_REGISTER_STRICT` | Boolean. Fails startup when Prometheus collector registration conflicts. |

Apply the rule: **if you are setting a `QSDMPLUS_*` variable today, add a `QSDM_*`
variable with the same value. Remove the legacy one after you have confirmed the
node starts clean.** Operators running systemd units can keep both names set during
the migration; when both are present the preferred name wins.

### 3.2 HTTP headers

| Preferred | Legacy | Direction |
|---|---|---|
| `X-QSDM-NGC-Secret` | `X-QSDMPLUS-NGC-Secret` | Client → node, NGC ingest auth. |
| `X-QSDM-Metrics-Scrape-Secret` | `X-QSDMPLUS-Metrics-Scrape-Secret` | Scrape client → dashboard. |

The node accepts either header on ingress. SDKs emit the preferred header.

### 3.3 NGC proof JSON fields

Bundles produced by the NVIDIA NGC sidecar are accepted with either the `qsdm_*`
or `qsdmplus_*` field names. The HMAC payload canonicalizes on the value regardless
of which field name carried it, so an in-flight rename of the sidecar does not
invalidate proofs.

| Preferred field | Legacy field |
|---|---|
| `qsdm_node_id` | `qsdmplus_node_id` |
| `qsdm_proof_hmac` | `qsdmplus_proof_hmac` |
| `qsdm_ingest_nonce` | `qsdmplus_ingest_nonce` |

### 3.4 Configuration files

The node loads the first file it finds in the following order. Each name is
resolved in both the current working directory and, where applicable, the
standard config search path.

| Preferred | Legacy |
|---|---|
| `qsdm.toml` | `qsdmplus.toml` |
| `qsdm.yaml` | `qsdmplus.yaml` |
| `qsdm.json` | `qsdmplus.json` |

Default database and log paths also prefer `qsdm.db` / `qsdm.log` but fall back to
an existing `qsdmplus.db` / `qsdmplus.log` if found, so running nodes do not
start a new chain on upgrade.

### 3.5 SDK symbols

#### Go

The existing package identifier `qsdmplus` at `sdk/go/` is kept; a new preferred
package is available at `sdk/go/qsdm/`:

```go
// Legacy import (still supported during the deprecation window):
import qsdmplus "github.com/blackbeardONE/QSDM/sdk/go"
c := qsdmplus.NewClient("http://node:8080")

// Preferred import:
import "github.com/blackbeardONE/QSDM/sdk/go/qsdm"
c := qsdm.NewClient("http://node:8080")
```

Type aliases `QSDMClient = Client` and `QSDMPlusClient = Client` are exported from
both packages so existing code continues to compile.

#### JavaScript

The npm package `qsdmplus` is superseded by `qsdm`. Both `qsdm.js` and the legacy
`qsdmplus.js` are shipped in the same tarball during the deprecation window.
`QSDMClient` is the preferred class name; `QSDMPlusClient` is a legacy alias
referring to the same constructor.

```js
const { QSDMClient } = require('qsdm');
const c = new QSDMClient('http://node:8080');
```

### 3.6 CI workflows

| Preferred | Legacy |
|---|---|
| `.github/workflows/qsdm-go.yml` | `.github/workflows/qsdmplus-go.yml` |
| `.github/workflows/qsdm-scylla-staging.yml` | `.github/workflows/qsdmplus-scylla-staging.yml` |

Workflow `name:` and `concurrency.group:` fields were updated accordingly. New
workflows introduced by the Major Update (`release-container.yml` split,
`mining-kernels.yml`, `sdk-js.yml`, `audit-gate.yml`) land in Phase 2 and later
and use the `qsdm-` prefix from the start.

### 3.7 Prometheus metric names

**Status: dual-emit landed.** Every metric the node exposes on
`/api/metrics/prometheus` is now emitted under **both** the legacy
`qsdmplus_*` prefix and the new `qsdm_*` prefix simultaneously. Existing
Grafana dashboards and alert-manager rules referencing `qsdmplus_*`
continue to work unchanged; dashboards written against the new prefix
also work. Per-metric help text on the legacy copies is annotated
`[DEPRECATED alias of qsdm_<name>; set QSDM_METRICS_EMIT_LEGACY=0 to
suppress.]` so the rename is visible in-band when operators `curl` the
endpoint.

Implementation: `pkg/monitoring/prometheus_prefix_migration.go`,
hooked into `PrometheusExporter.Render()`. Tests: 10 dedicated cases
in `prometheus_prefix_migration_test.go` covering default / legacy-off
/ new-off / both-off (force-fallback) states, plus the existing
`TestPrometheusExposition_containsNvidiaSeries` checking legacy names
stay present.

**Per-node knobs (environment variables, hot-reloadable — no restart):**

| Variable | Default | Effect |
|----------|---------|--------|
| `QSDM_METRICS_EMIT_LEGACY` | `1` (on) | When `0`, suppresses every `qsdmplus_*` series. Use after the operator's scrapers are fully on the new prefix. |
| `QSDM_METRICS_EMIT_QSDM` | `1` (on) | When `0`, suppresses every `qsdm_*` series. Unusual but supported (e.g. validator on an old Grafana fleet that explicitly needs legacy). |

Both-off is treated as "legacy only" with a self-observability counter
(`qsdm_metrics_emit_both_suppressed_total`) so the misconfiguration is
always visible in Grafana rather than silently killing the scrape.

**Emission state is self-observable:**

```text
qsdm_metrics_legacy_emission_enabled 1         # 0 once operator cuts over
qsdm_metrics_qsdm_emission_enabled   1         # always 1 after dual-emit lands
qsdm_metrics_emit_both_suppressed_total 0      # non-zero = misconfig
```

The same gauges are emitted under the `qsdmplus_` prefix so they are
discoverable regardless of which scraper is installed.

**Cutover timeline (indicative — subject to operator communication):**

| Release window | Default emission | Operator action |
|----------------|-------------------|------------------|
| now (deprecation window opens) | both (legacy + new) | *optional:* start porting dashboards to `qsdm_*` |
| +1 minor release | both, `qsdm_*` first-class in docs | port dashboards to `qsdm_*` |
| +2 minor releases | both | set `QSDM_METRICS_EMIT_LEGACY=0` on a canary node; verify dashboards; roll out |
| +3 minor releases | default flips to `QSDM_METRICS_EMIT_LEGACY=0` | opt back in if needed |
| +4 minor releases | legacy knob removed | — |

No metric is *renamed* — each just gains a `qsdm_*` twin for the
window. The `/api/metrics/json` JSON surface follows the same schedule
in a separate follow-up PR.

### 3.8 Binaries

| Preferred | Legacy | Notes |
|---|---|---|
| `qsdm` | `qsdmplus` | Node binary. During the deprecation window both names refer to the same build artifact — produced by the same `cmd/qsdmplus/main.go` source. Role is selected at runtime (validator default; miner when `mining_enabled=true` and the `miner` build tag is present — Phase 2.3). |
| `qsdmcli` | `qsdmcli` | Unchanged. |
| `qsdmminer` | — | New binary that lands in Phase 4.3. Runs the reference CPU miner and, post-audit (Phase 6), the CUDA miner. **Not** a consensus participant. |

## 4. Phase-0 values adopted in-repo

`Major Update.md` §11 lists ten open questions that counsel, the project team, and
(where relevant) governance must sign off on before mainnet genesis. For the
purpose of Phases 1–5 in-repo we adopt the plan's recommendations as **working
values**, marked throughout the documentation as "ratified per Phase 0
recommendation, awaiting counsel review". They are **not** binding until counsel
sign-off per §9 Phase 0.

| Open question | Value adopted in-repo | Source |
|---|---|---|
| Mining algorithm candidate | **C — mesh3D-tied useful PoW**, with **A — KawPow-class** as the fallback if C is not audit-ready at launch. | §5.2, §11.1 |
| NVIDIA-favored vs NVIDIA-exclusive | **Stance 1: NVIDIA-favored, not NVIDIA-exclusive.** AMD miners technically accepted; NGC attestation is optional and not a consensus rule. | §5.4, §10.1 |
| Target block time | 10 seconds (inherits from current `pkg/chain` config). | §4.2 |
| Total supply cap | **100,000,000 CELL.** | §4.1 |
| Decimals | **8**, matching Bitcoin. Smallest unit named `dust` in `pkg/branding` (replaces the plan's proposal of "micell" / "cytoplasm", which were both listed as flavour alternatives). | §4.1 |
| Pre-mine | **0%.** | §4.1 |
| Genesis treasury allocation | **10% (10 M CELL)**, vested linearly over 48 months, locked on-chain, treasury address published in the genesis block. | §4.1 |
| Mining emission share | **90% (90 M CELL)**, halving every 4 years. | §4.1 |
| Validator fee model | **Fee-only** (no block subsidy for validators). | §4.1 |
| Burn policy | **Optional EIP-1559-style base-fee burn**, decision deferred to pre-genesis. | §4.1 |
| Coin name fallback | If "Cell" fails trademark clearance the fallback order is **QCell → Cytoplasm → Vertex**. | §10.1, §11.8 |
| Treasury custody | Multisig held by a foundation, per §10.1 mitigation. Exact signer set is a Phase 0 decision and **not encoded in-repo**. | §10.1, §11.10 |

Changing any of these values after Phase 3 (tokenomics doc landing) requires
updating `CELL_TOKENOMICS.md`, the emission schedule unit tests in
`pkg/chain/emission_test.go`, the audit checklist in `pkg/audit/checklist.go`,
and the landing-page tokenomics widget in a single coordinated change.

## 5. What still requires a wall-clock external dependency

These items are explicitly **not** performed by the in-repo rebrand and remain
blocked on external work called out in `Major Update.md` §9:

- Counsel sign-off on the tokenomics posture, treasury vesting legality, Stance-1
  NVIDIA framing, and trademark clearance on "QSDM" and "Cell" (Phase 0).
- Trademark search and filings in US / UK / EU.
- External cryptographic + protocol audit of the mining layer (Phase 4, gating the
  CUDA miner ship).
- Incentivized testnet with ≥ 100 concurrent home miners (Phase 4 milestone).
- Mainnet genesis ceremony (one-way door).
- Repository root directory rename from `QSDM+/` to `QSDM/` (requires a coordinated
  tag / release to avoid breaking local clones and CI paths).

## 6. Operator checklist (one-time)

For each running node:

1. Add the `QSDM_*` equivalents of every `QSDMPLUS_*` variable currently set on the
   host, leaving the legacy values in place for one restart cycle so you can
   roll back cleanly.
2. Restart the node once and confirm no deprecation warning is logged.
3. If you have monitoring or scrape scripts that set `X-QSDMPLUS-*` headers,
   update them to `X-QSDM-*`; the node accepts either during the window.
4. If you ship the NVIDIA NGC sidecar, update it to emit `qsdm_*` JSON fields
   once the sidecar release containing the rename is available. The node
   continues to accept the legacy field names in the interim.
5. Configuration files named `qsdmplus.*` continue to work. You may rename them
   to `qsdm.*` at your next scheduled maintenance window; no other change is
   required.
6. When updating SDK consumers:
   - Go: switch imports from `github.com/blackbeardONE/QSDM/sdk/go` to
     `github.com/blackbeardONE/QSDM/sdk/go/qsdm`.
   - JavaScript: switch from `require('qsdmplus')` to `require('qsdm')`, and
     from `QSDMPlusClient` to `QSDMClient`.

## 7. References

- `Major Update.md` — the execution plan. Kept at the repository root until the
  five in-repo phases are green, then archived to
  `QSDM/docs/docs/history/MAJOR_UPDATE_EXECUTED.md`.
- `NEXT_STEPS.md` — current phase progress.
- `QSDM/docs/docs/CELL_TOKENOMICS.md` — Cell supply schedule and fee model
  (lands in Phase 3.1).
- `QSDM/docs/docs/MINING_PROTOCOL.md` — normative mining spec
  (lands in Phase 4.1).
- `QSDM/docs/docs/NODE_ROLES.md` — validator vs miner hardware model
  (lands in Phase 3.2).
- `QSDM/source/pkg/branding/branding.go` — canonical product identity constants.
- `QSDM/source/pkg/envcompat/envcompat.go` — env-var deprecation shim.
