# Capability Snapshot

Status date: 2026-08-31

This snapshot summarizes the current project files, test evidence, and the
known production boundary. It is an engineering readiness view, not a marketing
claim. The percentage estimates answer one question: how much of each domain is
implemented, tested, and safe to describe as working today?

## Scope

| Scope | Revision | Completion | Plain reading |
| --- | --- | ---: | --- |
| Public `main` | `591880f` | ~61% | The single-producer QSDM Network, Hive, wallet, trust, and task surfaces are usable. Several safety gates are implemented or scheduled, but full multi-validator consensus and settlement economics are still incomplete. |
| Ready PRs | #86 `c216b5d`, #87 `82bb806`, #88 `c693998` | ~63% if merged | These close strict producer-allowlist startup posture, add a trustcheck gate for signed-consensus posture, and make POL proof simulation fail closed. They are green and clean, but should not be counted as shipped until merged. |

Completion bands:

| Band | Range | Meaning |
| --- | ---: | --- |
| Production | 80-100% | Implemented, tested, monitored, and safe to run for its intended scope. |
| Working | 60-79% | Usable, with known limits or missing broader coverage. |
| Partial | 35-59% | Important pieces exist, but the workflow is not complete enough to call finished. |
| Prototype | 1-34% | Demonstrates intent or shape, but is not yet dependable. |
| Absent | 0% | Not implemented. |

## Domain Scores

| Domain | `main` | With ready PRs | Status | Plain reading |
| --- | ---: | ---: | --- | --- |
| Ledger core and consensus | 48% | 53% | Partial | Blocks, storage, signed gossip support, mempool, solo producer mode, follower append, block-producer allowlisting, task-action signatures, and transaction-content roots exist. The remaining boundary is not mysterious: dynamic validator membership, peer-vote origination, proposer rotation, and multi-node BFT commit/failover are not production-finished. |
| Crypto and wallet | 74% | 74% | Working | ML-DSA wallets, keystore JSON, Hive signing, qsdmcli signing, recovery-enabled new wallets, wallet import, website account login, and browser-extension linking are real. Old wallets still need migration UX for recovery phrases, and cross-device custody remains local-first rather than cloud-synced. |
| Storage and networking | 74% | 74% | Working | SQLite, health/readiness, metrics, rate limits, trusted-proxy buckets, static bootstrap, mDNS/local discovery, replay controls, and home gateway are usable. Scylla parity, peer exchange, and multi-site validator staging still need hardening. |
| Mining | 55% | 55% | Partial | Console mining, Hive task control, NVIDIA visibility, challenge flow, rejection tracking, deferred stake deduction, and NGC transparency are in place. The public-HMAC enrollment model still weakens hardware identity, and production earning must be judged from accepted proofs, not the task being toggled on. |
| Tokenomics, tasks, and governance | 41% | 41% | Partial | Task catalog, signed task actions, staking helpers, rewards, treasury docs, faucet/referral paths, and self-stake separation exist. Conserved integer accounting, dust-fork activation, governance rollout, and enforceable settlement payouts remain unfinished. |
| Edge pool and Mother Hive | 50% | 50% | Partial | Agent -> Relay -> Mother Hive pairing, caps, receipts, resource dashboards, and local workbench exist. These are schedulable QSDM jobs, not transparent operating-system CPU/RAM/GPU devices. Core-enforced leases, escrow settlement, quotas, and internet federation are still roadmap work. |
| QSDM Hive desktop | 68% | 68% | Working | Hive, task UI, wallet management, extension bridge, updater gate, packaging, miner integration, Mother Hive UI, and Linux/Windows flows are usable. Test coverage and lifecycle edges still need tightening before calling it mature. |
| Operator, gateway, and trust | 74% | 76% | Working | Trustcheck, home gateway, public audit pages, attester ingest, GPU truth reporting, trusted-proxy behavior, and readiness waits are working. PR #87 improves the trust probe so unsigned-consensus compatibility cannot be mistaken for strict signed enforcement. |
| Website, SDK, docs, and release | 72% | 72% | Working | qsdm.tech, docs, downloads, browser-extension packages, SRI checks, Go SDK, JS SDK, release policy, and privacy/support pages are in good shape. Windows publisher signing, npm publication, stale archive cleanup, and CI parity remain open. |

Weighted current estimate: **~61% on public `main`; ~63% after PRs #86-#88
merge.**

## Feature Scorecard

| Feature | Completion | Working now | Main limitation |
| --- | ---: | --- | --- |
| Single-producer QSDM Network | 78% | Yes | It is intentionally one producer plus followers, not a decentralized validator set yet. |
| External block append authorization | 82% | Yes when configured | PR #86 makes strict production refuse startup without a producer allowlist. |
| Signed consensus messages | 65% | Supported | Enforcement is still a coordinated rollout step, not the default live posture. |
| Transaction-content root | 70% | Scheduled at height `625000` | It changes block hashes, so every validator must activate at the same height. |
| Task-action signatures | 78% | Scheduled at height `625000` | Historical unsigned actions remain valid below activation. |
| POL finality guard | 70% | Better with PR #88 | PR #88 prevents proof-generation refusal from reopening anchored finality. |
| Hive consumer app | 68% | Yes | Release signing and full automated desktop tests are still thin. |
| Hive browser wallet extension | 62% | Yes | Store review, browser-specific packaging, and account dashboard polish remain. |
| QSDM Account web login | 58% | Yes | Email/Telegram login exists, but admin/role UX and account recovery need hardening. |
| QSDM Miner | 55% | Yes | GPU utilization and accepted-proof evidence must be monitored per machine. |
| Edge Agent / Edge Control | 55% | Yes locally | Internet federation and settlement are not yet enforceable by Core. |
| Mother Hive pooled resources | 50% | Yes as QSDM job capacity | It does not make remote hardware appear as local OS devices. |
| Sky Fang integration | 72% | Yes | Rewards depend on reliable Sky Fang link verification and task economics. |
| Treasury / faucet / referral economics | 45% | Partial | Needs funded wallets, payout policy, abuse caps, and auditable Core records. |
| Public trust and explorer surfaces | 76% | Yes | More checks must validate real behavior, not only response shape. |

## What Is Solid Today

- CELL wallet creation, import, backup, and local signing through QSDM Hive and
  `qsdmcli`.
- Browser and website wallet linking through QSDM Hive, with per-action approval
  and origin checks.
- Public `qsdm.tech` documentation, downloads, privacy/support pages, explorer,
  trust pages, and SRI linting.
- Local validator, gateway, monitor, attester, and Hive process visibility.
- QSDM Miner and Edge Worker task surfaces in Hive.
- Edge Agent -> Relay -> Mother Hive pairing and local receipt visibility.
- Read-only production trust probes after the rate-limit and GPU-reporting
  fixes.
- Public `/api/v1/status` consensus-auth posture reporting.
- Trusted-proxy rate-limit buckets verified through Caddy without allowing
  client header spoofing.

## What Must Not Be Overclaimed

- The chain is not yet fully BFT-safe across a real dynamic validator set.
- Mining is not proven just because the UI task is running; the release gate
  must include real NVIDIA utilization and accepted proof evidence.
- Edge pooled resources are schedulable QSDM job capacity, not transparent local
  operating-system CPU, RAM, or GPU devices.
- Referral, faucet, and edge settlement require funded treasury paths and
  enforceable Core records before they should be described as automatic public
  economics.
- Windows Hive releases remain unsigned until a trusted publisher certificate is
  available. Users must verify published hashes.

## Highest-Value Next Work

1. Merge the green consensus-safety PRs #86, #87, and #88 when approved.
2. Run a two-node staging test: producer rotation, follower agreement, signed
   gossip behavior, chain catch-up, and failover.
3. Complete conserved CELL accounting: integer dust accounting, supply
   invariants, and the dust-fork activation plan.
4. Replace public-HMAC mining identity with a private, attestable hardware or
   operator credential path.
5. Enable enforceable edge settlement: leases, receipts, escrow, quotas, and
   replay-safe payout rules in QSDM Core.
6. Tighten Hive release quality: Node 22 local install, full Hive test suite,
   updater smoke tests, extension acceptance tests, and signed release path when
   a publisher identity is available.
7. Clean stale archived phase reports that still claim old production-ready
   states.

## Verification Snapshot

Recent checks behind this snapshot:

| Check | Result |
| --- | --- |
| Public `main` | `591880f` after PR #85. |
| Ready consensus PRs | #86, #87, and #88 are clean with green GitHub checks. |
| PR #88 local tests | `CGO_ENABLED=0 go test ./pkg/networking ./pkg/chain` passed before push. |
| Transaction-content root config | `QSDM/qsdm.yaml`, `install-ubuntu-vps.sh`, and `bring-up-validator.sh` set height `625000`. |
| Signed consensus rollout doc | Documents one-producer-plus-followers as the current boundary. |
| Secret scanner posture | Tracked files are guarded; local/private scripts are excluded by policy. |

The untracked deep audit file remains useful raw evidence, but it is stale and
should not be published until its critical-status table is reconciled with the
current source and ready PRs.
