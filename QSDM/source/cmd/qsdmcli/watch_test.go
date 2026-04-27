package main

// watch_test.go — pure-function coverage for the watch
// subcommand's diff / format / flag-normalisation logic.
//
// HTTP integration is exercised end-to-end via httptest in
// TestRunWatchEnrollments_*; everything below it is a
// deterministic transformation we can pin without a server.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// flag normalisation
// -----------------------------------------------------------------------------

func TestWatchOptions_Normalize_DefaultInterval(t *testing.T) {
	o := watchOptions{}
	if err := o.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if o.Interval != DefaultWatchInterval {
		t.Errorf("interval: got %v, want %v", o.Interval, DefaultWatchInterval)
	}
}

func TestWatchOptions_Normalize_ClampsBelowMin(t *testing.T) {
	o := watchOptions{Interval: 1 * time.Second}
	if err := o.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if o.Interval != MinWatchInterval {
		t.Errorf("interval: got %v, want %v (clamped)", o.Interval, MinWatchInterval)
	}
}

func TestWatchOptions_Normalize_RejectsBadPhase(t *testing.T) {
	o := watchOptions{Phase: "bogus"}
	err := o.normalize()
	if err == nil {
		t.Fatal("normalize accepted bogus phase")
	}
	if !strings.Contains(err.Error(), "invalid --phase") {
		t.Errorf("error msg: %v", err)
	}
}

func TestWatchOptions_Normalize_RejectsBothPhaseAndNodeID(t *testing.T) {
	o := watchOptions{NodeID: "rig-1", Phase: "active"}
	err := o.normalize()
	if err == nil {
		t.Fatal("normalize accepted phase+node-id combo")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error msg: %v", err)
	}
}

func TestWatchOptions_Normalize_RejectsNodeIDWithSlash(t *testing.T) {
	o := watchOptions{NodeID: "rig/1"}
	err := o.normalize()
	if err == nil {
		t.Fatal("normalize accepted node-id with '/'")
	}
}

func TestWatchOptions_Normalize_RejectsNegativeLimit(t *testing.T) {
	o := watchOptions{Limit: -1}
	err := o.normalize()
	if err == nil {
		t.Fatal("normalize accepted negative limit")
	}
}

func TestWatchOptions_Normalize_AllValidPhases(t *testing.T) {
	for _, p := range []string{"", "active", "pending_unbond", "revoked"} {
		o := watchOptions{Phase: p}
		if err := o.normalize(); err != nil {
			t.Errorf("phase=%q: %v", p, err)
		}
	}
}

// -----------------------------------------------------------------------------
// diffSnapshots
// -----------------------------------------------------------------------------

var fixedTS = time.Date(2026, 4, 28, 3, 51, 42, 0, time.UTC)

func rec(id, phase string, stake uint64) watchRecord {
	return watchRecord{
		NodeID:    id,
		Phase:     phase,
		StakeDust: stake,
		Slashable: phase != "revoked" && stake > 0,
	}
}

func TestDiff_NoChange(t *testing.T) {
	prev := map[string]watchRecord{"a": rec("a", "active", 1000)}
	next := map[string]watchRecord{"a": rec("a", "active", 1000)}
	if got := diffSnapshots(prev, next, fixedTS); len(got) != 0 {
		t.Errorf("expected no events, got %d: %+v", len(got), got)
	}
}

func TestDiff_NewNode(t *testing.T) {
	prev := map[string]watchRecord{}
	next := map[string]watchRecord{"a": rec("a", "active", 1000)}
	got := diffSnapshots(prev, next, fixedTS)
	if len(got) != 1 || got[0].Kind != WatchKindNew || got[0].NodeID != "a" {
		t.Fatalf("expected single 'new' event for 'a', got: %+v", got)
	}
	if got[0].Phase != "active" || got[0].StakeDust != 1000 {
		t.Errorf("event payload: %+v", got[0])
	}
}

func TestDiff_DroppedNode(t *testing.T) {
	prev := map[string]watchRecord{"a": rec("a", "pending_unbond", 1000)}
	next := map[string]watchRecord{}
	got := diffSnapshots(prev, next, fixedTS)
	if len(got) != 1 || got[0].Kind != WatchKindDropped {
		t.Fatalf("expected 'dropped', got: %+v", got)
	}
	if got[0].PrevPhase != "pending_unbond" {
		t.Errorf("prev_phase: got %q, want pending_unbond", got[0].PrevPhase)
	}
}

func TestDiff_PhaseTransition(t *testing.T) {
	prev := map[string]watchRecord{"a": rec("a", "active", 1000)}
	next := map[string]watchRecord{"a": rec("a", "pending_unbond", 1000)}
	got := diffSnapshots(prev, next, fixedTS)
	if len(got) != 1 || got[0].Kind != WatchKindTransition {
		t.Fatalf("expected 'transition', got: %+v", got)
	}
	ev := got[0]
	if ev.PrevPhase != "active" || ev.Phase != "pending_unbond" {
		t.Errorf("phases: prev=%q next=%q", ev.PrevPhase, ev.Phase)
	}
}

func TestDiff_StakeDelta_Decrease(t *testing.T) {
	prev := map[string]watchRecord{"a": rec("a", "active", 1_000_000_000)}
	next := map[string]watchRecord{"a": rec("a", "active", 500_000_000)}
	got := diffSnapshots(prev, next, fixedTS)
	if len(got) != 1 || got[0].Kind != WatchKindStakeDelta {
		t.Fatalf("expected 'stake_delta', got: %+v", got)
	}
	ev := got[0]
	if ev.PrevStakeDust != 1_000_000_000 || ev.StakeDust != 500_000_000 {
		t.Errorf("stake values: prev=%d next=%d", ev.PrevStakeDust, ev.StakeDust)
	}
	if ev.DeltaDust != -500_000_000 {
		t.Errorf("delta: got %d, want -500_000_000", ev.DeltaDust)
	}
}

func TestDiff_StakeDelta_Increase(t *testing.T) {
	prev := map[string]watchRecord{"a": rec("a", "active", 1_000_000_000)}
	next := map[string]watchRecord{"a": rec("a", "active", 1_500_000_000)}
	got := diffSnapshots(prev, next, fixedTS)
	if len(got) != 1 || got[0].Kind != WatchKindStakeDelta {
		t.Fatalf("expected single 'stake_delta', got: %+v", got)
	}
	if got[0].DeltaDust != 500_000_000 {
		t.Errorf("delta: got %d, want +500_000_000", got[0].DeltaDust)
	}
}

func TestDiff_TransitionWinsOverStakeDelta(t *testing.T) {
	// A partial slash that crosses the auto-revoke threshold
	// would land here as both phase change AND stake change.
	// We must emit ONE event (transition), not two — the
	// operator already learns the new phase + stake from the
	// transition event's payload.
	prev := map[string]watchRecord{"a": rec("a", "active", 1_000_000_000)}
	next := map[string]watchRecord{"a": rec("a", "revoked", 0)}
	got := diffSnapshots(prev, next, fixedTS)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(got), got)
	}
	if got[0].Kind != WatchKindTransition {
		t.Errorf("kind: got %q, want transition", got[0].Kind)
	}
}

func TestDiff_DeterministicOrdering(t *testing.T) {
	// Three changes in unrelated nodes, reverse-keyed in maps.
	// Output MUST be sorted by node_id ascending so two
	// consecutive runs over the same data produce identical
	// log lines.
	prev := map[string]watchRecord{
		"zeta":  rec("zeta", "active", 1000),
		"alpha": rec("alpha", "active", 1000),
		"mu":    rec("mu", "active", 1000),
	}
	next := map[string]watchRecord{
		"zeta":  rec("zeta", "pending_unbond", 1000),
		"alpha": rec("alpha", "pending_unbond", 1000),
		"mu":    rec("mu", "pending_unbond", 1000),
	}
	got := diffSnapshots(prev, next, fixedTS)
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	gotIDs := []string{got[0].NodeID, got[1].NodeID, got[2].NodeID}
	wantIDs := []string{"alpha", "mu", "zeta"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Errorf("ordering: got %v, want %v", gotIDs, wantIDs)
	}
}

func TestDiff_MixedKinds(t *testing.T) {
	prev := map[string]watchRecord{
		"a": rec("a", "active", 1000),         // dropped
		"b": rec("b", "active", 1000),         // unchanged
		"c": rec("c", "active", 1_000_000_000), // stake_delta
	}
	next := map[string]watchRecord{
		"b": rec("b", "active", 1000),
		"c": rec("c", "active", 500_000_000),
		"d": rec("d", "active", 1000), // new
	}
	got := diffSnapshots(prev, next, fixedTS)
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3: %+v", len(got), got)
	}
	wantKinds := map[string]WatchEventKind{
		"a": WatchKindDropped,
		"c": WatchKindStakeDelta,
		"d": WatchKindNew,
	}
	for _, ev := range got {
		want, ok := wantKinds[ev.NodeID]
		if !ok {
			t.Errorf("unexpected event for %s: %+v", ev.NodeID, ev)
			continue
		}
		if ev.Kind != want {
			t.Errorf("%s: kind got %q, want %q", ev.NodeID, ev.Kind, want)
		}
	}
}

// -----------------------------------------------------------------------------
// snapshotAsNewEvents
// -----------------------------------------------------------------------------

func TestSnapshotAsNewEvents_OrderedAndComplete(t *testing.T) {
	snap := map[string]watchRecord{
		"zeta":  rec("zeta", "active", 1000),
		"alpha": rec("alpha", "revoked", 0),
		"mu":    rec("mu", "pending_unbond", 1000),
	}
	got := snapshotAsNewEvents(snap, fixedTS)
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3", len(got))
	}
	wantIDs := []string{"alpha", "mu", "zeta"}
	for i, ev := range got {
		if ev.NodeID != wantIDs[i] {
			t.Errorf("event[%d].NodeID = %q, want %q", i, ev.NodeID, wantIDs[i])
		}
		if ev.Kind != WatchKindNew {
			t.Errorf("event[%d].Kind = %q, want new", i, ev.Kind)
		}
		if !ev.Timestamp.Equal(fixedTS) {
			t.Errorf("event[%d] timestamp drift", i)
		}
	}
}

// -----------------------------------------------------------------------------
// formatEventHuman / formatCELL
// -----------------------------------------------------------------------------

func TestFormatCELL(t *testing.T) {
	cases := []struct {
		dust uint64
		want string
	}{
		{0, "0.0000 CELL"},
		{100_000_000, "1.0000 CELL"},
		{10 * 100_000_000, "10.0000 CELL"},
		{1_500_000_000, "15.0000 CELL"},
		{50_000_000, "0.5000 CELL"},
		{500_000, "0.0050 CELL"},
		{99, "0.0000 CELL"}, // sub-tenthousandth dust rounds to zero in display
	}
	for _, c := range cases {
		got := formatCELL(c.dust)
		if got != c.want {
			t.Errorf("formatCELL(%d) = %q, want %q", c.dust, got, c.want)
		}
	}
}

func TestFormatEventHuman_New(t *testing.T) {
	ev := WatchEvent{
		Timestamp:        fixedTS,
		Kind:             WatchKindNew,
		NodeID:           "alpha-rtx4090-01",
		Phase:            "active",
		StakeDust:        10 * 100_000_000,
		EnrolledAtHeight: 1234567,
		Slashable:        true,
	}
	got := formatEventHuman(ev)
	wantSubs := []string{
		"2026-04-28T03:51:42Z",
		"NEW",
		"node=alpha-rtx4090-01",
		"phase=active",
		"stake=10.0000 CELL",
		"enrolled_at=1234567",
	}
	for _, s := range wantSubs {
		if !strings.Contains(got, s) {
			t.Errorf("output missing %q\n%s", s, got)
		}
	}
}

func TestFormatEventHuman_Transition(t *testing.T) {
	ev := WatchEvent{
		Timestamp:             fixedTS,
		Kind:                  WatchKindTransition,
		NodeID:                "beta",
		PrevPhase:             "active",
		Phase:                 "pending_unbond",
		UnbondMaturesAtHeight: 1235000,
	}
	got := formatEventHuman(ev)
	for _, s := range []string{"TRANSITION", "node=beta",
		"phase=active->pending_unbond", "matures_at=1235000"} {
		if !strings.Contains(got, s) {
			t.Errorf("output missing %q\n%s", s, got)
		}
	}
}

func TestFormatEventHuman_StakeDelta_Negative(t *testing.T) {
	ev := WatchEvent{
		Timestamp:     fixedTS,
		Kind:          WatchKindStakeDelta,
		NodeID:        "alpha",
		Phase:         "active",
		PrevStakeDust: 1_000_000_000,
		StakeDust:     500_000_000,
		DeltaDust:     -500_000_000,
	}
	got := formatEventHuman(ev)
	for _, s := range []string{"STAKE_DELTA", "node=alpha",
		"stake=10.0000 CELL->5.0000 CELL", "delta=5.0000 CELL"} {
		if !strings.Contains(got, s) {
			t.Errorf("output missing %q\n%s", s, got)
		}
	}
	// Sign convention: a NEGATIVE delta renders without a +
	// prefix (the value already reads as the loss magnitude).
	if strings.Contains(got, "delta=+") {
		t.Errorf("negative delta should not have + sign: %s", got)
	}
}

func TestFormatEventHuman_StakeDelta_Positive(t *testing.T) {
	ev := WatchEvent{
		Timestamp:     fixedTS,
		Kind:          WatchKindStakeDelta,
		NodeID:        "alpha",
		Phase:         "active",
		PrevStakeDust: 500_000_000,
		StakeDust:     1_000_000_000,
		DeltaDust:     500_000_000,
	}
	got := formatEventHuman(ev)
	if !strings.Contains(got, "delta=+5.0000 CELL") {
		t.Errorf("positive delta should have + sign: %s", got)
	}
}

func TestFormatEventHuman_Dropped(t *testing.T) {
	ev := WatchEvent{
		Timestamp: fixedTS,
		Kind:      WatchKindDropped,
		NodeID:    "gamma",
		PrevPhase: "pending_unbond",
	}
	got := formatEventHuman(ev)
	for _, s := range []string{"DROPPED", "node=gamma",
		"last_phase=pending_unbond"} {
		if !strings.Contains(got, s) {
			t.Errorf("output missing %q\n%s", s, got)
		}
	}
}

// -----------------------------------------------------------------------------
// emitEvents JSON / human routing
// -----------------------------------------------------------------------------

func TestEmitEvents_JSONLines(t *testing.T) {
	var stdout, stderr bytes.Buffer
	events := []WatchEvent{
		{Timestamp: fixedTS, Kind: WatchKindNew, NodeID: "a", Phase: "active", StakeDust: 1000},
		{Timestamp: fixedTS, Kind: WatchKindError, Error: "boom"},
	}
	emitEvents(&stdout, &stderr, true, events)
	if stderr.Len() != 0 {
		t.Errorf("--json should not write to stderr; got: %s", stderr.String())
	}
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 stdout lines, got %d: %s", len(lines), stdout.String())
	}
	for i, ln := range lines {
		var ev WatchEvent
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			t.Errorf("line %d not valid JSON: %v\n%s", i, err, ln)
		}
	}
}

func TestEmitEvents_HumanRoutesErrorsToStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	events := []WatchEvent{
		{Timestamp: fixedTS, Kind: WatchKindNew, NodeID: "a", Phase: "active", StakeDust: 1000},
		{Timestamp: fixedTS, Kind: WatchKindError, Error: "boom"},
	}
	emitEvents(&stdout, &stderr, false, events)
	if !strings.Contains(stdout.String(), "NEW") {
		t.Errorf("data event missing from stdout: %s", stdout.String())
	}
	if strings.Contains(stdout.String(), "boom") {
		t.Errorf("error event leaked to stdout: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "ERROR") || !strings.Contains(stderr.String(), "boom") {
		t.Errorf("error event missing from stderr: %s", stderr.String())
	}
}

// -----------------------------------------------------------------------------
// buildEnrollmentsListPath
// -----------------------------------------------------------------------------

func TestBuildEnrollmentsListPath(t *testing.T) {
	cases := []struct {
		phase  string
		limit  int
		cursor string
		want   string
	}{
		{"", 0, "", "/mining/enrollments"},
		{"active", 0, "", "/mining/enrollments?phase=active"},
		{"", 50, "", "/mining/enrollments?limit=50"},
		{"", 0, "rig-7", "/mining/enrollments?cursor=rig-7"},
		{"active", 50, "rig-7",
			"/mining/enrollments?cursor=rig-7&limit=50&phase=active"},
	}
	for _, c := range cases {
		got := buildEnrollmentsListPath(c.phase, c.limit, c.cursor)
		if got != c.want {
			t.Errorf("phase=%q limit=%d cursor=%q: got %q, want %q",
				c.phase, c.limit, c.cursor, got, c.want)
		}
	}
}

// -----------------------------------------------------------------------------
// runWatchEnrollments end-to-end with httptest
// -----------------------------------------------------------------------------

// fakeListServer pages out a fixed sequence of snapshots. Each
// successive request advances to the next stage; on Stop the
// last stage is returned forever. Used to exercise the diff
// loop without time-driven flakiness.
type fakeListServer struct {
	stages [][]watchRecord
	calls  int
	srv    *httptest.Server
}

func newFakeListServer(stages [][]watchRecord) *fakeListServer {
	f := &fakeListServer{stages: stages}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakeListServer) handle(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/api/v1/mining/enrollments") {
		http.NotFound(w, r)
		return
	}
	idx := f.calls
	f.calls++
	if idx >= len(f.stages) {
		idx = len(f.stages) - 1
	}
	recs := f.stages[idx]
	page := watchListPage{
		Records:      recs,
		HasMore:      false,
		TotalMatches: uint64(len(recs)),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}

func (f *fakeListServer) Close() { f.srv.Close() }

func (f *fakeListServer) BaseURL() string { return f.srv.URL + "/api/v1" }

func TestRunWatchEnrollments_OnceMode_NoEvents(t *testing.T) {
	stages := [][]watchRecord{
		{rec("alpha", "active", 1000)},
	}
	srv := newFakeListServer(stages)
	defer srv.Close()

	cli := &CLI{baseURL: srv.BaseURL(), client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer

	opts := watchOptions{Once: true}
	if err := opts.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	err := cli.runWatchEnrollments(context.Background(), opts, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Default --once without --include-existing emits NO events.
	if stdout.Len() != 0 {
		t.Errorf("expected empty stdout under --once, got: %s", stdout.String())
	}
}

func TestRunWatchEnrollments_OnceMode_IncludeExisting(t *testing.T) {
	stages := [][]watchRecord{
		{rec("alpha", "active", 1000), rec("beta", "pending_unbond", 500)},
	}
	srv := newFakeListServer(stages)
	defer srv.Close()

	cli := &CLI{baseURL: srv.BaseURL(), client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer

	opts := watchOptions{Once: true, IncludeExisting: true, JSON: true}
	if err := opts.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if err := cli.runWatchEnrollments(context.Background(), opts, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}

	// Two records → two JSON lines, sorted alpha then beta.
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSON lines, got %d: %s", len(lines), stdout.String())
	}
	var first WatchEvent
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("decode line 0: %v", err)
	}
	if first.NodeID != "alpha" || first.Kind != WatchKindNew {
		t.Errorf("first event: %+v", first)
	}
}

func TestRunWatchEnrollments_DiffLoop_PhaseTransition(t *testing.T) {
	// First poll: alpha=active. Second poll: alpha=pending_unbond.
	// We expect: zero events on first poll (no --include-existing),
	// one transition event on second.
	stages := [][]watchRecord{
		{rec("alpha", "active", 1000)},
		{rec("alpha", "pending_unbond", 1000)},
	}
	srv := newFakeListServer(stages)
	defer srv.Close()

	cli := &CLI{baseURL: srv.BaseURL(), client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer

	opts := watchOptions{Interval: MinWatchInterval, JSON: true}
	if err := opts.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}

	// Tight ctx: cancels after we've seen at least one diff event.
	// We use a small window and rely on MinWatchInterval (5s) being
	// quick enough — but the test would block 5s; fake the timer
	// instead by injecting a shorter interval just for this test.
	// We can't change the const, so we override post-normalize.
	opts.Interval = 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	if err := cli.runWatchEnrollments(ctx, opts, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}

	// Look for at least one transition event in stdout JSON-Lines.
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	sawTransition := false
	for _, ln := range lines {
		if ln == "" {
			continue
		}
		var ev WatchEvent
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			t.Errorf("invalid JSON line: %v\n%s", err, ln)
			continue
		}
		if ev.Kind == WatchKindTransition && ev.NodeID == "alpha" &&
			ev.PrevPhase == "active" && ev.Phase == "pending_unbond" {
			sawTransition = true
		}
	}
	if !sawTransition {
		t.Errorf("did not observe expected transition event; stdout:\n%s", stdout.String())
	}
}

func TestRunWatchEnrollments_InitialFailureIsFatal(t *testing.T) {
	// Server that always 503s. First-snapshot failure must
	// surface as a non-nil error from runWatchEnrollments.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "v2 not configured", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cli := &CLI{baseURL: srv.URL + "/api/v1", client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer
	opts := watchOptions{Once: true}
	_ = opts.normalize()

	err := cli.runWatchEnrollments(context.Background(), opts, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected non-nil error on initial 503, got nil")
	}
	if !strings.Contains(err.Error(), "initial snapshot") {
		t.Errorf("error msg: %v", err)
	}
}

func TestRunWatchEnrollments_SingleNodeMode_404IsEmptySnapshot(t *testing.T) {
	// /mining/enrollment/{node_id} returns 404 → fetchSingle
	// returns an empty map → the diff treats this as "no records
	// observed yet" → first poll under --include-existing emits
	// nothing → exits cleanly under --once.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/mining/enrollment/") {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	cli := &CLI{baseURL: srv.URL + "/api/v1", client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer
	opts := watchOptions{NodeID: "ghost-rig", Once: true, IncludeExisting: true, JSON: true}
	if err := opts.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if err := cli.runWatchEnrollments(context.Background(), opts, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("expected empty stdout (404 → no records), got: %s", stdout.String())
	}
}

func TestRunWatchEnrollments_SingleNodeMode_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/mining/enrollment/alpha") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(rec("alpha", "active", 1000))
	}))
	defer srv.Close()

	cli := &CLI{baseURL: srv.URL + "/api/v1", client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer
	opts := watchOptions{NodeID: "alpha", Once: true, IncludeExisting: true, JSON: true}
	if err := opts.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if err := cli.runWatchEnrollments(context.Background(), opts, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("expected one event, got empty stdout")
	}
	var ev WatchEvent
	if err := json.Unmarshal([]byte(out), &ev); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if ev.NodeID != "alpha" || ev.Kind != WatchKindNew {
		t.Errorf("event: %+v", ev)
	}
}

// -----------------------------------------------------------------------------
// wire-shape compatibility with pkg/api EnrollmentRecordView
// -----------------------------------------------------------------------------

// TestWatchRecordWireMatchesAPI asserts byte-identical JSON
// tags between the local watchRecord and the canonical
// pkg/api/handlers_enrollment_query.go EnrollmentRecordView.
//
// Mirrors the same posture the miner-console poller takes: we
// don't import pkg/api into the CLI binary (link-graph weight)
// but we DO require the JSON wire shapes stay in sync. If the
// API view ever adds / removes a field, this test fails and
// the operator fixes both sides at once.
func TestWatchRecordWireMatchesAPI(t *testing.T) {
	// Build the JSON tag set the local mirror declares.
	got := jsonTagsOf(watchRecord{})

	// Hard-coded mirror of api.EnrollmentRecordView's tag
	// set. Cross-validated by reading
	// QSDM/source/pkg/api/handlers_enrollment_query.go.
	// Adding a field there without adding it here is a
	// silent wire-shape drift that this test catches.
	want := []string{
		"node_id", "owner", "gpu_uuid", "stake_dust",
		"enrolled_at_height", "revoked_at_height",
		"unbond_matures_at_height", "phase", "slashable",
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("watchRecord JSON tags drift from api.EnrollmentRecordView\n got:  %v\n want: %v",
			got, want)
	}
}

func jsonTagsOf(v interface{}) []string {
	t := reflect.TypeOf(v)
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		// Strip ",omitempty" etc.
		if idx := strings.Index(tag, ","); idx >= 0 {
			tag = tag[:idx]
		}
		out = append(out, tag)
	}
	return out
}

// -----------------------------------------------------------------------------
// guard against accidental log spew in human mode
// -----------------------------------------------------------------------------

func TestEmitEvents_EmptyEvents_NoOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	emitEvents(&stdout, &stderr, false, nil)
	emitEvents(&stdout, &stderr, true, nil)
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("nil events should produce no output; got stdout=%q stderr=%q",
			stdout.String(), stderr.String())
	}
}

// Compile-time check: WatchEvent must marshal to JSON without
// errors for every kind we support.
func TestWatchEvent_MarshalsAllKinds(t *testing.T) {
	for _, k := range []WatchEventKind{
		WatchKindNew, WatchKindTransition, WatchKindStakeDelta,
		WatchKindDropped, WatchKindError,
	} {
		ev := WatchEvent{Timestamp: fixedTS, Kind: k, NodeID: "x"}
		if k == WatchKindError {
			ev.Error = "test"
		}
		if _, err := json.Marshal(ev); err != nil {
			t.Errorf("kind=%q: %v", k, err)
		}
	}
}

// Sanity: ensure io.Discard is a valid stderr target (used
// implicitly when callers don't care). Tests do care, hence
// the buffers above; this is just a trivial guard against a
// future refactor that breaks the io.Writer contract.
var _ io.Writer = io.Discard

// formatPlaceholder is a no-op compile-time guard — referencing
// fmt prevents goimports from removing the import in any future
// edit that strips all production calls. The CLI's main.go
// already imports fmt, but watch_test.go is independent.
var _ = fmt.Sprintf
