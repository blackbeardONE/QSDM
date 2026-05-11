# Release Evidence Bundles

> One reproducible command produces a self-contained directory the next reviewer (operator, auditor, foundation member, or future-you) can read end-to-end without re-running the toolchain.

Released artefacts only mean something if a third party can verify them. The release-evidence bundle exists so that "we ran the tests, they passed" becomes "here is the hash-pinned manifest of the exact commands, outputs, and binaries — re-run any of them and you must get the same result, byte-for-byte".

## tl;dr — generate a bundle

Windows (PowerShell 7+):

```powershell
pwsh QSDM/scripts/release_evidence.ps1
```

Linux / macOS:

```bash
bash QSDM/scripts/release_evidence.sh
```

Both scripts emit to `_tmp_release_evidence_<UTC-timestamp>/` at the repo root by default. That prefix is already matched by the `_tmp_*` rule in `.gitignore` (added in session 73), so the bundle stays out of commits unless you deliberately move or rename it.

Useful flags (identical semantics across the two scripts):

| PowerShell flag | Bash flag | Effect |
|---|---|---|
| `-OutDir <path>` | `--out-dir <path>` | Write the bundle somewhere other than `_tmp_release_evidence_*`. Use this when handing the bundle off to an auditor. |
| `-Quick` | `--quick` | Skip `govulncheck` and the full non-`-short` `go test ./...`. The remaining steps still produce a useful integrity snapshot in ~30 s. |

A full run on a developer laptop (Windows, no CGO, single-threaded `go test`) takes 10–15 minutes — dominated by `go test ./... -count=1` (~30 s) and the 11 `cmd/*` clean builds. CI-class hardware brings it under 5 minutes.

## What's inside a bundle

Every bundle contains the same eleven files. Each artefact has a self-describing header (capture time, host, command) and a footer with the captured exit code, so an auditor can see *both* the output and whether the step itself passed.

| File | What it captures | Why an auditor reads it |
|---|---|---|
| `00_MANIFEST.txt` | sha256 + size of every other file in the bundle; git HEAD; host fingerprint. | First file to open. Lets the reviewer detect tampering: re-hash any artefact and compare. |
| `01_environment.txt` | OS, host, shell, `go version`, git HEAD/branch/origin, working-tree dirty flag, `node` + `npm` versions. | Establishes "this is the machine that produced the bundle". A clean working tree (`dirty=0`) is required for a real release. |
| `02_audit_report.md` | `cmd/auditreport -format markdown` rendered against `pkg/audit/checklist.go`. | The 81-item security checklist. Auditor flips each critical/high item to `passed` / `failed` / `waived` by feeding a reviewed JSON back through `cmd/auditreport -input`. |
| `03_go_mod_verify.txt` | `go mod verify`. | Cryptographic proof that every module in `go.sum` matches the bytes pulled from the proxy. Anything other than `all modules verified` on the last line is a hard stop. |
| `04_govulncheck.txt` | `govulncheck ./...` reachable-call-graph CVE scan. | Should report at most one finding: `GO-2024-3218` (libp2p-kad-dht), tracked as `supply-08` in the audit checklist. Anything else is a new exposure. The allowlist machinery lives in `QSDM/scripts/govulncheck-filter.sh`. |
| `05_go_vet.txt` | `go vet ./...` and `go vet -tags soak ./tests/...`. | Catches a class of bugs the compiler misses (printf format mismatches, struct-tag typos, unsafe pointer ops). Both exit codes must be 0. |
| `06_go_test_full.txt` | `go test ./... -count=1 -timeout 900s` (non-`-short`). | Every test, no skips. Tail must show `ok` for every package and no `FAIL`. |
| `07_jssdk_tests.txt` | `node --test sdk/javascript/qsdm.test.js`. | 17 cases must all pass — they cover every public SDK method, both auth headers, base-URL trimming, timeout abort, and every `ApiError` path. |
| `08_npm_pack.txt` | `npm pack --dry-run` from `sdk/javascript/`. | The auditor sees the exact tarball manifest that would land on `npmjs.com`: 6 files, ~6.3 kB packed, `LICENSE` + `CHANGELOG.md` present. Anything missing means the SDK is *not* publish-ready. |
| `09_binaries.txt` | For every `cmd/*`: clean build with `-trimpath -ldflags="-s -w"`, then sha256, size, and the first line of `--version`. | Reviewer verifies that the binaries an operator would install all stamp the expected Go toolchain version (currently `go1.25.10`). The sha256 lets the operator independently re-build and compare. |
| `10_soak_summary.txt` | Tail of any `_tmp_soak_*` logs in the repo root. | Latest mempool soak (10 min, 19.1 M txs, 31.9 K tx/sec — session 73) and pubsub soak (10 min, 4 hosts, 239 987 publishes, per-host receipts within 6 over 600 s — session 74) summaries. If absent, the file embeds the exact one-liners to reproduce them. |

## How a reviewer should use a bundle

The bundle is designed to be read top-to-bottom in numeric order:

1. **`00_MANIFEST.txt`** — note the git HEAD. If it doesn't match the release tag you were asked to review, stop and ask the operator which commit is canonical.
2. **`01_environment.txt`** — confirm `dirty=0`. A dirty working tree means the operator's local edits are baked into the binaries.
3. **`02_audit_report.md`** — this is the *real* review surface. Open the markdown, walk the categories (`api`, `authentication`, `authorisation`, `bridge`, `cryptography`, ...), and for each `critical` / `high` item that you accept as satisfied, write the corresponding `{id, status, reviewer, notes}` entry into a `reviewed.json`. Feed it back:
   ```
   go run ./cmd/auditreport -input reviewed.json -gate=true
   ```
   `-gate=true` exits with code 2 if *any* `critical` or `high` item is still `pending` or `failed`. That command is the green-light signal for mainnet.
4. **`03`–`05`** — three machine checks that should be boring. The interesting case is when one fails.
5. **`06`** — search for `--- FAIL` and `FAIL\b`. There must be none.
6. **`07`–`08`** — JS SDK gate. The npm tarball manifest must match what the `sdk-javascript-publish.yml` workflow would publish.
7. **`09`** — pick two binaries at random, re-build them locally with the same `-trimpath -ldflags`, and verify the sha256s match. (Reproducibility check.)
8. **`10`** — confirm the soaks ran at full length (≥ 10 minutes each) and the headline numbers are within ~1% of the targets.

## What is *not* in the bundle (and why)

These are real release gates but cannot be captured by a script:

- **External-auditor sign-off** on the 81-item checklist — only a human can flip items from `pending` to `passed` / `waived`.
- **Apple notarisation** of macOS binaries — requires Apple Developer ID secrets (`APPLE_DEVELOPER_ID_APPLICATION`, `APPLE_NOTARYTOOL_KEYCHAIN_PROFILE`). The scaffold is `QSDM/scripts/notarize_macos.sh`.
- **`NPM_TOKEN`-driven publish** of `qsdm-sdk@0.3.0` — *now published* at <https://www.npmjs.com/package/qsdm-sdk/v/0.3.0> (renamed from the bare `qsdm` after npm's typo-squatting heuristic rejected the original name; the registry copy carries an SLSA v1 provenance attestation tied to the GitHub Actions run via Sigstore Rekor logIndex `1506353451`).
- **Real-GPU CUDA validation** — the bundle proves the kernel builds and the CPU path passes; an NVIDIA card running the fat binary built by `pkg/mesh3d/kernels/Makefile` is a separate test rig.
- **Counsel sign-off** on `rebrand-03` (trademark) and `tok-01` (tokenomics) — out of scope for any code artefact.

These remaining items are the actual "what's next" after a green bundle. They are tracked as the **Wall-clock-blocked items** table at the top of `NEXT_STEPS.md` (operator-local) and as audit-checklist entries `rebrand-03`, `tok-01`, `mining-01`, `mining-05`, and `supply-08` in `pkg/audit/checklist.go`.
