# Trademark Filing Intake — `rebrand-03`

> **Status:** Intake packet for trademark counsel. The QSDM project
> has adopted the "QSDM" name for the protocol and "Cell" /
> "CELL" for the native coin since the Phase-0 rebrand. The names
> are in active in-tree use (`pkg/branding/*` is the authoritative
> source) and on the public website (`qsdm.tech`), but no
> trademark filings have been initiated.
>
> **Audit row:** `rebrand-03` (severity: medium, category: rebrand).
> Once filings are submitted (USPTO + selected international
> jurisdictions), the audit row moves from `pending` to `passed`
> with `Notes` referencing the filing numbers.
>
> **Distribution:** privileged + confidential, attorney-client work
> product. Distribute only to engaged trademark counsel under
> engagement letter.

---

## 1. Marks for which protection is sought

### 1.1 Primary marks

| Mark | Type | Used since | Use case |
|------|------|-----------|----------|
| **QSDM** | wordmark | 2025-Q3 (estimate; project lead to confirm) | software protocol name |
| **Quantum-Safe Distributed Mining** | wordmark (expansion of QSDM) | 2025-Q3 | software protocol name |
| **Cell** | wordmark | 2026-Q1 (post-rebrand from initial "QSDM" token name) | native coin name |
| **CELL** | wordmark (symbol) | 2026-Q1 | native coin symbol |

### 1.2 Design marks (logos)

Status: the project's website at `qsdm.tech` uses logo assets that
have evolved over time. A canonical design-mark version is **not
yet locked**; counsel should advise on whether to file the current
logo as-is or wait for a final design.

Open question for counsel: is it conventional to file the wordmarks
first and the design-mark later when the design is in flux, or to
wait and file together?

### 1.3 Composite marks

| Mark | Notes |
|------|-------|
| `Cell (CELL)` | typical reference style in QSDM documentation. Filing as a composite mark would protect the visual pairing without filing the parenthetical separately. |
| `qsdm.tech` | domain name; trademark filing for a domain is unconventional and probably not appropriate. Listed for completeness. |

## 2. Classes

The QSDM project intends to operate in two Nice Classification
classes initially:

### 2.1 Class 9 — software / downloadable software

- Downloadable computer software for cryptographic distributed-
  ledger consensus.
- Downloadable computer software for cryptographic wallets.
- Downloadable computer software for proof-of-work mining of
  digital tokens.

### 2.2 Class 42 — software-as-a-service / technology services

- Software-as-a-service featuring software for distributed-ledger
  protocols.
- Hosted computer services featuring digital-ledger consensus
  validation.
- Technical consultation related to distributed-ledger software.

### 2.3 Additional classes to discuss with counsel

- **Class 36** — financial services. Whether the project's
  operation of a Genesis treasury, a faucet, or a validator
  constitutes a "financial service" in the trademark sense is a
  judgment call counsel should make. The substantive question of
  whether QSDM is a money-services-business is being handled
  separately under the `tok-01` counsel engagement; the trademark-
  class question is independent of that.
- **Class 35** — advertising / business services. May apply to
  the project's leaderboard / community-development activities.
  Discuss with counsel.
- **Class 41** — education / entertainment. Unlikely to apply at
  the project's current scope; listed only for completeness.

## 3. Jurisdictions

The QSDM project does not have a canonical legal entity yet (see
the `tok-01` counsel engagement for the corporate-form question).
Trademark filings are typically made in (a) the jurisdictions of
the project's contributors, (b) the jurisdictions of the project's
infrastructure footprint, and (c) the jurisdictions of the
project's anticipated user base.

### 3.1 First-tier jurisdictions (file at launch)

| Jurisdiction | Rationale |
|--------------|-----------|
| **United States (USPTO)** | Largest English-speaking market; conventional first filing for a software-first protocol; provides a foundation for Madrid Protocol filings later. |
| **European Union (EUIPO)** | Single filing covers all 27 EU member states; cost-effective; project contributors include EU residents. |
| **India** | Production validator hosted at DigitalOcean's BLR1 (Bangalore) facility. Filing in the jurisdiction of operational infrastructure is conventional. |

### 3.2 Second-tier jurisdictions (Madrid Protocol via USPTO base, post-launch)

To be discussed with counsel based on (a) the actual contributor
and user-base footprint at the time of filing, and (b) the
project's commercial expansion plans.

Candidate jurisdictions for discussion:

- United Kingdom (post-Brexit, separate from EUIPO).
- Switzerland (conventional for foundation-style projects).
- Singapore (regional hub).
- Japan, South Korea (large cryptocurrency markets).
- Australia, Canada (common-law, English-speaking).

### 3.3 Jurisdictions NOT being pursued

- **China.** First-to-file system requires defensive filings; the
  project's footprint there is currently nil; we are choosing not
  to pursue defensive filings at this time. Counsel should flag if
  this posture is materially risky.
- **All other jurisdictions** not enumerated above. Filings can
  always be added later via Madrid Protocol if the project's
  footprint expands.

## 4. Prior-art search

Trademark counsel should perform full availability searches on
each proposed mark in each proposed jurisdiction before filing.
Project lead's preliminary observations (not a substitute for the
counsel's professional search):

- **"QSDM"** — short, distinctive, and (to our knowledge) not in
  prominent use by any conflicting party. Confidence: medium.
  Counsel's search will produce a more reliable answer.
- **"Quantum-Safe Distributed Mining"** — descriptive in part
  ("distributed mining" of cryptocurrency); the "quantum-safe"
  modifier may be the distinctive component. Counsel's view on
  registrability is welcomed.
- **"Cell"** — common dictionary word with many existing
  trademark registrations across many classes. The class-9 / class-42
  registrations for crypto-related uses are the relevant prior
  art; project lead is aware of several adjacent uses but has not
  done a comprehensive search. **This is the highest-risk mark.**
  Counsel should specifically address: is "Cell" registrable in
  class 9 / class 42 for the QSDM use case, and if not, what is
  the conventional fallback (e.g. file "Cell (CELL)" as a
  composite, file "CELL" alone, etc.).
- **"CELL"** — as a symbol it is shorter and more distinctive than
  the wordmark "Cell"; may have fewer prior-art conflicts.
  Counsel's view welcomed.

## 5. Specimens of use

Counsel will need evidence of the marks' use in commerce. The
project's existing materials that can serve as specimens:

| Mark | Specimen sources |
|------|------------------|
| QSDM | `qsdm.tech` website (multiple pages); GitHub repository README at `github.com/blackbeardONE/QSDM`; release binaries named `qsdm.linux-amd64` etc.; the QSDM API responses include the protocol name in their `X-QSDM-Version` header |
| Cell, CELL | `pkg/branding.CoinName = "Cell"`, `pkg/branding.CoinSymbol = "CELL"`; documentation at `QSDM/docs/docs/CELL_TOKENOMICS.md`; in-app references throughout the wallet UI |

The marks' actual first-use date should be confirmed via git-blame
on `pkg/branding/*` constants. Project lead will provide a
chronology under privileged cover.

## 6. Engagement scope

The engagement asks counsel to:

1. Perform availability searches on each proposed mark in each
   first-tier jurisdiction (§3.1).
2. Advise on registrability, including any descriptiveness or
   confusion-likelihood concerns.
3. File applications in the first-tier jurisdictions for each
   mark deemed registrable.
4. Maintain prosecution against any opposition or office action.
5. Advise on second-tier-jurisdiction strategy (§3.2) post-
   first-tier filings.

Out of scope for this engagement:

- Enforcement actions against third-party infringers. Separate
  retainer.
- Litigation. Separate retainer.
- Coordination with the corporate-form decision (under `tok-01`).
  Trademark counsel may flag interactions but should not duplicate
  the corporate counsel's work.

## 7. Timeline

| Phase | Calendar window | Notes |
|-------|-----------------|-------|
| Availability search | T+0 to T+3 weeks | Counsel produces a written search report covering each first-tier mark |
| Filing decisions | T+3 to T+4 weeks | Project lead reviews search report; confirms which marks to file |
| Initial filings | T+4 to T+6 weeks | USPTO / EUIPO / India intent-to-use or use-based filings as appropriate |
| Prosecution | T+6 weeks to T+18 months | Standard USPTO timeline; office actions handled on the firm's standard SLA |
| Second-tier filings (Madrid Protocol) | post first-tier registration | Decided at the time |

## 8. Commercial terms

Budget guidance:

| Component | Guidance |
|-----------|----------|
| Availability search (all first-tier marks) | USD low five figures |
| USPTO filing (per class, per mark) | USPTO fee + counsel's filing fee |
| EUIPO filing (single application, multiple classes) | EUIPO fee + counsel's filing fee |
| India filing (per class, per mark) | India fee + counsel's filing fee |
| Office-action response (per response) | counsel's hourly rate |
| Total budget (first-tier filings, no office actions) | USD low-to-mid five figures |

Counsel may propose a flat-fee engagement for the search + initial
filings; the project lead is open to that.

## 9. Counsel qualifications

The project seeks counsel with:

- **U.S. trademark practice with software / fintech experience.**
  Counsel should have a demonstrable track record of registering
  marks in classes 9 and 42, especially for blockchain or
  cryptocurrency projects.
- **International filing capability,** either in-house or through
  a reliable network of foreign-associate firms. The first-tier
  filings span three jurisdictions; counsel must be comfortable
  coordinating all three.
- **Prosecution capability.** Filing is the easy part; defending
  against office actions and oppositions is what determines
  whether the mark actually registers.

## 10. How to engage

Project lead to initiate via the firm's standard engagement
process. Engagement-letter terms:

- Mutual NDA covering the contents of this intake packet.
- Conflict check (the firm has not represented adverse parties on
  the proposed marks or in cryptocurrency-adjacent trademark
  disputes).
- Confirmation that the firm can handle the §3.1 jurisdictions
  directly or through its foreign-associate network.

---

## 11. Internal tracking

This packet is the engagement wrapper for audit row `rebrand-03`.
On filing, the row's `Notes` field is updated to reference (a) the
counsel firm, (b) the filing application numbers, (c) the filing
dates. The row's `Status` flips from `pending` to `failed` (active-
prosecution) and then to `passed` on first-tier registration of at
least the "QSDM" wordmark + the "Cell"/"CELL" mark (whichever
form survives counsel's search).
