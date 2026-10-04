# Roadmap

What has to happen before QSDM can leave the pilot stage, in the order the
project plans to do it. Dates are not promised; each step ships when it is
ready and is announced on [Network status](NETWORK_STATUS.md) and in the
[changelog](https://github.com/blackbeardONE/QSDM/blob/main/CHANGELOG.md).

> [!NOTE]
> This page replaces the earlier phase-by-phase roadmap (2024 to September
> 2026). That version is in the repository history.

## Where we are (October 2026)

- Public **pilot network (pre-mainnet)**, single block producer during recovery.
- Chain restarted on 2026-09-27; public NVIDIA mining reopened on 2026-10-02
  with hardened mining admission (durable proof and nonce de-duplication,
  persistent pending rewards, stop-on-storage-failure).
- QSDM Hive 1.4.21 for Windows and Linux, signed with the rotated v2 release key,
  with automatic owner signatures on mining proofs.
- Wallet transactions signed with ML-DSA-87.

## Next

| Step | What it means for you |
|---|---|
| **Reopen mining enrollment** | New NVIDIA GPUs can enroll through Hive again. Today only miners enrolled before the recovery can mine. |
| **ML-DSA-only transactions** | The generic peer-to-peer verifier stops accepting Ed25519 transaction signatures, so every transaction is ML-DSA-87 signed. |
| **Enforce signed consensus** | Consensus messages must carry valid signatures (`signed_consensus_active: true`). |
| **Standby producer** | A second, independently run producer that can take over block production, after signed consensus is enforced. |

## Mainnet gates

Mainnet is declared only after all of these are done (see
[Treasury policy §9](TREASURY_POLICY.md#9-mainnet-release-gates)):

1. Multiple independent validators in production.
2. Signed consensus enforced.
3. The treasury multisig (if the genesis treasury proposal is adopted) and an
   official, published genesis manifest.
4. An **independent external security audit**, with its findings fixed and the
   report published.

Until then, treat balances and history as pilot data.

## How to follow along

- Live state: [Network status](NETWORK_STATUS.md) and `GET https://api.qsdm.tech/api/v1/status`.
- Code and discussion: [github.com/blackbeardONE/QSDM](https://github.com/blackbeardONE/QSDM).
