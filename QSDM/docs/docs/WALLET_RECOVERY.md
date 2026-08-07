# QSDM Wallet Recovery

QSDM Recovery Words are the human-readable backup for new recovery-enabled
CELL wallets. A phrase contains **24 words** and can rebuild the same ML-DSA-87
wallet on Windows or Linux without the original JSON file.

## Why 24 words, not 12

A 12-word phrase carries 128 bits of entropy. QSDM uses a 32-byte ML-DSA-87
key-generation seed, so QSDM keeps the full 256-bit recovery strength by using
24 words. The words also include a checksum that catches most transcription
errors.

QSDM does not compress an encrypted JSON file into the words. An arbitrary
keystore is far larger than a short phrase, and encryption cannot make it fit
without losing information. New wallets derive their key directly from the
words. Upgraded older wallets use the words to locate and decrypt an opaque
recovery capsule replicated by QSDM Core. Both paths use established
cryptographic building blocks and versioned formats.

## What each secret does

- **24 QSDM Recovery Words** rebuild the wallet and control its CELL.
- **Wallet passphrase** encrypts the local keystore JSON. It can be replaced
  when restoring the wallet.
- **Keystore JSON** is the encrypted working copy used by Hive and `qsdmcli`.
  Keep a backup because it is the fastest recovery path.
- **Hive profile phrase**, if an old profile has one, restores only local Hive
  settings. It is not a CELL wallet backup.

Never store recovery words beside the wallet JSON, paste them into a website,
or send them to support. QSDM has no recovery service that can reverse a lost
phrase.

## Hive workflow

1. Open **Settings > Wallet > Create New Wallet**.
2. Set and confirm a local passphrase.
3. Write down all 24 words shown after creation and store them offline.
4. Use **Backup JSON** for an additional encrypted backup.

To restore, choose **Restore with 24 Words**, select the correct recovery type,
enter all words, and choose a new local passphrase. Hive reconstructs the same
address and writes a fresh encrypted JSON file.

**Export Words** is available only for a wallet originally created or restored
with QSDM Recovery Words. It requires the wallet passphrase and saves the words
to a private file selected by the user.

## Existing wallets

Older QSDM wallets were generated randomly and remain valid. Hive can now add
24-word recovery without changing the wallet address:

1. Unlock the old wallet with its existing JSON and passphrase.
2. Open **Settings > Wallet** and choose **Enable Recovery**.
3. Save the 24 words offline and wait for QSDM Core confirmation.
4. Keep the automatic pre-recovery JSON backup and the current encrypted JSON.

The words derive an encryption key and an opaque locator. Hive encrypts the
exact old ML-DSA key into a versioned AES-256-GCM capsule, signs the capsule
registration with the wallet, and QSDM Core replicates only the ciphertext.
Validators cannot read the private key or recovery words. On restore, choose
**Older wallet upgraded in Hive** so Hive retrieves and decrypts that capsule.

Activation requires the wallet to exist in QSDM account state and QSDM Core to
confirm the signed capsule. Unlike a newer wallet whose key is derived directly
from its words, an upgraded older wallet needs both its words and access to the
replicated capsule. Keep the encrypted JSON backup as an independent recovery
path. If both the JSON/passphrase and recovery words are lost, QSDM cannot
recover the wallet.

## Native CLI

The CLI accepts recovery words through private files rather than command-line
arguments, keeping them out of process listings and shell history.

```text
qsdmcli wallet new --out wallet.json --passphrase-file passphrase.txt \
  --recovery-out offline-recovery.txt

qsdmcli wallet restore --out restored-wallet.json \
  --passphrase-file new-passphrase.txt \
  --recovery-file offline-recovery.txt

qsdmcli wallet export-recovery --in wallet.json \
  --passphrase-file passphrase.txt \
  --out offline-recovery.txt

qsdmcli wallet enable-recovery --in old-wallet.json \
  --passphrase-file old-passphrase.txt \
  --recovery-out offline-recovery.txt \
  --api-url https://api.qsdm.tech/attest/home-validator/api/v1

qsdmcli wallet restore-legacy --out restored-old-wallet.json \
  --passphrase-file new-passphrase.txt \
  --recovery-file offline-recovery.txt \
  --api-url https://api.qsdm.tech/attest/home-validator/api/v1
```

The recovery format is `qsdm-wallet-recovery-v1`:

- 256 bits of operating-system random entropy;
- BIP-39 English word encoding and checksum;
- HKDF-SHA-256 domain separation for the ML-DSA-87 deterministic seed;
- AES-256-GCM encryption of recovery entropy inside the keystore;
- an independent PBKDF2 salt and AES-GCM nonce for that recovery record;
- authenticated binding between the recovery record and wallet address.

The phrase uses familiar BIP-39 words, but its derived wallet is QSDM-specific.
Import it only through QSDM software.

Upgraded older wallets use `qsdm-legacy-wallet-recovery-v1`:

- 256 bits of operating-system random entropy encoded as 24 BIP-39 words;
- HKDF-SHA-256 domain separation for the capsule key and opaque locator;
- AES-256-GCM encryption of the original ML-DSA private/public key and address;
- signed, nonce-protected capsule registration in QSDM consensus state;
- post-decryption verification that the private key still derives the recorded
  public key and address.
