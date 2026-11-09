# History

> **Status:** current. Where QSDM came from, from its first commit to the
> public pilot network (pre-mainnet) that runs today. Every network change
> since then is in [Network upgrades](NETWORK_UPGRADES.md) and the
> [changelog](../../../CHANGELOG.md).

QSDM began on **10 May 2025** in its first GitHub repository,
[github.com/BbOldaccount/QSDM](https://github.com/BbOldaccount/QSDM), which is
no longer updated. This repository took over on 22 April 2026.

## Timeline

| Date (UTC) | What happened |
|---|---|
| 2025-05-10 | First commit of QSDM, the *Quantum-Secure Dynamic Mesh Ledger*, in [github.com/BbOldaccount/QSDM](https://github.com/BbOldaccount/QSDM): "a non-AI, decentralized electronic cash system designed for quantum resistance and hardware-agnostic operation", planned in three phases. |
| 2025-05-11 | Phase 1, in Go: **Proof-of-Entanglement** consensus, which checks each transaction against parent cells; **CRYSTALS-Dilithium** signatures, the scheme NIST standardised as ML-DSA (FIPS 204); libp2p networking; SQLite storage. |
| 2025-05-16 to 05-18 | Phases 2 and 3: dynamic submeshes, a WASM SDK, governance voting, 3D mesh validation with CUDA, quarantines and a reputation system. |
| 2025-12-13 | Last commit to the first repository. A larger version was built under the name **QSDM+**; its repository is no longer online. |
| 2026-04-22 | **The Major Update.** QSDM+ took back the name QSDM, its coin became **Cell (CELL)**, and the code moved to this repository ([6bc7c5d](https://github.com/blackbeardONE/QSDM/commit/6bc7c5d), "QSDM: initial public import"). The plan is kept in [Major Update (executed)](history/MAJOR_UPDATE_EXECUTED.md), and the renames in [Rebrand notes](REBRAND_NOTES.md). |
| 2026-05-06 23:37 | Block 0 of the public pilot chain that runs today. |
| 2026-05-11 to 05-19 | Releases v0.3.0 to v0.4.3: the first tagged releases as QSDM, and signed transfers from self-custody wallets (v0.4.0). |
| 2026-09-22 to 09-27 | The chain was stopped, then restarted with the same release and a single block producer. Hardened mining code ran as a one-miner canary from 26 September. |
| 2026-10-02 | Public mining reopened, in time-boxed windows. |
| 2026-10-11 | Proof-of-Entanglement enforced from block 816,600: every CELL transfer names 2 to 10 transactions the chain already holds. |
| 2026-10-12 | Signed consensus enforced from block 827,000. |
| 2026-10-16 | Ed25519 transactions retired: only ML-DSA-87 signatures reach the chain. |
| 2026-10-19 | Mining open to every enrolled miner; [Genesis Policy](GENESIS_POLICY_v1.1.md) v1.0 (every CELL comes from mining); anyone can [run a full node](RUN_A_FULL_NODE.md). |
| 2026-11-02 | The producer accepts peer-to-peer connections from the internet. |
| 2026-11-06 | Release rc1106: the producer keeps checked block signatures on disk, and runs in about 4.5 GB instead of 6.6 GB. |
| 2026-11-07 | Release rc1107: the producer starts faster, and a full node needs 6 GB of RAM instead of 10 to 12 (full-node release fullnode-rc1107). |
| 2026-11-08 | Release rc1108: the producer's memory stays flat between restarts; full nodes get the same with fullnode-rc1108. |
| 2026-11-08 | A second node: the project's full node in Japan verifies every block, set up with the one-command installer. |

## What has not changed since 2025

- **Proof-of-Entanglement.** The code of 11 May 2025 checked a transaction
  against two parent cells. Since block 816,600 every CELL transfer must name
  2 to 10 transactions the chain already holds, inside the bytes its sender
  signs. See [Proof-of-Entanglement](PROOF_OF_ENTANGLEMENT.md).
- **Post-quantum signatures.** The first code signed with CRYSTALS-Dilithium.
  Wallets and blocks now sign with ML-DSA-87 (NIST FIPS 204), its
  standardised form.

## Where the dates come from

- The first repository's Git history: 12 commits, from 2025-05-10 to
  2025-12-13.
- This repository's Git history, from 6bc7c5d on 2026-04-22.
- Block 0's timestamp, from
  `GET https://api.qsdm.tech/api/v1/chain/blocks?from=0&to=0`.
- The heights in [Network upgrades](NETWORK_UPGRADES.md).

Some older documents in this repository carried dates from 2023 and 2024.
They were wrong, because no QSDM code existed before 10 May 2025, and were
corrected on 2026-11-06.
