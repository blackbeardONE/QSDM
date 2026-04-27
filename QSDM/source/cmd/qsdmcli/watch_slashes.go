package main

// watch_slashes.go — symmetric counterpart to
// watch_enrollments. Polls /api/v1/mining/slash/{tx_id} for a
// caller-supplied set of slash transaction ids and emits one
// event per resolution / eviction / outcome change.
//
// Why a separate watcher rather than overloading
// watch_enrollments:
//
//   - Enrollment polling is *registry-walking*: the watcher
//     does not know which node_ids exist a priori; it
//     discovers them. Slash polling is *id-driven*: the
//     operator submits a slash, gets a tx_id, and asks "did
//     it apply?". The natural inputs differ, so a separate
//     subcommand keeps the flag surface honest.
//   - Slash receipts are stored in a *bounded* FIFO store
//     (chain.SlashReceiptStore default cap 10000); enrolment
//     records are unbounded and never evict. The "evicted"
//     state is meaningful here and meaningless for
//     enrollments, so the kind enum should include it for
//     slashes only.
//   - The 404→200 lifecycle is the dominant event-stream
//     shape (one terminal "resolved" event per id), unlike
//     the long-running phase-change stream of enrollments.
//
// The subcommand still emits into the shared WatchEvent
// envelope so JSON-Lines consumers can decode either stream
// with one struct definition.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
)

// MaxWatchedTxIDs caps the number of distinct tx ids one
// watcher process will track. 1000 is comfortably above any
// realistic single-operator slash backlog while keeping the
// snapshot-poll fan-out (1 GET / id / cycle) below 200 RPS at
// the 5s floor cadence.
const MaxWatchedTxIDs = 1000

// MaxTxIDLen mirrors the 256-byte cap api.SlashReceiptHandler
// enforces server-side. Sanitising at parse time gives a
// clean operator-side error instead of HTTP 400 noise per cycle.
const MaxTxIDLen = 256

// watchSlashOptions is the parsed flag set for
// `qsdmcli watch slashes`. Kept as a struct so the snapshot
// + diff core can be unit-tested without re-implementing
// flag parsing.
type watchSlashOptions struct {
	TxIDs            []string
	TxIDsFile        string
	Interval         time.Duration
	Once             bool
	JSON             bool
	IncludePending   bool
	ExitOnResolved   bool
}

// slashReceiptWire mirrors api.SlashReceiptView. JSON tags
// MUST stay byte-identical to the canonical view. Same
// posture as watch.go's watchRecord and the
// qsdmminer-console enrollmentRecordWire — keep pkg/api out
// of the CLI's link graph but pin the wire shape via test.
type slashReceiptWire struct {
	TxID                    string    `json:"tx_id"`
	Outcome                 string    `json:"outcome"`
	RecordedAt              time.Time `json:"recorded_at"`
	Height                  uint64    `json:"height"`
	Slasher                 string    `json:"slasher,omitempty"`
	NodeID                  string    `json:"node_id,omitempty"`
	EvidenceKind            string    `json:"evidence_kind,omitempty"`
	SlashedDust             uint64    `json:"slashed_dust,omitempty"`
	RewardedDust            uint64    `json:"rewarded_dust,omitempty"`
	BurnedDust              uint64    `json:"burned_dust,omitempty"`
	AutoRevoked             bool      `json:"auto_revoked,omitempty"`
	AutoRevokeRemainingDust uint64    `json:"auto_revoke_remaining_dust,omitempty"`
	RejectReason            string    `json:"reject_reason,omitempty"`
	Err                     string    `json:"error,omitempty"`
}

// slashStatus is the per-tx state the diff loop carries
// across cycles. Distinct from slashReceiptWire because we
// also need to remember "we observed this id as 404" (vs
// "we never observed this id at all"); a present-but-empty
// Receipt + Pending=true encodes the "tracked but unresolved"
// state.
type slashStatus struct {
	TxID    string
	Pending bool             // true when validator returned 404
	Receipt *slashReceiptWire // non-nil iff a 200 was observed
}

// watchSlashes is the flag-parsing entry point for
// `qsdmcli watch slashes`. Mirrors watchEnrollments in shape
// so operators get a familiar flag surface across both.
func (c *CLI) watchSlashes(args []string) error {
	fs := flag.NewFlagSet("watch slashes", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		interval = fs.Duration("interval", DefaultWatchInterval,
			"polling cadence (clamped: ≥5s, default 30s)")
		once = fs.Bool("once", false,
			"emit a single snapshot and exit (no diff loop)")
		jsonOut = fs.Bool("json", false,
			"emit JSON-Lines (one event per line) instead of human-formatted lines")
		includePending = fs.Bool("include-pending", false,
			"emit a 'slash_pending' event each poll for tx ids that have not resolved yet (verbose)")
		exitOnResolved = fs.Bool("exit-on-resolved", false,
			"exit cleanly once every tracked tx has reached a terminal outcome")
		txIDsFile = fs.String("tx-ids-file", "",
			"path to a file containing one tx_id per line ('-' reads from stdin); merged with --tx-id flags")
	)
	// --tx-id is a repeatable flag. We use a custom
	// flag.Value so the operator can pass:
	//   --tx-id=tx-001 --tx-id=tx-002 --tx-id=tx-003
	// rather than fighting comma-separated parsing.
	var txIDList stringSliceFlag
	fs.Var(&txIDList, "tx-id",
		"slash tx_id to track (repeatable; mutually exclusive with --tx-ids-file is NOT enforced — both merge)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	opts := watchSlashOptions{
		TxIDs:           []string(txIDList),
		TxIDsFile:       *txIDsFile,
		Interval:        *interval,
		Once:            *once,
		JSON:            *jsonOut,
		IncludePending:  *includePending,
		ExitOnResolved:  *exitOnResolved,
	}
	if err := opts.normalize(); err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	return c.runWatchSlashes(ctx, opts, os.Stdout, os.Stderr)
}

// stringSliceFlag implements flag.Value for repeatable
// string flags. Standard Go pattern.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// normalize validates flags, merges --tx-id and --tx-ids-file
// inputs, deduplicates, and clamps the interval. Pure-ish:
// reads the tx-ids-file from disk (or stdin) but otherwise
// has no side effects.
func (o *watchSlashOptions) normalize() error {
	if o.Interval == 0 {
		o.Interval = DefaultWatchInterval
	}
	if o.Interval > 0 && o.Interval < MinWatchInterval {
		o.Interval = MinWatchInterval
	}

	merged, err := mergeTxIDs(o.TxIDs, o.TxIDsFile, os.Stdin)
	if err != nil {
		return err
	}
	if len(merged) == 0 {
		return errors.New("at least one --tx-id or a non-empty --tx-ids-file is required")
	}
	if len(merged) > MaxWatchedTxIDs {
		return fmt.Errorf("too many tx ids (%d); cap is %d — use multiple watcher processes",
			len(merged), MaxWatchedTxIDs)
	}
	for _, id := range merged {
		if id == "" {
			return errors.New("empty tx_id in input")
		}
		if strings.Contains(id, "/") {
			return fmt.Errorf("tx_id %q contains '/' (forbidden in URL path component)", id)
		}
		if len(id) > MaxTxIDLen {
			return fmt.Errorf("tx_id %q exceeds %d-byte cap", id, MaxTxIDLen)
		}
	}
	o.TxIDs = merged

	if o.ExitOnResolved && o.IncludePending {
		// Not strictly contradictory, but the combination
		// is a footgun: an operator running --exit-on-resolved
		// expects a fixed-size event stream; --include-pending
		// turns it into a per-poll repeating stream until
		// every tx resolves. Disallow rather than guess.
		return errors.New("--exit-on-resolved and --include-pending are mutually exclusive")
	}
	return nil
}

// mergeTxIDs combines --tx-id flags with the contents of
// --tx-ids-file (or stdin if path == "-"), strips comments
// and blanks, and deduplicates while preserving order. Pure
// function modulo the io.Reader — used in tests with a
// strings.NewReader.
func mergeTxIDs(flagIDs []string, filePath string, stdin io.Reader) ([]string, error) {
	out := make([]string, 0, len(flagIDs))
	seen := make(map[string]struct{}, len(flagIDs))
	add := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range flagIDs {
		add(strings.TrimSpace(id))
	}
	if filePath == "" {
		return out, nil
	}
	var rd io.Reader
	if filePath == "-" {
		if stdin == nil {
			return nil, errors.New("--tx-ids-file=- but stdin is unavailable")
		}
		rd = stdin
	} else {
		f, err := os.Open(filePath)
		if err != nil {
			return nil, fmt.Errorf("open --tx-ids-file: %w", err)
		}
		defer f.Close()
		rd = f
	}
	scanner := bufio.NewScanner(rd)
	// Allow long-line tokens (256-byte tx ids + a buffer
	// for trailing whitespace/comments well under 4 KiB).
	scanner.Buffer(make([]byte, 0, 4<<10), 4<<10)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Allow trailing comments after the id.
		if idx := strings.IndexByte(line, '#'); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		// Allow space-separated extra tokens — take the first.
		if idx := strings.IndexAny(line, " \t"); idx >= 0 {
			line = line[:idx]
		}
		add(line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read --tx-ids-file: %w", err)
	}
	return out, nil
}

// runWatchSlashes is the testable core: takes a normalised
// option set, polls the validator, emits events. Same
// initial-failure-is-fatal posture as watchEnrollments:
// non-nil error only if the very first cycle fails for
// *every* tracked tx id (signals the validator is unreachable
// or v1-only). A partial first cycle (some 200s, some
// transient errors) is treated as success.
func (c *CLI) runWatchSlashes(
	ctx context.Context,
	opts watchSlashOptions,
	stdout, stderr io.Writer,
) error {
	prev, fatalErr := c.fetchSlashSnapshot(ctx, opts.TxIDs)
	if fatalErr != nil {
		return fmt.Errorf("initial snapshot: %w", fatalErr)
	}

	now := time.Now().UTC()
	if opts.IncludePending {
		emitEvents(stdout, stderr, opts.JSON, slashSnapshotAsInitialEvents(prev, now))
	} else {
		// Default first-poll behaviour: emit only the
		// already-resolved receipts (uncommon but possible
		// — operator restarted the watcher, the slash
		// landed earlier). Pending ids are silently
		// tracked and surface their resolved event later.
		emitEvents(stdout, stderr, opts.JSON, slashSnapshotInitialResolvedOnly(prev, now))
	}

	if opts.Once {
		return nil
	}
	if opts.ExitOnResolved && allResolved(prev) {
		return nil
	}

	t := time.NewTicker(opts.Interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			next, fatal := c.fetchSlashSnapshot(ctx, opts.TxIDs)
			tickAt := time.Now().UTC()
			if fatal != nil {
				emitEvents(stdout, stderr, opts.JSON,
					[]WatchEvent{{
						Timestamp: tickAt,
						Kind:      WatchKindError,
						Error:     truncateForLine(fatal.Error(), 200),
					}})
				continue
			}
			events := diffSlashSnapshots(prev, next, tickAt, opts.IncludePending)
			emitEvents(stdout, stderr, opts.JSON, events)
			prev = next
			if opts.ExitOnResolved && allResolved(prev) {
				return nil
			}
		}
	}
}

// fetchSlashSnapshot polls every tracked tx id once and
// returns a tx_id-keyed map of slashStatus. Per-id transient
// errors are tolerated (the entry is omitted from the
// snapshot, so it gets retried next cycle); only a *total*
// failure (every id errored, network hard down) returns
// fatalErr. This matches operator intent: a slow validator
// shouldn't tear down a long-running watcher that's tracking
// dozens of ids.
//
// The 503 "v2 not configured" response IS treated as fatal
// when it hits every id, because that's a configuration error
// the operator wants surfaced loudly — the watcher pointed at
// the wrong node.
func (c *CLI) fetchSlashSnapshot(
	ctx context.Context,
	txIDs []string,
) (map[string]slashStatus, error) {
	out := make(map[string]slashStatus, len(txIDs))
	var lastErr error
	successCount := 0
	for _, id := range txIDs {
		status, err := c.fetchOneSlashReceipt(ctx, id)
		if err != nil {
			lastErr = err
			continue
		}
		out[id] = status
		successCount++
	}
	// Total failure: no id resolved 200 OR 404. Surface as
	// fatal so the caller decides (initial cycle = exit
	// non-zero; subsequent cycles = log + continue).
	if successCount == 0 && lastErr != nil {
		return nil, lastErr
	}
	return out, nil
}

// fetchOneSlashReceipt hits /mining/slash/{tx_id} and
// classifies the response:
//
//   - 200 → slashStatus{Pending: false, Receipt: &wire}
//   - 404 → slashStatus{Pending: true, Receipt: nil}
//   - 503 / 5xx / network error → error
func (c *CLI) fetchOneSlashReceipt(
	ctx context.Context,
	txID string,
) (slashStatus, error) {
	body, status, err := c.getWithStatus(ctx,
		"/mining/slash/"+url.PathEscape(txID))
	if err != nil {
		return slashStatus{}, err
	}
	switch status {
	case 200:
		var w slashReceiptWire
		if err := json.Unmarshal(body, &w); err != nil {
			return slashStatus{}, fmt.Errorf("decode receipt for %s: %w", txID, err)
		}
		// Defensive: the wire view's TxID should match the
		// path component. If it doesn't, surface as a
		// transient error rather than poison the snapshot
		// with a wrong-id record.
		if w.TxID != "" && w.TxID != txID {
			return slashStatus{}, fmt.Errorf("receipt tx_id mismatch: asked for %q, got %q",
				txID, w.TxID)
		}
		w.TxID = txID
		return slashStatus{TxID: txID, Pending: false, Receipt: &w}, nil
	case 404:
		return slashStatus{TxID: txID, Pending: true}, nil
	default:
		return slashStatus{}, fmt.Errorf("validator HTTP %d for %s: %s",
			status, txID, truncateForLine(string(body), 160))
	}
}

// allResolved returns true iff every entry in the snapshot
// has Pending=false and a non-nil Receipt. Driver of
// --exit-on-resolved.
func allResolved(snap map[string]slashStatus) bool {
	if len(snap) == 0 {
		return false
	}
	for _, s := range snap {
		if s.Pending || s.Receipt == nil {
			return false
		}
	}
	return true
}

// diffSlashSnapshots is the pure-function core of the diff
// loop. Computes, for each tx id present in next:
//
//   - prev pending → next resolved: WatchKindSlashResolved
//   - prev resolved → next pending: WatchKindSlashEvicted
//   - prev resolved → next resolved with different outcome:
//     WatchKindSlashOutcomeChange
//   - prev pending → next pending: WatchKindSlashPending
//     (suppressed unless includePending is true)
//   - prev resolved → next resolved with same outcome:
//     no event (steady state)
//
// Tx ids present in prev but missing from next are omitted
// (the per-cycle fetcher dropped them due to a transient
// error; they'll re-appear next cycle).
//
// Ordering: events sorted by tx_id ASC for determinism. One
// event per tx id per cycle.
func diffSlashSnapshots(
	prev, next map[string]slashStatus,
	ts time.Time,
	includePending bool,
) []WatchEvent {
	keys := make([]string, 0, len(next))
	for k := range next {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]WatchEvent, 0, len(keys))
	for _, id := range keys {
		nxt := next[id]
		prv, hadPrev := prev[id]

		switch {
		case nxt.Pending && nxt.Receipt == nil:
			// Currently 404.
			if hadPrev && prv.Receipt != nil {
				// Was resolved, now 404 → eviction.
				out = append(out, WatchEvent{
					Timestamp:   ts,
					Kind:        WatchKindSlashEvicted,
					TxID:        id,
					PrevOutcome: prv.Receipt.Outcome,
				})
				continue
			}
			if includePending {
				out = append(out, WatchEvent{
					Timestamp: ts,
					Kind:      WatchKindSlashPending,
					TxID:      id,
				})
			}
		case nxt.Receipt != nil:
			// Currently resolved.
			if hadPrev && prv.Receipt != nil {
				// Was resolved, still resolved.
				if prv.Receipt.Outcome == nxt.Receipt.Outcome {
					continue // steady state
				}
				out = append(out, WatchEvent{
					Timestamp:   ts,
					Kind:        WatchKindSlashOutcomeChange,
					TxID:        id,
					PrevOutcome: prv.Receipt.Outcome,
					Outcome:     nxt.Receipt.Outcome,
				})
				continue
			}
			// First time resolved (or was previously
			// pending / unobserved). Emit the canonical
			// resolved event.
			out = append(out, slashReceiptToResolvedEvent(*nxt.Receipt, ts))
		}
	}
	return out
}

// slashReceiptToResolvedEvent canonicalises a slashReceiptWire
// into a WatchKindSlashResolved event. Centralised so both the
// initial snapshot path and the diff path agree on the field
// set.
func slashReceiptToResolvedEvent(rec slashReceiptWire, ts time.Time) WatchEvent {
	return WatchEvent{
		Timestamp:               ts,
		Kind:                    WatchKindSlashResolved,
		TxID:                    rec.TxID,
		Outcome:                 rec.Outcome,
		Height:                  rec.Height,
		NodeID:                  rec.NodeID,
		EvidenceKind:            rec.EvidenceKind,
		Slasher:                 rec.Slasher,
		SlashedDust:             rec.SlashedDust,
		RewardedDust:            rec.RewardedDust,
		BurnedDust:              rec.BurnedDust,
		AutoRevoked:             rec.AutoRevoked,
		AutoRevokeRemainingDust: rec.AutoRevokeRemainingDust,
		RejectReason:            rec.RejectReason,
		Error:                   rec.Err,
	}
}

// slashSnapshotAsInitialEvents converts the first poll's
// snapshot into a slice of events: one slash_resolved per
// already-resolved id, one slash_pending per still-pending
// id. Used under --include-pending so the operator sees the
// watcher acknowledge every tracked id on startup.
func slashSnapshotAsInitialEvents(snap map[string]slashStatus, ts time.Time) []WatchEvent {
	keys := make([]string, 0, len(snap))
	for k := range snap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]WatchEvent, 0, len(keys))
	for _, k := range keys {
		s := snap[k]
		if s.Receipt != nil {
			out = append(out, slashReceiptToResolvedEvent(*s.Receipt, ts))
			continue
		}
		out = append(out, WatchEvent{
			Timestamp: ts,
			Kind:      WatchKindSlashPending,
			TxID:      k,
		})
	}
	return out
}

// slashSnapshotInitialResolvedOnly is the default first-poll
// behaviour: emit ONLY the resolved entries (rare but
// possible — operator restarted the watcher mid-cycle).
// Pending ids are silently tracked and surface their resolved
// event in a future cycle.
func slashSnapshotInitialResolvedOnly(snap map[string]slashStatus, ts time.Time) []WatchEvent {
	keys := make([]string, 0, len(snap))
	for k, s := range snap {
		if s.Receipt != nil {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]WatchEvent, 0, len(keys))
	for _, k := range keys {
		out = append(out, slashReceiptToResolvedEvent(*snap[k].Receipt, ts))
	}
	return out
}
