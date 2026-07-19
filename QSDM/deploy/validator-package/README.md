# QSDM Validator Package

This package contains a production-capable QSDM validator for one operating
system and architecture. It is built with SQLite support and the
wire-compatible ML-DSA-87 implementation used by current QSDM nodes.

The package does not contain a wallet, validator identity, chain database, or
secrets. Keep those operator-owned files outside release archives.

Use a unique install directory, service/task name, config, identity, and data
directory for every validator. A standby is useful only after it has connected
to an approved bootstrap peer and its chain height has converged.

## Verify

The release archive is listed in the release-level `SHA256SUMS` file and is
signed through the QSDM GitHub Actions Sigstore identity. The unpacked package
also contains `SHA256SUMS.txt`; the installer verifies the validator binary
against it before making any changes.

```bash
./qsdm-validator --version
sha256sum -c SHA256SUMS.txt
```

```powershell
.\qsdm-validator.exe --version
Get-FileHash -Algorithm SHA256 .\qsdm-validator.exe
```

## Linux

Prepare a production TOML or YAML configuration first. On a new host:

```bash
sudo ./install-or-update.sh --config /secure/path/qsdm.toml
```

On later releases, unpack the new package and run:

```bash
sudo ./install-or-update.sh
```

The default service is `qsdm.service`, the default install directory is
`/opt/qsdm`, and writable runtime data is kept separately in `/var/lib/qsdm`.
The API/dashboard should remain loopback-bound behind a TLS gateway. Override
these defaults with `--help` options when operating another validator slot.
The health URL must point directly to that validator's loopback API listener,
not to Caddy or another reverse proxy. Custom service, user, data-directory,
and health settings are retained in the protected install state for later
updates and rollback.

Rollback restores only the previously installed executable:

```bash
sudo ./rollback.sh
```

For a second Linux validator, pass a distinct `--install-dir`, `--service`,
`--user`, `--data-dir`, `--health-url`, and config whose P2P/API/dashboard
ports and data directory do not overlap the first node.

Relative SQLite, log, proposal, and identity paths in the config resolve from
the writable data directory. Keep every writable validator path there; the
systemd unit makes the rest of the filesystem read-only to the service.

## Windows

Open PowerShell as Administrator. Prepare a production TOML or YAML
configuration first, then run:

```powershell
.\install-or-update.ps1 -ConfigPath C:\secure\qsdm.toml
```

The installer registers `QSDM-Validator` as a hidden startup task running as
the low-privilege built-in `LOCAL SERVICE` account. Administrator-managed
files live under `%ProgramData%\QSDM\Validator`; writable runtime data defaults
to `%ProgramData%\QSDM\ValidatorData`. Subsequent packages can update the same
installation without re-supplying the config. A custom task name, data
directory, and direct loopback health URL are retained in the install state
for later updates and rollback:

```powershell
.\install-or-update.ps1
```

Rollback:

```powershell
.\rollback.ps1
```

For a second Windows validator, use distinct `-InstallDir`, `-DataDir`,
`-TaskName`, `-HealthUrl`, config ports, identity, and data paths.

## Safety Boundaries

- Install/update scripts never edit or remove databases, block journals,
  wallet keystores, validator identities, or recovery archives.
- The current executable is timestamp-backed-up before replacement.
- Install state and executable backups remain root-managed and checksummed;
  the unprivileged service can write only its separate runtime data directory.
- A failed liveness check restores the prior executable automatically when a
  prior version exists.
- Fresh installs write a protected in-progress marker before creating managed
  runtime files, so an interrupted first install can be resumed safely.
- Liveness is accepted only when the exact installed executable owns the
  configured loopback API port; another validator or reverse proxy answering
  the URL cannot pass the update.
- A new validator must still be configured with unique identity material and
  operator-approved bootstrap peers. Never copy another validator's state or
  identity directory.
