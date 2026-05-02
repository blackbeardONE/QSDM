# Governance Authority Rotation — Operator Runbook

Triage flow for the 3 alerts in the
`qsdm-v2-governance` group:

| Alert | Severity | Default `for:` | Anchor |
|---|---|---|---|
| `QSDMGovAuthorityVoteRecorded`     | info         | 5m | [§3.1](#31-mode-a--qsdmgovauthorityvoterecorded) |
| `QSDMGovAuthorityThresholdCrossed` | warning      | 1m | [§3.2](#32-mode-b--qsdmgovauthoritythresholdcrossed) |
| `QSDMGovAuthorityCountTooLow`      | **critical** | 5m | [§3.3](#33-mode-c--qsdmgovauthoritycounttoolow) |

> **Why a governance-authority runbook?** Authority
> rotation is the chain's **constitutional layer** —
> the `AuthorityList` is the M-of-N multisig set
> that signs `qsdm/gov/v1` parameter changes and
> votes on its own membership. Mistakes here have
> chain-rotation impact: a rotation that activates
> too soon, on the wrong key, or against a
> too-small authority set is the single biggest
> non-cryptographic governance failure mode the
> chain has. Modes A and B are **operator
> coordination glue** (the alerts ensure every
> multisig member knows a vote happened in case the
> coordinator forgets the out-of-band ping). Mode C
> is the chain's **single-signer hazard** alarm.

Companion observability: counters + gauge in
`pkg/monitoring/gov_metrics.go`, fed from
`pkg/chain/gov_apply.go` (the chain-side applier)
and `pkg/governance/chainparams/authority.go` (the
`AuthorityVoteStore` semantics).

---

## 1. Glossary (60-second skim)

- **AuthorityList** — the live set of public keys
  authorised to sign `qsdm/gov/v1` transactions.
  Size **N**. Rotated by add/remove proposals that
  themselves require multisig approval.
- **M-of-N threshold** — votes required to stage a
  proposal:
  - `N=0` → `M=0` (governance disabled — no
    proposal can ever cross)
  - `N=1` → `M=1` (single-authority bootstrap; the
    lone signer can add a second authority
    unilaterally)
  - `N≥2` → `M=N/2+1` (strict majority)
- **Operations (`op`):** `add` (admit a new
  authority) or `remove` (revoke an existing
  authority). The metric label `op` carries this;
  unrecognised values fall into `other`.
- **`AuthorityVoteStore`** — vote-tally store keyed
  by `(Op, Address, EffectiveHeight)`. Each
  proposal has a `Voters` set; a `Crossed` flag
  flips `false → true` exactly once when the vote
  count reaches threshold.
- **`EffectiveHeight`** — the chain height at which
  a Crossed proposal activates (gets applied to
  the live `AuthorityList`). The window between
  Crossed and EffectiveHeight is the
  **staging window** — multisig members can still
  veto out-of-band by removing themselves or
  re-rotating before activation, but no on-chain
  cancellation primitive exists.
- **Three lifecycle counters** (per `op`):
  - `qsdm_gov_authority_voted_total{op}` — bumps on
    every accepted vote (Mode A's source).
  - `qsdm_gov_authority_crossed_total{op}` — bumps
    once per proposal when its vote tally first
    reaches threshold (Mode B's source).
  - `qsdm_gov_authority_activated_total{op}` —
    bumps once per proposal at activation. Does
    not have a dedicated alert; the
    `qsdm_gov_authority_count` gauge moves with it.
- **`qsdm_gov_authority_count`** — the live
  `AuthorityList` size gauge (Mode C's source).
  Seeded at boot from genesis via
  `SetAuthorityCountGauge` (called from
  `internal/v2wiring`); updated on every
  add/remove activation.
- **Authority-rotation reject reasons** (counter
  `qsdm_gov_authority_rejected_total{reason}`):
  - `authority_already_present` — `op=add` for an
    already-authorised key.
  - `authority_not_present` — `op=remove` for a
    key not in the list.
  - `authority_would_empty` — `op=remove` that
    would push N below 1. **Hard reject.**
  - `duplicate_vote` — same voter casting twice
    for the same `(Op, Address, EffectiveHeight)`
    proposal.
  - `authority_vote_rejected` — generic vote
    validity failure (signature, voter membership,
    etc.).
  - `other` — unmapped reasons.

---

## 2. First-90-seconds checklist

1. **Identify the mode.** The alert name maps 1:1
   to the lifecycle stage:
   - `QSDMGovAuthorityVoteRecorded` → vote stage
     (Mode A).
   - `QSDMGovAuthorityThresholdCrossed` → cross
     stage (Mode B).
   - `QSDMGovAuthorityCountTooLow` → activated
     stage went somewhere unexpected (Mode C).

2. **Mode A is informational.** `severity: info`.
   Wire to a passive channel (chat ping). If the
   alert is paging you out of bed, the
   Alertmanager routing config is wrong.

3. **Mode B is the "is everyone aware?" page.**
   The proposal has crossed threshold and is now
   staged for activation. The window between
   Crossed and `EffectiveHeight` is the last
   chance to coordinate out-of-band — no on-chain
   cancellation exists.

4. **Mode C is the "chain just degenerated" page.**
   The recommended-minimum floor of N=2 has been
   broken. Don't reflexively trigger a rotation
   without first confirming whether the cause is
   a *real* count drop or a wiring bug masking a
   healthy authority list.

5. **Cross-reference the rejected counter.** A
   storm of `qsdm_gov_authority_rejected_total`
   increments alongside Mode A means the
   coordinator's vote-broadcast script has a
   bug — votes are arriving but bouncing.

---

## 3. Modes

### 3.1. Mode A — `QSDMGovAuthorityVoteRecorded`

`increase(qsdm_gov_authority_voted_total[24h]) > 0`
for 5m. Severity: **info**.

#### What triggered it

A multisig member submitted a vote on an
authority-rotation proposal (`op={add,remove}`).
The vote landed on chain (the counter only
increments on accepted votes; rejections flow
through `qsdm_gov_authority_rejected_total`).

This alert is **operator coordination glue** —
multisig members coordinate out-of-band on
rotation proposals (private chat, secure ops
channel) and the alert ensures everyone gets a
notification even if the coordinator forgets the
ping.

#### Symptoms

- `qsdm_gov_authority_voted_total{op="add"}` or
  `qsdm_gov_authority_voted_total{op="remove"}`
  incremented in the last 24h.
- The accumulating proposal is visible via the
  governance read API.

#### Triage

```bash
# What proposal is being voted on? List all open
# proposals with their vote counts and CrossedAt:
curl -s http://127.0.0.1:8080/api/v1/governance/authority/proposals \
  | jq '.proposals[] | {op, address, effective_height, voters: (.voters | length), crossed}'

# Or via the CLI watcher:
qsdmcli watch params

# Cross-reference per-op vote velocity:
sum by (op) (rate(qsdm_gov_authority_voted_total[1h]))

# Check for rejected vote storms (coordinator script bug):
sum by (reason) (rate(qsdm_gov_authority_rejected_total[1h]))
```

| Observation | Probable cause | Action |
|---|---|---|
| Single increment, identifiable proposal, expected coordinator schedule | Normal coordination — a member voted | None. Acknowledge the chat ping; track the proposal toward Mode B. |
| Single increment, identifiable proposal, **unexpected** | A multisig member acted outside the agreed schedule | Reach out via the secure ops channel to confirm intent. The proposal does not stage until threshold; out-of-band veto is still possible. |
| Sustained small bursts of votes + sustained `qsdm_gov_authority_rejected_total{reason="duplicate_vote"}` | A member's vote-broadcast script is retrying | Investigate the script; the duplicate-vote reject is a hard idempotency guard, so the on-chain state is correct, but the operator should fix the retry loop. |
| `qsdm_gov_authority_rejected_total{reason="authority_vote_rejected"}` non-zero | Vote signatures are failing validation OR voter is not in the AuthorityList | Audit the voter's signing setup; cross-reference `/api/v1/governance/authority/list` to confirm membership. |
| Concurrent fire across many `op` labels | Mass-rotation event (post-incident key rotation, scheduled annual rotation) | None — expected during planned mass rotations. Confirm with the coordinator that the schedule matches. |

#### Mitigation

This is an **informational alert**. Mitigation is
"acknowledge the ping" — no chain-side action.

If the rejected-counter path fires alongside, fix
the upstream script; the chain-side state is
correct because the rejects ARE the chain
enforcing its idempotency / membership rules.

---

### 3.2. Mode B — `QSDMGovAuthorityThresholdCrossed`

`increase(qsdm_gov_authority_crossed_total[24h]) > 0`
for 1m. Severity: warning.

#### What triggered it

A proposal's vote tally has reached the M-of-N
threshold and the proposal is now **staged for
activation** at its `EffectiveHeight`. The
counter increments **exactly once** per proposal —
subsequent votes after the cross do not re-fire.

This is louder than Mode A because the staging
window between Crossed and EffectiveHeight is the
**last opportunity to coordinate out-of-band**. No
on-chain cancellation primitive exists; if the
cross was unintended, the only remediation is to
race a counter-rotation proposal that activates
before the staged one.

#### Why this is warning, not info

A vote (Mode A) is reversible — until threshold,
voters can withdraw via duplicate-vote retries
that fail (an idempotency reject preserves the
on-chain state) or via secure-channel veto. A
**Crossed** proposal is one consensus step from
applying. Operators need a louder ping than the
informational vote signal.

#### Symptoms

- `qsdm_gov_authority_crossed_total{op="add"}` or
  `qsdm_gov_authority_crossed_total{op="remove"}`
  incremented.
- The proposal's `Crossed=true` flag is now set
  in `AuthorityVoteStore`; visible via the
  governance read API.
- `qsdm_gov_authority_count` gauge **has not yet
  moved** — activation only happens when the
  chain reaches `EffectiveHeight`.

#### Triage

```bash
# Find the staged proposal:
curl -s http://127.0.0.1:8080/api/v1/governance/authority/proposals \
  | jq '.proposals[] | select(.crossed==true) | {op, address, effective_height, crossed_at_height, voters: (.voters | map(.voter))}'

# Current chain height:
curl -s http://127.0.0.1:8080/api/v1/chain/height | jq .

# Time until activation (assuming ~target_block_time of T seconds):
# blocks_remaining = effective_height - current_height
# seconds_remaining ≈ blocks_remaining * T
```

#### Pre-activation checklist (before `EffectiveHeight`)

1. **Confirm intent.** Coordinator pings every
   member: "Proposal `(op, address, effective_height)`
   crossed with voters `[...]`. Activation at
   block `effective_height`. Anyone want to
   abort?"

2. **Validate the address.** `op=add` admitting
   an unfamiliar pubkey is the canonical
   social-engineering attack — confirm the new
   key was generated on hardware the operator
   trusts.

3. **Validate `EffectiveHeight`.** A staging
   window that's too short doesn't give members
   time to react; too long invites confusion. The
   chain enforces a maximum staging-window per
   `cfg.gov_max_effective_height_offset` (rejects
   beyond it land in
   `qsdm_gov_param_rejected_total{reason="effective_height_too_far"}`),
   but the *minimum* is operator policy.

4. **For `op=remove`:** double-check the post-
   activation count won't trigger Mode C. The
   chain hard-rejects an `op=remove` whose
   activation would push N below 1 (logged as
   `authority_would_empty`), but a remove that
   pushes N to exactly 1 will trip Mode C.

#### Veto path (no on-chain cancel exists)

The chain has no "cancel proposal" tx. If the
crossed proposal must NOT activate, the only
remediation is:

- **Race a counter-rotation.** Submit and cross a
  proposal that, when applied, undoes the staged
  one. Activation order is sorted by
  `EffectiveHeight asc, Op asc, Address asc` — if
  the counter-rotation's `EffectiveHeight` is
  earlier, it lands first and the original may
  fail (e.g. a counter `op=remove X` that lands
  before the original `op=add X` would mean the
  original tries to add an already-removed key…
  but `add` doesn't care about prior removes
  unless `authority_already_present`, so this
  case is brittle). The clean version: a counter
  whose effect is *opposite* and which activates
  AFTER the original.

- **Trip Mode C deliberately.** Not recommended,
  but if the staged rotation is genuinely
  hostile and no counter-rotation is achievable,
  removing other authorities to bring N below 2
  raises the alert and gives the operator a
  paged signal to halt the chain.

#### Mitigation — coordinated activation

If the cross was intended, no operator action is
needed. The chain applies the proposal at
`EffectiveHeight`:

- `qsdm_gov_authority_activated_total{op}`
  increments.
- `qsdm_gov_authority_count` gauge updates to
  the post-activation list size.
- A `proposal-applied` event is emitted on the
  chain event stream.

The alert auto-clears once 24h pass with no new
crosses (the rate-window expires).

---

### 3.3. Mode C — `QSDMGovAuthorityCountTooLow`

`qsdm_gov_authority_count < 2` for 5m. Severity:
**critical**.

#### Why this is critical-severity

An `AuthorityList` of size <2 means **a single
key compromise is a chain-rotation compromise**.
With N=1, M=N/2+1 evaluates to M=1 (special-cased
for the bootstrap path), so the lone signer can
unilaterally add or remove authorities. With N=0,
M=0 means **governance is disabled** — no
proposal can ever cross, and the only remediation
is a hard fork that re-seeds the AuthorityList
from a new genesis-equivalent process.

The recommended minimum of 2 is the chain's
**defence-in-depth floor** — past it, every
governance action becomes single-signer.

#### Two genuine root causes

1. **Real count drop.** An `op=remove` activated
   and pushed N to 1 (or pre-existing genesis
   has N=0/1 from a misconfigured bring-up).
   The chain accepted the rotation; the floor
   was breached.

2. **Wiring bug.** The gauge is stuck at 0 (or
   1) but the actual `AuthorityList` has ≥2
   members. The
   `pkg/monitoring/gov_recorder` adapter that
   bridges chain events to gauge updates has
   broken — most often because a refactor moved
   the boot-time `SetAuthorityCountGauge` call
   without re-wiring it, or the
   `RecordGovAuthorityActivated` callsite
   regressed.

The runbook **explicitly forks on which one** —
the wiring-bug branch is operationally the
opposite of the count-drop branch (no rotation
needed; fix the metric).

#### Symptoms

- `qsdm_gov_authority_count` reads 0 or 1.
- If real: a recent `qsdm_gov_authority_activated_total{op="remove"}`
  increment matches the drop.
- If wiring bug: no recent activation increments,
  but the chain event stream shows recent
  authority-related events; the read API returns
  ≥2 members.

#### Triage — confirm the cause

```bash
# Authoritative answer: how many authorities does
# the live chain say it has?
curl -s http://127.0.0.1:8080/api/v1/governance/authority/list \
  | jq '.authorities | length'

# Compare against the gauge value Prometheus is
# scraping:
curl -s http://127.0.0.1:8080/metrics | grep '^qsdm_gov_authority_count'

# Has there been any recent activation that would
# justify the count change?
curl -s http://127.0.0.1:8080/metrics | grep '^qsdm_gov_authority_activated_total'
```

#### Decision tree

```
Compare API list length L to the gauge G:

L == G == <2:
  REAL count drop. Authority list genuinely below
  the floor.
  ──> Branch A (below).

L >= 2  AND  G == 0 (or G < 2 with G != L):
  WIRING BUG. The chain has the right authority
  count but the metric is stuck.
  ──> Branch B (below).

L < 2  AND  L != G:
  IMPOSSIBLE in practice — would require the API
  layer and the metric to be reading from
  different sources of truth. File a P1 bug; the
  list and the gauge MUST come from the same
  underlying state.
```

#### Branch A — Real count drop

The chain genuinely has too few authorities. Two
sub-cases:

**Sub-case A1: planned testnet bring-up.**
Genesis seeded with N=1 for bootstrap; the first
rotation will admit a second authority. **No
incident.** Silence Mode C until N reaches 2.
The standard route is to add a silence rule in
Alertmanager scoped to the bootstrap deployment
label.

**Sub-case A2: production count drop.** An
`op=remove` activated unexpectedly. This is
serious:

1. Identify the activation. The chain event
   stream will have a `proposal-applied` for the
   most recent remove.
2. Confirm the remove was intended. Check the
   coordinator's records.
3. **Race an `op=add` proposal.** With N=1, the
   single remaining authority can add a new
   authority unilaterally (M=1 special case).
   Submit, sign, and self-activate as fast as
   the chain will let you. Once the new addition
   crosses and activates, N=2 and the floor is
   restored.
4. **If the remaining authority's key is
   compromised:** the chain is in single-signer
   compromise. Halt the chain and coordinate a
   hard-fork recovery.

#### Branch B — Wiring bug

The list is healthy; the metric is wrong.
Operator action is on the metric, not the chain:

1. Verify with the API that the list is genuinely
   ≥2 (re-run the `length` query above).
2. Check the chain event stream for recent
   activations that should have moved the gauge.
   If activations fired but the gauge didn't
   move, `RecordGovAuthorityActivated` is broken
   (or its callsite was removed).
3. Restart the validator process. The boot path
   calls `SetAuthorityCountGauge` from
   `internal/v2wiring` to seed the gauge from
   genesis state; a restart will re-emit.
4. If the gauge stays stuck post-restart, the
   wiring is genuinely broken — file a P1 bug
   and ship a fix to `pkg/monitoring/gov_recorder`.

#### Mitigation — Branch A2 (real production drop)

```bash
# Construct the rescue add-proposal:
# (Concrete tx submission depends on the operator
#  tooling; consult MINING_PROTOCOL_V2.md §gov for
#  the wire format.)

# Pre-flight: confirm threshold semantics:
curl -s http://127.0.0.1:8080/api/v1/governance/authority/list | jq '.authorities | length'
# Result must be 1 for the unilateral-add path to
# be available; result 0 means hard-fork required.
```

The unilateral-add path is the chain's escape
hatch from Branch A2 with `N=1`. With `N=0`,
governance is disabled and only a hard fork can
re-seed the `AuthorityList`.

#### Recovery validation

```promql
qsdm_gov_authority_count >= 2
```

The alert auto-clears once the gauge crosses 2
for one full evaluation window past `for: 5m`.

---

## 4. Cross-mode + cross-runbook escalation

| Concurrent alerts | Most likely root | Action |
|---|---|---|
| Mode A only | Normal coordination — a vote was cast | Acknowledge; track toward Mode B if the proposal accumulates votes |
| Mode A + sustained `authority_vote_rejected` rejects | Coordinator's vote-broadcast script has a bug (signing failure, stale state) OR a malicious vote is being submitted by a non-authority | Audit the rejected counters by reason; cross-check `/api/v1/governance/authority/list` for the voter |
| Mode B alone | A proposal staged successfully | §3.2 pre-activation checklist; coordinate or veto in the staging window |
| Mode B `op=remove` followed by Mode C | The staged remove activated and pushed N below the floor | §3.3 Branch A — race a unilateral `op=add` if N=1; hard-fork if N=0 |
| Mode C alone, post-restart | Wiring bug | §3.3 Branch B |
| Mode C alone, no recent activations, no restart | Either an undetected pre-existing wiring bug OR genesis was misconfigured | §3.3 decision tree — compare API list length to the gauge value |
| Mode C + chain-liveness alert | Authority drop has wedged the chain (fork rules require multisig signoff and there's no quorum to sign) | [`MINING_LIVENESS.md`](MINING_LIVENESS.md) takes precedence; governance recovery is gated on chain liveness |
| Mode C + slashing of an authority key | The slashed authority is one of N; the chain dropped its votes via `DropVotesByAuthority` and the count gauge moved | This is normal post-slash behaviour; cross-reference [`SLASHING_INCIDENT.md`](SLASHING_INCIDENT.md). The recovery path is the same as §3.3 Branch A |

---

## 5. Reference

- **Source files:**
  - [`pkg/monitoring/gov_metrics.go`](../../../source/pkg/monitoring/gov_metrics.go)
    — `qsdm_gov_authority_*` counters + gauge
  - [`pkg/governance/chainparams/authority.go`](../../../source/pkg/governance/chainparams/authority.go)
    — `AuthorityVoteStore` semantics, threshold
    formula, vote-rejection enum
  - [`pkg/chain/gov_apply.go`](../../../source/pkg/chain/gov_apply.go)
    — chain-side applier (records the counters,
    drives activation)
  - `pkg/governance/chainparams/admit.go` —
    pre-applier validity checks (signature,
    voter membership, schema)
  - `internal/v2wiring/v2wiring.go` —
    `SetAuthorityCountGauge` boot wiring
- **API endpoints:**
  - `GET /api/v1/governance/authority/list` —
    current authority members (the truth source
    against which Mode C Branch B is verified).
  - `GET /api/v1/governance/authority/proposals`
    — open + crossed proposals with vote
    tallies.
- **Prometheus series:**
  - `qsdm_gov_authority_voted_total{op}` —
    Mode A's source.
  - `qsdm_gov_authority_crossed_total{op}` —
    Mode B's source.
  - `qsdm_gov_authority_activated_total{op}` —
    activation counter (no dedicated alert; the
    gauge moves with it).
  - `qsdm_gov_authority_count` — Mode C's source
    gauge.
  - `qsdm_gov_authority_rejected_total{reason}` —
    per-reason vote/proposal rejects (bounded
    enum, six values).
- **Threshold formula** (`pkg/governance/chainparams.AuthorityThreshold`):
  - `N=0` → `M=0` (governance disabled)
  - `N=1` → `M=1` (bootstrap special case)
  - `N≥2` → `M=N/2+1` (strict majority)
- **Companion runbooks:**
  - [`SLASHING_INCIDENT.md`](SLASHING_INCIDENT.md)
    — slashing of a key that happens to be in
    the `AuthorityList` cascades into a Mode C
    via `DropVotesByAuthority` indirectly when
    the slashed key is also removed via gov.
  - [`MINING_LIVENESS.md`](MINING_LIVENESS.md) —
    Mode C alongside chain-stuck means the
    authority drop has wedged the chain; gov
    recovery is gated on chain liveness.
  - [`OPERATOR_GUIDE.md`](../OPERATOR_GUIDE.md)
    — multisig coordination context.
  - [`MINING_PROTOCOL_V2.md`](../MINING_PROTOCOL_V2.md)
    — `qsdm/gov/v1` tx schema.

---

## 6. Alert ↔ Mode quick-reference

| Alert                                | Mode | Severity     | Triage section |
| ------------------------------------ | ---- | ------------ | -------------- |
| `QSDMGovAuthorityVoteRecorded`       | A    | info         | §3.1           |
| `QSDMGovAuthorityThresholdCrossed`   | B    | warning      | §3.2           |
| `QSDMGovAuthorityCountTooLow`        | C    | **critical** | §3.3           |
