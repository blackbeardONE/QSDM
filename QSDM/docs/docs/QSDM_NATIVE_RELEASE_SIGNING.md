# QSDM-native release signing

QSDM Hive uses a pinned ML-DSA-87 release key to authenticate its Windows and
Linux update channel. This is an application-level trust system. It complements
checksums and future platform signing, but it is not Microsoft Authenticode.

To check a download yourself, see
[Downloads & verification](DOWNLOADS_AND_VERIFICATION.md).

## Enforced trust chain

1. A release owner builds immutable Hive artifacts from one reviewed commit.
2. The offline QSDM release wallet signs an exact JSON manifest containing the
   version, commit, validity window, platform, artifact names, sizes, roles, and
   SHA-256 hashes.
3. The manifest and signature are published as one atomic
   `qsdm.signed-release.v1` envelope on the hive-v2 release channel,
   `https://qsdm.tech/downloads/hive-v2/`:
   - `qsdm-hive-release-windows-v2.json`
   - `qsdm-hive-release-linux-v2.json`
4. Hive verifies the envelope with its pinned ML-DSA-87 public key and rejects
   any other `key_id`.
5. Hive verifies the updater metadata against the signed size and hash, then
   checks that its version and installer name match the signed release.
6. After download, Hive verifies the installer filename, size, and SHA-256
   before allowing installation.

Any missing, expired, malformed, mismatched, or incorrectly signed input fails
closed. Older clients and unapproved higher-version clients remain blocked by
the exact-version policy.

Signed manifests are valid for 90 days from issue and are re-signed before
they expire. Hive rejects a manifest that is expired, dated in the future
beyond normal clock skew, or valid for more than 120 days.

## Current release key (v2)

The current public release-key ID is:

```text
4081bf2c4755f4c5c1565b4fac75e14a7e0b52042ac3d34bf8a7f525866c64a9
```

The public key is tracked at
`QSDM/deploy/release-trust/qsdm-hive-release-key-v2.json`. It contains no secret
material. The `key_id` is the SHA-256 of the raw public key bytes, so it can be
recomputed from that file.

## Key rotation (October 2026)

The previous release key (`key_id`
`10ab9c5710761d4c9dca59d42446e9ea0e3315d15cdc3715df1dcb8c96fa07a1`, file
`QSDM/deploy/release-trust/qsdm-hive-release-key.json`) was rotated in October
2026 and no longer signs new releases. Hive 1.4.21 is the first release that
pins the v2 key.

Because Hive 1.4.20 (Windows) and 1.4.17 (Linux) trust only the previous key,
they cannot verify 1.4.21 on their own. Updating to 1.4.21 is a one-time manual
install from the [download page](https://qsdm.tech/download.html); wallet and
settings are kept. The previous `/downloads/` channel stays online so those
versions keep working until their signed manifests expire.

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

Key custody and the release-signing procedure are documented privately for
release owners.

## Incident response

If the release key may be exposed, stop publishing immediately, remove update
pointers, preserve evidence, and publish a security notice. Do not reuse a
version number or overwrite immutable artifacts. Generate a new key under clean
custody and require a reviewed trust-root migration.
