# Changelog

All user-visible changes to QSDM are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
uses [Semantic Versioning](https://semver.org/) for tagged releases.

Historical project activity prior to the Major Update rebrand (Phases
1–5, executed 2026-04-22) is captured verbatim in
[`QSDM/docs/docs/history/MAJOR_UPDATE_EXECUTED.md`](QSDM/docs/docs/history/MAJOR_UPDATE_EXECUTED.md)
and the `QSDM/docs/docs/archive/` folder; this file does **not**
attempt to retroactively enumerate that history.

## [Unreleased]

### Changed

- **Landing-page roadmap widget synced with reality (2026-04-23).**
  `deploy/landing/index.html` was still showing Phase 2 as "In progress"
  and Phase 3 as "Next" — both phases have been shipped in-tree for
  several sessions (submesh rules, Scylla migrate with dry-run,
  `/api/v1/network/topology`, finality-gadget partition heal, NVIDIA
  lock enforcement). Flipped the Phase 2 and Phase 3 status pills to
  `Shipped`, rewrote the lead paragraph to stop claiming deployments
  are on "Phase 1 infrastructure with Phase 2 submesh routing enabled",
  and added a post-grid beat linking to `CELL_TOKENOMICS.md`,
  `MINING_PROTOCOL.md`, and the `/trust.html` surface so the widget
  doesn't imply development ended at Phase 3 (the in-repo Major Update
  Phases 1–5 continue the arc, with only wall-clock-blocked gates —
  trademark filings, `mining-01` external audit, `mining-05`
  incentivized testnet, and the mainnet genesis ceremony — remaining,
  as tracked in `NEXT_STEPS.md`). Same phase-card CSS
  (`repeat(3, 1fr)` grid, `.status.shipped` pill), so no stylesheet
  changes were needed.

### Added

- **Quarantine Prometheus gauges + alert group (2026-04-23).**
  `pkg/quarantine/metrics.go` now exports four gauges via a
  nil-safe `MetricsCollector(*QuarantineManager)` closure, mirroring
  the `api.TrustMetricsCollector` pattern:

    - `qsdm_quarantine_submeshes` — count of submeshes currently
      quarantined.
    - `qsdm_quarantine_submeshes_tracked` — distinct submeshes the
      manager has ever observed (union of the three internal maps, so
      a submesh with only 1–9 transactions is counted before the
      10-tx window boundary writes into `quarantined`).
    - `qsdm_quarantine_submeshes_ratio` — quarantined / tracked, with
      `0` when `tracked==0` so ratio-based alerts don't flap on a
      quiet node.
    - `qsdm_quarantine_threshold` — the configured invalid-ratio
      policy threshold, exposed so dashboards render the decision
      boundary next to the observed state.

  The collector is registered in `cmd/qsdmplus/main.go` alongside the
  other `pe.RegisterCollector(...)` calls inside the `dash != nil`
  block. A new method `QuarantineManager.Stats()` returns a consistent
  snapshot under the existing mutex; collector scrapes are O(1) in
  the number of tracked submeshes.

  `alerts_qsdmplus.example.yml` gains a new `qsdm-quarantine` group
  with two rules:

    - **`QSDMQuarantineAnySubmesh`** (warn, 10m) — any non-zero
      quarantined count worth a human decision on recovery.
    - **`QSDMQuarantineMajorityIsolated`** (critical, 15m) — fires
      when `ratio > 0.5` and `tracked >= 4` (the `tracked` guard
      prevents flap on tiny fleets where 1/2 crosses the ratio).

  No warm-gate is needed (the manager is live from process start, so
  zero-at-t=0 is literally correct, and the denominator guard inside
  the collector keeps empty-fleet scrapes from paging). Tests live in
  `pkg/quarantine/metrics_test.go` covering nil-manager, empty-manager
  shape, sub-window tracking, full-window quarantine, post-removal
  gauges, and the `Stats.Quarantined` counts-only-true invariant.

- **`ATTESTATION_SIDECARS.md` operator guide (2026-04-23).** The
  recipe for getting the trust pill to `N/N` by standing up N
  attestation sources was previously spread across two CHANGELOG
  entries, `install_ngc_sidecar_vps.py` docstrings, and session
  notes. New `QSDM/docs/docs/ATTESTATION_SIDECARS.md` consolidates
  it: the reference three-source deployment (Windows PC + BLR1 VPS
  + OCI), the four required invariants (shared ingest URL, shared
  `QSDMPLUS_NGC_INGEST_SECRET`, **distinct** `QSDMPLUS_NGC_PROOF_NODE_ID`,
  cadence ≤ `fresh_within`/2), one-command install snippets per
  platform, a five-step verification ladder
  (`journalctl` → ingest counter → `qsdm_trust_*` gauges → public
  summary JSON → external CI probe), and a troubleshooting table
  keyed on the symptom operators actually see. Cross-linked from
  the aggregator implementation, Prometheus gauges, alert rule
  example, and the `trustcheck --min-attested` flag so someone
  landing in any of those files can find the canonical setup.

### Changed

- **`build_kernels.ps1` defaults to a Turing→Hopper fatbin
  (2026-04-23).** Previous default `-Arch 'sm_86'` only produced a
  DLL that worked on Ampere; running the same DLL on an RTX 4090
  (Ada, sm_89) or an H100 (Hopper, sm_90) silently fell back to a
  JIT recompile or failed to launch. New default
  `'sm_75,sm_86,sm_89,sm_90'` emits a fatbin covering Turing, Ampere,
  Ada, and Hopper in one build — roughly +30 s compile vs. the
  single-arch default, and no per-card rebuild for the four GPU
  lineups we've exercised on. Iterating on a known host can still
  narrow via `-Arch 'sm_86'` explicitly (documented under
  `.PARAMETER Arch` and in the `MESH3D_GPU_BENCHMARK.md`
  reproduction steps).

- **Prometheus alert rules for the trust-redundancy surface
  (2026-04-23).** Now that `qsdm_trust_attested` /
  `qsdm_trust_total_public` / `qsdm_trust_last_attested_seconds` /
  `qsdm_trust_last_checked_seconds` / `qsdm_trust_warm` /
  `qsdm_trust_ngc_service_healthy` gauges are exported by
  `api.TrustMetricsCollector`, the example rule file
  `QSDM/deploy/prometheus/alerts_qsdmplus.example.yml` gains a new
  `qsdm-trust-redundancy` group mirroring the external CI probe's
  `--min-attested 2` floor from inside Alertmanager:

    - **`QSDMTrustAttestationsBelowFloor`** — fires when a *warm*
      aggregator reports `qsdm_trust_attested < 2` for 10 min. Gated
      on `qsdm_trust_warm == 1` so redeploys do not page (the ring
      buffer is volatile; `attested` recovers on the next sidecar
      cadence).
    - **`QSDMTrustNGCServiceDegraded`** — fires when a warm aggregator
      reports `qsdm_trust_ngc_service_healthy == 0` (mapped from the
      summary JSON's `ngc_service_status` enum) for 10 min.
    - **`QSDMTrustLastAttestedStale`** — fires when the newest
      attestation is older than 30 min (twice the default
      `fresh_within` of 15 min), catching slow-death scenarios
      before `attested` itself tips to zero.
    - **`QSDMTrustAggregatorStale`** (severity `critical`) — fires
      when `qsdm_trust_last_checked_seconds` has not advanced for
      > 2 min (Refresh ticker wedged). Default refresh cadence is
      10 s, so this is unambiguously a stuck goroutine.

  The existing `qsdm-trust-transparency` group (proxy-metric alerts
  on the accepted/rejected ingest counter) is complementary and
  retained: it fires when *no* proof is flowing at all, regardless of
  aggregator state; the new group fires when proofs flow but the
  aggregator's distinct-source count drops below the operator's
  declared floor. Both sides — external CI probe + internal
  Prometheus — now enforce the same `attested >= 2` invariant from
  independent vantage points.

- **Prometheus gauges for the trust-transparency surface
  (2026-04-23).** The §8.5.x trust numbers (`attested`,
  `total_public`, `ratio`, `ngc_service_status`, `last_attested_at`,
  `last_checked_at`, warm-up state) were previously only available
  via `GET /api/v1/trust/attestations/summary`. Alertmanager and
  Grafana cannot scrape a bespoke JSON endpoint without bespoke
  exporters, so a silent drop from `attested=3` back down to
  `attested=1` was undetectable short of a human checking the
  widget. New `api.TrustMetricsCollector(*TrustAggregator)`
  registers a nil-safe, O(1) collector on
  `monitoring.GlobalScrapePrometheusExporter()` that surfaces:

    - `qsdm_trust_attested`                (gauge)
    - `qsdm_trust_total_public`            (gauge)
    - `qsdm_trust_ratio`                   (gauge)
    - `qsdm_trust_ngc_service_healthy`     (gauge, 0/1)
    - `qsdm_trust_last_attested_seconds`   (gauge, unix seconds)
    - `qsdm_trust_last_checked_seconds`    (gauge, unix seconds)
    - `qsdm_trust_warm`                    (gauge, 0/1)

  The collector reads the aggregator's already-cached summary on
  every scrape, so there is no new locking, no new ticker, no new
  wire traffic. It registers unconditionally when
  `[trust] disabled=false` and emits nothing when the aggregator
  is disabled (rather than zeroes that would falsely imply a
  denominator). Grafana alerts gated on `qsdm_trust_warm == 1` stay
  silent through a restart because the warm bit flips only after
  the aggregator's first full `Refresh()`. Full gauge shape,
  HELP text, and timestamp-parse behaviour are covered by
  `pkg/api/trust_metrics_test.go`.

- **`trustcheck --min-attested N` policy floor (2026-04-23).** The
  external transparency probe previously only validated the §8.5.x
  wire contracts (scope-note verbatim, enum membership,
  ratio-sanity, etc.) — none of which trip when a deployment
  silently loses attestation sources. Every value of `attested`
  from 0 through the entire validator set is a legal wire contract,
  by design. A new operator-policy flag `--min-attested N` lets a
  deployment declare an intended redundancy floor; the probe fails
  loudly when `summary.attested < N`. The assertion lives in a
  standalone `validateMinAttested` helper so running trustcheck
  without the flag (default `0`) behaves exactly as before. The
  GitHub-Actions workflow `.github/workflows/trustcheck-external.yml`
  defaults to `--min-attested 2` on scheduled / push / pull_request
  runs (matching the deployed "primary validator + OCI sidecar"
  invariant), and exposes a `min_attested` workflow-dispatch input
  so ops can drop the floor to `0` during a single-sidecar
  maintenance window.

- **Trust aggregator now counts distinct CPU-fallback sidecars as
  separate attestation sources (2026-04-23).** Operators who run
  multiple CPU-fallback attestation sidecars — e.g. one on the main
  validator VPS, one on an Oracle Cloud VM, one on a local dev PC,
  each stamping its own `QSDMPLUS_NGC_PROOF_NODE_ID` — were being
  collapsed into a single "local" peer row by `TrustAggregator`.
  `MonitoringLocalSource.LocalLatest()` only exposed the newest row
  from `monitoring.NGCProofSummaries()` and stamped it with the
  validator's libp2p host id, so ten sidecars looked like one peer
  and `attested` never climbed past 1 no matter how much redundant
  CPU attestation was running.

  New `monitoring.NGCProofDistinctByNodeID()` walks the NGC proof
  ring buffer and groups entries by `qsdmplus_node_id` (or the
  legacy `qsdm_node_id` alias), keeping the newest-observed bundle
  for each distinct id. New optional interface
  `api.LocalDistinctAttestationSource` exposes that view to the
  aggregator; `MonitoringLocalSource` now implements it, and
  `TrustAggregator.Refresh` prefers the distinct view when
  available, folding empty-id rows onto the local node's identity
  so bundles without an id still behave like before. Sources that
  don't implement the new interface fall back to the old single-row
  path — this is a strict addition, not a behaviour swap for legacy
  embedders.

  Verified live against the reference validator: adding the second
  and third sidecars (OCI `ap-singapore-1` and DO `blr1`) flipped
  `GET /api/v1/trust/attestations/summary` on `api.qsdm.tech` from
  `attested=1, total_public=2` to `attested=3, total_public=4`
  within one 10 s refresh tick, with each sidecar showing a distinct
  redacted `node_id_prefix` in `/recent`.

  Semantics note (recorded here, not a regression): anyone holding
  `QSDMPLUS_NGC_INGEST_SECRET` can drive `attested` up by POSTing
  from N distinct `qsdmplus_node_id` values. That is acceptable
  because (a) the ingest secret is already the trust root for this
  surface, (b) the `scope_note` field in every summary response
  caveats that these attestations are not consensus, and (c) the
  aggregator's freshness window (default 15 min) still applies per
  row, so a one-shot spoof cannot hold the pill green without
  continuous posts.

### Fixed

- **mesh3d Windows DLL now exports its host-side entry points
  (2026-04-23).** `pkg/mesh3d/kernels/sha256_validate.cu` declared
  `mesh3d_hash_cells` and `mesh3d_validate_cells` inside an
  `extern "C"` block but without `__declspec(dllexport)`. On Linux
  this is harmless — ELF exports every non-static symbol by default
  — but MSVC's PE linker only exports symbols explicitly marked
  dllexport, so the Windows build of `mesh3d_kernels.dll` shipped
  with a single `NvOptimusEnablementCuda` export and nothing else.
  CGO builds with `-tags cuda` then died with
  `undefined reference to 'mesh3d_hash_cells'` at link time.

  New `MESH3D_API` macro (`__declspec(dllexport)` on `_WIN32`,
  `__attribute__((visibility("default")))` elsewhere) now decorates
  both entry points; Linux `.so` behaviour is unchanged, Windows
  `.dll` now correctly exports the two symbols CGO needs. Verified
  via `gendef - mesh3d_kernels.dll` showing the symbols and via
  the `BenchmarkMesh3DGPUVsCPU` linking against them.

- **`pkg/mesh3d/cuda.go` no longer requires a `C:/CUDA` symlink on
  Windows (2026-04-23).** The old cgo directive block hard-coded
  `-IC:/CUDA/include -LC:/CUDA/lib/x64`, which is nowhere an
  NVIDIA installer ever lands. Every fresh Windows dev box failed
  to build until the operator manually created `C:\CUDA`. Replaced
  with split platform directives: Linux keeps `/usr/local/cuda`
  defaults, Windows relies on `CGO_CFLAGS` / `CGO_LDFLAGS` set by
  `QSDM/scripts/build_kernels.ps1`, which probes `$env:CUDA_PATH`
  and emits DOS 8.3 short-path forms so cgo's whitespace-splitting
  directive parser sees no spaces.

- **Dashboard user accounts now survive a service restart
  (2026-04-23).** `pkg/api.UserStore` used to be an in-memory
  `map[string]*User` with no persistence layer, so every
  `systemctl restart qsdmplus` (routine during redeploys) silently
  wiped every registered dashboard login. `AuthenticateUser` returned
  "user not found", which the handler then mapped to a generic
  `401 invalid credentials` — so the symptom an operator saw was
  "I swear I registered yesterday, why am I locked out?". Ledger
  state (transactions, balances, staking, bridge) was already
  persisted; only the dashboard-login credential map was affected.

  Fix in `pkg/api/user_persist.go`: versioned JSON file
  (`/opt/qsdmplus/qsdmplus_users.json`, mode `0600`), atomic
  temp-file-rename on every mutation, loader fail-closed on unknown
  version / malformed JSON (never silently reset). `RegisterUser`
  rolls back the in-memory insert when the disk write fails, so
  callers never observe a "registered but not persisted" half-state.

  Configured via `Config.UserStorePath`
  (TOML `[api] user_store_path`) with env overrides
  `QSDM_USER_STORE_PATH` / legacy `QSDMPLUS_USER_STORE_PATH`. The
  default in `cmd/qsdmplus/main.go` is
  `<dirname(SQLitePath)>/qsdmplus_users.json`, matching the sibling
  `qsdmplus_staking.json` / `qsdmplus_bridge_state.json` layout.

  Tests in `pkg/api/user_persist_test.go` cover the round-trip
  (register → reopen → auth), persist-failure rollback, unknown-
  version fail-closed, and malformed-JSON fail-closed. Verified
  end-to-end against the live VPS on 2026-04-23: registered,
  `systemctl restart qsdmplus`, re-logged in successfully from
  `https://dashboard.qsdm.tech/`.

### Added

- **Full mesh3d GPU benchmark runnable on a dev box (2026-04-23).**
  New `QSDM/source/pkg/mesh3d/mesh3d_gpu_bench_test.go` contains
  `BenchmarkMesh3DGPUVsCPU_Validate` and `_Hash`, each sweeping
  n ∈ {16, 256, 4096} across the CUDA and CPU-parallel backends
  with `b.SetBytes` so `go test -bench` prints MB/s directly.
  Skips with a clear diagnostic (`build mesh3d_kernels.dll / .so
  first`) when the GPU path isn't available, so CI runs on the
  CPU baseline without failing.
- **`QSDM/scripts/build_kernels.ps1` — Windows CUDA kernel build
  helper (2026-04-23).** Auto-locates CUDA via `$env:CUDA_PATH`
  (with a fall-back scan of the canonical install root),
  auto-locates MSVC via `vswhere.exe`, sources `vcvars64.bat`
  into a `cmd.exe` subshell for nvcc, compiles
  `mesh3d_kernels.dll` with per-GPU `-gencode` (default `sm_86`
  for the RTX 3050, comma-list supported), mirrors the DLL next
  to the Go source, regenerates a MinGW-compatible
  `libmesh3d_kernels.dll.a` via `gendef` + `dlltool` so MSYS2 Go
  + cgo can link it, and prints (or sets with `-SetEnv`) the
  `CGO_CFLAGS` / `CGO_LDFLAGS` / `PATH` lines the next build
  needs.
- **`QSDM/scripts/build_liboqs_win.ps1` — local liboqs build
  (2026-04-23).** Clones liboqs into
  `%LOCALAPPDATA%\QSDM\liboqs`, configures with CMake + Ninja +
  MinGW-w64 gcc + MSYS2 OpenSSL 3, builds the `oqs` target with
  `-DOQS_OPT_TARGET=generic` (MinGW doesn't assemble the AVX2
  fast paths), installs to `%LOCALAPPDATA%\QSDM\liboqs_install`,
  and emits CGO env lines that compose cleanly with the CUDA
  ones. Total runtime ~2 min on an RTX-3050-class dev box.
  Matches the Dockerfile.miner production build so local and
  CI signing/verification produce the same artefacts.
- **`docs/docs/MESH3D_GPU_BENCHMARK.md` — reference benchmark
  numbers (2026-04-23).** RTX 3050 + Xeon E5-2670 reference
  figures (0.04× at n=16, 4.06× at n=4096 for validate; 0.03×
  / 2.23× for hash) with the reproduction recipe and operator
  guidance on when a GPU actually helps mesh3d throughput. Cited
  from `docs/docs/MINER_QUICKSTART.md` so a new miner operator
  knows whether buying a card is worth it for their fan-out.

- **Live trust pill on `qsdm.tech` navigation bar (2026-04-23).**
  Compact `trust: 1/2 · healthy` chip next to `Open Dashboard`, poll
  of `/api/v1/trust/attestations/summary` every 60 s, four visual
  states (healthy / warming / degraded / offline). Shares the existing
  trust-widget fetch loop — one HTTP call drives both the pill and
  the pre-existing full-width widget. Hidden on viewports `<900px` so
  it does not crowd mobile.
- **Prometheus alert rules for attestation transparency
  (2026-04-23).** New group `qsdm-trust-transparency` in
  `deploy/prometheus/alerts_qsdmplus.example.yml` with two rules on
  the canonical `qsdm_*` prefix:
  `QSDMTrustNoAttestationsAccepted` (accepted-rate zero for 20 m) and
  `QSDMTrustIngestRejectRateElevated` (rejects outpace accepts by
  >1/s for 10 m). Safe to load on both dual-emit and legacy scrapers.
- **Grafana panels 16 + 17 in `qsdmplus-overview.json`
  (2026-04-23).** Stat "NGC attestations in last 15 min" with
  red/yellow/green thresholds matched to the pill on qsdm.tech
  (0 = red, ≥1 = orange, ≥3 = green) plus a bar chart
  "NGC attestations per hour (rolling)" for Scheduled-Task cadence
  verification. Idempotent patch — panel IDs 1–15 unchanged.
- **Self-rotating transcript in `local-attest.ps1` (2026-04-23).**
  New `-LogPath` / `-LogMaxBytes` (default 10 MiB) / `-LogKeep`
  (default 3) parameters. Rotates ring-style (`.1`, `.2`, `.3`)
  before opening the transcript and between loop iterations so the
  10-min refresh cadence does not grow a hundreds-of-MB transcript
  in a week on the refresh PC. Forwarded by
  `attest-from-env-file.ps1` as a splat. Documented in
  `apps/qsdmplus-nvidia-ngc/QUICKSTART.md` §8a.
- **GitHub Wiki live (2026-04-23).** `QSDM/scripts/sync-wiki.sh`
  now publishes eight pages to
  <https://github.com/blackbeardONE/QSDM/wiki> with a shared sidebar
  and footer: Home (Operator Guide), Node Roles,
  Validator/Miner Quickstart, Mining Protocol, Cell Tokenomics,
  NVIDIA Lock Scope, NGC Sidecar Quickstart. Pages auto-update from
  the canonical markdown under `QSDM/docs/docs/` whenever the script
  is re-run.
- **VPS-side CPU-fallback NGC attestation sidecar (2026-04-23).**
  New installer `QSDM/deploy/install_ngc_sidecar_vps.py` deploys
  `validator_phase1.py` to `/opt/qsdmplus/ngc-sidecar/`, reuses the
  existing `QSDMPLUS_NGC_INGEST_SECRET` from the qsdmplus service
  environment, and installs a systemd oneshot + 10-minute timer
  (`qsdm-ngc-attest.service` / `.timer`). The script runs without a
  GPU — `gpu_fingerprint` falls back to `available: false` and the
  fp16 matmul path degrades to `stub_no_cuda` cleanly. Result: the
  `/api/v1/trust/attestations/summary` badge stays `healthy` even
  when the operator's dev PC is offline. Re-running the installer
  picks up a rotated secret automatically.

### Security

- **Webviewer refuses to start on insecure default credentials
  (2026-04-22).** `internal/webviewer` used to silently fall back to
  `admin` / `password` when `WEBVIEWER_USERNAME` / `WEBVIEWER_PASSWORD`
  were unset — which was acceptable when the code was private but is
  now a real foot-gun with QSDM public on GitHub: anyone who clones,
  builds, and `./qsdmplus`-es without reading the docs gets a wide-open
  log stream on port `9000` / `LOG_VIEWER_PORT`. `StartWebLogViewer`
  now returns the new `webviewer.ErrInsecureDefaultCreds` when either
  var is unset or empty, and `cmd/qsdmplus/main.go` logs a clear
  remediation message and keeps the node running *without* the log
  viewer. Operators who explicitly want the old behaviour for local
  development must now opt in with `QSDM_WEBVIEWER_ALLOW_DEFAULT_CREDS=1`,
  which also emits a loud `[WEBVIEWER][WARN]` banner on every
  start-up. Basic-auth compares use `crypto/subtle.ConstantTimeCompare`
  now to remove the trivial timing side-channel. Covered by seven new
  unit tests in `internal/webviewer/webviewer_creds_test.go` (with
  eleven subtest permutations covering both-set, either-unset,
  truthy/falsey opt-in values, and the opt-in-doesn't-override-real-
  creds invariant). The existing integration test in
  `tests/webviewer_test.go` was updated to set the opt-in flag
  explicitly so its use of `admin`/`password` is intentional rather
  than accidental. The live VPS is unaffected because both env vars
  have been provisioned in `/etc/systemd/system/qsdmplus.service.d/secrets.conf`
  since the 2026-04-22 secret-rotation pass.

### CI

- **Legacy-metric guard in GitHub Actions (2026-04-22).** During the
  `qsdmplus_*` -> `qsdm_*` Prometheus metric-name prefix migration
  (Major Update §6), the dual-emit machinery in
  `pkg/monitoring/prometheus_prefix_migration.go` already publishes
  every metric under both prefixes, so any new code should register
  metrics under the canonical `qsdm_*` name only. Added
  `QSDM/scripts/check-no-new-legacy-metrics.sh` which `rg`-greps the
  Go tree for hand-written `"qsdmplus_<name>_(total|count|seconds|sum|
  bucket|bytes|info|ratio|current|last|active|inflight)"` string
  literals and fails if any appear outside a tight five-file allowlist
  of files that are part of the dual-emit machinery or test it. The
  regex is deliberately narrow so it does NOT flag non-metric branding
  aliases like `qsdmplus_node_id` in `pkg/branding/branding.go`
  (those are NGC proof JSON field names, not metrics). Wired into
  `.github/workflows/qsdm-go.yml` `build-test` job as the very first
  step so regressions fail fast without burning build compute. Has a
  `git grep` fallback for runners without ripgrep.

### Repository

- **`.gitattributes` forcing LF for scripts + YAML (2026-04-22).** The
  existing clone has `core.autocrlf=false`, so `.sh` / `.py` files
  authored on Windows were being committed with CRLF line endings and
  only working on CI by accident of how `bash script.sh` tolerates
  trailing `\r`. Added a minimal `.gitattributes` that pins `*.sh`,
  `*.py`, `*.yml`, `*.yaml`, and `Dockerfile` to LF at the blob level.
  Does not rewrite existing working copies -- just protects every
  future checkout and new file from the shebang-breakage class of
  bug (e.g. `/usr/bin/env: 'bash\r': No such file or directory`).

### Changed — deploy scripts

- **VPS host/user are now env-configurable (2026-04-22).** Every
  script under `QSDM/deploy/*.py` used to hardcode the reference
  validator's IP (`206.189.132.232`) and SSH user (`root`) at the top
  of the file. That made the scripts unusable by anyone forking the
  repo to run their own QSDM node, and it meant a future VPS
  migration would be a cross-file sed pass every single time. Added
  a tiny shared helper at `QSDM/deploy/_deploy_host.py` that reads
  `QSDM_VPS_HOST` / `QSDM_VPS_USER` from the environment with a
  sensible fallback to the historical reference-node values (the IP
  is not a secret — it is the public A record for `api.qsdm.tech` in
  DNS and is already documented in `QSDM/deploy/Caddyfile`). All
  seven scripts (`remote_apply`, `remote_verify`, `remote_harden_ssh`,
  `remote_install_caddy`, `remote_cmd`, `remote_fix_service`,
  `remote_bootstrap`) now import from `_deploy_host`. Running them
  against the reference node is unchanged; running them against a
  different node is now a single `$env:QSDM_VPS_HOST = '...'` away.

### Changed — repository / licensing

- **MIT license surfaced at the repo root (2026-04-22).** `QSDM/LICENSE`
  was one level too deep for GitHub's license detector, so the repo
  page was displaying neither a licence badge nor the "MIT" tag in the
  sidebar even though the project has always been MIT-licensed. Copied
  the licence to `/LICENSE` at the repo root (keeping `QSDM/LICENSE`
  in place so nothing else breaks), bumped the copyright line to
  `2024-2026` in both copies, and added a `## License` section to the
  root `README.md` that links to it. Also trimmed the root `README`
  down to files that actually exist in the public tree — the previous
  version pointed at several internal docs (`NEXT_STEPS.md`,
  `Major Update.md`, `nvidia_locked_qsdmplus_blockchain_architecture.md`,
  `apps/game-integration/`) that are correctly excluded from the
  public repo by `.gitignore`, so those links were 404s on GitHub.

### Deployed

- **VPS root password rotated (2026-04-22).** The previous root
  password has been retired now that `ed25519` key-auth is the proven
  primary SSH path (every deploy in the Major Update window used it).
  Rotation was performed over the existing key channel using
  `chpasswd` on stdin — the new secret never appears in argv, bash
  history, or the process table on the remote host. Key-auth was
  verified end-to-end by opening a second independent connection
  after the rotation, so the session could not have locked itself
  out. The new password lives only in `vps.txt` (which is
  `.gitignore`d) and is intended purely as a break-glass fallback via
  the DigitalOcean web console; no automation reads it.
- **Workspace placed under version control (2026-04-22).** Twelve
  weeks of Major Update work was living on a single Windows disk with
  no git history — a real disaster-recovery hole given the project
  is already public. `git init` + initial import commit on `main`
  (`bab2f8f`, 930 files, 129,786 insertions). The `.gitignore` was
  designed defensively around what actually exists on disk: it
  excludes `vps.txt`, the legacy `Nvidia Token` file, all `*.env`
  (except committed `*.env.example` templates), TLS material
  (`QSDM/api_server.crt`/`.key`), Go build artifacts (`*.exe`,
  `*.test`, explicit `qsdmplus`/`qsdmminer`/`trustcheck` paths),
  `target/` trees (Rust), runtime state (`*.log`, `QSDM/databases/*.db`,
  timestamped SQL dumps, `QSDM/storage/tx_*.dat`), and the usual
  Python/Node/IDE/OS caches. Vendored third-party binaries (notably
  `QSDM/source/wasmer-go-patched/.../libwasmer.*`) are deliberately
  kept because the build depends on them. Pre-commit audit confirmed
  no explicit secret path (vps.txt, api_server.key/crt, Nvidia Token,
  `_tmp_*.py`) slipped into the staged tree; largest staged file is
  ~15 MB (`libwasmer.so` for `linux-amd64`), well under GitHub's
  100 MB per-file soft cap. No remote is configured — that's a
  separate decision.
- **NGC proof-ingest secret provisioned on the VPS (2026-04-22).**
  Generated a fresh 256-bit random secret (`secrets.token_hex(32)`, 64
  hex chars — comfortably above the 16-char strict-secrets floor and
  obviously not a `charming123*` dev placeholder) and installed it as a
  systemd drop-in at
  `/etc/systemd/system/qsdmplus.service.d/ngc-secret.conf` (mode `0600`,
  owner `root:root`). Both the canonical `QSDMPLUS_NGC_INGEST_SECRET`
  and the legacy `QSDM_NGC_INGEST_SECRET` env keys are exported so the
  deprecation window for older sidecars is honoured. The drop-in path
  is chosen deliberately: subsequent runs of
  `deploy/remote_apply_paramiko.py` rewrite the unit file at
  `/etc/systemd/system/qsdmplus.service` but leave the `.service.d/`
  directory untouched, so the secret survives redeploys without ever
  entering the repo or the tarball. Post-install probes:
  `GET /api/v1/monitoring/ngc-proofs` with the correct
  `X-QSDMPLUS-NGC-Secret` returns `HTTP 200 {"count":0,"proofs":[]}`,
  a wrong secret returns `HTTP 401`, and no NGC-related errors appear
  in the journal. The ingest surface is now gated-live, but the
  `attested/total_public` ratio stays at `0/1` until a sidecar with
  matching secret and a real NVIDIA NGC attestation submits its first
  proof — that's a separate bring-up gated on having a GPU host, not
  on anything in this repo.
- **Trust surface denominator fix — `total_public` 2 → 1 (2026-04-22).**
  The `"bootstrap"` placeholder address, which `cmd/qsdmplus/main.go`
  registers against `nodeValidatorSet` purely to satisfy BFT quorum on
  a single-node network, was being counted by the transparency widget
  as if it were a public peer. The new `sentinelValidatorAddresses`
  allowlist in `pkg/api/trust_peer_provider.go` filters it out, so the
  live `/api/v1/trust/attestations/summary` on the VPS now reports
  `{"attested":0,"total_public":1}` — one real validator, zero fresh
  attestations, which is the honest anti-claim answer.
  (`pkg/api/trust_peer_provider.go`,
  `pkg/api/trust_peer_provider_test.go`).
- **Dashboard login page — registration/hostname copy removed
  (2026-04-22).** The two-paragraph `<div class="info">` block that
  explained `POST /api/v1/auth/register` and `localhost` vs `127.0.0.1`
  session-cookie guidance was stripped from
  `internal/dashboard/dashboard.go`. The `<noscript>` fallback notice
  is retained because it serves an accessibility purpose rather than
  a documentation one. Confirmed live on
  `https://dashboard.qsdm.tech/`.
- **Deploy-log noise fix — Caddy reload false alarm silenced
  (2026-04-22).** `systemctl reload caddy` returned non-zero on this
  host because the Caddyfile sets `admin off`, even though the config
  reload itself succeeded. `remote_apply_paramiko.py` now redirects
  reload/restart stderr+stdout and surfaces only
  `caddy: <is-active>` so a healthy deploy log stops containing
  "Job for caddy.service failed" red herrings.
- **Production VPS redeploy — Major Update payload live (2026-04-22).**
  `206.189.132.232` (Bangalore `blr1`) was upgraded from the
  pre-rebrand build to the current `[Unreleased]` tree. Public probes
  against `https://qsdm.tech/`, `https://qsdm.tech/trust.html`,
  `https://qsdm.tech/api/v1/trust/attestations/summary`, and
  `https://api.qsdm.tech/api/v1/health/live` all return HTTP 200. The
  trust aggregator is wired and serving the honest anti-claim payload
  `{"attested":0,"total_public":2,"ratio":0,…,"scope_note":"NVIDIA-lock
  is an opt-in, per-operator API policy — not a consensus rule …"}`.
  Node identity: `12D3KooWB639f3GXxAuyqgZnk8so9ZVVstNxxvU6W1ncjXoAbVKS`;
  `[trust]` block defaults applied on first restart
  (`fresh_within=15m`, `refresh_interval=10s`). Landing page
  (`index.html` + `trust.html`) synced to `/var/www/qsdm/` and served
  by the existing Caddy edge.

### Changed — deployment tooling

- **`QSDM/deploy/remote_apply_paramiko.py` rewritten** as a full-tree
  apply (tarball of 908 Major Update files, ~42 MiB gzipped) instead
  of the previous 4-file hotfix. Now: auto-auths with
  `~/.ssh/id_ed25519` before falling back to `QSDM_VPS_PASS`; restores
  the Unix exec bit on all `*.sh`/`*.py` after tar extraction
  (Windows-safe); keeps the existing CGO+liboqs production profile by
  default and exposes `QSDM_BUILD_TAGS=validator_only` as an opt-in
  cutover lever; non-destructively appends a `[trust]` block to
  `/opt/qsdmplus/qsdmplus.toml` on servers that predate the aggregator
  wiring; rolls back to `/opt/qsdmplus/qsdmplus.prev` if the new binary
  fails systemd's `is-active` gate; probes health, trust, dashboard,
  and the Caddy edge (`api.qsdm.tech`, `qsdm.tech` via
  `curl --resolve`) at the end so the operator sees green probes
  inline with the deploy log. `remote_verify_paramiko.py` gained the
  matching trust-endpoint probe block.
- **`QSDM/config/qsdmplus.toml.example` + `qsdmplus.yaml.example`**
  now ship `[node]` (two-tier role gate) and `[trust]` (attestation
  transparency) sections with inline commentary, so fresh installs get
  the correct scaffolding without needing to cross-reference the
  Major Update spec.

### Added

- **Trust aggregator wired into node startup.** `cmd/qsdmplus/main.go`
  now constructs a live `TrustAggregator` fed by a
  `ValidatorSetPeerProvider` (wrapping `ActiveValidators()`) and a
  `MonitoringLocalSource` (NGC ring buffer), and runs a background
  refresh goroutine at `cfg.TrustRefreshInterval` (default 10 s). The
  `/api/v1/trust/attestations/*` endpoints now return real data on a
  live node instead of perpetually answering 503 "warming up". New
  `[trust]` config section (TOML/YAML) and env knobs
  `QSDM_TRUST_DISABLED`, `QSDM_TRUST_FRESH_WITHIN`,
  `QSDM_TRUST_REFRESH_INTERVAL`, `QSDM_TRUST_REGION` (legacy
  `QSDMPLUS_*` still accepted). Setting `disabled=true` makes the
  endpoints return HTTP 404 per §8.5.3. (`pkg/api/trust_peer_provider.go`,
  `pkg/config/config.go`, `pkg/config/config_toml.go`,
  `cmd/qsdmplus/main.go`, audit item `rebrand-07`.)
- **Major Update Phase 5 — trust transparency surface.** New public
  endpoints `GET /api/v1/trust/attestations/summary` and
  `GET /api/v1/trust/attestations/recent` expose aggregate NGC
  attestation counts as an opt-in, per-operator *transparency signal*
  (not a consensus rule). Widgets render "X of Y" — never just "X" —
  per the anti-claim guardrail in Major Update §8.5.2.
  New: `/trust.html` transparency page on `qsdm.tech`, matching card on
  the operator dashboard. (`pkg/api/handlers_trust.go`,
  `deploy/landing/trust.html`, `internal/dashboard/static/dashboard.js`.)
- **Major Update Phase 1–4 deliverables landed in-repo.** Rebrand
  (`QSDM+ → QSDM`, native coin `Cell (CELL)`, `dust` smallest unit),
  two-tier node roles (validator / miner) with role-guard startup
  enforcement, emission schedule calculator, reference CPU miner
  (`cmd/qsdmminer`), split Dockerfiles (`Dockerfile.validator`,
  `Dockerfile.miner`), mining protocol spec (Candidate C: mesh3D-tied
  useful PoW). Full phase-by-phase table in
  [`NEXT_STEPS.md`](NEXT_STEPS.md).
- **`cmd/trustcheck`** — single-binary, stdlib-only external scraper
  that validates `/api/v1/trust/attestations/*` responses against the
  §8.5.2–§8.5.4 contracts. Third parties can run it on any cadence
  without pulling in the QSDM module.
- **`cmd/genesis-ceremony`** — pure-Go dry-run of the mainnet genesis
  ceremony. N-of-N commit-reveal with ed25519 signing (spec-level
  stand-in for ML-DSA-87), `run`/`verify`/`schema` modes. Every
  artefact flagged `dry_run: true`; the verifier refuses to bless a
  non-dry-run bundle.
- **`docs/docs/AUDIT_PACKET_MINING.md`** — external-auditor entry point
  for `mining-01`: threat model, 10 numbered consensus-safety
  invariants, invariant → source-location → test coverage matrix,
  reproducible-build recipe.
- **`.github/workflows/qsdm-split-profile.yml`** — CI workflow proving
  both the `validator_only` and full-profile builds compile and pass
  short tests on every push, plus clean Docker builds of
  `Dockerfile.validator` and `Dockerfile.miner` (no push).
- **`pkg/audit/checklist.go`** — 18 new audit items across the
  `rebrand`, `tokenomics`, `mining_audit`, and `trust_api` categories
  so each in-repo commitment has a single auditable identifier.

### Changed

- **OpenAPI spec refresh.** `QSDM/docs/docs/openapi.yaml` title,
  contact, and env-var references now lead with the `QSDM` name, with
  the legacy `QSDMPLUS_*` names documented as aliases during the
  deprecation window.
- **`NVIDIA_LOCK_CONSENSUS_SCOPE.md` refresh.** Aligned to the Major
  Update §5.4 Stance 1 ("NVIDIA-favored, not NVIDIA-exclusive") and
  cross-references the new trust endpoints. The one-sentence
  invariant — "NVIDIA-lock is an opt-in, per-operator API policy —
  not a consensus rule" — is preserved byte-for-byte because
  `pkg/api/handlers_trust.go` emits it verbatim.
- **`start-qsdm-local.ps1`.** Artefact now named `qsdm_local.exe`,
  with an automatic move of the legacy `qsdmplus_local.exe` path so
  operators' PID files and launch scripts keep working.

### Deprecated (migration window, one minor release)

- `QSDMPLUS_*` environment variables. Continue to work; log a single
  deprecation warning per process. Prefer `QSDM_*`. See
  `REBRAND_NOTES.md` §3.1.
- `X-QSDMPLUS-*` HTTP headers. Continue to be accepted. Prefer
  `X-QSDM-*`. See `REBRAND_NOTES.md` §3.2.
- `qsdmplus_*` Prometheus metric prefix. **Not yet renamed** — a
  cutover plan for dual-emit is staged under `REBRAND_NOTES.md` §3.7
  because the rename is breaking for Grafana/alert pipelines.
- `sdk/javascript/qsdmplus.*`, `sdk/go/qsdmplus*.go`. Shim packages
  that re-export from the new `qsdm` module path.

### Not changed

- Go module path stays `github.com/blackbeardONE/QSDM`.
- Address format, signature scheme (ML-DSA-87 via liboqs), GossipSub
  topics, and the `/api/v1/*` REST surface are unchanged.
- Existing databases, config files, and systemd units keep working
  without any renaming.
- PoE+BFT consensus is CPU-only and admits validators without a GPU.

### Audit-blocked / wall-clock items (from `NEXT_STEPS.md`)

These are **not** code changes — they are listed here so a release
reader can find them with one search.

- `rebrand-03` — trademark filings.
- `tok-01` — tokenomics genesis-policy sign-off.
- `mining-01` — external audit of the mining protocol (auditor packet
  ready at `docs/docs/AUDIT_PACKET_MINING.md`).
- `mining-05` — incentivized testnet launch.
- Mainnet genesis ceremony (dry-run driver ready at
  `cmd/genesis-ceremony`).
- NGC attestation service availability.
