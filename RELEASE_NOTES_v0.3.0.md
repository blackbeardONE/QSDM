# QSDM v0.3.0 — Release Notes

> **Scope.** This document captures the state of the repository at the v0.3.0 cut. It is the tracked, source-of-truth counterpart to the operator-local `NEXT_STEPS.md` (which is `.gitignore`d by design). The release is in-repo complete; the items in *Remaining external blockers* gate any actual public publish or mainnet announcement.

## At a glance

| | Value |
|---|---|
| Target tag (Go core) | `v0.3.0` — **pushed and published** (`https://github.com/blackbeardONE/QSDM/releases/tag/v0.3.0`) |
| Target tag (JavaScript SDK) | `sdk-js-v0.3.0` — held local pending `NPM_TOKEN` |
| Verified at git HEAD | `de2bf30` (session 73 baseline) → `c00fccd` (session 78, green release run) |
| Green CI run | `release-container.yml` run `25650171771` (10 / 10 jobs success) |
| Container images | `ghcr.io/blackbeardone/{qsdm,qsdm-validator,qsdm-miner}:0.3.0` (cosign-signed + SPDX SBOM attested) |
| Release assets | 66 files (20 binaries + 22 `.sig` + 22 `.pem` + source SBOM + `SHA256SUMS`) |
| Go toolchain | `1.25.10` (declared in `go.mod`; auto-fetched by every runner from the local bootstrap toolchain) |
| `golang.org/x/net` | `v0.53.0` |
| Audit-checklist size | 53 items in `pkg/audit/checklist.go` (full-suite render: 81 items including review-driven extras) |
| `govulncheck` reachable findings | 1 (`GO-2024-3218`, tracked) |
| Non-`-short` test pass rate | 67 / 67 packages |
| JS SDK test pass rate | 17 / 17 cases |
| npm tarball | `qsdm-0.3.0.tgz`, 6 files / 6.3 kB packed / 17.6 kB unpacked |

## Headline changes since the previous release line

### v0.3.0 surface (sessions 70–74)

- **Quantum-secure crypto path**: ML-DSA-87 (NIST FIPS 204) is the production signature scheme. The pure-Go fallback is gated by `QSDM_NO_CGO=1`; the CGO path uses liboqs and is the default on Linux/macOS/Windows once `QSDM/liboqs_install` exists.
- **Two-tier node model**: validator (CPU-only, PoE + BFT) and miner (Mesh3D-tied PoW) split into separate Docker images, K8s manifests, and binary `cmd/qsdm` + `cmd/qsdmminer`.
- **Cell tokenomics live**: emission schedule, 4-year halvings, 10-second target block time, treasury allocation. Exposed via `/api/v1/status`, the SDK, and the dashboard tokenomics panel.
- **Mining sub-protocol shipped**: `MINING_PROTOCOL.md`, `pkg/mining`, `cmd/qsdmminer`, reference CPU miner solves proofs that the verifier accepts. CUDA fat-binary kernel covers `sm_50` through `sm_90`.
- **Trust & attestation page**: `/api/v1/trust/attestations/*`, dashboard widget, anti-claim landing widget.
- **JavaScript SDK at parity with the Go SDK**: 17 `node:test` cases, full method coverage, ESM/CJS exports, `prepublishOnly` test gate, Sigstore provenance.
- **Soak harnesses validated at length** (this session): mempool 10 min / 19.1 M txs / 31.9 K tx/sec; pubsub 10 min / 4 hosts / 239 987 publishes / per-host receive spread of 6 messages over 600 s.
- **Supply-chain hardening**: source SBOM (SPDX 2.3) and image SBOMs via `anchore/sbom-action`; cosign keyless signing of every binary, every container image, and `SHA256SUMS`; OIDC-driven, no operator key custody.
- **CVE remediation (session 73)**: Go directive `1.25.9 → 1.25.10`, `golang.org/x/net` `v0.52.0 → v0.53.0` closed three reachable CVEs (`GO-2026-4976`, `GO-2026-4971`, `GO-2026-4918`). One unpatched finding (`GO-2024-3218`) is tracked as audit entry `supply-08` with a written mitigation rationale.

### Session 74 (verification cut)

This is a verification-only session — no code changes, only confirmation that the prior session is reproducible and release-ready.

| Step | Result |
|---|---|
| `git status --porcelain` at HEAD `de2bf30` | clean (0 files) |
| `go test ./... -count=1 -timeout 900s` (full, non-`-short`) | 67 / 67 packages OK |
| `go vet ./...` | clean |
| `go vet -tags soak ./tests/...` | clean |
| `go mod verify` | all modules verified |
| `govulncheck ./...` | 1 finding (`GO-2024-3218`, tracked as `supply-08`) |
| `node --test sdk/javascript/qsdm.test.js` | 17 / 17 pass in 11.34 s |
| `npm pack --dry-run` (sdk/javascript/) | `qsdm-0.3.0.tgz`, 6 files, 6.3 kB packed (LICENSE + CHANGELOG.md present) |
| 8 release binaries `-trimpath -ldflags="-s -w"` build | all clean; `--version` banner stamps `go1.25.10` |
| **10-min pubsub soak** (4 hosts × 2 producers × 50 Hz × 256 B) | **PASS in 601.99 s**: 239 987 publishes; 719 946 cross-host receipts; per-host receive totals `[179 985 / 179 987 / 179 984 / 179 990]` (total spread = 6 messages across 4 hosts over 600 s); no partition; no sustained-error window; flat rate throughout |

## Sessions 75–80: release pipeline shakedown + third-party verification

The first push of `v0.3.0` on `9c6bdde` (session 74) created an empty release skeleton with 0 assets — the workflow failed at four distinct latent bugs that had never run in production because no prior tag had exercised this path end-to-end. Four follow-up fix commits and one verification session resolved them:

| Session | Commit | Layer fixed | Root cause |
|---|---|---|---|
| 75 | `134abf1` | `qsdm-go.yml` red-since-session-72 | `ubuntu-latest` runners ship no `ripgrep`; the two rebrand-guardrail scripts (`check-no-new-legacy-metrics.sh`, `check-no-collapsed-env-preferred.sh`) call `rg` and exited 2. Fix: `apt-get install ripgrep` in the build-test job. |
| 76 | `d8326c6` | `gh release upload` duplicate-glob | `qsdmminer-*` and `*.sig` both matched `qsdmminer-*.sig`; gh tried to upload the same name twice and the API rejected the second attempt (`HTTP 422 ReleaseAsset.name already exists`). `--clobber` only handles the *existing* release, not earlier duplicates inside the same call. Fix: collapse to a single `./*` glob (the asset dir contains only release artefacts at that point). |
| 77 | `83c1128` | **The actual root cause of every SBOM failure** | GHCR is case-sensitive. `docker/metadata-action@v5` auto-lowercases the namespace per OCI convention so `docker/build-push-action` pushes to `ghcr.io/blackbeardone/<image>`. Every downstream step that constructed an image reference from `${{ github.repository_owner }}` raw used the mixed-case `blackbeardONE` and got "manifest unknown". Sessions 75 and 76 chased the symptoms (`registry-username` / `registry-password`, `docker pull`) — none of those addressed the actual case mismatch. Fix: a single `imgbase` step per image job that computes the lowercased ref once and feeds it to `docker/metadata-action`, the `docker pull` step, the `anchore/sbom-action`, and the `cosign attest` step. |
| 78 | `c00fccd` | bash `for tag in <newlines>` | `${{ steps.meta.outputs.tags }}` is newline-separated. The bash construct `for tag in ${{ … }}; do …` produces a multi-line `for` header that bash rejects (`syntax error near unexpected token 'ghcr.io/.../...:0.3'`). This bug had always been present but was shielded by earlier failing steps in sessions 75–77. Fix: pass `TAGS` through the env block and pipe `printf '%s\n' "$TAGS" | while read`. |

`release-container.yml` run `25650171771` on `c00fccd` finally produced a 10-of-10 green release pipeline:

| Job | Result |
|---|---|
| `binaries (linux/amd64)` | success |
| `binaries (linux/arm64)` | success |
| `binaries (darwin/amd64)` | success |
| `binaries (darwin/arm64)` | success |
| `binaries (windows/amd64)` | success |
| `source SBOM (SPDX)` | success |
| `ghcr qsdm (legacy image name)` | success — image + signature + SBOM attestation |
| `ghcr qsdm-validator (CPU-only)` | success — image + signature + SBOM attestation |
| `ghcr qsdm-miner (GPU miner runtime)` | success — image + signature + SBOM attestation |
| `attach binaries + SBOM + signatures to release` | success — 66 assets uploaded |

Plus two GitHub-side cleanups via the REST API: deleted two orphaned **draft** releases (ids `320195790` and `320254619`) that survived earlier tag deletions because GitHub keeps the release object as an orphan draft when its tag is removed.

### Session 79 — independent third-party verification

After publishing, an independent verifier on a Windows 11 / Go 1.24.2 / cosign v2.4.1 host (no Docker installed) downloaded the live release and reproduced the supply-chain claims end-to-end. Full procedure and expected output: [`QSDM/docs/docs/V030_POST_RELEASE_VERIFICATION.md`](QSDM/docs/docs/V030_POST_RELEASE_VERIFICATION.md). Headline:

| Verification | Result |
|---|---|
| `qsdmminer-windows-amd64.exe --version` (native) | `qsdmminer v0.3.0 (c00fccd, 2026-05-11T04:23:54Z, go1.25.10, windows/amd64)` |
| SHA256 cross-match of 6 representative downloads against `SHA256SUMS` | 6 / 6 byte-exact |
| `cosign verify-blob` against 7 keyless-signed blobs (5 binaries + `SHA256SUMS` + source SBOM) | 7 / 7 `Verified OK` |
| `cosign verify` against 3 GHCR images (anonymous, `DOCKER_CONFIG=empty`) | 3 / 3 success — every certificate pins `githubWorkflowSha=c00fccd93a66c5317aaaa03b80e9a09d111e87bd`, `githubWorkflowRef=refs/tags/v0.3.0` |
| `cosign verify-attestation --type spdxjson` against 3 image SBOMs | 3 / 3 success |
| Sigstore identity bound by every signing certificate | `https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0.3.0` (issuer: `https://token.actions.githubusercontent.com`) |
| `qsdm:0.3.0` image manifest digest | `sha256:3f46260eef8a702c2e45631824cab8f59f2f792bb2efcb952d0de514509dad1e` |

### Session 80 — post-release maintenance pass

After v0.3.0 shipped, two repo-state issues remained from the long debug period:

- **`macos-build.yml` queue was clogged.** 11 runs (sessions 73 onward, plus 6 dependabot PR runs) had been sitting in `queued` for up to 14 hours because the hosted macOS pool can only execute ~2 runners at a time and `concurrency.cancel-in-progress` only cancels runs in the *same* `head_ref` group. Cancelled 10 stale entries via `POST /actions/runs/{id}/cancel`, kept the latest v0.3.0 push run.
- **Hidden no-CGO bug in `build_macos.sh`.** Once the queue cleared and a fresh `macos-build` run on `ae88fdc` finally got a macOS runner, the no-CGO job failed immediately:
  ```
  go: cannot find main module, but found .git/config in /Users/runner/work/QSDM/QSDM
  ```
  Root cause: the no-CGO branch of `build_macos.sh` (`QSDM_NO_CGO=1`) ran `go build ./cmd/qsdm` from the QSDM repo root, but `go.mod` lives at `QSDM/source/go.mod`. The CGO branch (lines 98–107) already handled the `source/` indirection via an `if [[ -f source/go.mod ]]; then cd source` guard; the no-CGO branch (line 34) did not. This bug had been latent forever — the macOS workflow had never actually finished a run end-to-end because the runner queue was always backlogged. Fix: mirror the same `source/`-detection guard into the no-CGO branch.
- **Hidden smoke-check hang in `macos-build.yml`.** After the no-CGO build succeeded, the next step ran `./qsdm --version || ./qsdm version || true`, but `cmd/qsdm` is the **validator** binary — it does not implement `--version`; unknown flags are ignored and the binary launches a full validator node (DHT bootstrap, libp2p relay, pubsub, dashboard server). The smoke check therefore never returned, and the job sat consuming a runner until `timeout-minutes: 15` cancelled it. Fix: wrap each invocation in `timeout 5`. The exit code is irrelevant (the `|| true` chain absorbs any failure); we only need the binary's process to release the runner.
- **Hidden CGO `universal2` cross-compile bug in `rebuild_liboqs_macos.sh`.** With the no-CGO fix in place, the CGO macos-14 job exposed the next latent bug:
  ```
  error: unknown target CPU 'armv8-a+crypto'
  note: valid target CPU values are: nocona, core2, ... x86-64-v4
  ```
  Root cause: the script defaulted to `QSDM_LIBOQS_ARCH=universal2`, which sets `CMAKE_OSX_ARCHITECTURES="arm64;x86_64"`. liboqs's cmake auto-detection runs once and picks `-march=armv8-a+crypto` from the arm64 slice's feature probe, then applies it globally — including to the x86_64 slice's compile, which rejects `armv8-a+crypto` as an unknown target CPU. CI builds each arch separately in a matrix anyway, so universal2 is wasted work. Fix: default `QSDM_LIBOQS_ARCH` to `$(uname -m)` (arm64 on macos-14, x86_64 on macos-13). Operators distributing a single fat dylib can still opt in with `QSDM_LIBOQS_ARCH=universal2`.
- **Dependabot triage.** 10 open PRs (most pre-dating the session 75 `ripgrep` fix in `qsdm-go.yml`, so their `build-test` runs failed for an unrelated reason). Merged the two clean pure-Go bumps with full green CI:
  - `#11`: `github.com/libp2p/go-libp2p-pubsub` `0.15.0` → `0.16.0`
  - `#12`: `github.com/mattn/go-sqlite3` `1.14.28` → `1.14.44` (merged after rebase onto `ae88fdc`)
  Posted `@dependabot rebase` on the remaining six (`#1`, `#5`, `#6`, `#7`, `#8`, `#10`) so they pick up the ripgrep fix on their next CI run.
- **Deferred to next release cycle.** Two PRs are green at the qsdm-go layer but their PR CI does *not* exercise `release-container.yml` (which runs only on `push: tags: v*`). Merging them risks re-breaking the release pipeline we just stabilised in sessions 75–78:
  - `#2`: `docker/login-action` `3` → `4`
  - `#13`: `docker/build-push-action` `6` → `7`
  Path forward: cut a `v0.3.1-rc1` tag against a temporary branch carrying both bumps, watch `release-container.yml` complete end-to-end (especially the cosign attest + SBOM upload steps), then merge if all 10 jobs are green.

### Session 81 — npm publish attempt + package rename

Pushed tag `sdk-js-v0.3.0` against commit `c00fccd9`, supplied an `NPM_TOKEN`
with 2FA bypass, and re-ran `sdk-javascript-publish.yml`. The workflow ran
all tests, packed the tarball, signed the build with the GitHub-Actions OIDC
identity, and published the provenance attestation to Sigstore Rekor at
**logIndex `1506312160`** — and then the registry rejected the actual
`PUT /qsdm` with:

```
403 Forbidden — Package name too similar to existing packages
qs, esm, jsdom, tsm, tsd, tsdx; try renaming your package to
'@anachronoa/qsdm' and publishing with 'npm publish --access=public' instead.
```

This is npm's typo-squatting heuristic applied to new package names; it is
not appealable through CI. Two paths forward: (a) scoped name
`@<scope>/qsdm`, (b) unscoped name with a suffix. Chose **(b) `qsdm-sdk`**:
matches the `aws-sdk` / `stripe-sdk` convention, preserves the QSDM brand,
and avoids tying the public package id to any individual's npm username.

Rename touched only user-visible surface:

- `QSDM/source/sdk/javascript/package.json`: `"name": "qsdm"` → `"qsdm-sdk"`.
- `QSDM/source/sdk/javascript/README.md`: install line and `require()` example.
- `QSDM/source/sdk/javascript/qsdm.js`: JSDoc snippet.
- `QSDM/source/sdk/javascript/CHANGELOG.md`: explicit rename entry under 0.3.0.
- `.github/workflows/sdk-javascript-publish.yml`: header comment + job name.

Nothing else changes: the GitHub repo is still `blackbeardONE/QSDM`, the
binaries are still `qsdm` / `qsdmminer-gui` / `qsdmminer` / `trustcheck` /
`genesis-ceremony`, the import-time class is still `QSDMClient`, the
on-chain brand and the GHCR images are still `qsdm:0.3.0`. Only the npm
package id picks up the `-sdk` suffix.

The failed `sdk-js-v0.3.0` attempt's provenance is permanently archived on
Rekor (`logIndex=1506312160`) — that record links the GitHub Actions run
that produced it to the `qsdm-0.3.0.tgz` tarball SHA, even though npm never
accepted the upload. After the rename publish succeeds, the registry copy
will carry a fresh provenance entry for the `qsdm-sdk` name.

### Session 82 — self-custody wallet (CLI + browser, byte-compatible)

Closed the largest remaining product hole at v0.3.0: there was no
operator-facing way to obtain a QSDM address whose private key the
operator actually controlled. The existing `POST /api/v1/wallet/create`
handler generates an ML-DSA-87 keypair and *discards* the private key
when the request scope exits — fine as a write-only mining sink, useless
for self-custody.

**Shipped in this session:**

- **`pkg/keystore`** (new package): canonical JSON-on-disk keystore
  format. PBKDF2-HMAC-SHA-256 (600 000 iterations, OWASP 2023) → AES-256-GCM
  (12-byte nonce, 16-byte tag). `Validate` enforces algorithm + version +
  KDF-floor + `sha256(public_key) == address` cross-check. 13 unit tests
  cover round-trip, wrong-passphrase, schema-shape, tamper detection,
  weak-KDF rejection, and the empty-passphrase refusal.
- **`qsdmcli wallet new|show|inspect|sign`** (new subcommand): builds a
  fresh ML-DSA-87 keypair locally, encrypts the private key under a
  passphrase (prompted with `golang.org/x/term`, no echo, or supplied
  via `--passphrase-file`), writes the keystore as mode-0600. `new`
  prints **only** the address to stdout so it pipes straight into
  `qsdmminer --address=$(qsdmcli wallet new …)`. `inspect` decrypts
  and verifies the decrypted private key produces the stored public key
  (round-trip integrity check). `sign` produces a 4627-byte ML-DSA-87
  signature over an arbitrary message.
- **Browser wallet at `https://qsdm.tech/wallet/`** (new page +
  WASM module):
  - `wasm_modules/wallet/cmd/qsdm-wallet/main.go` — Go→WebAssembly entry
    point, ~3.1 MB. Exposes `qsdm_wallet_generate / sign / verify /
    address_from_public_key / version` to JavaScript via `js.FuncOf`.
  - `deploy/landing/wallet.html` — 3-tab UI (Generate / Open / Sign),
    matching the existing landing-page design language and nav.
  - `deploy/landing/wallet.js` — WebCrypto envelope (PBKDF2-SHA-256 →
    AES-256-GCM) with byte-identical parameters to `pkg/keystore`. The
    keystore JSON produced in the browser is interchangeable with the
    one produced by the CLI; an offline test (`_tmp_xcompat.js`) reads a
    CLI-generated keystore via Node's WebCrypto and signs through the
    WASM successfully.
  - `scripts/build_wallet_wasm.sh` — operator script: compiles WASM,
    copies `wasm_exec.js` from the local Go toolchain, drops both into
    `deploy/landing/`.
- **`pkg/wasm_modules/wallet/walletcrypto/crypto.go`** rewritten as a
  thin wrapper over `cloudflare/circl/sign/mldsa/mldsa87` (no liboqs,
  no CGO) so the same code compiles for CGO + non-CGO + WASM. The
  previous build-tag stubs (`crypto_stub.go` and the CGO-side stub) are
  deleted; both returned `wallet crypto: use pkg/crypto/dilithium.go
  instead` and broke the WASM `init()` path.
- **Documentation**: `docs/docs/WEB_WALLET.md` (threat model, keystore
  schema, deployer checklist, practical recipes) and an updated
  `MINER_QUICKSTART.md §1a` ("Generate a reward address") that points
  at both the CLI and the browser path.
- **Landing-site nav update**: `deploy/landing/index.html` gains a
  *Wallet* link in the primary nav so visitors reach `/wallet.html`
  from the home page.

**Test status:**

```
ok  github.com/blackbeardONE/QSDM/pkg/keystore           13.6 s   13 cases
ok  github.com/blackbeardONE/QSDM/cmd/qsdmcli             2.0 s   includes wallet build
ok  github.com/blackbeardONE/QSDM/wasm_modules/wallet     0.8 s   sign+verify round-trip
ok  github.com/blackbeardONE/QSDM/.../walletcore          0.2 s   was skipping before this change
```

Plus offline:

- `_tmp_wasm_smoke.js` (Node) — instantiates `wallet.wasm`, runs
  generate / sign / verify / verify-reject / address-derive. All pass.
- `_tmp_xcompat.js` (Node) — reads a CLI-generated keystore, decrypts
  via Node's `crypto.webcrypto.subtle` with the keystore's parameters,
  signs the recovered private key via WASM, verifies against the
  keystore's `public_key`. All pass.

Both Node scripts are temp/`.gitignored` and rebuildable from the
patterns above; the in-repo Go tests are the binding contract.

### Session 83 — wallet live on qsdm.tech + CSP fix + Go deploy tool

The Session 82 artefacts existed in-repo but were not yet on the public
edge. This session pushed them and fixed the one CSP gap that would
have blocked the browser wallet from running even after the static
files landed.

**Live deploy (qsdm.tech, BLR1 validator, 206.189.132.232):**

```
sha256(/var/www/qsdm/wallet.html)   = f57e6e58…ff9c9   (18,804 B)
sha256(/var/www/qsdm/wallet.js)     = 41e9247c…79c6dea (18,371 B)
sha256(/var/www/qsdm/wallet.wasm)   = 928bea8f…229676  (3,237,388 B)
sha256(/var/www/qsdm/wasm_exec.js)  = 0c949f49…acba14  (16,992 B)
sha256(/var/www/qsdm/index.html)    = 6e1a3eb4…001a328 (85,044 B, adds /wallet.html nav link)
```

A full backup of the prior `/var/www/qsdm` was tarred to
`/root/landing-backups/landing-20260511T182420Z.tgz` (307 MB; includes
historical release directories) and the prior Caddyfile to
`/root/landing-backups/Caddyfile-20260511T182420Z.bak`. Rollback is a
single `tar xzf` + `caddy restart`.

**Caddyfile Content-Security-Policy gap fixed.** The previous policy
was `script-src 'self' 'unsafe-inline'`, which under CSP Level 3 is
sufficient for inline `<script>` tags but **not** for
`WebAssembly.instantiate()` — the browser would have failed the WASM
load with *"Refused to compile or instantiate WebAssembly module
because 'wasm-unsafe-eval' is not an allowed source of script"*. Added
the minimal delta `'wasm-unsafe-eval'`. This is strictly narrower than
`'unsafe-eval'`: it allows `WebAssembly.{instantiate,compile}` but
*not* `eval()`, `Function()`, or `setTimeout(string, …)`. The rest of
the CSP (style-src, img-src, connect-src to api/dashboard subdomains
only, `frame-ancestors 'none'`) is unchanged.

Caddy's admin API is intentionally disabled in our config (`admin off`
in the global block, since we don't expose it on a listening port and
have no operational use for hot-reload short of a graceful restart),
so `caddy reload` returned `connect: connection refused` on
`localhost:2019`. Used `systemctl restart caddy` instead. The restart
was clean: all three listeners (`:443` apex, `:8443` API,
`:8081` dashboard) came back in under one second.

**Public-edge verification (Node `webassembly.instantiate` via curl
→ Go runtime shim):**

```
VERSION  : "qsdm-wallet v1 / ml-dsa-87 / circl"
PUB hex  : 5184 chars  (= 2592 B, ML-DSA-87 spec)
PRIV hex : 9792 chars  (= 4896 B, ML-DSA-87 spec)
ADDR     : 605ab7550bd6c74ce3e5b394c1f6334cea0f6e2951938ae7fc5c775a7e1ac7e2
SIG hex  : 9254 chars  (= 4627 B, ML-DSA-87 spec)
VERIFY   : true
TAMPER   : false    (1-byte message edit → signature rejected)
```

Source for the smoke-test was a Node script that `curl`s
`https://qsdm.tech/wallet.wasm` + `wasm_exec.js`, instantiates them in
a fresh `Go` runtime, and round-trips sign+verify. Temp, not
committed; the binding contract is the in-tree `pkg/keystore` and
`wasm_modules/wallet/*` Go tests.

**New operator tool: `cmd/qsdm-deploy-landing`.** A small Go binary
that takes `-file LOCAL=REMOTE` mappings and `-run "shell …"` steps,
dials the VPS over SSH with the local `~/.ssh/id_ed25519`, uploads
each file by piping through `cat > <remote>`, and runs each remote
command with live stdout/stderr. Replaces the historical pattern of
`python QSDM/deploy/remote_*_paramiko.py` for the landing-site case —
this workstation's `pip` is broken (MSYS2 MinGW Python 3.12 ships
without `ensurepip` functional), so the Python path was unavailable
without a Python detour. The Go path needs only `go build` from the
existing toolchain. The tool is general (host/user via flag or
`QSDM_VPS_HOST` / `QSDM_VPS_USER` env vars; key via `-key`); it is not
landing-specific by design.

**Endpoint health after deploy:**

| URL | HTTP | Notes |
|---|---|---|
| `https://qsdm.tech/` | 200 | index.html with new `/wallet.html` nav link |
| `https://qsdm.tech/wallet.html` | 200 | text/html; 18,804 B |
| `https://qsdm.tech/wallet.wasm` | 200 | application/wasm; 3,237,388 B |
| `https://qsdm.tech/wasm_exec.js` | 200 | text/javascript; 16,992 B |
| `https://qsdm.tech/wallet.js` | 200 | text/javascript; 18,371 B |
| `https://api.qsdm.tech/api/v1/health` | 200 | validator JSON API alive |
| `https://dashboard.qsdm.tech/` | 302 | dashboard redirect to login (unchanged behaviour) |

**What did NOT change:**

- No new server-side endpoint, no validator schema change, no
  consensus change. The wallet emits a `qsdm…` address derived as
  `hex(sha256(public_key))` — identical to the validator-side
  `pkg/wallet.NewWalletService` derivation. A wallet generated by
  either flow is immediately usable as a `--address` flag on
  `qsdmminer` and as the `to`/`from` field on `/api/v1/wallet/send`.

**Roadmap items deliberately deferred:**

- A "send transaction" tab on the browser wallet (depends on v2 mining
  envelope format stabilising; planned for v0.4.0).
- Mnemonic / BIP-39-style seed phrase. ML-DSA-87 keys do not have a
  deterministic short representation; the encrypted JSON keystore is
  the recovery artefact. Documented as such in `WEB_WALLET.md §6`.

### Session 84 — homepage rewrite + secondary-page navigation parity

After deploying the wallet in Session 83 the public landing was
out-of-date: no mention of the wallet beyond a single nav link, the
SDK install snippet still showed the pre-rename `qsdm` package, no
visible release version, and the four secondary pages (`chain.html`,
`validators.html`, `trust.html`, `download.html`) had no link to
`/wallet.html` — a visitor who deep-linked into Trust or Validators
could not discover the wallet without going back to `/`.

**`index.html` rewrite.** Cut from 1,479 lines / 85,044 B to
845 lines / ~44,000 B without losing any current information.
Restructured around three top-level sections: **Use** (cards for
Wallet / Mine / Validate, with one-click links into the deployed
flows), **Build** (developer-facing — `npm install qsdm-sdk`,
`go get`, WASM, REST API, `docker pull ghcr.io/blackbeardone/qsdm`),
and **Why** (the existing benefits, condensed and de-duplicated).
Added a version pill in the nav showing the current release tag
(`v0.3.1`). The pill fetches `/api/v1/status` on load and shows
whatever is reported; an inline filter rejects strings matching
`/^go\d+(\.\d+){1,2}$/` so the validator's accidental publication of
its Go toolchain version (`go1.25.9`, currently in the field) cannot
overwrite the release tag with a misleading value. Architecture SVG
updated to include the wallet box on the user-facing side.

**Secondary page navigation parity.** Audited `chain.html`,
`validators.html`, `trust.html`, `download.html` and added a `Wallet`
link to each. Expanded `trust.html`'s nav (previously just a
"← Back to landing" link) to a full Home / Wallet / Validators /
Chain / Download set so deep-link traffic from search engines or
Sigstore-Rekor links has the same discovery surface as the homepage.

**Deploy:** all five HTML files pushed via `cmd/qsdm-deploy-landing`
with a pre-run tar backup of `/var/www/qsdm` to
`/root/landing-backups/landing-pre-s84.tgz`. No Caddyfile change. No
validator-side change.

### Session 85 — wallet SRI hardening + read-only balance lookup

Two narrow, additive improvements to the deployed wallet at
`https://qsdm.tech/wallet.html`. No consensus change, no server-side
change, no Caddyfile change. Public edge re-verified end-to-end after
the deploy.

**1) Subresource Integrity (SRI) is now enforced on every loadable
sub-resource the wallet page consumes.** This closes the deferred item
from *Session 83*. Three sha384 hashes are pinned:

| File | Pinned in | Mechanism |
|------|-----------|-----------|
| `/wasm_exec.js` | `wallet.html` | `<script integrity="sha384-…" crossorigin="anonymous">` |
| `/wallet.js`    | `wallet.html` | `<script integrity="sha384-…" crossorigin="anonymous">` |
| `/wallet.wasm`  | `wallet.js`   | `fetch('/wallet.wasm', { integrity: 'sha384-…' })` |

If any of these bytes differ from the pinned hash, the browser refuses
the load and surfaces a visible error rather than executing the rogue
code path. `wallet.html` is at the root of the trust chain — it is
itself fetched fresh on every page load — so its integrity is bounded
by HTTPS + the operator's control of `/var/www/qsdm/wallet.html`. SRI
extends that root-of-trust to the three sub-resources, which is the
class of attack SRI was designed for (CDN swap, cached-asset poisoning,
operator-error overwrite of one file but not the HTML).

`QSDM/scripts/build_wallet_wasm.sh` now rotates all three hashes
automatically (`openssl dgst -sha384 -binary | openssl base64 -A`) in
dependency order — wasm_exec.js → wallet.wasm → wallet.js → wallet.html
— and `--refresh-sri-only` is a new flag that re-pins the hashes from
on-disk artefacts without re-running the `GOOS=js GOARCH=wasm go build`
(useful for HTML/JS-only edits). The script `grep`-asserts every
substitution actually took effect so a future template error can't ship
a stale-hash wallet.

End-to-end public-edge verification after deploy:

```
on-the-wire sha384 vs pinned (Caddy → curl → SHA-384 → compare to
attribute literal):

  /wasm_exec.js   PWCs+V4B…  ⇄  pinned in wallet.html  →  MATCH
  /wallet.js      7QOp7prD…  ⇄  pinned in wallet.html  →  MATCH
  /wallet.wasm    yHrwzrXe…  ⇄  pinned in wallet.js    →  MATCH
```

**2) New "Check balance" tab on the wallet.** A fourth tab alongside
Generate / Open / Sign. The user types or pastes any QSDM address
(64 hex chars), the page sends a single
`GET https://api.qsdm.tech/api/v1/wallet/balance?address=<addr>` (the
endpoint is public — `pkg/api/middleware.go` exempts it from auth so
game servers and explorers can poll), and the balance is rendered as
`X.YYYYYYYY CELL` plus the raw JSON for operators who want to see
exactly what the validator returned.

The Generate and Open tabs now also feed the address they just
produced into a "Use my last address" shortcut on the Balance pane —
mostly a UX nicety so a freshly-minted wallet can be checked in one
click. AbortController bounds the fetch at 12 s so a slow validator
doesn't leave the UI spinning forever. The endpoint's response shape
(`{ "address": "<echo>", "balance": <float CELL> }`) is sanity-checked
against the requested address so a MITM that rewrites the JSON in
flight can't silently substitute a different account's balance — the
UI surfaces a clear "address mismatch" error in that case.

The page copy now explicitly differentiates the network behaviour of
each tab: Generate / Open / Sign remain pure-browser (no POST, no GET
of anything beyond the three static files); Balance is the one tab
that contacts the network, and only after an explicit button click.
That precision is important — the original page promised "no network",
which would have become subtly false the moment Balance shipped.

**Deployed files (sha384, byte size):**

```
/var/www/qsdm/wallet.html   sha384-EHFEu4ZH…  21,572 B   (+2,768 B vs s83)
/var/www/qsdm/wallet.js     sha384-7QOp7prD…  25,780 B   (+7,409 B vs s83)
/var/www/qsdm/wallet.wasm   sha384-yHrwzrXe…   3,237,388 B   (unchanged)
/var/www/qsdm/wasm_exec.js  sha384-PWCs+V4B…  16,992 B   (unchanged)
```

Backup of the prior pair at `/root/backups/wallet.{html,js}.bak-20260512-031728`
on BLR1. Roll-back is `cp …bak-… /var/www/qsdm/<file>` + chown.

**Deploy tool fix (`cmd/qsdm-deploy-landing`).** No code change this
session — but discovered that PowerShell on Windows expands `$(date …)`
locally before sending the shell command to the remote, so a literal
`$(date …)` in a `-pre-run` flag fails with "Cannot bind parameter
'Date'". Documented in operator commentary; the fix on the caller side
is to compute the timestamp via `(Get-Date).ToString(…)` in PowerShell
and pass it as a plain string. The deploy tool itself is OS-agnostic.

**Verified working end-to-end:**

```
$ curl -s https://qsdm.tech/wallet.html | grep -c 'data-tab="balance"'   → 1
$ curl -s https://qsdm.tech/wallet.js   | grep -c 'BALANCE_ENDPOINT'    → 1
$ curl -s 'https://api.qsdm.tech/api/v1/wallet/balance?address=605ab7…' →
  {"address":"605ab7…","balance":0}      # public, no auth header
```

### Session 86 — full v1 deprecation: status posture, miner preflight, release matrix, doc rewrite

User instruction: *"do it all by yourself. make sure v1 is no longer
an option for everybody."* Closes the audit gap surfaced in session
85, where the live mainnet has been v2-only at consensus
(`FORK_V2_HEIGHT = 0` since the Phase-4 chain reset) but several
user-facing surfaces still suggested v1 was a viable path. Six
coordinated changes — all consensus-neutral, all additive.

**1) `/api/v1/status` now self-advertises the v2 posture.** New
`mining` block (`pkg/api/handlers_status.go`):

```json
"mining": {
  "protocol_versions_accepted": [2],
  "fork_v2_height":              0,
  "fork_v2_active":             true,
  "fork_v2_tc_height":           <varies>,
  "fork_v2_tc_active":          false,
  "attestation_types_required": ["nvidia-cc-v1","nvidia-hmac-v1"],
  "min_enroll_stake_dust":      1000000000
}
```

The booleans fold `(scheduled? & reached?)` into a single field so
clients don't have to reason about the `math.MaxUint64` sentinel
that means "fork not yet scheduled". The height fields are
`omitempty` so a v1-only validator emits a clean minimal payload.
Implementation reads `mining.ForkV2Height()` / `ForkV2TCHeight()`
atomically and computes activeness against the current chain tip
— same logic the verifier uses for proof admission, so the
posture the endpoint reports is the posture the verifier enforces.

**2) Both miners refuse to start v1 against a v2-active validator
(`pkg/mining/preflight`).** A new helper package fetches
`/api/v1/status`, parses the `mining` block, and returns one of
`DecisionProceedV{1,2}` / `DecisionRefuseV1`. Both `cmd/qsdmminer`
and `cmd/qsdmminer-console` call this immediately after flag parse
and *before* entering the mining loop:

- v2-active validator + v1 caller → refuse + exit 3 with a banner
  pointing operators at `qsdmminer-console --protocol=v2` and the
  MINER_QUICKSTART.md.
- v1 validator + v1 caller → proceed; banner explains.
- Probe failure (network, parse error, older validator without the
  `mining` block) → fail-OPEN with a warning so a degraded
  `/api/v1/status` doesn't lock out local devnet usage.
- `--allow-v1` (CLI flag) / `allow_v1 = true` (miner.toml) bypasses
  the refusal for forensic / replay use, with a loud "all submitted
  proofs WILL be rejected" warning printed to stderr.

The probe shape was deliberately implemented without a dependency
on `pkg/api.StatusResponse` to avoid an import cycle and to make
the miner brittle-resistant to future status-schema growth.
Comprehensive unit coverage (`preflight_test.go`, 9 cases) exercises
all 8 (validator state × caller posture × probe success) cells of
the decision table.

**3) `cmd/qsdmminer` is no longer a public release artefact.**
`.github/workflows/release-container.yml` no longer cross-compiles
or cosign-signs the v1 reference miner; the artefact list in the
header comment and the asset-glob in the aggregator job are both
trimmed to `qsdmminer-console-*`, `trustcheck-*`,
`genesis-ceremony-*`. Two reasons documented in the workflow:
(a) every v1 proof against mainnet is rejected at consensus, so a
shipped binary would mis-route operators into a guaranteed-reject
loop; (b) reproducibility — the binary stays in-tree so any
auditor can `go build ./cmd/qsdmminer` from a tagged commit and
verify it byte-for-byte against the SBOM. `qsdm-split-profile.yml`
keeps the `qsdmminer --self-test` CI gate alive as a canary on the
v1 ComputeMixDigest code path (the verifier still ingests historical
v1 blocks if any chain ever produces them), but drops `qsdmminer`
from the `--version` ldflags-injection smoke (no release binary →
nothing to stamp).

**4) `QSDM/docs/docs/MINER_QUICKSTART.md` rewritten v2-first.** The
top of the document used to lead with "install qsdmminer →
self-test → connect to validator". That sequence is wrong for any
2026-mainnet operator: they need an enrolled NVIDIA GPU and a
bonded 10 CELL stake before any miner binary will produce
accepted work. The rewrite reorders the doc into a v2-mainnet flow
(`§1 Requirements → §2 Reward address → §3 HMAC key + on-chain
enrollment → §4 Mine → §5 Lifecycle commands`), demotes the
original §2 / §3 (CPU install + validator-discovery + systemd
unit) to **Appendix A. v1 audit / local-devnet builds** with an
explicit "Mainnet operators: this section is not for you" header,
and adds the §1a "self-detect the validator's posture" callout
that documents how `/api/v1/status.mining` is the canonical source
of truth.

**5) Homepage Mine card + wallet Balance-tab help.** The
`index.html#mine` article now reads "v2 only" with the explicit
"v1 CPU path is rejected at consensus (`ReasonBadVersion`) and the
v1 reference binary is no longer a public release artefact"
disclaimer, a Hardware / Tooling / On-chain / Funding-caveat
bullet list, and a link to the new Appendix B. The wallet's
"Check balance" tab help text now points operators at
`qsdmcli enroll` + `qsdmminer-console --protocol=v2` and surfaces
the funding caveat. SHA-384 SRI of `wallet.js` was rotated by
`build_wallet_wasm.sh --refresh-sri-only`; `wallet.wasm` is
unchanged.

**6) New "Appendix B. Enrollment-funding status" in
MINER_QUICKSTART.md — an honest audit.** The chain reset at
FORK_V2_HEIGHT=0 zeroed total supply, so a fresh outside operator
who follows the v2 enrollment flow hits an `insufficient_balance`
rejection from the admission gate at the 10 CELL stake step. The
appendix walks the four funding routes:

- *Initial-operator allocation* — none on the live chain as of
  v0.3.2. The single-operator genesis allocation went to the
  validator-operator's own miner address.
- *Reward from your own v2 proofs* — circular: requires enrollment,
  which requires CELL.
- *Peer transfer* — possible via `/api/v1/transactions`; the
  browser wallet's *Send transaction* tab is a deferred v0.4 item.
- *Public bootstrap faucet* — **not yet shipped**. The string
  `faucet` does not occur anywhere in `QSDM/source/` (verified by
  `grep -ri faucet QSDM/`).

The appendix also documents the broken `/api/v1/wallet/mint`
endpoint: it is publicly callable, returns HTTP 200 with
`status:"minted"`, and **does not credit the recipient's balance**
(a `GET /api/v1/wallet/balance?address=<recipient>` after a
successful POST returns the recipient's pre-mint balance unchanged).
The endpoint is documented in `pkg/api/middleware.go publicPaths`
as "Public for game server to mint $CELL" — a stub for an external
authoritative service that was never wired up. Treat as no-op.

This appendix is the deliverable for the previously-open audit
item *enroll-funding*. The practical answer for a fresh outside
operator today is "social-bootstrap (ask an existing holder for
10 CELL) or run a local devnet"; the faucet build-out is now the
project's highest-priority operator-funding work item.

**Files touched (Session 86):**

```
QSDM/source/pkg/api/handlers_status.go     — +MiningInfo + buildMiningInfo
QSDM/source/pkg/mining/preflight/          — new package (preflight.go + tests)
QSDM/source/cmd/qsdmminer/main.go          — preflight gate + banner rewrite
QSDM/source/cmd/qsdmminer-console/main.go  — preflight gate + AllowV1 config
.github/workflows/release-container.yml    — drop qsdmminer from release matrix
.github/workflows/qsdm-split-profile.yml   — drop qsdmminer from --version smoke
QSDM/docs/docs/MINER_QUICKSTART.md         — v2-first rewrite + Appendix A + B
QSDM/deploy/landing/index.html             — Mine card → v2 only
QSDM/deploy/landing/wallet.html            — CLI snippet → v2 flow
QSDM/deploy/landing/wallet.js              — Balance-tab help → v2 flow
RELEASE_NOTES_v0.3.0.md                    — this entry
```

**Consensus / wire compatibility.** The new `/api/v1/status.mining`
block is additive (older SDK callers that don't know about it just
ignore the new field). The preflight refusal is a CLIENT-side
behaviour: no change to admission rules, no change to verifier, no
change to block format. The release-matrix drop is a release-time
packaging change only. Every test in the targeted sweep passes:

```
ok  github.com/blackbeardONE/QSDM/pkg/api                           1.483s
ok  github.com/blackbeardONE/QSDM/pkg/mining/preflight              0.317s
ok  github.com/blackbeardONE/QSDM/cmd/qsdmminer-console             2.636s
+ 17 other pkg/mining/* packages, all green
```

## What's safe to publish today (post-publish status)

These artefacts are sign-off-ready and can be shipped the moment the corresponding external blocker clears:

- ✅ **GHCR container images** (`qsdm`, `qsdm-validator`, `qsdm-miner` `:0.3.0`). **Published.** `release-container.yml` keyless-signs them via Sigstore OIDC and attaches an SPDX 2.3 SBOM as a cosign attestation. Reproducible with `cosign verify` (see `V030_POST_RELEASE_VERIFICATION.md` §"Step 5").
- ✅ **Linux / Windows / macOS binaries** (`qsdmminer`, `qsdmminer-console`, `trustcheck`, `genesis-ceremony` × 5 platforms = 20 binaries) with cosign signatures and a source SBOM. **Published.** Reproducible with `cosign verify-blob` (see `V030_POST_RELEASE_VERIFICATION.md` §"Step 4").
- ⏳ **`qsdm-sdk@0.3.0` on npm.** Re-push tag `sdk-js-v0.3.0` (moved to the post-rename commit); the `.github/workflows/sdk-javascript-publish.yml` workflow validates that the tag suffix matches `package.json`, re-runs the test suite as a `prepublishOnly` gate, and runs `npm publish --provenance --access public`. External blocker: `NPM_TOKEN` repo secret with 2FA-bypass (the previous attempt under the bare name `qsdm` was rejected by the registry's typo-squatting heuristic — see *Session 81*; the package was renamed `qsdm-sdk` to satisfy that check while preserving the QSDM brand).

## Remaining external blockers

These are the items the repo cannot close itself. They are tracked individually in `pkg/audit/checklist.go` (visible via `cmd/auditreport`) and at the top of `NEXT_STEPS.md` (operator-local).

| ID | Blocker | Owner | What unlocks |
|---|---|---|---|
| `rebrand-03` | Trademark filings for "QSDM" and "Cell (CELL)" | Counsel | Paid advertising; legally safe public launch. |
| `tok-01` | Tokenomics genesis policy sign-off (100 M cap, 10 M treasury, 90 M mining, 4-year halvings) | Counsel + foundation | Mainnet genesis ceremony. |
| `mining-01` | External audit of `MINING_PROTOCOL.md` + `pkg/mining` | Independent cryptography / consensus auditor | CUDA miner public release. Auditor entry-point: `QSDM/docs/docs/AUDIT_PACKET_MINING.md`. |
| `mining-05` | Incentivised testnet launch | Ops + marketing | Real-world stress of the reference miner before mainnet emission begins. |
| `supply-08` | Upstream fix for `GO-2024-3218` (libp2p-kad-dht) | go-libp2p maintainers | Removes the only accepted-with-mitigation entry. Practical exposure already bounded by bootstrap allowlist + peer scoring. |
| — | `NPM_TOKEN` repo secret (2FA-bypass) | Ops | npm publish of `qsdm-sdk@0.3.0` (renamed from `qsdm` after the registry's name-similarity heuristic rejected the bare name; see *Session 81*). |
| — | `APPLE_DEVELOPER_ID_APPLICATION` + `APPLE_NOTARYTOOL_KEYCHAIN_PROFILE` | Ops with Apple Developer account | Notarised macOS binaries. Scaffold: `QSDM/scripts/notarize_macos.sh`. |
| — | NVIDIA hardware + `nvcc` toolchain | Ops | Production Mesh3D PoW. Kernel and Makefile already in tree at `pkg/mesh3d/kernels/`. |
| — | Mainnet genesis ceremony | Foundation + validator set | After `tok-01` and `mining-01` clear. Dry-run driver at `cmd/genesis-ceremony` flags every artefact `dry_run: true`. |

## How to reproduce this report

```powershell
pwsh QSDM/scripts/release_evidence.ps1
```

…or the bash twin (`QSDM/scripts/release_evidence.sh`). Output goes to `_tmp_release_evidence_<UTC>/` and contains the full set of artefacts described in [`QSDM/docs/docs/RELEASE_EVIDENCE.md`](QSDM/docs/docs/RELEASE_EVIDENCE.md). Hand the directory to an auditor; every step is hash-pinned in `00_MANIFEST.txt`.

## Annotated-tag templates

The two tag annotations below are pre-drafted so the operator can copy them verbatim once external blockers clear.

### `v0.3.0` (Go core)

```
QSDM v0.3.0

In-repo release. Verified at HEAD de2bf30 (session 73), re-confirmed
in session 74:
  * go test ./... -count=1 (non-short) -> 67/67 packages OK
  * govulncheck ./...                   -> 1 finding (GO-2024-3218,
                                           tracked as supply-08)
  * go mod verify                       -> all modules verified
  * 10-min pubsub soak (4 hosts)        -> 239,987 publishes,
                                           per-host receive spread
                                           = 6 msgs across 600 s
  * 10-min mempool soak (8 producers)   -> 19.1 M txs at 31.9 K tx/s

External blockers tracked in pkg/audit/checklist.go (rebrand-03,
tok-01, mining-01, mining-05, supply-08).
```

### `sdk-js-v0.3.0` (JavaScript SDK)

```
qsdm-sdk@0.3.0 (JavaScript SDK) -- published 2026-05-11

Feature parity with sdk/go. 17/17 node:test cases pass. Tarball:
6 files, 6.7 kB packed, 18.7 kB unpacked (manifest:
package.json + qsdm.js + qsdm.d.ts + README.md + CHANGELOG.md +
LICENSE). Sigstore provenance attached at publish time
(Rekor logIndex 1506353451, SLSA v1 predicate).

Registry:  https://www.npmjs.com/package/qsdm-sdk/v/0.3.0
Tarball:   https://registry.npmjs.org/qsdm-sdk/-/qsdm-sdk-0.3.0.tgz
shasum:    c4e53da187d25bbb2fd4a15c477c12ec7a0c62c1
SLSA URL:  https://registry.npmjs.org/-/npm/v1/attestations/qsdm-sdk@0.3.0

The bare name `qsdm` was rejected by npm's typo-squatting
heuristic on first attempt; the package was renamed to
`qsdm-sdk` (see Session 81). The repo, GHCR images, binaries,
on-chain brand, and the import-time QSDMClient symbol all
keep the original QSDM naming. The Rekor record for the
rejected first attempt is preserved at logIndex 1506312160.
```
