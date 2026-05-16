# Request for Proposal — QSDM mining-protocol security audit

> **Status:** Draft engagement letter for external security firms. Companion
> to the technical packet at
> [`AUDIT_PACKET_MINING.md`](../AUDIT_PACKET_MINING.md) which is the
> *what-to-read* document for the auditor's engineers. This RFP is the
> *how-to-engage* document for the firm's business contact.
>
> **Audit row:** `mining-01` (severity: critical, category: mining_audit).
> Once a firm is engaged, the row in
> [`pkg/audit/checklist.go`](../../../source/pkg/audit/checklist.go) moves
> from `pending` to `failed` with `Notes` naming the engagement, then to
> `passed` on receipt of the final report. Engagement state is
> tracked in [`EXTERNAL_REQUESTS.md`](./EXTERNAL_REQUESTS.md).
>
> **Distribution:** under NDA. The audit-row Notes are public; the firm
> identities and proposal contents are not until QSDM publishes the
> audit findings post-remediation.

---

## 1. About QSDM

QSDM (Quantum-Safe Distributed Mining) is an open-source distributed
ledger project building a useful-Proof-of-Work consensus tied to
verified GPU hashrate. The repository lives at
<https://github.com/blackbeardONE/QSDM>; production node lives at
`api.qsdm.tech`. The codebase is pure Go for everything on the
consensus path (no CGO dependencies on the validator binary) and is
licensed Apache-2.0.

QSDM is currently in **pre-mainnet, pre-Phase-6** state:

- Pure-Go reference miner runs in production on a single validator at
  BLR1 (DigitalOcean Bangalore).
- CUDA miner kernel exists but is gated behind Phase 6 of the project's
  Major Update plan — **not in scope for this audit**.
- Mainnet launch is gated on this audit's clean report plus the
  engagement separately tracked under audit row `tok-01` (counsel
  review of tokenomics) and `mining-05` (incentivized testnet).
- Internal audit checklist score at time of this RFP: **95.29%
  (81/85)**. The four remaining pending rows are all wall-clock-blocked
  on external parties; `mining-01` is the most consequential of those
  blockers.

## 2. Scope

In-scope artefacts:

1. The QSDM mining sub-protocol — Candidate C, mesh3D-tied useful PoW.
   Normative spec at `QSDM/docs/docs/MINING_PROTOCOL.md`. Reference
   implementation at:
   - `QSDM/source/pkg/mining/` (proof codec, PoW hash, verifier
     pipeline, difficulty retarget, epoch rotation, DAG construction,
     reference solver)
   - `QSDM/source/pkg/mining/roleguard/` (startup gate preventing a
     validator binary from mining or a miner binary from signing
     blocks)
   - `QSDM/source/pkg/chain/emission.go` (integer-only halving
     calculator)
2. The Genesis-pre-fund and bootstrap-miner registration paths
   (`pkg/genesis`, `pkg/mining/preflight`) — narrow surface, but
   load-bearing for the chain's first 100 blocks.
3. The validator-side reward issuance flow — how a miner's accepted
   proof becomes a token mint in the on-chain ledger
   (`pkg/contracts/cell_minting`, `pkg/wallet`).

Out-of-scope (handled separately):

- The CUDA miner kernel (`mining-02`, deferred to Phase 6).
- The PoE+BFT consensus layer itself. Consensus is deliberately
  out of scope; mining is additive. (Auditors are welcome to flag any
  finding where mining and consensus interact poorly, but a
  comprehensive consensus audit is not requested here.)
- The NVIDIA NGC attestation transparency surface — covered by trust-API
  guardrails (`trust-01` through `trust-03`) and the `trustcheck` CLI.
- The dashboard / metrics / runbook surface — covered by separate
  in-tree audit rows (all currently `passed`).

The auditor's remit is to confirm that the **pure-Go reference
implementation** of the mining protocol:

1. Matches the spec in `MINING_PROTOCOL.md` byte-for-byte where
   determinism is required (canonical JSON, integer-only difficulty
   maths, etc.).
2. Is bit-for-bit reproducible across implementations and platforms
   (Linux x86-64 is the only fully-supported runtime; macOS and
   Windows are dev-only but should not produce divergent proofs).
3. Admits no economic or DoS vector that would let an attacker halt
   or fork PoE+BFT consensus through the mining surface.

Detailed threat model (six adversary classes:
`A_rogue_miner` / `A_validator_briber` / `A_supply_chain` /
`A_clock_skew` / `A_chain_rebase` / `A_genesis_pre-fund_griefer`)
is enumerated in `AUDIT_PACKET_MINING.md` §2.

## 3. Expected deliverables

The engagement deliverables, in increasing order of seniority of the
audience:

### 3.1 Confidential finding catalog (engineering audience)

One document per finding. Per-finding fields:

| Field | Required | Notes |
|-------|----------|-------|
| ID | yes | `QSDM-MINING-<NNN>` |
| Severity | yes | Critical / High / Medium / Low / Informational |
| Affected components | yes | file:line or function:protocol-step |
| Threat-model class | yes | one of the six in `AUDIT_PACKET_MINING.md §2` (or new class with rationale) |
| Reproduction | yes | minimal repro that QSDM can run from `QSDM/source` |
| Remediation guidance | yes | not a patch, but a clear direction QSDM should pursue |
| Public-disclosure recommendation | yes | "publish in final report" / "private until fix lands" / "private indefinitely" |
| Status | tracked | open / fix-acked / fixed-on-branch / fixed-on-main / disputed |

### 3.2 Executive summary (operator audience)

≤4 pages. The operator running a QSDM validator should understand,
after reading this:

- Whether the mining protocol is fit for production.
- The set of monitoring signals an operator should treat as
  attack-shaped.
- Any operational posture changes recommended (config knobs,
  alert thresholds, etc.).

### 3.3 Public-disclosure-ready report (community audience)

Released by QSDM after remediation lands. ≤30 pages. Contains:

- The executive summary verbatim.
- The finding catalog filtered to "publish in final report" items.
- The auditor's high-level methodology and process notes.
- A statement of any informational findings or recommendations that
  are non-actionable but inform future design (these are valuable to
  community reviewers).
- Negative findings — where the auditor *looked* and found nothing —
  are equally valuable and explicitly requested. The community-facing
  report should make clear what the audit's coverage envelope was.

### 3.4 Reproducible artefacts (auditor-internal)

- Any custom test fixtures or property-based testing harnesses the
  auditor builds during the engagement, contributed back to the
  repository under Apache-2.0 (or a license compatible with it). This
  is a hard requirement: QSDM does not engage on a "audit-only,
  artefacts withheld" basis.
- The exact commit SHA the audit covered. QSDM will provide one or
  more reproducible build environments (Linux x86-64 + the
  validator-only Docker image).

## 4. Auditor qualifications

The firm's audit team for this engagement should include:

- **At least one senior cryptographer** with prior published work on
  proof-of-work soundness, Merkle / DAG construction, canonical
  serialization, or related primitives.
- **At least one Go-fluent engineer** comfortable reading stdlib-only
  Go (no CGO required for the in-scope code), preferably with prior
  audits of Go-implemented blockchain protocols.
- **At least one consensus / distributed-systems reviewer** to
  evaluate the interaction surface between mining and PoE+BFT — even
  though the latter is out of scope, the *interface* between them is
  in scope.

If the firm proposes a different team composition, please make the
case in the proposal.

## 5. Timeline

QSDM proposes the following windows; we are open to adjusting based
on the firm's availability and findings velocity.

| Phase | Calendar window | Description |
|-------|-----------------|-------------|
| Engagement setup | T+0 to T+2 weeks | NDA, scope confirmation, repository access, reproducible build verification |
| Active audit | T+2 weeks to T+8 weeks | Finding catalog populated incrementally; weekly written sync; one mid-engagement 60-min call between auditor + QSDM consensus lead |
| Remediation window | T+8 weeks to T+12 weeks | QSDM addresses Critical / High findings on a private branch; auditor re-reviews each fix on a per-finding basis |
| Public disclosure | T+12 weeks to T+14 weeks | Executive summary and final public report published; private findings remain confidential |

Total elapsed time: ~14 weeks from kickoff. The audit covers a single
commit SHA frozen at T+0; subsequent merges to `main` are out of
scope for this engagement but may motivate a follow-up engagement.

## 6. Commercial terms

Budget guidance is provided to help firms calibrate proposals; binding
terms will be set in the final SOW.

| Component | Guidance |
|-----------|----------|
| Total engagement | USD low-five-figures to mid-six-figures, depending on team size and depth |
| Payment milestones | 25% on kickoff, 50% on finding-catalog draft delivery, 25% on final public report |
| Reproducible-build attestation | included in base fee |
| Follow-up engagement for a future SHA | priced separately at the time |
| Public-disclosure embargo | minimum 30 calendar days post-final-report; QSDM may request shorter for critical findings remediated faster than expected |

Travel is not required; the engagement is fully remote. If on-site
work is preferred by the firm, travel is at the firm's expense and
QSDM will provide a remote-friendly workspace at the validator's
operational location.

## 7. Selection criteria

Proposals will be evaluated on, in roughly decreasing weight:

1. **Prior public audits of blockchain consensus or PoW protocols.**
   The firm should be able to point to at least two public audit
   reports of a similar scope. Links to those reports are required;
   QSDM will read them as part of evaluation.
2. **Response time on critical findings during prior engagements.**
   The firm should be willing to disclose, on a per-engagement basis
   (with the prior client's consent), the median wall-clock time
   from "finding identified" to "finding written up and shared with
   client". A 1-2 day median is excellent; >5 day median suggests a
   process mismatch with QSDM's velocity.
3. **Communication cadence preference.** QSDM expects at least one
   written sync per week and one 60-min call per month during the
   active-audit window. Firms that work primarily asynchronously
   over email + shared documents (rather than synchronously over
   meetings) are preferred.
4. **Team-member identity.** Proposals should name the specific
   senior cryptographer + Go reviewer + consensus reviewer who will
   actually work on the engagement, not "a senior cryptographer to
   be assigned". Substitutions during the engagement require
   QSDM-side written approval.
5. **Confidentiality posture.** QSDM expects the firm to operate
   under a mutual NDA covering the finding catalog (the executive
   summary and final public report are explicitly carved out). The
   firm should propose an NDA template or accept the standard
   industry mutual-NDA.

## 8. How to respond

One email to `audit-rfp@qsdm.tech` (placeholder — substitute with the
operator's address before sending). Subject line:
`QSDM-MINING-RFP-<firm-name>`.

Email body should contain:

- A 1-page introduction to the firm and its blockchain-audit practice.
- Links to two prior comparable public audit reports.
- Proposed team composition (names, roles, brief CVs).
- Proposed timeline, calibrated to the firm's pipeline.
- Proposed commercial terms within the guidance bands in §6.
- Any clarifications on the scope or threat model the firm requests
  before committing.

**Response window:** 4 weeks from RFP distribution. Late responses
considered on a case-by-case basis; QSDM will not penalize a firm for
asking for a clarification that delays their response by 1-2 days.

**Selection timeline:** QSDM will respond to every received proposal
within 1 week of submission, either with a follow-up clarification or
a shortlist invitation. Shortlist candidates will be invited to a
30-min Q&A call; final selection within 2 weeks of the shortlist
call.

## 9. Information packet

Provided under NDA on engagement:

| Doc | Public? | Description |
|-----|---------|-------------|
| `QSDM/docs/docs/MINING_PROTOCOL.md` | yes | Normative spec |
| `QSDM/docs/docs/AUDIT_PACKET_MINING.md` | yes | Reading guide + test coverage matrix |
| `QSDM/docs/docs/CELL_TOKENOMICS.md` | yes | Reward schedule + emission |
| `QSDM/docs/docs/NODE_ROLES.md` | yes | Validator / miner split |
| `QSDM/docs/docs/REBRAND_NOTES.md` §4 | yes | Phase-0 working hypotheses |
| `QSDM/source/pkg/mining/**` | yes | Reference implementation |
| `QSDM/source/pkg/audit/checklist.go` | yes | Full audit-row inventory (this engagement satisfies `mining-01`) |
| Internal audit-row Notes for `mining-*` rows | NDA | Evidence chain for every passed mining-cluster audit row, useful for the auditor to triangulate spec ↔ implementation ↔ test coverage |
| BLR1 validator config (sanitised) | NDA | Production deployment for context — not for auditor remote access |

Public docs are linkable directly from this RFP; NDA-protected docs
will be shared on signed mutual-NDA.

## 10. Confidentiality

This RFP is shared under the firm's standard mutual NDA (or QSDM's
template, available on request). Auditor must not:

- Disclose the existence of this engagement publicly until the final
  public report is released, except as required by law or required to
  satisfy regulatory disclosure obligations.
- Disclose any finding to any third party other than the firm's
  engagement team and QSDM-named contacts.
- Use any QSDM-provided material for purposes other than this
  engagement.

QSDM commits to:

- Not disclose the firm's name publicly until both parties have
  confirmed in writing that the engagement is concluded.
- Provide reasonable response time on findings (target: written ack
  within 2 business days, fix-or-dispute within 10 business days for
  Critical / High).
- Credit the firm appropriately in the final public report and in any
  community-facing announcements, in a manner the firm pre-approves.

## 11. Contact

Engagement contact: **(to be filled before send)**.

Technical lead at QSDM (the auditor's day-to-day counterpart):
**(to be filled before send)**.

Legal contact for NDA execution:
**(to be filled before send)**.

---

## Sign-off

This RFP is approved for distribution by:

| Role | Name | Signature | Date |
|------|------|-----------|------|
| QSDM project lead | _________ | _________ | ____ |
| Tech lead (consensus + mining) | _________ | _________ | ____ |
| Operational lead (deployment) | _________ | _________ | ____ |

**Distribution list:** (to be filled before send; recommended initial
shortlist of 4-6 firms with prior blockchain consensus audit work).

---

> **Internal note (NOT for distribution).** This document is the
> engagement wrapper. The technical-quality of the engagement
> depends entirely on the auditor's actual experience and the
> finding catalog they produce; the RFP is just the gate. Once a
> firm is selected, this document is superseded by the SOW; the
> RFP is archived in `QSDM/docs/docs/audit/sent/<YYYY-MM-DD>/`
> with the firm name redacted to preserve confidentiality.
