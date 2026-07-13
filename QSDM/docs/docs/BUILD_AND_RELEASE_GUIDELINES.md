# QSDM Build and Release Guidelines

This is the canonical build and release policy for QSDM Core, QSDM Hive,
the console miner, Edge Agent, Edge Control, and their Windows and Linux
packages. Component READMEs may add commands, but they must not weaken these
gates.

## Release principles

1. Build a release from one identified commit in a clean checkout.
2. Keep wallet material, passphrases, tokens, deployment credentials, local
   databases, and operator state outside the source tree and every artifact.
3. Test the exact source and dependency lock state used to build the artifacts.
4. Review the complete release delta, including generated files and bundled
   native binaries.
5. Publish immutable versioned artifacts with SHA-256 checksums, provenance,
   release notes, and rollback instructions.
6. Release coupled protocol components together. A protocol change is not
   complete if Core, Hive, Edge Control, or Edge Agent still speaks an
   incompatible version.
7. Only the latest approved production version is supported. Updater metadata
   and client gates must reject both older builds and unapproved higher builds.
8. A human release owner makes scope, severity, exception, risk-acceptance, and
   promotion decisions. Automation may collect evidence but may not approve
   its own release.

Read the repository [security policy](../../../SECURITY.md) before handling a
release candidate.

## Build workflow

### 1. Define the candidate

Record these values before testing:

- release version and Git commit;
- release owner;
- components and platforms in scope;
- approved review base;
- protocol/schema changes and compatibility plan;
- expected updater channel and public download paths;
- known exclusions and their human-approved rationale.

Update `CHANGELOG.md` with user-visible behavior, migration steps, security
changes, and rollback impact. Never reuse a published version for different
bytes.

### 2. Preflight the source

The review phase may inspect a dirty worktree when that dirty state is captured
exactly. The actual release build must use a clean checkout of the reviewed and
committed revision.

Required checks from the workspace root:

```powershell
git status --short
git rev-parse HEAD
git submodule status --recursive
python scripts/check_secrets.py --worktree
```

Stop for an active merge, rebase, or cherry-pick; unexpected untracked files;
missing submodules; secret-scan findings; or a source revision that differs from
the approved candidate.

### 3. Run the assurance gates

1. Review the full committed and working-tree delta for security, compatibility,
   generated files, and bundled native binaries.
2. Record findings, coverage gaps, and human disposition with the candidate.
3. Generate the existing QSDM Core evidence bundle:

```powershell
pwsh QSDM/scripts/release_evidence.ps1 -OutDir E:\QSDM-release-evidence\<version>\core
```

Use the non-`-Quick` mode for production. Review the manifest, environment,
audit checklist, module verification, vulnerability scan, vet output, tests,
SDK checks, and binary hashes as described in
[Release Evidence Bundles](RELEASE_EVIDENCE.md).

No Critical or High finding may remain unresolved. A Medium finding needs a
documented fix, release-owner acceptance, or release deferral. Low findings and
coverage gaps must remain visible in the evidence even when they do not block.

### 4. Validate components

Run the tests that cover the changed behavior, then the full component gates.

Core and command-line components:

```powershell
Push-Location QSDM/source
$env:CGO_ENABLED = '0'
Remove-Item Env:\CGO_CFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:\CGO_LDFLAGS -ErrorAction SilentlyContinue
go mod verify
go vet ./...
go test ./... -count=1 -timeout 900s
Pop-Location
```

Production cross-platform builds use QSDM's pure-Go CIRCL ML-DSA-87 backend.
Do not let workstation-global liboqs or Wasmer CGO flags silently change the
backend being tested. A separately approved liboqs compatibility build must use
an isolated toolchain and run the cross-backend wire-format tests.

Hive:

```powershell
Push-Location apps/qsdm-hive/qsdm-hive-main
npm ci
npm test -- --runInBand
npx tsc --noEmit
npm run build
Pop-Location
```

Edge pool and federation:

```powershell
Push-Location QSDM/source
go test ./pkg/edgepool ./cmd/qsdm-edge-agent ./cmd/qsdm-edge-control -count=1
Pop-Location
pwsh QSDM/scripts/build_edge_pool.ps1 -Version <MAJOR.MINOR.PATCH>
```

Run real NVIDIA hardware validation for miner or CUDA changes. A successful
compile or a mocked `nvidia-smi` response does not prove GPU work executed.

### 5. Build packages

Build production artifacts only after all source gates are green:

```powershell
pwsh QSDM/scripts/build_release.ps1 -Tag <version>

Push-Location apps/qsdm-hive/qsdm-hive-main
npm run package
Pop-Location
```

`npm run package` is host-aware. On Windows it first runs the canonical native
tool build and CUDA self-test, then packages Electron. On Linux it delegates to
`QSDM/deploy/scripts/build_hive_linux.sh`. The Electron `afterPack` gate rejects
a miner whose embedded Hive version differs from the app and rejects Edge Agent
or Edge Control versions that differ from `apps/qsdm-edge-agent/VERSION`.
The Windows command then rejects the release unless the installer, desktop
executable, and all bundled QSDM executables have valid timestamped
Authenticode signatures from the configured publisher. A SignPath Foundation
release uses the two-stage GitHub-hosted workflow in
`.github/workflows/qsdm-hive-windows.yml`: sign the unpacked QSDM executables,
package that signed directory with electron-builder `--prepackaged`, then sign
the outer NSIS installer. The workflow extracts the QSDM executables from NSIS
and requires their hashes to match the signed source payload. Signing only the
installer is not sufficient. Use
`npm run package:windows:unsigned` normally produces local smoke-test or signing
input. A repository-owner-approved unsigned prerelease may be published only
through the isolated `/downloads/unsigned-preview/` channel defined in
`CODE_SIGNING_POLICY.md`; it must not modify stable updater manifests or enable
automatic updates.
The first SignPath Foundation-signed Windows release is a publisher transition
and must be installed manually after verifying its signature and checksum.
Only subsequent releases signed by the same publisher use the normal updater
path.
Direct `npm run release` publishing is intentionally blocked: publish only the
joint, verified Windows/Linux artifact set through
`QSDM/deploy/scripts/publish_hive_release.sh`.

Build and smoke-test Hive on each supported operating system. A Windows package
does not validate Linux AppImage behavior, and a Linux package does not validate
Windows service, UAC, tray, clipboard, or installer behavior.

Before packaging Hive, confirm the bundled native directory contains the exact
reviewed Core/CLI, miner, Edge Agent, and Edge Control versions expected by the
release. Record their SHA-256 hashes before and after packaging.

### 6. Smoke-test the actual artifacts

Use clean or disposable Windows and Linux profiles. At minimum verify:

- install, first launch, PIN gate, restart, update, and uninstall;
- exact-version enforcement for older, current, and unapproved newer clients;
- wallet create/import, keystore JSON backup, passphrase handling, signing, and
  clipboard behavior without logging secret material;
- local Core and canonical gateway status, failover, timeout recovery, and no
  false zero balance during a transient outage;
- task catalog, staking persistence, rewards, round status, and restart restore;
- console and Hive NVIDIA mining with observed GPU utilization and accepted
  protocol proofs;
- Edge Agent to Relay to Mother Hive pairing, resource limits, workload
  execution, receipt verification, reconnect, invitation expiry, replay
  rejection, and federation key rotation;
- public download URL, update manifest, checksums, signatures, and installer
  metadata resolve to the same approved version.

Do not use production wallets or unrelated nodes for destructive testing.

### 7. Publish and verify

Publish versioned, immutable artifacts first. Update the `latest` pointer only
after remote checksum/signature verification and smoke tests pass. Attach or
retain:

- release notes and migration/rollback instructions;
- artifact manifest and SHA-256 checksums;
- Windows Authenticode signatures and trusted timestamp evidence (mandatory
  for production Hive releases), plus other certificate/transparency evidence
  where configured;
- SBOM and dependency provenance where configured;
- QSDM Core release-evidence bundle;
- security review summary, findings disposition, and coverage gaps, with
  secrets and private paths redacted from any public copy;
- human release-owner signoff.

After publication, download the public artifacts from a separate workstation,
verify hashes and signatures, launch them, and confirm the updater reports that
exact version as current.

## Failure and rollback rules

- A failed gate blocks promotion; it is not converted to a warning merely to
  finish a release.
- Never replace bytes behind an existing version or tag. Fix the issue and bump
  the version.
- Keep the previous immutable artifacts available for investigation, but do not
  make an unsupported version pass the latest-version gate.
- Roll back the server or latest pointer only with release-owner approval and a
  documented compatibility check for ledger, wallet, task, and federation
  state.
- Rotate credentials and wallet material immediately if evidence collection,
  packaging, logs, or publication may have exposed them.

## Definition of release-ready

A candidate is release-ready only when the source revision is clean and fixed,
all required tests/builds/smokes are green, QSDM evidence and the security
review record are complete, all blocking findings are resolved, cross-component
protocol versions are compatible, public artifacts verify byte-for-byte, and
the human release owner signs off.
