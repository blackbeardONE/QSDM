package contracts

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCallTracer_BeginFinish(t *testing.T) {
	ct := NewCallTracer(100)

	tb := ct.BeginTrace("trace-1", "contract-a", "transfer", "alice",
		map[string]interface{}{"to": "bob", "amount": 100})

	tb.RecordOp("state_read", 0, 50, map[string]interface{}{"key": "balance"}, nil)
	tb.RecordOp("state_write", 50, 250, map[string]interface{}{"key": "balance", "value": 900}, nil)
	tb.RecordOp("state_write", 250, 450, map[string]interface{}{"key": "balance", "value": 100}, nil)

	trace := ct.Finish(tb, 450, map[string]interface{}{"ok": true}, nil)

	if !trace.Success {
		t.Fatal("expected success")
	}
	if trace.TotalGas != 450 {
		t.Fatalf("expected 450 gas, got %d", trace.TotalGas)
	}
	if len(trace.Ops) != 3 {
		t.Fatalf("expected 3 ops, got %d", len(trace.Ops))
	}
	if trace.DurationMs < 0 {
		t.Fatal("expected non-negative duration")
	}
}

func TestCallTracer_FailedTrace(t *testing.T) {
	ct := NewCallTracer(100)

	tb := ct.BeginTrace("trace-fail", "contract-b", "divide", "alice", nil)
	tb.RecordOp("compute", 0, 100, nil, fmt.Errorf("division by zero"))

	trace := ct.Finish(tb, 100, nil, fmt.Errorf("division by zero"))

	if trace.Success {
		t.Fatal("expected failure")
	}
	if trace.Error != "division by zero" {
		t.Fatalf("unexpected error: %s", trace.Error)
	}
	if trace.Ops[0].Error != "division by zero" {
		t.Fatal("op should record error")
	}
}

func TestCallTrace_Summary(t *testing.T) {
	ct := NewCallTracer(100)

	tb := ct.BeginTrace("trace-s", "c", "fn", "alice", nil)
	tb.RecordOp("state_read", 0, 50, nil, nil)
	tb.RecordOp("state_read", 50, 100, nil, nil)
	tb.RecordOp("state_write", 100, 300, nil, nil)
	tb.RecordOp("compute", 300, 320, nil, nil)

	trace := ct.Finish(tb, 320, nil, nil)
	summary := trace.Summary()

	if summary.TotalOps != 4 {
		t.Fatalf("expected 4 ops, got %d", summary.TotalOps)
	}
	if summary.ByOp["state_read"] != 100 {
		t.Fatalf("expected 100 gas for state_read, got %d", summary.ByOp["state_read"])
	}
	if summary.ByOp["state_write"] != 200 {
		t.Fatalf("expected 200 gas for state_write, got %d", summary.ByOp["state_write"])
	}
	if summary.MostExpOp != "state_write" {
		t.Fatalf("expected state_write as most expensive, got %s", summary.MostExpOp)
	}
}

func TestCallTracer_GetByCall(t *testing.T) {
	ct := NewCallTracer(100)

	for i := 0; i < 3; i++ {
		tb := ct.BeginTrace(fmt.Sprintf("t-%d", i), "contract-x", "mint", "alice", nil)
		ct.Finish(tb, int64(i*100), nil, nil)
	}

	// Different function
	tb := ct.BeginTrace("t-other", "contract-x", "burn", "alice", nil)
	ct.Finish(tb, 50, nil, nil)

	traces := ct.GetByCall("contract-x", "mint")
	if len(traces) != 3 {
		t.Fatalf("expected 3 mint traces, got %d", len(traces))
	}

	burns := ct.GetByCall("contract-x", "burn")
	if len(burns) != 1 {
		t.Fatalf("expected 1 burn trace, got %d", len(burns))
	}
}

func TestCallTracer_Recent(t *testing.T) {
	ct := NewCallTracer(100)

	for i := 0; i < 5; i++ {
		tb := ct.BeginTrace(fmt.Sprintf("t-%d", i), "c", "f", "a", nil)
		ct.Finish(tb, 0, nil, nil)
	}

	recent := ct.Recent(3)
	if len(recent) != 3 {
		t.Fatalf("expected 3, got %d", len(recent))
	}
	if recent[0].TraceID != "t-4" {
		t.Fatalf("expected most recent first, got %s", recent[0].TraceID)
	}
}

func TestCallTracer_Eviction(t *testing.T) {
	ct := NewCallTracer(3) // only keep 3

	for i := 0; i < 5; i++ {
		tb := ct.BeginTrace(fmt.Sprintf("t-%d", i), "c", "f", "a", nil)
		ct.Finish(tb, 0, nil, nil)
	}

	if ct.Count() != 3 {
		t.Fatalf("expected 3 after eviction, got %d", ct.Count())
	}

	// Oldest should be evicted
	if _, ok := ct.Get("t-0"); ok {
		t.Fatal("t-0 should have been evicted")
	}
	if _, ok := ct.Get("t-1"); ok {
		t.Fatal("t-1 should have been evicted")
	}
	if _, ok := ct.Get("t-4"); !ok {
		t.Fatal("t-4 should still exist")
	}
}

func TestCallTracer_Stats(t *testing.T) {
	ct := NewCallTracer(100)

	tb := ct.BeginTrace("ok", "c", "f", "a", nil)
	ct.Finish(tb, 100, nil, nil)

	tb2 := ct.BeginTrace("fail", "c", "f", "a", nil)
	ct.Finish(tb2, 50, nil, fmt.Errorf("boom"))

	stats := ct.Stats()
	if stats["total_traces"].(int) != 2 {
		t.Fatal("expected 2 traces")
	}
	if stats["failed_traces"].(int) != 1 {
		t.Fatal("expected 1 failed")
	}
	if stats["total_gas"].(int64) != 150 {
		t.Fatal("expected 150 total gas")
	}
}

func TestCallTracer_Get(t *testing.T) {
	ct := NewCallTracer(100)
	tb := ct.BeginTrace("x", "c", "f", "a", nil)
	ct.Finish(tb, 0, nil, nil)

	tr, ok := ct.Get("x")
	if !ok || tr.TraceID != "x" {
		t.Fatal("should find trace x")
	}

	_, ok = ct.Get("nonexistent")
	if ok {
		t.Fatal("should not find nonexistent")
	}
}

func TestCallTracer_OpStepOrdering(t *testing.T) {
	ct := NewCallTracer(100)
	tb := ct.BeginTrace("t", "c", "f", "a", nil)
	for i := 0; i < 5; i++ {
		tb.RecordOp("op", 0, 0, nil, nil)
	}
	trace := ct.Finish(tb, 0, nil, nil)

	for i, op := range trace.Ops {
		if op.Step != i {
			t.Fatalf("expected step %d, got %d", i, op.Step)
		}
	}
}

func TestCallTracer_PersistLoadAndPrune(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "traces.ndjson")

	ct := NewCallTracer(100)
	ct.ConfigureRetention(path, time.Hour)
	tb := ct.BeginTrace("persist-1", "c1", "fn", "alice", nil)
	ct.Finish(tb, 10, nil, nil)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected persist file: %v", err)
	}

	ct2 := NewCallTracer(100)
	ct2.ConfigureRetention(path, 0)
	n, err := ct2.LoadPersistedTraces()
	if err != nil || n != 1 {
		t.Fatalf("load: n=%d err=%v", n, err)
	}
	if _, ok := ct2.Get("persist-1"); !ok {
		t.Fatal("expected trace after load")
	}

	ct2.ConfigureRetention(path, time.Nanosecond)
	if ct2.PruneByMaxAge(time.Now().Add(time.Hour)) != 1 {
		t.Fatal("expected prune to remove stale trace")
	}
	if ct2.Count() != 0 {
		t.Fatalf("expected empty after prune, count=%d", ct2.Count())
	}
}

func TestCallTracer_CompactPersistedTraces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.ndjson")
	ct := NewCallTracer(50)
	ct.ConfigureRetention(path, 0)
	for i := 0; i < 3; i++ {
		tb := ct.BeginTrace(fmt.Sprintf("c-%d", i), "c", "f", "a", nil)
		ct.Finish(tb, 0, nil, nil)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Size() < 10 {
		t.Fatalf("expected non-trivial persist file: %v size=%d", err, fi.Size())
	}
	if err := ct.CompactPersistedTraces(); err != nil {
		t.Fatalf("compact: %v", err)
	}
	fi2, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi2.Size() == 0 {
		t.Fatal("expected compact file non-empty")
	}
}
