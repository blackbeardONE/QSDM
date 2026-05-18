# External-engagement requests — index

> Status board for the four wall-clock-blocked audit rows. These rows
> cannot be flipped from `pending` to `passed` by in-tree code or
> docs alone — they require an external party (an auditor, counsel,
> the market, or a trademark office) to act. Each engagement has a
> wrapper artefact under this directory that turns the wall-clock
> wait into a request-in-flight.
>
> The score-impact analysis: as of 2026-05-18, the internal audit
> checklist is at 95.40% (83 of 87 passed). The four rows tracked
> here are the entirety of what's left. If all four close cleanly,
> the score goes to 100.00% (87 of 87). (Baseline was 81/85 when
> this index was first drafted on 2026-05-16; the totals moved
> to 86 on 2026-05-17 when `infra-04` — Public security-disclosure
> file (RFC 9116) — was added and pre-flipped to passed in the
> same commit, then to 87 on 2026-05-18 when `infra-05` — Sitemap
> lastmod freshness contract, script-enforced — was added in
> commit `80c7faf` and pre-flipped to passed. The four wall-clock-
> blocked rows here are unchanged across both transitions; only
> the denominator and the passed-count moved. See the Rolling
> status entries below.)

## Status board

| Row | Severity | Wrapper | Engagement state | Target close |
|-----|----------|---------|------------------|--------------|
| `mining-01` | critical | [`MINING_AUDITOR_RFP.md`](./MINING_AUDITOR_RFP.md) | Drafted, awaiting project-lead sign-off + distribution list | T+14 weeks from kickoff |
| `tok-01` | critical | [`COUNSEL_BRIEF_TOKENOMICS.md`](./COUNSEL_BRIEF_TOKENOMICS.md) | Drafted, awaiting project-lead engagement of counsel | T+8 weeks from engagement-letter execution |
| `mining-05` | medium | [`TESTNET_LAUNCH_PLAN.md`](./TESTNET_LAUNCH_PLAN.md) | Plan drafted; faucet + leaderboard + announcement still to build | T+14 days from public launch |
| `rebrand-03` | medium | [`TRADEMARK_FILING_INTAKE.md`](./TRADEMARK_FILING_INTAKE.md) | Drafted, awaiting project-lead engagement of trademark counsel | T+18 months for first-tier registration |

## What this directory is for

Each row's wrapper is **the document QSDM sends to the external
party**. It is not a substitute for the in-tree technical evidence
(which lives in the rest of `QSDM/docs/docs/` and in the source
itself); it is the engagement-level cover that turns "we have all
the technical evidence" into "we have asked the right person to
look at it".

A wrapper is:

- **Self-contained.** A counsel firm or auditor receiving the
  wrapper should be able to understand the engagement scope, the
  commercial terms, and the next step without reading the entire
  QSDM repo.
- **Linked to the in-tree evidence.** Every wrapper points to the
  source-of-truth docs the external party will need to consume.
- **Audited for stale references.** When the wrapper references
  audit-row scores, in-tree docs, or release tags, those references
  are pinned to the date the wrapper was drafted; periodic re-
  review is the project lead's responsibility.

## Dependency graph

```
                          mainnet launch
                                ▲
                                │
              ┌─────────────────┼─────────────────┐
              │                 │                 │
         tok-01            mining-01          mining-05
        (counsel)         (auditor)          (testnet)
              │                 │                 │
              │                 │                 │
              │                 └─────── (mining-05 can launch
              │                           in parallel with         
              │                           mining-01 active)        
              │
              └─── rebrand-03 (independent of mainnet; can complete in parallel)
```

Reading: `tok-01` and `mining-01` are mainnet-launch gates and are
independent of each other. `mining-05` is a mainnet-launch gate but
can launch in parallel with `mining-01`. `rebrand-03` is
independent of mainnet launch entirely; it should run on its own
schedule.

## Once a wrapper is sent

The audit-row `Notes` field is updated to reference:

- The wrapper artefact (this directory).
- The engaged firm / counsel / counterparty (if disclosable; some
  engagements are NDA'd before disclosure).
- The engagement-letter date.
- The target deliverable date.

The audit-row `Status` field transitions:

```
pending  ──(wrapper sent)──▶  pending  ──(engagement signed)──▶  failed
                                                                    │
                                                       (deliverable received)
                                                                    │
                                                                    ▼
                                                                passed
```

The `failed` interim state is correct: it surfaces to the audit
checklist that the row's answer is in motion and unconfirmed; once
the deliverable arrives, the row flips to `passed`.

## Rolling status

Maintained by the project lead. Update this section each time a
wrapper moves states.

| Date | Row | Update |
|------|-----|--------|
| 2026-05-16 | all four | wrappers drafted under `QSDM/docs/docs/audit/` |
| 2026-05-18 | (board-level) | total-row count refreshed from 85 → 86 after `infra-04` (RFC 9116 security.txt) was added 2026-05-17 and pre-flipped to passed; the four wall-clock-blocked rows tracked here are unchanged. Score-impact analysis above updated from 95.29% (81/85) baseline to 95.35% (82/86) and the "all four close" target updated from 85/85 to 86/86. No engagement-state changes. |
| 2026-05-18 | (board-level) | second total-row count refresh of the day, 86 → 87 after `infra-05` (Sitemap lastmod freshness contract, script-enforced) was added in commit `80c7faf` and pre-flipped to passed. The four wall-clock-blocked rows tracked here are again unchanged. Score-impact analysis above updated from 95.35% (82/86) baseline to 95.40% (83/87) and the "all four close" target updated from 86/86 to 87/87. No engagement-state changes. |
| (next) | | (to be filled) |

## Related artefacts

- [`AUDIT_PACKET_MINING.md`](../AUDIT_PACKET_MINING.md) — technical
  reading guide for the `mining-01` auditor.
- [`CELL_TOKENOMICS.md`](../CELL_TOKENOMICS.md) — normative
  tokenomics reference for the `tok-01` counsel.
- [`MINING_PROTOCOL.md`](../MINING_PROTOCOL.md) — normative protocol
  spec.
- [`REBRAND_NOTES.md`](../REBRAND_NOTES.md) — Phase-0 working
  hypotheses, including the trademark-relevant naming history.
- [`MINER_QUICKSTART.md`](../MINER_QUICKSTART.md) — the user-facing
  doc the `mining-05` testnet will lean on.
- `pkg/audit/checklist.go` — the source of truth for every audit
  row's status and notes.

> **A note on the project's planning-notes posture.** The project's
> `.gitignore` excludes `**/NEXT_STEPS.md`, `BUILD_STATUS.md`,
> `PROJECT_STATUS.md`, and similar planning-narrative files by
> deliberate convention (these tend to go stale faster than they
> can be cleaned up, and end up cluttering history). This file —
> `EXTERNAL_REQUESTS.md` — is the project's *tracked* status
> board, and is the canonical place for engagement state. A local
> contributor may keep an untracked `NEXT_STEPS.md` alongside the
> repo for personal chronological notes; that file does not feed
> back into the audit-row Notes or the engagement state machine.
