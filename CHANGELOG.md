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
