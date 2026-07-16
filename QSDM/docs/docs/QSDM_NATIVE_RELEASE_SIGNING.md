# QSDM-native release signing

QSDM can build its own release-signing layer, but it must not be confused with
Microsoft Authenticode.

## What QSDM-native signing can prove

- A release manifest was approved by a QSDM-controlled release key.
- Downloaded artifacts match the hashes in that signed manifest.
- The release key is the same key advertised in the repository and website.
- A user or Hive updater can reject modified, missing, or mismatched files.

## What it cannot prove

- It does not make Windows show a verified publisher.
- It does not remove Microsoft SmartScreen warnings.
- It does not replace a paid Authenticode certificate, SignPath, timestamping,
  or Microsoft reputation.
- It does not protect users if the release private key, website, and repository
  are all compromised at the same time.

## Proposed design

1. Generate an offline QSDM release-signing key.
2. Publish the public key in the repository, website, and release evidence.
3. Build immutable artifacts and `SHA256SUMS`.
4. Generate a canonical `release-manifest.json` containing version, commit,
   artifact names, sizes, SHA-256 hashes, supported platforms, and expiry.
5. Sign the manifest with the offline key.
6. Publish `release-manifest.json` and `release-manifest.sig`.
7. Teach Hive updater to verify:
   - exact app version policy;
   - manifest signature;
   - artifact hash;
   - artifact filename and platform;
   - rollback/downgrade rejection.

## Operational rules

- Keep the release private key off GitHub, CI, VPS hosts, and developer laptops
  used for daily work.
- Use a hardware token or offline encrypted storage when possible.
- Rotate the key only through a signed key-transition manifest.
- Never ask users to install a private root certificate.
- Continue pursuing Authenticode when funding or public-trust criteria make it
  realistic.
