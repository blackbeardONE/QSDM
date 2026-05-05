# Hot-Reload — Operator Runbook

Two-mode runbook for the runtime config hot-reload
subsystem. Mode A catches **sustained apply failures**
(live config swaps being rejected by the in-process
reload path); Mode B is the lower-severity precursor
case where **the on-disk config can't even pass a
dry-run** — the next planned apply is going to fail
unless the config is fixed.

| Alert | Severity | Default `for:` | Anchor |
|---|---|---|---|
| `QSDMHotReloadApplyFailures`    | warning | 10m | [§3.1](#31-mode-a--qsdmhotreloadapplyfailures)    |
| `QSDMHotReloadDryRunDegraded`   | info    | 30m | [§3.2](#32-mode-b--qsdmhotreloaddryrundegraded)   |

> **What this runbook closes.** The hot-reload subsystem
> already exposed five Prometheus counters and four
> gauges (`qsdm_hot_reload_*` in
> `pkg/monitoring/prometheus_scrape.go`), but no alert
> ever fired against them. A jammed apply path (live
> swaps rejected) or a dry-run that can't even parse
> the on-disk file were both invisible to alerting until
> a downstream subsystem broke.

---

## 1. Glossary (60-second skim)

- **Hot-reload** — the in-process config swap
  mechanism. Lets operators change validator settings
  without a process restart. Driven by
  `pkg/config.HotReloader` and the admin dry-run
  endpoint.
- **Apply** — a real config swap. Increments
  `qsdm_hot_reload_apply_success_total` or
  `qsdm_hot_reload_apply_failure_total`.
- **Dry-run** — a non-mutating check: load the on-disk
  config, validate it against policy, but don't swap
  the live config. Used by the admin dashboard to give
  operators a "would this apply?" preview. Updates the
  four `qsdm_hot_reload_last_dry_run_*` gauges.
- **`last_dry_run_load_ok`** — 1 if the last dry-run
  successfully loaded and parsed the on-disk file;
  0 if parsing failed.
- **`last_dry_run_policy_ok`** — 1 if the loaded
  config also passed the policy guard (e.g., minimum
  authority count, valid TLS material); 0 otherwise.
- **`last_dry_run_timestamp`** — Unix time of last
  dry-run; 0 if none has been attempted on this
  process. Both alerts gate on `timestamp > 0` so
  cold-start nodes don't fire.

---

## 2. Pre-flight: which side of the reload pipeline failed?

Apply failures (Mode A) and dry-run failures (Mode B)
are usually correlated, but the cause is in different
places:

- **Mode A only** (apply fails, dry-run is fine) →
  the reload hooks themselves are jamming. Often a
  subsystem-specific `Apply()` returning an error
  during the swap window.
- **Mode B only** (dry-run fails, no apply attempts) →
  a config file edit landed on disk but no apply has
  been triggered yet. The next apply will fail.
- **Both** → the on-disk config is broken AND someone
  has tried to apply it. Dry-run was the precursor.

---

## 3. Per-mode triage

### 3.1 Mode A — `QSDMHotReloadApplyFailures`

**Severity:** warning. **Default `for:`** 10m.

**Fires when**: `rate(qsdm_hot_reload_apply_failure_total[5m]) > 0`
sustained for ≥10m.

**Why this matters**: live config swaps are being
rejected. The validator is running on the previous
config until the swap path is fixed. Any operator
intent expressed via config (rate-limit tweaks,
authority adds, submesh policy edits) is silently NOT
in effect.

**Triage**:

1. **Inspect node logs** for hot-reload error lines
   (search for `hot_reload`, `Apply`, or
   `reload failed`). The validator surfaces
   subsystem-specific reload errors.
2. **Check Mode B**: if
   `QSDMHotReloadDryRunDegraded` is co-firing, the
   on-disk file is broken — fix that first, then the
   apply path will recover.
3. **Common subsystem failure causes**:
   - Authority-list shrink below the threshold
     (cross-check `QSDMGovAuthorityCountTooLow`).
   - Submesh-policy validator rejecting the new
     policy (cross-check
     `QSDMSubmeshAPISustained422`).
   - TLS material rotation that left an invalid cert
     in place.
4. **Lock-contention check**: if a deploy script is
   hammering the apply endpoint (e.g.,
   curl-in-a-loop), the apply path's mutex can wedge.
   Throttle the script and retry.

**Companions:**
[`GOVERNANCE_AUTHORITY_INCIDENT.md`](GOVERNANCE_AUTHORITY_INCIDENT.md)
(authority-list reload failures cascade through this
alert),
[`SUBMESH_POLICY_INCIDENT.md`](SUBMESH_POLICY_INCIDENT.md)
(submesh-policy reload failures cascade through this
alert),
[Mode B below](#32-mode-b--qsdmhotreloaddryrundegraded)
(precursor signal — fix dry-run first).

---

### 3.2 Mode B — `QSDMHotReloadDryRunDegraded`

**Severity:** info. **Default `for:`** 30m.

**Fires when**:
`qsdm_hot_reload_last_dry_run_load_ok == 0` OR
`qsdm_hot_reload_last_dry_run_policy_ok == 0`,
sustained for ≥30m, AND
`qsdm_hot_reload_last_dry_run_timestamp > 0`
(filters out cold-start nodes that haven't run a
dry-run yet).

**Why this matters**: the last dry-run reports the
on-disk config either doesn't parse cleanly
(`load_ok=0`) or fails the policy guard
(`policy_ok=0`). The next planned apply WILL fail —
this is the precursor signal to Mode A.

**Triage**:

1. **Distinguish load vs. policy**:
   ```promql
   qsdm_hot_reload_last_dry_run_load_ok == 0
   qsdm_hot_reload_last_dry_run_policy_ok == 0
   ```
   - **`load_ok == 0`**: the YAML / JSON file itself
     is malformed. Check the most-recent edit on disk:
     ```sh
     git -C <config-dir> log --oneline -10
     ```
     Roll back or fix.
   - **`policy_ok == 0`**: the file parses but fails
     a policy invariant (authority count too low,
     TLS expiry, etc.). The validator log line tells
     you which invariant failed. Fix the config or
     widen the policy.
2. **Check `last_dry_run_changed`**: if it's 1, the
   file changed since the previous dry-run. The change
   broke something. Bisect.
3. **Don't apply until dry-run is green**. Mode B is
   a soft signal precisely because it's
   pre-incident — the validator is still running on
   the previous (good) config. The recovery is
   "fix the on-disk file"; there's no urgency at the
   live-traffic level.

**Companions:**
[Mode A above](#31-mode-a--qsdmhotreloadapplyfailures)
(downstream — Mode B usually precedes Mode A by the
time-between-dry-run-and-next-apply),
[`GOVERNANCE_AUTHORITY_INCIDENT.md`](GOVERNANCE_AUTHORITY_INCIDENT.md)
(`policy_ok=0` due to low authority count),
[`SUBMESH_POLICY_INCIDENT.md`](SUBMESH_POLICY_INCIDENT.md)
(`policy_ok=0` due to invalid submesh policy).

---

## 4. Cross-references

- `pkg/monitoring/prometheus_scrape.go` —
  `qsdm_hot_reload_*` exposition surface (5 counters
  + 4 gauges).
- `pkg/monitoring/metrics.go` — `Metrics` struct
  fields backing the counters
  (`HotReloadApplySuccess`, `HotReloadApplyFailure`,
  `HotReloadDryRunTotal`, `LastHotReloadDryRunAt`,
  `LastHotReloadDryRunChanged`,
  `LastHotReloadDryRunPolicyOK`,
  `LastHotReloadDryRunLoadOK`).
- `pkg/config.HotReloader` and the admin dry-run
  endpoint — increment / mutate the metrics.
- `QSDM/deploy/prometheus/alerts_qsdm.example.yml` —
  `qsdm-hot-reload` group.
- `QSDM/deploy/grafana/dashboards/qsdm-runbook-hot-reload-incident.json`
  — auto-generated panel.
- [`GOVERNANCE_AUTHORITY_INCIDENT.md`](GOVERNANCE_AUTHORITY_INCIDENT.md)
  (authority-list reload failures).
- [`SUBMESH_POLICY_INCIDENT.md`](SUBMESH_POLICY_INCIDENT.md)
  (submesh-policy reload failures).
