# Release Evidence — v0.4.0

> Independent supply-chain verification of the v0.4.0 release line.
> Every signature below was checked from a **third-party workstation
> (not the release runner)** against the Sigstore public good
> instance, the Rekor transparency log, and the public GHCR
> registry. The verifications can be reproduced by anyone with
> internet access and a recent `cosign` binary — no privileged
> credentials are needed.
>
> Companion document to [`RELEASE_EVIDENCE.md`](RELEASE_EVIDENCE.md)
> (which covers the v0.3.0 release and the broader CI methodology)
> and [`RELEASE_EVIDENCE_v0.3.3.md`](RELEASE_EVIDENCE_v0.3.3.md)
> (which covers the v0.3.3 line — libp2p host key persistence,
> NGC attestation ring persistence, and `/wallet/mint` deprecation).
> This file is **only** the cosign / Rekor evidence for v0.4.0.

## What v0.4.0 ships

v0.4.0 is the **self-custody "Send transaction"** release. The
qsdm.tech browser wallet's new Send tab signs a fully client-side
ML-DSA-87 envelope and POSTs it to a new endpoint
`/api/v1/wallet/submit-signed` — the sender's private key never
leaves the browser. The new endpoint and its WASM helper are both
covered by the artefacts verified below.

## At a glance

| Verification | Subject | Result |
|--------------|---------|--------|
| SHA256SUMS (root of binary integrity tree) | `release-container.yml@refs/tags/v0.4.0` | ✓ Verified |
| Individual binary signature (`qsdmminer-console-linux-amd64`) | same | ✓ Verified |
| Source SBOM (`qsdm-source-sbom.spdx.json`) | same | ✓ Verified |
| Container `ghcr.io/blackbeardone/qsdm:0.4.0` | same | ✓ Verified |
| Container `ghcr.io/blackbeardone/qsdm-validator:0.4.0` | same | ✓ Verified |
| Container `ghcr.io/blackbeardone/qsdm-miner:0.4.0` | same | ✓ Verified |
| Binary content hash vs SHA256SUMS row | `qsdmminer-console-linux-amd64` | ✓ MATCH (`7009d562dfb3…3e2e8711`) |

**7/7 supply-chain checks passed.**

All sig entries are bound to the same OIDC subject
(`refs/tags/v0.4.0`) and the same GitHub workflow run
(`25811046765` — 10/10 jobs green, `release-container.yml` at commit
`318ed5e` — the exact commit the `v0.4.0` git tag points at).

## Provenance fingerprint

Every cosign certificate emitted by the v0.4.0 release run carries
the following Sigstore custom-OID claims (all identical across
binaries and containers, which is the whole point — they pin every
artefact to the same workflow run). Extracted from the binary
cosign cert with `openssl x509 -in <decoded.pem> -noout -text`:

| Sigstore OID | Value |
|--------------|-------|
| `1.3.6.1.4.1.57264.1.1` (Issuer) | `https://token.actions.githubusercontent.com` |
| `1.3.6.1.4.1.57264.1.9` (Build signer URI) | `https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0.4.0` |
| `1.3.6.1.4.1.57264.1.12` (Source repo URI) | `https://github.com/blackbeardONE/QSDM` |
| `1.3.6.1.4.1.57264.1.16` (Repo owner URI) | `https://github.com/blackbeardONE` |
| `1.3.6.1.4.1.57264.1.18` (Workflow ref) | `https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0.4.0` |
| `1.3.6.1.4.1.57264.1.21` (Workflow run URL) | `https://github.com/blackbeardONE/QSDM/actions/runs/25811046765/attempts/1` |
| Subject URI | `https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0.4.0` |
| Issuer (parent CA) | `O=sigstore.dev, CN=sigstore-intermediate` |

If a future build reproduces the same source tree (same `318ed5e…`
commit, same workflow ref) the cert claims must still match this
fingerprint. A mismatch on any of those OIDs is the operator
trip-wire for "someone hand-uploaded an artefact under the v0.4.0
tag without going through the workflow."

## Container image digests (immutable references)

The Sigstore signatures bind to the **manifest digest**, not the
tag. Anyone pulling the images can reference these digests instead
of the mutable `:0.4.0` tag and still get a cosign verification
match. All three are OCI image indexes (`application/vnd.oci.image.index.v1+json`,
856 bytes), each fanning out to per-architecture manifests:

| Image | Manifest-list digest |
|-------|----------------------|
| `ghcr.io/blackbeardone/qsdm@<digest>` | `sha256:00ccc73d4f3748240f2fefd6d942f75c1237b30c387f363dfebdc376614a1325` |
| `ghcr.io/blackbeardone/qsdm-validator@<digest>` | `sha256:a6cff8598ddb24294aee08c00305907c01f43ab0c495af96531763a1f96ac28e` |
| `ghcr.io/blackbeardone/qsdm-miner@<digest>` | `sha256:5176fb9c4102a27d21bede22083bd5a7a5a599f7a86be75d6e22127c48711590` |

The matching `.sig` and `.att` (SBOM-attestation) tags surface on
the GHCR API as
`sha256-<digest-hex>.sig` / `sha256-<digest-hex>.att` — they exist
for all three images per a tags/list probe at
`https://ghcr.io/v2/<image>/tags/list`.

## Binary content hash anchor

| File | SHA-256 |
|------|---------|
| `qsdmminer-console-linux-amd64` (15 089 848 bytes) | `7009d562dfb302ed04486731a7a89fc0dd984191cad2fc740fd870d83e2e8711` |
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

## Browser wallet WASM anchor (NEW in v0.4.0)

v0.4.0 is the first release where the browser-wallet WASM is
load-bearing on the new signing path. The deployed asset on
`qsdm.tech` is integrity-pinned to its sha384 by Subresource
Integrity (SRI) in both `wallet.js` and `wallet.html`:

| File | sha384 (SRI form) | Size |
|------|-------------------|------|
| `wallet.wasm` | `sha384-XKMSFMnk27ul5OLXqm2zFMPtsdSVUGNXK8sChbKc/Y2nIqVLEB330Ll+UDhz0Eb6` | 3 884 131 B |
| `wallet.js`   | `sha384-S7vr1mAtCqz5ww1XEdINXJXYsqupNK3tsjS3a/RO97wbxLlON5grl3/ZrvPsVJgZ` | 37 062 B |

Operator self-check (any platform):

```bash
curl -sSL https://qsdm.tech/wallet.wasm | openssl dgst -sha384 -binary | base64 -w0
# Expected: XKMSFMnk27ul5OLXqm2zFMPtsdSVUGNXK8sChbKc/Y2nIqVLEB330Ll+UDhz0Eb6
```

If a CDN or middlebox tampered with the bytes, the browser's SRI
check fails before `qsdm_wallet_sign_transaction` is ever invoked
and the Send tab shows an inline "WASM SRI hash mismatch" warning
rather than signing against an attacker-controlled module.

## Reproducing this evidence

Anyone can repeat the seven verifications without any privileged
credentials:

```powershell
# 1. Set up a clean directory.
mkdir release_verification\v0.4.0
cd release_verification\v0.4.0

# 2. Pull everything we want to verify.
$base = "https://github.com/blackbeardONE/QSDM/releases/download/v0.4.0"
foreach ($f in @(
    "SHA256SUMS","SHA256SUMS.sig","SHA256SUMS.pem",
    "qsdmminer-console-linux-amd64",
    "qsdmminer-console-linux-amd64.sig","qsdmminer-console-linux-amd64.pem",
    "qsdm-source-sbom.spdx.json",
    "qsdm-source-sbom.spdx.json.sig","qsdm-source-sbom.spdx.json.pem"
)) { curl.exe -sLO "$base/$f" }

# 3. Verify the three blob signatures.
$id    = "https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/v0.4.0"
$issu  = "https://token.actions.githubusercontent.com"
foreach ($f in @(
    "SHA256SUMS","qsdmminer-console-linux-amd64","qsdm-source-sbom.spdx.json"
)) {
  cosign verify-blob `
    --certificate "$f.pem" --signature "$f.sig" `
    --certificate-identity "$id" --certificate-oidc-issuer "$issu" `
    $f
}

# 4. Verify the three GHCR images. Run with DOCKER_CONFIG pointing at an
#    empty {} config so cosign skips any docker-credential-* helper.
$env:DOCKER_CONFIG = "$env:TEMP\_emptydockercfg"
New-Item -ItemType Directory -Force $env:DOCKER_CONFIG | Out-Null
'{}' | Out-File -FilePath "$env:DOCKER_CONFIG\config.json" -Encoding ascii
foreach ($img in @(
    "ghcr.io/blackbeardone/qsdm:0.4.0",
    "ghcr.io/blackbeardone/qsdm-validator:0.4.0",
    "ghcr.io/blackbeardone/qsdm-miner:0.4.0"
)) {
  cosign verify `
    --certificate-identity "$id" --certificate-oidc-issuer "$issu" $img
}

# 5. Confirm the binary you have is the binary the SHA256SUMS row signed.
sha256sum -c (Select-String -Path SHA256SUMS -Pattern qsdmminer-console-linux-amd64$).Line
```

All steps run on a fresh workstation with no QSDM credentials.

## Live-environment anchors (BLR1 + qsdm.tech)

The verifications above only prove "the artefacts are what the
release workflow built." To close the loop on "the artefacts are
what the production environment is actually running," v0.4.0
recorded these live-environment anchors at deploy time:

| Surface | Probe | Expected response |
|---------|-------|-------------------|
| Public validator | `curl https://api.qsdm.tech/api/v1/status` → `version` | `"v0.4.0"` ✓ |
| New endpoint reachability (POST) | `curl -X POST -d {} https://api.qsdm.tech/api/v1/wallet/submit-signed` | `HTTP 400` + `"invalid sender address: ... cannot be empty"` ✓ (proves the handler is in the routing tree and rejects on the first contract check) |
| New endpoint reachability (GET)  | `curl https://api.qsdm.tech/api/v1/wallet/submit-signed` | `HTTP 405` + `"method not allowed"` ✓ (proves the route is registered POST-only) |
| BLR1 systemd unit | `/etc/systemd/system/qsdm.service.d/version.conf` | `Environment="QSDM_BUILD_VERSION=v0.4.0"` ✓ |
| BLR1 binary swap | `sha256sum /opt/qsdm/qsdm` | `2874f088039bace6662754e2461c1f229b223a42deefc185fae5270e46d6d4fb` (matches local cross-compile; the v0.3.3 backup is preserved at `/opt/qsdm/qsdm.v033.bak`) |
| Landing pill | `curl https://qsdm.tech/` → `<span id="ver-pill-text">` | `v0.4.0` ✓ |
| Landing WASM | `curl https://qsdm.tech/wallet.wasm \| openssl dgst -sha384 -binary \| base64 -w0` | `XKMSFMnk27ul5OLXqm2zFMPtsdSVUGNXK8sChbKc/Y2nIqVLEB330Ll+UDhz0Eb6` ✓ |

On v0.3.3 both the GET and POST probes returned `HTTP 302`
(redirected to the dashboard login because the route did not yet
exist — fall-through to the catch-all auth-required dashboard
handler). The status-code split (`405` for GET, `400` for POST `{}`)
is a strong route-registered-but-validating signal that the
v0.4.0 handler is the one serving the request, not the v0.3.3
fall-through.

## Closing notes

- The cosign root of trust is the Sigstore public good instance.
  No private key material owned by the project signs any artefact —
  every signature derives from a short-lived OIDC token issued by
  GitHub to that specific workflow run.
- The Rekor transparency log entry for each `verify-blob` /
  `verify` invocation is offline-verified by cosign during the
  check (you'll see "Existence of the claims in the transparency
  log was verified offline" in cosign's stderr).
- This file is intentionally machine-readable in the rows above
  (digest, hash, OID) so a downstream audit tool can lift the
  expected anchors directly out of the Markdown table without
  needing to re-derive them from the artefacts.
