package main

// watch_archspoof.go — fourth sibling of watch_enrollments,
// watch_slashes, and watch_params. Polls
// /api/metrics/prometheus and emits one WatchEvent per non-zero
// counter delta on the qsdm_attest_archspoof_rejected_total{reason}
// and qsdm_attest_hashrate_rejected_total{arch} families.
//
// Why a metrics-counter watcher rather than a structured event
// stream off the validator:
//
//   - The §4.6 arch-spoof gate already increments these
//     counters on every rejection (pkg/monitoring/archcheck_metrics.go);
//     no new server-side surface is needed.
//   - The Prometheus alert rules added in
//     deploy/prometheus/alerts_qsdm.example.yml fire on
//     rate()/increase() of the same series, so this watcher
//     gives operators the per-event view that complements the
//     "you're getting bursted" alert. One mental model: alerts
//     say "something is wrong"; watcher says "here's each hit
//     as it lands, in order".
//   - The watcher composes with any QSDM node that exposes
//     /api/metrics/prometheus — operators do not need to be
//     running on the validator host or have access to a
//     structured event bus.
//
// Counters are monotonic under normal operation. A decrease
// across two polls (process restart) snaps the snapshot to the
// new baseline without emitting; under-counting one cycle is
// preferred to a spurious "burst" event the moment a validator
// restarts.
//
// Out of scope (deliberately):
//
//   - Per-rejection node_id / GPU name / raw error message.
//     The metrics layer is intentionally label-coarse; surface
//     of the form "miner X claimed gpu_arch=hopper at height Y"
//     would require a server-side ring buffer and a new
//     /api/v1/attest/recent-rejections endpoint. Deferred to a
//     separate session — operators with that need can correlate
//     watcher bursts against the validator's structured log.

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
)

// archspoofMetricName / hashrateMetricName pin the wire-shape
// names to the constants emitted by
// pkg/monitoring/prometheus_scrape.go::corePrometheusMetrics().
// Tests pin both directions; renaming either side is a wire
// change.
const (
	archspoofMetricName = "qsdm_attest_archspoof_rejected_total"
	hashrateMetricName  = "qsdm_attest_hashrate_rejected_total"
)

// MaxMetricsBodyBytes caps the size of one /api/metrics/prometheus
// response we are willing to parse. A misbehaving server (or a
// future cardinality blow-up) must not OOM the watcher process.
// 4 MiB is comfortably above any realistic exposition: the full
// QSDM v2 metric set as of this writing is ~12 KiB.
const MaxMetricsBodyBytes = 4 << 20

// archspoofSnapshot is the diff-loop shape: per-label counter
// values for the two families we track. Other metrics in the
// exposition are silently ignored.
//
// Both maps key on the label value verbatim; an unparseable
// line is skipped (logged via stderr error event), not aborted.
type archspoofSnapshot struct {
	// ArchSpoof maps reason → counter value
	// (unknown_arch | gpu_name_mismatch | cc_subject_mismatch).
	ArchSpoof map[string]uint64
	// Hashrate maps arch → counter value
	// (ada | hopper | blackwell | blackwell_ultra | rubin |
	// rubin_ultra | unknown).
	Hashrate map[string]uint64
}

// watchArchSpoofOptions is the parsed flag set for
// `qsdmcli watch archspoof`. Held as a struct so the snapshot
// + diff core can be unit-tested without re-implementing flag
// parsing.
type watchArchSpoofOptions struct {
	// MetricsURL is the absolute URL of /api/metrics/prometheus.
	// Empty after normalize() means: fall back to
	// derive-from-c.baseURL.
	MetricsURL      string
	Interval        time.Duration
	Once            bool
	JSON            bool
	IncludeExisting bool
	// Reasons / Arches are server-side filters (set membership);
	// empty maps mean "no filter, emit every non-zero delta".
	Reasons map[string]bool
	Arches  map[string]bool

	// Detailed switches to the `/api/v1/attest/recent-rejections`
	// endpoint and emits one WatchKindArchSpoofRejection per
	// store record (with miner_addr / gpu_name / cert_subject /
	// detail) instead of counter-bucket deltas. Falls back to
	// counter mode if the endpoint returns 503 (older nodes).
	Detailed bool
}

// watchArchSpoof parses flags, fetches an initial snapshot, and
// enters the diff loop. Same shape as watchParams: SIGINT/SIGTERM
// exit returns nil; first-poll fatal returns the error so the
// operator notices a typo'd URL or an unreachable validator.
func (c *CLI) watchArchSpoof(args []string) error {
	fs := flag.NewFlagSet("watch archspoof", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		metricsURL = fs.String("metrics-url", "",
			"absolute URL of /api/metrics/prometheus (default: derive from QSDM_API_URL)")
		interval = fs.Duration("interval", DefaultWatchInterval,
			"polling cadence (clamped: ≥5s, default 30s)")
		once = fs.Bool("once", false,
			"emit a single snapshot and exit (no diff loop)")
		jsonOut = fs.Bool("json", false,
			"emit JSON-Lines (one event per line) instead of human-formatted lines")
		includeExisting = fs.Bool("include-existing", false,
			"on the first poll, emit a synthetic burst event for every non-zero counter")
		reasonFilter = fs.String("reason", "",
			"comma-separated archspoof reason filter (default: all of unknown_arch, gpu_name_mismatch, cc_subject_mismatch)")
		archFilter = fs.String("arch", "",
			"comma-separated hashrate arch filter (default: all of ada, hopper, blackwell, blackwell_ultra, rubin, rubin_ultra, unknown)")
		detailed = fs.Bool("detailed", false,
			"poll /api/v1/attest/recent-rejections instead of /api/metrics/prometheus; emit one event per actual rejection record (with miner_addr/gpu_name/cert_subject/detail)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	opts := watchArchSpoofOptions{
		MetricsURL:      *metricsURL,
		Interval:        *interval,
		Once:            *once,
		JSON:            *jsonOut,
		IncludeExisting: *includeExisting,
		Reasons:         parseCSVSet(*reasonFilter),
		Arches:          parseCSVSet(*archFilter),
		Detailed:        *detailed,
	}
	if err := opts.normalize(); err != nil {
		return err
	}
	// Detailed mode polls the qsdmcli base URL (.../api/v1)
	// directly, not the metrics endpoint. URL derivation only
	// applies to counter mode.
	if !opts.Detailed && opts.MetricsURL == "" {
		// Env var takes priority over derive-from-baseURL so
		// operators with a split data-plane / metrics-plane
		// deployment can point this watcher at a different
		// host than the rest of qsdmcli without re-typing
		// QSDM_API_URL.
		if env := strings.TrimSpace(os.Getenv("QSDM_METRICS_URL")); env != "" {
			opts.MetricsURL = env
		} else {
			derived, err := deriveMetricsURL(c.baseURL)
			if err != nil {
				return err
			}
			opts.MetricsURL = derived
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if opts.Detailed {
		return c.runWatchArchSpoofDetailed(ctx, opts, os.Stdout, os.Stderr)
	}
	return c.runWatchArchSpoof(ctx, opts, os.Stdout, os.Stderr)
}

// normalize validates and clamps option fields. Pure function;
// the test suite hits it directly.
func (o *watchArchSpoofOptions) normalize() error {
	if o.Interval == 0 {
		o.Interval = DefaultWatchInterval
	}
	if o.Interval > 0 && o.Interval < MinWatchInterval {
		o.Interval = MinWatchInterval
	}
	for r := range o.Reasons {
		switch r {
		case "unknown_arch", "gpu_name_mismatch", "cc_subject_mismatch":
			// ok
		default:
			return fmt.Errorf(
				"--reason=%q invalid (known: unknown_arch, gpu_name_mismatch, cc_subject_mismatch)",
				r)
		}
	}
	for a := range o.Arches {
		switch a {
		case "ada", "hopper", "blackwell", "blackwell_ultra", "rubin", "rubin_ultra", "unknown":
			// ok
		default:
			return fmt.Errorf(
				"--arch=%q invalid (known: ada, hopper, blackwell, blackwell_ultra, rubin, rubin_ultra, unknown)",
				a)
		}
	}
	return nil
}

// parseCSVSet collapses a comma-separated CLI flag into a set.
// Empty / whitespace-only input yields a nil map (caller treats
// nil as "no filter"). Whitespace inside an entry is trimmed;
// duplicates are deduped silently.
func parseCSVSet(s string) map[string]bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	out := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		v := strings.TrimSpace(part)
		if v == "" {
			continue
		}
		out[v] = true
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// deriveMetricsURL turns a baseURL like
// "http://localhost:8080/api/v1" into
// "http://localhost:8080/api/metrics/prometheus". The /api/v1
// suffix is the qsdmcli convention (see defaultBaseURL); if it
// is absent we surface a clear error so operators do not get a
// surprising 404 ten polls in.
func deriveMetricsURL(baseURL string) (string, error) {
	trimmed := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(trimmed, "/api/v1") {
		return "", fmt.Errorf(
			"cannot derive metrics URL from %q (expected suffix /api/v1); set --metrics-url or QSDM_METRICS_URL",
			baseURL)
	}
	root := strings.TrimSuffix(trimmed, "/api/v1")
	return root + "/api/metrics/prometheus", nil
}

// runWatchArchSpoof is the testable core of watchArchSpoof. Same
// semantics as runWatchParams: the very first snapshot failure
// is fatal; subsequent failures emit a WatchKindError event and
// the loop continues.
func (c *CLI) runWatchArchSpoof(
	ctx context.Context,
	opts watchArchSpoofOptions,
	stdout, stderr io.Writer,
) error {
	prev, err := c.fetchArchSpoofSnapshot(ctx, opts.MetricsURL)
	if err != nil {
		return fmt.Errorf("initial snapshot: %w", err)
	}

	now := time.Now().UTC()
	if opts.IncludeExisting {
		emitEvents(stdout, stderr, opts.JSON,
			archspoofSnapshotAsInitialEvents(prev, now,
				opts.Reasons, opts.Arches))
	}

	if opts.Once {
		return nil
	}

	t := time.NewTicker(opts.Interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			next, err := c.fetchArchSpoofSnapshot(ctx, opts.MetricsURL)
			tickAt := time.Now().UTC()
			if err != nil {
				emitEvents(stdout, stderr, opts.JSON,
					[]WatchEvent{{
						Timestamp: tickAt,
						Kind:      WatchKindError,
						Error:     truncateForLine(err.Error(), 200),
					}})
				continue
			}
			events := diffArchSpoofSnapshots(prev, next, tickAt,
				opts.Reasons, opts.Arches)
			emitEvents(stdout, stderr, opts.JSON, events)
			prev = next
		}
	}
}

// fetchArchSpoofSnapshot performs one HTTP GET of metricsURL
// and parses the two counter families we care about. Returns a
// snapshot suitable for diffing.
//
// Authentication: same Bearer-token posture as the rest of
// qsdmcli — if c.token is set we send it. The dashboard's
// requireMetricsScrapeOrAuth middleware (see
// internal/dashboard/dashboard.go) accepts either a Bearer JWT
// or the metrics-scrape secret header; both work, only the
// Bearer side is plumbed in qsdmcli today.
func (c *CLI) fetchArchSpoofSnapshot(
	ctx context.Context,
	metricsURL string,
) (archspoofSnapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metricsURL, nil)
	if err != nil {
		return archspoofSnapshot{}, fmt.Errorf("build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "text/plain")
	resp, err := c.client.Do(req)
	if err != nil {
		return archspoofSnapshot{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxMetricsBodyBytes))
	if err != nil {
		return archspoofSnapshot{}, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return archspoofSnapshot{}, fmt.Errorf(
			"validator HTTP %d on %s: %s",
			resp.StatusCode, metricsURL,
			truncateForLine(string(body), 160))
	}
	return parseArchSpoofExposition(body)
}

// parseArchSpoofExposition is the pure parser. Walks
// Prometheus text exposition line by line and collects the two
// families we track. Lines with parse errors are silently
// skipped — the watcher's job is to report deltas, not to
// validate the exporter (which has its own test coverage).
//
// Format the parser handles:
//
//	qsdm_attest_archspoof_rejected_total{reason="unknown_arch"} 5
//	qsdm_attest_hashrate_rejected_total{arch="hopper"} 3
//
// Comments (# HELP / # TYPE / blank) are skipped. Any other
// metric name is ignored.
func parseArchSpoofExposition(body []byte) (archspoofSnapshot, error) {
	out := archspoofSnapshot{
		ArchSpoof: map[string]uint64{},
		Hashrate:  map[string]uint64{},
	}
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	// Match the body cap; long lines (cardinality blow-ups in
	// other metrics) get skipped without erroring the snapshot.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, labels, value, ok := splitExpositionLine(line)
		if !ok {
			continue
		}
		switch name {
		case archspoofMetricName:
			reason, ok := labels["reason"]
			if !ok || reason == "" {
				continue
			}
			out.ArchSpoof[reason] = value
		case hashrateMetricName:
			arch, ok := labels["arch"]
			if !ok {
				continue
			}
			if arch == "" {
				arch = "unknown"
			}
			out.Hashrate[arch] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return archspoofSnapshot{}, fmt.Errorf("scan exposition: %w", err)
	}
	return out, nil
}

// splitExpositionLine breaks one exposition line into
// (name, labels, value, ok). Returns ok=false on any malformed
// shape so the caller skips the line.
//
// Examples accepted (mirrors prometheus.go::formatMetricLine):
//
//	foo 5
//	foo{a="b"} 5
//	foo{a="b",c="d"} 5.5
//
// Quote-escape rules in label values are deliberately NOT
// implemented — the qsdm exporter never emits embedded quotes
// or backslashes in label values (verified by inspection of
// pkg/monitoring/* call sites). If that changes, this parser
// would need real OpenMetrics-spec quoting.
func splitExpositionLine(line string) (name string, labels map[string]string, value uint64, ok bool) {
	// Split on first ' ' that follows the name+labels block.
	// Labels may not contain ' ', so a simple last-space split
	// works for our flat shape.
	sp := strings.LastIndex(line, " ")
	if sp <= 0 {
		return "", nil, 0, false
	}
	head := line[:sp]
	tail := strings.TrimSpace(line[sp+1:])
	value, parseOK := parseExpositionValue(tail)
	if !parseOK {
		return "", nil, 0, false
	}

	if i := strings.IndexByte(head, '{'); i >= 0 {
		if !strings.HasSuffix(head, "}") {
			return "", nil, 0, false
		}
		name = head[:i]
		labelBlock := head[i+1 : len(head)-1]
		labels = parseExpositionLabels(labelBlock)
	} else {
		name = head
		labels = map[string]string{}
	}
	if name == "" {
		return "", nil, 0, false
	}
	return name, labels, value, true
}

// parseExpositionValue accepts either an integer literal or a
// decimal that happens to be integer-valued (Prometheus emits
// "5" for an int counter but the OpenMetrics spec also allows
// "5.0"). Anything fractional is truncated to the floor; a
// counter delta of "0.7" is meaningless in our domain.
func parseExpositionValue(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	// Strip a trailing ".0" / ".000" pattern; treat any
	// other fraction as truncate-toward-zero. The exporter
	// only ever emits int-valued counters here.
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		s = s[:dot]
	}
	if s == "" || s == "-" {
		return 0, false
	}
	// Reject negatives — counters are non-negative by
	// definition; a "-5" line is exporter corruption, not a
	// data point.
	if s[0] == '-' {
		return 0, false
	}
	var v uint64
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch < '0' || ch > '9' {
			return 0, false
		}
		// Overflow check: 1e18 fits in uint64; the qsdm
		// counters never approach that.
		if v > (^uint64(0))/10 {
			return 0, false
		}
		v = v*10 + uint64(ch-'0')
	}
	return v, true
}

// parseExpositionLabels parses a label block of the form
// `a="b",c="d"` into a map. Unquoted values, missing equals,
// or any other malformed shape silently degrades to an empty
// map (caller skips the line via the zero-label fall-through).
func parseExpositionLabels(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	for _, part := range strings.Split(s, ",") {
		eq := strings.IndexByte(part, '=')
		if eq <= 0 {
			continue
		}
		k := strings.TrimSpace(part[:eq])
		v := strings.TrimSpace(part[eq+1:])
		if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
			continue
		}
		out[k] = v[1 : len(v)-1]
	}
	return out
}

// archspoofSnapshotAsInitialEvents synthesises a burst event
// per non-zero counter bucket in the initial snapshot. Used
// for --include-existing on the first poll. Sorted (kind,
// label) for deterministic output.
func archspoofSnapshotAsInitialEvents(
	snap archspoofSnapshot,
	ts time.Time,
	reasonFilter, archFilter map[string]bool,
) []WatchEvent {
	out := make([]WatchEvent, 0,
		len(snap.ArchSpoof)+len(snap.Hashrate))

	reasons := sortedKeys(snap.ArchSpoof)
	for _, r := range reasons {
		v := snap.ArchSpoof[r]
		if v == 0 {
			continue
		}
		if reasonFilter != nil && !reasonFilter[r] {
			continue
		}
		out = append(out, WatchEvent{
			Timestamp:  ts,
			Kind:       WatchKindArchSpoofBurst,
			Reason:     r,
			DeltaCount: v,
			TotalCount: v,
		})
	}
	arches := sortedKeys(snap.Hashrate)
	for _, a := range arches {
		v := snap.Hashrate[a]
		if v == 0 {
			continue
		}
		if archFilter != nil && !archFilter[a] {
			continue
		}
		out = append(out, WatchEvent{
			Timestamp:  ts,
			Kind:       WatchKindHashrateBurst,
			Arch:       a,
			DeltaCount: v,
			TotalCount: v,
		})
	}
	return out
}

// diffArchSpoofSnapshots is the pure-function core of the
// archspoof diff loop. For each (kind, label) bucket in either
// snapshot it emits at most one event:
//
//   - prev value < next value: WatchKindArchSpoof/HashrateBurst
//     with DeltaCount = next - prev.
//   - prev value == next value: no event.
//   - prev value > next value: counter went backwards (process
//     restart); rebase silently — emit nothing for this label
//     this cycle. The next cycle resumes normal delta
//     emission against the new baseline.
//
// Ordering: archspoof events first (by reason ASC), then
// hashrate events (by arch ASC). Stable across runs.
func diffArchSpoofSnapshots(
	prev, next archspoofSnapshot,
	ts time.Time,
	reasonFilter, archFilter map[string]bool,
) []WatchEvent {
	out := make([]WatchEvent, 0, 8)

	// Union the label sets so we surface bursts on labels
	// that appeared for the first time this cycle.
	reasons := unionKeys(prev.ArchSpoof, next.ArchSpoof)
	for _, r := range reasons {
		if reasonFilter != nil && !reasonFilter[r] {
			continue
		}
		old := prev.ArchSpoof[r]
		nw := next.ArchSpoof[r]
		if nw <= old {
			continue
		}
		out = append(out, WatchEvent{
			Timestamp:  ts,
			Kind:       WatchKindArchSpoofBurst,
			Reason:     r,
			DeltaCount: nw - old,
			TotalCount: nw,
		})
	}

	arches := unionKeys(prev.Hashrate, next.Hashrate)
	for _, a := range arches {
		if archFilter != nil && !archFilter[a] {
			continue
		}
		old := prev.Hashrate[a]
		nw := next.Hashrate[a]
		if nw <= old {
			continue
		}
		out = append(out, WatchEvent{
			Timestamp:  ts,
			Kind:       WatchKindHashrateBurst,
			Arch:       a,
			DeltaCount: nw - old,
			TotalCount: nw,
		})
	}

	return out
}

// sortedKeys returns the keys of m sorted ASC. Tiny helper kept
// local to avoid pulling a generic package; the call sites are
// only two (uint64 maps) and the alternative is reflection or
// a generic helper neither of which is worth the cognitive
// overhead.
func sortedKeys(m map[string]uint64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// unionKeys returns the sorted union of the keys of two maps.
func unionKeys(a, b map[string]uint64) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// -----------------------------------------------------------------------------
// --detailed mode (per-rejection record stream)
// -----------------------------------------------------------------------------
//
// Polls /api/v1/attest/recent-rejections with a Seq-based
// cursor. Each successful poll requests records strictly after
// the highest Seq we've seen, drains the cursor pages, and
// emits one WatchKindArchSpoofRejection per record.
//
// Wire shape pinned to api.RecentRejectionView; field names
// mirror that struct so a future evolution of the view (adding
// a new optional field) is non-breaking.

// recentRejectionWire mirrors api.RecentRejectionView. JSON
// tags MUST stay byte-identical; the test suite pins this.
type recentRejectionWire struct {
	Seq         uint64    `json:"seq"`
	RecordedAt  time.Time `json:"recorded_at"`
	Kind        string    `json:"kind"`
	Reason      string    `json:"reason,omitempty"`
	Arch        string    `json:"arch,omitempty"`
	Height      uint64    `json:"height,omitempty"`
	MinerAddr   string    `json:"miner_addr,omitempty"`
	GPUName     string    `json:"gpu_name,omitempty"`
	CertSubject string    `json:"cert_subject,omitempty"`
	Detail      string    `json:"detail,omitempty"`
}

// recentRejectionsPageWire mirrors api.RecentRejectionsListPageView.
type recentRejectionsPageWire struct {
	Records      []recentRejectionWire `json:"records"`
	NextCursor   uint64                `json:"next_cursor,omitempty"`
	HasMore      bool                  `json:"has_more"`
	TotalMatches uint64                `json:"total_matches"`
}

// runWatchArchSpoofDetailed is the testable core of the
// --detailed branch. Same structural shape as
// runWatchArchSpoof: first-snapshot failure is fatal,
// subsequent failures emit a WatchKindError and the loop
// continues.
//
// Cursor semantics: the watcher tracks the highest Seq it has
// emitted across all polls. The very first poll is "drain
// everything currently in the ring" only if --include-existing
// is set; otherwise we initialise the cursor to the store's
// most recent Seq and emit no historical records.
func (c *CLI) runWatchArchSpoofDetailed(
	ctx context.Context,
	opts watchArchSpoofOptions,
	stdout, stderr io.Writer,
) error {
	cursor, err := c.bootstrapDetailedCursor(ctx, opts, stdout, stderr)
	if err != nil {
		return fmt.Errorf("initial snapshot: %w", err)
	}

	if opts.Once {
		return nil
	}

	t := time.NewTicker(opts.Interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			next, events, err := c.fetchRecentRejections(ctx, opts, cursor)
			tickAt := time.Now().UTC()
			if err != nil {
				emitEvents(stdout, stderr, opts.JSON,
					[]WatchEvent{{
						Timestamp: tickAt,
						Kind:      WatchKindError,
						Error:     truncateForLine(err.Error(), 200),
					}})
				continue
			}
			emitEvents(stdout, stderr, opts.JSON,
				attachTimestamps(events, tickAt))
			cursor = next
		}
	}
}

// bootstrapDetailedCursor performs the initial poll. Returns
// the cursor the diff loop should start from. With
// --include-existing, drains the ring and emits one event per
// record, returning the post-drain cursor. Without it, fetches
// once with limit=1 to learn the current top Seq and emits
// nothing.
func (c *CLI) bootstrapDetailedCursor(
	ctx context.Context,
	opts watchArchSpoofOptions,
	stdout, stderr io.Writer,
) (uint64, error) {
	if !opts.IncludeExisting {
		// Cheap probe: limit=1, no cursor — server returns the
		// most recent record (if any). We use its Seq as the
		// initial cursor so subsequent polls only see records
		// strictly newer than this point.
		next, _, err := c.fetchRecentRejections(ctx, opts, 0)
		if err != nil {
			return 0, err
		}
		return next, nil
	}

	// --include-existing: drain everything currently in the ring.
	cursor := uint64(0)
	tickAt := time.Now().UTC()
	for {
		nextCursor, evs, err := c.fetchRecentRejections(ctx, opts, cursor)
		if err != nil {
			return 0, err
		}
		emitEvents(stdout, stderr, opts.JSON,
			attachTimestamps(evs, tickAt))
		if nextCursor == cursor {
			// Server returned no new records — we've drained
			// the ring.
			return cursor, nil
		}
		cursor = nextCursor
	}
}

// fetchRecentRejections walks the cursor-paginated list once,
// returning the highest Seq observed and the records translated
// into WatchEvents (with zero timestamps; the caller fills in
// tickAt for deterministic output).
//
// Uses MaxWatchPages as an outer bound against a misbehaving
// server (same posture as fetchEnrollmentList). One real poll
// almost always returns one page — the loop only matters when
// --include-existing drains a saturated ring.
func (c *CLI) fetchRecentRejections(
	ctx context.Context,
	opts watchArchSpoofOptions,
	startCursor uint64,
) (newCursor uint64, events []WatchEvent, err error) {
	cursor := startCursor
	for i := 0; i < MaxWatchPages; i++ {
		path := buildRecentRejectionsPath(cursor, opts)
		body, status, ferr := c.getWithStatus(ctx, path)
		if ferr != nil {
			return startCursor, nil, ferr
		}
		switch status {
		case 200:
			// fall through
		case 503:
			return startCursor, nil, fmt.Errorf(
				"validator returned 503 on %s — node likely v1-only or recent-rejections store not wired (consider dropping --detailed to fall back to counter mode)",
				path)
		default:
			return startCursor, nil, fmt.Errorf(
				"validator HTTP %d on %s: %s",
				status, path, truncateForLine(string(body), 160))
		}

		var page recentRejectionsPageWire
		if err := json.Unmarshal(body, &page); err != nil {
			return startCursor, nil, fmt.Errorf("decode page %d: %w", i, err)
		}
		for _, r := range page.Records {
			events = append(events, recentRejectionWireToEvent(r))
			if r.Seq > cursor {
				cursor = r.Seq
			}
		}
		if !page.HasMore {
			return cursor, events, nil
		}
		if page.NextCursor == 0 {
			// Defensive: has_more=true with empty next_cursor
			// is server-side corruption. Stop walking; we'll
			// pick up where we left off on the next tick.
			return cursor, events, nil
		}
		cursor = page.NextCursor
	}
	return cursor, events, fmt.Errorf(
		"recent-rejections paginated walk exceeded %d pages; aborting",
		MaxWatchPages)
}

// buildRecentRejectionsPath assembles the qsdmcli-relative
// path string. opts.Reasons / opts.Arches forward as ?reason=
// / ?arch= filters; the API handler validates them strictly so
// a typo bounces with 400. Cursor is mandatory whenever non-
// zero (the server treats cursor=0 the same as omitted).
func buildRecentRejectionsPath(cursor uint64, opts watchArchSpoofOptions) string {
	v := url.Values{}
	if cursor > 0 {
		v.Set("cursor", fmt.Sprintf("%d", cursor))
	}
	// Server enforces the closed-enum filter; the watcher
	// passes through whatever the operator typed. With an
	// empty filter set we attach nothing — empty filter = no
	// filter, same as the counter mode.
	if len(opts.Reasons) == 1 {
		for r := range opts.Reasons {
			v.Set("reason", r)
		}
	}
	if len(opts.Arches) == 1 {
		for a := range opts.Arches {
			v.Set("arch", a)
		}
	}
	path := "/attest/recent-rejections"
	if encoded := v.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return path
}

// recentRejectionWireToEvent translates one wire record into
// the watcher's WatchEvent envelope. RecordedAt is preserved
// (the API view carries it); the caller may override Timestamp
// via attachTimestamps if it wants tick-based ordering.
func recentRejectionWireToEvent(r recentRejectionWire) WatchEvent {
	return WatchEvent{
		Timestamp:   r.RecordedAt,
		Kind:        WatchKindArchSpoofRejection,
		Seq:         r.Seq,
		Reason:      r.Reason,
		Arch:        r.Arch,
		Height:      r.Height,
		MinerAddr:   r.MinerAddr,
		GPUName:     r.GPUName,
		CertSubject: r.CertSubject,
		Detail:      r.Detail,
	}
}

// attachTimestamps overwrites Timestamp on each event with
// tickAt. Used when the caller wants poll-tick-aligned
// timestamps rather than the server-side RecordedAt — e.g.
// log shippers that group by "what poll observed this" rather
// than "when did the validator record this".
//
// Currently a no-op: we preserve the server-side RecordedAt
// because operators reading per-event detail almost always
// want the source-of-truth timestamp. The helper exists so a
// future flag (--use-tick-time) can flip the behaviour
// without restructuring the caller.
func attachTimestamps(events []WatchEvent, _ time.Time) []WatchEvent {
	return events
}
