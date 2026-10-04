# What is QSDM

**QSDM** (Quantum-Secure Dynamic Mesh Ledger) is an open-source ledger for the
coin **Cell (CELL)**. Wallet transactions are signed with **ML-DSA-87**, the
NIST-standardised post-quantum signature scheme (FIPS 204). New CELL is created
by GPU mining, on NVIDIA hardware today, through the **QSDM Hive** desktop app.

QSDM runs as a **public pilot network (pre-mainnet)** with a **single block
producer during recovery**. The design aims at many independent validators;
that part is not active yet. [Network status](NETWORK_STATUS.md) shows exactly
what runs today.

## The pieces

| Piece | What it does | Start here |
|---|---|---|
| **Core node** | Keeps the ledger, produces and serves blocks, exposes the public HTTP API | [API reference](API_REFERENCE.md) |
| **QSDM Hive** | Desktop app for Windows and Linux: wallet, NVIDIA mining, tasks | [Mining today](MINING_TODAY.md), [Hive app guide](QSDM_HIVE.md) |
| **Web wallet** | Self-custody wallet in the browser; keys stay on your device | [Web wallet](WEB_WALLET.md) |
| **Explorer** | Blocks, transactions and balances | [qsdm.tech/explorer.html](https://qsdm.tech/explorer.html) |
| **`qsdmcli`** | Command-line wallet and tools | [Command-line miner](MINER_QUICKSTART.md) |

## CELL in numbers

- Max supply **90,000,000 CELL**, all from mining. (A 10,000,000 CELL genesis
  treasury is a proposal for mainnet; it is not part of the current chain.)
- 10 second target block time; the block reward halves about every four years.
- 8 decimals; the smallest unit is called *dust* (1 CELL = 100,000,000 dust).

Details: [CELL tokenomics](CELL_TOKENOMICS.md).

## What is honest to expect today

- Your wallet keys are yours: QSDM software creates them on your device, and
  transfers need your ML-DSA-87 signature.
- Block production is run by the project during recovery. Signed consensus and
  independent validators are on the [Roadmap](ROADMAP.md).
- No independent external audit has been completed yet.

Read the [Security model](SECURITY_MODEL.md) before you store value on the
pilot network.

## Next steps

- New user: [Downloads & verification](DOWNLOADS_AND_VERIFICATION.md), then the
  [Web wallet](WEB_WALLET.md) or Hive.
- Miner: [Mining today](MINING_TODAY.md).
- Developer: [API reference](API_REFERENCE.md).
- Source code: [github.com/blackbeardONE/QSDM](https://github.com/blackbeardONE/QSDM).
