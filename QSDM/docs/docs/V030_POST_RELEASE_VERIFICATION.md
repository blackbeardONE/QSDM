# v0.3.0 — Third-Party Post-Release Verification

> Independent reproduction of every supply-chain claim made for the
> published QSDM v0.3.0 release. This is the document an auditor or
> downstream packager reads *first* — it does not rely on any QSDM
> tooling or QSDM-signed artefacts to bootstrap trust. Every command
> below uses only upstream public infrastructure (GitHub, GHCR,
> Sigstore Rekor, Sigstore Fulcio) and `cosign` built from upstream
> source.

## What was verified

The verification covers every published artefact of QSDM v0.3.0:

| Artefact | Channel | Verification |
|---|---|---|
| 20 release binaries (5 platforms × 4 commands) | GitHub Releases | SHA256 cross-match against `SHA256SUMS` **and** keyless cosign signature against the workflow OIDC identity |
| `SHA256SUMS` | GitHub Releases | keyless cosign signature |
| `qsdm-source-sbom.spdx.json` (SPDX 2.3, 690 KB) | GitHub Releases | keyless cosign signature |
| `ghcr.io/blackbeardone/qsdm:0.3.0` | GHCR | keyless cosign image signature + cosign SBOM attestation |
| `ghcr.io/blackbeardone/qsdm-validator:0.3.0` | GHCR | keyless cosign image signature + cosign SBOM attestation |
| `ghcr.io/blackbeardone/qsdm-miner:0.3.0` | GHCR | keyless cosign image signature + cosign SBOM attestation |

Verifier identity bound by every certificate: `https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0.3.0`.
Issuer: `https://token.actions.githubusercontent.com`. Workflow commit
SHA on every certificate: `c00fccd93a66c5317aaaa03b80e9a09d111e87bd`
(this is the session-78 commit on `main` that finally produced a
green release pipeline; see `RELEASE_NOTES_v0.3.0.md` §"Sessions
75–78" for the four CI bug fixes that got us there).

## Reproduce the verification yourself (Windows / Linux / macOS)

Prerequisites:

- Go 1.21+ (only used to install cosign, *not* to build QSDM).
- `curl` (or `Invoke-WebRequest`).
- ~60 MB of free disk for the 21-file representative download.
- An internet route to `github.com`, `ghcr.io`, `rekor.sigstore.dev`,
  and `fulcio.sigstore.dev`.

### Step 1 — install cosign from upstream source

```bash
go install github.com/sigstore/cosign/v2/cmd/cosign@v2.4.1
cosign version  # expect gitVersion: v2.4.1
```

The compiled binary lands in `${GOPATH:-$HOME/go}/bin/cosign`. We
deliberately do not use `cosign-installer@v3` here — that action is
what the QSDM release workflow uses to *produce* the signatures, so
re-using it would be circular.

### Step 2 — download a representative slice of the release

```bash
BASE=https://github.com/blackbeardONE/QSDM/releases/download/v0.3.0
mkdir verify-v030 && cd verify-v030
for f in \
  SHA256SUMS SHA256SUMS.sig SHA256SUMS.pem \
  qsdm-source-sbom.spdx.json qsdm-source-sbom.spdx.json.sig qsdm-source-sbom.spdx.json.pem \
  qsdmminer-windows-amd64.exe qsdmminer-windows-amd64.exe.sig qsdmminer-windows-amd64.exe.pem \
  qsdmminer-linux-amd64 qsdmminer-linux-amd64.sig qsdmminer-linux-amd64.pem \
  trustcheck-linux-amd64 trustcheck-linux-amd64.sig trustcheck-linux-amd64.pem \
  genesis-ceremony-linux-amd64 genesis-ceremony-linux-amd64.sig genesis-ceremony-linux-amd64.pem \
  qsdmminer-console-darwin-arm64 qsdmminer-console-darwin-arm64.sig qsdmminer-console-darwin-arm64.pem
do
  curl -sLo "$f" "$BASE/$f"
done
```

The full release has 66 assets (20 binaries + 20 `.sig` + 20 `.pem` +
`SHA256SUMS` + `SHA256SUMS.{sig,pem}` + the source SBOM +
`qsdm-source-sbom.spdx.json.{sig,pem}`). The subset above is what
QSDM's own reviewers walked end-to-end; downloading the rest is
arithmetic.

### Step 3 — cross-check SHA256SUMS against the actual files

```bash
sha256sum --check --ignore-missing SHA256SUMS
```

Every line printed must end with `OK`. The QSDM in-house run produced
the following byte-exact matches (write these down before you run —
they are the public ground truth):

```
1265c0a8a33d2fe06b56327df772de57a1e818ff5127a5f64522b282b934bfe4  ./qsdmminer-windows-amd64.exe
144602f1a8dab6322a634985f0aee6db427b913e242189a80e153442f2812fb0  ./qsdmminer-linux-amd64
72f209f0b9a3495a34cc95722cceb542641323746ac5c8a82a688579e9b6a08a  ./trustcheck-linux-amd64
23e1d1c6d617aa61096261facb8c10d4484b734bdc2abedc51b99c244b11996f  ./genesis-ceremony-linux-amd64
fdead0d4509ee555e9e3ffb32f6d2a496d6b48015f098673fc1dd7fb25a367b1  ./qsdmminer-console-darwin-arm64
ece9cdb5e39ebf6a27146e4eac11e0a4637ea67d07bb36cba7fa1fa625bc5d3a  qsdm-source-sbom.spdx.json
```

### Step 4 — verify the keyless cosign signature on every blob

```bash
ID_RE='https://github\.com/blackbeardONE/QSDM/\.github/workflows/release-container\.yml@refs/tags/v.+'
ISSUER='https://token.actions.githubusercontent.com'

for f in \
  qsdmminer-windows-amd64.exe \
  qsdmminer-linux-amd64 \
  trustcheck-linux-amd64 \
  genesis-ceremony-linux-amd64 \
  qsdmminer-console-darwin-arm64 \
  SHA256SUMS \
  qsdm-source-sbom.spdx.json
do
  cosign verify-blob \
    --certificate "$f.pem" \
    --signature   "$f.sig" \
    --certificate-identity-regexp "$ID_RE" \
    --certificate-oidc-issuer     "$ISSUER" \
    "$f"
done
```

Each command must print `Verified OK` and exit 0. The cosign
verifier performs four checks at once:

1. The `.pem` chains up to a Sigstore Fulcio root certificate.
2. The `.pem`'s OIDC SAN matches the regex — i.e. the signer was
   the QSDM release workflow running on the `v0.3.0` tag, not any
   other workflow or branch.
3. The `.sig` is a valid signature by the public key in `.pem` over
   the file's SHA256.
4. A Rekor transparency-log entry exists for this signature, proving
   the signature was minted before the issuance window expired
   (the Fulcio cert is intentionally short-lived).

### Step 5 — verify the published container images

cosign talks to GHCR directly over HTTPS — Docker is **not** required.
If you have Docker installed locally and your `~/.docker/config.json`
references a credential helper that isn't on PATH, point
`DOCKER_CONFIG` at an empty directory so cosign falls back to
anonymous access:

```bash
mkdir empty-docker-config && echo '{}' > empty-docker-config/config.json
export DOCKER_CONFIG="$PWD/empty-docker-config"
```

Then for each image:

```bash
for img in \
  ghcr.io/blackbeardone/qsdm:0.3.0 \
  ghcr.io/blackbeardone/qsdm-validator:0.3.0 \
  ghcr.io/blackbeardone/qsdm-miner:0.3.0
do
  cosign verify \
    --certificate-identity-regexp "$ID_RE" \
    --certificate-oidc-issuer     "$ISSUER" \
    "$img"
done
```

Output for each image includes the manifest digest, the workflow
identity, and a JSON dump of every Rekor entry that signed this
digest. Sample output for `ghcr.io/blackbeardone/qsdm:0.3.0`:

```
Verification for ghcr.io/blackbeardone/qsdm:0.3.0 --
The following checks were performed on each of these signatures:
  - The cosign claims were validated
  - Existence of the claims in the transparency log was verified offline
  - The code-signing certificate was verified using trusted certificate authority certificates

[
  {
    "critical": {
      "identity":  { "docker-reference": "ghcr.io/blackbeardone/qsdm" },
      "image":     { "docker-manifest-digest": "sha256:3f46260eef8a702c2e45631824cab8f59f2f792bb2efcb952d0de514509dad1e" },
      "type":      "cosign container image signature"
    },
    "optional": {
      "Issuer":                    "https://token.actions.githubusercontent.com",
      "Subject":                   "https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0.3.0",
      "githubWorkflowName":        "Release container",
      "githubWorkflowRef":         "refs/tags/v0.3.0",
      "githubWorkflowRepository":  "blackbeardONE/QSDM",
      "githubWorkflowSha":         "c00fccd93a66c5317aaaa03b80e9a09d111e87bd",
      "githubWorkflowTrigger":     "push",
      ...
    }
  }
]
```

The four `githubWorkflow*` claims are pinned into the certificate by
Fulcio at signing time — a stolen `NPM_TOKEN`, a forked workflow on
an attacker's repo, or a workflow run on `main` instead of the tag
cannot reproduce these claims. They are the cryptographic counterpart
to "this image came from THIS workflow on THIS tag".

### Step 6 — verify the SPDX SBOM attestation on each image

```bash
for img in \
  ghcr.io/blackbeardone/qsdm:0.3.0 \
  ghcr.io/blackbeardone/qsdm-validator:0.3.0 \
  ghcr.io/blackbeardone/qsdm-miner:0.3.0
do
  cosign verify-attestation \
    --type spdxjson \
    --certificate-identity-regexp "$ID_RE" \
    --certificate-oidc-issuer     "$ISSUER" \
    "$img"
done
```

Each invocation must exit 0 and print a header that includes:

```
Verification for ghcr.io/blackbeardone/qsdm:0.3.0 --
The following checks were performed on each of these signatures:
  - The cosign claims were validated
  - Existence of the claims in the transparency log was verified offline
```

The body is the base64-wrapped SPDX 2.3 SBOM produced by `syft`
inside the workflow run. Decode the `payload` field of the DSSE
envelope, base64-decode it, and you have the same SBOM document
shape as `qsdm-source-sbom.spdx.json` from the Releases page but
scoped to *this image*'s files and packages instead of the source
tree.

## QSDM in-house verification (recorded)

This section is the byte-for-byte record of the verification QSDM
itself ran from a Windows 11 + Go 1.24.2 + cosign v2.4.1 environment
on `2026-05-11T07:4x:00Z`, against the live `https://github.com/blackbeardONE/QSDM`
release page. It exists so a reviewer can sanity-check their own run
against ours.

### Native execution (sanity)

```
> .\qsdmminer-windows-amd64.exe --version
qsdmminer v0.3.0 (c00fccd, 2026-05-11T04:23:54Z, go1.25.10, windows/amd64)
```

The `c00fccd` short SHA matches the full SHA recorded by every
Sigstore certificate (see Step 5). `go1.25.10` matches the toolchain
directive in `QSDM/source/go.mod`; the Windows host bootstrap is
Go 1.24.2 — Go's toolchain auto-fetch is what closed the gap, not
a manual install. The full ldflags chain is therefore confirmed
working end-to-end from the runner all the way to the published
artefact.

### Verify results (7 blobs)

```
== cosign verify-blob qsdmminer-windows-amd64.exe         ==  Verified OK
== cosign verify-blob qsdmminer-linux-amd64               ==  Verified OK
== cosign verify-blob trustcheck-linux-amd64              ==  Verified OK
== cosign verify-blob genesis-ceremony-linux-amd64        ==  Verified OK
== cosign verify-blob qsdmminer-console-darwin-arm64      ==  Verified OK
== cosign verify-blob SHA256SUMS                          ==  Verified OK
== cosign verify-blob qsdm-source-sbom.spdx.json          ==  Verified OK
```

### Verify results (3 images + 3 attestations)

| Artefact | `cosign verify` | `cosign verify-attestation --type spdxjson` |
|---|---|---|
| `ghcr.io/blackbeardone/qsdm:0.3.0` | OK (manifest digest `sha256:3f46260eef8a702c2e45631824cab8f59f2f792bb2efcb952d0de514509dad1e`) | OK |
| `ghcr.io/blackbeardone/qsdm-validator:0.3.0` | OK | OK |
| `ghcr.io/blackbeardone/qsdm-miner:0.3.0` | OK | OK |

Every signature and attestation chains back to `https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0.3.0`,
issuer `https://token.actions.githubusercontent.com`, workflow SHA
`c00fccd93a66c5317aaaa03b80e9a09d111e87bd`.

## What this does (and does not) prove

What it proves:

- The exact files served by GitHub's CDN and GHCR's manifest endpoint
  *today* match the files produced by a GitHub Actions runner on
  `2026-05-11T04:2x:00Z`, executing `.github/workflows/release-container.yml`
  on the v0.3.0 tag, on commit `c00fccd93a66c5317aaaa03b80e9a09d111e87bd`.
- Nothing was substituted between the runner and the public endpoint.
  A man-in-the-middle on the download channel, a compromised CDN edge,
  or a malicious GitHub admin who edited a release after the fact
  would all break at least one of these checks.
- The image SBOMs were produced by the same workflow run that pushed
  the images. An auditor can therefore use the SBOM as the
  authoritative dependency manifest for what is actually inside the
  image, not what some sidecar told them is inside.

What it does *not* prove:

- That the Go and C source code at `c00fccd9` is free of bugs. That
  is what `pkg/audit/checklist.go` (53 items) and the soak-test
  harnesses in `tests/` and the per-package unit tests address.
- That the human operator pushing the v0.3.0 tag is who they claim
  to be. Sigstore attests the workflow's identity, not the tag
  pusher's. The workflow trigger metadata (`githubWorkflowTrigger:
  push`) is the closest available proxy.
- That GHCR's "make package public" lever is on. It is currently on
  for v0.3.0 (these commands work without auth); operators who later
  toggle visibility to private should expect anonymous reproductions
  of Step 5 / Step 6 to start failing while authenticated ones keep
  working.

## See also

- [`RELEASE_NOTES_v0.3.0.md`](../../../RELEASE_NOTES_v0.3.0.md) — full
  release narrative including the four CI fixes (sessions 75–78) that
  produced the green workflow run that this document verifies.
- [`RELEASE_EVIDENCE.md`](RELEASE_EVIDENCE.md) — instructions for
  generating a self-contained, hash-pinned bundle of *pre-publish*
  evidence (test output, audit report, vulnerability scan,
  reproducible build hashes). The verification in *this* document is
  its *post-publish* twin.
- [Release page on GitHub](https://github.com/blackbeardONE/QSDM/releases/tag/v0.3.0).
- The four sessions of CI fixes that produced the green run:
  - `134abf1` session 75 — unblock release-container + qsdm-go
  - `d8326c6` session 76 — real fix after observed s75 failure
  - `83c1128` session 77 — fix the ACTUAL root cause (GHCR case-sensitivity)
  - `c00fccd` session 78 — fix bash for-loop over newline-separated tag list
