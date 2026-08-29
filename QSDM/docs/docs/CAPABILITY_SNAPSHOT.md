# Capability Snapshot

Status date: 2026-08-29

This snapshot summarizes the current project files, test evidence, and the local
capability audit. It is an engineering readiness view, not a marketing claim.
The percentage estimates answer one question: how much of each domain is
implemented, tested, and safe to describe as working today?

## Overall

| Scope | Completion | Meaning |
| --- | ---: | --- |
| Current checkout | ~55% | Many user-facing paths work, but consensus, token accounting, and settlement still have hard blockers. |
| Public main / deployed state | ~55% | Main and the VPS are aligned at `238a0e9` for the status/consensus-auth posture fix; deeper consensus and economics work remain incomplete. |

Completion bands:

| Band | Range | Meaning |
| --- | ---: | --- |
| Production | 80-100% | Implemented, tested, monitored, and safe to run for its intended scope. |
| Working | 60-79% | Usable, with known limits or missing broader coverage. |
| Partial | 35-59% | Important pieces exist, but the workflow is not complete enough to call finished. |
| Prototype | 1-34% | Demonstrates intent or shape, but is not yet dependable. |
| Absent | 0% | Not implemented. |

## Domain Scores

| Domain | Completion | Status | Plain reading |
| --- | ---: | --- | --- |
| Ledger core and consensus | 40% | Partial | Blocks, storage, signed gossip, mempool, solo/follower behavior, and public consensus-auth posture reporting work. Multi-validator commit safety, vote enforcement, and final BFT flow remain the main risk. |
| Crypto and wallet | 73% | Working | ML-DSA wallets, keystore JSON, qsdmcli, Hive signing, recovery-enabled wallets, and submit-signed actions are strong. Browser recovery and cross-device wallet sync are not complete. |
| Storage and networking | 73% | Working | SQLite, health endpoints, metrics, rate limiting, trusted-proxy client buckets, libp2p bootstrap, and replay controls are usable. Scylla/FileStorage parity and peer exchange remain incomplete. |
| Mining | 50% | Partial | Mining APIs, console miner path, rejection tracking, CUDA plumbing, challenge flow, and deferred bond logic exist. Full NVIDIA enforcement, fraud proofs, retargeting, and production hashrate evidence still need more proof. |
| Tokenomics, tasks, and governance | 35% | Partial | Task catalog, task actions, staking helpers, emissions, and rewards are present. Conserved integer accounting, governance activation, bridge maturity, and supply invariants remain unfinished. |
| Edge pool and Mother Hive | 46% | Partial | Agent -> Relay -> Mother Hive control, resource caps, receipts, and local workbench exist. Core-enforced leases, escrow settlement, quotas, and public federation are not finished. |
| QSDM Hive desktop | 64% | Working | Hive, wallet management, tasks, extension bridge, updater gate, packaging, miner integration, and Mother Hive UI are usable. Test coverage, generic task runtime, and some lifecycle edges still need tightening. |
| Operator, gateway, and trust | 68% | Working | Trustcheck, home gateway, public audit pages, attester ingest, GPU truth reporting, and per-client proxy quota behavior are working. Deeper consensus safety is still open. |
| Website, SDK, and release | 70% | Working | qsdm.tech, docs, downloads, browser extension packages, SRI checks, Go SDK, and JS SDK are in good shape. Release signing, OpenAPI publication, and CI parity still need work. |

Weighted current estimate: **~55% complete**.

## What Is Solid Today

- CELL wallet creation, import, backup, and local signing through QSDM Hive and qsdmcli.
- Browser and website wallet linking through QSDM Hive, with per-action approval and origin checks.
- Public qsdm.tech documentation, downloads, privacy/support pages, explorer, trust pages, and SRI linting.
- Local validator, gateway, monitor, attester, and Hive process visibility.
- QSDM Miner and Edge Worker task surfaces in Hive.
- Edge Agent -> Relay -> Mother Hive pairing and local receipt visibility.
- Read-only production trust probes after the rate-limit and GPU-reporting fixes.
- Public `/api/v1/status` consensus-auth posture reporting on the VPS.
- Trusted-proxy rate-limit buckets verified through Caddy without allowing client header spoofing.

## What Must Not Be Overclaimed

- The chain is not yet fully BFT-safe across a real validator set.
- Mining is not proven just because the UI task is running; the release gate must include real NVIDIA utilization and accepted proof evidence.
- Edge pooled resources are schedulable QSDM job capacity, not transparent local operating-system CPU, RAM, or GPU devices.
- Referral, faucet, and edge settlement require funded treasury paths and enforceable Core records before they should be described as automatic public economics.
- Windows Hive releases remain unsigned until a trusted publisher certificate is available. Users must verify published hashes.

## Highest-Value Next Work

1. Close consensus safety: transaction-content commitments, signed vote checks, validator membership agreement, and multi-validator commit tests.
2. Finish conserved CELL accounting: integer dust accounting, supply invariants, and the dust fork activation plan.
3. Harden mining economics: replace public HMAC identity, prove real GPU work, and strengthen fraud/retarget evidence.
4. Enable enforceable edge settlement: leases, receipts, escrow, quotas, and replay-safe payout rules in QSDM Core.
5. Tighten Hive release quality: Node 22 local install, full Hive test suite, updater smoke tests, extension acceptance tests, and signed release path when a publisher identity is available.
6. Clean stale documentation that still claims old production-ready states from archived phase reports.

## Verification Snapshot

Recent local checks:

| Check | Result |
| --- | --- |
| QSDM Core Go tests | `pkg/api`, `pkg/config`, `pkg/chain`, and `pkg/networking` passed for the consensus-auth status change. |
| Go SDK tests | Passed. |
| JS SDK tests | 29/29 passed. |
| Python tooling tests | 24/24 passed. |
| Production trustcheck | `16/16` passed against `https://api.qsdm.tech` with `--min-attested 2 --check-mining-path`; summary was `2 of 3` attested. |
| VPS deploy | `238a0e9` deployed from a Linux CGO build; public and local API both reported tip `628146` during verification. |
| VPS storage check | Fresh logs showed `Using SQLite storage`; no new SQLite/file-storage/panic/fork/divergence errors after restart. |
| Trusted proxy check | Direct VPS bucket: same IP hit `429` at request 31, different IP stayed `200`; public spoofed `X-Real-IP` rotation still hit `429` at request 31. |
| Secret scanner | Passed across tracked files. |
| Website SRI lint | Passed. |
| GitHub Actions pin check | Passed. |
| Hive tests | Not run locally because `node_modules` is incomplete and local Node is below the required 22.12 floor. |

The untracked deep audit file remains the best raw evidence source until it is reviewed and published.
