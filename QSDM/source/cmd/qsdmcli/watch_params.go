package main

// watch_params.go — third sibling of watch_enrollments and
// watch_slashes. Polls /api/v1/governance/params and emits one
// WatchEvent per state transition across consecutive snapshots.
//
// Why a separate watcher rather than overloading
// watch_enrollments / watch_slashes:
//
//   - The wire shape is fundamentally different: a snapshot is
//     a {param-name: value} active map plus a list of pending
//     changes plus an authority list. Diffing it is closer to
//     the slash-receipt watcher (key-driven 200/404 lifecycle)
//     than the enrollment-list watcher (paginated registry
//     walk), but the events it emits (staged / superseded /
//     activated / removed / authorities_changed) are unique
//     to governance.
//   - The endpoint is single-shot per cycle (one HTTP GET, all
//     params returned), so no fan-out logic; the diff core is
//     the entire complexity.
//   - Operator workflow is "I just submitted a proposal, did
//     it land?" — symmetrical to slash-receipt polling but
//     with a different terminal event ("activated" vs
//     "resolved").
//
// Events use the shared WatchEvent envelope so JSON-Lines
// consumers can decode every watcher's output with one struct.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"
)

// watchParamsOptions is the parsed flag set for
// `qsdmcli watch params`. Held as a struct so the snapshot +
// diff core can be unit-tested without re-implementing flag
// parsing.
type watchParamsOptions struct {
	Interval        time.Duration
	Once            bool
	JSON            bool
	IncludeExisting bool
	ParamFilter     string
}

// govParamsSnapshot is the minimal post-decode shape the diff
// core consumes. Mirrors api.GovernanceParamsView field-for-
// field but with the canonical wire-shape names (kept
// byte-identical to the API view; tests pin this).
//
// Use govParamsRemoteView from gov_helper.go as the on-the-wire
// decoder, then collapse it into this convenience shape so the
// diff loop has indexed access.
type govParamsSnapshot struct {
	Active            map[string]uint64
	Pending           map[string]govParamsPendingWire
	Authorities       []string
	GovernanceEnabled bool
}

// watchParams parses flags, fetches an initial snapshot, and
// enters the diff loop. Same shape as watchEnrollments /
// watchSlashes: SIGINT/SIGTERM exit returns nil; first-poll
// fatal returns the error so the operator notices a typo'd
// URL or an unreachable validator.
func (c *CLI) watchParams(args []string) error {
	fs := flag.NewFlagSet("watch params", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		interval = fs.Duration("interval", DefaultWatchInterval,
			"polling cadence (clamped: ≥5s, default 30s)")
		once = fs.Bool("once", false,
			"emit a single snapshot and exit (no diff loop)")
		jsonOut = fs.Bool("json", false,
			"emit JSON-Lines (one event per line) instead of human-formatted lines")
		includeExisting = fs.Bool("include-existing", false,
			"on the first poll, emit a 'param_staged' event for every existing pending change")
		paramFilter = fs.String("param", "",
			"limit emitted events to a single registered param name (default: all)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	opts := watchParamsOptions{
		Interval:        *interval,
		Once:            *once,
		JSON:            *jsonOut,
		IncludeExisting: *includeExisting,
		ParamFilter:     *paramFilter,
	}
	if err := opts.normalize(); err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	return c.runWatchParams(ctx, opts, os.Stdout, os.Stderr)
}

// normalize validates and clamps option fields.
func (o *watchParamsOptions) normalize() error {
	if o.Interval == 0 {
		o.Interval = DefaultWatchInterval
	}
	if o.Interval > 0 && o.Interval < MinWatchInterval {
		o.Interval = MinWatchInterval
	}
	if o.ParamFilter != "" && len(o.ParamFilter) > 64 {
		return fmt.Errorf("--param=%q exceeds 64-byte cap (chainparams names are short snake_case)",
			o.ParamFilter)
	}
	return nil
}

// runWatchParams is the testable core of watchParams. Same
// semantics as runWatchEnrollments: the very first snapshot
// failure is fatal (likely typo'd URL or v1-only validator);
// subsequent failures emit a WatchKindError event and the loop
// continues.
func (c *CLI) runWatchParams(
	ctx context.Context,
	opts watchParamsOptions,
	stdout, stderr io.Writer,
) error {
	prev, err := c.fetchGovParamsSnapshot(ctx)
	if err != nil {
		return fmt.Errorf("initial snapshot: %w", err)
	}

	now := time.Now().UTC()
	if opts.IncludeExisting {
		emitEvents(stdout, stderr, opts.JSON,
			govParamsSnapshotAsInitialEvents(prev, now, opts.ParamFilter))
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
			next, err := c.fetchGovParamsSnapshot(ctx)
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
			events := diffGovParamsSnapshots(prev, next, tickAt, opts.ParamFilter)
			emitEvents(stdout, stderr, opts.JSON, events)
			prev = next
		}
	}
}

// fetchGovParamsSnapshot polls /governance/params, decodes the
// wire shape, and collapses it into the diff-loop shape.
//
// 503 from the validator (governance not configured) is
// surfaced as an error here on purpose: a watcher pointed at a
// v1-only node should fail loudly on the very first cycle,
// matching watch_enrollments / watch_slashes behaviour.
func (c *CLI) fetchGovParamsSnapshot(ctx context.Context) (govParamsSnapshot, error) {
	body, status, err := c.getWithStatus(ctx, "/governance/params")
	if err != nil {
		return govParamsSnapshot{}, err
	}
	if status != 200 {
		return govParamsSnapshot{}, fmt.Errorf(
			"validator HTTP %d on /governance/params: %s",
			status, truncateForLine(string(body), 160))
	}
	var view govParamsRemoteView
	if err := json.Unmarshal(body, &view); err != nil {
		return govParamsSnapshot{}, fmt.Errorf("decode /governance/params: %w", err)
	}
	out := govParamsSnapshot{
		Active:            map[string]uint64{},
		Pending:           map[string]govParamsPendingWire{},
		Authorities:       append([]string(nil), view.Authorities...),
		GovernanceEnabled: view.GovernanceEnabled,
	}
	for k, v := range view.Active {
		out.Active[k] = v
	}
	for _, p := range view.Pending {
		out.Pending[p.Param] = p
	}
	sort.Strings(out.Authorities)
	return out, nil
}

// govParamsSnapshotAsInitialEvents synthesises a `param_staged`
// event per pending change in the initial snapshot. Used for
// --include-existing on the first poll. Sorted by param name
// for deterministic output.
func govParamsSnapshotAsInitialEvents(
	snap govParamsSnapshot,
	ts time.Time,
	paramFilter string,
) []WatchEvent {
	keys := make([]string, 0, len(snap.Pending))
	for k := range snap.Pending {
		if paramFilter != "" && k != paramFilter {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]WatchEvent, 0, len(keys))
	for _, k := range keys {
		p := snap.Pending[k]
		out = append(out, WatchEvent{
			Timestamp:              ts,
			Kind:                   WatchKindParamStaged,
			Param:                  k,
			ActiveValue:            snap.Active[k],
			PendingValue:           p.Value,
			PendingEffectiveHeight: p.EffectiveHeight,
			Authority:              p.Authority,
			Memo:                   p.Memo,
		})
	}
	return out
}

// diffGovParamsSnapshots is the pure-function core of the
// params diff loop. For each parameter present in either
// snapshot it emits zero or one event:
//
//   - active changed: WatchKindParamActivated (always wins
//     over staging events for the same param in the same
//     cycle — a change that activated also vacated its
//     pending slot, which would otherwise look like
//     ParamRemoved).
//   - active unchanged + new pending: WatchKindParamStaged.
//   - active unchanged + pending value changed:
//     WatchKindParamSuperseded.
//   - active unchanged + pending vacated without activation:
//     WatchKindParamRemoved.
//   - active unchanged + pending unchanged: no event.
//
// Plus, exactly once per cycle: WatchKindParamAuthoritiesChanged
// when the authority list differs across snapshots.
//
// Ordering: events sorted by param ASC for determinism;
// authorities event sorted last so an operator scrolling can
// see "param events first, governance config last".
func diffGovParamsSnapshots(
	prev, next govParamsSnapshot,
	ts time.Time,
	paramFilter string,
) []WatchEvent {
	allParams := map[string]struct{}{}
	for k := range prev.Active {
		allParams[k] = struct{}{}
	}
	for k := range next.Active {
		allParams[k] = struct{}{}
	}
	for k := range prev.Pending {
		allParams[k] = struct{}{}
	}
	for k := range next.Pending {
		allParams[k] = struct{}{}
	}
	keys := make([]string, 0, len(allParams))
	for k := range allParams {
		if paramFilter != "" && k != paramFilter {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]WatchEvent, 0, len(keys)+1)
	for _, k := range keys {
		prevActive, hadPrevActive := prev.Active[k]
		nextActive, hasNextActive := next.Active[k]
		prevPending, hadPrevPending := prev.Pending[k]
		nextPending, hasNextPending := next.Pending[k]

		// Active changed → activated event. Wins over any
		// pending-slot diff for this param in this cycle.
		if hadPrevActive && hasNextActive && prevActive != nextActive {
			out = append(out, WatchEvent{
				Timestamp:       ts,
				Kind:            WatchKindParamActivated,
				Param:           k,
				ActiveValue:     nextActive,
				PrevActiveValue: prevActive,
			})
			continue
		}
		// Brand-new active key (snapshot grew a registry
		// entry between polls; rare and means a binary
		// upgrade between polls). Treat as activated from
		// implicit prior == default; surface PrevActiveValue
		// as 0 so consumers can detect this corner case.
		if !hadPrevActive && hasNextActive {
			// Only emit if there's no concurrent staged event
			// to attribute the value to (we can't tell whose
			// proposal landed). Skip silently if no pending
			// either way to avoid noise on first connect to
			// an upgraded validator.
			if hadPrevPending || hasNextPending {
				out = append(out, WatchEvent{
					Timestamp:   ts,
					Kind:        WatchKindParamActivated,
					Param:       k,
					ActiveValue: nextActive,
				})
			}
			continue
		}

		// Active unchanged → branch on pending diff.
		switch {
		case !hadPrevPending && hasNextPending:
			// Staged.
			out = append(out, WatchEvent{
				Timestamp:              ts,
				Kind:                   WatchKindParamStaged,
				Param:                  k,
				ActiveValue:            nextActive,
				PendingValue:           nextPending.Value,
				PendingEffectiveHeight: nextPending.EffectiveHeight,
				Authority:              nextPending.Authority,
				Memo:                   nextPending.Memo,
			})
		case hadPrevPending && hasNextPending:
			// Same param had a pending in both snapshots.
			// If the value or effective_height changed, a
			// supersede happened. If both are identical,
			// steady state — no event.
			if prevPending.Value != nextPending.Value ||
				prevPending.EffectiveHeight != nextPending.EffectiveHeight {
				out = append(out, WatchEvent{
					Timestamp:              ts,
					Kind:                   WatchKindParamSuperseded,
					Param:                  k,
					ActiveValue:            nextActive,
					PendingValue:           nextPending.Value,
					PendingEffectiveHeight: nextPending.EffectiveHeight,
					PrevPendingValue:       prevPending.Value,
					Authority:              nextPending.Authority,
					Memo:                   nextPending.Memo,
				})
			}
		case hadPrevPending && !hasNextPending:
			// Pending vacated. If active didn't change above
			// then this is a removal-without-activation —
			// rare and warrants a defensive event so the
			// operator notices.
			out = append(out, WatchEvent{
				Timestamp:        ts,
				Kind:             WatchKindParamRemoved,
				Param:            k,
				ActiveValue:      nextActive,
				PrevPendingValue: prevPending.Value,
			})
		}
	}

	// Authority-list diff. Compare sorted slices for set
	// equality; emit one event per cycle when they diverge.
	added, removed := diffAuthorityLists(prev.Authorities, next.Authorities)
	if len(added) > 0 || len(removed) > 0 {
		out = append(out, WatchEvent{
			Timestamp:          ts,
			Kind:               WatchKindParamAuthoritiesChanged,
			AuthoritiesAdded:   added,
			AuthoritiesRemoved: removed,
		})
	}
	return out
}

// diffAuthorityLists returns (added, removed) for two sorted
// authority slices. Pure function for unit testing.
func diffAuthorityLists(prev, next []string) (added, removed []string) {
	pset := make(map[string]struct{}, len(prev))
	for _, a := range prev {
		pset[a] = struct{}{}
	}
	nset := make(map[string]struct{}, len(next))
	for _, a := range next {
		nset[a] = struct{}{}
	}
	for a := range nset {
		if _, ok := pset[a]; !ok {
			added = append(added, a)
		}
	}
	for a := range pset {
		if _, ok := nset[a]; !ok {
			removed = append(removed, a)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}
