# QSDM Hive

QSDM Hive is a desktop application for operating a QSDM task node. It provides a focused interface for running tasks, managing wallets, checking rewards, and monitoring node health.

## Platforms

- **Windows and Linux (x64):** the wallet, tasks and the CELL miner.
- **macOS 12 (Monterey) or newer, on Intel and Apple silicon Macs:** the
  wallet and tasks. There is no mining on a Mac: mining needs Windows or Linux
  with an NVIDIA GPU, so the Mac app does not include the miner.
  The Mac app is not signed by Apple yet. The first time, right-click (or
  Control-click) QSDM Hive in Applications and choose **Open**, then **Open**
  again; on macOS 15 and newer, open it once, then choose **Open Anyway** in
  System Settings > Privacy & Security. Hive does not update itself on a Mac
  yet: install each new version by hand. See [PACKAGING.md](PACKAGING.md#macos).

## Prerequisites

- Node.js 22.12 or newer, with npm 10.9 or newer
- npm or yarn
- Git, if you are cloning from a repository

## Installation

```sh
npm install
```

Copy `.env.example` to `.env` when environment-specific configuration is required, then update the values for your deployment.

## Development

```sh
npm start
```

## Testing

```sh
npm test
```

## Packaging

Before producing or publishing a package, follow the canonical
[QSDM Build and Release Guidelines](../../../QSDM/docs/docs/BUILD_AND_RELEASE_GUIDELINES.md).
They define the cross-platform test matrix, QSDM evidence, version gate,
artifact integrity, and release-owner signoff.

Use the host-native package command. It rebuilds and verifies the bundled QSDM
CLI, miner, Edge tools, and CUDA helper before Electron creates an artifact:

```sh
npm run package
```

Windows artifacts are written to `release/build/`. Linux artifacts are written
to `release/build-linux/` and must be built on Linux. The macOS app (universal,
ad-hoc signed, not notarized) is written to `release/build-mac/` and must be
built on a Mac; see [PACKAGING.md](PACKAGING.md#macos). Direct Electron Builder
publishing is disabled; the release host publishes a verified Windows/Linux set
with `QSDM/deploy/scripts/publish_hive_release.sh`.

Production Windows packaging requires a trusted, timestamped Authenticode
identity whose publisher exactly matches the approved release configuration.
The local first-party default is `QSDM`; the open-source SignPath
workflow would use `SignPath Foundation`. `npm run package` fails closed when any
required executable is unsigned. `npm run package:windows:unsigned` exists only
for local smoke testing and must never be published or placed behind an updater
manifest.

QSDM applied for the SignPath Foundation open-source program, but the
application was declined for now, so SignPath signing is not available and no
QSDM artifact may be described as SignPath-signed. Current public Windows Hive
releases are unsigned by Microsoft Authenticode; they are published only when
the release owner explicitly approves that and all release gates pass. The repository
[code signing policy](../../../CODE_SIGNING_POLICY.md) defines the trusted-build
and manual-approval flow; the [privacy policy](../../../PRIVACY.md) documents
Hive's local and network data boundaries.

## Updates

Hive runs only the exact version named by the current signed QSDM release, and
it updates itself to that version. It reads the signed release envelope from
`https://qsdm.tech/downloads/hive-v2/` and checks its ML-DSA-87 signature
against the release key built into Hive; see
[QSDM-native release signing](../../../QSDM/docs/docs/QSDM_NATIVE_RELEASE_SIGNING.md).

Since Hive 1.4.23, Hive keeps working when qsdm.tech is
down or the computer is offline. After
every successful check it saves the signed envelope in a
`release-envelope-cache` folder in its application-data directory. When a later
check fails, Hive checks that saved envelope again in full, signature included,
and uses it until the envelope's own expiry date (`expires_at`). There is no
other time limit. If the saved envelope names a different version, the update
is still required. Once the envelope has expired, or if the saved copy is
missing or damaged, Hive stays locked until it can check the website again.

From that release on, Hive never goes back to an older envelope: one issued
before the envelope Hive
saved is ignored. Every new release, renewal, or rollback must therefore be a
newly signed envelope with a later `issued_at`.

## Support

- Documentation: https://qsdm.tech/docs
- Website and support: https://qsdm.tech
- Problem reports: since Hive 1.4.23, open the menu of
  the Hive tray icon, choose **Save a
  diagnostics report…** and attach the saved file. Hive keeps crash records on
  your computer and sends nothing; see the
  [privacy policy](../../../PRIVACY.md#crash-records-and-diagnostics-reports).
- Mining error `gpu_uuid already bound to an active enrollment`: see the
  troubleshooting in [Mining today](../../../QSDM/docs/docs/MINING_TODAY.md#troubleshooting).

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
