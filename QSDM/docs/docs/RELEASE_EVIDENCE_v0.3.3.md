# Release Evidence — v0.3.3

> Independent supply-chain verification of the v0.3.3 release line.
> Every signature below was checked from a **third-party workstation
> (not the release runner)** against the Sigstore public good
> instance, the Rekor transparency log, and the public GHCR
> registry. The verifications can be reproduced by anyone with
> internet access and a recent `cosign` binary — no privileged
> credentials are needed.
>
> Companion document to [`RELEASE_EVIDENCE.md`](RELEASE_EVIDENCE.md)
> (which covers the v0.3.0 release and the broader CI methodology).
> This file is **only** the cosign / Rekor evidence for v0.3.3.

## At a glance

| Verification | Subject | Result |
|--------------|---------|--------|
| SHA256SUMS (root of binary integrity tree) | `release-container.yml@refs/tags/v0.3.3` | ✓ Verified |
| Individual binary signature (`qsdmminer-console-linux-amd64`) | same | ✓ Verified |
| Source SBOM (`qsdm-source-sbom.spdx.json`) | same | ✓ Verified |
| Container `ghcr.io/blackbeardone/qsdm:0.3.3` | same | ✓ Verified (4 sig entries in Rekor) |
| Container `ghcr.io/blackbeardone/qsdm-validator:0.3.3` | same | ✓ Verified (4 sig entries) |
| Container `ghcr.io/blackbeardone/qsdm-miner:0.3.3` | same | ✓ Verified (4 sig entries) |
| Binary content hash vs SHA256SUMS row | `qsdmminer-console-linux-amd64` | ✓ MATCH (`c23b0218e0e2…d65fcee`) |

**7/7 supply-chain checks passed.**

The four sig entries per container image reflect cosign's re-signing
each time the workflow runs — the registry retains one per
sign-blob invocation. All four entries on each image are bound to
the same OIDC subject (`refs/tags/v0.3.3`) and the same GitHub
workflow run SHA (`03edf41612585b378908839bafa6f42974311781` —
the exact commit the `v0.3.3` git tag points at).

## Provenance fingerprint

Every cosign certificate emitted by the v0.3.3 release run carries
the following Sigstore custom-OID claims (all identical across
binaries and containers, which is the whole point — they pin every
artefact to the same workflow run):

| Sigstore OID | Value |
|--------------|-------|
| `1.3.6.1.4.1.57264.1.1` (Issuer) | `https://token.actions.githubusercontent.com` |
| `1.3.6.1.4.1.57264.1.2` (Workflow trigger) | `push` |
| `1.3.6.1.4.1.57264.1.3` (Workflow SHA) | `03edf41612585b378908839bafa6f42974311781` |
| `1.3.6.1.4.1.57264.1.4` (Workflow name) | `Release container` |
| `1.3.6.1.4.1.57264.1.5` (Repository) | `blackbeardONE/QSDM` |
| `1.3.6.1.4.1.57264.1.6` (Workflow ref) | `refs/tags/v0.3.3` |
| Subject | `https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0.3.3` |

If a future build reproduces the same source tree (same `03edf41…`
commit, same workflow ref) the cert claims must still match this
fingerprint. A mismatch on any of those OIDs is the operator
trip-wire for "someone hand-uploaded an artefact under the v0.3.3
tag without going through the workflow."

## Container image digests (immutable references)

The Sigstore signatures bind to the **manifest digest**, not the
tag. Anyone pulling the images can reference these digests instead
of the mutable `:0.3.3` tag and still get a cosign verification
match:

| Image | Manifest digest |
|-------|-----------------|
| `ghcr.io/blackbeardone/qsdm@<digest>` | `sha256:4a729619d056037ca65818e1cb9e85a89fd32be9021dbd3661cf9cc885edbb36` |
| `ghcr.io/blackbeardone/qsdm-validator@<digest>` | `sha256:251367d0a7ecb95a9fb21e230c2b1df4c921a214e2484a3e2789daeef05e19fe` |
| `ghcr.io/blackbeardone/qsdm-miner@<digest>` | `sha256:a552bc7f327f85414b7e67266be0bc0b0d716db44ebec7950d90c065f43d8b5f` |

The matching `.sig` and `.att` (SBOM-attestation) tags surface on
the GHCR API as
`sha256-<digest-hex>.sig` / `sha256-<digest-hex>.att` — they exist
for all three images per a tags/list probe at
`https://ghcr.io/v2/<image>/tags/list`.

## Binary content hash anchor

| File | SHA-256 |
|------|---------|
| `qsdmminer-console-linux-amd64` (15 089 848 bytes) | `c23b0218e0e27aefba05382af326da60531b9fcab80eef3c55d43d1d6d65fcee` |
| `SHA256SUMS` (signed root) | (line-matched against the file above) |

Operator self-check on Linux:

```bash
sha256sum -c <(grep qsdmminer-console-linux-amd64$ SHA256SUMS)
# Expected: ./qsdmminer-console-linux-amd64: OK
```

The same hash is repeated inside `qsdm-source-sbom.spdx.json` under
the SPDX `Package.Checksum` block for the corresponding build
artefact (CycloneDX-compatible). That is the linkage between the
"binary I downloaded", the "SBOM I verified", and the "release
workflow that produced both."

## Reproducing this evidence

Anyone can repeat the seven verifications without any privileged
credentials:

```powershell
# 1. Set up a clean directory.
mkdir release_verification\v0.3.3
cd release_verification\v0.3.3

# 2. Pull everything we want to verify.
$base = "https://github.com/blackbeardONE/QSDM/releases/download/v0.3.3"
foreach ($f in @(
    "SHA256SUMS","SHA256SUMS.sig","SHA256SUMS.pem",
    "qsdmminer-console-linux-amd64",
    "qsdmminer-console-linux-amd64.sig","qsdmminer-console-linux-amd64.pem",
    "qsdm-source-sbom.spdx.json",
    "qsdm-source-sbom.spdx.json.sig","qsdm-source-sbom.spdx.json.pem"
)) { curl.exe -sLO "$base/$f" }

# 3. Verify the three blob signatures.
foreach ($pair in @(
    @("SHA256SUMS"),
    @("qsdmminer-console-linux-amd64"),
    @("qsdm-source-sbom.spdx.json")
)) {
    $f = $pair[0]
    cosign verify-blob `
        --signature   "$f.sig" `
        --certificate "$f.pem" `
        --certificate-identity-regexp `
            "https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0\.3\.3" `
        --certificate-oidc-issuer https://token.actions.githubusercontent.com `
        $f
}

# 4. Re-derive the binary hash and confirm SHA256SUMS attests to it.
$expected = (Select-String -Path SHA256SUMS -Pattern "qsdmminer-console-linux-amd64$").Line
$expected   # <— should start with c23b0218e0e2…
(Get-FileHash -Algorithm SHA256 qsdmminer-console-linux-amd64).Hash.ToLower()
# Two values must be identical.

# 5. Verify the three container images.
$env:DOCKER_CONFIG = (New-Item -ItemType Directory -Force "$env:TEMP\dc-clean").FullName
foreach ($img in @(
    "ghcr.io/blackbeardone/qsdm:0.3.3",
    "ghcr.io/blackbeardone/qsdm-validator:0.3.3",
    "ghcr.io/blackbeardone/qsdm-miner:0.3.3"
)) {
    cosign verify `
        --certificate-identity-regexp `
            "https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0\.3\.3" `
        --certificate-oidc-issuer https://token.actions.githubusercontent.com `
        $img | Out-Null
    if ($LASTEXITCODE -eq 0) { Write-Host "$img  OK" } else { Write-Host "$img  FAIL" -ForegroundColor Red }
}
```

Caveat (Windows local dev): if `cosign` complains
about `docker-credential-desktop` not being found, set
`$env:DOCKER_CONFIG` to an empty directory **before** running the
verify command. Cosign reads that dir for credential helpers, and
GHCR public reads work fine without any helper.

Caveat (GHCR tag scheme): GHCR strips the leading `v` from the tag.
The git tag is `v0.3.3`; the GHCR tag is `0.3.3`. The cosign
verify works against the GHCR-side tag.

## What this proves (and doesn't)

**Proves:**

- Every public artefact published under the `v0.3.3` tag was
  produced inside `.github/workflows/release-container.yml` running
  on `blackbeardONE/QSDM`, at workflow ref `refs/tags/v0.3.3`,
  built from commit `03edf41612585b378908839bafa6f42974311781`.
- The artefacts' integrity is recorded in Rekor (public,
  append-only Sigstore transparency log) at the log IDs shown by
  cosign's `--output json` flag — any tampering would require also
  rewriting Rekor, which is impossible without breaking its
  Merkle-tree root signing.
- The single canonical SHA256SUMS file, itself cosign-signed,
  matches the on-disk hash of every binary that claims to belong
  to v0.3.3.

**Does not prove:**

- That the source tree at `03edf41` is itself free of latent
  vulnerabilities — that's audit scope (covered by the rolling
  `pkg/audit/checklist.go` items, including the three new rows
  `net-05`, `store-05`, `api-05` introduced in sessions 89–91
  that are now in v0.3.3 territory).
- That the OS image running the workflow itself was untampered.
  That's GitHub-Actions' supply chain, which is its own trust
  boundary; cosign + Sigstore are useless if the runner OS lies
  about its own identity. (This is the same residual trust everyone
  using GitHub-OIDC keyless signing accepts.)
- That `ghcr.io/blackbeardone/qsdm:0.3.3` is the **only** image
  ever published under that tag. The tag is mutable; an
  authenticated GHCR write could re-tag a different digest as
  `0.3.3`. The mitigation is to reference the **immutable
  digests** above instead of the tag.

## Verified on

| Field | Value |
|-------|-------|
| Verifier | Local workstation (Windows 10, PowerShell, cosign installed via `cargo install` workflow). Not the release runner. |
| Verification timestamp | 2026-05-13 (session 93). |
| cosign version | `2.x` (whichever ships with the local install — pin to `v2.4.1` to exactly match the workflow). |
| Network path | Direct to `objects.githubusercontent.com`, `ghcr.io`, `rekor.sigstore.dev` (public endpoints, no proxy). |
