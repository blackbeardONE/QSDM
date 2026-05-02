# Submesh Policy Incident — Operator Runbook

Triage flow for the 2 alerts in the `qsdm-submesh`
group:

| Alert | Severity | Default `for:` | Anchor |
|---|---|---|---|
| `QSDMSubmeshP2PRejects`        | warning | 5m | [§3.1](#31-mode-a--qsdmsubmeshp2prejects) |
| `QSDMSubmeshAPISustained422`   | warning | 5m | [§3.2](#32-mode-b--qsdmsubmeshapisustained422) |

> **What is the submesh policy gate?** Before a
> transaction reaches consensus admission or
> validation, it must pass the **submesh policy gate**
> — a fee/geotag → submesh route lookup followed by a
> per-route `max_tx_size` cap. Transactions that fail
> are dropped at the gate; **validation never runs on
> them** (they don't increment the rejected-tx
> counter). The submesh gate is the chain's
> traffic-shaping layer: it routes economic load to
> the right submesh and enforces per-submesh size
> budgets so one bad submesh can't fill the mempool
> with payloads the others can't carry.

> **Why a dedicated runbook?** These two alerts catch
> the **operator-policy-mismatch** failure mode (client
> sends fee/geotag/size that doesn't match the
> validator's `submesh_config`), distinct from the
> **adversarial-traffic** failure mode that
> `QUARANTINE_INCIDENT.md` covers. The submesh gate
> here is the **per-tx policy decision**; the
> quarantine manager is the **per-submesh aggregate
> response** to sustained policy hits. When this
> runbook's alerts fire alone, it's a config drift
> issue. When they fire alongside
> [`QUARANTINE_INCIDENT.md`](QUARANTINE_INCIDENT.md)
> alerts, the policy hits have crossed the threshold
> for whole-submesh isolation — see §4 below.

Companion observability: five counters in
`pkg/monitoring/submesh_metrics.go`, recorded from
two separate code paths:

| Counter | Path | Triggered by |
|---|---|---|
| `qsdm_submesh_p2p_reject_route_total`             | libp2p (`cmd/qsdm/transaction/transaction.go`) | `submesh.ErrSubmeshNoRoute` — fee/geotag does not match any loaded submesh |
| `qsdm_submesh_p2p_reject_size_total`              | libp2p                                         | `submesh.ErrSubmeshPayloadTooLarge` — message exceeds matched submesh's `max_tx_size` |
| `qsdm_submesh_api_wallet_reject_route_total`      | HTTP `POST /wallet/send` (`pkg/api/handlers.go`) | `EnforceWalletSendPolicy` returned `ErrSubmeshNoRoute`; client receives **HTTP 422** |
| `qsdm_submesh_api_wallet_reject_size_total`       | HTTP `POST /wallet/send`                         | `EnforceWalletSendPolicy` returned `ErrSubmeshPayloadTooLarge`; client receives **HTTP 422** |
| `qsdm_submesh_api_privileged_reject_size_total`   | HTTP mint / token-create                          | `EnforcePrivilegedLedgerPayloadCap` returned `ErrSubmeshPayloadTooLarge`; client receives **HTTP 422** |

(Privileged paths use the *strictest* `max_tx_size`
across all submeshes and have no route check — privilege
is enforced at admission, not at the submesh layer.)

---

## 1. Glossary (60-second skim)

- **Submesh** — a logical partition of the chain
  identified by fee tier, geotag, and a per-route
  `max_tx_size` budget. Each submesh has a route
  (fee + geotag matcher) and a size cap. The set of
  submeshes loaded at runtime is the
  **`submesh_config`**.
- **Route match** — a transaction matches a submesh
  iff its `fee` falls in the submesh's fee range
  AND its `geo_tag` is in the submesh's allowlist.
  Failing the match is `ErrSubmeshNoRoute`.
- **Size cap** — once a route matches, the
  transaction's serialized size is compared against
  that submesh's `max_tx_size`. Exceeding is
  `ErrSubmeshPayloadTooLarge`.
- **Privileged operations** — mint and token-create
  paths use a *single global cap* (the strictest
  `max_tx_size` across all submeshes). No route
  match — privilege is gated separately at admission.
- **`EnforceWalletSendPolicy(fee, geoTag, txBytes)`**
  — API-side enforcement. Returns the typed error;
  handlers convert to HTTP 422.
- **`MatchP2POrReject(fee, geoTag, msg)`** — P2P-side
  enforcement. Same logic; rejects are silently
  dropped (no client-visible response — libp2p is
  fire-and-forget).
- **422 Unprocessable Entity** — the HTTP status
  code returned to API clients on submesh-policy
  reject. The chain considers the request
  syntactically valid but operationally
  out-of-policy.
- **Validation never runs.** The submesh gate is
  *upstream* of validation; rejected txs do NOT
  increment `qsdm_transactions_invalid_total` and do
  NOT consume validator CPU. This is by design —
  the gate is cheap (string compare + integer
  compare) and trims load before expensive checks.

---

## 2. First-90-seconds checklist

1. **Read both axes.**
   ```promql
   # Per-counter rates over the last hour:
   topk(5, rate(qsdm_submesh_p2p_reject_route_total[15m]))
   topk(5, rate(qsdm_submesh_p2p_reject_size_total[15m]))
   topk(5, rate(qsdm_submesh_api_wallet_reject_route_total[10m]))
   topk(5, rate(qsdm_submesh_api_wallet_reject_size_total[10m]))
   topk(5, rate(qsdm_submesh_api_privileged_reject_size_total[10m]))
   ```
   The counter names tell you the path (P2P vs API
   wallet vs API privileged) and the reason (route
   vs size). The dominant counter is the dominant
   failure mode.

2. **Decide: client problem or server problem?**
   - **Route rejects only** ⇒ client is sending
     fee/geotag combinations that don't match any
     loaded submesh. Either client config drift OR
     validator's `submesh_config` was reloaded with
     a tighter route set.
   - **Size rejects only** ⇒ client is sending
     payloads larger than the matched submesh's
     `max_tx_size`. Either client framing changed
     (new field, larger encoding) OR
     `submesh_config` `max_tx_size` was lowered.
   - **Both, simultaneously** ⇒ a client-side
     migration is in flight (new client version
     that emits both new geotags AND larger
     payloads); freeze rollouts while triaging.

3. **Cross-reference quarantine state.** Sustained
   policy hits at the gate eventually trigger the
   QuarantineManager to isolate the offending
   submesh. If `qsdm_quarantine_submeshes > 0`
   concurrently with these alerts, the cascade is
   already in motion — see §4.

4. **Don't relax the policy reflexively.** The
   submesh gate is a *correctness* mechanism (it
   enforces the per-submesh economic invariant),
   not a *throttling* mechanism. Lowering the cap
   to silence the alert can leak invalid traffic
   into validation; raising the cap without
   verifying the underlying submesh's storage
   budget can cause a downstream OOM.

---

## 3. Modes

### 3.1. Mode A — `QSDMSubmeshP2PRejects`

`increase(qsdm_submesh_p2p_reject_route_total[15m]) > 0
or increase(qsdm_submesh_p2p_reject_size_total[15m]) > 0`
for 5m. Severity: warning.

#### What triggered it

A peer is sending libp2p transactions that fail the
submesh policy gate. Either the fee/geotag combination
doesn't match any loaded submesh route
(`ErrSubmeshNoRoute`), or the message exceeds the
matched submesh's `max_tx_size` (`ErrSubmeshPayloadTooLarge`).

The offending tx is dropped silently — libp2p is
fire-and-forget — and the producer has no immediate
feedback. Sustained activity means the producer
either:

- doesn't know its txs are being dropped (no client-
  side visibility into the drop counter), or
- knows and is retrying without fixing the upstream
  fee/geotag/size config.

#### Symptoms

- One or both `qsdm_submesh_p2p_reject_*_total`
  counters incrementing during the alert window.
- No corresponding increase in
  `qsdm_transactions_invalid_total` (the gate is
  upstream of validation).
- Mempool size may be **lower than expected** for
  the observed peer count — txs are being shed
  before they get a chance to enter the mempool.
- If the gate is the only filter dropping the
  traffic, `rate(qsdm_transactions_processed_total)`
  may be lower than the peer's send rate.

#### Triage

```promql
# Which sub-reason dominates?
sum(rate(qsdm_submesh_p2p_reject_route_total[15m]))
sum(rate(qsdm_submesh_p2p_reject_size_total[15m]))

# Has the loaded submesh_config changed recently?
# (No direct gauge — read the validator process
# logs for "submesh_config reload" lines, or compare
# the running config snapshot against the file
# under cfg.SubmeshConfigPath.)
```

| Dominant counter | Probable cause | Action |
|---|---|---|
| `route_total` only | Client-side fee/geotag drift OR validator just reloaded a `submesh_config` with tighter routes | Compare client's emitted fee/geotag distribution against the loaded routes; if validator-side reload was recent, audit the rollout against the client release notes |
| `size_total` only | Client-side payload-size drift (new field added, encoding got fatter) OR validator just lowered a submesh's `max_tx_size` | Inspect a recent oversize message via libp2p tap or peer logs; cross-reference the matched submesh's `max_tx_size` |
| Both in roughly equal proportion | Client migration in flight that touches both fee/geotag AND payload encoding | Pause the migration; the new client version is incompatible with the validator's loaded submesh_config |
| Concentrated on one peer (logs name a single multiaddr) | Single misconfigured peer | Reach out; their producer is mis-tuned. Other peers are unaffected |
| Spread across many peers | Fleet-wide config drift (a release shipped that changed fee/geotag/size client-side without coordinating with the validator's submesh_config) | Roll back the client release OR fast-track a coordinated submesh_config reload on validators |

#### Mitigation

- **Client-side fix** — when the producer is
  identifiable. Operator-side action: confirm the
  fix, observe the counters return to baseline.
- **Validator-side `submesh_config` reload** — when
  the loaded config is the source of the drift
  (recent reload tightened routes / lowered sizes
  inappropriately). Reload the previous config; the
  gate's stateless evaluation means rejects stop
  immediately on the next tx.
- **Do NOT silently drop the alert.** The libp2p
  layer has no client-visible signal; an operator
  silencing the alert without identifying the
  producer leaves a population of peers permanently
  unable to participate. The visible signal is the
  alert itself.

#### Recovery validation

```promql
rate(qsdm_submesh_p2p_reject_route_total[5m]) == 0
rate(qsdm_submesh_p2p_reject_size_total[5m]) == 0
```

The alert auto-clears once both counters stop
incrementing for one full evaluation window past
`for: 5m`.

---

### 3.2. Mode B — `QSDMSubmeshAPISustained422`

`(increase(qsdm_submesh_api_wallet_reject_route_total[10m])
+ increase(qsdm_submesh_api_wallet_reject_size_total[10m])
+ increase(qsdm_submesh_api_privileged_reject_size_total[10m])
) >= 5` for 5m. Severity: warning.

#### What triggered it

API clients are getting **HTTP 422** from at least
one of three submesh-policy endpoints:

- `POST /wallet/send` — wallet transfer rejected
  for `route` or `size`.
- Mint / token-create — privileged ledger op
  rejected for `size` only (no route check on
  privileged paths).

The 422 is client-visible, so unlike Mode A the
producer **does** know their request was rejected —
but if they're retrying without fixing the
underlying cause, the alert paints.

The summed-counter expression and ≥5 threshold give
the alert noise immunity: a single mistuned wallet
client doesn't trip it, but a sustained
mis-rollout does.

#### Symptoms

- HTTP 422 responses to `/wallet/send` and/or mint
  / token-create endpoints.
- One or more `qsdm_submesh_api_*_reject_*_total`
  counters incrementing.
- Wallet UX impact: transfers that worked on a
  previous client version are now bouncing.
- Service-account producers (automated minters,
  market-maker bots, etc.) generating sustained
  retry traffic — visible as elevated request rate
  on the submesh-policy endpoints.

#### Triage

```promql
# Which endpoint?
rate(qsdm_submesh_api_wallet_reject_route_total[10m])
rate(qsdm_submesh_api_wallet_reject_size_total[10m])
rate(qsdm_submesh_api_privileged_reject_size_total[10m])

# Is the API server healthy in absolute terms?
# (Filter out spurious 422s from a broken handler.)
sum(rate(qsdm_http_responses_total{code="422"}[10m]))
```

| Dominant counter | Probable cause | Action |
|---|---|---|
| `wallet_reject_route_total` | Wallet client has stale fee/geotag defaults; or new submesh_config dropped a route the wallet was using | Audit wallet client release vs. validator's loaded routes; coordinate a rollout |
| `wallet_reject_size_total` | Wallet client started attaching a memo / metadata field that pushed payloads past the cap; or `max_tx_size` was lowered on the matched submesh | Inspect a 422 response body for the typed error; compare current vs prior wallet release framing |
| `privileged_reject_size_total` | Mint / token-create caller is sending payloads above the strictest cap. Most often a service-account producer hit a payload-size regression | Check the privileged caller's binary; tighten its payload generator |
| Mixed | Multi-endpoint client (a single SDK that hits both wallet and privileged paths) shipped a release with a payload-shape change | Roll back the SDK client side; validator is correct |

#### Mitigation — client-side

- Wallet UX: pin minimum wallet version that knows
  the current `submesh_config`; ship a wallet update
  if the validator's reloaded config has shifted.
- Service-account producer: add a payload-size
  guard upstream of the API call; the producer
  should never hit the cap.

#### Mitigation — validator-side

- **Reload `submesh_config`** if the policy was
  tightened in error. Reload is online; existing
  in-flight requests don't observe the change.
- **Privileged-path tuning** — the strictest
  `max_tx_size` is the floor; raising it is a
  global operation that affects every privileged
  caller. Don't tune it for one bad producer;
  fix the producer.

#### Recovery validation

The summed-rate expression returns to <5 / 10m on
its own once the offending caller is fixed. No
operator action needed beyond verifying clients
have rolled forward.

---

## 4. Cross-mode + cross-runbook escalation

The submesh policy gate doesn't directly cause
quarantine — it's the *aggregate* of policy hits
that the QuarantineManager reads. A single submesh
seeing sustained P2P rejects from many peers will
eventually be quarantined. The mapping:

| Concurrent alerts | Most likely root | Action |
|---|---|---|
| Mode A only | P2P-side config drift (libp2p clients) | §3.1 |
| Mode B only | API-side config drift (wallet / privileged callers) | §3.2 |
| Mode A + Mode B | Fleet-wide release drift hitting both paths simultaneously | §3.1 + §3.2 in parallel; pause rollouts |
| Mode A or B + [`QUARANTINE_INCIDENT.md`](QUARANTINE_INCIDENT.md) Mode A | Sustained submesh-policy hits have triggered per-submesh quarantine | Submesh-policy is the **upstream cause**; quarantine is the response. Fix the policy hit (this runbook); quarantine clears once the rate drops below the threshold OR the operator calls `RemoveQuarantine` |
| Mode A or B + [`QUARANTINE_INCIDENT.md`](QUARANTINE_INCIDENT.md) Mode B | Majority of submeshes are quarantined AND the gate is rejecting from multiple sides simultaneously — a fleet-wide release bug | [`QUARANTINE_INCIDENT.md`](QUARANTINE_INCIDENT.md#32-mode-b--qsdmquarantinemajorityisolated) takes precedence; submesh-policy hits are downstream confirmation. Roll back the most-recent change first |
| Mode A or B + chain-liveness alert | Quarantine has isolated enough peers that consensus is wedging | [`MINING_LIVENESS.md`](MINING_LIVENESS.md) takes precedence |

---

## 5. Reference

- **Source files:**
  - [`pkg/submesh/manager.go`](../../../source/pkg/submesh/manager.go)
    — submesh policy implementation (route + size
    enforcement, error types)
  - [`pkg/monitoring/submesh_metrics.go`](../../../source/pkg/monitoring/submesh_metrics.go)
    — five counters + Record/Count function pairs
  - [`pkg/api/handlers.go`](../../../source/pkg/api/handlers.go)
    — API-side enforcement (`EnforceWalletSendPolicy`,
    `EnforcePrivilegedLedgerPayloadCap`)
  - [`cmd/qsdm/transaction/transaction.go`](../../../source/cmd/qsdm/transaction/transaction.go)
    — P2P-side enforcement (`MatchP2POrReject`)
- **Error types** (in `pkg/submesh`):
  - `ErrSubmeshNoRoute` — fee/geotag did not match
    any loaded submesh.
  - `ErrSubmeshPayloadTooLarge` — exceeded the
    matched submesh's `max_tx_size`. (Privileged
    paths use the strictest cap globally.)
- **Configuration:**
  - `cfg.SubmeshConfigPath` — file containing the
    loaded submesh routes + per-route
    `max_tx_size` budgets. Reloadable online.
- **Prometheus series:**
  - `qsdm_submesh_p2p_reject_route_total`
  - `qsdm_submesh_p2p_reject_size_total`
  - `qsdm_submesh_api_wallet_reject_route_total`
  - `qsdm_submesh_api_wallet_reject_size_total`
  - `qsdm_submesh_api_privileged_reject_size_total`
- **Companion runbooks:**
  - [`QUARANTINE_INCIDENT.md`](QUARANTINE_INCIDENT.md)
    — when sustained submesh-policy hits trigger
    per-submesh isolation. This runbook is the
    upstream cause; QUARANTINE is the aggregate
    response.
  - [`MINING_LIVENESS.md`](MINING_LIVENESS.md) —
    when quarantine has isolated enough validators
    that consensus stalls.
  - [`REJECTION_FLOOD.md`](REJECTION_FLOOD.md) —
    distinct subsystem (per-§4.6-rejection forensic
    ring), but the operator pattern of
    "sustained-rejects-then-quarantine" is parallel
    and may co-occur during a fleet-wide attack.
  - [`OPERATOR_HYGIENE_INCIDENT.md`](OPERATOR_HYGIENE_INCIDENT.md)
    — when submesh-policy is rejecting 100% of
    inbound P2P traffic (fleet-wide route mismatch
    or `max_tx_size` lowered to zero), the hygiene
    runbook's Mode D
    (`QSDMNoTransactionsStored`) co-fires as the
    "chain admitting txs but storing none"
    sentinel. SUBMESH_POLICY is the upstream gate;
    OPERATOR_HYGIENE Mode D is the throughput
    symptom.

---

## 6. Alert ↔ Mode quick-reference

| Alert                             | Mode | Severity | Triage section |
| --------------------------------- | ---- | -------- | -------------- |
| `QSDMSubmeshP2PRejects`           | A    | warning  | §3.1           |
| `QSDMSubmeshAPISustained422`      | B    | warning  | §3.2           |
