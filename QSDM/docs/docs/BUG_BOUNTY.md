# Bug bounty

> [!IMPORTANT]
> **Proposed program. Reward amounts are proposed; the owner has to confirm them.**
> This page sets out QSDM's bug bounty for good-faith security research. It is
> part of the open [community security review](COMMUNITY_REVIEW.md), which is
> the project's current path in place of a paid audit firm (owner decision,
> 17 October 2026). **No independent security audit of QSDM has been
> completed**, and no bounty has been paid yet. Until the owner confirms the
> reward tiers below, reports are still welcome and are handled under this
> policy, but the amounts are not a commitment.

QSDM is a public pilot network (pre-mainnet) with a single block producer. See
[Network status](NETWORK_STATUS.md) and [Security model](SECURITY_MODEL.md) for
what is live today.

## 1. How to report

Use the existing private security contacts, the same ones listed in
[`security.txt`](https://qsdm.tech/.well-known/security.txt) and
[`SECURITY.md`](https://github.com/blackbeardONE/QSDM/blob/main/SECURITY.md):

1. **Preferred:** a private GitHub security advisory on the QSDM repository:
   <https://github.com/blackbeardONE/QSDM/security/advisories/new>.
2. **If you cannot use GitHub:** email `admin@qsdm.tech`. Email is not
   encrypted, so send a short description first, without exploit details, keys
   or personal data, and we will set up a private advisory with you.

**Never** open a public issue, pull request, discussion or social-media post
about an unfixed vulnerability.

Please include:

- the component (use the scope names in §3) and the affected version, release
  name or commit;
- the impact as you see it, and the severity you propose (§4);
- step-by-step reproduction **on a local node or local network**, with the
  smallest proof of concept that shows the impact;
- your configuration (role, build tags, activation heights) if it differs from
  the live network values in [Consensus rules](CONSENSUS_RULES.md);
- a CELL address for a possible reward, and how you want to be credited (name,
  handle, or anonymous).

## 2. Rules of engagement

Good-faith research that follows these rules is covered by the safe harbour in
§7.

- **Use a local node.** Reproduce every exploit on your own node, a local
  network of nodes, or a copy of chain data you made yourself. The
  [community review build instructions](COMMUNITY_REVIEW.md#4-reproducible-build)
  show how to build the same binary the network runs.
- **Never touch other people's funds or data.** Do not move, freeze, burn or
  spend CELL you do not own, do not use other users' keys, wallets, accounts or
  mining identities, and do not read or keep personal data. If you reach data
  that is not yours by accident, stop, do not copy it, and tell us.
- **No denial of service against the live network.** Do not flood, load-test
  or crash the live block producer, the public API (`api.qsdm.tech`), the
  website, the backup node or any relay. Show availability bugs locally; a
  description plus a local reproduction is enough.
- **No live exploitation of mining or settlement.** Do not submit forged,
  replayed or duplicate mining proofs, enrollments or edge settlements to the
  live network. Use a local producer.
- **Ordinary use is fine.** You may read the public API at normal rates and
  send ordinary transactions from wallets you own.
- **No social engineering, phishing or physical attacks** against the owner,
  operators, users, hosting providers or GitHub.
- **No attacks on third-party services** (GitHub, hosting or DNS providers,
  NVIDIA services). Report their bugs to them.
- **Give us time.** Keep the details private until the disclosure date in §6.
- **Do not demand payment** as a condition of disclosure. Reports that do are
  not eligible.

## 3. Scope

Severity follows demonstrated impact on the live network's configuration (the
activation heights and roles in [Consensus rules](CONSENSUS_RULES.md)). The
component list below follows the [community review scope](COMMUNITY_REVIEW.md#2-review-scope).

| # | Component | In scope | Out of scope |
|---|---|---|---|
| C1 | **Consensus and block authentication** | ML-DSA-87 block, vote, certificate and evidence signatures; the producer allowlist and producer transition; height-gated enforcement (signed consensus from 827,000); anything that makes an honest follower accept an unsigned or wrong-producer block, or makes two honest nodes compute different state (a fork) | Censorship or reordering by the single producer (an accepted pilot limitation); the multi-validator BFT library code that is not wired into production, except as "future" informational reports |
| C2 | **Supply, issuance and mining rewards** | The mining admission guard, durable proof store and payout ledger; invariants I1–I7; crash recovery and replay; the emission schedule and the 90,000,000 CELL cap; anything that mints above the schedule, pays a reward twice, or pays the wrong recipient | Choices of emission parameters (policy, not a bug); the size of the synthetic issuance account's bookkeeping balance (a known issue, see §3.2) unless you can turn it into issuance above the schedule |
| C3 | **Mining protocol v2, attestation and enrollment** | Proof verification; `nvidia-cc-v1` and `nvidia-hmac-v1` verifiers; enrollment, bond and unenrollment; the enrollment admission switch; per-owner and global caps; latches that an anonymous client can trip | The GPU miner kernels and their performance; mining on hardware other than NVIDIA GPUs (not supported) |
| C4 | **Wallets and transaction signatures** | ML-DSA-87 signing and verification (FIPS 204 via Cloudflare CIRCL); signature malleability or wrong-key acceptance; nonces and replay; keystore encryption (PBKDF2-SHA-256, AES-256-GCM); recovery phrases; account login challenges; `qsdmcli`; key handling and signing in the web wallet and in QSDM Hive's wallet | Attacks that need malware already on the victim's device, a stolen passphrase, or a user pasting attacker code into the browser console |
| C5 | **Proof-of-Entanglement** | Parent-reference rules for transfers (from 816,600); acceptance of a transfer whose parents are invalid; history-lookup cost that lets one transfer stall block validation | — |
| C6 | **Edge relay settlement** | ML-DSA-87 settlement proofs; the relay-only rule (from 833,800); authorized relay IDs; receipt roots and replay maps; the 70/15/15 split and the ecosystem share | Edge task scheduling quality; the operator-only test status of paid edge work |
| C7 | **Task actions and reserved IDs** | Signed task actions (from 625,000); hijacking built-in tasks; reward-ID or transaction-ID spoofing | — |
| C8 | **Node and public API** | Authentication or write-allowlist bypass; request-size, parsing or resource bugs that crash or stall a node with a small number of requests (show it locally); information leaks of keys, tokens or private configuration; services that should be loopback-only but are reachable | Volumetric or network-level DoS (bandwidth floods); rate limits on public read endpoints with no further impact; missing security headers or TLS settings with no demonstrated impact |
| C9 | **Release build and provenance** | Anything that lets a binary differ from the reviewed source without detection: the reproducible build, `SHA256SUMS`, the follower release verifier, Hive release signature checks | Compromise of the release owner's offline machine; GitHub or CDN account takeover |
| C10 | **P2P networking** | libp2p configuration, gossip-topic handling, reputation bans, eclipse attacks. The producer's P2P port is not public today, so these are rated on their impact once validators are opened, and capped at High | Attacks that only work against the closed producer port from the same host |

**Producer and other key compromise paths** are in scope when they are remote or
software-based: a path that reads, derives, logs or exports the producer
consensus key, a relay settlement key, the reserved task-catalog key, an owner
key or a wallet key through the API, a file-permission or path bug in QSDM
code, a backup or snapshot tool, or a crash dump. Compromise through the
hosting provider, stolen credentials, phishing or physical access is out of
scope.

### 3.1 Out of scope everywhere

- The dashboard UI, the bridge and smart-contract packages (not active on the
  pilot), and the vendored `wasmer-go-patched` (report upstream).
- Bugs in third-party dependencies with no exploitable path in QSDM (report
  upstream; a working QSDM exploit path is in scope).
- Reports from automated scanners without a demonstrated impact.
- Clickjacking on pages with no sensitive action, self-XSS, CSRF on logout, or
  username or email enumeration with no further impact.
- Anything that needs the producer key or another privileged key that you
  already hold, unless it escalates beyond what that key is meant to do.
- Issues already reported by someone else or already known to us (§5).
- Issues already disclosed in public before your report.

### 3.2 Known issues (not eligible as reported)

These limits are disclosed. A report is eligible only if it shows a new exploit
path with a greater impact than described here.

1. A single block producer: it can censor or reorder transactions, and there is
   no multi-validator BFT.
2. Rewards are paid from a synthetic issuance account whose bookkeeping balance
   is far larger than the cap. Issuance is bounded by the emission schedule and
   the payout-ledger checks, not by that balance. Replacing it is a mainnet gate.
3. The code ceiling for supply is larger than the published 90,000,000 CELL cap
   ([Genesis Policy v1.0](GENESIS_POLICY_v1.0.md)); aligning the code is
   planned.
4. Transaction fees are burned.
5. `nvidia-hmac-v1` is an economic control, not a hardware proof: its keys are
   public chain state. Theft is prevented by owner binding, owner-signed
   submissions and per-owner and global caps.
6. Chain history before block 482,566 is unsigned; the activation heights must
   be the same on every node.
7. The live producer allowlist still names the earlier producer ID; the
   producer transition takes precedence.
8. No libp2p connection or resource limits are configured; the P2P port is not
   public.
9. Rate limits for some routes are enforced at the reverse proxy, not in the
   node.
10. Core releases are protected by reproducible builds and `SHA256SUMS`; a
    detached signature for core binaries is not produced yet.
11. Ed25519 transaction retirement is an admission rule only; replay does not
    re-check the algorithm of old gossip transactions.

## 4. Severity

Severity is set by the impact you demonstrate, adjusted by how hard the attack
is. We use a CVSS-like rubric so that ratings are explainable:

| Factor | Raises severity | Lowers severity |
|---|---|---|
| **Impact** | Loss or creation of CELL, a chain fork, key exposure, a lasting network halt | Temporary or local effect, information only |
| **Attacker needs** | Nothing (any internet client) | An enrolled miner, an authorized relay, a user action, or a privileged key |
| **Complexity** | Reliable, cheap, repeatable | Needs rare timing, large resources, or an unusual configuration |
| **Scope** | Affects every node or every user | Affects one node, one user, or only the attacker |
| **Live config** | Works with the live activation heights and roles | Needs a setting the live network does not use |

### Tiers and examples

| Severity | What it means | QSDM examples |
|---|---|---|
| **Critical** | Breaks a core guarantee for everyone: supply, consensus, or other people's funds | Makes honest followers accept a block not signed by the authorized producer, or accept unsigned consensus messages above 827,000 (a forgeable chain or fork). Double-spends a committed transfer. Mints CELL above the emission schedule or the 90,000,000 CELL cap without tripping the ledger's FREEZE. Forges an ML-DSA-87 signature, or makes a verifier accept a signature for the wrong key or message, in a way that moves someone else's CELL. Extracts the producer consensus key remotely. Two honest nodes compute different state from the same untrusted input. |
| **High** | Theft or duplication that is bounded, or a lasting outage caused by cheap input | Steals another owner's mining reward or quota. Gets one proof paid twice. Settles unpaid or duplicate edge work, or redirects the ecosystem share, through a relay. Stops block production with a handful of crafted requests until an operator steps in (a crash or panic, not a flood). Lets an anonymous client trip a global mining latch (kill, freeze, admission stopped). Bypasses the write allowlist. Hijacks a built-in task. Makes the release verifier accept a tampered binary. Remotely extracts a relay or owner key. |
| **Medium** | Real security impact with limits | Stalls the API or a node for a while with cheap requests, and it recovers by itself. Bypasses the enrollment bond without stealing rewards. Gets a transfer with invalid Proof-of-Entanglement parents accepted, without a double spend. Replays a non-transfer message. Exhausts disk slowly through accepted input. Leaks private configuration (not keys). Web wallet XSS that needs a user click. |
| **Low** | Minor impact or defence-in-depth | Leaks versions or internal paths. A service listens on a wider interface than intended with no sensitive data. A rate-limit gap on a non-sensitive route. A security-relevant documentation error that could mislead operators. |

Informational reports (best-practice suggestions with no demonstrated impact)
are welcome as public issues once you are sure they are not exploitable; they
are credited in the review log but carry no reward.

## 5. Rewards (proposed, owner to confirm)

> [!WARNING]
> **Proposed tiers, not yet confirmed by the owner.** The amounts below are a
> proposal. They become binding only when the owner confirms them on this page.

| Severity | Proposed reward |
|---|---|
| Critical | up to **20,000 CELL** |
| High | up to **5,000 CELL** |
| Medium | up to **1,000 CELL** |
| Low | up to **100 CELL** |

- **Source of rewards.** Rewards are paid from the owner's own mined CELL
  holdings by an ordinary signed transfer. **No CELL is ever minted** for the
  bounty; it does not change the supply, the emission schedule or the 90,000,000
  CELL cap, and it uses no treasury (there is none; see
  [Genesis Policy v1.0](GENESIS_POLICY_v1.0.md)).
- **Amount within a tier.** Set by the demonstrated impact on the live
  configuration, the quality of the report and proof of concept, and whether a
  fix is suggested.
- **Budget.** The owner will publish the total CELL set aside for the program
  when confirming the tiers. If that pool runs out, the program pauses with
  notice on this page; reports already accepted keep their award.
- **Value.** CELL on the pilot network has no promised value, and the project
  does not sell or buy it. A reward is not an investment.
- **Eligibility.** You must follow §2, be the first reporter (below), and not be
  the owner, a project maintainer, or someone paid by the project for the same
  work. We cannot pay where the law forbids it (for example, sanctioned persons
  or places). You are responsible for any tax. You may decline a reward or ask
  to be credited only.

**Duplicates and first reporter.** The first report that lets us reproduce the
issue gets the reward. Later reports of the same root cause are duplicates and
are not rewarded, though they may be credited if they add something material.
Several symptoms of one root cause count as one report. An issue we already
track privately counts as a duplicate; we will show you the date we recorded
it. Timestamps of the GitHub advisory or the email decide the order.

## 6. Response and disclosure timeline

| Step | Target |
|---|---|
| Acknowledge the report | within 72 hours |
| Confirm, rate severity and tell you | within 7 days |
| Fix or documented mitigation, Critical and High | aim for 14 days |
| Coordinated public disclosure | **90 days** after the report, or earlier once a fix is released and followers have had time to upgrade, whichever comes first |

- We publish fixed issues as GitHub security advisories, request a CVE where it
  applies, and credit you as you wish.
- If a fix needs a consensus change at a future activation height, we may ask
  for an extension so that operators can upgrade first. Extensions are agreed
  with you, not imposed.
- If we miss the 90-day date without agreeing an extension, you may publish
  after giving us 7 days' notice.
- If an issue is being exploited on the live network, we may disclose sooner to
  protect users, and we will tell you first.

## 7. Safe harbour

If you make a good-faith effort to follow this policy:

- we consider your research authorized, and we will not bring or support any
  legal action against you for it, including under computer-misuse or
  anti-circumvention laws, or under the [terms of use](https://qsdm.tech/terms.html)
  of the website and API;
- we will not report you to law enforcement for it;
- if someone else brings legal action over research you did under this policy,
  we will say publicly that your research was authorized by us;
- you may break this policy's rules only where that is unavoidable to report a
  serious issue, and you stop and tell us as soon as you can.

Good faith means: you follow §2, you do no harm beyond what is needed to show
the issue, you do not access or keep other people's data or funds, and you do
not use the issue for gain or extortion. This safe harbour covers QSDM's own
systems and code only. It cannot bind third parties such as hosting providers or
other users, and it is not legal advice. If you are unsure whether something is
allowed, ask through the private channel first.

## 8. Payout process

1. You report privately (§1).
2. We reproduce the issue and agree the severity with you. If we disagree, we
   explain why in writing; you may add evidence.
3. We fix it, and you may check the fix before release.
4. After the fix ships, the owner sends the reward as a signed CELL transfer to
   the address you gave. The transaction ID is recorded in the
   [review log](COMMUNITY_REVIEW.md#7-review-log) with your consent.
5. The advisory is published on the disclosure date (§6), with credit.

## 9. Acknowledgments

Reporters who agree to be named are listed in the
[review log](COMMUNITY_REVIEW.md#7-review-log). No bounty reports have been
received or paid yet.

## 10. Changes to this policy

Changes are made by pull request to this file and noted in the review log.
A change never reduces the reward or the safe harbour for a report that was
already submitted.
