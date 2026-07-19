# QSDM-native release signing

QSDM Hive uses a pinned ML-DSA-87 release key to authenticate its Windows and
Linux update channel. This is an application-level trust system. It complements
checksums and future platform signing, but it is not Microsoft Authenticode.

## Enforced trust chain

1. A release owner builds immutable Hive artifacts from one reviewed commit.
2. The offline QSDM release wallet signs an exact JSON manifest containing the
   version, commit, validity window, platform, artifact names, sizes, roles, and
   SHA-256 hashes.
3. The manifest and signature are published as one atomic
   `qsdm.signed-release.v1` envelope:
   - `qsdm-hive-release-windows.json`
   - `qsdm-hive-release-linux.json`
4. Hive verifies the envelope with its pinned ML-DSA-87 public key.
5. Hive verifies the updater metadata against the signed size and hash, then
   checks that its version and installer name match the signed release.
6. After download, Hive verifies the installer filename, size, and SHA-256
   before allowing installation.

Any missing, expired, malformed, mismatched, or incorrectly signed input fails
closed. Older clients and unapproved higher-version clients remain blocked by
the exact-version policy.

The current public release-key ID is:

```text
10ab9c5710761d4c9dca59d42446e9ea0e3315d15cdc3715df1dcb8c96fa07a1
```

The public key is tracked at
`QSDM/deploy/release-trust/qsdm-hive-release-key.json`. It contains no secret
material.

## Initialize key custody

Run this once on the dedicated Windows signing account:

```powershell
pwsh QSDM/deploy/scripts/initialize_hive_release_signing.ps1 `
  -QsdmCliPath <reviewed-qsdmcli.exe>
```

The default private storage is `.cache/qsdm-release-signing`, which is ignored
by Git. It contains an encrypted QSDM keystore and a passphrase protected by
Windows DPAPI. Move that directory to encrypted offline storage and keep at
least one tested offline backup. Do not place it in GitHub, CI, a VPS, a shared
drive, or a normal release artifact.

The signing script refuses to operate unless the private key's public half
matches the trust root pinned in Hive.

## Sign a release

Build and finalize all artifacts first. Then create both platform envelopes:

```powershell
pwsh QSDM/deploy/scripts/new_hive_release_manifest.ps1 `
  -Platform windows `
  -Version <version> `
  -DownloadsDirectory <staged-downloads-directory> `
  -Commit <full-40-character-commit>

pwsh QSDM/deploy/scripts/new_hive_release_manifest.ps1 `
  -Platform linux `
  -Version <version> `
  -DownloadsDirectory <staged-downloads-directory> `
  -Commit <full-40-character-commit>
```

The signer validates the updater version and installer name, hashes every
required artifact, signs the exact manifest bytes, and immediately verifies its
own signature through `qsdmcli wallet verify`. The Windows envelope also
authenticates the versioned QSDM Wallet browser-extension ZIP and checksum file;
the extension is never published as a checksum-only side artifact.

Publish only through the QSDM Hive publisher scripts. They require both signed
envelopes, verify their pinned key ID and inner version, publish immutable
artifacts first, and move update pointers last.

## Security boundaries

QSDM-native signing proves that an artifact was approved by the pinned QSDM
release key and remained byte-for-byte intact. It does not:

- make Windows show a verified publisher;
- remove Microsoft SmartScreen warnings;
- replace Authenticode, trusted timestamping, or platform reputation;
- prevent reverse engineering of a distributed desktop application;
- recover safely from theft of both the release private key and the source that
  pins its public key.

Never ask users to install a private root certificate. Continue pursuing
Authenticode when it becomes financially practical. A release-key rotation is
a security migration: ship a reviewed Hive version that pins the new key before
publishing releases signed only by that key. Do not silently replace the public
key on the website.

## Incident response

If the release key may be exposed, stop publishing immediately, remove update
pointers, preserve evidence, and publish a security notice. Do not reuse a
version number or overwrite immutable artifacts. Generate a new key under clean
custody and require a reviewed trust-root migration.
