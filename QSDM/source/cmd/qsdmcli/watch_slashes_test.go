package main

// watch_slashes_test.go — coverage for the slash-receipt watcher.
// Mirrors watch_test.go's structure: pure-function diff coverage,
// flag normalisation, format/CELL helpers, plus end-to-end
// httptest scenarios.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// flag normalisation + tx-id merge
// -----------------------------------------------------------------------------

func TestWatchSlashOptions_Normalize_RequiresAtLeastOneTxID(t *testing.T) {
	o := watchSlashOptions{}
	if err := o.normalize(); err == nil {
		t.Fatal("normalize accepted zero tx ids")
	} else if !strings.Contains(err.Error(), "at least one") {
		t.Errorf("error msg: %v", err)
	}
}

func TestWatchSlashOptions_Normalize_RejectsSlashInTxID(t *testing.T) {
	o := watchSlashOptions{TxIDs: []string{"tx/with/slash"}}
	err := o.normalize()
	if err == nil {
		t.Fatal("normalize accepted '/' in tx_id")
	}
	if !strings.Contains(err.Error(), "'/'") {
		t.Errorf("error msg: %v", err)
	}
}

func TestWatchSlashOptions_Normalize_RejectsOversizedTxID(t *testing.T) {
	huge := strings.Repeat("x", MaxTxIDLen+1)
	o := watchSlashOptions{TxIDs: []string{huge}}
	if err := o.normalize(); err == nil {
		t.Fatal("normalize accepted oversized tx_id")
	}
}

func TestWatchSlashOptions_Normalize_RejectsTooManyTxIDs(t *testing.T) {
	ids := make([]string, MaxWatchedTxIDs+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("tx-%d", i)
	}
	o := watchSlashOptions{TxIDs: ids}
	if err := o.normalize(); err == nil {
		t.Fatal("normalize accepted >MaxWatchedTxIDs ids")
	}
}

func TestWatchSlashOptions_Normalize_ClampsBelowMin(t *testing.T) {
	o := watchSlashOptions{TxIDs: []string{"tx-1"}, Interval: 1 * time.Second}
	if err := o.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if o.Interval != MinWatchInterval {
		t.Errorf("interval: got %v, want %v", o.Interval, MinWatchInterval)
	}
}

func TestWatchSlashOptions_Normalize_DefaultsInterval(t *testing.T) {
	o := watchSlashOptions{TxIDs: []string{"tx-1"}}
	if err := o.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if o.Interval != DefaultWatchInterval {
		t.Errorf("interval: got %v, want %v", o.Interval, DefaultWatchInterval)
	}
}

func TestWatchSlashOptions_Normalize_RejectsExitOnResolvedWithIncludePending(t *testing.T) {
	o := watchSlashOptions{
		TxIDs:          []string{"tx-1"},
		IncludePending: true,
		ExitOnResolved: true,
	}
	if err := o.normalize(); err == nil {
		t.Fatal("normalize accepted footgun combo")
	}
}

func TestMergeTxIDs_FlagsAndFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ids.txt")
	body := strings.Join([]string{
		"# this is a comment",
		"",
		"tx-from-file-1",
		"tx-from-file-2  # trailing comment",
		"tx-from-file-1", // dup with itself
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write ids file: %v", err)
	}
	got, err := mergeTxIDs([]string{"tx-flag-1", "tx-flag-2", "tx-from-file-1"}, path, nil)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	want := []string{"tx-flag-1", "tx-flag-2", "tx-from-file-1", "tx-from-file-2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merged: got %v, want %v", got, want)
	}
}

func TestMergeTxIDs_StdinDash(t *testing.T) {
	got, err := mergeTxIDs(nil, "-", strings.NewReader("tx-1\ntx-2\n#comment\ntx-1\n"))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	want := []string{"tx-1", "tx-2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merged: got %v, want %v", got, want)
	}
}

func TestMergeTxIDs_NoFile_ReturnsFlagsOnly(t *testing.T) {
	got, err := mergeTxIDs([]string{"tx-a"}, "", nil)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"tx-a"}) {
		t.Errorf("got %v", got)
	}
}

// -----------------------------------------------------------------------------
// allResolved
// -----------------------------------------------------------------------------

func TestAllResolved_EmptySnapshotIsFalse(t *testing.T) {
	if allResolved(map[string]slashStatus{}) {
		t.Error("empty snapshot reported as resolved; want false")
	}
}

func TestAllResolved_AllPendingIsFalse(t *testing.T) {
	snap := map[string]slashStatus{
		"a": {TxID: "a", Pending: true},
		"b": {TxID: "b", Pending: true},
	}
	if allResolved(snap) {
		t.Error("all-pending reported resolved")
	}
}

func TestAllResolved_MixedIsFalse(t *testing.T) {
	snap := map[string]slashStatus{
		"a": {TxID: "a", Pending: true},
		"b": {TxID: "b", Receipt: &slashReceiptWire{TxID: "b", Outcome: "applied"}},
	}
	if allResolved(snap) {
		t.Error("mixed reported resolved")
	}
}

func TestAllResolved_AllResolvedIsTrue(t *testing.T) {
	snap := map[string]slashStatus{
		"a": {TxID: "a", Receipt: &slashReceiptWire{TxID: "a", Outcome: "applied"}},
		"b": {TxID: "b", Receipt: &slashReceiptWire{TxID: "b", Outcome: "rejected"}},
	}
	if !allResolved(snap) {
		t.Error("all-resolved reported not-resolved")
	}
}

// -----------------------------------------------------------------------------
// diffSlashSnapshots
// -----------------------------------------------------------------------------

var slashTS = time.Date(2026, 4, 28, 4, 20, 0, 0, time.UTC)

func pendingStatus(id string) slashStatus {
	return slashStatus{TxID: id, Pending: true}
}

func resolvedStatus(id, outcome string, height uint64) slashStatus {
	return slashStatus{
		TxID: id,
		Receipt: &slashReceiptWire{
			TxID:    id,
			Outcome: outcome,
			Height:  height,
		},
	}
}

func TestDiffSlashes_PendingToResolved(t *testing.T) {
	prev := map[string]slashStatus{"tx-1": pendingStatus("tx-1")}
	next := map[string]slashStatus{"tx-1": resolvedStatus("tx-1", "applied", 42)}
	got := diffSlashSnapshots(prev, next, slashTS, false)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got), got)
	}
	if got[0].Kind != WatchKindSlashResolved || got[0].TxID != "tx-1" ||
		got[0].Outcome != "applied" || got[0].Height != 42 {
		t.Errorf("event: %+v", got[0])
	}
}

func TestDiffSlashes_ResolvedToPending_IsEviction(t *testing.T) {
	prev := map[string]slashStatus{"tx-1": resolvedStatus("tx-1", "applied", 42)}
	next := map[string]slashStatus{"tx-1": pendingStatus("tx-1")}
	got := diffSlashSnapshots(prev, next, slashTS, false)
	if len(got) != 1 || got[0].Kind != WatchKindSlashEvicted {
		t.Fatalf("expected slash_evicted, got: %+v", got)
	}
	if got[0].PrevOutcome != "applied" {
		t.Errorf("prev_outcome: got %q, want applied", got[0].PrevOutcome)
	}
}

func TestDiffSlashes_OutcomeChange(t *testing.T) {
	prev := map[string]slashStatus{"tx-1": resolvedStatus("tx-1", "applied", 42)}
	next := map[string]slashStatus{"tx-1": resolvedStatus("tx-1", "rejected", 42)}
	got := diffSlashSnapshots(prev, next, slashTS, false)
	if len(got) != 1 || got[0].Kind != WatchKindSlashOutcomeChange {
		t.Fatalf("expected slash_outcome_change, got: %+v", got)
	}
	if got[0].PrevOutcome != "applied" || got[0].Outcome != "rejected" {
		t.Errorf("outcomes: %+v", got[0])
	}
}

func TestDiffSlashes_PendingToPending_NoEventByDefault(t *testing.T) {
	prev := map[string]slashStatus{"tx-1": pendingStatus("tx-1")}
	next := map[string]slashStatus{"tx-1": pendingStatus("tx-1")}
	got := diffSlashSnapshots(prev, next, slashTS, false)
	if len(got) != 0 {
		t.Errorf("expected no events without --include-pending, got: %+v", got)
	}
}

func TestDiffSlashes_PendingToPending_EmitsWhenIncluded(t *testing.T) {
	prev := map[string]slashStatus{"tx-1": pendingStatus("tx-1")}
	next := map[string]slashStatus{"tx-1": pendingStatus("tx-1")}
	got := diffSlashSnapshots(prev, next, slashTS, true)
	if len(got) != 1 || got[0].Kind != WatchKindSlashPending {
		t.Fatalf("expected slash_pending, got: %+v", got)
	}
}

func TestDiffSlashes_ResolvedSteadyState_NoEvent(t *testing.T) {
	prev := map[string]slashStatus{"tx-1": resolvedStatus("tx-1", "applied", 42)}
	next := map[string]slashStatus{"tx-1": resolvedStatus("tx-1", "applied", 42)}
	got := diffSlashSnapshots(prev, next, slashTS, true)
	if len(got) != 0 {
		t.Errorf("steady-state resolved should emit nothing, got: %+v", got)
	}
}

func TestDiffSlashes_DeterministicOrdering(t *testing.T) {
	prev := map[string]slashStatus{
		"zeta":  pendingStatus("zeta"),
		"alpha": pendingStatus("alpha"),
		"mu":    pendingStatus("mu"),
	}
	next := map[string]slashStatus{
		"zeta":  resolvedStatus("zeta", "applied", 1),
		"alpha": resolvedStatus("alpha", "rejected", 1),
		"mu":    resolvedStatus("mu", "applied", 1),
	}
	got := diffSlashSnapshots(prev, next, slashTS, false)
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	gotIDs := []string{got[0].TxID, got[1].TxID, got[2].TxID}
	want := []string{"alpha", "mu", "zeta"}
	if !reflect.DeepEqual(gotIDs, want) {
		t.Errorf("ordering: got %v, want %v", gotIDs, want)
	}
}

func TestDiffSlashes_PrevMissingFromNext_Skipped(t *testing.T) {
	// Per-cycle fetcher dropped tx-2 due to a transient
	// HTTP error; tx-2 is in prev but not in next. The
	// diff must NOT emit anything for tx-2 — a transient
	// fetch failure is not a state change.
	prev := map[string]slashStatus{
		"tx-1": pendingStatus("tx-1"),
		"tx-2": resolvedStatus("tx-2", "applied", 42),
	}
	next := map[string]slashStatus{
		"tx-1": resolvedStatus("tx-1", "applied", 42),
		// tx-2 missing
	}
	got := diffSlashSnapshots(prev, next, slashTS, false)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got), got)
	}
	if got[0].TxID != "tx-1" {
		t.Errorf("emitted unexpected event for tx %q", got[0].TxID)
	}
}

// -----------------------------------------------------------------------------
// slashReceiptToResolvedEvent + initial-snapshot helpers
// -----------------------------------------------------------------------------

func TestSlashReceiptToResolvedEvent_AppliedFields(t *testing.T) {
	rec := slashReceiptWire{
		TxID:                    "tx-applied",
		Outcome:                 "applied",
		Height:                  42,
		Slasher:                 "alice",
		NodeID:                  "rig-77",
		EvidenceKind:            "forged-attestation",
		SlashedDust:             500_000_000,
		RewardedDust:            10_000_000,
		BurnedDust:              490_000_000,
		AutoRevoked:             true,
		AutoRevokeRemainingDust: 100_000_000,
	}
	ev := slashReceiptToResolvedEvent(rec, slashTS)
	if ev.Kind != WatchKindSlashResolved {
		t.Errorf("kind: got %q", ev.Kind)
	}
	if ev.TxID != "tx-applied" || ev.Outcome != "applied" ||
		ev.Height != 42 || ev.Slasher != "alice" ||
		ev.NodeID != "rig-77" || ev.EvidenceKind != "forged-attestation" {
		t.Errorf("identity fields: %+v", ev)
	}
	if ev.SlashedDust != 500_000_000 || ev.RewardedDust != 10_000_000 ||
		ev.BurnedDust != 490_000_000 {
		t.Errorf("financial fields: %+v", ev)
	}
	if !ev.AutoRevoked || ev.AutoRevokeRemainingDust != 100_000_000 {
		t.Errorf("auto-revoke fields: %+v", ev)
	}
}

func TestSlashReceiptToResolvedEvent_RejectedFields(t *testing.T) {
	rec := slashReceiptWire{
		TxID:         "tx-rejected",
		Outcome:      "rejected",
		Height:       7,
		Slasher:      "bob",
		NodeID:       "rig-99",
		EvidenceKind: "double-mining",
		RejectReason: "verifier_failed",
		Err:          "verifier said no",
	}
	ev := slashReceiptToResolvedEvent(rec, slashTS)
	if ev.RejectReason != "verifier_failed" || ev.Error != "verifier said no" {
		t.Errorf("rejected fields: %+v", ev)
	}
	// Applied-path fields must be zero.
	if ev.SlashedDust != 0 || ev.RewardedDust != 0 || ev.BurnedDust != 0 ||
		ev.AutoRevoked || ev.AutoRevokeRemainingDust != 0 {
		t.Errorf("applied-path fields leaked into rejected event: %+v", ev)
	}
}

func TestSlashSnapshotInitialResolvedOnly_Filters(t *testing.T) {
	snap := map[string]slashStatus{
		"a": pendingStatus("a"),
		"b": resolvedStatus("b", "applied", 1),
		"c": pendingStatus("c"),
		"d": resolvedStatus("d", "rejected", 2),
	}
	got := slashSnapshotInitialResolvedOnly(snap, slashTS)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	gotIDs := []string{got[0].TxID, got[1].TxID}
	if !reflect.DeepEqual(gotIDs, []string{"b", "d"}) {
		t.Errorf("filtered ids: got %v, want [b d]", gotIDs)
	}
}

func TestSlashSnapshotAsInitialEvents_IncludesBoth(t *testing.T) {
	snap := map[string]slashStatus{
		"a": pendingStatus("a"),
		"b": resolvedStatus("b", "applied", 1),
	}
	got := slashSnapshotAsInitialEvents(snap, slashTS)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	// Sorted alpha, beta.
	if got[0].TxID != "a" || got[0].Kind != WatchKindSlashPending {
		t.Errorf("event[0]: %+v", got[0])
	}
	if got[1].TxID != "b" || got[1].Kind != WatchKindSlashResolved {
		t.Errorf("event[1]: %+v", got[1])
	}
}

// -----------------------------------------------------------------------------
// formatEventHuman — slash kinds
// -----------------------------------------------------------------------------

func TestFormatEventHuman_SlashResolved_Applied(t *testing.T) {
	ev := WatchEvent{
		Timestamp:    slashTS,
		Kind:         WatchKindSlashResolved,
		TxID:         "tx-applied",
		Outcome:      "applied",
		Height:       42,
		NodeID:       "rig-77",
		EvidenceKind: "forged-attestation",
		SlashedDust:  500_000_000,
		RewardedDust: 10_000_000,
		BurnedDust:   490_000_000,
		AutoRevoked:  true,
	}
	got := formatEventHuman(ev)
	for _, s := range []string{
		"2026-04-28T04:20:00Z",
		"SLASH_RESOLVED",
		"tx=tx-applied",
		"outcome=applied",
		"node=rig-77",
		"kind=forged-attestation",
		"height=42",
		"slashed=5.0000 CELL",
		"rewarded=0.1000 CELL",
		"burned=4.9000 CELL",
		"auto_revoked=true",
	} {
		if !strings.Contains(got, s) {
			t.Errorf("output missing %q\n%s", s, got)
		}
	}
}

func TestFormatEventHuman_SlashResolved_Rejected(t *testing.T) {
	ev := WatchEvent{
		Timestamp:    slashTS,
		Kind:         WatchKindSlashResolved,
		TxID:         "tx-rejected",
		Outcome:      "rejected",
		Height:       7,
		NodeID:       "rig-99",
		EvidenceKind: "double-mining",
		RejectReason: "verifier_failed",
		Error:        "verifier said no",
	}
	got := formatEventHuman(ev)
	for _, s := range []string{
		"SLASH_RESOLVED",
		"outcome=rejected",
		"reason=verifier_failed",
		"err=verifier said no",
	} {
		if !strings.Contains(got, s) {
			t.Errorf("output missing %q\n%s", s, got)
		}
	}
	// Applied-path strings must NOT appear.
	for _, s := range []string{"slashed=", "rewarded=", "burned="} {
		if strings.Contains(got, s) {
			t.Errorf("rejected event leaked applied-path field %q\n%s", s, got)
		}
	}
}

func TestFormatEventHuman_SlashPending(t *testing.T) {
	ev := WatchEvent{Timestamp: slashTS, Kind: WatchKindSlashPending, TxID: "tx-1"}
	got := formatEventHuman(ev)
	for _, s := range []string{"SLASH_PENDING", "tx=tx-1"} {
		if !strings.Contains(got, s) {
			t.Errorf("output missing %q\n%s", s, got)
		}
	}
}

func TestFormatEventHuman_SlashEvicted(t *testing.T) {
	ev := WatchEvent{
		Timestamp:   slashTS,
		Kind:        WatchKindSlashEvicted,
		TxID:        "tx-old",
		PrevOutcome: "applied",
	}
	got := formatEventHuman(ev)
	for _, s := range []string{"SLASH_EVICTED", "tx=tx-old", "last_outcome=applied"} {
		if !strings.Contains(got, s) {
			t.Errorf("output missing %q\n%s", s, got)
		}
	}
}

func TestFormatEventHuman_SlashOutcomeChange(t *testing.T) {
	ev := WatchEvent{
		Timestamp:   slashTS,
		Kind:        WatchKindSlashOutcomeChange,
		TxID:        "tx-flip",
		PrevOutcome: "applied",
		Outcome:     "rejected",
	}
	got := formatEventHuman(ev)
	for _, s := range []string{
		"SLASH_OUTCOME_CHANGE",
		"tx=tx-flip",
		"outcome=applied->rejected",
	} {
		if !strings.Contains(got, s) {
			t.Errorf("output missing %q\n%s", s, got)
		}
	}
}

// -----------------------------------------------------------------------------
// runWatchSlashes end-to-end via httptest
// -----------------------------------------------------------------------------

// fakeSlashServer responds per-tx-id from a programmable map.
// Setting Receipt to nil for a tx returns 404; setting it
// returns the encoded body. Each call increments the
// `calls` counter so tests can sequence stage transitions.
type fakeSlashServer struct {
	stages []map[string]*slashReceiptWire // per-cycle tx -> receipt|nil
	calls  int
	srv    *httptest.Server
}

func newFakeSlashServer(stages []map[string]*slashReceiptWire) *fakeSlashServer {
	f := &fakeSlashServer{stages: stages}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakeSlashServer) handle(w http.ResponseWriter, r *http.Request) {
	const prefix = "/api/v1/mining/slash/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, prefix)

	// Each id in a stage returns 200/404 based on the
	// stage map. After every id in the stage has been
	// handled once, advance to the next stage. This is
	// approximate (the test interleaves multi-id polls)
	// so we simply advance every time len(stage) requests
	// have come in.
	stageIdx := 0
	if len(f.stages) > 1 {
		stageIdx = f.calls / len(f.stages[0])
		if stageIdx >= len(f.stages) {
			stageIdx = len(f.stages) - 1
		}
	}
	f.calls++
	stage := f.stages[stageIdx]

	rec, ok := stage[id]
	if !ok || rec == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rec)
}

func (f *fakeSlashServer) Close()           { f.srv.Close() }
func (f *fakeSlashServer) BaseURL() string  { return f.srv.URL + "/api/v1" }

func TestRunWatchSlashes_OnceMode_Empty(t *testing.T) {
	// Every tracked id is 404. --once with no flags emits
	// nothing and exits cleanly.
	srv := newFakeSlashServer([]map[string]*slashReceiptWire{
		{"tx-1": nil},
	})
	defer srv.Close()

	cli := &CLI{baseURL: srv.BaseURL(), client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer

	opts := watchSlashOptions{TxIDs: []string{"tx-1"}, Once: true}
	if err := opts.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if err := cli.runWatchSlashes(context.Background(), opts, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("expected empty stdout (default + all-pending), got: %s", stdout.String())
	}
}

func TestRunWatchSlashes_OnceMode_IncludePending(t *testing.T) {
	srv := newFakeSlashServer([]map[string]*slashReceiptWire{
		{
			"tx-1": nil,
			"tx-2": {TxID: "tx-2", Outcome: "applied", Height: 42, NodeID: "rig-77"},
		},
	})
	defer srv.Close()

	cli := &CLI{baseURL: srv.BaseURL(), client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer

	opts := watchSlashOptions{
		TxIDs:          []string{"tx-1", "tx-2"},
		Once:           true,
		IncludePending: true,
		JSON:           true,
	}
	if err := opts.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if err := cli.runWatchSlashes(context.Background(), opts, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSON lines, got %d: %s", len(lines), stdout.String())
	}
	// Sorted: tx-1 (pending) then tx-2 (resolved).
	var ev1, ev2 WatchEvent
	if err := json.Unmarshal([]byte(lines[0]), &ev1); err != nil {
		t.Fatalf("decode 0: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &ev2); err != nil {
		t.Fatalf("decode 1: %v", err)
	}
	if ev1.TxID != "tx-1" || ev1.Kind != WatchKindSlashPending {
		t.Errorf("ev1: %+v", ev1)
	}
	if ev2.TxID != "tx-2" || ev2.Kind != WatchKindSlashResolved || ev2.Outcome != "applied" {
		t.Errorf("ev2: %+v", ev2)
	}
}

func TestRunWatchSlashes_OnceMode_DefaultEmitsResolvedOnly(t *testing.T) {
	srv := newFakeSlashServer([]map[string]*slashReceiptWire{
		{
			"tx-1": nil,
			"tx-2": {TxID: "tx-2", Outcome: "applied", Height: 42},
		},
	})
	defer srv.Close()

	cli := &CLI{baseURL: srv.BaseURL(), client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer

	opts := watchSlashOptions{TxIDs: []string{"tx-1", "tx-2"}, Once: true, JSON: true}
	if err := opts.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if err := cli.runWatchSlashes(context.Background(), opts, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSON line, got %d: %s", len(lines), stdout.String())
	}
	var ev WatchEvent
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ev.TxID != "tx-2" || ev.Kind != WatchKindSlashResolved {
		t.Errorf("event: %+v", ev)
	}
}

func TestRunWatchSlashes_DiffLoop_PendingThenResolved(t *testing.T) {
	// Cycle 1: tx-1 is pending. Cycle 2+: tx-1 is applied.
	// We expect exactly one slash_resolved event when the
	// state transitions.
	stages := []map[string]*slashReceiptWire{
		{"tx-1": nil},
		{"tx-1": {TxID: "tx-1", Outcome: "applied", Height: 42}},
	}
	srv := newFakeSlashServer(stages)
	defer srv.Close()

	cli := &CLI{baseURL: srv.BaseURL(), client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer

	opts := watchSlashOptions{TxIDs: []string{"tx-1"}, JSON: true}
	if err := opts.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	opts.Interval = 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := cli.runWatchSlashes(ctx, opts, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}

	resolved := 0
	for _, ln := range strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n") {
		if ln == "" {
			continue
		}
		var ev WatchEvent
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			t.Errorf("invalid JSON: %v\n%s", err, ln)
			continue
		}
		if ev.Kind == WatchKindSlashResolved && ev.TxID == "tx-1" && ev.Outcome == "applied" {
			resolved++
		}
	}
	if resolved == 0 {
		t.Errorf("did not observe slash_resolved transition; stdout:\n%s", stdout.String())
	}
}

func TestRunWatchSlashes_ExitOnResolved_TerminatesCleanly(t *testing.T) {
	// All ids resolve in cycle 2; --exit-on-resolved must
	// return promptly without ctx cancellation.
	stages := []map[string]*slashReceiptWire{
		{"tx-1": nil, "tx-2": nil},
		{
			"tx-1": {TxID: "tx-1", Outcome: "applied", Height: 1},
			"tx-2": {TxID: "tx-2", Outcome: "rejected", Height: 1, RejectReason: "verifier_failed"},
		},
	}
	srv := newFakeSlashServer(stages)
	defer srv.Close()

	cli := &CLI{baseURL: srv.BaseURL(), client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer

	opts := watchSlashOptions{
		TxIDs:          []string{"tx-1", "tx-2"},
		JSON:           true,
		ExitOnResolved: true,
	}
	if err := opts.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	opts.Interval = 50 * time.Millisecond

	// Long ctx — if exit-on-resolved doesn't fire, the
	// test will fail-fast at this 2-second deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- cli.runWatchSlashes(ctx, opts, &stdout, &stderr)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("--exit-on-resolved did not terminate the loop within 1.5s")
	}

	// Should have observed two slash_resolved events.
	resolved := 0
	for _, ln := range strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n") {
		if ln == "" {
			continue
		}
		var ev WatchEvent
		_ = json.Unmarshal([]byte(ln), &ev)
		if ev.Kind == WatchKindSlashResolved {
			resolved++
		}
	}
	if resolved < 2 {
		t.Errorf("expected at least 2 resolved events, got %d; stdout:\n%s",
			resolved, stdout.String())
	}
}

func TestRunWatchSlashes_AllErrorIsFatalOnInitial(t *testing.T) {
	// Server always 503s. Initial snapshot returns no
	// successes, so runWatchSlashes must error out.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "v2 not configured", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cli := &CLI{baseURL: srv.URL + "/api/v1", client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer

	opts := watchSlashOptions{TxIDs: []string{"tx-1", "tx-2"}, Once: true}
	if err := opts.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	err := cli.runWatchSlashes(context.Background(), opts, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected initial-snapshot error, got nil")
	}
	if !strings.Contains(err.Error(), "initial snapshot") {
		t.Errorf("error msg: %v", err)
	}
}

func TestRunWatchSlashes_PartialErrorOK(t *testing.T) {
	// tx-1 returns 200; tx-2 returns 500. Initial snapshot
	// must succeed because at least one id resolved.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "tx-1") {
			_ = json.NewEncoder(w).Encode(slashReceiptWire{
				TxID: "tx-1", Outcome: "applied", Height: 42,
			})
			return
		}
		http.Error(w, "broken", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cli := &CLI{baseURL: srv.URL + "/api/v1", client: &http.Client{Timeout: 5 * time.Second}}
	var stdout, stderr bytes.Buffer

	opts := watchSlashOptions{
		TxIDs:          []string{"tx-1", "tx-2"},
		Once:           true,
		IncludePending: true,
		JSON:           true,
	}
	if err := opts.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if err := cli.runWatchSlashes(context.Background(), opts, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v (partial failure should be OK)", err)
	}
	// Only tx-1 should appear (tx-2 was dropped due to
	// transient error and is therefore absent from the
	// snapshot).
	if !strings.Contains(stdout.String(), `"tx_id":"tx-1"`) {
		t.Errorf("tx-1 missing from stdout: %s", stdout.String())
	}
	if strings.Contains(stdout.String(), `"tx_id":"tx-2"`) {
		t.Errorf("tx-2 unexpectedly emitted as pending: %s", stdout.String())
	}
}

// -----------------------------------------------------------------------------
// wire-shape pin: slashReceiptWire vs api.SlashReceiptView
// -----------------------------------------------------------------------------

func TestSlashReceiptWireMatchesAPI(t *testing.T) {
	got := jsonTagsOf(slashReceiptWire{})

	// Hard-coded mirror of api.SlashReceiptView's tag set.
	// Verified against
	// QSDM/source/pkg/api/handlers_slash_query.go.
	want := []string{
		"tx_id", "outcome", "recorded_at", "height",
		"slasher", "node_id", "evidence_kind",
		"slashed_dust", "rewarded_dust", "burned_dust",
		"auto_revoked", "auto_revoke_remaining_dust",
		"reject_reason", "error",
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("slashReceiptWire JSON tags drift from api.SlashReceiptView\n got:  %v\n want: %v",
			got, want)
	}
}
