# `trustcheck` — independent scraper for QSDM attestation transparency

`trustcheck` is a single-binary, stdlib-only HTTP client that hits a QSDM
validator's public trust endpoints and validates every response against
the anti-claim contracts laid out in `Major Update.md` §8.5.2–§8.5.4.

It exists so that third-party monitoring services, journalists, and
operators can independently verify a validator's "X of Y attested"
transparency surface **without** trusting QSDM-maintained SDKs or the
node's own dashboard.

## Build

```powershell
# from QSDM/source/
go build -o trustcheck.exe ./cmd/trustcheck
```

The command has no non-stdlib dependencies, so it cross-compiles cleanly
for any `GOOS`/`GOARCH` target Go supports.

## Usage

```
trustcheck [flags]

Flags:
  -base string          Base URL of the validator HTTP surface (default "https://api.qsdm.tech")
  -timeout duration     HTTP timeout per request (default 10s)
  -limit int            limit query for /recent endpoint (default 50)
  -allow-warmup         Treat HTTP 503 "aggregator warming up" as success (exit 0)
  -allow-disabled       Treat HTTP 404 "operator opted out" as success (exit 0)
  -json                 Emit machine-readable JSON instead of a checklist

Exit codes:
  0  all assertions passed
  1  usage / network / HTTP-level error
  2  one or more contract assertions failed
  3  endpoint returned 503 warming-up / 404 disabled (downgrade with -allow-*)
```

## What it checks

On `/api/v1/trust/attestations/summary`:

- `total_public ≥ attested` (denominator always present and ≥ numerator).
- Never "attested > 0 && total_public == 0" — prevents §8.5.2's
  "X without denominator" footgun.
- `scope_note` is the §8.5.2 verbatim string.
- `fresh_within` parses as a Go `time.Duration`.
- `ngc_service_status ∈ {healthy, degraded, outage}`.
- `last_checked_at` is RFC3339 and within one hour of local clock.
- `last_attested_at` (when present) is RFC3339.
- `ratio` matches `attested / total_public` within 0.01 (or is 0 when
  `total_public == 0`).

On `/api/v1/trust/attestations/recent?limit=N`:

- `count` equals the length of the `attestations` array.
- `fresh_within` is identical to summary (both endpoints must agree on
  the same window).
- `count ≤ summary.attested` — stale entries must not leak through.
- Every `node_id_prefix` contains the `…` ellipsis (redaction rule).
- `node_id_prefix` values are unique within a single feed.
- `region_hint ∈ {eu, us, apac, other}`.
- `attested_at` is RFC3339.
- `fresh_age_seconds ≥ 0` and monotonically non-decreasing across rows
  (rows must be ordered newest-first, per §8.5.3).

Every assertion is surfaced in the checklist output so failures are
immediately identifiable.

## CI example

```yaml
- name: Validate trust endpoints
  run: |
    ./trustcheck -base https://validator.example.tld -json > trust.json
    cat trust.json | jq -e '.pass'
```

## Security note

`trustcheck` performs **only read-only GET** requests against public
endpoints. It sends no credentials and stores no state. It is safe to
run continuously in a cron job against arbitrary validators.

It does **not** verify the *truthfulness* of any attestation — only the
*shape* and *anti-claim discipline* of the transparency surface. Actual
NGC proof validation is the responsibility of the node that produced
the attestation; this tool intentionally trusts the aggregate numbers
the way any third-party observer would.
