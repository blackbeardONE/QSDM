# Security model

This page says plainly what protects CELL users on the pilot network today,
what does not exist yet, and what you have to trust while it is missing. It
describes the live network, not the end-state design. For the stage of the
network see [Network status](NETWORK_STATUS.md).

> [!IMPORTANT]
> QSDM is a pilot network (pre-mainnet) with a single block producer during
> recovery. No independent external audit has been completed. Do not keep
> value on it that you cannot afford to lose.

## Summary

| Area | Protected today | Not yet / what you trust |
|---|---|---|
| Wallet keys | Generated and kept on your device; private keys are stored encrypted | Your device, your passphrase, and the software you installed |
| Wallet transactions | Signed with **ML-DSA-87** (NIST FIPS 204, Cloudflare CIRCL); nonces stop replays | The generic peer-to-peer transaction verifier still also accepts Ed25519 |
| Block production | One producer runs the chain; every block is stored and served publicly | You trust that producer to include valid transactions and not to censor or reorder them |
| Consensus | Signed consensus messages are implemented | Not enforced yet (`signed_consensus_active: false`); there is no multi-validator BFT today |
| Mining | Enrollment needs a signed enrollment and a 10 CELL bond; proofs carry NVIDIA attestation; duplicate proofs and nonces are rejected durably | Consumer-GPU attestation (`nvidia-hmac-v1`) is an economic control tied to the enrollment and its bond, not a hardware proof |
| Software downloads | QSDM Hive releases are signed with the v2 release key and checked by Hive before updating | You trust the release key holder; the previous key was rotated in 2026 |
| Public API | Read endpoints are public; admin and certificate routes are not exposed; write routes are limited during recovery | Rate limits and nginx rules run on infrastructure operated by the project |
| Review | An internal, self-maintained [security review](SECURITY_AUDIT.md) | No independent external audit yet |

## Keys and wallets

- **Where keys live.** Hive, `qsdmcli` and the [web wallet](WEB_WALLET.md)
  create ML-DSA-87 key pairs on your device. The private key never needs to
  leave it. The web wallet stores it AES-256-GCM encrypted under a key derived
  from your passphrase (PBKDF2-SHA-256, 600,000 iterations).
- **Backups.** A recovery phrase and an encrypted JSON keystore are both
  supported; see [Wallet recovery](WALLET_RECOVERY.md). The optional
  [QSDM Account](QSDM_ACCOUNT.md) can hold an encrypted backup. Losing both the
  phrase and the keystore means losing the wallet; nobody can reset it.
- **What can still go wrong.** Malware on your device, a weak passphrase, or a
  tampered copy of the web wallet page can steal keys. Install Hive only from
  the [verified downloads](DOWNLOADS_AND_VERIFICATION.md).

## Transactions

- Wallet transfers are signed with ML-DSA-87 and submitted through
  `POST /api/v1/wallet/submit-signed`. The node checks the signature, the
  sender's nonce and the balance before accepting the transfer; see
  [Signed transfers](V040_WALLET_SEND_DESIGN.md) and
  [Replay protection](V041_REPLAY_PROTECTION_DESIGN.md).
- ML-DSA-87 is a NIST-standardised post-quantum signature scheme (security
  category 5). It is designed to resist known quantum attacks on signatures.
  It does not make the rest of the system "quantum-proof": transport (TLS),
  consensus messages and Ed25519-signed peer traffic use classical
  cryptography.
- The generic peer-to-peer transaction verifier still accepts Ed25519
  signatures. Wallets made by QSDM software do not produce them.

## Block production and consensus

- Today one block producer creates every block, with a backup node that follows
  the chain. The status API reports `peers: 0`.
- That producer can, in principle, delay or leave out transactions. It cannot
  forge your ML-DSA-87 signature, so it cannot spend from your wallet.
- Signed consensus messages are supported in the node but are not enforced.
  Independent validators and BFT finality are on the [Roadmap](ROADMAP.md).
- Mainnet needs the gates in [Treasury policy §9](TREASURY_POLICY.md#9-mainnet-release-gates):
  independent validators, the treasury multisig, an official genesis manifest
  and an independent external audit.

## Mining

- Only mining protocol v2 proofs are accepted. Each proof must carry an NVIDIA
  attestation of type `nvidia-cc-v1` (data-center confidential computing) or
  `nvidia-hmac-v1` (consumer GPUs).
- `nvidia-hmac-v1` binds a GPU to an enrolled operator through a shared key and
  a 10 CELL bond, which can be paid up front or withheld from the first
  rewards. It is an economic control, not cryptographic proof of the hardware.
  New enrollments are paused during recovery. The
  [Mining protocol v2](MINING_PROTOCOL_V2.md) document explains its limits.
- Since October 2026 the node stores accepted proofs and nonces durably, so a
  proof cannot be paid twice after a restart, and it stops producing blocks
  rather than continue after a storage failure. Pending rewards survive
  restarts.
- Mining runs in public windows with per-IP rate limits. Hive 1.4.21 signs
  proofs with the miner's wallet (owner signature) automatically.

## Software and releases

- QSDM Hive checks a signed release manifest before it installs an update.
  Releases since Hive 1.4.21 use the **v2 release key**; the previous key was
  rotated. How to check a download by hand is in
  [Downloads & verification](DOWNLOADS_AND_VERIFICATION.md).
- Source code is public in this repository. The live release name and base
  commit are in `/api/v1/status` (`version`, `git_sha`).

## Public API surface

- Read endpoints (status, blocks, transactions, balances) are public and need
  no key.
- Administrative routes and certificate issuance are not reachable from the
  internet. New account registration is disabled during recovery.
- The operator dashboard is private.
- Details per endpoint are in the [API reference](API_REFERENCE.md).

## Reporting a vulnerability

Please report security issues privately as described in the
[security policy](https://github.com/blackbeardONE/QSDM/blob/main/SECURITY.md).
Do not open a public issue for a vulnerability.
