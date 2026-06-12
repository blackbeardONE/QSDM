# QSDM Hive

QSDM Hive is the Windows client for CELL wallets, signed QSDM tasks, integrations, NVIDIA-only protocol mining, and CPU shared edge participation.

## Install path

Hive is the public release path for most users. It is the recommended way to use
CELL wallets, run signed tasks, link integrations, and start eligible mining
work. The standalone console miner remains an advanced operator artifact. QSDM
does not ship a separate consumer GUI miner.

1. Install QSDM Hive.
2. Create or import a QSDM wallet.
3. Back up the QSDM keystore JSON and passphrase.
4. Run CELL tasks, integrations, or qualifying mining work.

## Wallet backup

QSDM CELL wallet recovery uses the **QSDM keystore JSON plus its passphrase**. Hive profile phrases, when present, restore only local Hive profile data. They are not CELL wallet recovery phrases.

## Tasks in Hive

- **QSDM Miner** is NVIDIA-only protocol mining for supported GPUs. Minimum path: NVIDIA Turing or newer, CUDA compute capability 7.5+, working NVIDIA drivers/nvidia-smi, and a funded QSDM signer.
- **QSDM Edge Worker** enables CPU shared edge participation for users without NVIDIA GPUs.
- **Sky Fang - MMORPG** verifies that a Sky Fang account is linked to the active QSDM wallet before reward proofs are submitted.

## Console mining

Advanced operators can run `qsdmminer-console` directly when they need a
terminal-first service workflow. Consumer setups should use Hive. The retired
GUI miner is not a public release path.

## Networking

Hive uses local services for the desktop app and node monitor. Public reachability should go through the QSDM home gateway or network tunnel unless an operator intentionally exposes validator services.

## Related pages

- [Download QSDM Hive](https://qsdm.tech/download.html)
- [CELL tokenomics](CELL_TOKENOMICS.md)
- [Sky Fang official website](https://skyfang.xyz/)
- [Sky Fang integration notes](https://skyfang.xyz/docs)
- [Miner quickstart](MINER_QUICKSTART.md)
- [Wallet explanation](WALLET_EXPLANATION.md)
