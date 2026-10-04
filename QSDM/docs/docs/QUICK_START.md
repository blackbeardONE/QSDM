# Quick start

Five minutes from nothing to a CELL wallet on the QSDM pilot network
(pre-mainnet). Pick the path that fits you.

## 1. Look at the network (no install)

- Open the [explorer](https://qsdm.tech/explorer.html) to see blocks as they
  arrive (about one every 10 seconds).
- Or ask the API directly:

  ```bash
  curl -s https://api.qsdm.tech/api/v1/status
  ```

  [Network status](NETWORK_STATUS.md) explains every field.

## 2. Get a wallet

**In the browser:** open the [web wallet](https://qsdm.tech/wallet.html), create
a wallet and choose a strong passphrase. Your ML-DSA-87 key is generated and
encrypted on your device. Download the encrypted backup and keep it safe.
More: [Web wallet](WEB_WALLET.md).

**On the desktop:** install QSDM Hive 1.4.21 for Windows or Linux from the
[download page](https://qsdm.tech/download.html), check the file hash
([how](DOWNLOADS_AND_VERIFICATION.md)), then open **Settings → Wallet → Create
New Wallet** and write down the 24 recovery words.

Your address is 64 hexadecimal characters. Share it to receive CELL.

## 3. Check a balance

```bash
curl -s "https://api.qsdm.tech/api/v1/wallet/balance?address=<your address>"
```

## 4. Send CELL

Use **Send** in the web wallet or in Hive. The wallet fetches your next nonce,
signs the transfer with ML-DSA-87 and submits it; it is included in the next
block. Developers can do the same through `POST /api/v1/wallet/submit-signed`
([API reference](API_REFERENCE.md#post-walletsubmit-signed)).

## 5. Mine (NVIDIA GPU)

Mining runs in QSDM Hive on NVIDIA GPUs (Turing or newer). New enrollments are
paused during recovery; see [Mining today](MINING_TODAY.md) for the current
state and the full walkthrough.

## Before you store value

QSDM is a pilot network run by a single block producer during recovery, and it
has not had an independent external audit. Read the
[Security model](SECURITY_MODEL.md).

## Building the node from source

Developers who want to build and run the Go node locally should start with the
repository [README](https://github.com/blackbeardONE/QSDM/blob/main/QSDM/README.md)
and the [Operator guide](OPERATOR_GUIDE.md).
