package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mkActive(active map[string]uint64) govParamsSnapshot {
	out := govParamsSnapshot{
		Active:  map[string]uint64{},
		Pending: map[string]govParamsPendingWire{},
	}
	for k, v := range active {
		out.Active[k] = v
	}
	return out
}

// -----------------------------------------------------------------------------
// diffGovParamsSnapshots
// -----------------------------------------------------------------------------

func TestDiffGovParamsSnapshots_NoChange(t *testing.T) {
	prev := mkActive(map[string]uint64{"reward_bps": 500})
	next := mkActive(map[string]uint64{"reward_bps": 500})
	got := diffGovParamsSnapshots(prev, next, time.Unix(0, 0).UTC(), "")
	if len(got) != 0 {
		t.Errorf("want zero events, got %+v", got)
	}
}

func TestDiffGovParamsSnapshots_StagedNewPending(t *testing.T) {
	prev := mkActive(map[string]uint64{"reward_bps": 500})
	next := mkActive(map[string]uint64{"reward_bps": 500})
	next.Pending["reward_bps"] = govParamsPendingWire{
		Param:           "reward_bps",
		Value:           750,
		EffectiveHeight: 1000,
		Authority:       "alice",
		Memo:            "hello",
	}
	ts := time.Unix(1, 0).UTC()
	got := diffGovParamsSnapshots(prev, next, ts, "")
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %+v", got)
	}
	ev := got[0]
	if ev.Kind != WatchKindParamStaged {
		t.Errorf("Kind=%s, want %s", ev.Kind, WatchKindParamStaged)
	}
	if ev.Param != "reward_bps" || ev.PendingValue != 750 ||
		ev.PendingEffectiveHeight != 1000 || ev.Authority != "alice" {
		t.Errorf("event mismatch: %+v", ev)
	}
}

func TestDiffGovParamsSnapshots_Superseded(t *testing.T) {
	prev := mkActive(map[string]uint64{"reward_bps": 500})
	prev.Pending["reward_bps"] = govParamsPendingWire{
		Param: "reward_bps", Value: 600, EffectiveHeight: 1000, Authority: "alice",
	}
	next := mkActive(map[string]uint64{"reward_bps": 500})
	next.Pending["reward_bps"] = govParamsPendingWire{
		Param: "reward_bps", Value: 800, EffectiveHeight: 1100, Authority: "bob",
	}
	got := diffGovParamsSnapshots(prev, next, time.Now().UTC(), "")
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %+v", got)
	}
	ev := got[0]
	if ev.Kind != WatchKindParamSuperseded {
		t.Errorf("Kind=%s, want %s", ev.Kind, WatchKindParamSuperseded)
	}
	if ev.PrevPendingValue != 600 || ev.PendingValue != 800 {
		t.Errorf("supersede event mismatch: %+v", ev)
	}
}

func TestDiffGovParamsSnapshots_Activated(t *testing.T) {
	prev := mkActive(map[string]uint64{"reward_bps": 500})
	prev.Pending["reward_bps"] = govParamsPendingWire{
		Param: "reward_bps", Value: 750, EffectiveHeight: 1000,
	}
	next := mkActive(map[string]uint64{"reward_bps": 750})
	got := diffGovParamsSnapshots(prev, next, time.Now().UTC(), "")
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %+v", got)
	}
	ev := got[0]
	if ev.Kind != WatchKindParamActivated {
		t.Errorf("Kind=%s, want %s", ev.Kind, WatchKindParamActivated)
	}
	if ev.PrevActiveValue != 500 || ev.ActiveValue != 750 {
		t.Errorf("activated event mismatch: %+v", ev)
	}
}

func TestDiffGovParamsSnapshots_Removed(t *testing.T) {
	prev := mkActive(map[string]uint64{"reward_bps": 500})
	prev.Pending["reward_bps"] = govParamsPendingWire{
		Param: "reward_bps", Value: 750, EffectiveHeight: 1000,
	}
	// next has no pending and active is unchanged -> removed-without-activation
	next := mkActive(map[string]uint64{"reward_bps": 500})
	got := diffGovParamsSnapshots(prev, next, time.Now().UTC(), "")
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %+v", got)
	}
	if got[0].Kind != WatchKindParamRemoved {
		t.Errorf("Kind=%s, want %s", got[0].Kind, WatchKindParamRemoved)
	}
	if got[0].PrevPendingValue != 750 {
		t.Errorf("PrevPendingValue=%d, want 750", got[0].PrevPendingValue)
	}
}

func TestDiffGovParamsSnapshots_AuthoritiesChanged(t *testing.T) {
	prev := mkActive(map[string]uint64{"reward_bps": 500})
	prev.Authorities = []string{"alice", "bob"}
	next := mkActive(map[string]uint64{"reward_bps": 500})
	next.Authorities = []string{"alice", "carol"}

	got := diffGovParamsSnapshots(prev, next, time.Now().UTC(), "")
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %+v", got)
	}
	ev := got[0]
	if ev.Kind != WatchKindParamAuthoritiesChanged {
		t.Errorf("Kind=%s, want %s", ev.Kind, WatchKindParamAuthoritiesChanged)
	}
	if len(ev.AuthoritiesAdded) != 1 || ev.AuthoritiesAdded[0] != "carol" {
		t.Errorf("AuthoritiesAdded=%v, want [carol]", ev.AuthoritiesAdded)
	}
	if len(ev.AuthoritiesRemoved) != 1 || ev.AuthoritiesRemoved[0] != "bob" {
		t.Errorf("AuthoritiesRemoved=%v, want [bob]", ev.AuthoritiesRemoved)
	}
}

func TestDiffGovParamsSnapshots_FilterByParam(t *testing.T) {
	prev := mkActive(map[string]uint64{
		"reward_bps":                500,
		"auto_revoke_min_stake_dust": 100,
	})
	next := mkActive(map[string]uint64{
		"reward_bps":                500,
		"auto_revoke_min_stake_dust": 200,
	})
	next.Pending["reward_bps"] = govParamsPendingWire{
		Param: "reward_bps", Value: 750, EffectiveHeight: 1000,
	}
	// Filter to reward_bps; should NOT see the auto_revoke change.
	got := diffGovParamsSnapshots(prev, next, time.Now().UTC(), "reward_bps")
	if len(got) != 1 {
		t.Fatalf("want 1 event with filter, got %+v", got)
	}
	if got[0].Param != "reward_bps" || got[0].Kind != WatchKindParamStaged {
		t.Errorf("filtered event mismatch: %+v", got[0])
	}
}

func TestDiffGovParamsSnapshots_DeterministicOrder(t *testing.T) {
	prev := mkActive(map[string]uint64{"a": 1, "b": 1, "c": 1})
	next := mkActive(map[string]uint64{"a": 1, "b": 1, "c": 1})
	next.Pending["c"] = govParamsPendingWire{Param: "c", Value: 2, EffectiveHeight: 100}
	next.Pending["a"] = govParamsPendingWire{Param: "a", Value: 2, EffectiveHeight: 100}
	next.Pending["b"] = govParamsPendingWire{Param: "b", Value: 2, EffectiveHeight: 100}

	got := diffGovParamsSnapshots(prev, next, time.Now().UTC(), "")
	if len(got) != 3 {
		t.Fatalf("want 3 events, got %d", len(got))
	}
	// Expect a, b, c in that order.
	if got[0].Param != "a" || got[1].Param != "b" || got[2].Param != "c" {
		t.Errorf("non-deterministic order: %v %v %v",
			got[0].Param, got[1].Param, got[2].Param)
	}
}

// -----------------------------------------------------------------------------
// govParamsSnapshotAsInitialEvents
// -----------------------------------------------------------------------------

func TestGovParamsSnapshotAsInitialEvents_EmitsPerPending(t *testing.T) {
	snap := mkActive(map[string]uint64{"reward_bps": 500, "other": 1})
	snap.Pending["reward_bps"] = govParamsPendingWire{
		Param: "reward_bps", Value: 750, EffectiveHeight: 1000, Authority: "alice",
	}
	snap.Pending["other"] = govParamsPendingWire{
		Param: "other", Value: 2, EffectiveHeight: 999, Authority: "bob",
	}
	ts := time.Now().UTC()
	got := govParamsSnapshotAsInitialEvents(snap, ts, "")
	if len(got) != 2 {
		t.Fatalf("want 2 initial events, got %d", len(got))
	}
	for _, ev := range got {
		if ev.Kind != WatchKindParamStaged {
			t.Errorf("initial event kind=%s, want %s", ev.Kind, WatchKindParamStaged)
		}
	}
	// Sorted by name.
	if got[0].Param != "other" || got[1].Param != "reward_bps" {
		t.Errorf("initial events not sorted: %v %v", got[0].Param, got[1].Param)
	}
}

func TestGovParamsSnapshotAsInitialEvents_FilteredEmitsOne(t *testing.T) {
	snap := mkActive(map[string]uint64{"a": 1, "b": 1})
	snap.Pending["a"] = govParamsPendingWire{Param: "a", Value: 2, EffectiveHeight: 1}
	snap.Pending["b"] = govParamsPendingWire{Param: "b", Value: 2, EffectiveHeight: 1}
	got := govParamsSnapshotAsInitialEvents(snap, time.Now().UTC(), "a")
	if len(got) != 1 || got[0].Param != "a" {
		t.Fatalf("want one event for 'a', got %+v", got)
	}
}

// -----------------------------------------------------------------------------
// JSON-Lines wire-format pin
// -----------------------------------------------------------------------------

// Make sure a param event JSON-decodes back into something
// readable; lock in the field names operators consume.
func TestParamWatchEvent_JSONLinesShape(t *testing.T) {
	ev := WatchEvent{
		Timestamp:              time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC),
		Kind:                   WatchKindParamStaged,
		Param:                  "reward_bps",
		ActiveValue:            500,
		PendingValue:           750,
		PendingEffectiveHeight: 1000,
		Authority:              "alice",
		Memo:                   "post-mortem #14",
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`"event":"param_staged"`,
		`"param":"reward_bps"`,
		`"active_value":500`,
		`"pending_value":750`,
		`"pending_effective_height":1000`,
		`"authority":"alice"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("JSON missing %q in %s", want, s)
		}
	}
}

// -----------------------------------------------------------------------------
// watchParamsOptions normalize
// -----------------------------------------------------------------------------

func TestWatchParamsOptions_NormalizeClampsInterval(t *testing.T) {
	opts := watchParamsOptions{Interval: 1 * time.Second}
	if err := opts.normalize(); err != nil {
		t.Fatal(err)
	}
	if opts.Interval != MinWatchInterval {
		t.Errorf("Interval=%v, want clamp to %v", opts.Interval, MinWatchInterval)
	}
}

func TestWatchParamsOptions_NormalizeRejectsHugeFilter(t *testing.T) {
	opts := watchParamsOptions{ParamFilter: strings.Repeat("x", 200)}
	if err := opts.normalize(); err == nil {
		t.Error("want error on too-long --param filter, got nil")
	}
}
