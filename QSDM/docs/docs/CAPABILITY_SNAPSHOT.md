# Capability Snapshot

Status date: 2026-09-09

This is an engineering readiness view, not a marketing claim. The percentages
estimate how much of each area is implemented, tested, and safe to describe as
working for its intended scope. They do not turn incomplete consensus or
economic mechanisms into production guarantees.

## Current Scope

| Scope | Completion | Plain reading |
| --- | ---: | --- |
| Public `main` | ~60% | QSDM has a usable single-producer network, Hive desktop client, wallets, task surfaces, trust reporting, and operator tooling. It is not yet a production-ready independent multi-validator BFT network or a fully conserved CELL economy. |
| Strict validator setup | ~80% | A validator started in strict mode must pin one or more allowed producer IDs. The deployment scripts now fail before provisioning when those IDs are missing, rather than leaving an unprotected empty allowlist. |

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
| Ledger core and consensus | 48% | Partial | Blocks, storage, signed gossip support, mempool, single-producer mode, follower append, producer allowlisting, task-action signatures, and transaction-content roots exist. Dynamic validator membership, peer-vote origination, proposer rotation, multi-node BFT commit, and failover are not production-finished. |
| Crypto and wallet | 74% | Working | ML-DSA wallets, keystore JSON, Hive and CLI signing, wallet import, recovery-enabled new wallets, website account login, and browser-extension linking work. Wallet custody remains local-first rather than cloud-synced. |
| Storage and networking | 74% | Working | SQLite, health and readiness endpoints, metrics, rate limits, static bootstrap, local discovery, replay controls, and the home gateway are usable. Scylla parity, peer exchange, and multi-site validator staging still need hardening. |
| Mining | 55% | Partial | Console mining, Hive task control, NVIDIA visibility, challenge flow, rejection tracking, deferred stake deduction, and NGC transparency are implemented. The public-HMAC enrollment model weakens hardware identity, so earning must be judged from accepted proofs rather than a running task switch. |
| Tokenomics, tasks, and governance | 41% | Partial | Task catalog, signed task actions, staking helpers, rewards, treasury documentation, faucet/referral paths, and self-stake separation exist. Conserved integer accounting, dust-fork activation, governance rollout, and enforceable settlement payouts remain unfinished. |
| Edge pool and Mother Hive | 50% | Partial | Agent-to-Relay-to-Mother-Hive pairing, caps, receipts, resource dashboards, and local workbench exist. They are schedulable QSDM job capacity, not transparent operating-system CPU, RAM, or GPU devices. Core-enforced leases, escrow settlement, quotas, and internet federation remain roadmap work. |
| QSDM Hive desktop | 68% | Working | Hive task UI, wallet management, extension bridge, updater gate, packaging, miner integration, Mother Hive UI, and Windows/Linux flows are usable. Test coverage and lifecycle edges need further work before calling the client mature. |
| Operator, gateway, and trust | 75% | Working | Trustcheck, home gateway, public audit surfaces, attester ingest, GPU truth reporting, trusted-proxy configuration, and readiness waits are implemented. Their availability still depends on a correctly deployed public operator. |
| Website, SDK, docs, and release | 72% | Working | qsdm.tech, docs, downloads, browser-extension packages, SRI checks, Go SDK, JS SDK, release policy, and privacy/support pages are in place. Windows publisher signing, npm publication, stale archive cleanup, and CI parity remain open. |

Weighted current estimate: **~60% complete on public `main`**.

## Feature Scorecard

| Feature | Completion | Working now | Main limitation |
| --- | ---: | --- | --- |
| Single-producer QSDM Network | 78% | Yes | It is intentionally one configured producer plus followers, not a decentralized validator set. |
| External block append authorization | 82% | Yes in strict configuration | Strict validator setup requires an explicit producer allowlist. Non-strict nodes must also configure an allowlist before they should be treated as protected. |
| Signed consensus messages | 65% | Supported | Fleet-wide enforcement is a coordinated rollout step; `require_signed_votes` remains off by default for compatibility. |
| Transaction-content root | 70% | Configured for a future activation in deployment templates | It changes block hashes, so every validator must use the same reviewed activation height. |
| Task-action signatures | 78% | Configured for a future activation in deployment templates | Historical unsigned actions remain valid below the shared activation height. |
| POL finality guard | 70% | Local sealed blocks are recorded before synthetic proof work | This prevents an authenticated proof-generation refusal from reopening anchored local finality; it does not complete multi-validator BFT. |
| Hive consumer app | 68% | Yes | Release signing and complete automated desktop coverage remain thin. |
| Hive browser wallet extension | 62% | Yes | Store review, browser-specific packaging, and account-dashboard polish remain. |
| QSDM Account web login | 58% | Yes | Email/Telegram login exists, but account recovery and role management need hardening. |
| QSDM Miner | 55% | Yes | GPU utilization and accepted-proof evidence must be monitored per machine. |
| Edge Agent / Edge Control | 55% | Yes locally | Internet federation and settlement are not yet enforceable by Core. |
| Mother Hive pooled resources | 50% | Yes as QSDM job capacity | It does not make remote hardware appear as local OS devices. |
| Sky Fang integration | 72% | Yes | Rewards depend on reliable Sky Fang link verification and task economics. |
| Treasury / faucet / referral economics | 45% | Partial | Needs funded wallets, payout policy, abuse caps, and auditable Core records. |
| Public trust and explorer surfaces | 76% | Yes when deployed | More checks must validate real behavior, not only response shape. |

## What Is Solid Today

- CELL wallet creation, import, backup, and local signing through QSDM Hive and
  `qsdmcli`.
- Browser and website wallet linking through QSDM Hive, with per-action approval
  and origin checks.
- Public `qsdm.tech` documentation, downloads, privacy/support pages, explorer,
  trust pages, and SRI linting.
- Local validator, gateway, monitor, attester, and Hive process visibility.
- QSDM Miner and Edge Worker task surfaces in Hive.
- Edge Agent-to-Relay-to-Mother-Hive pairing and local receipt visibility.
- Strict validator deployment checks that require explicit producer pinning.
- Public status and trust-reporting surfaces when an operator deploys them.

## What Must Not Be Overclaimed

- The chain is not fully BFT-safe across a real dynamic validator set.
- The current public topology is one configured producer plus followers, not
  VPS-independent automatic consensus or failover.
- Signed-vote enforcement is supported but not enabled by default. It must be
  activated at one reviewed future height across every validator.
- Mining is not proven just because the UI task is running; release evidence
  must include real NVIDIA utilization and accepted proof records.
- Edge pooled resources are schedulable QSDM job capacity, not transparent local
  operating-system CPU, RAM, or GPU devices.
- Referral, faucet, and edge settlement require funded treasury paths and
  enforceable Core records before they can be described as automatic public
  economics.
- Windows Hive releases remain unsigned until a trusted publisher certificate is
  available. Users must verify published hashes.

## Highest-Value Next Work

1. Run a two-node staging rehearsal: producer rotation, follower agreement,
   signed gossip behavior, chain catch-up, and failover.
2. Use that rehearsal to choose and validate one shared signed-consensus
   activation height before enabling `require_signed_votes` anywhere.
3. Complete conserved CELL accounting: integer dust accounting, supply
   invariants, and the dust-fork activation plan.
4. Replace public-HMAC mining identity with a private, attestable hardware or
   operator credential path.
5. Enable enforceable edge settlement: leases, receipts, escrow, quotas, and
   replay-safe payout rules in QSDM Core.
6. Tighten Hive release quality: Node 22 local install, full Hive test suite,
   updater smoke tests, extension acceptance tests, and a signed release path
   when a publisher identity is available.
7. Reconcile stale archive reports before publishing a broader capability audit.

## Verification Snapshot

Recent checks behind this snapshot:

| Check | Result |
| --- | --- |
| Strict validator configuration | `go test ./pkg/config` passed after the strict producer-allowlist guard was added. |
| Deployment scripts | `deploy/scripts/test_authorized_block_producers.sh` passed for empty, duplicate, malformed, and explicit producer inputs. |
| Producer authorization | `qsdm.yaml` pins a reference producer for the checked-in development profile; strict production deployments require operators to provide their own reviewed pin set. |
| Signed consensus defaults | `require_signed_votes` remains false unless operators schedule an agreed non-zero activation height. |
| Transaction and task-action activation templates | New deployment scripts default their activation settings to height `625000`; operators must verify that height against every participating chain before relying on it. |
| Trust surfaces | Code and tests exist for public status, attestation, and mining-path checks; reachability is verified separately against a deployed operator. |
| Secret scanner posture | Tracked files are guarded; local/private scripts are excluded by policy. |

The deep audit remains useful raw evidence, but it must be reconciled with the
current source and deployment configuration before publication.
