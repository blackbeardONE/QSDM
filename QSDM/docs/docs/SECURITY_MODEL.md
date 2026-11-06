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
| Wallet transactions | Signed with **ML-DSA-87** (NIST FIPS 204, Cloudflare CIRCL); nonces stop replays; transfers must name committed parents (Proof-of-Entanglement, from block 816,600) | Ed25519 transaction signatures are refused (`ed25519_tx_accepted: false`). CELL stream session keys are still Ed25519 until a stream retirement height is set |
| Block production | One producer runs the chain; every block is stored and served publicly | You trust that producer to include valid transactions and not to censor or reorder them |
| Consensus | **Signed consensus enforced from block 827,000** (`signed_consensus_active: true`): every BFT vote and consensus message must carry a valid ML-DSA signature; unsigned consensus traffic is refused | There is only one producer and no independent validators yet, so there is no multi-validator BFT today |
| Mining | Enrollment needs a signed enrollment and a 10 CELL bond; proofs carry NVIDIA attestation; duplicate proofs and nonces are rejected durably | Consumer-GPU attestation (`nvidia-hmac-v1`) is an economic control tied to the enrollment and its bond, not a hardware proof |
| Software downloads | QSDM Hive releases are signed with the v2 release key and checked by Hive before updating | You trust the release key holder; the previous key was rotated in 2026 |
| Public API | Read endpoints are public; admin and certificate routes are not exposed; write routes are limited during recovery | Rate limits and nginx rules run on infrastructure operated by the project |
| Review | An internal, self-maintained [security review](SECURITY_AUDIT.md); an open [community review](COMMUNITY_REVIEW.md) with a [bug bounty](BUG_BOUNTY.md) | No independent audit has been completed; community review is not an audit |

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
  It does not make the rest of the system "quantum-proof": transport (TLS)
  and some peer-to-peer traffic still use classical cryptography.
- Ed25519 transaction signatures are retired: since 16 October 2026 the
  generic peer-to-peer transaction-gossip verifier refuses them, and
  ML-DSA-87 is the only accepted transaction signature (status API
  `ed25519_tx_accepted: false`). Wallets, task actions and consensus use
  ML-DSA. CELL stream session keys, which sign stream usage receipts, are
  still Ed25519 until a stream retirement height
  (`stream_ed25519_retirement_height`) is set; see the [Roadmap](ROADMAP.md).
- From block 816,600 every transfer must name committed parent transactions
  ([Proof-of-Entanglement](PROOF_OF_ENTANGLEMENT.md)). The parents are part of
  the signed bytes.

## Block production and consensus

- Today one block producer creates every block, with a backup node that follows
  the chain. Since 2 November 2026 its peer-to-peer port is public, with
  connection, stream and block-request limits; the status API's `peers` counts
  the connected peers. The code of those limits is in this repository: the
  block-request budget is always on, and the connection, stream and gossip
  limits are opt-in for other nodes ([P2P limits](P2P_LIMITS.md)). A node
  that only follows a known producer can run in private peer-to-peer mode,
  which accepts and dials only listed peer IDs over private addresses
  ([Private P2P mode](PRIVATE_P2P.md)).
- On 3 November 2026 fuzzing found that one block with a null transaction entry
  stopped a node before any signature was checked. The producer's peer-to-peer
  port was closed while a fixed build went in, and it reopened on 4 November.
  The decoders that read peer messages, public API request bodies, mining
  submissions and snapshot files are fuzzed every night, and so are the
  producer transition rules (55 targets in
  `.github/workflows/go-fuzz-nightly.yml`).
- A new full node can start from a verified snapshot instead of replaying the
  chain from genesis. It trusts only a checkpoint its operator pins (height,
  block hash and state root) and the producer's ML-DSA-87 signature on that
  block, and it rebuilds the state root before it installs anything
  ([Verified snapshot bootstrap](SNAPSHOT_BOOTSTRAP.md)).
- A second producer hand-over, for example to a standby producer, is written
  as a version 2 producer transition table. Each hand-over is pinned to the
  last block of the previous producer, so it cannot be scheduled in advance,
  and only the final producer can produce
  ([Producer transition](PRODUCER_TRANSITION.md)).
- Bridge events on the `qsdm-bridge` topic are applied only when they are
  signed by a relayer key on the operator's allowlist, which is empty by
  default. See [Bridge relay authentication](BRIDGE_RELAY_AUTH.md).
- That producer can, in principle, delay or leave out transactions. It cannot
  forge your ML-DSA-87 signature, so it cannot spend from your wallet.
- Since block 827,000 (12 October 2026, 18:11 UTC) every BFT vote and
  consensus message must carry a valid ML-DSA signature, and unsigned
  consensus traffic is refused. This stops forged consensus messages; it does
  not add independent validators. Today the one producer signs them all.
- Block production by miners, in stages, is the next step on the
  [Roadmap](ROADMAP.md) (decision of 19 October 2026). Multi-validator BFT is
  in the code but no longer planned. The full list of rules in force is in
  [Consensus rules](CONSENSUS_RULES.md).
- Mainnet needs the [mainnet gates](ROADMAP.md#mainnet-gates) and
  [Treasury policy §9](TREASURY_POLICY.md#9-mainnet-release-gates):
  decentralised block production, a published migration manifest
  ([Genesis Policy v1.0](GENESIS_POLICY_v1.0.md)) and a published security
  review with its findings fixed. No independent audit has been completed.

## Mining

- Only mining protocol v2 proofs are accepted. Each proof must carry an NVIDIA
  attestation of type `nvidia-cc-v1` (data-center confidential computing) or
  `nvidia-hmac-v1` (consumer GPUs).
- `nvidia-hmac-v1` binds a GPU to an enrolled operator through a shared key and
  a 10 CELL bond, which can be paid up front or withheld from the first
  rewards. It is an economic control, not cryptographic proof of the hardware.
  The [Mining protocol v2](MINING_PROTOCOL_V2.md) document explains its limits.
- Since October 2026 the node stores accepted proofs and nonces durably, so a
  proof cannot be paid twice after a restart, and it stops producing blocks
  rather than continue after a storage failure. Pending rewards survive
  restarts.
- Mining is open to every enrolled miner (since 19 October 2026) in 7-day
  (168-hour) windows that renew automatically, with caps per owner per 24-hour
  epoch and per-IP rate limits. Every proof must carry the enrollment owner's
  signature; Hive 1.4.22 adds it automatically.

## Software and releases

- QSDM Hive checks a signed release manifest before it installs an update.
  Releases since Hive 1.4.21 use the **v2 release key**; the previous key was
  rotated. How to check a download by hand is in
  [Downloads & verification](DOWNLOADS_AND_VERIFICATION.md).
- From the first Hive release after 1.4.22, Hive keeps working through a
  qsdm.tech outage with the last signed release envelope it verified, until
  that envelope's own expiry. It ignores an envelope issued before one it has
  already verified, so an old envelope cannot roll it back. See
  [QSDM-native release signing](QSDM_NATIVE_RELEASE_SIGNING.md#website-outages).
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
Do not open a public issue for a vulnerability. The [bug bounty](BUG_BOUNTY.md)
sets out the scope, the rules (test on a local node, never against other
users' funds or the live producer), the safe harbour and the rewards
(confirmed by the owner on 17 October 2026; 50,000 CELL pool).
