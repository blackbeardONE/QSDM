# Mining today (QSDM Hive 1.4.22)

How CELL mining works on the pilot network right now, for people using the QSDM
Hive desktop app. For the protocol details see
[Mining protocol v2](MINING_PROTOCOL_V2.md); for running the miner without Hive
see the [command-line miner](MINER_QUICKSTART.md).

> [!IMPORTANT]
> **Mining is open to every enrolled miner** (public mode since 19 October
> 2026), in 7-day windows that renew automatically. Every proof must carry the
> enrollment owner's operator signature, which QSDM Hive 1.4.21 or newer adds
> by itself while your Hive wallet is unlocked. Caps apply per owner per
> 24-hour epoch.
>
> **Enrollment is open.** Install Hive, create your wallet and enroll your GPU
> (section 3). Mining was paused from 22 October to 1 November 2026; see
> [Network status](NETWORK_STATUS.md).

## At a glance

| | |
|---|---|
| Hardware | NVIDIA GPU, Turing or newer (compute capability 7.5 or higher): GeForce RTX 20/30/40/50 series and newer data-center GPUs |
| Software | QSDM Hive 1.4.22 for Windows x64 or Linux x86-64 |
| Driver | A current NVIDIA driver with a working `nvidia-smi`. You do not need the CUDA Toolkit; Hive bundles what it needs. |
| Linux extras | glibc 2.34 or newer; GTK 3 for the AppImage |
| Bond | 10 CELL per enrolled GPU, paid up front or taken from your first rewards |
| Reward | The current block reward (3.56490987 CELL per block, 10 second target) for accepted proofs; see [CELL tokenomics](CELL_TOKENOMICS.md) |
| Network | Pilot network (pre-mainnet), single block producer. Mining has been open to every enrolled miner since 2026-10-19, in 7-day (168-hour) windows that renew automatically, with caps per owner per 24-hour epoch. |

GPU mining is NVIDIA-only today. Every proof must carry an NVIDIA attestation;
the network rejects proofs without one.

## 1. Install Hive 1.4.22

1. Download Hive from the [download page](https://qsdm.tech/download.html).
   Version 1.4.22 is served from the release channel
   `https://qsdm.tech/downloads/hive-v2/`. It also adds
   [Proof-of-Entanglement](PROOF_OF_ENTANGLEMENT.md) parents to every
   transfer, which the network requires since block 816,600.
2. Check the SHA-256 of the file before you run it. The steps are in
   [Downloads & verification](DOWNLOADS_AND_VERIFICATION.md).
3. Windows: the installer is not Authenticode-signed yet, so SmartScreen may show
   a warning. Check the hash, then choose **More info → Run anyway**.

> [!NOTE]
> **Coming from Hive 1.4.20 (Windows) or 1.4.17 (Linux)?** Those versions
> cannot update themselves to 1.4.22 because the release signing key was
> rotated. Install 1.4.22 once by hand; your wallet and settings are kept.
> Hive 1.4.17 stopped working after 9 November 2026, and 1.4.20 stops after
> 19 December 2026: install 1.4.22 by hand.

## 2. Create or restore your wallet

In Hive, open **Settings → Wallet**:

- **Create New Wallet**, choose a passphrase, and write down the 24 recovery
  words. Keep them offline.
- Or **Import Wallet** / **Restore with 24 Words** if you already have one.

This wallet is your mining address: rewards are paid to it and it owns your
enrollment. Wallet transactions are signed with ML-DSA-87. See
[Wallet recovery](WALLET_RECOVERY.md) for backups.

**Keep the wallet unlocked while you mine.** Hive 1.4.22 signs every mining
proof with your wallet automatically (the owner signature). If the wallet is
locked, Hive shows "Unlock your QSDM wallet in Hive to mine." and does not
start the miner.

## 3. Enroll your GPU

Start the **QSDM Miner** task. If the GPU is not enrolled, Hive asks
**Enroll this NVIDIA miner?** and offers two ways to put up the 10 CELL bond:

| Option | What happens |
|---|---|
| **Use mining earnings** | Starts with 0 CELL. Your first 10 CELL of rewards fill the bond; rewards after that are spendable. |
| **Lock CELL now** | Locks 10 CELL from your wallet right away (needs at least 10.001 CELL; the extra 0.001 CELL is the fee). |

Hive then creates a signed enrollment (`qsdm/enroll/v2`) for this GPU and your
wallet and submits it for you. You do not need to edit any files.

One GPU can back only one active enrollment. If Hive says
`gpu_uuid already bound to an active enrollment`, see
[Troubleshooting](#troubleshooting).

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
| Enrollment fails | Enrollment has been open since 17 October 2026. **Lock CELL now** needs at least 10.001 CELL in the wallet; with less, choose **Use mining earnings**. Check [Network status](NETWORK_STATUS.md) for any change. |
| Enrollment fails with `gpu_uuid already bound to an active enrollment` | One GPU can back only one active enrollment, and this GPU already has one. Hive 1.4.22 makes a new node ID after a Windows reinstall, a new PC name, a lost `.qsdm` folder or another wallet, and the chain refuses the second enrollment. With Hive 1.4.22: if the old `.qsdm` folder still exists, copy its `miner.toml` and `miner-hmac.key` back and restart Hive. Otherwise, the next Hive release finds and reuses the enrollment by itself when Hive holds the wallet that owns it. An enrollment owned by another wallet can be ended only by that wallet (unenroll). After an unenroll the GPU can enroll again at once; the old bond is held for 201,600 blocks (about 23 days) and then returns to its owner. |
| Miner reports `mining_unavailable` (HTTP 503) | Mining is paused, for example while the producer restarts. Windows renew automatically, so mining reopens by itself; check [Network status](NETWORK_STATUS.md). |
| Many `429` responses in the miner log | You hit the per-IP rate limit, for example with several rigs behind one internet address. Run fewer rigs per address. |
| 1.4.20 / 1.4.17 does not offer 1.4.22 | Expected. Install 1.4.22 by hand once. |

More help: [Hive app guide](QSDM_HIVE.md) and
[Troubleshooting](TROUBLESHOOTING.md).
