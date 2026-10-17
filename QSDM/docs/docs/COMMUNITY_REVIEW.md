# Community security review

> [!IMPORTANT]
> **This is not an independent professional audit.** QSDM's security review is
> open and community-led: anyone may review the code, rebuild the release and
> report what they find. The people who take part are not hired, vetted or
> supervised by an audit firm, they do not cover the scope systematically, and
> nobody signs off on the result. **No independent security audit of QSDM has
> been completed.** A professional audit remains welcome; see §9.

On 17 October 2026 the owner decided to run an open, community-led review with
a [bug bounty](BUG_BOUNTY.md) in place of engaging a paid external audit firm,
and to keep the single block producer for now. This page publishes the review
scope, shows how to rebuild the exact binary the network runs, gives a
checklist per component, explains how findings are handled, and keeps a public
review log.

## 1. What this program is

| | |
|---|---|
| What it is | A published scope, a reproducible build, per-component checklists, a private reporting channel, a bug bounty and a public log of every review and finding |
| What it is not | An audit. There is no engagement letter, no guaranteed coverage, no auditor opinion and no attestation by a firm |
| Who can take part | Anyone. Reviews can be anonymous |
| How to report a vulnerability | Privately, as described in the [bug bounty](BUG_BOUNTY.md#1-how-to-report) and [`SECURITY.md`](https://github.com/blackbeardONE/QSDM/blob/main/SECURITY.md) |
| How to publish review notes | §6 |
| Rewards | CELL tiers up to 20,000 CELL and a 50,000 CELL pool, confirmed by the owner on 17 October 2026; paid from the owner's own mined holdings by signed transfers and never minted ([bug bounty §5](BUG_BOUNTY.md#5-rewards)) |

## 2. Review scope

The scope is the one prepared for an external audit in October 2026, reused
unchanged for community review.

### 2.1 Review target

| | |
|---|---|
| Release | `hardened-legacy-20261016-d7ffcd4-hl2-edge-go1269-ids-pqe` (proposed) |
| Source commit | `8d7b159` (proposed review commit) |
| Go module | `src/` in the review export (`QSDM/source/` in the public repository) |
| Size | About 145,000 lines of non-test Go in total; the core review set (ranks 1–7 below) is about 45,000–55,000 lines |

> [!NOTE]
> The public repository's `QSDM/source/` tree is older than the review commit,
> and the release build tooling is not in it yet. Publishing the review commit
> together with its build and follower-release tooling is the first entry
> (R0) in the [review log](#7-review-log). Until that entry is marked done,
> review the public tree and say which commit you reviewed.

### 2.2 Components, ranked by risk

| Rank | Component | Why it matters | Where to start (Go packages and files) |
|---|---|---|---|
| 1 | **Consensus and block authentication**: BFT signing; block, certificate and evidence signatures; producer transition and allowlist; height gates | Forgery or fork of the chain | `pkg/chain`: `bft_sig.go`, `bft_signer_key.go`, `bft_executor.go` (`checkInboundAuth`), `block_sig.go`, `pol_sig.go`, `evidence*.go`, `producer_transition.go`, `block_external_authz.go`; `pkg/producerpolicy/transition.go`; wiring in `cmd/qsdm/main.go` |
| 2 | **Wallet and transaction signatures** (ML-DSA-87), keystore, recovery phrase, account login | Theft, signature malleability, key derivation | `pkg/crypto/dilithium_circl.go`; `pkg/wallet/wallet.go` (`VerifyTransactionData`); `pkg/chain/txsig.go`, `wallet_transfer.go`; `pkg/keystore`; `pkg/walletrecovery`; `pkg/account/wallet_login.go` |
| 3 | **Mining admission and rewards**: guard, durable proof store, payout ledger, invariants I1–I7, crash recovery, emission and supply | Inflation, double payment, liveness | `internal/legacymining` (`guard.go`: `Admit`, `Precheck`, `Freeze`; `guard_owner.go`; `ledger.go`: `PreSeal`, `OnDurableBlock`, `checkRewardsLocked`, `checkFunderLocked`); `internal/miningsvc`; `internal/hl1tail`; `cmd/qsdm/hl1_persist.go`, `hl1_replay.go`, `hl1_receipts_regen.go`, `failstop.go`; `pkg/chain/mining_reward.go`, `emission.go`, `dustfork.go` |
| 4 | **Mining protocol v2, attestation and enrollment admission** | Sybil enrollment, forged attestation, bond bypass, state-root divergence | `pkg/mining/enrollment` (`envelope.go`, `validate.go`, `admit.go`); `pkg/chain/enrollment_admission.go`; `pkg/api/follower_readonly.go`; `cmd/qsdm/hl2_enrollment.go`; `pkg/mining/pow/v2`; `pkg/mining/attest`; `pkg/mining/slashing` |
| 5 | **Edge relay settlement** and the relay-only rule | Payout theft or duplication, redirection of the ecosystem share | `pkg/edgepool` (`settlement.go`, `protocol.go`, `coordinator.go`); `pkg/chain/task_state.go`, `edge_relay_only.go`; `cmd/qsdm-edge-agent`, `cmd/qsdm-edge-control`, `cmd/qsdm-relay` |
| 6 | **Proof-of-Entanglement** parent rules | Consensus validity, history-lookup DoS | `pkg/poe/poe.go`; `pkg/chain/poe.go` (`checkWalletTransferParents`, `verifyBlockPoE`) |
| 7 | **Task actions and reserved IDs** | Hijacking built-in tasks, reward-ID spoofing | `pkg/chain/task_action_auth.go`, `task_catalog.go`, `wallet_transfer_admission.go`; `pkg/api/handlers_task_actions.go` |
| 8 | **Release build and provenance** | A binary that differs from the reviewed source | `hl1/build.sh`, `hl1/linux.sh`, `hl1/make-release.py`, `hl1/overlay-manifest.py`, `hl1/make-follower-release.py`, `hl1/verify-follower-release.py` |
| 9 (optional) | **P2P networking**, needed before validators are opened | DoS, eclipse | `pkg/networking` (`libp2p.go`, `bootstrap.go`, `reputation.go`, `ban_gater.go`) |

**Out of scope:** the dashboard UI, the Hive desktop app (except its wallet key
handling and update-signature checks, which the [bug bounty](BUG_BOUNTY.md#3-scope)
covers), the GPU miner kernels, the bridge and contract packages (not active on
the pilot), the release-signing infrastructure, the vendored
`wasmer-go-patched`, and the live servers. **Do not test against the live
network**; see the [rules of engagement](BUG_BOUNTY.md#2-rules-of-engagement).

### 2.3 Trust boundaries

| Boundary | Attacker-controlled side | What enforces it |
|---|---|---|
| Public HTTPS API | Any internet client | Reverse proxy; a fail-closed write allowlist on the producer (signed transfers, plus task actions and enrollment when switched on) |
| Mining submission | Miners, enrolled or not | The admission guard, per-owner limits, invariants I1–I7 |
| P2P (libp2p) | Peers | StrictSign pubsub, static bootstrap, reputation bans. The producer's P2P port is loopback-only today and the producer drops transaction gossip |
| Block acceptance on followers | The producer and any gossip or HTTP source | Producer transition and signature, the signed-consensus gate, full re-execution, Proof-of-Entanglement |
| Edge relay settlement | Relays, workers, Mother Hive | ML-DSA-87 settlement proof, authorized relay IDs in the task manifest, replay maps |
| Loopback-only services | LAN and internet | Edge control, the edge-agent relay, faucet, treasury signer URL and dashboard bind to loopback by default |
| Release supply chain | Module sources, distribution | Pinned image digest, offline module proxy, `-mod=readonly`, two-build bit identity, `SHA256SUMS` |

### 2.4 Threat model

| Adversary | Goal | Defences to test |
|---|---|---|
| A1 Remote client | Steal funds, forge or replay transfers, crash or stall the producer, exhaust disk | ML-DSA-87 verification, nonces, Proof-of-Entanglement, the write allowlist, request-size limits, fail-stop on storage errors |
| A2 Rogue or Sybil miner | Earn more than its share, be paid twice, spend another owner's quota, trip a global latch | The guard, durable de-duplication, per-owner buckets, operator-signature checks, the bond, I1–I7 |
| A3 Malicious peer | Make followers accept a forged or unsigned block, vote or certificate; flood gossip | Signed-consensus gates, producer transition, reputation bans (connection limits are a known gap) |
| A4 Rogue relay, worker or Mother Hive | Settle unpaid or duplicate work; redirect the ecosystem share | Settlement proof, manifest authorization, receipt root, replay maps |
| A5 Determinism tripper | Make two honest nodes compute different state | Integer-only emission and dust accounting, canonical encodings, height-gated rules, replay tools (`cmd/qsdm/hl1_replay.go`, `cmd/hl-audit`) |
| A6 Faulty or compromised producer | Over-issue, censor, rewrite history | Followers re-execute every block; the emission cap; the producer transition pins history. Censorship and reordering are accepted for the pilot |
| A7 Supply chain | Ship a binary that differs from the source | Pinned toolchain, offline module cache, reproducible builds, overlay manifest |
| A8 Key theft on a host | Sign as producer, relay or owner | File modes, key separation, the producer-transition procedure. No HSM: common HSMs do not support ML-DSA |

### 2.5 Payout-ledger invariants (I1–I7)

| # | Invariant |
|---|---|
| I1 | Each reward transaction matches its in-flight record (ID, nonce, amount bits, recipient, payload) |
| I2 | No reward ID is repeated or already paid |
| I3 | The sum of reward amounts is at most the schedule |
| I4 | The funder nonce advances by exactly k |
| I5 | Replaying funder-touching transactions gives exactly the funder balance, bit for bit; an unknown kind fails |
| I6 | The recipient is allowlisted |
| I7 | Every transaction in a local block belongs to an allowed family (funder heartbeat or reward, signed wallet transfer, signed task action, and signed `qsdm/enroll/v2` when enrollment is open) |

The central question for rank 3: **can any sequence of submissions, crashes or
restarts break an invariant without tripping FREEZE, or can an anonymous client
trip a global latch?** Crash-injection builds use the `hl_crashpoints` build
tag and are never shipped.

### 2.6 Known issues

The known limitations are listed in the
[bug bounty §3.2](BUG_BOUNTY.md#32-known-issues-not-eligible-as-reported).
Review notes on them are welcome, especially fix proposals.

## 3. Prior reviews (none independent)

| Review | Note |
|---|---|
| [Internal security review](SECURITY_AUDIT.md) (May 2026) | Self-assessment of web and API hardening; predates the current mining code |
| Internal audit checklist ([qsdm.tech/audit.html](https://qsdm.tech/audit.html)) | Self-maintained; a row passing is not an independent finding |
| Design critique rounds of the hardened mining path | Internal, model-assisted |
| Producer transition, edge relay and write-activation reviews | Internal |

## 4. Reproducible build

The network runs an untagged "hardened-legacy" build. The follower release
tooling rebuilds that exact `qsdm` bit for bit, so a reviewer can check that
the binary matches the source they read.

**Toolchain (pinned):**

- Go `1.26.9` (`go.mod`), inside
  `docker.io/library/golang@sha256:bcef992b77b1e2031aaaa51da75cebc32da8e9c182e12e633ea79023c79d9eff`
  (golang 1.26.9-bookworm, gcc 12.2), run by Podman with the network off.
- `GOTOOLCHAIN=local GOFLAGS=-mod=readonly CGO_ENABLED=1`, `-trimpath`,
  `-buildvcs=false`, build tags `validator_only,dilithium_circl`.
- Modules come from an offline module cache; `go.sum` still verifies every
  module. Without the cache, download modules with checksum verification
  (`GOSUMDB` on). If your build then does not reproduce the published
  checksums, report it: that is a review finding.

**Build twice and compare** (from the root of the review export):

```bash
HL1_MODPROXY='<module cache>' bash hl1/build.sh --out rev-1 --version <release>
HL1_MODPROXY='<module cache>' bash hl1/build.sh --out rev-2 --version <release>
bash hl1/build.sh --compare out/rev-1 out/rev-2      # must be bit-identical
```

Then compare `out/rev-1/SHA256SUMS` with the published release `SHA256SUMS`.

**Verify a published follower release bundle:**

```bash
python3 verify-follower-release.py <BUNDLE> \
    --public-key-json <release key JSON> --expected-key-id <KEY_ID> --qsdmcli <path to qsdmcli>
```

The last line must read `verify-follower-release: VERIFIED <release> qsdm <hash>`.
`--checksums-only` checks files but not the signature and prints
`SIGNATURE NOT VERIFIED`; such a bundle must not be treated as released.

**Run the tests** inside the pinned image:

```bash
HL1_WS='<path to tree>' hl1/linux.sh 'go test -tags validator_only,dilithium_circl ./...'
hl1/linux.sh 'go test -tags validator_only,dilithium_circl ./internal/legacymining/... ./pkg/chain/... ./pkg/poe/...'
HL1_WS='<path to tree>' hl1/linux.sh 'cd /work && python3 -B -m unittest discover -s hl1/tests'
hl1/linux.sh 'go test -tags validator_only,dilithium_circl ./cmd/hl-audit'
```

A small number of `pkg/api` tests are known to fail for environmental reasons;
the expected list ships with the tree (`hl1/BASELINE.md`).

**Run a local network for testing.** Start a `solo` or `network-producer` node
and one `network-follower` on your machine with the live activation heights
from [Consensus rules](CONSENSUS_RULES.md). Never point test traffic at
`api.qsdm.tech`.

## 5. Review checklist per component

Tick what you covered and say what you did not cover. A note that says "looked
at X and found nothing" is useful.

**C1 Consensus and block authentication**
- [ ] Does any path accept an unsigned or wrong-producer block at or above
      482,566, or an unsigned consensus message at or above 827,000?
- [ ] Does activation height 0 mean "always", and are paired settings enforced?
- [ ] Is every signature domain-separated and bound to the signer's address?
- [ ] Is the producer-transition prefix hash computed over exactly the history
      it claims?
- [ ] Can evidence or certificates from an untrusted source change state?

**C2 Mining admission and rewards**
- [ ] Can any submission, crash or restart sequence break I1–I7 without FREEZE?
- [ ] Can an anonymous client trip kill, freeze or admission-stopped?
- [ ] Is de-duplication durable before a reward is promised?
- [ ] Is the configuration parse strict, and is the config pinned to its hash?
- [ ] Is emission integer-only, and can the total ever exceed the schedule or
      the 90,000,000 CELL cap?

**C3 Mining protocol v2 and enrollment**
- [ ] Can an attestation be forged or replayed for another owner or GPU?
- [ ] Can enrollment skip the 10 CELL bond, reuse a reserved ID, or reuse an ID
      that already has receipts?
- [ ] Do the per-owner and global enrollment rate limits hold?
- [ ] Is the enrollment state root deterministic across nodes?

**C4 Wallets and signatures**
- [ ] Is ML-DSA-87 verification strict (lengths, encodings, context), with no
      malleability that changes a transaction ID?
- [ ] Is the address always SHA-256 of the exact public key that verified?
- [ ] Do nonces stop replay across restarts?
- [ ] Are keystore KDF parameters enforced on decrypt, and is AES-GCM nonce
      handling safe?
- [ ] Is the recovery-phrase derivation deterministic and free of bias?
- [ ] Can the account login challenge be replayed or bound to another wallet?
- [ ] Are Ed25519 transactions refused at admission?

**C5 Proof-of-Entanglement**
- [ ] Are 2–10 committed parents required from 816,600, and can a transfer name
      parents that are uncommitted, duplicated or its own?
- [ ] Is the cost of parent lookup bounded?

**C6 Edge relay settlement**
- [ ] Is the relay ID derived from the public key with its domain prefix?
- [ ] Are receipts limited (512), sorted, unique and bound by the receipt root?
- [ ] Can a settlement be replayed or credited to an unauthorized relay?
- [ ] Is the 70/15/15 split exact, and is the ecosystem wallet fixed?
- [ ] Do v1 pooled proofs fail closed?

**C7 Task actions**
- [ ] Are task actions signed from 625,000, and can reserved IDs be claimed?

**C8 Node and API**
- [ ] Does the producer's write allowlist fail closed?
- [ ] Are request sizes bounded on every write route?
- [ ] Do loopback-only services stay on loopback by default?
- [ ] Does a storage error stop the node instead of continuing?

**C9 Release build and provenance**
- [ ] Do two builds give identical `SHA256SUMS`, and do they match the
      published release?
- [ ] Does the follower verifier refuse every tampered file and a wrong key?

**C10 P2P (optional)**
- [ ] What happens with many connections or large gossip messages on each
      topic, given that no connection limits are configured?
- [ ] Can reputation bans be used to isolate an honest node?

## 6. How findings are tracked

1. **Vulnerabilities go private first.** Report through a private GitHub
   security advisory, or email as described in the
   [bug bounty](BUG_BOUNTY.md#1-how-to-report). Severity follows the
   [bug bounty rubric](BUG_BOUNTY.md#4-severity).
2. **Fixes are prepared in private** (a private advisory fork or branch). The
   reporter is invited to check the fix.
3. **Disclosure** follows the bounty timeline: a GitHub security advisory, a
   CVE where it applies, and an entry in the review log below.
4. **Review notes that contain no vulnerability** (coverage, hardening ideas,
   questions) can be public: open an issue titled `community-review: <component>`
   or a pull request that adds your note to the review log. If you are not sure
   whether something is exploitable, report it privately.
5. **Build attestations** (you rebuilt the release and the checksums matched, or
   did not) go in the review log with the commit, the release name and your
   `SHA256SUMS`.

## 7. Review log

Every review, build attestation, finding and program change is recorded here.
An empty log means nobody has reported a review yet; it does not mean the code
has been reviewed.

| ID | Date | Who | Component(s) | Commit / release | Type | Result | Link |
|---|---|---|---|---|---|---|---|
| R0 | 2026-10-17 | QSDM project | All | `8d7b159` (proposed) | Program | Community review and bug bounty published. Bounty tiers and a 50,000 CELL pool confirmed by the owner on 17 October 2026. Review export with build and follower-release tooling: **not yet published** | This page |

No external reviews, build attestations or bounty reports have been logged yet.

**To add an entry**, include: a short ID (next free `R<n>`), the date, your
name or handle (or "anonymous"), the components from §2.2, the exact commit or
release you reviewed, the type (review notes, build attestation, finding,
program change), a one-line result, and a link to your notes or the advisory.
Vulnerability entries are added only after disclosure.

## 8. Relation to the audit checklist and mainnet gates

- The internal audit row `mining-01` ("mining protocol external audit") stays
  **pending**. This program does not close it by itself. The owner decides what
  evidence closes it; a suggested bar is: published review notes covering ranks
  1–4 from at least two reviewers independent of the project, at least two
  independent build attestations that match the release, and no open Critical
  or High findings.
- The mainnet gate in [Treasury policy §9](TREASURY_POLICY.md#9-mainnet-release-gates)
  now asks for a published security review of consensus, economics and custody
  with its findings fixed. The current path to it is this program.
- Public pages must keep saying that **no independent audit has been
  completed** until one has.

## 9. A professional audit remains welcome

If an audit firm, a foundation or a sponsor wants to carry out or fund an
independent audit, the project will share the full audit packet (system
overview, trust boundaries, threat model, ranked code map with entry points,
build and test instructions, known issues and expected deliverables) and give
the auditor the same build. Its findings would be logged here like any other
review, and the public pages would then say what was audited, by whom, and at
which commit.

## 10. Related pages

- [Bug bounty](BUG_BOUNTY.md)
- [Security model](SECURITY_MODEL.md)
- [Consensus rules in force](CONSENSUS_RULES.md)
- [Internal security review](SECURITY_AUDIT.md)
- [Network status](NETWORK_STATUS.md)
- [Security policy](https://github.com/blackbeardONE/QSDM/blob/main/SECURITY.md)
