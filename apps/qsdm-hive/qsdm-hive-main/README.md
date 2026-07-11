# QSDM Hive

QSDM Hive is a desktop application for operating a decentralized task node. It provides a focused interface for running tasks, managing wallets, checking rewards, and monitoring node health.

## Prerequisites

- Node.js 16 or newer
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
to `release/build-linux/` and must be built on Linux. Direct Electron Builder
publishing is disabled; the release host publishes a verified Windows/Linux set
with `QSDM/deploy/scripts/publish_hive_release.sh`.

## Support

- Documentation: https://qsdm.tech/docs
- Website and support: https://qsdm.tech

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
