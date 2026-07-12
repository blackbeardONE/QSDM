# QSDM Use Cases

**Last Updated:** July 2026

---

## Overview

**QSDM** (Quantum-Secure Dynamic Mesh) is a post-quantum mesh ledger with native coin **Cell (CELL)**. Validators run PoE + BFT consensus; miners mint CELL with NVIDIA-attested Proof-of-Work. **QSDM Hive** is the Windows/Linux client for wallets, signed tasks, mining, and integrations. This document lists where the stack fits today.

---

## 1. Electronic Cash & Payments

### Primary use case
Decentralized electronic cash with quantum-safe cryptography.

### Characteristics
- Quantum-safe transactions — ML-DSA-87 (NIST FIPS 204)
- Self-custody browser wallet and Hive desktop wallets
- Public receipts and explorer for verification
- Compressed signatures with Zstd

### Applications
- Peer-to-peer CELL transfers
- Micropayments and low-fee submeshes
- Cross-border remittances with long-term signature security

---

## 2. Quantum-Safe Financial Services

### Use case
Services that need long-term security against cryptographically relevant quantum adversaries.

### Why QSDM?
- ML-DSA-87 is NIST FIPS 204
- Explicit node-role split (validators vs miners)
- Transparent audit and trust APIs on the reference node

### Applications
- Digital asset custody
- WASM smart contracts
- DeFi-style protocols that require post-quantum signatures

---

## 3. Consumer Mining & Protocol Emission

### Use case
GPU operators mint CELL under Mining Protocol v2.

### Why QSDM?
- NVIDIA-attested proofs (Turing+)
- On-chain enrollment with slashable bond
- Hive Miner task for consumers; `qsdmminer-console` for operators

### Applications
- Home NVIDIA mining via Hive
- Lab/office mining fleets against a home gateway or public validator
- Emission and slash transparency via public mining APIs

---

## 4. Signed Task Markets & Staking

### Use case
Permissionless task catalogs with fund/stake/start/submit/claim flows.

### Why QSDM?
- Consensus task registry (`qsdm/tasks/v1`)
- Task Studio in Hive publishes signed manifests
- Replay-safe signed action IDs

### Applications
- Wallet-linked verification tasks
- Reward pools with on-chain stake
- Integration tasks (e.g. Sky Fang account link)

---

## 5. Pooled Edge Compute (Mother Hive)

### Use case
Trusted LAN or lab pools of CPU, NVIDIA GPU, and RAM capacity settled in CELL.

### Why QSDM?
- Agent → Relay → Hive (Mother) → Core topology
- Walletless Agents; fixed algorithms only (no remote shell)
- Core-enforced 70% / 15% / 15% settlement split

### Applications
- Computer laboratories
- Office batch jobs via Application Compute Gateway (`127.0.0.1:7742`)
- Bounded CUDA helper work separate from protocol mining

---

## 6. Game & App Integrations

### Use case
External apps bind accounts to QSDM wallets and pay earn-only CELL rewards.

### Why QSDM?
- Hive local signing and ownership proofs
- Anti-pay-to-win stance for combat power
- Public wallet and receipt surfaces for verification

### Applications
- **Sky Fang Online** — play-to-earn MMORPG wallet link
- Future games/apps via HTTP API and SDKs

---

## 7. Home Validator Operation

### Use case
Run a CPU validator at home without exposing wallet/admin APIs.

### Why QSDM?
- `qsdm-home-gateway` narrow public allowlist via outbound relay
- Local GUI + tray monitor for health
- Loopback-bound Core with optional public mining/status only

### Applications
- Bootstrap peers for Phase 4 testnet
- Private validators that still accept mining work
- Operator hygiene with tray status snapshots

---

## 8. Governance & Submesh Policy

### Use case
Token-weighted parameter and submesh rule changes without black-box automation.

### Why QSDM?
- Snapshot-style governance voting
- Explicit submesh fee/priority/geotag profiles
- Quarantine and reputation with staked deposits

### Applications
- DAO-style parameter votes
- Fee-market submeshes
- Community quarantine decisions

---

## 9. Cross-Chain Bridge Flows

### Use case
Atomic lock / redeem / refund swaps with audited secret handling.

### Why QSDM?
- Bridge package with expiry and fee integrity checks
- Incident runbooks for contracts/bridge events

### Applications
- CELL-adjacent interoperability experiments
- Future CELL-denominated bridge collateral (see tokenomics)

---

## 10. Transparency & Operator Assurance

### Use case
Public proof that a deployment matches its claimed security posture.

### Why QSDM?
- Public audit checklist and badge
- Trust attestation summary/recent feeds
- Explorer, chain status board, security.txt

### Applications
- External auditors and researchers
- Operator onboarding without trusting marketing copy
- CI trust probes (`trustcheck`)

---

## Getting started

### Consumers
1. [Download QSDM Hive](https://qsdm.tech/download.html)
2. [Hive guide](https://qsdm.tech/docs/#/qsdm-hive)
3. [CELL tokenomics](https://qsdm.tech/docs/#/cell-tokenomics)

### Operators
1. [Operator guide](https://qsdm.tech/docs/#/operator-guide)
2. [Validator quickstart](https://qsdm.tech/docs/#/validator-quickstart) or [Miner quickstart](https://qsdm.tech/docs/#/miner-quickstart)
3. [Home gateway](https://qsdm.tech/docs/#/home-gateway) for home public surfaces

### Developers
1. [API reference](https://qsdm.tech/docs/#/api-reference)
2. [Web wallet](https://qsdm.tech/docs/#/web-wallet)
3. SDKs under `QSDM/source/sdk/`

---

## Summary

QSDM fits applications that need:

- Post-quantum signatures (ML-DSA-87)
- Native CELL balances, stake, and mining emission
- Consumer Hive path plus operator Core path
- Signed tasks and optional edge compute pools
- Public audit/trust surfaces

**Primary use cases today:** electronic cash, consumer/operator mining, signed task markets, Mother Hive edge pools, game wallet linking, home validators, governance, bridge experiments, and transparency tooling.
