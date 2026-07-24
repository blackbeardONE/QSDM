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
without losing information. QSDM also does not use a private, unreviewed
cipher. The QSDM-specific part is the versioned wallet derivation contract;
the building blocks remain established cryptography.

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

To restore, choose **Restore with 24 Words**, enter all words, and choose a new
local passphrase. Hive reconstructs the same address and writes a fresh
encrypted JSON file.

**Export Words** is available only for a wallet originally created or restored
with QSDM Recovery Words. It requires the wallet passphrase and saves the words
to a private file selected by the user.

## Existing wallets

Older QSDM wallets were generated randomly and remain valid. Their recovery
method is still **keystore JSON + passphrase**. They cannot be assigned a true
phrase afterward because the original random key was not derived from phrase
entropy.

To move an older wallet to phrase recovery:

1. Create a new recovery-enabled wallet and secure its 24 words.
2. Verify the new address from the words on a second device or temporary
   profile.
3. Transfer CELL and task ownership using the normal signed migration paths.
4. Keep the old JSON and passphrase until every balance and task is confirmed.

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
