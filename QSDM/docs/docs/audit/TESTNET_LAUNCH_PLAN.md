# Incentivized Testnet Launch Plan — `mining-05`

> **Status:** Operational plan for the QSDM incentivized testnet
> launch. This is the wall-clock-blocking document for audit row
> `mining-05`. Most of the in-tree technical readiness work is
> already done (reference miner is functional and documented in
> `MINER_QUICKSTART.md`); what remains is operational
> infrastructure + announcement.
>
> **Audit row:** `mining-05` (severity: medium, category: mining_audit).
> Once the testnet is live and at least the first reward epoch has
> closed cleanly, the audit row moves from `pending` to `passed`.
>
> **Distribution:** internal QSDM-project use; the public-facing
> announcement materials referenced below will be drafted separately
> from this plan and routed through the project's communications
> review before publishing.

---

## 1. Why an incentivized testnet

The reference CPU miner (`cmd/qsdmminer`) is functional and
documented, but it has only been exercised by QSDM contributors. Two
things only an incentivized testnet can produce:

1. **Real-miner attack data.** External miners running unmodified
   binaries against unmodified validators produce a far broader
   profile of edge cases than internal testing. Audit row `mining-01`
   (the external auditor) covers the spec ↔ implementation surface;
   `mining-05` covers the deployment ↔ ecosystem surface.
2. **Operational practice for the validator.** The single BLR1
   validator needs to handle real-world traffic before mainnet: GPU
   hashrate variance, geographically-distributed connections, miners
   gaming the difficulty retarget, faucet abuse, etc.

The testnet is **not** a fundraising event. The testnet token has
no value, is not tradable on any centralised venue (and we will
actively pursue takedowns of any unauthorised listing), and is not
convertible to mainnet CELL on any deterministic schedule.

## 2. Scope

In scope:

- A single QSDM validator running the latest stable release (the
  binary at `https://github.com/blackbeardONE/QSDM/releases/tag/v0.4.x`
  or later).
- A faucet endpoint that hands out testnet-CELL to anyone who
  requests with a valid identity-check (TBD per §3.4).
- A leaderboard / explorer that shows live mining stats (per-miner
  hashrate, accepted-proof count, rolling-window reward).
- A public Discord / forum for miner support.
- Marketing announcement to the project's existing community
  channels (currently: GitHub repo, project website).

Out of scope:

- The CUDA miner (still gated on Phase 6 of the Major Update).
- Mainnet-style economic incentives. The testnet rewards are
  symbolic; we are not committing to any future conversion ratio
  between testnet-CELL and mainnet-CELL.
- A public bug bounty for the testnet binary. The bug bounty is a
  separate work item (placeholder: audit row `mining-04`); the
  testnet may receive bug reports through the same channel but is
  not gated on that program.

## 3. Pre-launch checklist

### 3.1 Infrastructure readiness

| Item | Status (as of plan-author date) | Owner | Estimated effort |
|------|--------------------------------|-------|-----------------|
| Validator binary tagged at a known SHA | `v0.4.2` released; intended bump to `v0.4.3` after current audit-row sweep + push | release lead | done |
| Validator running at `api.qsdm.tech` (BLR1) | Live, audited, 95.29% on internal audit checklist | ops | done |
| Public Prometheus dashboard | `qsdm-runbook-*.json` Grafana dashboards exist; need a public-facing variant | ops | 1-2 days |
| Faucet endpoint | Not yet built | ops | 3-5 days |
| Leaderboard UI | Existing dashboard has parts of this; needs a public-only variant | frontend | 5-10 days |
| Status page (uptime / incident history) | Not yet built; could use a hosted service (statuspage.io / similar) | ops | 1-2 days |
| DNS plan for `testnet.qsdm.tech` (separate from `api.qsdm.tech`) | Not yet configured | ops | <1 day |
| Logging / alerting on the testnet validator | Same Prometheus alert rules as `api.qsdm.tech`; needs a separate-FQDN alertmanager route | ops | 1 day |

### 3.2 Software readiness

| Item | Status | Owner | Estimated effort |
|------|--------|-------|-----------------|
| `MINER_QUICKSTART.md` published | yes, in repo | docs | done |
| Pre-built miner binaries for Linux x86-64 | currently a build step; ship as release asset | release lead | 1 day |
| Pre-built miner binaries for Windows x86-64 | currently a build step; ship as release asset | release lead | 1 day |
| Pre-built miner binaries for macOS arm64 | currently a build step; ship as release asset | release lead | 1 day |
| Docker image for the miner | exists; bump tag at testnet launch | release lead | <1 day |
| Reproducible-build manifest published | partially — full reproducibility is post-Phase-6 | release lead | 2-3 days (best-effort attestation only) |
| Reference CLI wallet UX | exists, browser variant exists, mobile is post-mainnet | wallet lead | done |
| Backup / restore tooling in the Blackbeard tools tree | exists locally; not part of release artifacts | wallet lead | done (intentionally not shipped) |

### 3.3 Documentation

| Item | Status | Owner |
|------|--------|-------|
| `MINER_QUICKSTART.md` | done | docs |
| FAQ for testnet participants | not yet written | docs |
| "How to read the leaderboard" doc | not yet written | docs |
| Faucet ToS (no warranty, may rate-limit, etc.) | not yet written | legal/docs |
| Code of conduct for the testnet Discord | not yet written | community |
| Incident-response playbook for the public testnet | leverage existing runbooks; needs a public-summary version | ops |

### 3.4 Faucet ABUSE-resistance

Faucet design is the highest-risk single sub-system. Tactical
considerations:

- **Identity check.** GitHub-account-OAuth-gated is conventional and
  low-friction; supplement with rate-limit-per-IP and per-GitHub-
  account. CAPTCHA is acceptable but degrades UX.
- **Dust amount.** Hand out enough testnet-CELL to test a few
  transactions; not so much that a Sybil farm becomes economically
  worthwhile. Target: 100 testnet-CELL per request, 1 request per
  GitHub account per 24 hours.
- **Address validation.** The faucet must verify that the requested
  address is well-formed before crediting. (Trivial in-protocol
  check.)
- **Monitoring.** Faucet should emit Prometheus metrics covering:
  requests-per-minute, accept-rate, reject-reasons, balance-
  remaining. Alert on (a) balance running low, (b) reject-rate
  spike, (c) request-rate spike.
- **Hard caps.** Daily faucet drain capped at N testnet-CELL,
  refilled by ops the next UTC midnight. Hard cap means a
  successful Sybil attack costs a day's worth of testnet but
  doesn't permanently destabilise the testnet economy.

### 3.5 Public-relations readiness

Not the engineering team's primary concern, but tracked here so it
isn't dropped:

- Announcement blog post on the project's site (currently `qsdm.tech`)
  drafted, reviewed, and queued for publication.
- Social-media plan (which platforms, what cadence, who replies to
  what).
- Influencer / community outreach plan, if any. Project lead's call.
- Press / mailing-list. Project lead's call.

## 4. Launch sequence

T-7 days

- Infrastructure freeze: no validator binary upgrades until T+14
  days unless a critical security fix is required (in which case
  follow the established hot-patch playbook).
- Faucet deployed to `testnet.qsdm.tech` in dry-run mode (returns
  responses but does not actually credit).
- Public-relations announcement scheduled.
- Status page activated.

T-1 day

- Faucet flipped from dry-run to live; pre-seeded with N testnet-CELL.
- Discord opened to early-access list.

T-0 (launch)

- Public announcement posted.
- Faucet open to all GitHub-OAuth'd accounts.
- Leaderboard goes live.
- First incident-response on-call shift begins.

T+1 day

- First post-launch retrospective: faucet abuse-rate, validator
  hashrate distribution, any P0 incidents.

T+7 days

- First reward epoch closes (10 s blocks × 7 days = ~60,480 blocks;
  one full halving cadence is 4 years, but a single difficulty
  retarget covers a useful operational interval).

T+14 days

- Second retrospective. Assess whether the testnet has accumulated
  enough operational data to satisfy `mining-05`.
- Audit row `mining-05` flips to `passed` if (and only if):
  1. The validator has been continuously live for 14 days.
  2. At least 100 distinct miner-identity-keys have submitted
     accepted proofs.
  3. At least one full difficulty retarget cycle has completed
     cleanly (no validator-side intervention needed).
  4. The faucet has handed out at least 100 distinct addresses'
     worth of testnet-CELL without a credentialed-incident.
  5. No outstanding P0 or P1 incidents in the public-facing
     incident-tracker.

## 5. Post-launch operations

The testnet is not a one-shot event. Once live, it runs indefinitely
(or until QSDM publicly announces a sunset). The ops cost is:

- ~1 hour per week of validator-health monitoring (lightweight; the
  existing Prometheus + Grafana stack covers most of this).
- ~1 hour per week of faucet-operations review.
- Ad-hoc Discord support, bounded by what the community can self-
  support after the first 30 days.

The testnet sunsets when:

- Mainnet launches (replacement, not retirement).
- A protocol-breaking change requires a fresh chain (replacement
  with `testnet-2`).
- The project decides the testnet is no longer providing value.

## 6. Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Faucet drain by Sybil | medium | low (testnet has no value) | identity-check + daily caps |
| Validator outage during launch week | low | medium (PR hit) | rehearse failover; have a hot-standby on a separate cloud region |
| Difficulty retarget instability | low | medium (chain stalls or wildly oscillates) | the retarget maths is integer-only and unit-tested in `pkg/mining/difficulty_test.go`; rehearse on a fresh local chain before launch |
| Discord raid / coordinated abuse | medium | low | community moderators + rate-limited channels + clear COC |
| Press / community criticism over a "fake" token | low | medium | clear messaging that testnet-CELL has no value; faucet ToS reinforces this |
| Legal challenge alleging the testnet is an unregistered offering | very low | high | the `tok-01` counsel engagement covers this question; do not launch until counsel has signed off on the testnet-CELL posture |

## 7. Decision points (gate criteria)

QSDM cannot launch until:

- [ ] `tok-01` counsel review at least covers the testnet-CELL question
  (does not need to cover the full mainnet question — testnet can
  proceed once counsel has opined specifically on the testnet
  posture).
- [ ] `mining-01` external audit is at least scheduled (does not
  need to be complete — testnet may launch in parallel with the
  audit window).
- [ ] All §3 checklist items are at least 80% complete (≥4 of 5
  rows under each subheading; faucet must be 100% complete).
- [ ] The infrastructure freeze can be sustained for T-7 to T+14
  (i.e. no scheduled mandatory upgrades during the launch window).

---

## 8. Internal tracking

This plan is the wall-clock-blocking artifact for audit row
`mining-05`. Progress against the §3 checklist is tracked in
`NEXT_STEPS.md` (to be created); on launch, the audit-row Notes
field is updated to reference the launch announcement URL and the
first-week retrospective document.
