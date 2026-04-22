package contracts

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TraceOp represents a single operation within a contract execution trace.
type TraceOp struct {
	Step      int                    `json:"step"`
	OpName    string                 `json:"op_name"`
	GasBefore int64                  `json:"gas_before"`
	GasAfter  int64                  `json:"gas_after"`
	GasCost   int64                  `json:"gas_cost"`
	Timestamp time.Time              `json:"timestamp"`
	Detail    map[string]interface{} `json:"detail,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

// CallTrace is the complete trace of a single contract call.
type CallTrace struct {
	TraceID      string    `json:"trace_id"`
	ContractID   string    `json:"contract_id"`
	FunctionName string    `json:"function_name"`
	Caller       string    `json:"caller"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
	DurationMs   float64   `json:"duration_ms"`
	TotalGas     int64     `json:"total_gas"`
	Success      bool      `json:"success"`
	Error        string    `json:"error,omitempty"`
	Ops          []TraceOp `json:"ops"`
	InputArgs    map[string]interface{} `json:"input_args,omitempty"`
	Output       interface{}            `json:"output,omitempty"`
}

// GasSummary breaks down gas usage by operation type.
type GasSummary struct {
	ByOp       map[string]int64 `json:"by_op"`
	TotalOps   int              `json:"total_ops"`
	TotalGas   int64            `json:"total_gas"`
	MostExpOp  string           `json:"most_expensive_op"`
	MostExpGas int64            `json:"most_expensive_gas"`
}

// Summary computes a gas-usage breakdown from the trace ops.
func (ct *CallTrace) Summary() GasSummary {
	s := GasSummary{ByOp: make(map[string]int64)}
	for _, op := range ct.Ops {
		s.ByOp[op.OpName] += op.GasCost
		s.TotalGas += op.GasCost
		s.TotalOps++
		if s.ByOp[op.OpName] > s.MostExpGas {
			s.MostExpGas = s.ByOp[op.OpName]
			s.MostExpOp = op.OpName
		}
	}
	return s
}

// CallTracer records execution traces for contract calls.
type CallTracer struct {
	mu     sync.RWMutex
	traces map[string]*CallTrace // traceID -> trace
	byCall map[string][]string   // contractID:funcName -> traceIDs
	order  []string              // insertion order
	limit  int                   // max stored traces (ring buffer)

	persistPath string        // NDJSON append path; empty disables disk write
	maxTraceAge time.Duration // TTL from EndTime; PruneByMaxAge uses this when >0
}

// NewCallTracer creates a tracer that retains up to `limit` traces.
func NewCallTracer(limit int) *CallTracer {
	if limit <= 0 {
		limit = 10000
	}
	return &CallTracer{
		traces: make(map[string]*CallTrace),
		byCall: make(map[string][]string),
		limit:  limit,
	}
}

// ConfigureRetention sets optional NDJSON persistence path and in-memory TTL
// (EndTime-based). Call PruneByMaxAge periodically or after LoadPersistedTraces.
func (ct *CallTracer) ConfigureRetention(path string, maxTraceAge time.Duration) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.persistPath = path
	ct.maxTraceAge = maxTraceAge
}

// LoadPersistedTraces reads NDJSON lines from persistPath into memory (same ring
// buffer rules as live traces). persistPath must be set via ConfigureRetention.
func (ct *CallTracer) LoadPersistedTraces() (int, error) {
	ct.mu.RLock()
	path := ct.persistPath
	ct.mu.RUnlock()
	if path == "" {
		return 0, fmt.Errorf("persist path not configured")
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()

	var loaded []*CallTrace
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var tr CallTrace
		if err := json.Unmarshal(line, &tr); err != nil {
			return len(loaded), err
		}
		cp := tr
		loaded = append(loaded, &cp)
	}
	if err := sc.Err(); err != nil {
		return len(loaded), err
	}

	ct.mu.Lock()
	defer ct.mu.Unlock()
	for _, t := range loaded {
		ct.storeTraceLocked(t)
	}
	return len(loaded), nil
}

// PruneByMaxAge removes traces whose EndTime is older than now-maxTraceAge.
// Returns number removed. No-op if maxTraceAge is zero.
func (ct *CallTracer) PruneByMaxAge(now time.Time) int {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	if ct.maxTraceAge <= 0 {
		return 0
	}
	cutoff := now.Add(-ct.maxTraceAge)
	removed := 0
	var newOrder []string
	for _, id := range ct.order {
		tr := ct.traces[id]
		if tr == nil {
			continue
		}
		if !tr.EndTime.IsZero() && tr.EndTime.Before(cutoff) {
			key := fmt.Sprintf("%s:%s", tr.ContractID, tr.FunctionName)
			ct.removeFromByCall(key, id)
			delete(ct.traces, id)
			removed++
			continue
		}
		newOrder = append(newOrder, id)
	}
	ct.order = newOrder
	return removed
}

func (ct *CallTracer) storeTraceLocked(t *CallTrace) {
	if len(ct.order) >= ct.limit {
		evict := ct.order[0]
		ct.order = ct.order[1:]
		if old, ok := ct.traces[evict]; ok {
			key := fmt.Sprintf("%s:%s", old.ContractID, old.FunctionName)
			ct.removeFromByCall(key, evict)
			delete(ct.traces, evict)
		}
	}
	ct.traces[t.TraceID] = t
	ct.order = append(ct.order, t.TraceID)
	key := fmt.Sprintf("%s:%s", t.ContractID, t.FunctionName)
	ct.byCall[key] = append(ct.byCall[key], t.TraceID)
}

func (ct *CallTracer) appendTraceLine(t *CallTrace) error {
	ct.mu.RLock()
	path := ct.persistPath
	ct.mu.RUnlock()
	if path == "" {
		return nil
	}
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

// CompactPersistedTraces rewrites the NDJSON persist file from current in-memory
// traces in insertion order. Use after PruneByMaxAge to shrink the on-disk log.
func (ct *CallTracer) CompactPersistedTraces() error {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	path := ct.persistPath
	if path == "" {
		return fmt.Errorf("persist path not configured")
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "traces-compact-*.ndjson")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	for _, id := range ct.order {
		tr := ct.traces[id]
		if tr == nil {
			continue
		}
		b, err := json.Marshal(tr)
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
			return err
		}
		if _, err := tmp.Write(append(b, '\n')); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	_ = os.Remove(path)
	return os.Rename(tmpPath, path)
}

// StartTraceCompactionLoop runs until ctx is cancelled. Each tick it optionally prunes
// by maxTraceAge, then compacts the NDJSON file when maxPersistBytes <= 0 and pruning
// removed traces, or when maxPersistBytes > 0 and the file is at least that large.
func (ct *CallTracer) StartTraceCompactionLoop(ctx context.Context, interval time.Duration, maxPersistBytes int64) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ct.mu.RLock()
				path := ct.persistPath
				maxAge := ct.maxTraceAge
				ct.mu.RUnlock()
				if path == "" {
					continue
				}
				removed := 0
				if maxAge > 0 {
					removed = ct.PruneByMaxAge(time.Now())
				}
				shouldCompact := false
				if maxPersistBytes <= 0 {
					shouldCompact = removed > 0
				} else if fi, err := os.Stat(path); err == nil && fi.Size() >= maxPersistBytes {
					shouldCompact = true
				}
				if shouldCompact {
					_ = ct.CompactPersistedTraces()
				}
			}
		}
	}()
}

// TraceBuilder builds a trace incrementally during contract execution.
type TraceBuilder struct {
	trace *CallTrace
	step  int
}

// BeginTrace starts recording a new call trace.
func (ct *CallTracer) BeginTrace(traceID, contractID, funcName, caller string, args map[string]interface{}) *TraceBuilder {
	trace := &CallTrace{
		TraceID:      traceID,
		ContractID:   contractID,
		FunctionName: funcName,
		Caller:       caller,
		StartTime:    time.Now(),
		InputArgs:    args,
	}
	return &TraceBuilder{trace: trace}
}

// RecordOp adds a single operation to the trace.
func (tb *TraceBuilder) RecordOp(opName string, gasBefore, gasAfter int64, detail map[string]interface{}, err error) {
	op := TraceOp{
		Step:      tb.step,
		OpName:    opName,
		GasBefore: gasBefore,
		GasAfter:  gasAfter,
		GasCost:   gasAfter - gasBefore,
		Timestamp: time.Now(),
		Detail:    detail,
	}
	if err != nil {
		op.Error = err.Error()
	}
	tb.trace.Ops = append(tb.trace.Ops, op)
	tb.step++
}

// Finish finalizes the trace and stores it.
func (ct *CallTracer) Finish(tb *TraceBuilder, totalGas int64, output interface{}, execErr error) *CallTrace {
	tb.trace.EndTime = time.Now()
	tb.trace.DurationMs = float64(tb.trace.EndTime.Sub(tb.trace.StartTime).Microseconds()) / 1000.0
	tb.trace.TotalGas = totalGas
	tb.trace.Output = output
	if execErr != nil {
		tb.trace.Error = execErr.Error()
		tb.trace.Success = false
	} else {
		tb.trace.Success = true
	}

	ct.mu.Lock()
	ct.storeTraceLocked(tb.trace)
	ct.mu.Unlock()

	if err := ct.appendTraceLine(tb.trace); err != nil {
		_ = err // best-effort persistence
	}

	return tb.trace
}

// Get retrieves a trace by ID.
func (ct *CallTracer) Get(traceID string) (*CallTrace, bool) {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	t, ok := ct.traces[traceID]
	return t, ok
}

// GetByCall returns traces for a specific contract function.
func (ct *CallTracer) GetByCall(contractID, funcName string) []*CallTrace {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	key := fmt.Sprintf("%s:%s", contractID, funcName)
	ids := ct.byCall[key]
	out := make([]*CallTrace, 0, len(ids))
	for _, id := range ids {
		if t, ok := ct.traces[id]; ok {
			out = append(out, t)
		}
	}
	return out
}

// Recent returns the last N traces.
func (ct *CallTracer) Recent(n int) []*CallTrace {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	if n <= 0 || len(ct.order) == 0 {
		return nil
	}
	start := len(ct.order) - n
	if start < 0 {
		start = 0
	}
	out := make([]*CallTrace, 0, n)
	for i := len(ct.order) - 1; i >= start; i-- {
		if t, ok := ct.traces[ct.order[i]]; ok {
			out = append(out, t)
		}
	}
	return out
}

// Count returns the number of stored traces.
func (ct *CallTracer) Count() int {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	return len(ct.traces)
}

// Stats returns aggregate statistics.
func (ct *CallTracer) Stats() map[string]interface{} {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	var totalGas int64
	var failed int
	var totalDuration float64
	for _, t := range ct.traces {
		totalGas += t.TotalGas
		totalDuration += t.DurationMs
		if !t.Success {
			failed++
		}
	}

	out := map[string]interface{}{
		"total_traces":    len(ct.traces),
		"failed_traces":   failed,
		"total_gas":       totalGas,
		"avg_duration_ms": totalDuration / float64(maxInt(1, len(ct.traces))),
		"unique_calls":    len(ct.byCall),
	}
	if ct.persistPath != "" {
		out["persist_path"] = ct.persistPath
	}
	if ct.maxTraceAge > 0 {
		out["max_trace_age_sec"] = ct.maxTraceAge.Seconds()
	}
	return out
}

func (ct *CallTracer) removeFromByCall(key, traceID string) {
	ids := ct.byCall[key]
	for i, id := range ids {
		if id == traceID {
			ct.byCall[key] = append(ids[:i], ids[i+1:]...)
			break
		}
	}
	if len(ct.byCall[key]) == 0 {
		delete(ct.byCall, key)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
