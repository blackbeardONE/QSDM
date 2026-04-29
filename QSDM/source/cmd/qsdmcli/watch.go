package main

// watch.go — operator-facing surveillance subcommand.
//
// `qsdmcli watch enrollments [flags]` polls the validator's
// /api/v1/mining/enrollments (multi-node) or
// /api/v1/mining/enrollment/{node_id} (single-node) endpoints
// and streams phase-change / stake-delta events to stdout.
//
// Design philosophy:
//
//   - This is the operator's equivalent of the
//     EnrollmentPoller already shipping inside qsdmminer-console
//     (cmd/qsdmminer-console/enrollment_poller.go). The miner
//     uses it to repaint its own dashboard; operators running
//     fleets / dashboards / monitoring pipelines need the same
//     signal as a standalone process they can compose with
//     systemd / cron / log shippers.
//   - Polling-only: this command never submits a transaction
//     and never holds a key. Safe to run on a low-trust admin
//     host that talks to a public RPC node.
//   - Diff-based: the snapshot is held in memory between
//     ticks. First poll emits either nothing (default) or a
//     synthetic `new` event for every existing record
//     (--include-existing); subsequent polls emit one event
//     per detected change.
//   - Deterministic ordering: events from a single tick are
//     emitted sorted by node_id ASC so that snapshot/replay
//     diffs against the output are reproducible.
//   - Two output modes: human (column-aligned, default) and
//     --json (one JSON object per line, for log shippers).
//
// Out of scope (deliberately):
//
//   - Slash watcher. A future `qsdmcli watch slashes` could
//     poll /mining/slash/{tx_id} for known IDs, but that needs
//     a different shape (operators provide the IDs to watch)
//     and is queued behind the watcher-bot reference impl.
//   - Long-term persistence. The diff state lives in process
//     memory; restart the watcher and the next poll re-emits
//     the full snapshot under --include-existing semantics.
//     Operators who need durable state should pipe --json into
//     a real time-series store.

import (
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

// DefaultWatchInterval is the cadence at which `qsdmcli watch`
// re-fetches its target snapshot. Matches DefaultEnrollmentPollInterval
// in the miner-console poller so operators have one mental model
// for "how fresh is my enrollment view" across both tools.
const DefaultWatchInterval = 30 * time.Second

// MinWatchInterval mirrors MinEnrollmentPollInterval. Operators
// debugging a slash race iterate fast; mass deployments must not
// be able to DDoS validators by pointing 1000 watchers at one RPC.
const MinWatchInterval = 5 * time.Second

// MaxWatchPages defends against a misbehaving server that loops
// has_more=true forever. Identical posture to enrollments --all.
const MaxWatchPages = 10000

// WatchEventKind enumerates the diff outputs of one watch cycle.
//
// String values are stable wire format: --json consumers parse
// them, log-aggregator filters key on them, and the test suite
// pins them. Adding a new kind is a non-breaking change; renaming
// or removing one is.
type WatchEventKind string

const (
	// WatchKindNew — node_id observed for the first time
	// (either after --include-existing on the very first
	// poll, or any subsequent poll that brings a new
	// node_id into the matched set).
	WatchKindNew WatchEventKind = "new"

	// WatchKindTransition — same node_id, phase changed
	// between two consecutive successful polls. The
	// canonical "your enrollment lifecycle moved" event.
	WatchKindTransition WatchEventKind = "transition"

	// WatchKindStakeDelta — same node_id, same phase,
	// non-zero change in stake_dust. Surfaces partial
	// slashes that did NOT cross the auto-revoke
	// threshold.
	WatchKindStakeDelta WatchEventKind = "stake_delta"

	// WatchKindDropped — node_id was in the previous
	// poll's matched set but is gone now. Could mean: the
	// record was archived; the operator narrowed --phase
	// filter externally; pagination skew. Operators
	// should treat this as "investigate" not "alarm".
	WatchKindDropped WatchEventKind = "dropped"

	// WatchKindError — a poll cycle failed (network /
	// HTTP / decode). Always emitted on stderr in human
	// mode; emitted on stdout (alongside data events) in
	// --json mode so log shippers see the error stream.
	WatchKindError WatchEventKind = "error"

	// --- slash-receipt watcher kinds (see watch_slashes.go) ---
	//
	// The slash watcher uses the same WatchEvent envelope so
	// JSON-Lines consumers can decode either stream with one
	// struct and switch on `event`. These kinds NEVER appear
	// in `qsdmcli watch enrollments` output and vice versa.

	// WatchKindSlashPending — a tx_id is currently 404 at
	// the validator. Emitted on the very first poll for any
	// tx_id the operator asked us to watch (so they can see
	// "yes, the watcher is tracking it") and on every
	// subsequent poll where the receipt has not yet landed.
	// Suppressed unless --include-pending is set, otherwise
	// the watcher would emit one event per tx per cycle until
	// the slash applies — way too noisy.
	WatchKindSlashPending WatchEventKind = "slash_pending"

	// WatchKindSlashResolved — the canonical "the slash
	// landed" event. Emitted exactly once per tx_id, at the
	// poll where the validator transitions from 404 to a
	// terminal SlashReceiptView. Carries every wire-shape
	// field of the receipt so the operator does not have to
	// re-fetch.
	WatchKindSlashResolved WatchEventKind = "slash_resolved"

	// WatchKindSlashEvicted — a tx_id was observed as
	// resolved in a prior poll but the validator now
	// returns 404. Almost always means FIFO eviction from
	// the bounded SlashReceiptStore; under chain reorg it
	// could also mean the receipt was rolled back. Either
	// way, surface it loudly so the operator stops
	// expecting the receipt to be queryable.
	WatchKindSlashEvicted WatchEventKind = "slash_evicted"

	// WatchKindSlashOutcomeChange — a tx_id's `outcome`
	// string changed across two consecutive successful
	// polls. Should never happen on a healthy single-chain
	// network (receipts are immutable once recorded), but
	// could surface a chain reorg or a buggy receipt store
	// rebuilding from a stale checkpoint. Defensive event,
	// always emitted.
	WatchKindSlashOutcomeChange WatchEventKind = "slash_outcome_change"

	// --- governance-params watcher kinds (see watch_params.go) ---
	//
	// The params watcher polls /api/v1/governance/params and
	// emits one event per state transition. It uses the same
	// WatchEvent envelope as the other watchers so JSON-Lines
	// consumers can decode any stream with a single struct.

	// WatchKindParamStaged — a new pending change appeared in
	// the snapshot for a parameter that previously had no
	// pending entry (or had a different pending entry; the
	// supersede case is reported as ParamSuperseded). The
	// canonical "an authority just submitted a proposal" signal.
	WatchKindParamStaged WatchEventKind = "param_staged"

	// WatchKindParamSuperseded — a parameter had a pending
	// change in the prior snapshot AND has a (different)
	// pending change in the current snapshot, and the
	// pending entry was REPLACED rather than promoted (we
	// detect this by checking that the prior pending value is
	// not what currently sits in `active`). Represents the
	// "one authority overrode another's still-pending
	// proposal" lifecycle.
	WatchKindParamSuperseded WatchEventKind = "param_superseded"

	// WatchKindParamActivated — a parameter's active value
	// changed across two consecutive successful polls. Almost
	// always means a pending change just promoted at its
	// EffectiveHeight; could also mean a binary upgrade
	// rotated the active value through a non-governance path
	// (rare). The canonical "your proposal landed" signal.
	WatchKindParamActivated WatchEventKind = "param_activated"

	// WatchKindParamRemoved — a parameter that had a pending
	// change in the prior snapshot has no pending change in
	// the current snapshot AND its active value is unchanged.
	// Means the change was rejected / dropped from the
	// pending slot via some path that is not promotion. Today
	// the chain has no such path (pending entries either
	// supersede or promote), but the watcher emits this
	// defensively so an operator notices unexpected store
	// churn.
	WatchKindParamRemoved WatchEventKind = "param_removed"

	// WatchKindParamAuthoritiesChanged — the validator's
	// reported authority list differs across two snapshots.
	// Today the list is binary-baked, so this should never
	// fire under normal operation; emitting it loudly is a
	// signal that the validator restarted with a different
	// chain config (or a future binary added a multisig-gated
	// rotation tx and the operator missed the announcement).
	WatchKindParamAuthoritiesChanged WatchEventKind = "param_authorities_changed"

	// --- attestation-rejection watcher kinds (see watch_archspoof.go) ---
	//
	// The archspoof watcher polls /api/metrics/prometheus and
	// diffs the qsdm_attest_archspoof_rejected_total{reason}
	// and qsdm_attest_hashrate_rejected_total{arch} counter
	// families. It emits one event per non-zero delta per
	// label across consecutive successful polls. Counters
	// only ever monotonically increase under normal operation;
	// a decrease (process restart wiping in-memory counters)
	// resets the snapshot to the new baseline without emitting.
	//
	// Operators run this alongside the existing Prometheus
	// alert rules: the alerts say "something is wrong"; the
	// watcher says "here is each individual hit, in order, as
	// they happen". The two views complement each other for
	// incident-response.

	// WatchKindArchSpoofBurst — at least one new
	// qsdm_attest_archspoof_rejected_total increment was
	// observed for a (reason) bucket since the prior poll.
	// Reason is one of: unknown_arch, gpu_name_mismatch,
	// cc_subject_mismatch (see MINING_PROTOCOL_V2 §4.6.4).
	WatchKindArchSpoofBurst WatchEventKind = "archspoof_burst"

	// WatchKindHashrateBurst — at least one new
	// qsdm_attest_hashrate_rejected_total increment was
	// observed for an (arch) bucket since the prior poll.
	// Arch is one of the canonical NVIDIA architecture
	// names (ada / hopper / blackwell / blackwell_ultra /
	// rubin / rubin_ultra) or "unknown" for arch-less hits.
	WatchKindHashrateBurst WatchEventKind = "hashrate_burst"
)

// WatchEvent is the unified wire shape of one diff output across
// every `qsdmcli watch *` subcommand. Field presence depends on
// Kind (see WatchEventKind doc); zero-valued fields are omitted
// from --json via omitempty so consumers can switch on Kind
// without branching on Go zero-values.
//
// Field set is grouped by which subcommand populates it:
//
//   - Always populated: Timestamp, Kind.
//   - Enrollment-only: Phase, PrevPhase, StakeDust, PrevStakeDust,
//     DeltaDust, Slashable, EnrolledAtHeight, UnbondMaturesAtHeight,
//     RevokedAtHeight. NodeID identifies the rig.
//   - Slash-only: TxID, Outcome, PrevOutcome, Height, EvidenceKind,
//     Slasher, SlashedDust, RewardedDust, BurnedDust, AutoRevoked,
//     AutoRevokeRemainingDust, RejectReason. NodeID identifies the
//     slashed rig (same field, different role — semantically a
//     node_id either way).
//   - Error events: Error.
//
// Adding fields is non-breaking. Renaming JSON tags is a wire-
// format change.
type WatchEvent struct {
	Timestamp time.Time      `json:"ts"`
	Kind      WatchEventKind `json:"event"`

	// NodeID — the rig identifier. For enrollment events it
	// is the watched rig; for slash events it is the rig
	// against which the slash was filed (the receipt's
	// `node_id` field).
	NodeID string `json:"node_id,omitempty"`

	// --- enrollment-watcher payload ---

	Phase     string `json:"phase,omitempty"`
	PrevPhase string `json:"prev_phase,omitempty"`

	StakeDust     uint64 `json:"stake_dust,omitempty"`
	PrevStakeDust uint64 `json:"prev_stake_dust,omitempty"`
	DeltaDust     int64  `json:"delta_dust,omitempty"`

	Slashable             bool   `json:"slashable,omitempty"`
	EnrolledAtHeight      uint64 `json:"enrolled_at_height,omitempty"`
	UnbondMaturesAtHeight uint64 `json:"unbond_matures_at_height,omitempty"`
	RevokedAtHeight       uint64 `json:"revoked_at_height,omitempty"`

	// --- slash-watcher payload ---

	// TxID is the slash transaction id the operator asked
	// the watcher to track. Always populated on slash_*
	// events; never populated on enrollment events.
	TxID string `json:"tx_id,omitempty"`

	// Outcome / PrevOutcome — the receipt's `outcome` field
	// ("applied" | "rejected"). PrevOutcome is set only on
	// WatchKindSlashOutcomeChange.
	Outcome     string `json:"outcome,omitempty"`
	PrevOutcome string `json:"prev_outcome,omitempty"`

	// Height — block height at which the receipt was
	// recorded. Only populated on slash_resolved /
	// slash_outcome_change.
	Height uint64 `json:"height,omitempty"`

	// EvidenceKind — the receipt's `evidence_kind` field
	// ("forged-attestation" | "double-mining" |
	// "freshness-cheat"). Surfaced so dashboards can group
	// or color-code by offence kind.
	EvidenceKind string `json:"evidence_kind,omitempty"`

	// Slasher — address that submitted the slash transaction.
	Slasher string `json:"slasher,omitempty"`

	// Slash-applied financial outcome. All three are
	// populated only on the applied path (slash_resolved
	// with Outcome=="applied"). On rejected receipts they
	// are zero, hence omitempty.
	SlashedDust  uint64 `json:"slashed_dust,omitempty"`
	RewardedDust uint64 `json:"rewarded_dust,omitempty"`
	BurnedDust   uint64 `json:"burned_dust,omitempty"`

	// AutoRevoked + AutoRevokeRemainingDust — the post-slash
	// auto-revoke signal. AutoRevoked is true iff the slash
	// drained the rig's stake below the auto-revoke
	// threshold; AutoRevokeRemainingDust is the residual
	// stake at the moment the auto-revoke fired.
	AutoRevoked             bool   `json:"auto_revoked,omitempty"`
	AutoRevokeRemainingDust uint64 `json:"auto_revoke_remaining_dust,omitempty"`

	// RejectReason — the receipt's `reject_reason` field
	// ("verifier_failed", "evidence_replayed", etc.).
	// Populated only on the rejected path (slash_resolved
	// with Outcome=="rejected").
	RejectReason string `json:"reject_reason,omitempty"`

	// Error carries the trimmed error message for
	// WatchKindError events. Empty for all other kinds.
	Error string `json:"error,omitempty"`

	// --- governance-params watcher payload ---
	//
	// Param identifies the governance parameter the event
	// describes (e.g. "reward_bps"). Always populated on
	// WatchKindParam* events; never on enrollment / slash
	// events.
	Param string `json:"param,omitempty"`

	// ActiveValue / PrevActiveValue — the active value of the
	// parameter on the current and prior snapshot. Both are
	// populated on WatchKindParamActivated; only ActiveValue
	// is populated on staged / superseded / removed events.
	ActiveValue     uint64 `json:"active_value,omitempty"`
	PrevActiveValue uint64 `json:"prev_active_value,omitempty"`

	// PendingValue / PendingEffectiveHeight — the pending
	// change's value and activation height. Both populated on
	// WatchKindParamStaged and WatchKindParamSuperseded.
	PendingValue           uint64 `json:"pending_value,omitempty"`
	PendingEffectiveHeight uint64 `json:"pending_effective_height,omitempty"`

	// PrevPendingValue — the pending value the supersede
	// replaced (only on WatchKindParamSuperseded /
	// WatchKindParamRemoved).
	PrevPendingValue uint64 `json:"prev_pending_value,omitempty"`

	// Authority — the address that submitted the pending
	// change. Empty when not applicable.
	Authority string `json:"authority,omitempty"`

	// Memo — the operator-supplied memo on the pending
	// change. Empty when not applicable.
	Memo string `json:"memo,omitempty"`

	// AuthoritiesAdded / AuthoritiesRemoved — set on
	// WatchKindParamAuthoritiesChanged. Sorted ASC.
	AuthoritiesAdded   []string `json:"authorities_added,omitempty"`
	AuthoritiesRemoved []string `json:"authorities_removed,omitempty"`

	// --- archspoof / hashrate watcher payload ---
	//
	// Reason — populated on WatchKindArchSpoofBurst. Mirrors
	// the {reason="..."} label of the underlying counter:
	// unknown_arch | gpu_name_mismatch | cc_subject_mismatch.
	Reason string `json:"reason,omitempty"`

	// Arch — populated on WatchKindHashrateBurst (canonical
	// NVIDIA arch name or "unknown"). NOT populated on
	// WatchKindArchSpoofBurst even though some reasons carry
	// arch context internally — keeping the counter labels
	// 1:1 with the wire shape eliminates a class of mis-
	// reading bugs in dashboards.
	Arch string `json:"arch,omitempty"`

	// DeltaCount — non-zero, positive count delta observed
	// for this (kind, label) bucket between the prior and
	// current poll. Always ≥ 1 on archspoof/hashrate events;
	// zero deltas are filtered out before emission.
	DeltaCount uint64 `json:"delta_count,omitempty"`

	// TotalCount — the absolute counter value at the moment
	// of the current poll. Lets dashboards reconstruct the
	// monotonic series without re-fetching the metrics
	// endpoint.
	TotalCount uint64 `json:"total_count,omitempty"`
}

// watchRecord mirrors api.EnrollmentRecordView. Kept local to
// avoid pulling pkg/api into the CLI binary (same posture as
// the miner-console poller's enrollmentRecordWire). JSON tags
// MUST stay byte-identical to api.EnrollmentRecordView; the
// test suite pins this.
type watchRecord struct {
	NodeID                string `json:"node_id"`
	Owner                 string `json:"owner"`
	GPUUUID               string `json:"gpu_uuid"`
	StakeDust             uint64 `json:"stake_dust"`
	EnrolledAtHeight      uint64 `json:"enrolled_at_height"`
	RevokedAtHeight       uint64 `json:"revoked_at_height,omitempty"`
	UnbondMaturesAtHeight uint64 `json:"unbond_matures_at_height,omitempty"`
	Phase                 string `json:"phase"`
	Slashable             bool   `json:"slashable"`
}

// watchListPage mirrors api.EnrollmentListPageView for the
// list endpoint. Same byte-identicality requirement.
type watchListPage struct {
	Records      []watchRecord `json:"records"`
	NextCursor   string        `json:"next_cursor,omitempty"`
	HasMore      bool          `json:"has_more"`
	TotalMatches uint64        `json:"total_matches"`
	Phase        string        `json:"phase,omitempty"`
}

// watchOptions is the parsed flag set for `qsdmcli watch enrollments`.
// Held as a struct so the snapshot-fetch logic can be unit-tested
// without re-implementing flag parsing.
type watchOptions struct {
	Interval         time.Duration
	Phase            string
	NodeID           string
	Limit            int
	Once             bool
	JSON             bool
	IncludeExisting  bool
}

// watchCommand handles `qsdmcli watch <subcommand> [flags...]`.
// Currently `enrollments`, `slashes`, `params`, and `archspoof`
// are implemented; future subcommands (`proofs`, etc.) plug in
// here.
func (c *CLI) watchCommand(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: qsdmcli watch <subcommand> [flags]\n\nSubcommands:\n  enrollments    stream enrollment phase-change events\n  slashes        stream slash-receipt resolution events\n  params         stream governance-parameter staging/activation events\n  archspoof      stream attestation arch-spoof and hashrate-band rejection bursts")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "enrollments":
		return c.watchEnrollments(rest)
	case "slashes":
		return c.watchSlashes(rest)
	case "params":
		return c.watchParams(rest)
	case "archspoof":
		return c.watchArchSpoof(rest)
	default:
		return fmt.Errorf("unknown watch subcommand: %q (known: enrollments, slashes, params, archspoof)", sub)
	}
}

// watchEnrollments parses flags, fetches an initial snapshot,
// and enters the diff loop. Returns on SIGINT/SIGTERM with no
// error (operator-driven exit is the success path) or on the
// first fatal startup error (e.g. unreachable validator before
// we've ever observed a snapshot).
func (c *CLI) watchEnrollments(args []string) error {
	fs := flag.NewFlagSet("watch enrollments", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		interval = fs.Duration("interval", DefaultWatchInterval,
			"polling cadence (clamped: ≥5s, default 30s)")
		phase = fs.String("phase", "",
			"server-side phase filter: active | pending_unbond | revoked")
		nodeID = fs.String("node-id", "",
			"single-node mode: poll one record instead of paginating the list")
		limit = fs.Int("limit", 0,
			"page size for list mode (0 = server default)")
		once = fs.Bool("once", false,
			"emit a single snapshot and exit (no diff loop; useful for cron)")
		jsonOut = fs.Bool("json", false,
			"emit JSON-Lines (one event per line) instead of human-formatted lines")
		includeExisting = fs.Bool("include-existing", false,
			"on the first poll, emit a synthetic 'new' event for every existing record")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	opts := watchOptions{
		Interval:        *interval,
		Phase:           *phase,
		NodeID:          *nodeID,
		Limit:           *limit,
		Once:            *once,
		JSON:            *jsonOut,
		IncludeExisting: *includeExisting,
	}
	if err := opts.normalize(); err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	return c.runWatchEnrollments(ctx, opts, os.Stdout, os.Stderr)
}

// normalize validates flags and clamps values. Pure function;
// the test suite hits it directly.
func (o *watchOptions) normalize() error {
	if o.Interval == 0 {
		o.Interval = DefaultWatchInterval
	}
	if o.Interval > 0 && o.Interval < MinWatchInterval {
		o.Interval = MinWatchInterval
	}
	switch o.Phase {
	case "", "active", "pending_unbond", "revoked":
		// ok
	default:
		return fmt.Errorf("invalid --phase=%q; want one of: active, pending_unbond, revoked", o.Phase)
	}
	if o.NodeID != "" && strings.Contains(o.NodeID, "/") {
		return fmt.Errorf("--node-id must not contain '/'")
	}
	if o.NodeID != "" && o.Phase != "" {
		return fmt.Errorf("--node-id and --phase are mutually exclusive (single-node mode has no filter)")
	}
	if o.Limit < 0 {
		return fmt.Errorf("--limit must be ≥ 0")
	}
	return nil
}

// runWatchEnrollments is the testable core of watchEnrollments.
// stdout / stderr are passed in so tests can capture output;
// production code wires them to os.Stdout / os.Stderr.
//
// Returns nil on ctx-cancellation (operator-driven exit) or on
// --once completion. Returns a non-nil error only when the very
// first snapshot fails — at that point we've never confirmed the
// validator speaks v2, so the operator wants a non-zero exit.
// Subsequent failures are emitted as WatchKindError events and
// the loop continues.
func (c *CLI) runWatchEnrollments(
	ctx context.Context,
	opts watchOptions,
	stdout, stderr io.Writer,
) error {
	// First snapshot. Failure here is fatal — the operator
	// likely typo'd the URL or the validator is hard down.
	prev, err := c.fetchEnrollmentSnapshot(ctx, opts)
	if err != nil {
		return fmt.Errorf("initial snapshot: %w", err)
	}

	now := time.Now().UTC()
	if opts.IncludeExisting {
		emitEvents(stdout, stderr, opts.JSON, snapshotAsNewEvents(prev, now))
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
			next, err := c.fetchEnrollmentSnapshot(ctx, opts)
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
			events := diffSnapshots(prev, next, tickAt)
			emitEvents(stdout, stderr, opts.JSON, events)
			prev = next
		}
	}
}

// fetchEnrollmentSnapshot returns a node_id-keyed map of
// records currently visible at the validator under opts.
// Single-node mode produces a 0- or 1-element map; list mode
// walks the cursor until exhausted. The map shape gives the
// diff function O(1) lookup at the cost of one allocation per
// poll, which is irrelevant at 30s cadence.
func (c *CLI) fetchEnrollmentSnapshot(
	ctx context.Context,
	opts watchOptions,
) (map[string]watchRecord, error) {
	if opts.NodeID != "" {
		return c.fetchSingleEnrollment(ctx, opts.NodeID)
	}
	return c.fetchEnrollmentList(ctx, opts)
}

// fetchSingleEnrollment polls /mining/enrollment/{node_id}.
// 404 (record not present) is NOT an error — it produces an
// empty map, and the diff layer turns the gap into a `dropped`
// event. Any other non-2xx is fatal for this cycle.
func (c *CLI) fetchSingleEnrollment(
	ctx context.Context,
	nodeID string,
) (map[string]watchRecord, error) {
	body, status, err := c.getWithStatus(ctx,
		"/mining/enrollment/"+url.PathEscape(nodeID))
	if err != nil {
		return nil, err
	}
	switch status {
	case 200:
		// fall through
	case 404:
		return map[string]watchRecord{}, nil
	default:
		return nil, fmt.Errorf("validator HTTP %d: %s",
			status, truncateForLine(string(body), 160))
	}

	var rec watchRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		return nil, fmt.Errorf("decode record: %w", err)
	}
	if rec.NodeID == "" {
		// Defensive: server returned 200 but the body has
		// no node_id. Treat as "no record" rather than
		// poison the snapshot.
		return map[string]watchRecord{}, nil
	}
	return map[string]watchRecord{rec.NodeID: rec}, nil
}

// fetchEnrollmentList walks the cursor-paginated list endpoint
// and accumulates every record into a map. Reuses the
// MaxWatchPages defence against a misbehaving server.
func (c *CLI) fetchEnrollmentList(
	ctx context.Context,
	opts watchOptions,
) (map[string]watchRecord, error) {
	out := make(map[string]watchRecord, 64)
	cursor := ""
	for i := 0; i < MaxWatchPages; i++ {
		path := buildEnrollmentsListPath(opts.Phase, opts.Limit, cursor)
		body, status, err := c.getWithStatus(ctx, path)
		if err != nil {
			return nil, err
		}
		if status != 200 {
			return nil, fmt.Errorf("validator HTTP %d: %s",
				status, truncateForLine(string(body), 160))
		}
		var page watchListPage
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("decode page %d: %w", i, err)
		}
		for _, rec := range page.Records {
			if rec.NodeID == "" {
				continue
			}
			out[rec.NodeID] = rec
		}
		if !page.HasMore {
			return out, nil
		}
		if page.NextCursor == "" {
			return nil, fmt.Errorf("page %d: has_more=true with empty next_cursor", i)
		}
		cursor = page.NextCursor
	}
	return nil, fmt.Errorf("paginated list exceeded %d pages; aborting", MaxWatchPages)
}

// buildEnrollmentsListPath assembles the /mining/enrollments
// query string. Pure function for testability.
func buildEnrollmentsListPath(phase string, limit int, cursor string) string {
	v := url.Values{}
	if phase != "" {
		v.Set("phase", phase)
	}
	if limit > 0 {
		v.Set("limit", fmt.Sprintf("%d", limit))
	}
	if cursor != "" {
		v.Set("cursor", cursor)
	}
	path := "/mining/enrollments"
	if encoded := v.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return path
}

// getWithStatus is c.get's lower-level cousin: returns the
// status code alongside the body so callers can switch on 404
// vs other errors. Doesn't error on non-2xx; that's the
// caller's call. Context-aware so SIGINT cancels an in-flight
// poll cleanly.
func (c *CLI) getWithStatus(ctx context.Context, path string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return nil, 0, fmt.Errorf("read body: %w", err)
	}
	return body, resp.StatusCode, nil
}

// snapshotAsNewEvents synthesises a `new` event per record in
// the snapshot, sorted by node_id. Used for --include-existing
// on the first poll.
func snapshotAsNewEvents(snap map[string]watchRecord, now time.Time) []WatchEvent {
	out := make([]WatchEvent, 0, len(snap))
	keys := make([]string, 0, len(snap))
	for k := range snap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rec := snap[k]
		out = append(out, recordToNewEvent(rec, now))
	}
	return out
}

// recordToNewEvent builds a `new` WatchEvent from a record.
// Centralised so the include-existing path and the diff path
// agree on the field set.
func recordToNewEvent(rec watchRecord, ts time.Time) WatchEvent {
	return WatchEvent{
		Timestamp:             ts,
		Kind:                  WatchKindNew,
		NodeID:                rec.NodeID,
		Phase:                 rec.Phase,
		StakeDust:             rec.StakeDust,
		Slashable:             rec.Slashable,
		EnrolledAtHeight:      rec.EnrolledAtHeight,
		UnbondMaturesAtHeight: rec.UnbondMaturesAtHeight,
		RevokedAtHeight:       rec.RevokedAtHeight,
	}
}

// diffSnapshots compares two consecutive snapshots and emits
// events for: new node_ids, dropped node_ids, phase
// transitions, and stake deltas (same phase, different stake).
// Ordering: events sorted by node_id ASC, then by kind in the
// fixed order (new, transition, stake_delta, dropped). A
// single node_id never emits more than one event per cycle —
// transition wins over stake_delta if both apply (because the
// caller cares more about the phase change).
//
// Pure function: deterministic, no side effects, easy to test.
func diffSnapshots(prev, next map[string]watchRecord, ts time.Time) []WatchEvent {
	keys := make(map[string]struct{}, len(prev)+len(next))
	for k := range prev {
		keys[k] = struct{}{}
	}
	for k := range next {
		keys[k] = struct{}{}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	out := make([]WatchEvent, 0, len(sorted))
	for _, id := range sorted {
		prevRec, hadPrev := prev[id]
		nextRec, hasNext := next[id]
		switch {
		case !hadPrev && hasNext:
			out = append(out, recordToNewEvent(nextRec, ts))
		case hadPrev && !hasNext:
			out = append(out, WatchEvent{
				Timestamp: ts,
				Kind:      WatchKindDropped,
				NodeID:    id,
				PrevPhase: prevRec.Phase,
			})
		case hadPrev && hasNext:
			if prevRec.Phase != nextRec.Phase {
				out = append(out, WatchEvent{
					Timestamp:             ts,
					Kind:                  WatchKindTransition,
					NodeID:                id,
					PrevPhase:             prevRec.Phase,
					Phase:                 nextRec.Phase,
					StakeDust:             nextRec.StakeDust,
					Slashable:             nextRec.Slashable,
					EnrolledAtHeight:      nextRec.EnrolledAtHeight,
					UnbondMaturesAtHeight: nextRec.UnbondMaturesAtHeight,
					RevokedAtHeight:       nextRec.RevokedAtHeight,
				})
				continue
			}
			if prevRec.StakeDust != nextRec.StakeDust {
				out = append(out, WatchEvent{
					Timestamp:     ts,
					Kind:          WatchKindStakeDelta,
					NodeID:        id,
					Phase:         nextRec.Phase,
					PrevStakeDust: prevRec.StakeDust,
					StakeDust:     nextRec.StakeDust,
					DeltaDust:     int64(nextRec.StakeDust) - int64(prevRec.StakeDust),
					Slashable:     nextRec.Slashable,
				})
			}
		}
	}
	return out
}

// emitEvents writes events to the appropriate stream.
//
// Routing:
//
//   - JSON mode: every event goes to stdout as JSON-Lines (one
//     compact JSON object per line). WatchKindError included
//     so log shippers see the error stream in-line.
//   - Human mode: data events to stdout (column-formatted),
//     error events to stderr (free-form). Exit code is NOT
//     bumped — the watcher is meant to keep running through
//     transient failures.
func emitEvents(stdout, stderr io.Writer, jsonMode bool, events []WatchEvent) {
	for _, ev := range events {
		if jsonMode {
			b, err := json.Marshal(ev)
			if err != nil {
				fmt.Fprintf(stderr, "watch: marshal event: %v\n", err)
				continue
			}
			fmt.Fprintln(stdout, string(b))
			continue
		}
		if ev.Kind == WatchKindError {
			fmt.Fprintf(stderr, "%s ERROR       %s\n",
				ev.Timestamp.Format(time.RFC3339), ev.Error)
			continue
		}
		fmt.Fprintln(stdout, formatEventHuman(ev))
	}
}

// formatEventHuman renders a WatchEvent as a single column-aligned
// stdout line. Pure function for testability.
//
// Format:
//
//	<RFC3339>  <KIND-padded-11>  node=<id>  <phase-summary>  [stake fields]
//
// Examples:
//
//	2026-04-28T03:51:42Z NEW         node=alpha-rtx4090-01  phase=active                       stake=10.0000 CELL
//	2026-04-28T03:52:12Z TRANSITION  node=beta-rtx3090-02   phase=active->pending_unbond       matures_at=1235000
//	2026-04-28T03:55:42Z DROPPED     node=gamma-rtx5090-03  last_phase=pending_unbond
//	2026-04-28T03:56:12Z STAKE_DELTA node=alpha-rtx4090-01  phase=active                       stake=10.0000->5.0000 CELL  delta=-5.0000 CELL
func formatEventHuman(ev WatchEvent) string {
	ts := ev.Timestamp.Format(time.RFC3339)
	kind := strings.ToUpper(string(ev.Kind))
	// Pad to width 25 so columns line up across all kinds:
	// enrollment kinds NEW(3), TRANSITION(10), STAKE_DELTA(11),
	// DROPPED(7), ERROR(5); slash kinds SLASH_PENDING(13),
	// SLASH_RESOLVED(14), SLASH_EVICTED(13), SLASH_OUTCOME_CHANGE(20);
	// param kinds up to PARAM_AUTHORITIES_CHANGED(25);
	// archspoof kinds ARCHSPOOF_BURST(15), HASHRATE_BURST(14).
	// Picked the max so mixed-stream tee'd output stays aligned.
	for len(kind) < 25 {
		kind += " "
	}
	switch ev.Kind {
	case WatchKindNew:
		s := fmt.Sprintf("%s %s node=%s  phase=%s  stake=%s",
			ts, kind, ev.NodeID, ev.Phase, formatCELL(ev.StakeDust))
		if ev.EnrolledAtHeight != 0 {
			s += fmt.Sprintf("  enrolled_at=%d", ev.EnrolledAtHeight)
		}
		return s
	case WatchKindTransition:
		s := fmt.Sprintf("%s %s node=%s  phase=%s->%s",
			ts, kind, ev.NodeID, ev.PrevPhase, ev.Phase)
		if ev.UnbondMaturesAtHeight != 0 {
			s += fmt.Sprintf("  matures_at=%d", ev.UnbondMaturesAtHeight)
		}
		if ev.RevokedAtHeight != 0 {
			s += fmt.Sprintf("  revoked_at=%d", ev.RevokedAtHeight)
		}
		return s
	case WatchKindStakeDelta:
		sign := ""
		if ev.DeltaDust > 0 {
			sign = "+"
		}
		return fmt.Sprintf("%s %s node=%s  phase=%s  stake=%s->%s  delta=%s%s",
			ts, kind, ev.NodeID, ev.Phase,
			formatCELL(ev.PrevStakeDust), formatCELL(ev.StakeDust),
			sign, formatCELLSigned(ev.DeltaDust))
	case WatchKindDropped:
		return fmt.Sprintf("%s %s node=%s  last_phase=%s",
			ts, kind, ev.NodeID, ev.PrevPhase)
	case WatchKindSlashPending:
		return fmt.Sprintf("%s %s tx=%s",
			ts, kind, ev.TxID)
	case WatchKindSlashResolved:
		s := fmt.Sprintf("%s %s tx=%s  outcome=%s",
			ts, kind, ev.TxID, ev.Outcome)
		if ev.NodeID != "" {
			s += "  node=" + ev.NodeID
		}
		if ev.EvidenceKind != "" {
			s += "  kind=" + ev.EvidenceKind
		}
		if ev.Height != 0 {
			s += fmt.Sprintf("  height=%d", ev.Height)
		}
		switch ev.Outcome {
		case "applied":
			s += fmt.Sprintf("  slashed=%s  rewarded=%s  burned=%s",
				formatCELL(ev.SlashedDust),
				formatCELL(ev.RewardedDust),
				formatCELL(ev.BurnedDust))
			if ev.AutoRevoked {
				s += "  auto_revoked=true"
				if ev.AutoRevokeRemainingDust != 0 {
					s += fmt.Sprintf("(remaining=%s)",
						formatCELL(ev.AutoRevokeRemainingDust))
				}
			}
		case "rejected":
			if ev.RejectReason != "" {
				s += "  reason=" + ev.RejectReason
			}
			if ev.Error != "" {
				s += "  err=" + truncateForLine(ev.Error, 80)
			}
		}
		return s
	case WatchKindSlashEvicted:
		s := fmt.Sprintf("%s %s tx=%s",
			ts, kind, ev.TxID)
		if ev.PrevOutcome != "" {
			s += "  last_outcome=" + ev.PrevOutcome
		}
		return s
	case WatchKindSlashOutcomeChange:
		return fmt.Sprintf("%s %s tx=%s  outcome=%s->%s",
			ts, kind, ev.TxID, ev.PrevOutcome, ev.Outcome)
	case WatchKindParamStaged:
		s := fmt.Sprintf("%s %s param=%s  active=%d  pending=%d@H+%d",
			ts, kind, ev.Param, ev.ActiveValue,
			ev.PendingValue, ev.PendingEffectiveHeight)
		if ev.Authority != "" {
			s += "  by=" + ev.Authority
		}
		if ev.Memo != "" {
			s += "  memo=" + truncateForLine(ev.Memo, 80)
		}
		return s
	case WatchKindParamSuperseded:
		s := fmt.Sprintf("%s %s param=%s  pending=%d->%d  effective=H+%d",
			ts, kind, ev.Param,
			ev.PrevPendingValue, ev.PendingValue, ev.PendingEffectiveHeight)
		if ev.Authority != "" {
			s += "  by=" + ev.Authority
		}
		return s
	case WatchKindParamActivated:
		return fmt.Sprintf("%s %s param=%s  active=%d->%d",
			ts, kind, ev.Param,
			ev.PrevActiveValue, ev.ActiveValue)
	case WatchKindParamRemoved:
		return fmt.Sprintf("%s %s param=%s  active=%d  prev_pending=%d (dropped without activation)",
			ts, kind, ev.Param, ev.ActiveValue, ev.PrevPendingValue)
	case WatchKindParamAuthoritiesChanged:
		s := fmt.Sprintf("%s %s authorities_changed", ts, kind)
		if len(ev.AuthoritiesAdded) > 0 {
			s += "  added=" + strings.Join(ev.AuthoritiesAdded, ",")
		}
		if len(ev.AuthoritiesRemoved) > 0 {
			s += "  removed=" + strings.Join(ev.AuthoritiesRemoved, ",")
		}
		return s
	case WatchKindArchSpoofBurst:
		return fmt.Sprintf("%s %s reason=%s  delta=+%d  total=%d",
			ts, kind, ev.Reason, ev.DeltaCount, ev.TotalCount)
	case WatchKindHashrateBurst:
		arch := ev.Arch
		if arch == "" {
			arch = "unknown"
		}
		return fmt.Sprintf("%s %s arch=%s  delta=+%d  total=%d",
			ts, kind, arch, ev.DeltaCount, ev.TotalCount)
	default:
		// Defensive — unreachable in normal flow because
		// WatchKindError takes the stderr path before
		// formatEventHuman is called.
		return fmt.Sprintf("%s %s node=%s",
			ts, kind, ev.NodeID)
	}
}

// formatCELL renders a dust amount as "X.YYYY CELL" with 4
// decimal places. 1 CELL = 100_000_000 dust (per
// MINING_PROTOCOL_V2.md §13). Operators reading watch output
// don't want to mentally divide by 1e8 every time a slash
// lands.
func formatCELL(dust uint64) string {
	const dustPerCELL = 100_000_000
	whole := dust / dustPerCELL
	frac := dust % dustPerCELL
	// 4-decimal rounding: dust / 10_000 yields the 4-decimal
	// scaled fraction.
	return fmt.Sprintf("%d.%04d CELL", whole, frac/10_000)
}

// formatCELLSigned is formatCELL for an int64 delta. The
// caller supplies the sign; we render the magnitude.
func formatCELLSigned(deltaDust int64) string {
	mag := uint64(abs64(deltaDust))
	return formatCELL(mag)
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// truncateForLine clamps an arbitrary string to n runes,
// stripping newlines so it fits in one line of operator
// output. Mirrors the helper in qsdmminer-console's
// enrollment_poller.go but local to the CLI binary.
func truncateForLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
