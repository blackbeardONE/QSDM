# Changelog

All user-visible changes to QSDM are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
uses [Semantic Versioning](https://semver.org/) for tagged releases.

Historical project activity prior to the Major Update rebrand (Phases
1–5, executed 2026-04-22) is captured verbatim in
[`QSDM/docs/docs/history/MAJOR_UPDATE_EXECUTED.md`](QSDM/docs/docs/history/MAJOR_UPDATE_EXECUTED.md)
and the `QSDM/docs/docs/archive/` folder; this file does **not**
attempt to retroactively enumerate that history.

## [Unreleased]

### Added

- **Recent-rejection ring on-disk persistence — restart no
  longer wipes §4.6 forensic record (2026-04-29).**
  Closes the explicit "out of scope" placeholder in
  `pkg/mining/attest/recentrejects`'s package doc: the ring
  was volatile by design, and every restart wiped the entire
  forensic record of arch-spoof / hashrate-band / CC-subject
  rejections. Production validators now configure
  `Config.RecentRejectionsPath` in `internal/v2wiring` to
  point the ring at a JSONL log under the state directory;
  Wire() opens or creates the file, attaches it to the
  `recentrejects.Store`, and replays prior records into the
  in-memory ring at boot. Empty path = legacy in-memory-only
  posture, fine for ephemeral testnets.
  - **New `recentrejects.Persister` interface + no-op default
    + `FilePersister` implementation in
    `pkg/mining/attest/recentrejects/persistence.go`.** The
    Persister is narrow (Append / LoadAll / Close), mirrors
    the dependency-inversion shape of `MetricsRecorder`, and
    keeps a future SQLite or rotation-aware backend behind
    the same surface without touching the Store's call sites.
    `FilePersister` is the production implementation:
    append-only JSONL, per-call open/close (≈10 µs syscall
    overhead per record — 0.1% CPU at 100 rejections/s),
    crash-recovery framing that prepends a leading newline if
    the prior write tailed mid-record, and corruption-tolerant
    `LoadAll` that skips malformed JSON lines so a partial
    write at the file's tail does not block boot.
  - **Bounded growth via soft-cap compaction.** Default
    `softCap = recentrejects.DefaultMaxRejections` (1024
    records). Every 1024 successful Appends the persister
    rewrites the file, keeping only the most recent 1024
    records (write to `<path>.tmp`, atomic rename onto
    `<path>` — same crash-safe pattern as
    `chainparams.SaveSnapshotWith`). Worst-case on-disk
    footprint is ≈ 512 KiB before compaction fires; recovered
    footprint is ≈ 256 KiB. A malicious miner cannot use the
    persister as a DoS vector to fill the disk.
  - **`Store.SetPersister(p)` + `Store.RestoreFromPersister()`
    + `Store.PersistErrorCount()` API surface.** Setter is
    idempotent and accepts nil to revert to the no-op
    default; `RestoreFromPersister` is the explicit one-shot
    boot replay that fails loud on a second invocation
    (catches double-restore wiring bugs); `PersistErrorCount`
    returns the cumulative count of `Persister.Append`
    failures observed by `Record()` (forensic dashboards
    join this with the new Prometheus mirror counter).
  - **Best-effort persistence semantics.** A failed Append
    does NOT roll back the in-memory record — operators can
    still see the rejection live via
    `GET /api/v1/attest/recent-rejections`, and the
    `qsdm_attest_rejection_persist_errors_total` counter +
    `Store.PersistErrorCount()` accessor surface the
    filesystem failure independently. The forensic ring is
    operator telemetry, not consensus state; degraded
    durability is recoverable on the next successful Append.
  - **New Prometheus series
    `qsdm_attest_rejection_persist_errors_total`** in
    `pkg/monitoring/recentrejects_metrics.go`. Unlabeled
    (filesystem failures are not field-keyed). The
    monitoring adapter implements the new optional
    `recentrejects.PersistErrorRecorder` interface alongside
    the existing `MetricsRecorder` so a method drop is a
    compile-time error rather than a silent dashboard gap.
  - **`internal/v2wiring.Wire()` integration.** Two new
    Config fields:
    - `RecentRejectionsPath string` — empty = legacy in-memory-only
      ring; non-empty = construct `FilePersister`,
      `SetPersister`, `RestoreFromPersister`. Construction
      failure is non-fatal and routed through
      `LogRecentRejectionsError`; the ring degrades to
      in-memory-only rather than aborting boot.
    - `LogRecentRejectionsError func(error)` — invoked on
      construction failure and on boot-time restore failure.
      Per-record Append failures are NOT routed here (too
      noisy under filesystem flap); they bump
      `qsdm_attest_rejection_persist_errors_total` for
      dashboard / alert use.
  - **New audit checklist row** `store-04` under
    `CatStorage` (severity Medium): "Recent-rejection ring
    persistence bounded + corruption-tolerant" with explicit
    acceptance criteria for the 0600 file mode, JSONL
    framing, atomic-rename compaction, hard-kill recovery
    behaviour, and the persist-errors metric coverage.
  - **Test coverage:**
    - `pkg/mining/attest/recentrejects/persistence_test.go`
      (16 tests): round-trip Append/LoadAll, missing-file
      tolerance, corrupt-line tolerance after a simulated
      hard kill, soft-cap compaction (both no-trim and
      trim paths), Store restore populates the ring,
      Restore respects in-memory cap (drops oldest beyond
      `Cap()`), Restore reseeds the Seq counter, Restore
      double-call fails loud, no-op persister default,
      Record fires Append, PersistErrorCount increments on
      failing persister, concurrent Append (8 workers ×
      50 iterations under `-race`), empty-path rejection,
      default soft-cap, SetPersister(nil) reverts to noop.
    - `pkg/monitoring/recentrejects_metrics_test.go`
      (3 new tests): persist-error counter increments on
      non-nil error, nil error is a no-op, end-to-end
      adapter routing from `Store.Record` through the
      dependency-inverted chain to the Prometheus counter.
    - `internal/v2wiring/v2wiring_recentrejects_persist_test.go`
      (4 tests): empty path → no-op persister, non-empty
      path → on-disk Append, restart survival across two
      Wire() calls, unwritable path surfaces via
      `LogRecentRejectionsError` without crashing boot.
  - **Backward compatibility.** Pre-existing
    `recentrejects.MetricsRecorder` implementations need no
    change — `PersistErrorRecorder` is a separate optional
    interface the Store probes via type assertion. Tests
    that construct a Store without setting a persister
    continue to behave exactly as before (no filesystem
    dependency, no behavioural change).

- **Recent-rejection ring truncation telemetry — operators can
  now alert on cap pressure before it goes silent (2026-04-29).**
  Closes the observability gap on the
  `pkg/mining/attest/recentrejects` ring's defensive
  rune-truncation layer: every Record() call truncates
  `Detail` to 200 runes, `GPUName` and `CertSubject` to 256
  runes (defending the validator against a malicious miner
  stuffing megabyte attestation fields), but until now those
  truncations were invisible — operators discovered them by
  noticing `qsdmcli watch archspoof --detailed` output ended
  with `…` and grep'ing source for the cap.
  - **New dependency-inverted
    `recentrejects.MetricsRecorder` interface + no-op default
    + `SetMetricsRecorder` setter** mirrors the
    `mining.MiningMetricsRecorder` and
    `mining.RejectionRecorder` posture: pkg/mining/attest/recentrejects
    declares the narrow surface in `metrics.go` so it stays
    independent of pkg/monitoring (the import cycle the
    inversion exists to break), and pkg/monitoring's new
    `recentrejects_recorder.go` registers a Prometheus-backed
    adapter at `init()` time.
  - **`Store.Record()` now observes pre-truncation rune
    counts on every non-empty observed field.** New
    `observeAndTruncate(fieldName, s, cap)` helper wraps the
    existing `truncateRunes` clamp with a single
    `atomic.Value`-backed recorder call; one rune-slice
    allocation per non-empty field (matching the prior cost)
    plus an interface dispatch. Empty fields skip the
    recorder entirely so HMAC-only paths (CertSubject empty)
    and CC-only paths (GPUName empty) do not skew the
    truncation-rate denominator.
  - **Three new Prometheus series in
    `pkg/monitoring/recentrejects_metrics.go`:**
    - `qsdm_attest_rejection_field_runes_observed_total{field}`
      — denominator for the truncation rate; one increment
      per non-empty observed field per `Record()` call.
    - `qsdm_attest_rejection_field_truncated_total{field}`
      — numerator; only increments when the pre-truncation
      rune count exceeded the in-store cap.
    - `qsdm_attest_rejection_field_runes_max{field}`
      — process-lifetime monotonic max gauge (CAS loop on
      `atomic.Uint64`); the "how close are we to the cap?"
      headroom signal. Resets only on process restart.
    Cardinality: 3 series families × 3 fields = 9 series,
    well under any best-practice ceiling. Unknown field
    names from a future code path are silently ignored so
    the cardinality bound holds even under a typo regression.
    Negative rune counts are clamped to 0 so an
    arithmetic-bug under-flow cannot wedge the gauge at
    `MaxUint64`.
  - **`prometheus_scrape.go` integration.** `corePrometheusMetrics()`
    now emits the three new series families next to the
    existing `qsdm_attest_archspoof_rejected_total{reason}` and
    `qsdm_attest_hashrate_rejected_total{arch}` counters, in a
    stable (detail, gpu_name, cert_subject) order so dashboard
    PromQL expressions can rely on a fixed series shape.
  - **Two new example Prometheus alert rules in
    `QSDM/deploy/prometheus/alerts_qsdm.example.yml`** under
    a new `qsdm-v2-attest-recent-rejections` group:
    - `QSDMAttestRejectionFieldTruncationSustained` — fires
      when `rate(truncated)/rate(observed) > 25%` over 10m
      for any field, with a denominator guard (rate(observed)
      > 0) so quiet nodes do not page on 0/0.
    - `QSDMAttestRejectionFieldRunesMaxNearCap` — info-only
      leading indicator: fires when `runes_max` is within 10%
      of the cap (180/200 for detail, 230/256 for gpu_name
      and cert_subject) for >30m, so operators see the ramp
      before the truncation-rate alert paints.
  - **Test coverage.** 19 new unit tests:
    - 9 in `pkg/mining/attest/recentrejects/metrics_test.go`
      covering observeAndTruncate firing on non-empty fields,
      skipping empty fields, pre-truncation rune count
      preservation, the cap-vs-cap+1 boundary on the
      truncated flag, the no-op default, the
      `SetMetricsRecorder(nil)` revert path, full
      `Store.Record()` integration with all three fields,
      absent-field skipping on HMAC-only rejections,
      cap-pressure on a CC mismatch, and an `atomic.Value`
      concurrent-swap smoke (1000 records × 50 swaps with no
      lost ObserveField calls).
    - 6 in
      `pkg/monitoring/recentrejects_metrics_test.go`
      covering observed/truncated/runes_max bucketing by
      field, unknown-field cardinality bound, negative-rune
      clamp, runes_max monotonicity (CAS loop), labelled-
      output stable ordering, and the init()-time adapter
      registration smoke (drives a real `Store.Record()`
      through the production wiring and asserts the
      monitoring counters incremented).
    - Plus a compile-time assertion that the adapter
      satisfies the recorder interface.
    Total: `pkg/mining/attest/recentrejects` 18 → 27 tests;
    `pkg/monitoring` adds 6 tests (recentrejects-side).
  - **MINER_QUICKSTART note.** New `## §4.6 telemetry — recent-
    rejection ring truncation` operator subsection documents
    the three series, how to derive the truncation rate via
    PromQL, and which constants to bump in
    `pkg/mining/attest/recentrejects/recentrejects.go` if
    sustained truncation indicates the caps are too tight.
  - **Backward-compatible.** A pure-recentrejects build
    (e.g. a unit test that depends only on
    `pkg/mining/attest/recentrejects` without
    `pkg/monitoring`) keeps the no-op default recorder and
    runs unchanged. Production binaries that link
    `pkg/monitoring` get the Prometheus-backed adapter the
    moment the binary's `init()` chain fires; no
    configuration change required.

- **Structured `*archcheck.RejectionDetail` wrapper — `gpu_name`
  and `cert_subject` now populate end-to-end on §4.6 rejections
  (2026-04-29).** Closes the "GPUName / CertSubject empty
  end-to-end" caveat noted in the prior commit. The outer
  verifier never sees the bundle's gpu_name or the leaf cert's
  subject directly — both live inside the per-type verifier
  (`pkg/mining/attest/{hmac,cc}/`) — so until now the
  `recent-rejections` ring populated those fields with empty
  strings.
  - **New `archcheck.RejectionDetail` error wrapper** carries
    the offending value (`GPUName` on HMAC paths,
    `CertSubject` on CC paths, raw `GPUArch` on outer-arch
    paths) plus the matched/expected `Patterns`. Implements
    `Unwrap()` returning the canonical sentinel
    (`ErrArchUnknown` / `ErrArchGPUNameMismatch` /
    `ErrArchCertSubjectMismatch`) so every existing
    `errors.Is(err, archcheck.ErrArch*)` call site keeps
    working byte-for-byte.
  - **`ValidateOuterArch`,
    `ValidateBundleArchConsistencyHMAC`, and
    `ValidateBundleArchConsistencyCC` now return
    `*RejectionDetail`** instead of bare `fmt.Errorf("%w: ...")`.
    The rendered `Error()` string is preserved verbatim
    (operator log lines do not visibly drift).
  - **Outer verifier traverses the wrapper via `errors.As`.**
    `recordRejectionForArchSpoof` now extracts `GPUName` /
    `CertSubject` from the structured detail attached to the
    error chain — works through the per-type verifier's
    `fmt.Errorf("hmac: %w: %w", err,
    mining.ErrAttestationSignatureInvalid)` double-wrap (Go
    1.20+ multi-`%w`). Outer-verifier signature simplified:
    `recordRejectionForArchSpoof(err, p)` — no more
    placeholder `gpuName, certSubject` args.
  - **Wire surface unchanged.** Every JSON consumer of
    `/api/v1/attest/recent-rejections` and
    `qsdmcli watch archspoof --detailed` automatically starts
    receiving populated `gpu_name` / `cert_subject` fields the
    moment a node deploys this binary; no client-side code
    change required.
  - **Test coverage.** 19 new unit / integration tests
    (1973 → 1992 module-wide):
    - 14 in
      `pkg/mining/attest/archcheck/rejection_detail_test.go`
      covering `errors.Is` parity for all three sentinels,
      `errors.As` extraction (HMAC/CC/outer-unknown,
      including the per-type verifier's double-`%w`), Error()
      string parity (allowed-list suffix on outer-unknown,
      gpu_name + patterns on HMAC, cert_subject on CC, empty
      gpu_name special case), nil-detail safety, and
      defensive Patterns-slice copying.
    - 3 in
      `pkg/mining/verifier_recentrejects_test.go` driving the
      verifier hot path with real `archcheck.Validate*`
      returns wrapped under `ErrAttestationSignatureInvalid`,
      validating that `RejectionEvent.GPUName` /
      `.CertSubject` surface automatically end-to-end.
    - 2 in
      `internal/v2wiring/v2wiring_recentrejects_test.go`
      locking the round trip through the production HTTP
      handler: a record with `GPUName` populated round-trips
      to `view.Records[0].GPUName`; same for `CertSubject`.
  - **Backward-compatible.** Older deployments running the
    prior binary against this new client / dashboard simply
    continue to emit empty-string `gpu_name` / `cert_subject`
    — the omitempty JSON tags drop them from the wire, and
    consumers handle absence the same way they handle
    "rejection happened on a path that doesn't carry that
    detail".

- **`/api/v1/attest/recent-rejections` endpoint — per-event detail
  companion to the §4.6 archspoof / hashrate Prometheus counters
  (2026-04-29).** Closes the "out of scope" caveat shipped with
  `qsdmcli watch archspoof`: where the counters answer "how
  many rejections by reason/arch?" the new endpoint answers
  "*who* got bounced, *what* did they claim, *which* leaf cert
  subject was rejected?" without round-tripping through metrics
  scrape or grepping validator logs.
  - **New package `pkg/mining/attest/recentrejects`** — bounded
    FIFO ring of structured `Rejection{Seq, RecordedAt, Kind,
    Reason, Arch, Height, MinerAddr, GPUName, CertSubject,
    Detail}` records (default cap 1024, ~256 KiB saturated).
    Cursor-based pagination via monotonic `Seq`; binary-search
    cursor lookup keeps page reads O(log n + page_size). All
    string fields are length-clamped at write time (Detail at
    200 runes, GPUName/CertSubject at 256) so a malicious
    miner cannot OOM the validator with megabyte attestation
    payloads.
  - **Dependency-inverted `mining.RejectionRecorder` hook** —
    new `mining.SetRejectionRecorder(...)` mirrors the existing
    `MiningMetricsRecorder` posture: pkg/mining declares the
    narrow interface + a no-op default, internal/v2wiring
    installs the bounded ring at boot. Verifier hot path adds
    one atomic.Load + interface dispatch per §4.6 rejection
    alongside the existing metrics-counter call. Fires on
    `archcheck.ValidateOuterArch` failure (kind
    `archspoof_unknown_arch`),
    `archcheck.ValidateClaimedHashrate` failure
    (`hashrate_out_of_band`), and per-type verifier
    `ErrArchGPUNameMismatch` /
    `ErrArchCertSubjectMismatch` returns
    (`archspoof_gpu_name_mismatch` /
    `archspoof_cc_subject_mismatch`). Generic crypto errors
    (HMAC tag mismatch, expired cert) deliberately do NOT
    bucket — same posture as the metrics counters.
  - **`GET /api/v1/attest/recent-rejections` HTTP handler.**
    Cursor-paginated list endpoint with closed-enum filter
    validation: `?cursor=<seq>`, `?limit=N` (clamped to
    [1, 500]), `?kind=`, `?reason=`, `?arch=`,
    `?since=<unix-secs>`. Bad filter values return 400 with a
    helpful message (so a typo'd `kind` doesn't silently
    degrade to "no filter"); empty store returns 200 with
    `records: []` (distinct from 503 = "store not wired").
    Echoes the parsed filters back in the response so clients
    can audit what the server applied. Mounted in
    `pkg/api/handlers.go` next to the slash / enrollment read
    endpoints.
  - **`qsdmcli watch archspoof --detailed` operator UX.**
    New flag flips the watcher from counter-bucket diffing to
    per-record streaming via the new endpoint. Emits one
    `WatchKindArchSpoofRejection` event per actual store
    record with `seq`, `reason`, `arch`, `height`,
    `miner_addr`, `gpu_name`, `cert_subject`, and `detail`.
    Cursor-based: the watcher tracks the highest `Seq`
    observed across polls; default mode (no
    `--include-existing`) starts from "now" so operators
    don't replay history at startup. 503 from the endpoint
    fails loudly with a fallback hint ("drop --detailed to
    use counter mode") rather than silently looping.
    Server-side single-value `?reason=` / `?arch=` filters
    forward when exactly one value is set; multi-value
    filter sets fall back to client-side filtering (server
    only accepts one filter value per parameter).
  - **`internal/v2wiring` integration.** `Wire()` constructs
    one `recentrejects.Store` and installs it under both the
    producer-side (`mining.SetRejectionRecorder`) and the
    consumer-side (`api.SetRecentRejectionLister`) adapters.
    A new `Wired.RecentRejections` field exposes the store
    handle for tests + future call sites.
    `miningRejectionRecorderAdapter` and
    `recentRejectionListerAdapter` keep `pkg/api` and
    `pkg/mining` free of cross-imports.
  - **Test coverage.** 51 new unit / integration tests
    (1922 → 1973 module-wide):
    - 18 in `pkg/mining/attest/recentrejects` covering
      ring construction, FIFO eviction at the cap, monotonic
      `Seq`, RecordedAt fill, defensive truncation, filter
      matrix (kind / reason / arch / since / combined),
      cursor pagination, nil-store safety, and concurrent-
      writer correctness with sequence monotonicity assertion.
    - 6 in `pkg/mining/verifier_recentrejects_test.go`
      driving the verifier hot path against a capturing
      recorder: each of the four kinds plus a no-bucket-on-
      generic-crypto-error pin and a nil-recorder fallback
      smoke test.
    - 14 in `pkg/api/handlers_recent_rejections_test.go`
      covering happy-path, empty-store-returns-200,
      503/405/400 paths, all four filter validations, limit
      clamping, filter forwarding, echoed-filters response,
      and Content-Type pin.
    - 4 in
      `internal/v2wiring/v2wiring_recentrejects_test.go`
      driving Wire() → store → handler round trip,
      kind-filter forwarding through the production
      adapter, multi-page pagination round trip, and the
      503 fallback when Wire() never ran.
    - 9 in `cmd/qsdmcli/watch_archspoof_test.go` covering
      `--detailed` once-mode no-events, drain-on-include-
      existing with two records, 503 fail-loud, human-
      readable formatting with all populated fields, and
      `buildRecentRejectionsPath` filter / cursor wiring.
    Total `qsdmcli` tests 195 → 204; total `pkg/mining`
    tests 479 → 485; total `internal/v2wiring` tests
    36 → 40.
  - **Out of scope.** Persistence — the ring is volatile;
    a restart wipes it. The same boundary is documented for
    `chain.SlashReceiptStore`. A future on-disk
    implementation can plug behind the
    `mining.RejectionRecorder` and
    `api.RecentRejectionLister` interfaces without changing
    the verifier or the handler.

- **`qsdmcli watch archspoof` — operator-facing live stream of
  arch-spoof and hashrate-band rejection bursts (2026-04-29).**
  Fourth member of the `qsdmcli watch *` family alongside
  `enrollments` / `slashes` / `params`. Polls
  `/api/metrics/prometheus`, parses the
  `qsdm_attest_archspoof_rejected_total{reason}` and
  `qsdm_attest_hashrate_rejected_total{arch}` counter
  families, and emits one event per non-zero counter delta on
  each tick. Designed as the per-event complement to the
  Prometheus alert rules shipped in the previous slot: alerts
  say "something is wrong"; the watcher says "here is each hit
  as it lands, in order".
  - **Two new event kinds** on the shared `WatchEvent`
    envelope: `archspoof_burst` (with `reason`,
    `delta_count`, `total_count`) and `hashrate_burst` (with
    `arch`, `delta_count`, `total_count`). JSON-Lines
    consumers can decode every watcher's output with a single
    struct definition; renaming either kind is a wire-format
    change pinned by tests.
  - **Counter-rollback handling.** Counters monotonically
    increase under normal operation; a decrease across two
    polls (process restart wiping in-memory counters) snaps
    the snapshot to the new baseline without emitting. Under-
    counting one cycle is preferred to a spurious "burst" the
    moment a validator restarts. Covered by
    `TestDiffArchSpoofSnapshots_CounterRollback_Silent`.
  - **Server-side filters.** `--reason` and `--arch` flags
    accept comma-separated allowlists (e.g.
    `--reason=cc_subject_mismatch` to monitor only the
    critical bucket); flag values are validated against the
    canonical enums at parse time so typos surface
    immediately rather than as silent no-matches.
  - **Metrics-URL derivation.** Defaults to deriving from
    `QSDM_API_URL` (replacing the trailing `/api/v1` with
    `/api/metrics/prometheus`); overridable via
    `--metrics-url` flag or `QSDM_METRICS_URL` env var for
    operators with split data-plane / metrics-plane
    deployments.
  - **Auth.** Same Bearer-token plumbing as the rest of
    `qsdmcli`. The dashboard's `requireMetricsScrapeOrAuth`
    middleware accepts either a JWT or the metrics-scrape
    secret; the Bearer side is the one wired in `qsdmcli`
    today.
  - **Test coverage.** 34 new unit / integration tests in
    [`QSDM/source/cmd/qsdmcli/watch_archspoof_test.go`](QSDM/source/cmd/qsdmcli/watch_archspoof_test.go)
    covering flag normalisation, CSV-set parsing, URL
    derivation, exposition parsing (happy path, float values,
    malformed lines, empty arch label normalisation),
    `splitExpositionLine` direct cases, diff-core semantics
    (no-change, single-bucket burst, multi-bucket sorted
    output, counter rollback, filter enforcement),
    `--include-existing` snapshot synthesis, end-to-end
    `--once` mode against an `httptest` metrics server, and
    router dispatch / unknown-subcommand error advertisement.
    Total qsdmcli tests: 161 → 195.
  - **Out of scope (deliberately).** Per-rejection `node_id` /
    GPU name / raw error message — the metrics layer is
    label-coarse on purpose; surfacing that detail would
    require a server-side ring buffer and a new
    `/api/v1/attest/recent-rejections` endpoint. Operators
    needing per-event detail can correlate watcher bursts
    against the validator's structured log; a recent-
    rejections endpoint is queued behind the watcher-bot
    reference impl in a future session.

- **Prometheus alert rules + scrape wiring for the §4.6
  arch-spoof gate, hashrate-band gate, and `qsdm/gov/v1`
  authority-rotation pipeline (2026-04-29).** Three new alert
  rule groups land in
  [`QSDM/deploy/prometheus/alerts_qsdm.example.yml`](QSDM/deploy/prometheus/alerts_qsdm.example.yml)
  alongside a wiring fix that closes a silent gap caught while
  shipping this work.
  - **`qsdm-v2-attest-archspoof`** — three rules, one per reason
    label on `qsdm_attest_archspoof_rejected_total`:
    `unknown_arch` (warning, sustained probe), `gpu_name_mismatch`
    (warning, lazy spoof by enrolled operator), and
    `cc_subject_mismatch` (**critical, fires on a single
    increment** because reaching that branch means the proof
    has already passed cert-chain pin + AIK signature, so the
    contradiction is a cryptographic anomaly).
  - **`qsdm-v2-attest-hashrate`** — single rule keyed on
    `{{ $labels.arch }}` so all five canonical GPU
    architectures (Hopper/Blackwell/Ada/Ampere/Turing) are
    covered without manual duplication. Annotation includes
    the §4.6.3 reference band table so the on-call gets the
    full triage context inline.
  - **`qsdm-v2-governance`** — three rules: vote recorded
    (info, FYI ping for the multisig set), threshold crossed
    (warning, proposal staged), and AuthorityList size below 2
    (critical floor protecting against single-signer
    governance degeneration).
  - **Scrape-path wiring fix.** While exploring the existing
    metrics surface I found that the four
    `qsdm_gov_authority_*` series — counters defined and
    incremented from `gov_metrics.go` since the multisig work
    landed — were never iterated over in
    [`prometheus_scrape.go::corePrometheusMetrics`](QSDM/source/pkg/monitoring/prometheus_scrape.go).
    Operators would have seen empty `/metrics` for the entire
    governance authority surface; the alerts above wouldn't
    have anything to fire on. Fixed by adding the four
    `for/range` blocks plus the gauge `add()` call mirroring
    the existing param-pipeline shape. Locked down by 4 new
    tests in
    [`gov_metrics_scrape_test.go`](QSDM/source/pkg/monitoring/gov_metrics_scrape_test.go)
    that drive the recorders and assert the names, types, and
    labels appear in `corePrometheusMetrics()` output (and one
    end-to-end test of `PrometheusExposition()` to catch
    formatter regressions).
  - **CI guard.**
    [`.github/workflows/validate-deploy.yml::prometheus-rules-check`](.github/workflows/validate-deploy.yml)
    runs `promtool check rules` on every push that touches
    `QSDM/deploy/prometheus/**` — the new rules pass clean
    locally with `promtool 2.55.1` (`SUCCESS: 33 rules
    found`).
  - **Docs.**
    [`QSDM/deploy/prometheus/README.md`](QSDM/deploy/prometheus/README.md)
    table of rule groups extended to include the three new
    families with the same shape as the existing v2-mining
    entries, plus a short note explaining why
    `cc_subject_mismatch` is intentionally critical.
  - **Tests**: 4 new monitoring tests + all 1880+ existing
    tests pass; `go vet ./...` clean.

- **CC-path leaf cert subject ↔ `gpu_arch` consistency check
  (§4.6.5, 2026-04-29).** Replaces the earlier no-op stub
  `archcheck.ValidateBundleArchConsistencyCC` with a real
  evidence-based rule wired as Step 9 of the
  [`cc.Verifier`](QSDM/source/pkg/mining/attest/cc/verifier.go)
  flow, after the PCR floor. Completes the symmetry with the
  HMAC path's §3.3 step-8 `gpu_name` cross-check.
  - **Evidence-based, not strict.** If the leaf cert's
    `Subject.CommonName` contains a substring matching ANY
    canonical NVIDIA product pattern, the claimed `gpu_arch`
    must match the longest-pattern attribution (rejection
    wraps `archcheck.ErrArchCertSubjectMismatch` under
    `mining.ErrAttestationSignatureInvalid`). If the CN
    contains NO product evidence (test fixtures, corporate
    AIK labels like `"NVIDIA Confidential Computing AIK"`,
    OID-based model encodings), Step 9 passes through — the
    cert-chain pin (Step 3) and AIK signature (Step 4)
    remain the cryptographic locks. If
    `Attestation.GPUArch` is empty (standalone-call path /
    pre-fork bring-up), Step 9 is skipped.
  - **Longest-pattern overlap rule.** A subject like
    `"RTX 6000 Ada Generation"` matches both `"rtx 6000 ada"`
    (Ada) and `"rtx 6000"` (Turing) as substrings. The longer
    pattern wins, so the Ada attribution dominates and a
    `gpu_arch=turing` claim on that cert rejects. Locked
    down by
    [`TestVerifier_ArchCheck_LongestPatternWins`](QSDM/source/pkg/mining/attest/cc/verifier_archcheck_test.go).
  - **New Prometheus reason** for the `archspoof_rejected`
    counter:
    `qsdm_attest_archspoof_rejected_total{reason="cc_subject_mismatch"}`
    — distinct from `gpu_name_mismatch` so dashboards can
    split CC-path leaf-cert contradictions from HMAC-path
    lazy spoofs (different remediation playbooks). Cardinality
    is now ≤ 9 series total; the
    `mining.SetMiningMetricsRecorder` adapter forwards via
    `errors.Is(err, archcheck.ErrArchCertSubjectMismatch)` in
    [`pkg/mining/metrics.go`](QSDM/source/pkg/mining/metrics.go).
  - **Test scaffolding.**
    [`BuildOpts.LeafSubjectCN`](QSDM/source/pkg/mining/attest/cc/testvectors.go)
    (and `RootSubjectCN`) now lets test code mint
    product-named leaves; existing fixtures default to
    `"qsdm-test-nvidia-aik"` (product-free), so every
    pre-existing CC test passes through Step 9 unchanged.
    `cc.Verifier` wraps the rejection with double-`%w`
    (Go 1.20+) so callers can `errors.Is` against EITHER
    sentinel.
  - **Tests**: 6 new archcheck unit tests
    (`HappyPath`, `NoEvidencePassesThrough`,
    `RejectsContradiction`, `LongestPatternWins`,
    `CaseInsensitive`, `RejectsUnknownArch`) + 8 new CC
    verifier integration tests
    ([`verifier_archcheck_test.go`](QSDM/source/pkg/mining/attest/cc/verifier_archcheck_test.go))
    covering both spoof shapes, no-evidence pass-through for
    test fixture + corporate CNs, alias acceptance, longest-
    pattern wins, and the `GPUArch=""` skip-Step-9 path.
    Monitoring counter test extended to cover the new
    `cc_subject_mismatch` reason. Full suite (`go test ./...`)
    passes; `go vet ./...` clean.
  - **Docs**: `MINING_PROTOCOL_V2.md` §4.6.5 rewritten from
    "placeholder" to a full design with accept/reject table,
    overlap-resolution rule, and source links; §3.2 verifier
    flow renumbered to 9 steps; §4.6.4 metric reason set
    extended to include `cc_subject_mismatch`.

- **Hashrate-band plausibility check + Prometheus telemetry for
  the §4.6 arch-spoof gate (2026-04-29).** The §4.6 closed-enum
  + arch ↔ `gpu_name` rejection from earlier today now has a
  third leg: per-arch [Min, Max] bounds on
  `Attestation.ClaimedHashrateHPS`. A claim outside the band
  rejects with `archcheck.ErrHashrateOutOfBand` (wrapped in
  `mining.ErrAttestationSignatureInvalid`) BEFORE the per-type
  dispatcher fires, so an implausible-hashrate proof never
  pays the HMAC or X.509 work. `ClaimedHashrateHPS == 0` is
  treated as "not asserted" and passes through — preserves
  backward compat with miners and fixtures that don't populate
  the field.
  - **Bands** ([`archcheck.HashrateBandFor`](QSDM/source/pkg/mining/attest/archcheck/archcheck.go))
    are deliberately wide (~100x range per arch) so legitimate
    variation across a product family doesn't false-positive.
    Catches obvious lies — RTX 4090 claiming 200 MH/s, H100
    claiming 100 H/s, the 18 PH/s units-confusion typo.
  - **Prometheus telemetry** for the whole §4.6 rejection gate:
    - `qsdm_attest_archspoof_rejected_total{reason}` —
      `unknown_arch` | `gpu_name_mismatch`. Counts the §4.6.1
      allowlist rejects + §4.6.2 HMAC step-8 cross-check
      rejects.
    - `qsdm_attest_hashrate_rejected_total{arch}` — labelled by
      the canonical arch the claim was made against. Counts
      the §4.6.3 hashrate-band rejects.
    - Cardinality stays ≤ 8 series total. Both wire through
      a new dependency-inverted recorder
      ([`pkg/mining/metrics.go`](QSDM/source/pkg/mining/metrics.go) +
       [`pkg/monitoring/mining_recorder.go`](QSDM/source/pkg/monitoring/mining_recorder.go))
      mirroring the `pkg/chain.SetChainMetricsRecorder`
      pattern, so pkg/mining stays free of pkg/monitoring
      imports.
  - **Tests**: 6 new archcheck unit tests (zero-as-sentinel,
    happy-path, inclusive bounds, lazy-spoof, low-CPU
    spoof, unknown-arch programmer-error path) + 4 new
    verifier wiring tests in
    [`verifier_hashrate_test.go`](QSDM/source/pkg/mining/verifier_hashrate_test.go) (zero passes,
    high-spoof rejects, low-spoof rejects, in-band accepts) +
    5 new monitoring tests in
    [`archcheck_metrics_test.go`](QSDM/source/pkg/monitoring/archcheck_metrics_test.go) (per-reason and
    per-arch counter routing, unknown-bucketing for both,
    init-time adapter registration). 1870 / 1870 tests
    passing across 68 packages.
  - **Docs**: `MINING_PROTOCOL_V2.md` §4.6 now has §4.6.3
    (hashrate band table + rationale) and §4.6.4 (operator
    metrics).

- **Arch-spoof rejection (§4.6 / §3.3 step 8) — closed-enum
  allowlist + arch ↔ `gpu_name` cross-check (2026-04-29).** The
  long-deferred step 8 of the HMAC verifier acceptance flow now
  ships, replacing an earlier draft that proposed using a
  "matmul rounding fingerprint" — a non-starter once §4.3's
  byte-exact IEEE-754 RNE rules made `ComputeMixDigestV2`
  produce the same digest on every conforming arch.
  - **Allowlist** ([`pkg/mining/attest/archcheck`](QSDM/source/pkg/mining/attest/archcheck/archcheck.go))
    fixes the canonical set to `hopper`, `blackwell`,
    `ada-lovelace`, `ampere`, `turing`. Older arches (Volta,
    Pascal, Maxwell, Kepler) are intentionally OFF; future
    arches require a registry append plus matching `gpu_name`
    patterns in the same change. The qsdmminer-console-emitted
    `ada` short form is accepted as an alias for backward
    compat. The closed-enum check fires in the outer
    `pkg/mining/verifier.go` BEFORE per-type dispatch, so a
    malformed / typo / future-arch-sneak proof costs a single
    map lookup and never pays the HMAC or X.509 work.
  - **arch ↔ `bundle.gpu_name` consistency** fires inside the
    HMAC verifier ([`pkg/mining/attest/hmac/verifier.go`](QSDM/source/pkg/mining/attest/hmac/verifier.go))
    as step 8. Catches the lazy spoof — an attacker who flips
    `gpu_arch=hopper` but forgot to also lie about the
    `nvidia-smi` name on their consumer Ada card. Bundle
    `gpu_name` is HMAC-bound, so a determined attacker still
    has to forge a valid HMAC and choose at sign time; the
    on-chain registry's `(gpu_uuid, hmac_key)` pairing (§5.2)
    + §5.4 stake bonding + §8 slashing surface are the
    economic locks behind it.
  - **CC-path placeholder** (`ValidateBundleArchConsistencyCC`)
    is a no-op today — the device certificate chain itself
    binds to a specific physical Hopper / Blackwell GPU at
    the protocol level. Reserved as a fixed wiring point for
    a future strict cert-subject parsing pass.
  - **Tests**: 15 unit tests in `archcheck_test.go` (every
    canonical, alias, all-known-product happy path, every
    cross-family lazy-spoof + AMD spoof + downgrade spoof) +
    5 integration tests in [`hmac/verifier_archcheck_test.go`](QSDM/source/pkg/mining/attest/hmac/verifier_archcheck_test.go) (lazy spoof
    re-using fixture, determined spoof with re-signed bundle,
    cross-family rejection, alias acceptance, unknown-arch
    rejection) + 3 wiring tests in [`verifier_archspoof_test.go`](QSDM/source/pkg/mining/verifier_archspoof_test.go) (cheap
    reject before dispatch, alias acceptance, pre-fork
    bypass). 1850 / 1850 tests passing across 68 packages.
  - **Docs**: `MINING_PROTOCOL_V2.md` §3.3 step 8 + a
    rewritten §4.6 with the design correction prominently
    flagged.

- **`fork_v2_tc_height` is now a governance-tunable chain parameter
  (2026-04-28).** The Tensor-Core PoW mixin activation height is
  registered as `chainparams.ParamForkV2TCHeight` (bounds
  `[0, math.MaxUint64]`, default `MaxUint64` = TC disabled).
  `v2wiring.Wire()` reads the active value from the `ParamStore`
  at chain init and pins it into `pkg/mining` via
  `SetForkV2TCHeight`; after every `PromotePending` call inside
  the `SealedBlockHook` it re-pins from the (possibly just-
  promoted) value, so a successful `qsdm/gov/v1` `param-set` tx
  makes the new fork height visible to the verifier and reference
  solver on the very next sealed block — without a binary restart.
  Genesis bake-in is supported via the new
  `v2wiring.Config.ForkV2TCHeight *uint64` field; the snapshot
  replay path takes precedence over the genesis seed across
  restarts so the chain's committed governance history cannot be
  silently overwritten by a config change. Closes the operational
  side of the §12.2 deployment readiness work; the cryptographic
  side (`pkg/mining/pow/v2`) was shipped earlier.
  - **Registry**: [`pkg/governance/chainparams/params.go`](QSDM/source/pkg/governance/chainparams/params.go)
    appends `ParamForkV2TCHeight` with the new bounds.
  - **Wiring**: [`internal/v2wiring/v2wiring.go`](QSDM/source/internal/v2wiring/v2wiring.go)
    seeds, pins, and re-pins the runtime mining knob.
  - **Tests**: [`internal/v2wiring/v2wiring_tcfork_test.go`](QSDM/source/internal/v2wiring/v2wiring_tcfork_test.go)
    locks the four lifecycle paths — default-disabled, genesis
    seed (zero + future activation), governance-driven re-pin
    on promote, and snapshot replay across simulated restart.
  - **Docs**: `MINING_PROTOCOL_V2.md` §4, §10 registry table, and
    §12.2 deliverable updated.

### Performance

- **`pkg/mining/pow/v2` — 22% faster validator hot path
  (2026-04-28).** A 256 KB FP16→FP32 lookup table populated at
  package init, plus a benchmark scaffold that pins the
  per-stage breakdown so future regressions are loud:

  | Benchmark                | Before  | After  | Speedup |
  |--------------------------|--------:|-------:|--------:|
  | `ComputeMixDigestV2`     | 384 µs  | 298 µs | 1.29×   |
  | `TensorMul`              | 1 667 ns| 528 ns | 3.16×   |
  | `FP16ToFloat32`          | 6.7 ns  | 1.92 ns| 3.49×   |
  | `MatrixFromMix`          | 3 224 ns| 3 167 ns | (noise) |
  | `Float32ToFP16RNE`       | 11.6 ns | 11.3 ns| (untouched) |

  Numbers are on the user's Xeon E5-2670 (Sandy Bridge, 2.6 GHz,
  2012). All allocations preserved at 1 / 32 B per
  `ComputeMixDigestV2` call (the digest copy); no new heap
  pressure introduced.

  - **LUT impl**:
    [`pkg/mining/pow/v2/fp16_lut.go`](QSDM/source/pkg/mining/pow/v2/fp16_lut.go)
    populates `fp16ToFP32LUT [65536]float32` from the unrolled
    IEEE-754 reference (`fp16ToFloat32Slow`) at init time, then
    self-checks against a hand-picked boundary set
    (signed zero, smallest subnormal, smallest normal, 1.0
    neighbourhood, largest finite, ±Inf, NaN). A misconfigured
    table panics at startup — silently producing wrong
    mix-digests would be a much worse failure mode than refusing
    to start.
  - **Equivalence guard**:
    `TestFP16ToFP32_LUTMatchesSlow` in
    [`pkg/mining/pow/v2/fp16_test.go`](QSDM/source/pkg/mining/pow/v2/fp16_test.go)
    asserts the LUT is bit-identical to the slow reference for
    every one of the 65,536 possible FP16 inputs. Combined with
    the frozen byte-exact golden mix-digest vector
    (`ef9319a6…53f4`) this is two independent locks on
    correctness.
  - **Benchmarks**:
    [`pkg/mining/pow/v2/bench_test.go`](QSDM/source/pkg/mining/pow/v2/bench_test.go)
    establishes per-stage baselines for
    `ComputeMixDigestV2`, `MatrixFromMix`, `TensorMul`,
    `FP16ToFloat32`, `Float32ToFP16RNE`. Run with
    `go test -bench=. -benchtime=2s ./pkg/mining/pow/v2/...`.
  - **Spec update**:
    [`MINING_PROTOCOL_V2.md`](QSDM/docs/docs/MINING_PROTOCOL_V2.md)
    §4.3 now lists the per-stage cost breakdown and explains
    the two micro-optimizations (LUT + stack-friendly SHAKE
    allocation pattern), so future SIMD/BLAS or assembly
    fast-paths know exactly which budget they're trying to
    beat.

### Added

- **Verifier + reference solver wired through
  `FORK_V2_TC_HEIGHT` (2026-04-28).** The pure-Go Tensor-Core
  mix-digest reference is no longer dead code: Step 10 of
  [`pkg/mining/verifier.go`](QSDM/source/pkg/mining/verifier.go)
  and the per-attempt loop of
  [`pkg/mining/solver.go`](QSDM/source/pkg/mining/solver.go) now
  height-gate between the v1 SHA3 walk and the v2 mixin
  (`powv2.ComputeMixDigestV2`) using a new runtime-settable
  knob in [`pkg/mining/fork.go`](QSDM/source/pkg/mining/fork.go):
  - `ForkV2TCHeight() uint64` — current activation height
    (default `math.MaxUint64` = TC disabled, safe).
  - `SetForkV2TCHeight(h uint64)` — pin the activation height
    at chain-init time. Calling mid-execution is a bug;
    validators MUST NOT be able to move the gate at runtime in
    response to adversarial input.
  - `IsV2TC(height uint64) bool` — boundary-inclusive helper
    (`true` at `height == ForkV2TCHeight()`).
  - **Independent of `ForkV2Height`**: the two fork heights are
    deliberately separate so the v2 attestation fork can ship
    independently of the PoW-algorithm change.
  - **Soft-tightening fork**: a v1 proof at a post-TC height
    fails Step 10 with `ReasonWork` /
    `"mix_digest mismatch"`; a v2 proof at a pre-TC height
    fails the same way. No proof-wire-format change, no chain
    reset.
  - **Import-cycle break**: `pkg/mining/pow/v2/mixdigest.go`
    now defines `DAG` as a local minimal interface
    (`N() uint32; Get(uint32) ([32]byte, error)`) so it does
    not import `pkg/mining`. Go's structural interfaces mean
    `*mining.InMemoryDAG` and `*mining.LazyDAG` still satisfy
    it for free.
  - **Tests**: six new cases in
    [`pkg/mining/verifier_v2tc_test.go`](QSDM/source/pkg/mining/verifier_v2tc_test.go)
    cover the default-disabled invariant, boundary inclusivity
    at `H-1 / H / H+1`, the post-TC happy path (Solve + Verify
    both routed through v2), and both algorithm-mismatch
    rejection directions. The pre-existing
    `TestVerifyAcceptsValidProof` keeps passing untouched —
    the safety guarantee of the default.

- **Pure-Go Tensor-Core PoW v2 reference implementation —
  `pkg/mining/pow/v2/` (2026-04-28).** The validator-side byte-
  exact reference for the §4 Tensor-Core mixin specified in
  [`MINING_PROTOCOL_V2.md`](QSDM/docs/docs/MINING_PROTOCOL_V2.md).
  Locks down four implementation-defined IEEE-754 details that
  any future CUDA miner MUST match bit-for-bit:
  - **Matrix expansion**: `MatrixFromMix(mix [32]byte)` uses
    SHAKE256 with the domain separator
    `"qsdm/pow/v2/matrix\x00"` to fan the 32-byte running mix
    out to a 16×16 FP16 matrix in row-major big-endian.
    Implemented in
    [`pkg/mining/pow/v2/matrix.go`](QSDM/source/pkg/mining/pow/v2/matrix.go).
  - **FP16 codec**: self-contained 16-bit-exact
    encode/decode + RNE FP32↔FP16 conversion + canonical NaN
    handling (FP16 NaN → `0x7E00`, FP32 NaN → `0x7FC00000`)
    so platform-specific NaN payloads never leak into SHA3.
    Implemented in
    [`pkg/mining/pow/v2/fp16.go`](QSDM/source/pkg/mining/pow/v2/fp16.go).
  - **Matmul**: `TensorMul` performs FP16×FP16 widened to
    FP32 (exact), accumulates in **strict left-to-right FP32**
    (NOT tree-reduction; CUDA WMMA users must emulate this
    order in software), and down-converts to FP16 with RNE.
  - **Step body**: `ComputeMixDigestV2` runs the 64-step DAG
    walk with the v2 step body
    `mix := SHA3-256(mix || entry || tc)` where `tc` is the
    32-byte BE-packed result vector. Implemented in
    [`pkg/mining/pow/v2/mixdigest.go`](QSDM/source/pkg/mining/pow/v2/mixdigest.go).
  - **Tests**:
    [`fp16_test.go`](QSDM/source/pkg/mining/pow/v2/fp16_test.go)
    exhaustively covers all 65,536 FP16 bit patterns (decode/
    encode round-trip + FP16→FP32→FP16 round-trip for every
    non-NaN value) plus boundary specials (signed zero,
    smallest subnormal, smallest normal, largest finite, ±Inf,
    halfway-tie rounding, overflow to Inf, NaN
    canonicalization).
    [`mixdigest_test.go`](QSDM/source/pkg/mining/pow/v2/mixdigest_test.go)
    covers matrix-expansion determinism, vector unpack with
    NaN canonicalization, identity matmul, hand-computed row,
    full v2 determinism, v1≠v2 sanity, avalanche/diffusion
    against a 1-bit nonce change, and a frozen byte-exact
    **golden mix-digest vector**
    (`ef9319a6134aeb9b77f315427ec81cdbc40a03c60414284864a3e9bbd68153f4`)
    that any compliant CUDA miner MUST reproduce bit-for-bit.
  - **Documentation**:
    [`MINING_PROTOCOL_V2.md`](QSDM/docs/docs/MINING_PROTOCOL_V2.md)
    §4 status flips from "specified, not implemented" to
    "byte-exact validator-side reference shipped"; new
    subsections §4.2.1–4.2.4 lock the matrix expansion,
    vector unpack, matmul order, and NaN canonicalization in
    the spec itself. §12.2 deferred-work register splits into
    "reference shipped" + "CUDA kernel deferred"; remaining
    estimate trimmed from 14d to 10d post-hardware.
  - **Activation**: still gated behind `FORK_V2_TC_HEIGHT`;
    pre-fork validators continue to use the v1 walk in
    `pkg/mining.ComputeMixDigest`. The v1 path is unchanged
    and stays in-tree for replaying pre-fork blocks.

- **Multisig-gated authority rotation — `qsdm/gov/v1` `authority-set` payload kind (2026-04-28).**
  The `qsdm/gov/v1` ContractID now carries TWO payload
  kinds: `param-set` (already shipped) and `authority-set`.
  Each `authority-set` tx is one authority's vote on a
  proposal tuple `(op, address, effective_height)`; the
  chain accumulates votes and stages the rotation when
  M-of-N threshold is crossed (`threshold = max(1, N/2 + 1)`).
  The on-chain AuthorityList is now itself rotatable
  without a binary redeploy, closing the prior posture's
  "captured single authority can self-disable" hazard.
  - **Wire format**:
    [`pkg/governance/chainparams/types.go`](QSDM/source/pkg/governance/chainparams/types.go)
    adds `AuthoritySetPayload` (with `Op ∈ {add, remove}`,
    `Address`, `EffectiveHeight`, `Memo`) and the
    `PayloadKindAuthoritySet` discriminator. The kind tag
    is the dispatch axis the admit gate
    (`PeekKind` in
    [`pkg/governance/chainparams/validate.go`](QSDM/source/pkg/governance/chainparams/validate.go))
    and the chain applier
    ([`pkg/chain/gov_apply.go`](QSDM/source/pkg/chain/gov_apply.go))
    use to route to per-shape validators / handlers.
  - **Vote-tally store**:
    [`pkg/governance/chainparams/authority.go`](QSDM/source/pkg/governance/chainparams/authority.go)
    introduces `AuthorityVoteStore` (interface +
    `InMemoryAuthorityVoteStore` reference impl). Tracks
    proposals keyed by `(op, address, effective_height)`,
    each carrying an ordered voter set + sticky `Crossed`
    flag. `RecordVote` is idempotent on duplicate voters
    (returns `ErrDuplicateVote`) and the threshold helper
    `AuthorityThreshold(n)` is exported so the CLI / API
    can render the same "M of N" string the chain uses.
  - **Activation semantics**: the existing
    `GovApplier.PromotePending(height)` now ALSO promotes
    crossed authority proposals — `add` inserts into the
    AuthorityList under a new `authorityMu` RWMutex,
    `remove` drops the address AND drops the removed
    authority's votes from every still-open proposal
    (`DropVotesByAuthority` + `RecomputeCrossed` re-
    evaluates which open proposals now satisfy the
    smaller threshold). A `remove` that would empty the
    AuthorityList is REFUSED at promotion (governance
    cannot disable itself from on-chain — the operator
    must redeploy binaries for that).
  - **Events**: a new `GovAuthorityEvent` family with
    kinds `authority-voted`, `authority-staged`,
    `authority-activated`, `authority-abandoned`, and
    `authority-rejected` rides on the existing
    `GovEventPublisher` (a new `PublishGovAuthority`
    method; existing implementations grow a no-op).
  - **Metrics**:
    [`pkg/monitoring/gov_metrics.go`](QSDM/source/pkg/monitoring/gov_metrics.go)
    adds five Prometheus surfaces:
    `qsdm_gov_authority_voted_total{op}`,
    `qsdm_gov_authority_crossed_total{op}`,
    `qsdm_gov_authority_activated_total{op}`,
    `qsdm_gov_authority_count` (gauge),
    `qsdm_gov_authority_rejected_total{reason}`.
    `MetricsRecorder` grows the matching four methods.
  - **Persistence**: snapshot format bumps to
    `SnapshotVersion=2` (backwards-compatible read of
    v1). New `SaveSnapshotWith(store, votes, path)` and
    `LoadOrNewWith(path)` entry points carry the
    authority-rotation state through restarts; a node
    that crashes between threshold-crossing and the
    activation block replays correctly under the fresh
    binary. v1 snapshots load cleanly under v2 binaries
    (vote store boots empty); v2 snapshots refuse to
    load on v1 binaries (silently dropping in-flight
    rotations across a downgrade is the wrong default).
  - **CLI**:
    [`cmd/qsdmcli/gov_helper.go`](QSDM/source/cmd/qsdmcli/gov_helper.go)
    grows a `propose-authority` subcommand
    (`--op`, `--address`, `--effective-height`, `--memo`,
    `--out`, `--print-cmd`) and the existing `inspect`
    subcommand now dispatches on the wire-kind tag so
    both payload kinds round-trip through the same
    helper.
  - **Tests**: ~50 new test cases across the layers —
    threshold table, vote-store record / promote / drop /
    recompute mechanics, validate / admit kind dispatch,
    applier rejection branches, persistence round-trip
    + v1↔v2 compatibility, and an end-to-end integration
    rig in
    [`internal/v2wiring/v2wiring_authority_test.go`](QSDM/source/internal/v2wiring/v2wiring_authority_test.go)
    that drives a real chain through `vote → cross →
    activate → AuthorityList expanded` AND a
    `crash-between-cross-and-activate` persistence-replay
    scenario.
  - **Docs**: §9.4.7 of
    [`MINING_PROTOCOL_V2.md`](QSDM/docs/docs/MINING_PROTOCOL_V2.md)
    is the new operator-facing specification (wire
    format, threshold rule, activation semantics,
    rejection branches, events, metrics, persistence,
    CLI). The deferred-work register's §12.5 marks
    multisig-gated rotation as **SHIPPED**, completing
    the "authority list is itself NOT governance-tunable
    in this revision" caveat from the prior commit.

- **Governance production-readiness: persistent `ParamStore` + end-to-end integration tests (2026-04-28).**
  Closes the two production gaps in the freshly-shipped
  `qsdm/gov/v1` runtime tuning hook: state is now durable
  across node restarts, and the full chain-side glue
  (admit → stage → promote → SlashApplier reads new value)
  is now exercised by integration tests through the real
  `internal/v2wiring` boot path.
  - **Persistence**:
    [`pkg/governance/chainparams/persist.go`](QSDM/source/pkg/governance/chainparams/persist.go)
    ships `SaveSnapshot(store, path)` and
    `LoadOrNew(path)` free functions, mirroring the shape
    of `pkg/chain/staking_persist.go`. Snapshot format is
    a version-tagged JSON document with `active` map +
    `pending[]` array, written atomically through a `.tmp`
    file + rename. Forward/backward compat: unknown params
    in the snapshot are dropped silently; out-of-bounds
    values are clamped to registry defaults; an unknown
    version refuses to load (no silent downgrade). A
    missing file is treated as first-boot and returns a
    fresh defaults-seeded store.
  - **Wiring**: `internal/v2wiring.Config` grows two new
    optional fields:
    - `GovParamStorePath string` — when non-empty, `Wire()`
      calls `LoadOrNew(path)` at boot, and the
      `SealedBlockHook` saves a fresh snapshot AFTER each
      block's `PromotePending` runs. The genesis-seed for
      `reward_bps` from `Config.SlashRewardBPS` is now
      conditional on the loaded value being equal to the
      registry default — preserving previously-activated
      governance state across restarts.
    - `LogSnapshotError func(uint64, error)` — operator
      hook for save failures. The chain continues; the
      next sealed block re-saves and recovers.
    - When `GovParamStorePath` is empty, behaviour is
      byte-identical to the prior in-memory-only posture
      (fine for ephemeral testnets, NOT for production).
  - **Integration tests**:
    [`internal/v2wiring/v2wiring_gov_test.go`](QSDM/source/internal/v2wiring/v2wiring_gov_test.go)
    drives the full lifecycle through the real production
    boot path. 8 new tests cover:
    - Proposal activates at effective_height (the canonical
      regression test — a bug in `SetGovApplier`,
      `SealedBlockHook` composition, or
      `chainparams.AdmissionChecker` ordering breaks this).
    - Future-effective-height stays pending across multiple
      blocks and flips on the right one.
    - Non-authority sender rejected at apply time (admission
      stateless, authority check stateful in `GovApplier`).
    - Two authorities; carol supersedes alice's pending
      entry; supersede activates correctly.
    - HTTP read-API surface reflects live chain state.
    - Persistence: post-promote active value preserved
      across simulated restart.
    - Persistence: pending entry replayed across restart;
      promotes correctly on the post-restart chain.
    - Persistence: corrupted snapshot causes `Wire()` to
      return a hard error (no silent state corruption).
  - **Persistence unit tests**:
    [`pkg/governance/chainparams/persist_test.go`](QSDM/source/pkg/governance/chainparams/persist_test.go)
    adds 14 test cases covering save/load round-trip
    (actives + pending), defaults fallback for partial
    snapshots, missing-file → fresh-store, no-op on nil
    store / empty path, unknown-param drop, out-of-bounds
    clamp, version reject, malformed JSON reject, atomic
    cleanup of `.tmp`, overwrite of stale snapshot, and
    full stage → save → load → promote → save → load
    lifecycle.
  - **Documentation**: `MINING_PROTOCOL_V2.md` §9.4 grows a
    "Persistence — `GovParamStorePath`" subsection covering
    the boot-load + per-sealed-block-save flow, the atomic
    write contract, the snapshot wire format, and the
    "missing path = ephemeral testnet only" deployment
    rule.

- **Governance read-API + `qsdmcli watch params` (2026-04-28).**
  Operator-facing surface for the on-chain `qsdm/gov/v1` runtime
  tuning hook. The chain side has been live since the prior
  release; this round wires up the read-only HTTP / CLI plumbing
  authorities and dashboards need to see what's active, what's
  pending, and when proposals land.
  - **HTTP**: two new read endpoints in
    [`pkg/api/handlers_governance.go`](QSDM/source/pkg/api/handlers_governance.go),
    routed in [`pkg/api/handlers.go`](QSDM/source/pkg/api/handlers.go):
    - `GET /api/v1/governance/params` returns a
      `GovernanceParamsView` (active map, pending list sorted
      by `(effective_height ASC, param ASC)`, registry list
      sorted by name, authorities list sorted ASC,
      `governance_enabled` bool). Empty slices/maps are
      normalised to `[]` / `{}` so diff-driven consumers don't
      branch on null.
    - `GET /api/v1/governance/params/{name}` returns a
      `GovernanceParamView` (active value, optional pending
      entry, registry entry). 400 on empty / over-long name,
      404 on unknown param.
    - Both return **503** until the validator wires a
      `GovernanceParamsProvider` via `api.SetGovernanceProvider`,
      matching the posture of the existing v2 enrollment /
      slash read endpoints (v1-only nodes return a clean
      "not configured" rather than ambiguous 404s).
  - **Wiring**: `internal/v2wiring` grows a
    `governanceProviderAdapter` that bridges the live
    `chainparams.ParamStore` + `chain.GovApplier` to the
    pkg/api provider interface, snapshotting active values,
    pending changes, and the authority list under each
    component's own RWMutex (no global snapshot lock; pending
    promotions are atomic in `Promote`).
  - **CLI — `qsdmcli gov-helper params --remote`**: the
    existing offline `params` listing now optionally queries
    the running validator and merges live `active` + `pending`
    columns into the table. Best-effort: 503 / network error
    falls back to the offline view with a stderr warning. The
    JSON output (`--json --remote`) emits the validator's
    snapshot verbatim. A stderr footer reports
    `governance_enabled` plus the authority list so a proposer
    can confirm "yes, my key admits" before building a
    payload.
  - **CLI — `qsdmcli watch params`**: third sibling of
    `watch enrollments` and `watch slashes`. Polls
    `/governance/params` and emits one event per parameter
    transition across consecutive snapshots. Event kinds:
    `param_staged`, `param_superseded`, `param_activated`,
    `param_removed` (defensive), `param_authorities_changed`
    (defensive), `error`. Same flag surface as the other
    watchers (`--interval` floored at 5s, `--once`, `--json`,
    `--include-existing`) plus a `--param=NAME` filter.
    SIGINT/SIGTERM exits 0; first-poll fatal exits non-zero.
  - **Tests**: 14 new unit tests across
    `pkg/api/handlers_governance_test.go` (503-when-unwired,
    happy path, disabled-posture rendering, single-param 404 /
    400, GET-only methods) and
    `cmd/qsdmcli/watch_params_test.go` (diff engine for every
    transition kind, deterministic ordering, param filter,
    initial-events synthesis, JSON-Lines wire-format pin,
    options normalisation).
  - **Documentation**: `MINING_PROTOCOL_V2.md` §9.2 watcher
    table grows a `watch params` row; §9.4 grows
    "HTTP read API" + "`qsdmcli watch params`" subsections;
    §12.4 deferred-work register marks the operator-facing
    surface as **SHIPPED**.

- **`qsdm/gov/v1` runtime parameter-tuning hook (2026-04-28).**
  Two protocol-economy parameters that previously lived as
  construction-time arguments to `chain.SlashApplier` —
  `reward_bps` (slasher reward share) and
  `auto_revoke_min_stake_dust` (auto-revoke threshold) — are
  now governance-tunable at runtime. No more coordinated
  binary swaps to retune economic knobs.
  - **New package**:
    [`pkg/governance/chainparams/`](QSDM/source/pkg/governance/chainparams/)
    ships the `ParamSetPayload` wire format, the param
    `Registry` (whitelist + bounds + defaults + units), the
    `ParamStore` interface with an `InMemoryParamStore`
    reference implementation, the stateless mempool
    `AdmissionChecker`, and the codec / validator pair.
  - **Chain-side applier**:
    [`pkg/chain/gov_apply.go`](QSDM/source/pkg/chain/gov_apply.go)
    ships `GovApplier` with the same shape as the existing
    `SlashApplier` / `EnrollmentApplier`. Routing is wired
    through `EnrollmentAwareApplier.SetGovApplier(...)`.
  - **Authority model**: applier holds an
    `AuthorityList []string`. Tx sender must be on it; an
    empty list disables on-chain governance entirely
    (every gov tx rejects with
    `chainparams.ErrGovernanceNotConfigured`). The list is
    NOT itself governance-tunable in this revision —
    modifying it requires a binary upgrade or a chain-config
    reload, by deliberate design (a circular "governance can
    change the list of governors" surface lets a captured
    authority lock out the rest).
  - **Activation semantics**: the tx field
    `effective_height` MUST satisfy
    `currentHeight ≤ effective_height ≤ currentHeight + MaxActivationDelay`
    (~3 days at 3-second blocks). The applier stages the
    change in a per-param "pending" slot; the
    `SealedBlockHook` calls
    `GovApplier.PromotePending(blockHeight)` after each
    block, which atomically promotes any pending changes
    whose `effective_height` has been reached. Promotion
    order is deterministic across nodes (by height ascending,
    then by name ascending). One pending change per parameter
    at a time; subsequent submissions for the same parameter
    SUPERSEDE the prior pending entry.
  - **`SlashApplier` refactor**: the existing struct fields
    (`RewardBPS`, `AutoRevokeMinStakeDust`) become static
    fallbacks read only when no `ParamStore` is wired. With a
    store wired (the production posture from
    `internal/v2wiring`), every `ApplySlashTx` call reads the
    active value from the store. Backward-compatible: tests
    and binaries that don't set a store keep their existing
    behaviour byte-for-byte.
  - **Mempool admission**: layered above the slashing /
    enrollment gates, mirroring the existing stack — `gov >
    slash > enroll > base`.
  - **CLI**:
    [`qsdmcli gov-helper`](QSDM/source/cmd/qsdmcli/gov_helper.go)
    ships three offline subcommands (no key required;
    governance authorities typically run from air-gapped
    hosts):
    - `propose-param --param=NAME --value=N --effective-height=H [--memo=STR] [--out=PATH] [--print-cmd]`
      builds a canonical `ParamSetPayload` and writes the
      encoded JSON. Pre-flight checks mirror the chain-side
      admission so an authority sees out-of-bounds /
      unknown-param rejections locally.
    - `params [--json]` lists the registered tunables with
      bounds, defaults, units, and descriptions.
    - `inspect (--payload-file=PATH | --payload-hex=HEX)`
      decodes a previously-built payload and pretty-prints
      the structured view with the matched registry entry.
  - **Observability**: four new Prometheus metrics in
    `pkg/monitoring/gov_metrics.go`:
    `qsdm_gov_param_staged_total{param}`,
    `qsdm_gov_param_activated_total{param}`,
    `qsdm_gov_param_value{param}` (gauge),
    `qsdm_gov_param_rejected_total{reason}`. Plus a new
    `GovEventPublisher` interface (separate from
    `ChainEventPublisher` to avoid forcing existing slash /
    enrollment subscribers to grow no-op handlers) emitting
    four `GovParamEvent` flavours: `param-staged`,
    `param-superseded`, `param-activated`, `param-rejected`.
  - **v2wiring extension**: `Config.GovernanceAuthorities`
    is the single new knob; populating it activates governance.
    The `InMemoryParamStore` is wired UNCONDITIONALLY (so the
    `SlashApplier` reads always route through it), seeded
    with `cfg.SlashRewardBPS` as the genesis active value.
    Migration cost for existing operators is zero: leaving
    `GovernanceAuthorities` empty is byte-identical to the
    pre-governance posture.
  - **Tests**: 30+ unit tests across `chainparams_test.go`
    (registry, codec, store, admission), `gov_apply_test.go`
    (applier construction, every rejection path, supersede,
    promote, slash-applier integration with both reward_bps
    and auto_revoke_min_stake_dust scenarios), and
    `gov_helper_test.go` (CLI happy paths, every flag-
    rejection, --print-cmd, --json table, inspect
    round-trip). Full repo `go test ./...` green.
  - **Spec update**: `MINING_PROTOCOL_V2.md` §6 (component
    table), §9.4 (governance — runtime parameter tuning,
    new section), and §12.4 (deferred-work register, marked
    SHIPPED).

- **`freshness-cheat` slasher — verifier shipped, witness
  deferred (2026-04-28).** Closes the v2 slashing trilogy:
  `forged-attestation` + `double-mining` + `freshness-cheat`
  all now ship with concrete `EvidenceVerifier`
  implementations. Lives in
  [`pkg/mining/slashing/freshnesscheat`](QSDM/source/pkg/mining/slashing/freshnesscheat/).
  - **What it detects**: a v2 proof whose `bundle.issued_at`
    is older than `FRESHNESS_WINDOW + grace` (default 60 s +
    30 s) measured against the chain block-time of the
    inclusion height, i.e. retroactive evidence of validator
    collusion or clock skew.
  - **`BlockInclusionWitness` abstraction**: rather than ship
    a permanent `StubVerifier`, the package factors the
    BFT-finality dependency into a `BlockInclusionWitness`
    interface that callers wire to whatever observability
    layer they have. Three implementations ship today:
    `RejectAllWitness` (production default — rejects every
    slash with a kind-specific `ErrEvidenceVerification`
    naming the missing dependency, matching the previous
    `StubVerifier` end-user behaviour with materially better
    diagnostics), `TrustingTestWitness` (testnet / dev — lets
    the slashing path run end-to-end so bugs surface before
    mainnet), and `FixedAnchorWitness` (ops — certifies one
    pre-registered `(height, block_time, proof_id)` tuple).
    Once BFT finality lands, a real `quorum.HeaderWitness`
    plugs into the same interface and freshness-cheat starts
    slashing for real with no other code changes.
  - **Verifier checks**: protocol version (`Version ≥ 2`),
    structural attestation presence, bundle parse, bundle
    `node_id` ↔ payload `node_id` binding, anchor sanity
    (anchor strictly post-`IssuedAt`, ≤ 1 year delta),
    staleness threshold (strict `>` against window + grace
    so borderline cases are not slashed), registry binding,
    and finally `Witness.VerifyAnchor`. Per-offence cap
    matches the rest of the trilogy at `10 CELL` (full
    `MIN_ENROLL_STAKE` bond drain).
  - **Wire format**: `evidenceWire = { proof: <canonical-JSON>,
    anchor_height: <uint64-as-string>, anchor_block_time:
    <int64 unix seconds>, memo?: <≤256 B> }`. Proof is
    serialised via `mining.Proof.CanonicalJSON()` so the
    bytes the verifier hashes are byte-identical to what the
    chain accepted. `DisallowUnknownFields` is set so wire
    drift is rejected loudly.
  - **Production wiring**: `slashing.ProductionConfig` gains
    a `FreshnessCheat` slot with the same kind-mismatch guard
    as the other two; leaving it nil keeps a `StubVerifier`
    in place for binaries that don't import the freshnesscheat
    package. A convenience factory
    `freshnesscheat.NewProductionSlashingDispatcher` wires all
    three verifiers in one call.
  - **CLI**: `qsdmcli slash-helper freshness-cheat` constructs
    evidence locally with the same staleness / anchor-sanity
    / node_id checks the chain runs (so an operator does not
    burn a tx fee on guaranteed-rejection evidence). Includes
    a `--print-cmd` mode that emits a copy-pasteable
    `qsdmcli slash` invocation. `qsdmcli slash-helper inspect
    --kind=freshness-cheat` decodes evidence and renders the
    operator-facing JSON view (proof summary + anchor height +
    anchor block-time + computed staleness).
  - **Tests**: 30+ unit tests in `freshnesscheat_test.go`
    (happy path, every rejection path, every witness flavour,
    encode/decode round-trip, production-dispatcher
    integration) plus 6 new CLI tests covering the
    `slash-helper freshness-cheat` and
    `slash-helper inspect --kind=freshness-cheat` surfaces.
    All pass alongside the existing repo suite.
  - **Spec update**: `MINING_PROTOCOL_V2.md` §1 (overview),
    §6 (component table), §8.2 (slashing-table row), and
    §12.3 (deferred-work register) updated to reflect the
    new posture.

- **`qsdmcli watch slashes` — symmetric operator-facing
  surveillance subcommand (2026-04-28).** Polls
  `/api/v1/mining/slash/{tx_id}` for a caller-supplied set of
  slash transaction ids and streams resolution events to
  stdout. Mirrors `qsdmcli watch enrollments` in flag surface
  and wire shape so operators get matched tooling across the
  enrollment + slashing surfaces in one place. Use case: an
  operator submits a slash with `qsdmcli slash` (or assembles
  evidence offline with `qsdmcli slash-helper`), captures the
  returned `tx_id`, and the watcher surfaces "did it apply?"
  without manual polling.
  - Inputs: `--tx-id=ID` (repeatable) and/or
    `--tx-ids-file=PATH` (one tx id per line; `'#'` starts
    a comment; `-` reads from stdin); both merge and
    deduplicate. Capped at 1000 distinct tx ids per
    process; tx ids are validated against the same 256-byte
    cap and `'/'`-rejection rule the validator enforces.
  - Four slash event kinds plus shared `error`, all in the
    unified `WatchEvent` envelope so JSON-Lines consumers
    decode either watcher's stream with one struct:
    `slash_resolved` (tx transitioned from 404 → applied/
    rejected; the canonical "the slash landed" event,
    fires exactly once per id), `slash_pending` (tx is
    still 404; suppressed by default to keep the stream
    quiet, opt in via `--include-pending`),
    `slash_evicted` (tx was resolved earlier but the
    bounded `SlashReceiptStore` evicted it under FIFO
    pressure), `slash_outcome_change` (defensive — fires
    if the same tx returns a different `outcome` across
    polls; should never happen on a healthy network).
  - `--exit-on-resolved` returns `0` once every tracked
    tx has reached a terminal outcome; ideal for CI
    pipelines that submit a slash and need to wait for
    the apply. Mutually exclusive with `--include-pending`
    (the combination is a footgun and we error at flag
    parse time rather than guessing intent).
  - First-poll behaviour matches operator intuition by
    default: only already-resolved receipts emit events
    (covers the "watcher restarted after the slash
    landed" case); pending tx ids are silently tracked
    until they resolve. Pass `--include-pending` to also
    echo a `slash_pending` event each cycle for unresolved
    ids (useful when debugging "why isn't my slash
    landing?").
  - Per-cycle partial failures are non-fatal: a transient
    HTTP error on one tx id silently drops it from the
    snapshot and retries next cycle. Only a *total*
    failure (every id errors, e.g. validator unreachable
    or pointed at a v1-only node) emits an `error` event;
    on the very first cycle, total failure exits non-zero
    so misconfigured invocations fail loudly at startup.
  - Diff core (`diffSlashSnapshots`) is a pure function;
    initial-snapshot helper (`slashSnapshotInitialResolvedOnly`
    / `slashSnapshotAsInitialEvents`) and the resolved-event
    canonicaliser (`slashReceiptToResolvedEvent`) are
    likewise pure and unit-tested.
  - `WatchEvent` extended with slash-specific fields
    (`tx_id`, `outcome`, `prev_outcome`, `height`,
    `evidence_kind`, `slasher`, `slashed_dust`,
    `rewarded_dust`, `burned_dust`, `auto_revoked`,
    `auto_revoke_remaining_dust`, `reject_reason`); all
    omitempty so enrollment events still marshal to the
    same byte stream they did before. The kind enum
    gained `slash_resolved` / `slash_pending` /
    `slash_evicted` / `slash_outcome_change`. The
    human-format kind-pad width was bumped from 11 to 20
    chars so columns line up across both watcher streams
    when piped to one log file.
  - `formatEventHuman` switch dispatches on Kind so each
    event renders the field set the operator expects:
    applied receipts show `slashed`/`rewarded`/`burned`
    in CELL plus `auto_revoked=true(remaining=…)`;
    rejected receipts show `reason=…  err=…`; evictions
    show `last_outcome=…`; outcome changes show
    `outcome=A->B`.

  No new validator-side endpoints: pure-client consumer of
  the existing `/api/v1/mining/slash/{tx_id}` GET handler
  introduced in `pkg/api/handlers_slash_query.go`. Coverage:
  39 new tests (`cmd/qsdmcli/watch_slashes_test.go`) — flag
  validation (zero-id rejection, `'/'` rejection, oversize
  rejection, cap rejection, interval clamp, footgun-combo
  rejection, file + stdin merge, default first-poll filter),
  `allResolved` truth table (empty / all-pending / mixed /
  all-resolved), `diffSlashSnapshots` truth table (pending →
  resolved, resolved → pending = eviction, outcome change,
  pending steady state with and without `--include-pending`,
  resolved steady state, deterministic ordering, prev-missing
  → no event), `slashReceiptToResolvedEvent` field-mapping
  for both applied and rejected paths, both initial-snapshot
  helpers, all four slash human-format kinds (with applied-
  path / rejected-path field guards), and end-to-end
  `httptest` scenarios for `--once` empty / `--once`
  resolved-only / `--once --include-pending` / diff-loop
  pending → resolved transition / `--exit-on-resolved`
  cleanup / initial-failure-is-fatal / partial-cycle-error.
  Wire-shape parity with `api.SlashReceiptView` is asserted
  by `TestSlashReceiptWireMatchesAPI` (mirrors the
  `TestWatchRecordWireMatchesAPI` pattern). Documentation:
  new "Streaming slash-receipt events" section in
  `MINER_QUICKSTART.md` and a second row in
  `MINING_PROTOCOL_V2.md` §9.2 (operator-surface table). All
  90+ pre-existing `cmd/qsdmcli` tests remain green.

- **`qsdmcli watch enrollments` — operator-facing surveillance
  subcommand (2026-04-28).** A new diff-based polling tool that
  streams enrollment phase-change events to stdout, mirroring the
  signal that `qsdmminer-console`'s `EnrollmentPoller` already
  surfaces internally on its dashboard. Designed for fleet
  operators, indexers, and dashboard / alerting pipelines that
  want a composable building block (systemd, cron, log shippers)
  rather than a per-rig embedded poller.
  - Two modes: **list mode** (default) walks
    `/api/v1/mining/enrollments` with cursor pagination and
    supports `--phase=active|pending_unbond|revoked` server-side
    filtering; **single-node mode** (`--node-id=…`) hits
    `/api/v1/mining/enrollment/{node_id}` and treats `404` as
    "no record".
  - Five event kinds, all sharing one `WatchEvent` wire shape:
    `new`, `transition`, `stake_delta`, `dropped`, `error`. The
    `transition` event wins over `stake_delta` when both apply
    in the same poll (e.g. a partial slash that crosses
    auto-revoke), so an operator never has to reconcile two
    events about the same node_id from one cycle.
  - Two output modes: **human** (column-aligned RFC3339 + kind
    + `node=…` + phase/stake summary, default) and **`--json`**
    (JSON-Lines, one event per line, including `error` events
    so log shippers see the error stream in-line).
  - Deterministic ordering: events from a single tick are
    sorted by `node_id` ASC, so two consecutive runs over the
    same data produce byte-identical output. Diff captures
    against expected logs work without filtering.
  - Operational defaults match the embedded poller:
    `--interval` defaults to 30s, clamped to ≥ 5s; the same
    `MaxWatchPages = 10000` defence as `qsdmcli enrollments
    --all` against a misbehaving server returning
    `has_more=true` forever; 1 MiB body cap per request.
  - Exit codes: `0` on `SIGINT`/`SIGTERM`. Non-zero **only**
    when the very first snapshot fails (so the operator catches
    URL typos and v1-only validators at startup); subsequent
    poll failures emit a `WatchKindError` event and the loop
    continues.
  - `--once` (single snapshot then exit) and
    `--include-existing` (synthesise a `new` event per existing
    record on the first poll) compose for a one-shot dump:
    `qsdmcli watch enrollments --once --include-existing --json`
    is the canonical "give me every enrolled node_id right now,
    in JSON-Lines, in one process" call.

  No new validator-side endpoints: this is a pure-client
  consumer of the existing `/api/v1/mining/enrollment*` reads.
  Coverage: 35 new tests (`cmd/qsdmcli/watch_test.go`) — flag
  normalisation, the pure-function diff core, human / JSON
  formatting, end-to-end `httptest`-driven scenarios for
  initial-failure-is-fatal, single-node 404, single-node happy
  path, list-mode `--once`, list-mode `--include-existing`, and
  diff-loop phase-transition observation. Wire-shape parity
  with `api.EnrollmentRecordView` is asserted by
  `TestWatchRecordWireMatchesAPI`. Documentation: new
  "Streaming phase-change events" section in
  `MINER_QUICKSTART.md` and a row in `MINING_PROTOCOL_V2.md`
  §9.2 (operator surface table).

### Documentation

- **v2 mining-protocol spec consolidation (2026-04-28).** The three
  historical fragments
  [`MINING_PROTOCOL_V2_NVIDIA_LOCKED.md`](QSDM/docs/docs/MINING_PROTOCOL_V2_NVIDIA_LOCKED.md)
  (Phase-1 design draft),
  [`MINING_PROTOCOL_V2_RATIFICATION.md`](QSDM/docs/docs/MINING_PROTOCOL_V2_RATIFICATION.md)
  (2026-04-24 owner sign-off), and
  [`MINING_PROTOCOL_V2_TIER3_SCOPE.md`](QSDM/docs/docs/MINING_PROTOCOL_V2_TIER3_SCOPE.md)
  (rolling shipped-vs-deferred register) have been merged into a
  single canonical spec at
  [`MINING_PROTOCOL_V2.md`](QSDM/docs/docs/MINING_PROTOCOL_V2.md).
  The new spec carries an unambiguous §0–§14 numbering scheme,
  inline shipped/deferred status against concrete Go files in
  every §§5–9 table, a consolidated deferred-work register at §12,
  and the historical decision record at §13. The three superseded
  fragments are retained as redirect stubs with section-by-section
  mapping tables, so existing PR / issue / landing-page / source-
  comment references keep resolving. Cross-references in
  [`README.md`](README.md), the landing page
  ([`QSDM/deploy/landing/index.html`](QSDM/deploy/landing/index.html)),
  [`MINER_QUICKSTART.md`](QSDM/docs/docs/MINER_QUICKSTART.md), the
  wiki sync script
  ([`QSDM/scripts/sync-wiki.sh`](QSDM/scripts/sync-wiki.sh)), and
  ~13 Go source-comment sites
  (`pkg/mining/{fork,proof,challenge,enrollment,attest/cc,attest/hmac,slashing}`,
  `pkg/api/handlers.go`, `pkg/chain/slash_apply.go`,
  `cmd/qsdmcli/mining.go`) now point at the canonical doc and the
  correct anchors. No consensus or wire-format change.

### Changed / Deprecated

- **NVIDIA-lock pivot — retire the CPU-miner onboarding UX
  (2026-04-24).** The project is re-aligning on the architecture
  described in `nvidia_locked_qsdm_blockchain_architecture.md`:
  the mainline protocol will hard-fork to `v2` which requires a
  valid NGC attestation bundle on every proof, making CPU-only and
  non-NVIDIA-GPU mining impossible on mainnet by construction. As
  a first, fully-reversible step in that rollout we:
  - **Delete** `scripts/install-qsdmminer-console.sh`,
    `scripts/install-qsdmminer-console.ps1`, and
    `QSDM/Dockerfile.miner-console`. Nobody should be onboarded to
    a mining path that will stop earning rewards in the next major
    release.
  - **Remove** the `ghcr-miner-console` job from
    `release-container.yml` and the companion
    `docker-miner-console-build` + `install-scripts-lint` jobs
    from `qsdm-split-profile.yml`. No new `qsdm-miner-console`
    image will be published on the next tag. Previously-pushed
    tags remain on GHCR for operators who want to roll forward
    manually during the deprecation window.
  - **Add** a startup deprecation banner to `cmd/qsdmminer` and
    `cmd/qsdmminer-console` pointing at the NVIDIA-lock design
    doc. The banner is suppressed on `--version` and `--self-test`
    (machine-parseable paths must stay clean for CI) but fires on
    every real mining run.
  - **Update** `MINER_QUICKSTART.md §2.5` and `OPERATOR_GUIDE.md
    §3.4` — the one-command install block is replaced by a
    deprecation notice, and the sections are relabelled "testnet /
    reference only".

  The binaries themselves are **not** deleted in this pass. They
  continue to ship as release artefacts, build cleanly, and pass
  `--self-test`, so testnet operators who want to replay the
  current protocol can still do so. The actual retirement of
  `cmd/qsdmminer` and `cmd/qsdmminer-console` will land together
  with the v2 hard fork (phased plan recorded in the issue
  tracker).

### Withdrawn

- **Docker image `ghcr.io/<owner>/qsdm-miner-console` + one-command
  install scripts for Linux / macOS / Windows (2026-04-24).**
  Originally landed in c4bdca5 and now retired without having been
  tagged — see the "NVIDIA-lock pivot" entry above. Preserving
  the original entry for historical context:

- **Docker image `ghcr.io/<owner>/qsdm-miner-console` + one-command
  install scripts for Linux / macOS / Windows (2026-04-24).** The
  console miner now has two frictionless install paths in addition to
  the signed binaries:

  1. **Container image** — a ~15 MB CPU-only image on
     `gcr.io/distroless/static-debian12:nonroot`. Built by a new
     `ghcr-miner-console` job in `release-container.yml` on every
     `v*` tag, pushed to GHCR with semver, major.minor, and major
     tags. Build-args propagate the same `BUILDINFO_*` values the
     binary release workflow injects, so `docker inspect
     ghcr.io/.../qsdm-miner-console:<tag>` and
     `docker run ... --version` both surface the exact release tag
     + commit SHA. Default entrypoint runs with `--plain` and
     `--config /config/miner.toml`, so the canonical invocation is
     `docker run -v $HOME/.qsdm:/config ... --validator=… --address=…`.
     `qsdm-split-profile.yml` gains a companion `docker build
     (miner-console)` job that builds the image no-push on every
     push, runs `--version` against a synthetic-tag build to verify
     the build-arg → ldflags pipeline, and executes `--self-test`
     inside the container to gate protocol conformance.

  2. **`scripts/install-qsdmminer-console.sh`** — Linux/macOS
     installer intended for `curl -sSL … | bash`. Detects platform,
     resolves the latest release via the GitHub API (or honours
     `QSDM_VERSION=vX.Y.Z`), downloads the matching
     `qsdmminer-console-<os>-<arch>` binary plus `SHA256SUMS`,
     verifies the hash (refuses to install on mismatch), installs
     to `$QSDM_INSTALL_DIR` / `/usr/local/bin` / `~/.local/bin`
     (whichever is writable without surprise `sudo`), and runs
     `--version` to confirm the binary identifies as a release
     build. A `dev` or `unknown` in the `--version` line aborts
     the install — a defence-in-depth assertion that the download
     did not bypass the release pipeline.

  3. **`scripts/install-qsdmminer-console.ps1`** — Windows
     PowerShell 5.1+ equivalent, bootstrappable via `iwr … | iex`.
     Never elevates (installs under `%LOCALAPPDATA%\Programs\QSDM`
     by default), performs the same SHA-256 verification with
     `Get-FileHash`, runs the installed binary's `--version`, and
     aborts on `dev`/`unknown` metadata identically to the bash
     installer.

  Both install scripts gain a `install-scripts-lint` CI job
  (`shellcheck` + `bash -n` for the sh, `PowerShell Parser` for the
  ps1) so syntax regressions are caught at push time — these are
  on the critical path for new-operator onboarding so any drift
  cannot be tolerated.

- **Embedded build metadata + `--version` on every release artefact
  (2026-04-24).** Every one of the four release binaries (`qsdmminer`,
  `qsdmminer-console`, `trustcheck`, `genesis-ceremony`) now accepts a
  `--version` flag that prints a single line identifying the exact
  artefact:

  ```
  qsdmminer-console v0.1.0 (abc1234, 2026-04-22T10:00:00Z, go1.25.9, linux/amd64)
  ```

  The values come from a new `pkg/buildinfo` package whose three vars
  (`Version`, `GitSHA`, `BuildDate`) are injected at link time by
  `.github/workflows/release-container.yml` via `-ldflags -X`. Local
  `go build` / `go run` produce `dev` / `unknown` sentinels — a
  deliberate, inspectable signal that the binary was not produced by
  the release pipeline. An `IsReleaseBuild()` helper exposes the
  same distinction to downstream callers that want to gate telemetry
  or Prometheus labels to released builds only.

  Two CI gates protect the wiring:
  - `qsdm-split-profile.yml` gains a `--version smoke` step under
    both profile matrix cells that runs each in-scope binary with
    synthetic ldflags and asserts the injected tag + SHA appear in
    the output. This catches a regression where a new `cmd/` binary
    is added without wiring `--version` to `buildinfo`.
  - `release-container.yml` runs the same smoke on the native
    (linux/amd64) matrix cell against the actual release ldflags —
    so any tag push that would have shipped a "dev" binary fails
    before the upload step.

  The `pkg/buildinfo` package ships with four dedicated unit tests
  (default sentinels visible, ldflags injection honoured, short banner
  stays terse, `IsReleaseBuild` distinguishes all five sentinel
  combinations). Tests execute under both validator and full CI
  profiles because every release artefact — including the
  validator-profile `trustcheck` — depends on it.

- **HTTP integration test for `cmd/qsdmminer-console` (2026-04-24).**
  Unit tests covered pure helpers; `--self-test` covered the in-process
  protocol; neither exercised the HTTP pipeline that a real miner uses
  against a validator. A new `cmd/qsdmminer-console/integration_test.go`
  drives `fetchWork` / `submitProof` / the full `runLoop` end-to-end
  against an in-process `httptest.Server` that mimics
  `/api/v1/mining/{work,submit}`. Five tests:

  - `TestIntegration_FetchWork_RoundTrip` — validator-shaped JSON
    decodes through `api.MiningWork` with every wire field preserved;
    asserts the `Accept: application/json` header the miner sends.
  - `TestIntegration_FetchWork_HTTPErrorSurfacesStatus` — a 503
    surfaces its status code in the returned error (so rate-limit /
    warming-up errors don't get mis-logged as decode failures).
  - `TestIntegration_SubmitProof_AcceptedParsesProofID` — a 200
    `Accepted=true` populates `ProofID` and the `Content-Type: application/json`
    header is present on the request.
  - `TestIntegration_SubmitProof_BadRequestStillDecodesRejection` —
    the validator returns 400 with a shaped `MiningSubmitResponse`
    for rejections; the miner must decode the body rather than
    treat it as a transport error.
  - `TestIntegration_RunLoop_EndToEnd` — runs the full loop against
    a fixture server, waits for the first `EvProofAccepted`, and
    asserts `EvConnected` / `EvEpochChanged` / `EvDAGReady` fired in
    the right order before it. This is the strongest regression gate
    in the suite: any break in fetch → DAG build → Solve → submit
    → event emission causes the test to time out.

  Tests execute in <2s on CI hardware (difficulty=2, `N=128`, same
  budget as `--self-test`) so they run on every `go test
  ./cmd/qsdmminer-console/...` without a `-short` gate. No extra CI
  job needed — they flow through the existing `full` profile of
  `qsdm-split-profile.yml`.

- **CI + release coverage for `cmd/qsdmminer-console`
  (2026-04-24).** The friendly console miner binary added earlier
  today is now a first-class release artifact and has push-time
  protocol-drift protection:

  - `.github/workflows/release-container.yml` builds
    `qsdmminer-console-<os>-<arch>[.exe]` alongside the existing
    `qsdmminer` / `trustcheck` / `genesis-ceremony` binaries on
    every `v*` tag (linux amd64/arm64, darwin amd64/arm64,
    windows amd64). Same `-trimpath -ldflags="-s -w"` CGO-free
    deterministic build as the other release binaries, folded
    into the consolidated `SHA256SUMS` asset, and uploaded to
    the GitHub Release. Non-Go-developer miners can now grab a
    signed binary instead of installing a toolchain.
  - `.github/workflows/qsdm-split-profile.yml` runs the new
    `./cmd/qsdmminer-console/...` unit test suite under the
    `full` profile matrix cell (12 tests; config round-trip,
    malformed-TOML rejection, poll defaulting, formatters,
    dashboard state machine, plain-renderer format, kindLabel
    exhaustiveness), and the `validator_only` cell explicitly
    excludes the package because it depends on `pkg/mining`
    which is absent from the validator tag surface.
  - Same workflow gains a `Protocol self-test` step under the
    `full` profile that `go run`s `qsdmminer --self-test` and
    `qsdmminer-console --self-test` back-to-back. Both exercise
    the same `pkg/mining.Solve` / `Verify` round-trip against
    independent `main` packages, so any drift between the
    reference miner and the console miner relative to
    `MINING_PROTOCOL.md` surfaces on push instead of at release
    time.

- **`OPERATOR_GUIDE.md §3.4` rewritten to reflect the two miner
  binaries (2026-04-24).** Previously named only `qsdmminer` and
  walked the reader through flag-heavy setup. The new section
  presents `qsdmminer-console` as the recommended path for home
  operators (wizard + live panel + config persistence), keeps
  `qsdmminer` as the protocol-truth reference for conformance
  testing, and documents that pre-built signed binaries now ship
  on every tagged release for both.

- **`cmd/qsdmminer-console` — friendly console miner binary
  (2026-04-24).** A sibling of `cmd/qsdmminer` that layers three
  ergonomic improvements on top of the same `pkg/mining` primitives,
  without touching the reference miner's audit-clean surface:

    1. **First-run setup wizard.** Running `qsdmminer-console` with
       no flags prompts for validator URL, reward address, batch
       count, and poll interval; answers are persisted to
       `~/.qsdm/miner.toml` (Windows: `%USERPROFILE%\.qsdm\miner.toml`)
       at mode 0600 and reused by future runs.
    2. **Live console panel.** In a TTY, the binary redraws a 14-line
       panel at 2 Hz showing reward address (redacted),
       validator URL, connection state (colored), current epoch
       and DAG readiness, 10-second rolling hashrate, accepted /
       rejected proof counters, uptime, and the last event. Non-TTY
       stdout (pipe, `journalctl`, CI) auto-detects and falls back
       to a one-line-per-event log. `--plain` forces the log mode
       on demand.
    3. **Flag overrides.** Every config field has a corresponding
       flag (`--validator`, `--address`, `--batch-count`, `--poll`,
       `--config`) so an operator can point at a different node for
       one run without editing the TOML. `--setup` re-runs the
       wizard. `--self-test` runs the same Phase 4.5 acceptance
       gate as `qsdmminer --self-test`.

  The mining loop is a targeted port of `cmd/qsdmminer`'s
  `fetchWork` / `Solve` / `submitProof` flow, emitting typed
  `Event`s into a channel consumed by the renderer. This keeps the
  `qsdmminer` reference binary unchanged — that binary remains
  mappable 1-to-1 against `MINING_PROTOCOL.md` with no TUI layered
  on top — while giving home operators a less hostile first-run
  experience. 12 unit tests cover config round-trip, malformed-TOML
  rejection, `pollDuration` defaulting, the hashrate / duration /
  address formatters, the `Dashboard` event state machine, the
  plain renderer's log format, and the `kindLabel` exhaustiveness
  guard against silently missing an `EventKind` in the log-label
  switch.

- **Landing page + MINER_QUICKSTART.md reflect that the reference
  miner is shipped (2026-04-24).** The landing page previously
  described the mining layer as "planned" (`#products` tile, `#mine`
  section lead, `#consensus-layer` pillar, footer link). Those have
  been updated to name both miner binaries and to clarify that only
  the CUDA production miner is gated on external audit.
  `MINER_QUICKSTART.md` gains a new `§2.5 Friendly console miner`
  that walks through build, wizard, panel, and flag overrides, and
  links the §2 reference-binary section to it as the recommended
  starting point for home operators. The root `README.md`'s
  summary paragraph and the "Run a miner" bullet were corrected
  accordingly (the prior wording implied GPU-bound PoW was the only
  path, which is untrue).

- **`cmd/trustcheck` JSON output schema is now pinned by tests
  (2026-04-24).** The `--json` flag and the `trustcheck.json`
  artifact upload in `trustcheck-external.yml` have shipped for
  several sessions, but the wire shape (top-level `summary`,
  `recent`, `assertions`, `pass` keys; per-row `name`/`pass`/
  optional `detail`; summary mirror of `attested`/`total_public`/
  `ratio`/`fresh_within`/`last_attested_at`/`last_checked_at`/
  `ngc_service_status`/`scope_note`) was not covered by tests, so a
  rename of any JSON tag would have silently broken every Datadog /
  Grafana / `jq` pipe consuming the artifact. Refactored
  `emitJSON(...)` in `cmd/trustcheck/main.go` to delegate to a pure
  `buildJSONReport(...)` helper, then added five schema tests in
  `main_test.go` covering (a) the top-level required keys, (b)
  per-row `name`/`pass` shape with `detail` omitted on pass rows,
  (c) top-level `pass` mirroring `rs.allOK()`, (d) `summary` and
  `recent` sub-objects being omitted when nil (the warming-up /
  disabled informational paths), and (e) the summary wire-field
  names matching the server-side `pkg/api.TrustSummary` JSON tags.
  Any future rename now fails the test and forces the contract
  change to be explicit in the diff. 23 tests pass
  (18 existing + 5 new).

### Changed

- **Landing-page roadmap widget synced with reality (2026-04-23).**
  `deploy/landing/index.html` was still showing Phase 2 as "In progress"
  and Phase 3 as "Next" — both phases have been shipped in-tree for
  several sessions (submesh rules, Scylla migrate with dry-run,
  `/api/v1/network/topology`, finality-gadget partition heal, NVIDIA
  lock enforcement). Flipped the Phase 2 and Phase 3 status pills to
  `Shipped`, rewrote the lead paragraph to stop claiming deployments
  are on "Phase 1 infrastructure with Phase 2 submesh routing enabled",
  and added a post-grid beat linking to `CELL_TOKENOMICS.md`,
  `MINING_PROTOCOL.md`, and the `/trust.html` surface so the widget
  doesn't imply development ended at Phase 3 (the in-repo Major Update
  Phases 1–5 continue the arc, with only wall-clock-blocked gates —
  trademark filings, `mining-01` external audit, `mining-05`
  incentivized testnet, and the mainnet genesis ceremony — remaining,
  as tracked in `NEXT_STEPS.md`). Same phase-card CSS
  (`repeat(3, 1fr)` grid, `.status.shipped` pill), so no stylesheet
  changes were needed.

### Added

- **Quarantine Prometheus gauges + alert group (2026-04-23).**
  `pkg/quarantine/metrics.go` now exports four gauges via a
  nil-safe `MetricsCollector(*QuarantineManager)` closure, mirroring
  the `api.TrustMetricsCollector` pattern:

    - `qsdm_quarantine_submeshes` — count of submeshes currently
      quarantined.
    - `qsdm_quarantine_submeshes_tracked` — distinct submeshes the
      manager has ever observed (union of the three internal maps, so
      a submesh with only 1–9 transactions is counted before the
      10-tx window boundary writes into `quarantined`).
    - `qsdm_quarantine_submeshes_ratio` — quarantined / tracked, with
      `0` when `tracked==0` so ratio-based alerts don't flap on a
      quiet node.
    - `qsdm_quarantine_threshold` — the configured invalid-ratio
      policy threshold, exposed so dashboards render the decision
      boundary next to the observed state.

  The collector is registered in `cmd/qsdm/main.go` alongside the
  other `pe.RegisterCollector(...)` calls inside the `dash != nil`
  block. A new method `QuarantineManager.Stats()` returns a consistent
  snapshot under the existing mutex; collector scrapes are O(1) in
  the number of tracked submeshes.

  `alerts_qsdm.example.yml` gains a new `qsdm-quarantine` group
  with two rules:

    - **`QSDMQuarantineAnySubmesh`** (warn, 10m) — any non-zero
      quarantined count worth a human decision on recovery.
    - **`QSDMQuarantineMajorityIsolated`** (critical, 15m) — fires
      when `ratio > 0.5` and `tracked >= 4` (the `tracked` guard
      prevents flap on tiny fleets where 1/2 crosses the ratio).

  No warm-gate is needed (the manager is live from process start, so
  zero-at-t=0 is literally correct, and the denominator guard inside
  the collector keeps empty-fleet scrapes from paging). Tests live in
  `pkg/quarantine/metrics_test.go` covering nil-manager, empty-manager
  shape, sub-window tracking, full-window quarantine, post-removal
  gauges, and the `Stats.Quarantined` counts-only-true invariant.

- **`ATTESTATION_SIDECARS.md` operator guide (2026-04-23).** The
  recipe for getting the trust pill to `N/N` by standing up N
  attestation sources was previously spread across two CHANGELOG
  entries, `install_ngc_sidecar_vps.py` docstrings, and session
  notes. New `QSDM/docs/docs/ATTESTATION_SIDECARS.md` consolidates
  it: the reference three-source deployment (Windows PC + BLR1 VPS
  + OCI), the four required invariants (shared ingest URL, shared
  `QSDM_NGC_INGEST_SECRET`, **distinct** `QSDM_NGC_PROOF_NODE_ID`,
  cadence ≤ `fresh_within`/2), one-command install snippets per
  platform, a five-step verification ladder
  (`journalctl` → ingest counter → `qsdm_trust_*` gauges → public
  summary JSON → external CI probe), and a troubleshooting table
  keyed on the symptom operators actually see. Cross-linked from
  the aggregator implementation, Prometheus gauges, alert rule
  example, and the `trustcheck --min-attested` flag so someone
  landing in any of those files can find the canonical setup.

### Changed

- **`build_kernels.ps1` defaults to a Turing→Hopper fatbin
  (2026-04-23).** Previous default `-Arch 'sm_86'` only produced a
  DLL that worked on Ampere; running the same DLL on an RTX 4090
  (Ada, sm_89) or an H100 (Hopper, sm_90) silently fell back to a
  JIT recompile or failed to launch. New default
  `'sm_75,sm_86,sm_89,sm_90'` emits a fatbin covering Turing, Ampere,
  Ada, and Hopper in one build — roughly +30 s compile vs. the
  single-arch default, and no per-card rebuild for the four GPU
  lineups we've exercised on. Iterating on a known host can still
  narrow via `-Arch 'sm_86'` explicitly (documented under
  `.PARAMETER Arch` and in the `MESH3D_GPU_BENCHMARK.md`
  reproduction steps).

- **Prometheus alert rules for the trust-redundancy surface
  (2026-04-23).** Now that `qsdm_trust_attested` /
  `qsdm_trust_total_public` / `qsdm_trust_last_attested_seconds` /
  `qsdm_trust_last_checked_seconds` / `qsdm_trust_warm` /
  `qsdm_trust_ngc_service_healthy` gauges are exported by
  `api.TrustMetricsCollector`, the example rule file
  `QSDM/deploy/prometheus/alerts_qsdm.example.yml` gains a new
  `qsdm-trust-redundancy` group mirroring the external CI probe's
  `--min-attested 2` floor from inside Alertmanager:

    - **`QSDMTrustAttestationsBelowFloor`** — fires when a *warm*
      aggregator reports `qsdm_trust_attested < 2` for 10 min. Gated
      on `qsdm_trust_warm == 1` so redeploys do not page (the ring
      buffer is volatile; `attested` recovers on the next sidecar
      cadence).
    - **`QSDMTrustNGCServiceDegraded`** — fires when a warm aggregator
      reports `qsdm_trust_ngc_service_healthy == 0` (mapped from the
      summary JSON's `ngc_service_status` enum) for 10 min.
    - **`QSDMTrustLastAttestedStale`** — fires when the newest
      attestation is older than 30 min (twice the default
      `fresh_within` of 15 min), catching slow-death scenarios
      before `attested` itself tips to zero.
    - **`QSDMTrustAggregatorStale`** (severity `critical`) — fires
      when `qsdm_trust_last_checked_seconds` has not advanced for
      > 2 min (Refresh ticker wedged). Default refresh cadence is
      10 s, so this is unambiguously a stuck goroutine.

  The existing `qsdm-trust-transparency` group (proxy-metric alerts
  on the accepted/rejected ingest counter) is complementary and
  retained: it fires when *no* proof is flowing at all, regardless of
  aggregator state; the new group fires when proofs flow but the
  aggregator's distinct-source count drops below the operator's
  declared floor. Both sides — external CI probe + internal
  Prometheus — now enforce the same `attested >= 2` invariant from
  independent vantage points.

- **Prometheus gauges for the trust-transparency surface
  (2026-04-23).** The §8.5.x trust numbers (`attested`,
  `total_public`, `ratio`, `ngc_service_status`, `last_attested_at`,
  `last_checked_at`, warm-up state) were previously only available
  via `GET /api/v1/trust/attestations/summary`. Alertmanager and
  Grafana cannot scrape a bespoke JSON endpoint without bespoke
  exporters, so a silent drop from `attested=3` back down to
  `attested=1` was undetectable short of a human checking the
  widget. New `api.TrustMetricsCollector(*TrustAggregator)`
  registers a nil-safe, O(1) collector on
  `monitoring.GlobalScrapePrometheusExporter()` that surfaces:

    - `qsdm_trust_attested`                (gauge)
    - `qsdm_trust_total_public`            (gauge)
    - `qsdm_trust_ratio`                   (gauge)
    - `qsdm_trust_ngc_service_healthy`     (gauge, 0/1)
    - `qsdm_trust_last_attested_seconds`   (gauge, unix seconds)
    - `qsdm_trust_last_checked_seconds`    (gauge, unix seconds)
    - `qsdm_trust_warm`                    (gauge, 0/1)

  The collector reads the aggregator's already-cached summary on
  every scrape, so there is no new locking, no new ticker, no new
  wire traffic. It registers unconditionally when
  `[trust] disabled=false` and emits nothing when the aggregator
  is disabled (rather than zeroes that would falsely imply a
  denominator). Grafana alerts gated on `qsdm_trust_warm == 1` stay
  silent through a restart because the warm bit flips only after
  the aggregator's first full `Refresh()`. Full gauge shape,
  HELP text, and timestamp-parse behaviour are covered by
  `pkg/api/trust_metrics_test.go`.

- **`trustcheck --min-attested N` policy floor (2026-04-23).** The
  external transparency probe previously only validated the §8.5.x
  wire contracts (scope-note verbatim, enum membership,
  ratio-sanity, etc.) — none of which trip when a deployment
  silently loses attestation sources. Every value of `attested`
  from 0 through the entire validator set is a legal wire contract,
  by design. A new operator-policy flag `--min-attested N` lets a
  deployment declare an intended redundancy floor; the probe fails
  loudly when `summary.attested < N`. The assertion lives in a
  standalone `validateMinAttested` helper so running trustcheck
  without the flag (default `0`) behaves exactly as before. The
  GitHub-Actions workflow `.github/workflows/trustcheck-external.yml`
  defaults to `--min-attested 2` on scheduled / push / pull_request
  runs (matching the deployed "primary validator + OCI sidecar"
  invariant), and exposes a `min_attested` workflow-dispatch input
  so ops can drop the floor to `0` during a single-sidecar
  maintenance window.

- **Trust aggregator now counts distinct CPU-fallback sidecars as
  separate attestation sources (2026-04-23).** Operators who run
  multiple CPU-fallback attestation sidecars — e.g. one on the main
  validator VPS, one on an Oracle Cloud VM, one on a local dev PC,
  each stamping its own `QSDM_NGC_PROOF_NODE_ID` — were being
  collapsed into a single "local" peer row by `TrustAggregator`.
  `MonitoringLocalSource.LocalLatest()` only exposed the newest row
  from `monitoring.NGCProofSummaries()` and stamped it with the
  validator's libp2p host id, so ten sidecars looked like one peer
  and `attested` never climbed past 1 no matter how much redundant
  CPU attestation was running.

  New `monitoring.NGCProofDistinctByNodeID()` walks the NGC proof
  ring buffer and groups entries by `qsdm_node_id` (or the
  legacy `qsdm_node_id` alias), keeping the newest-observed bundle
  for each distinct id. New optional interface
  `api.LocalDistinctAttestationSource` exposes that view to the
  aggregator; `MonitoringLocalSource` now implements it, and
  `TrustAggregator.Refresh` prefers the distinct view when
  available, folding empty-id rows onto the local node's identity
  so bundles without an id still behave like before. Sources that
  don't implement the new interface fall back to the old single-row
  path — this is a strict addition, not a behaviour swap for legacy
  embedders.

  Verified live against the reference validator: adding the second
  and third sidecars (OCI `ap-singapore-1` and DO `blr1`) flipped
  `GET /api/v1/trust/attestations/summary` on `api.qsdm.tech` from
  `attested=1, total_public=2` to `attested=3, total_public=4`
  within one 10 s refresh tick, with each sidecar showing a distinct
  redacted `node_id_prefix` in `/recent`.

  Semantics note (recorded here, not a regression): anyone holding
  `QSDM_NGC_INGEST_SECRET` can drive `attested` up by POSTing
  from N distinct `qsdm_node_id` values. That is acceptable
  because (a) the ingest secret is already the trust root for this
  surface, (b) the `scope_note` field in every summary response
  caveats that these attestations are not consensus, and (c) the
  aggregator's freshness window (default 15 min) still applies per
  row, so a one-shot spoof cannot hold the pill green without
  continuous posts.

### Fixed

- **mesh3d Windows DLL now exports its host-side entry points
  (2026-04-23).** `pkg/mesh3d/kernels/sha256_validate.cu` declared
  `mesh3d_hash_cells` and `mesh3d_validate_cells` inside an
  `extern "C"` block but without `__declspec(dllexport)`. On Linux
  this is harmless — ELF exports every non-static symbol by default
  — but MSVC's PE linker only exports symbols explicitly marked
  dllexport, so the Windows build of `mesh3d_kernels.dll` shipped
  with a single `NvOptimusEnablementCuda` export and nothing else.
  CGO builds with `-tags cuda` then died with
  `undefined reference to 'mesh3d_hash_cells'` at link time.

  New `MESH3D_API` macro (`__declspec(dllexport)` on `_WIN32`,
  `__attribute__((visibility("default")))` elsewhere) now decorates
  both entry points; Linux `.so` behaviour is unchanged, Windows
  `.dll` now correctly exports the two symbols CGO needs. Verified
  via `gendef - mesh3d_kernels.dll` showing the symbols and via
  the `BenchmarkMesh3DGPUVsCPU` linking against them.

- **`pkg/mesh3d/cuda.go` no longer requires a `C:/CUDA` symlink on
  Windows (2026-04-23).** The old cgo directive block hard-coded
  `-IC:/CUDA/include -LC:/CUDA/lib/x64`, which is nowhere an
  NVIDIA installer ever lands. Every fresh Windows dev box failed
  to build until the operator manually created `C:\CUDA`. Replaced
  with split platform directives: Linux keeps `/usr/local/cuda`
  defaults, Windows relies on `CGO_CFLAGS` / `CGO_LDFLAGS` set by
  `QSDM/scripts/build_kernels.ps1`, which probes `$env:CUDA_PATH`
  and emits DOS 8.3 short-path forms so cgo's whitespace-splitting
  directive parser sees no spaces.

- **Dashboard user accounts now survive a service restart
  (2026-04-23).** `pkg/api.UserStore` used to be an in-memory
  `map[string]*User` with no persistence layer, so every
  `systemctl restart qsdm` (routine during redeploys) silently
  wiped every registered dashboard login. `AuthenticateUser` returned
  "user not found", which the handler then mapped to a generic
  `401 invalid credentials` — so the symptom an operator saw was
  "I swear I registered yesterday, why am I locked out?". Ledger
  state (transactions, balances, staking, bridge) was already
  persisted; only the dashboard-login credential map was affected.

  Fix in `pkg/api/user_persist.go`: versioned JSON file
  (`/opt/qsdm/qsdm_users.json`, mode `0600`), atomic
  temp-file-rename on every mutation, loader fail-closed on unknown
  version / malformed JSON (never silently reset). `RegisterUser`
  rolls back the in-memory insert when the disk write fails, so
  callers never observe a "registered but not persisted" half-state.

  Configured via `Config.UserStorePath`
  (TOML `[api] user_store_path`) with env overrides
  `QSDM_USER_STORE_PATH` / legacy `QSDM_USER_STORE_PATH`. The
  default in `cmd/qsdm/main.go` is
  `<dirname(SQLitePath)>/qsdm_users.json`, matching the sibling
  `qsdm_staking.json` / `qsdm_bridge_state.json` layout.

  Tests in `pkg/api/user_persist_test.go` cover the round-trip
  (register → reopen → auth), persist-failure rollback, unknown-
  version fail-closed, and malformed-JSON fail-closed. Verified
  end-to-end against the live VPS on 2026-04-23: registered,
  `systemctl restart qsdm`, re-logged in successfully from
  `https://dashboard.qsdm.tech/`.

### Added

- **Full mesh3d GPU benchmark runnable on a dev box (2026-04-23).**
  New `QSDM/source/pkg/mesh3d/mesh3d_gpu_bench_test.go` contains
  `BenchmarkMesh3DGPUVsCPU_Validate` and `_Hash`, each sweeping
  n ∈ {16, 256, 4096} across the CUDA and CPU-parallel backends
  with `b.SetBytes` so `go test -bench` prints MB/s directly.
  Skips with a clear diagnostic (`build mesh3d_kernels.dll / .so
  first`) when the GPU path isn't available, so CI runs on the
  CPU baseline without failing.
- **`QSDM/scripts/build_kernels.ps1` — Windows CUDA kernel build
  helper (2026-04-23).** Auto-locates CUDA via `$env:CUDA_PATH`
  (with a fall-back scan of the canonical install root),
  auto-locates MSVC via `vswhere.exe`, sources `vcvars64.bat`
  into a `cmd.exe` subshell for nvcc, compiles
  `mesh3d_kernels.dll` with per-GPU `-gencode` (default `sm_86`
  for the RTX 3050, comma-list supported), mirrors the DLL next
  to the Go source, regenerates a MinGW-compatible
  `libmesh3d_kernels.dll.a` via `gendef` + `dlltool` so MSYS2 Go
  + cgo can link it, and prints (or sets with `-SetEnv`) the
  `CGO_CFLAGS` / `CGO_LDFLAGS` / `PATH` lines the next build
  needs.
- **`QSDM/scripts/build_liboqs_win.ps1` — local liboqs build
  (2026-04-23).** Clones liboqs into
  `%LOCALAPPDATA%\QSDM\liboqs`, configures with CMake + Ninja +
  MinGW-w64 gcc + MSYS2 OpenSSL 3, builds the `oqs` target with
  `-DOQS_OPT_TARGET=generic` (MinGW doesn't assemble the AVX2
  fast paths), installs to `%LOCALAPPDATA%\QSDM\liboqs_install`,
  and emits CGO env lines that compose cleanly with the CUDA
  ones. Total runtime ~2 min on an RTX-3050-class dev box.
  Matches the Dockerfile.miner production build so local and
  CI signing/verification produce the same artefacts.
- **`docs/docs/MESH3D_GPU_BENCHMARK.md` — reference benchmark
  numbers (2026-04-23).** RTX 3050 + Xeon E5-2670 reference
  figures (0.04× at n=16, 4.06× at n=4096 for validate; 0.03×
  / 2.23× for hash) with the reproduction recipe and operator
  guidance on when a GPU actually helps mesh3d throughput. Cited
  from `docs/docs/MINER_QUICKSTART.md` so a new miner operator
  knows whether buying a card is worth it for their fan-out.

- **Live trust pill on `qsdm.tech` navigation bar (2026-04-23).**
  Compact `trust: 1/2 · healthy` chip next to `Open Dashboard`, poll
  of `/api/v1/trust/attestations/summary` every 60 s, four visual
  states (healthy / warming / degraded / offline). Shares the existing
  trust-widget fetch loop — one HTTP call drives both the pill and
  the pre-existing full-width widget. Hidden on viewports `<900px` so
  it does not crowd mobile.
- **Prometheus alert rules for attestation transparency
  (2026-04-23).** New group `qsdm-trust-transparency` in
  `deploy/prometheus/alerts_qsdm.example.yml` with two rules on
  the canonical `qsdm_*` prefix:
  `QSDMTrustNoAttestationsAccepted` (accepted-rate zero for 20 m) and
  `QSDMTrustIngestRejectRateElevated` (rejects outpace accepts by
  >1/s for 10 m). Safe to load on both dual-emit and legacy scrapers.
- **Grafana panels 16 + 17 in `qsdm-overview.json`
  (2026-04-23).** Stat "NGC attestations in last 15 min" with
  red/yellow/green thresholds matched to the pill on qsdm.tech
  (0 = red, ≥1 = orange, ≥3 = green) plus a bar chart
  "NGC attestations per hour (rolling)" for Scheduled-Task cadence
  verification. Idempotent patch — panel IDs 1–15 unchanged.
- **Self-rotating transcript in `local-attest.ps1` (2026-04-23).**
  New `-LogPath` / `-LogMaxBytes` (default 10 MiB) / `-LogKeep`
  (default 3) parameters. Rotates ring-style (`.1`, `.2`, `.3`)
  before opening the transcript and between loop iterations so the
  10-min refresh cadence does not grow a hundreds-of-MB transcript
  in a week on the refresh PC. Forwarded by
  `attest-from-env-file.ps1` as a splat. Documented in
  `apps/qsdm-nvidia-ngc/QUICKSTART.md` §8a.
- **GitHub Wiki live (2026-04-23).** `QSDM/scripts/sync-wiki.sh`
  now publishes eight pages to
  <https://github.com/blackbeardONE/QSDM/wiki> with a shared sidebar
  and footer: Home (Operator Guide), Node Roles,
  Validator/Miner Quickstart, Mining Protocol, Cell Tokenomics,
  NVIDIA Lock Scope, NGC Sidecar Quickstart. Pages auto-update from
  the canonical markdown under `QSDM/docs/docs/` whenever the script
  is re-run.
- **VPS-side CPU-fallback NGC attestation sidecar (2026-04-23).**
  New installer `QSDM/deploy/install_ngc_sidecar_vps.py` deploys
  `validator_phase1.py` to `/opt/qsdm/ngc-sidecar/`, reuses the
  existing `QSDM_NGC_INGEST_SECRET` from the qsdm service
  environment, and installs a systemd oneshot + 10-minute timer
  (`qsdm-ngc-attest.service` / `.timer`). The script runs without a
  GPU — `gpu_fingerprint` falls back to `available: false` and the
  fp16 matmul path degrades to `stub_no_cuda` cleanly. Result: the
  `/api/v1/trust/attestations/summary` badge stays `healthy` even
  when the operator's dev PC is offline. Re-running the installer
  picks up a rotated secret automatically.

### Security

- **Webviewer refuses to start on insecure default credentials
  (2026-04-22).** `internal/webviewer` used to silently fall back to
  `admin` / `password` when `WEBVIEWER_USERNAME` / `WEBVIEWER_PASSWORD`
  were unset — which was acceptable when the code was private but is
  now a real foot-gun with QSDM public on GitHub: anyone who clones,
  builds, and `./qsdm`-es without reading the docs gets a wide-open
  log stream on port `9000` / `LOG_VIEWER_PORT`. `StartWebLogViewer`
  now returns the new `webviewer.ErrInsecureDefaultCreds` when either
  var is unset or empty, and `cmd/qsdm/main.go` logs a clear
  remediation message and keeps the node running *without* the log
  viewer. Operators who explicitly want the old behaviour for local
  development must now opt in with `QSDM_WEBVIEWER_ALLOW_DEFAULT_CREDS=1`,
  which also emits a loud `[WEBVIEWER][WARN]` banner on every
  start-up. Basic-auth compares use `crypto/subtle.ConstantTimeCompare`
  now to remove the trivial timing side-channel. Covered by seven new
  unit tests in `internal/webviewer/webviewer_creds_test.go` (with
  eleven subtest permutations covering both-set, either-unset,
  truthy/falsey opt-in values, and the opt-in-doesn't-override-real-
  creds invariant). The existing integration test in
  `tests/webviewer_test.go` was updated to set the opt-in flag
  explicitly so its use of `admin`/`password` is intentional rather
  than accidental. The live VPS is unaffected because both env vars
  have been provisioned in `/etc/systemd/system/qsdm.service.d/secrets.conf`
  since the 2026-04-22 secret-rotation pass.

### CI

- **Legacy-metric guard in GitHub Actions (2026-04-22).** During the
  `qsdm_*` -> `qsdm_*` Prometheus metric-name prefix migration
  (Major Update §6), the dual-emit machinery in
  `pkg/monitoring/prometheus_prefix_migration.go` already publishes
  every metric under both prefixes, so any new code should register
  metrics under the canonical `qsdm_*` name only. Added
  `QSDM/scripts/check-no-new-legacy-metrics.sh` which `rg`-greps the
  Go tree for hand-written `"qsdm_<name>_(total|count|seconds|sum|
  bucket|bytes|info|ratio|current|last|active|inflight)"` string
  literals and fails if any appear outside a tight five-file allowlist
  of files that are part of the dual-emit machinery or test it. The
  regex is deliberately narrow so it does NOT flag non-metric branding
  aliases like `qsdm_node_id` in `pkg/branding/branding.go`
  (those are NGC proof JSON field names, not metrics). Wired into
  `.github/workflows/qsdm-go.yml` `build-test` job as the very first
  step so regressions fail fast without burning build compute. Has a
  `git grep` fallback for runners without ripgrep.

### Repository

- **`.gitattributes` forcing LF for scripts + YAML (2026-04-22).** The
  existing clone has `core.autocrlf=false`, so `.sh` / `.py` files
  authored on Windows were being committed with CRLF line endings and
  only working on CI by accident of how `bash script.sh` tolerates
  trailing `\r`. Added a minimal `.gitattributes` that pins `*.sh`,
  `*.py`, `*.yml`, `*.yaml`, and `Dockerfile` to LF at the blob level.
  Does not rewrite existing working copies -- just protects every
  future checkout and new file from the shebang-breakage class of
  bug (e.g. `/usr/bin/env: 'bash\r': No such file or directory`).

### Changed — deploy scripts

- **VPS host/user are now env-configurable (2026-04-22).** Every
  script under `QSDM/deploy/*.py` used to hardcode the reference
  validator's IP (`206.189.132.232`) and SSH user (`root`) at the top
  of the file. That made the scripts unusable by anyone forking the
  repo to run their own QSDM node, and it meant a future VPS
  migration would be a cross-file sed pass every single time. Added
  a tiny shared helper at `QSDM/deploy/_deploy_host.py` that reads
  `QSDM_VPS_HOST` / `QSDM_VPS_USER` from the environment with a
  sensible fallback to the historical reference-node values (the IP
  is not a secret — it is the public A record for `api.qsdm.tech` in
  DNS and is already documented in `QSDM/deploy/Caddyfile`). All
  seven scripts (`remote_apply`, `remote_verify`, `remote_harden_ssh`,
  `remote_install_caddy`, `remote_cmd`, `remote_fix_service`,
  `remote_bootstrap`) now import from `_deploy_host`. Running them
  against the reference node is unchanged; running them against a
  different node is now a single `$env:QSDM_VPS_HOST = '...'` away.

### Changed — repository / licensing

- **MIT license surfaced at the repo root (2026-04-22).** `QSDM/LICENSE`
  was one level too deep for GitHub's license detector, so the repo
  page was displaying neither a licence badge nor the "MIT" tag in the
  sidebar even though the project has always been MIT-licensed. Copied
  the licence to `/LICENSE` at the repo root (keeping `QSDM/LICENSE`
  in place so nothing else breaks), bumped the copyright line to
  `2024-2026` in both copies, and added a `## License` section to the
  root `README.md` that links to it. Also trimmed the root `README`
  down to files that actually exist in the public tree — the previous
  version pointed at several internal docs (`NEXT_STEPS.md`,
  `Major Update.md`, `nvidia_locked_qsdm_blockchain_architecture.md`,
  `apps/game-integration/`) that are correctly excluded from the
  public repo by `.gitignore`, so those links were 404s on GitHub.

### Deployed

- **VPS root password rotated (2026-04-22).** The previous root
  password has been retired now that `ed25519` key-auth is the proven
  primary SSH path (every deploy in the Major Update window used it).
  Rotation was performed over the existing key channel using
  `chpasswd` on stdin — the new secret never appears in argv, bash
  history, or the process table on the remote host. Key-auth was
  verified end-to-end by opening a second independent connection
  after the rotation, so the session could not have locked itself
  out. The new password lives only in `vps.txt` (which is
  `.gitignore`d) and is intended purely as a break-glass fallback via
  the DigitalOcean web console; no automation reads it.
- **Workspace placed under version control (2026-04-22).** Twelve
  weeks of Major Update work was living on a single Windows disk with
  no git history — a real disaster-recovery hole given the project
  is already public. `git init` + initial import commit on `main`
  (`bab2f8f`, 930 files, 129,786 insertions). The `.gitignore` was
  designed defensively around what actually exists on disk: it
  excludes `vps.txt`, the legacy `Nvidia Token` file, all `*.env`
  (except committed `*.env.example` templates), TLS material
  (`QSDM/api_server.crt`/`.key`), Go build artifacts (`*.exe`,
  `*.test`, explicit `qsdm`/`qsdmminer`/`trustcheck` paths),
  `target/` trees (Rust), runtime state (`*.log`, `QSDM/databases/*.db`,
  timestamped SQL dumps, `QSDM/storage/tx_*.dat`), and the usual
  Python/Node/IDE/OS caches. Vendored third-party binaries (notably
  `QSDM/source/wasmer-go-patched/.../libwasmer.*`) are deliberately
  kept because the build depends on them. Pre-commit audit confirmed
  no explicit secret path (vps.txt, api_server.key/crt, Nvidia Token,
  `_tmp_*.py`) slipped into the staged tree; largest staged file is
  ~15 MB (`libwasmer.so` for `linux-amd64`), well under GitHub's
  100 MB per-file soft cap. No remote is configured — that's a
  separate decision.
- **NGC proof-ingest secret provisioned on the VPS (2026-04-22).**
  Generated a fresh 256-bit random secret (`secrets.token_hex(32)`, 64
  hex chars — comfortably above the 16-char strict-secrets floor and
  obviously not a `charming123*` dev placeholder) and installed it as a
  systemd drop-in at
  `/etc/systemd/system/qsdm.service.d/ngc-secret.conf` (mode `0600`,
  owner `root:root`). Both the canonical `QSDM_NGC_INGEST_SECRET`
  and the legacy `QSDM_NGC_INGEST_SECRET` env keys are exported so the
  deprecation window for older sidecars is honoured. The drop-in path
  is chosen deliberately: subsequent runs of
  `deploy/remote_apply_paramiko.py` rewrite the unit file at
  `/etc/systemd/system/qsdm.service` but leave the `.service.d/`
  directory untouched, so the secret survives redeploys without ever
  entering the repo or the tarball. Post-install probes:
  `GET /api/v1/monitoring/ngc-proofs` with the correct
  `X-QSDM-NGC-Secret` returns `HTTP 200 {"count":0,"proofs":[]}`,
  a wrong secret returns `HTTP 401`, and no NGC-related errors appear
  in the journal. The ingest surface is now gated-live, but the
  `attested/total_public` ratio stays at `0/1` until a sidecar with
  matching secret and a real NVIDIA NGC attestation submits its first
  proof — that's a separate bring-up gated on having a GPU host, not
  on anything in this repo.
- **Trust surface denominator fix — `total_public` 2 → 1 (2026-04-22).**
  The `"bootstrap"` placeholder address, which `cmd/qsdm/main.go`
  registers against `nodeValidatorSet` purely to satisfy BFT quorum on
  a single-node network, was being counted by the transparency widget
  as if it were a public peer. The new `sentinelValidatorAddresses`
  allowlist in `pkg/api/trust_peer_provider.go` filters it out, so the
  live `/api/v1/trust/attestations/summary` on the VPS now reports
  `{"attested":0,"total_public":1}` — one real validator, zero fresh
  attestations, which is the honest anti-claim answer.
  (`pkg/api/trust_peer_provider.go`,
  `pkg/api/trust_peer_provider_test.go`).
- **Dashboard login page — registration/hostname copy removed
  (2026-04-22).** The two-paragraph `<div class="info">` block that
  explained `POST /api/v1/auth/register` and `localhost` vs `127.0.0.1`
  session-cookie guidance was stripped from
  `internal/dashboard/dashboard.go`. The `<noscript>` fallback notice
  is retained because it serves an accessibility purpose rather than
  a documentation one. Confirmed live on
  `https://dashboard.qsdm.tech/`.
- **Deploy-log noise fix — Caddy reload false alarm silenced
  (2026-04-22).** `systemctl reload caddy` returned non-zero on this
  host because the Caddyfile sets `admin off`, even though the config
  reload itself succeeded. `remote_apply_paramiko.py` now redirects
  reload/restart stderr+stdout and surfaces only
  `caddy: <is-active>` so a healthy deploy log stops containing
  "Job for caddy.service failed" red herrings.
- **Production VPS redeploy — Major Update payload live (2026-04-22).**
  `206.189.132.232` (Bangalore `blr1`) was upgraded from the
  pre-rebrand build to the current `[Unreleased]` tree. Public probes
  against `https://qsdm.tech/`, `https://qsdm.tech/trust.html`,
  `https://qsdm.tech/api/v1/trust/attestations/summary`, and
  `https://api.qsdm.tech/api/v1/health/live` all return HTTP 200. The
  trust aggregator is wired and serving the honest anti-claim payload
  `{"attested":0,"total_public":2,"ratio":0,…,"scope_note":"NVIDIA-lock
  is an opt-in, per-operator API policy — not a consensus rule …"}`.
  Node identity: `12D3KooWB639f3GXxAuyqgZnk8so9ZVVstNxxvU6W1ncjXoAbVKS`;
  `[trust]` block defaults applied on first restart
  (`fresh_within=15m`, `refresh_interval=10s`). Landing page
  (`index.html` + `trust.html`) synced to `/var/www/qsdm/` and served
  by the existing Caddy edge.

### Changed — deployment tooling

- **`QSDM/deploy/remote_apply_paramiko.py` rewritten** as a full-tree
  apply (tarball of 908 Major Update files, ~42 MiB gzipped) instead
  of the previous 4-file hotfix. Now: auto-auths with
  `~/.ssh/id_ed25519` before falling back to `QSDM_VPS_PASS`; restores
  the Unix exec bit on all `*.sh`/`*.py` after tar extraction
  (Windows-safe); keeps the existing CGO+liboqs production profile by
  default and exposes `QSDM_BUILD_TAGS=validator_only` as an opt-in
  cutover lever; non-destructively appends a `[trust]` block to
  `/opt/qsdm/qsdm.toml` on servers that predate the aggregator
  wiring; rolls back to `/opt/qsdm/qsdm.prev` if the new binary
  fails systemd's `is-active` gate; probes health, trust, dashboard,
  and the Caddy edge (`api.qsdm.tech`, `qsdm.tech` via
  `curl --resolve`) at the end so the operator sees green probes
  inline with the deploy log. `remote_verify_paramiko.py` gained the
  matching trust-endpoint probe block.
- **`QSDM/config/qsdm.toml.example` + `qsdm.yaml.example`**
  now ship `[node]` (two-tier role gate) and `[trust]` (attestation
  transparency) sections with inline commentary, so fresh installs get
  the correct scaffolding without needing to cross-reference the
  Major Update spec.

### Added

- **Trust aggregator wired into node startup.** `cmd/qsdm/main.go`
  now constructs a live `TrustAggregator` fed by a
  `ValidatorSetPeerProvider` (wrapping `ActiveValidators()`) and a
  `MonitoringLocalSource` (NGC ring buffer), and runs a background
  refresh goroutine at `cfg.TrustRefreshInterval` (default 10 s). The
  `/api/v1/trust/attestations/*` endpoints now return real data on a
  live node instead of perpetually answering 503 "warming up". New
  `[trust]` config section (TOML/YAML) and env knobs
  `QSDM_TRUST_DISABLED`, `QSDM_TRUST_FRESH_WITHIN`,
  `QSDM_TRUST_REFRESH_INTERVAL`, `QSDM_TRUST_REGION` (legacy
  `QSDM_*` still accepted). Setting `disabled=true` makes the
  endpoints return HTTP 404 per §8.5.3. (`pkg/api/trust_peer_provider.go`,
  `pkg/config/config.go`, `pkg/config/config_toml.go`,
  `cmd/qsdm/main.go`, audit item `rebrand-07`.)
- **Major Update Phase 5 — trust transparency surface.** New public
  endpoints `GET /api/v1/trust/attestations/summary` and
  `GET /api/v1/trust/attestations/recent` expose aggregate NGC
  attestation counts as an opt-in, per-operator *transparency signal*
  (not a consensus rule). Widgets render "X of Y" — never just "X" —
  per the anti-claim guardrail in Major Update §8.5.2.
  New: `/trust.html` transparency page on `qsdm.tech`, matching card on
  the operator dashboard. (`pkg/api/handlers_trust.go`,
  `deploy/landing/trust.html`, `internal/dashboard/static/dashboard.js`.)
- **Major Update Phase 1–4 deliverables landed in-repo.** Rebrand
  (`QSDM → QSDM`, native coin `Cell (CELL)`, `dust` smallest unit),
  two-tier node roles (validator / miner) with role-guard startup
  enforcement, emission schedule calculator, reference CPU miner
  (`cmd/qsdmminer`), split Dockerfiles (`Dockerfile.validator`,
  `Dockerfile.miner`), mining protocol spec (Candidate C: mesh3D-tied
  useful PoW). Full phase-by-phase table in
  [`NEXT_STEPS.md`](NEXT_STEPS.md).
- **`cmd/trustcheck`** — single-binary, stdlib-only external scraper
  that validates `/api/v1/trust/attestations/*` responses against the
  §8.5.2–§8.5.4 contracts. Third parties can run it on any cadence
  without pulling in the QSDM module.
- **`cmd/genesis-ceremony`** — pure-Go dry-run of the mainnet genesis
  ceremony. N-of-N commit-reveal with ed25519 signing (spec-level
  stand-in for ML-DSA-87), `run`/`verify`/`schema` modes. Every
  artefact flagged `dry_run: true`; the verifier refuses to bless a
  non-dry-run bundle.
- **`docs/docs/AUDIT_PACKET_MINING.md`** — external-auditor entry point
  for `mining-01`: threat model, 10 numbered consensus-safety
  invariants, invariant → source-location → test coverage matrix,
  reproducible-build recipe.
- **`.github/workflows/qsdm-split-profile.yml`** — CI workflow proving
  both the `validator_only` and full-profile builds compile and pass
  short tests on every push, plus clean Docker builds of
  `Dockerfile.validator` and `Dockerfile.miner` (no push).
- **`pkg/audit/checklist.go`** — 18 new audit items across the
  `rebrand`, `tokenomics`, `mining_audit`, and `trust_api` categories
  so each in-repo commitment has a single auditable identifier.

### Changed

- **OpenAPI spec refresh.** `QSDM/docs/docs/openapi.yaml` title,
  contact, and env-var references now lead with the `QSDM` name, with
  the legacy `QSDM_*` names documented as aliases during the
  deprecation window.
- **`NVIDIA_LOCK_CONSENSUS_SCOPE.md` refresh.** Aligned to the Major
  Update §5.4 Stance 1 ("NVIDIA-favored, not NVIDIA-exclusive") and
  cross-references the new trust endpoints. The one-sentence
  invariant — "NVIDIA-lock is an opt-in, per-operator API policy —
  not a consensus rule" — is preserved byte-for-byte because
  `pkg/api/handlers_trust.go` emits it verbatim.
- **`start-qsdm-local.ps1`.** Artefact now named `qsdm_local.exe`,
  with an automatic move of the legacy `qsdm_local.exe` path so
  operators' PID files and launch scripts keep working.

### Deprecated (migration window, one minor release)

- `QSDM_*` environment variables. Continue to work; log a single
  deprecation warning per process. Prefer `QSDM_*`. See
  `REBRAND_NOTES.md` §3.1.
- `X-QSDM-*` HTTP headers. Continue to be accepted. Prefer
  `X-QSDM-*`. See `REBRAND_NOTES.md` §3.2.
- `qsdm_*` Prometheus metric prefix. **Not yet renamed** — a
  cutover plan for dual-emit is staged under `REBRAND_NOTES.md` §3.7
  because the rename is breaking for Grafana/alert pipelines.
- `sdk/javascript/qsdm.*`, `sdk/go/qsdm*.go`. Shim packages
  that re-export from the new `qsdm` module path.

### Not changed

- Go module path stays `github.com/blackbeardONE/QSDM`.
- Address format, signature scheme (ML-DSA-87 via liboqs), GossipSub
  topics, and the `/api/v1/*` REST surface are unchanged.
- Existing databases, config files, and systemd units keep working
  without any renaming.
- PoE+BFT consensus is CPU-only and admits validators without a GPU.

### Audit-blocked / wall-clock items (from `NEXT_STEPS.md`)

These are **not** code changes — they are listed here so a release
reader can find them with one search.

- `rebrand-03` — trademark filings.
- `tok-01` — tokenomics genesis-policy sign-off.
- `mining-01` — external audit of the mining protocol (auditor packet
  ready at `docs/docs/AUDIT_PACKET_MINING.md`).
- `mining-05` — incentivized testnet launch.
- Mainnet genesis ceremony (dry-run driver ready at
  `cmd/genesis-ceremony`).
- NGC attestation service availability.
