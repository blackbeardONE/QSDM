# QSDM Code Signing Policy

This policy covers public QSDM Hive releases for Windows. It complements the
[build and release guidelines](QSDM/docs/docs/BUILD_AND_RELEASE_GUIDELINES.md)
and the [security policy](SECURITY.md).

## Signing status

QSDM is preparing an application for the SignPath Foundation open-source code
signing program. The application is pending. No QSDM artifact may be described
as SignPath-signed until SignPath Foundation accepts the project and the final
artifact passes the verification gates in this repository.

Planned open-source signing acknowledgement:

> Free code signing provided by [SignPath.io](https://signpath.io/), certificate
> by [SignPath Foundation](https://signpath.org/).

The certificate used by that program is issued to SignPath Foundation. Windows
will therefore show SignPath Foundation as the verified publisher. QSDM will
not substitute a self-signed certificate or install a private root certificate
on consumer computers to imitate public trust.

## Project and roles

- Repository: <https://github.com/blackbeardONE/QSDM>
- License: [MIT](LICENSE)
- Committer and reviewer: [@blackbeardONE](https://github.com/blackbeardONE)
- Release approver: [@blackbeardONE](https://github.com/blackbeardONE)

QSDM currently has one repository custodian. Automated build credentials may
submit a signing request, but they cannot approve it. The release approver must
review the source revision, CI evidence, artifact manifest, and security gates
before manually approving a production signing request. GitHub and signing
accounts used for these roles must have multi-factor authentication enabled.

## What is signed

The QSDM Hive Windows signing scope is limited to artifacts built from this
repository:

- the QSDM Hive desktop executable;
- QSDM CLI, miner, CUDA solver, Edge Agent, Edge Control, and GPU helper
  executables bundled with Hive; and
- the final QSDM Hive installer.

Third-party executables and libraries are not re-signed as QSDM software. Their
upstream signatures are preserved and verified where the package format and
tooling support that check.

## Trusted build and approval flow

1. A release starts from an immutable Git tag that points to a reviewed commit
   on the protected default branch.
2. GitHub-hosted runners build the unsigned Windows payload from that exact
   commit and dependency lock state. Self-hosted runner output is not eligible
   for SignPath Foundation production signing.
3. The unsigned payload is uploaded as a GitHub Actions artifact before the
   SignPath connector receives the request. Local workstation uploads are for
   diagnosis only and are not production release inputs.
4. SignPath verifies build origin and signs only paths permitted by the QSDM
   artifact configuration. Each production request requires manual approval.
5. The signed payload is packaged into the installer, and the installer is
   submitted as a separate signing request from the same workflow and commit.
6. `QSDM/deploy/scripts/verify_hive_nsis_payload.ps1` requires every embedded
   QSDM executable to match the signed source payload byte-for-byte, and
   `verify_hive_windows_signature.ps1` rejects the release unless every
   required executable and the installer has a valid, timestamped
   Authenticode signature from the configured publisher.
7. Checksums, signature evidence, source revision, release notes, and rollback
   instructions are retained with the immutable release.

Unsigned artifacts are named and retained only as development or signing-input
artifacts. They must never be copied to the public updater or download paths.

The first SignPath Foundation-signed release is a publisher transition from
existing unsigned or locally identified builds. It requires a manual installer
upgrade after signature and checksum verification. Automatic updates resume
only between releases signed by the same trusted publisher identity.

## Release requirements

A signing request is denied when any of these conditions is true:

- the source revision is not reviewed, tagged, or clean;
- a required CI, test, secret scan, security scan, or release-evidence gate
  failed or did not run;
- the package contains an unexpected executable or an unreviewed binary;
- bundled QSDM component versions do not match the Hive release;
- a wallet, passphrase, token, deployment credential, local database, or other
  private runtime state is present in the artifact;
- required Windows metadata or final Authenticode verification is missing; or
- the release owner has not explicitly approved promotion.

Mining remains opt-in and visible to the user. The signed application must
retain clean stop and uninstall controls and must not silently enable mining or
resource sharing.

## Verification

Users can inspect a downloaded installer with Windows PowerShell:

```powershell
Get-AuthenticodeSignature .\qsdm-hive-<version>-win-x64.exe |
  Format-List Status,StatusMessage,SignerCertificate,TimeStamperCertificate
Get-FileHash .\qsdm-hive-<version>-win-x64.exe -Algorithm SHA256
```

The signature status must be `Valid`, the publisher must match the publisher
declared for that release, and the SHA-256 value must match QSDM's immutable
release manifest.

## Privacy and incident response

QSDM Hive's data-handling boundaries are documented in the
[privacy policy](PRIVACY.md). Vulnerabilities or suspected signing-key misuse
must be reported through the private process in [SECURITY.md](SECURITY.md).
Signing is suspended during an unresolved supply-chain incident. A compromised
credential or certificate is revoked before another release is approved.
