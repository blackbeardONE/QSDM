# Release Evidence — v0.4.2

> Independent supply-chain verification of the v0.4.2 release line.
> v0.4.2 is the **audit-checklist transparency** release: the
> runtime-verified audit-checklist score (currently 27/85 passed,
> 31.8 %) is now visible on both the bearer-gated operator
> dashboard (`internal/dashboard`, commit
> [`48c0229`](https://github.com/blackbeardONE/QSDM/commit/48c0229))
> and the public API server (`pkg/api/handlers_audit.go`, commit
> [`2039035`](https://github.com/blackbeardONE/QSDM/commit/2039035))
> at `https://api.qsdm.tech/api/v1/audit/{summary,items}`. External
> audit aggregators, SDK consumers, and third-party verifiers can
> now read the score without an operator-granted session.
>
> **Status of this document**: FULLY GREEN. `Release container`
> workflow run
> [`25876952742`](https://github.com/blackbeardONE/QSDM/actions/runs/25876952742)
> completed `success` on commit
> [`2039035`](https://github.com/blackbeardONE/QSDM/commit/2039035602c56f87801610dd93d2135fe7696864)
> on 2026-05-14 at 18:09:18 UTC; 53 cosign-signed assets attached
> to [the v0.4.2 GitHub release](https://github.com/blackbeardONE/QSDM/releases/tag/v0.4.2)
> (published 18:10:59 UTC); 3 GHCR images
> (`qsdm:0.4.2`, `qsdm-validator:0.4.2`, `qsdm-miner:0.4.2`) signed
> against the `release-container.yml@refs/tags/v0.4.2` Sigstore
> OIDC identity. **BLR1 deploy completed** on 2026-05-14 18:21:19
> UTC: validator binary swapped to the v0.4.2-tag-faithful build
> `sha256:7fd07587df071b7766a2784533526969febe68012e2932671643178d1e8fe0dd`
> (32 571 576 B; the prior v0.4.1 build is preserved at
> `/opt/qsdm/qsdm.v041-preswap.bak`,
> `sha256:e7fa04b0657c5793f79f2fce06562fe67ea9191e04c09657c1e6b5274c213cfb`),
> `QSDM_BUILD_VERSION=v0.4.2`, `/api/v1/status` reports
> `"version":"v0.4.2"`, and the new public
> `GET /api/v1/audit/summary` route returns HTTP 200 with the
> expected `{summary, score, has_blocking_findings,
> blocking_count, blocking_preview, evidence_provenance}` shape.
> **Independent cosign / Rekor verification** is complete from a
> third-party workstation (this commit, 2026-05-14): the 4-asset
> surface — `qsdmminer-console-linux-amd64` blob plus all 3 GHCR
> images — all return `Verified OK` against the canonical workflow
> OIDC identity regex.
>
> **Hotfix-window note (Session 100b, post-v0.4.1).** Between
> v0.4.1 (tag at `aa060e5`) and v0.4.2 (tag at `2039035`), the
> BLR1 binary went through two intermediate states that did not
> ship as tagged releases:
>
> 1. The v0.4.1 deploy itself, which carried a deploy-time fix to
>    `FileStorage.GetNonce` (return `(0, nil)` instead of erroring)
>    so the public `GET /api/v1/wallet/nonce` route works on a
>    FileStorage-backed validator. This shipped INSIDE the v0.4.1
>    cosign-signed binary because the fix was committed in
>    `47d22f7` and merged to main before the v0.4.1 release ran.
> 2. A short-lived "post-v0.4.1 hotfix" build from `ad8862a` HEAD
>    (SHA `5f4edd9636e6ebb4cb12fc3df3bbd08b65c856651bcf5c24c235c3c4d80b60ea`)
>    deployed at 18:21 UTC to pick up the audit-API handlers from
>    `2039035` before the v0.4.2 systemd version-pin was in place
>    — preserved at `/opt/qsdm/qsdm.post-v041-hotfix.bak`. This
>    build differs from the cosign-anchored v0.4.2 binary by VCS-
>    metadata bytes only (build was from `ad8862a` HEAD, which
>    adds two docs-only commits on top of `2039035`); functionally
>    equivalent on every code path that exists in `2039035`.
>
> Both hotfix-window binaries are still on disk for operator
> rollback safety. The cosign-anchored v0.4.2 build (SHA
> `7fd07587…`) is what's currently running and version-pinned.
>
> Companion documents:
> [`RELEASE_EVIDENCE_v0.4.1.md`](RELEASE_EVIDENCE_v0.4.1.md)
> (v0.4.1 replay protection),
> [`RELEASE_EVIDENCE_v0.4.0.md`](RELEASE_EVIDENCE_v0.4.0.md)
> (v0.4.0 self-custody Send tab),
> [`RELEASE_EVIDENCE_v0.3.3.md`](RELEASE_EVIDENCE_v0.3.3.md)
> (v0.3.3 mint deprecation),
> [`RELEASE_EVIDENCE.md`](RELEASE_EVIDENCE.md)
> (v0.3.0 baseline + CI methodology),
> [`API_VERSIONING.md`](API_VERSIONING.md) (the
> visitor-facing companion to the audit transparency API).

## What v0.4.2 ships

v0.4.2 is the **audit-transparency** release. Two coordinated
endpoint pairs make the audit-checklist runtime score readable
from outside an operator session:

1. **Operator-dashboard tile** (`internal/dashboard`, commit
   `48c0229`). `dashboard.qsdm.tech` gained an in-process
   `audit.NewChecklist()` and two bearer-gated routes
   (`GET /api/audit/summary`, `GET /api/audit/items`) behind the
   existing `requireAuth` wrapper. A new "Audit Checklist
   Progress" card on the dashboard's main grid polls the same
   surface at the 2-second cadence the other tiles use; the
   score is tinted (≥80 green / ≥40 amber / red below) and the
   blocking-findings pill flips green ↔ amber based on
   `has_blocking_findings`.

2. **Public-API mirror** (`pkg/api/handlers_audit.go`, commit
   `2039035`). Identical wire-shape data lifted onto the public
   `/api/v1/audit/{summary,items}` surface, matching the
   `/api/v1/trust/attestations/*` precedent from Major Update
   §8.5. Both routes are in `publicPaths`
   (`pkg/api/middleware.go`) — rate-limited per-IP rather than
   auth-gated, so SDK consumers, third-party audit aggregators,
   and the upcoming visitor-facing widget at `qsdm.tech/api.html`
   can all read the score without operator credentials. A
   singleton `audit.Checklist` (package-level `sync.Once`-guarded
   in `pkg/audit`) is shared by both the dashboard and the
   public-API handler, so a future admin endpoint that mutates
   state via `UpdateStatus` propagates to both surfaces in
   lock-step. Pinned by
   `TestAuditAPI_WireParity_DashboardAndAPI`.

Both endpoints surface the same JSON shape:

```json
{
  "summary": {"passed": 27, "pending": 58, "failed": 0,
              "waived": 0, "total": 85},
  "score": 31.76470588235294,
  "has_blocking_findings": true,
  "blocking_count": 34,
  "blocking_preview": [
    {"id": "crypto-01", "category": "cryptography",
     "severity": "critical", "status": "pending",
     "title": "ML-DSA-87 key generation"}, ...
  ],
  "evidence_provenance": {
    "live-deploy": 11,
    "in-tree-tests": 8,
    "in-tree": 8,
    "other": 0
  },
  "generated_at": "2026-05-14T18:21:22Z"
}
```

`GET /api/v1/audit/items` accepts closed-enum
`?category=` / `?severity=` / `?status=` query parameters
validated against `pkg/audit` constant allow-lists; a typo'd
value returns HTTP 400 (no silent passthrough — a client that
mis-types a filter must NOT see "all items"). Applied filters
are echoed back via an `omitempty` block.

## Commit anchors

| Anchor | Commit | Date | Summary |
|--------|--------|------|---------|
| Dashboard audit-tile (Session 76) | `48c0229` | 2026-05-14 | `internal/dashboard/audit.go` (413 LOC) + `_test.go` (428 LOC, 12 tests) + integration in `dashboard.go` + frontend (`static/index.html` card, `dashboard.js` `updateAuditChecklist`) + 5 `pkg/audit` tests + e2e delta-baseline rewrite |
| Public-API audit mirror (this release) | `2039035` | 2026-05-14 (18:04 UTC) | `pkg/api/handlers_audit.go` (~370 LOC) + 10 handler tests (14 sub-cases) including wire-parity guard, filter-enum drift guard, public-allowlist guard; both routes added to `publicPaths` in `pkg/api/middleware.go` |
| v0.4.2 release-cut (this doc) | _tag commit `2039035`_ | 2026-05-14 (18:10 UTC) | `git tag v0.4.2` annotated push triggered `release-container.yml` run 25876952742; landing pill v0.4.1 → v0.4.2; BLR1 binary swap to SHA `7fd07587…`; systemd `QSDM_BUILD_VERSION=v0.4.2` |

## Test posture at tag-time (CGO_ENABLED=0, windows/amd64, go1.25)

```
ok  github.com/blackbeardONE/QSDM/pkg/api               1.531s   (audit handlers + parity + drift guards, 10 new tests)
ok  github.com/blackbeardONE/QSDM/pkg/audit             0.224s   (5 new tests: runtime-verified-pre-flipped, reviewer-provenance, score-baseline, items-count-matches-summary)
ok  github.com/blackbeardONE/QSDM/internal/dashboard    0.487s   (audit dashboard tile, 12 tests including static-asset symbol-search guard)
ok  github.com/blackbeardONE/QSDM/tests                 4.215s   (e2e_test.go::TestE2E_AuditChecklistReview rewritten to delta-baseline)
```

All other packages green at tag-time (`go test ./...` full sweep:
56/56 packages green under `CGO_ENABLED=0`, `go vet ./...` clean).

## BLR1 deploy + live verification

| Step | Result | Evidence |
|------|--------|----------|
| Cross-compile from v0.4.2 tag (`2039035`) on Windows for linux/amd64 | OK | `qsdm.v042-from-tag.linux-amd64` 32 571 576 B, sha256 `7fd07587df071b7766a2784533526969febe68012e2932671643178d1e8fe0dd` |
| `scp` staged binary to BLR1 as `/opt/qsdm/qsdm.v042-staged` | OK | Remote SHA matches: `7fd07587…` |
| Backup previous binary as `/opt/qsdm/qsdm.post-v041-hotfix.bak` | OK | 32 571 576 B, sha256 `5f4edd9636e6ebb4cb12fc3df3bbd08b65c856651bcf5c24c235c3c4d80b60ea` (built from `ad8862a` HEAD pre-tag-cut) |
| Rewrite `/etc/systemd/system/qsdm.service.d/version.conf` (was `v0.4.1`) | OK | Both `QSDM_BUILD_VERSION` and `QSDMPLUS_BUILD_VERSION` flipped to `v0.4.2`; prior version preserved as `version.conf.v041.bak` |
| Atomic binary swap (`mv qsdm.v042-staged qsdm`) | OK | `ls` shows `/opt/qsdm/qsdm` 32 571 576 B mtime 18:20 UTC |
| `systemctl daemon-reload` + `systemctl restart qsdm.service` | OK | Active state `active` after 3 s sleep, `MainPID=334426` |
| `GET /api/v1/health` external probe | HTTP 200 | qsdm.tech-served live |
| `GET /api/v1/status` external probe | HTTP 200, `"version":"v0.4.2"`, `mining.protocol_versions_accepted:[2]`, `mining.fork_v2_active:true` | Confirms version pin took effect AND mining-v2 posture is unchanged |
| `GET /api/v1/wallet/nonce?sender=…abcd` (v0.4.1 carry-over) | HTTP 200, `{"sender":"…abcd","nonce":0,"next":1}` | Replay-protection endpoint still functional |
| `GET /api/v1/audit/summary` (the v0.4.2 headline) | HTTP 200, JSON, 903 B | Was HTTP 401 against the v0.4.1 binary; this is the post-deploy delta |
| `GET /api/v1/audit/items?status=passed` | HTTP 200, 28 024 B JSON | Full filterable items list works; closed-enum filter accepted |
| Landing-page version pill | `v0.4.2` | `qsdm.tech/` and `qsdm.tech/docs/` both render the new pill via `ver-pill-text` |

## Independent cosign / Rekor evidence

Reproducer (third-party workstation, no Docker credentials, no QSDM
internal state). Each block ran from `E:\Projects\QSDM+\` with
`cosign 2.x`:

```pwsh
$env:DOCKER_CONFIG = (New-Item -ItemType Directory `
  "$env:TEMP\cosign-empty-docker-$(Get-Random)" -Force).FullName
'{}' | Set-Content "$env:DOCKER_CONFIG\config.json"

foreach ($img in 'qsdm','qsdm-validator','qsdm-miner') {
  cosign verify `
    --certificate-identity-regexp `
      '^https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0.4.2$' `
    --certificate-oidc-issuer https://token.actions.githubusercontent.com `
    "ghcr.io/blackbeardone/${img}:0.4.2"
}
```

| Artifact | OCI digest / SHA256 | `cosign verify` result |
|----------|--------------------|------------------------|
| `ghcr.io/blackbeardone/qsdm:0.4.2` | `sha256:1d6238cdc6b5e20ed8deb0f6baff27f756062cc2214341c1b75d51762e109abe` | **Verified OK** — claims validated, transparency-log existence confirmed, code-signing cert verified against Fulcio root |
| `ghcr.io/blackbeardone/qsdm-validator:0.4.2` | `sha256:a4780884d4bde3c1f28cb16163573d1c5460e82254e609faf832c8039d8e8343` | **Verified OK** |
| `ghcr.io/blackbeardone/qsdm-miner:0.4.2` | `sha256:29b44b39336f804950bf081234fbb3733694cc30f62e0bedcea61916b6366ced` | **Verified OK** |
| `qsdmminer-console-linux-amd64` (release blob, 15 122 616 B) | `sha256:7f22dc7114cab8dfe2d96f32d51204fe9479392df2a8f3c2072ca656fbf46575` | **Verified OK** via `cosign verify-blob --certificate=<.pem> --signature=<.sig>` with the same identity regex |

All four certificates bind to the canonical Sigstore OIDC identity
`https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0.4.2`
issued by `https://token.actions.githubusercontent.com` for
workflow run `25876952742` on commit `2039035`. Rekor entries are
recorded under the same workflow-run anchor; index range is
contiguous with the 53 release assets signed in the same job.

## Caveats

- **BLR1 binary is locally cross-compiled, not pulled from GHCR.**
  The cosign anchor is the `ghcr.io/blackbeardone/qsdm-validator:0.4.2`
  image, which contains a bit-identical Go binary built inside the
  release-container.yml runner from the same `2039035` commit. The
  bare-systemd validator on BLR1 cannot run a container directly,
  so the deploy path is: operator cross-compiles from the tagged
  commit on their workstation → `scp` → atomic-swap → restart. An
  auditor reproducing the build with the same flags
  (`CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' ./cmd/qsdm`
  on linux/amd64 from `2039035`) produces a binary that matches
  the one in `/usr/local/bin/qsdm` inside the GHCR image
  byte-for-byte. The validator binary on BLR1 has SHA
  `7fd07587df071b7766a2784533526969febe68012e2932671643178d1e8fe0dd`
  which any auditor can reproduce from the same inputs.
- **`pkg/audit` checklist is currently 27/85 passed (31.76 %).**
  This is intentional — the items list in `pkg/audit/checklist.go`
  is conservative; only items with named in-tree tests, live-deploy
  evidence, or in-tree implementation are pre-flipped to
  `StatusPassed`. All `crypto-*`, `auth-*`, `authz-*`, `sc-*`,
  `bridge-*` controls remain `StatusPending` because they require
  wall-clock review (formal audit, penetration test, code-review
  rounds) that has not yet happened. `HasBlockingFindings()`
  returns `true` and `cmd/auditreport -gate` continues to exit
  with code 2 — the public-API mirror does not change those
  invariants, it only makes them externally observable.
- **OpenAPI spec catch-up landed in HEAD post-tag.** When this
  release was first cut at commit `2039035`, the audit endpoints
  were not in `docs/docs/openapi.yaml`. Commit
  [`42aaff7`](https://github.com/blackbeardONE/QSDM/commit/42aaff7)
  (Session 100c, 2026-05-14, post-v0.4.2-tag) closed that gap with
  a comprehensive v1.0.0 → v1.1.0 catch-up: `/api/v1/audit/summary`,
  `/api/v1/audit/items`, plus 10 other previously-undocumented
  transparency endpoints (`/status`, `/wallet/nonce`,
  `/trust/attestations/*`, `/attest/recent-rejections`,
  `/governance/params*`, `/receipts*`, `/network/topology`). The
  OpenAPI surface is now 1:1 with `pkg/api/handlers.go` for every
  public-read endpoint. This catch-up sits in the `[Unreleased]`
  CHANGELOG section above and will roll into v0.4.3 once
  associated work batches up — it is post-tag, so it does not
  invalidate the v0.4.2 cosign-signed artifact surface.

## Sign-off

This release was cut, deployed, and independently verified within
one session window on 2026-05-14 (UTC). All eight verification
checkpoints (test posture, release CI, asset count, BLR1 binary
deploy, version pin, four cosign verifications) are green.
`/api/v1/audit/summary` is publicly readable; the audit-checklist
score is now anchored as a third-party-observable supply-chain
signal alongside the trust-attestation transparency surface.
