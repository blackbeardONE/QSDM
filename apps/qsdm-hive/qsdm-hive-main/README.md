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

```sh
npm run package
```

Packaged builds are written to `release/build/`.

## Support

- Documentation: https://qsdm.tech/docs
- Website and support: https://qsdm.tech

## Orca

In QSDM Hive, go to Settings > Task Extensions and install Orca. If automatic installation fails, use the manual instructions at https://docs.chaindeck.io/orcaNode.

On Linux, virtualization support may require qemu:

```sh
apt install qemu-system
```

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
