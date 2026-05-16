# Counsel Brief — Cell (CELL) tokenomics sign-off

> **Status:** Draft engagement brief for external legal counsel. Companion
> to the technical reference at
> [`CELL_TOKENOMICS.md`](../CELL_TOKENOMICS.md) which is the
> *what-the-system-does* document. This brief is the
> *what-we-need-counsel-to-opine-on* document.
>
> **Audit row:** `tok-01` (severity: critical, category: tokenomics).
> Once counsel issues a written opinion, the audit row in
> [`pkg/audit/checklist.go`](../../../source/pkg/audit/checklist.go)
> moves from `pending` to `passed` with `Notes` referencing the
> opinion-letter date and counsel firm.
>
> **Distribution:** privileged + confidential, attorney-client work
> product. Distribute only to engaged counsel under engagement letter.
>
> **This document is not legal advice and does not constitute legal
> advice. It is a request for counsel's opinion. The QSDM project lead
> has not made and is not making any representation about the
> regulatory characterisation of the CELL coin pending counsel's
> review.**

---

## 1. Background

QSDM (Quantum-Safe Distributed Mining) is an open-source distributed-
ledger project. The protocol mints a native coin called **Cell**
(symbol: `CELL`) on a deterministic, integer-only schedule defined in
the project's source code. The full normative reference is in
[`CELL_TOKENOMICS.md`](../CELL_TOKENOMICS.md). Headline parameters:

| Parameter | Value |
|-----------|-------|
| Total supply cap | 100,000,000 CELL |
| Treasury (genesis pre-fund) | 10,000,000 CELL |
| Mining emission | 90,000,000 CELL over ~21 years |
| Halving cadence | every 4 years (210,000 blocks at 10 s) |
| Block time target | 10 seconds |
| Decimals | 8 |
| Smallest unit | "dust" (1 CELL = 10^8 dust) |
| Issuance mechanism | useful-Proof-of-Work tied to verified GPU hashrate |
| Validator fees | denominated in CELL; never mint new supply |

The project is currently in **pre-mainnet** state. No CELL has been
issued on a public mainnet. The reference implementation runs on a
single validator at `api.qsdm.tech`; that validator is in development /
testnet posture, not production.

## 2. The questions on which counsel's opinion is sought

QSDM requests counsel's written opinion on the following, in
descending order of priority:

### 2.1 Securities-law characterisation under U.S. federal law

Under the Howey test and subsequent guidance (including but not
limited to SEC guidance, the FIT21 framework where applicable, and
the recent post-Ripple jurisprudence), is the CELL coin, as
configured per [`CELL_TOKENOMICS.md`](../CELL_TOKENOMICS.md):

- A **security** at the moment of mining issuance? (PoW emission to
  the miner who produced the proof.)
- A **security** at the moment of validator-fee payment? (Fees are
  paid in already-minted CELL; no new supply is created.)
- A **security** if traded peer-to-peer on a decentralised exchange?
- A **security** if listed on a centralised exchange? (We are not
  presently in conversation with any centralised exchange, and the
  question is asked for forward-looking planning, not as a step we
  are about to take.)

The Genesis treasury (10M CELL) is a particularly sensitive question:

- The treasury allocation is held by a multisig in the QSDM project's
  custody at genesis (see §4 of `CELL_TOKENOMICS.md`).
- It is not sold pre-mainnet or at mainnet launch.
- It may be deployed for grants, bug-bounty payouts, exchange listing
  fees, ecosystem development, or sold on-market post-mainnet at the
  project's discretion subject to community governance (see §6 of
  `CELL_TOKENOMICS.md`).

Specifically: under what (if any) Genesis-treasury-management posture
would the treasury allocation itself constitute the offer or sale of
a security to the project's contributors / founders / early
participants? What carve-outs (e.g. vesting, lockups, public
disclosure of deployments) are conventional to mitigate that risk?

### 2.2 Money-transmitter and money-services-business considerations

Under FinCEN guidance and applicable state law (including New York's
BitLicense framework, California's DFPI guidance, and any other
jurisdiction counsel deems relevant given the contributor footprint):

- Does the QSDM project, as the originating-protocol developer,
  engage in money transmission or money services by:
  (a) operating a validator that includes mined transactions in blocks,
  (b) operating the treasury multisig,
  (c) operating a faucet for the planned incentivized testnet
      (audit row `mining-05`),
  (d) publishing the reference wallet software (browser + CLI)?
- If any of (a)-(d) trigger MTL / MSB obligations, what is the
  minimum-viable compliance posture (registration, KYC, reporting,
  recordkeeping)?

### 2.3 OFAC / sanctions-compliance posture

The QSDM validator currently runs at a single physical location
(`api.qsdm.tech`, hosted at DigitalOcean's BLR1 facility). Validator
operations are permissionless once consensus is decentralised;
mining is permissionless from the outset.

- Does the protocol's permissionless mining-and-fees architecture
  create OFAC exposure for the project's contributors? In particular,
  if a sanctioned address transacts on QSDM mainnet, does that
  create exposure for (a) the validator operator, (b) the mining
  pool that included the transaction's parent proof, (c) the QSDM
  project's contributors who publish the reference implementation?
- What mitigation posture (sanctions screening on the validator's
  mempool, address-blacklist enforcement at the protocol level,
  protocol-level OFAC exit, etc.) is technically feasible without
  compromising the protocol's permissionless guarantees, and what
  is counsel's view on its legal sufficiency?

### 2.4 Tax characterisation (U.S. IRS posture)

QSDM is not engaged on tax planning for any individual contributor;
the project's interest is in the protocol-level questions:

- The mining-emission curve creates new CELL on each block. Is the
  miner who produces an accepted proof in receipt of taxable income
  at the moment of issuance, or at the moment of disposition?
  (We note IRS Rev. Rul. 2019-24 and subsequent guidance; we seek
  counsel's view on its application to QSDM specifically.)
- The Genesis treasury (10M CELL) is held by a multisig at genesis.
  Is the project, or are the project's contributors collectively,
  in receipt of taxable income at the moment of mainnet launch?
  What posture mitigates that risk?

### 2.5 Intellectual property and contributor-license posture

The repository is Apache-2.0 licensed. Outside contributors submit
via PR with an implicit Apache-2.0 grant per the LICENSE file.

- Is the Apache-2.0 ICA (inbound = outbound) license model sufficient
  for a coin-issuing protocol of QSDM's complexity, or should the
  project move to a CLA (Contributor License Agreement) model before
  mainnet?
- The "Cell" name and "QSDM" name are tracked separately under
  trademark filing audit row `rebrand-03`. Counsel's view on the
  interaction between the trademark filings and the broader IP
  posture is welcomed.

### 2.6 Jurisdictional questions

The project has contributors in multiple jurisdictions; the
production validator is in India; the canonical legal entity (if
any) is to be determined. Counsel is asked to:

- Identify jurisdictions where the project's footprint is heaviest
  (this requires the project's contributor and infrastructure list,
  provided under engagement).
- Recommend the conventional posture for a project of QSDM's stage:
  e.g. is incorporation of a foundation (Cayman, Swiss, etc.)
  appropriate; should the project remain unincorporated until a
  later milestone; what minimum-viable corporate-form decision
  serves the project's interests pre-mainnet?

## 3. Deliverables

The engagement deliverable is a **written opinion letter** addressed
to the QSDM project lead, on counsel's letterhead, containing:

- A clear answer to each numbered question in §2 (or a documented
  reason why a question is out of scope / cannot be answered without
  further factual development).
- A summary of the regulatory risk posture, with QSDM-actionable
  recommendations sorted by leverage.
- The opinion's footnoted authority chain (statutes, regulations,
  guidance documents, case law) such that QSDM contributors (who
  are not lawyers) can read the opinion and understand what it
  rests on.
- Confidence levels per question — "high / medium / low confidence"
  is sufficient; we don't expect counsel to commit to a binary
  yes/no on questions where the law is genuinely unsettled.
- Any flagged follow-up questions counsel would recommend
  addressing before mainnet launch.

The deliverable is **not** a public-facing legal disclosure. The
opinion letter is privileged; QSDM may reference the existence of
the engagement and the firm's name in public communications subject
to counsel's consent, but the opinion's substance is not for
disclosure.

## 4. What QSDM will provide

On execution of engagement letter:

| Material | Description |
|----------|-------------|
| `CELL_TOKENOMICS.md` | Normative tokenomics spec |
| `REBRAND_NOTES.md` §4 | Phase-0 working hypotheses on tokenomics |
| `pkg/branding/*` source | Authoritative coin-identity constants |
| `pkg/chain/emission.go` source | Integer-only emission calculator |
| Treasury-multisig design doc | Genesis multisig signers, signing threshold, governance proposal — under privileged cover |
| Contributor list | Names + jurisdictions of regular contributors — under privileged cover |
| Infrastructure inventory | Validators, faucet (if any), public endpoints — under privileged cover |
| Prior counsel correspondence | If any (none expected for this engagement) |

## 5. Timeline

QSDM is not currently under any specific regulatory deadline. The
deliverable is gated by the audit-row `tok-01` blocking mainnet
launch, which is itself gated by:

- The mining-protocol audit under audit row `mining-01`
  (`MINING_AUDITOR_RFP.md` — currently being shortlisted).
- The incentivized testnet under audit row `mining-05`.

A realistic target for counsel's opinion letter is **T+8 weeks** from
engagement-letter execution, with weekly progress check-ins. QSDM is
flexible if counsel's review identifies questions that require
substantial additional factual development.

## 6. Commercial terms

Budget guidance is provided for calibration; final terms in the
engagement letter.

| Component | Guidance |
|-----------|----------|
| Opinion-letter engagement | USD low-to-mid five figures (counsel-rate-dependent) |
| Payment structure | retainer + monthly billing; final invoice on opinion-letter delivery |
| Follow-up on a single discrete question (e.g. "we are now considering an exchange listing — does the answer to §2.1 change?") | priced per-engagement at counsel's standard rate |
| Travel | not required; engagement is fully remote |

## 7. Counsel qualifications

The QSDM project seeks counsel with:

- **Demonstrable digital-asset regulatory practice.** Counsel should
  be able to point to (a) public regulatory comment-letter
  submissions on digital-asset rulemakings, (b) prior opinion
  letters on tokenomics for similarly-staged projects (under
  appropriate redactions), or (c) bar-association or conference
  speaking engagements on the topic.
- **Cross-jurisdictional fluency.** Even if the engagement focuses
  on U.S. federal law (with state-law overlay), counsel should be
  comfortable identifying when a jurisdictional question requires
  local-counsel consultation (e.g. for a Cayman foundation or a
  Swiss Stiftung) and either provide that referral or coordinate
  it.
- **Comfort with the technical substrate.** Counsel does not need
  to read Go source, but should be comfortable with the underlying
  cryptographic and consensus concepts at a working level (PoW,
  block production, multisig wallets, smart-contract-free architectures).

## 8. How to engage

Initial discussion at the project lead's discretion. Engagement
letter execution gated on:

- Mutual NDA covering the engagement scope and the contributor list.
- Clear conflict-check (the firm has not represented QSDM-adverse
  parties in any related matter).
- Confirmation of the §2 question list (counsel may propose
  re-ordering, narrowing, or broadening; QSDM will agree or
  document the disagreement).

---

## 9. Internal tracking

This brief is the engagement wrapper for audit row `tok-01`. Once
the engagement letter is signed:

- The audit row's `Notes` field is updated to reference the
  engagement (firm name, engagement-letter date, target opinion-
  letter date).
- The row's `Status` may flip from `pending` to `failed` during the
  active engagement (to surface that the answer is in motion); it
  flips to `passed` on receipt of the opinion letter.
- The opinion-letter delivery date is recorded in `NEXT_STEPS.md`
  (to be created) and in the row's `ReviewedAt` timestamp.

The opinion letter itself is archived under privileged cover; only
its existence, date, and high-level conclusions (if counsel approves)
are referenced in public-facing audit-row notes.
