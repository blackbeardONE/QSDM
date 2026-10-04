# Mining today (QSDM Hive 1.4.21)

How CELL mining works on the pilot network right now, for people using the QSDM
Hive desktop app. For the protocol details see
[Mining protocol v2](MINING_PROTOCOL_V2.md); for running the miner without Hive
see the [command-line miner](MINER_QUICKSTART.md).

> [!IMPORTANT]
> **New enrollments are paused during recovery.** GPUs that were enrolled
> before the September 2026 recovery can mine now. If your GPU is not enrolled
> yet, you can install Hive and create your wallet today; enrollment will
> reopen later and the date will be posted on [Network status](NETWORK_STATUS.md).

## At a glance

| | |
|---|---|
| Hardware | NVIDIA GPU, Turing or newer (compute capability 7.5 or higher): GeForce RTX 20/30/40/50 series and newer data-center GPUs |
| Software | QSDM Hive 1.4.21 for Windows x64 or Linux x86-64 |
| Driver | A current NVIDIA driver with a working `nvidia-smi`. You do not need the CUDA Toolkit; Hive bundles what it needs. |
| Linux extras | glibc 2.34 or newer; GTK 3 for the AppImage |
| Bond | 10 CELL per enrolled GPU, paid up front or taken from your first rewards |
| Reward | The current block reward (3.56490987 CELL per block, 10 second target) for accepted proofs; see [CELL tokenomics](CELL_TOKENOMICS.md) |
| Network | Pilot network (pre-mainnet), single block producer during recovery. Mining reopened on 2026-10-02 in public windows with per-owner limits. |

GPU mining is NVIDIA-only today. Every proof must carry an NVIDIA attestation;
the network rejects proofs without one.

## 1. Install Hive 1.4.21

1. Download Hive from the [download page](https://qsdm.tech/download.html).
   Version 1.4.21 is served from the new release channel
   `https://qsdm.tech/downloads/hive-v2/`.
2. Check the SHA-256 of the file before you run it. The steps are in
   [Downloads & verification](DOWNLOADS_AND_VERIFICATION.md).
3. Windows: the installer is not Authenticode-signed yet, so SmartScreen may show
   a warning. Check the hash, then choose **More info → Run anyway**.

> [!NOTE]
> **Coming from Hive 1.4.20 (Windows) or 1.4.17 (Linux)?** Those versions
> cannot update themselves to 1.4.21 because the release signing key was
> rotated. Install 1.4.21 once by hand; your wallet and settings are kept.
> Hive 1.4.17 stops receiving updates after 9 November 2026 and 1.4.20 after
> 19 December 2026.

## 2. Create or restore your wallet

In Hive, open **Settings → Wallet**:

- **Create New Wallet**, choose a passphrase, and write down the 24 recovery
  words. Keep them offline.
- Or **Import Wallet** / **Restore with 24 Words** if you already have one.

This wallet is your mining address: rewards are paid to it and it owns your
enrollment. Wallet transactions are signed with ML-DSA-87. See
[Wallet recovery](WALLET_RECOVERY.md) for backups.

**Keep the wallet unlocked while you mine.** Hive 1.4.21 signs every mining
proof with your wallet automatically (the owner signature). If the wallet is
locked, Hive shows "Unlock your QSDM wallet in Hive to mine." and does not
start the miner.

## 3. Enroll your GPU (when enrollment is open)

Start the **QSDM Miner** task. If the GPU is not enrolled, Hive asks
**Enroll this NVIDIA miner?** and offers two ways to put up the 10 CELL bond:

| Option | What happens |
|---|---|
| **Use mining earnings** | Starts with 0 CELL. Your first 10 CELL of rewards fill the bond; rewards after that are spendable. |
| **Lock CELL now** | Locks 10 CELL from your wallet right away (needs at least 10.001 CELL; the extra 0.001 CELL is the fee). |

Hive then creates a signed enrollment (`qsdm/enroll/v2`) for this GPU and your
wallet and submits it for you. You do not need to edit any files.

The bond stays locked while the GPU is enrolled. Unenrolling starts an
unbonding period of 201,600 blocks (about 23 days at 10-second blocks) before the CELL is
released; see [Mining protocol v2](MINING_PROTOCOL_V2.md) for the rules.

## 4. Mine

With an enrolled GPU and an unlocked wallet, Hive runs the bundled miner on your
NVIDIA GPU. It:

- fetches work from the network and solves it on the GPU;
- attaches an NVIDIA attestation and your owner signature to each proof;
- submits proofs to `https://api.qsdm.tech`. The mining endpoints are
  rate-limited per IP address and answer HTTP 429 when the limit is reached.

Accepted proofs earn CELL, paid to your Hive wallet. Check your balance in Hive,
in the [web wallet](https://qsdm.tech/wallet.html), or on the
[explorer](https://qsdm.tech/explorer.html).

## Attestation in one paragraph

Each proof says which NVIDIA attestation it carries:

- **`nvidia-hmac-v1`** is used by consumer GPUs (GeForce/RTX). At enrollment
  Hive registers the GPU's UUID and a private key file; every proof then carries
  a fresh signed bundle tied to that enrollment and to a short-lived challenge
  from the network. It is backed by the bond, not by a hardware certificate.
- **`nvidia-cc-v1`** is for data-center GPUs with NVIDIA Confidential
  Computing, checked against NVIDIA's signed attestation certificates.

## Logs and files

`~` is your home folder (`%USERPROFILE%` on Windows).

| File | Where |
|---|---|
| Miner log | `~/.qsdm/miner.log` (rotated at 10 MB). After start it should show `operator_signature = enabled (<your address>)`. |
| Miner settings | `~/.qsdm/miner.toml` |
| Miner key file | `~/.qsdm/miner-hmac.key`. Keep it private, like a password. |
| Hive app log | Windows `%APPDATA%\QSDM-Hive\logs\main.log`; Linux `~/.config/QSDM-Hive/logs/main.log` |
| Task log | Open it from the task menu in Hive |

## Troubleshooting

| Symptom | What to do |
|---|---|
| "Unlock your QSDM wallet in Hive to mine." | Unlock the wallet in **Settings → Wallet** and start the task again. |
| "QSDM protocol mining requires NVIDIA Turing or newer (7.5+)." | The GPU is too old for protocol v2. |
| Hive cannot read the GPU | Make sure `nvidia-smi` runs in a terminal; update the NVIDIA driver. |
| Enrollment fails | New enrollments are paused during recovery. Check [Network status](NETWORK_STATUS.md). |
| Many `429` responses in the miner log | You hit the per-IP rate limit, for example with several rigs behind one internet address. Run fewer rigs per address. |
| 1.4.20 / 1.4.17 does not offer 1.4.21 | Expected. Install 1.4.21 by hand once. |

More help: [Hive app guide](QSDM_HIVE.md) and
[Troubleshooting](TROUBLESHOOTING.md).
