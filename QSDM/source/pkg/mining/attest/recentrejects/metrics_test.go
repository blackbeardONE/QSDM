package recentrejects

// metrics_test.go: unit tests for the dependency-inverted
// MetricsRecorder layer used by Store.Record().
//
// These tests run against the package-default no-op recorder
// most of the time, swap in a capturing fake when an
// assertion needs to inspect what the store told the metrics
// layer, and never assume the production pkg/monitoring
// adapter is loaded — pure pkg/mining/attest/recentrejects
// builds (no monitoring import) MUST stay green.
//
// Every test that swaps the recorder cleans up via
// t.Cleanup(...) so order-dependence in `go test` does not
// leak a test fake into a sibling test's hot path.

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// captureRecorder is a fake MetricsRecorder that records
// every ObserveField invocation in arrival order. Safe for
// concurrent use so the concurrency test can drive it.
type captureRecorder struct {
	mu    sync.Mutex
	calls []captureCall
}

type captureCall struct {
	Field     string
	Runes     int
	Truncated bool
}

func (c *captureRecorder) ObserveField(field string, runes int, truncated bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, captureCall{Field: field, Runes: runes, Truncated: truncated})
}

func (c *captureRecorder) snapshot() []captureCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]captureCall, len(c.calls))
	copy(out, c.calls)
	return out
}

// withCaptureRecorder installs a fresh captureRecorder for
// the duration of the test and restores the no-op default on
// cleanup so a panicking test still leaves global state in a
// known-good shape.
func withCaptureRecorder(t *testing.T) *captureRecorder {
	t.Helper()
	cap := &captureRecorder{}
	SetMetricsRecorder(cap)
	t.Cleanup(func() { SetMetricsRecorder(nil) })
	return cap
}

// -----------------------------------------------------------------------------
// observeAndTruncate (lower-level helper)
// -----------------------------------------------------------------------------

// TestObserveAndTruncate_FiresOnNonEmptyField pins the
// "non-empty input → exactly one ObserveField call" contract
// for each of the three field constants. A future helper
// refactor that accidentally double-fires (e.g. once before
// and once after the rune cast) will bump len(calls) past 1
// and trip this assertion.
func TestObserveAndTruncate_FiresOnNonEmptyField(t *testing.T) {
	cap := withCaptureRecorder(t)
	for _, field := range []string{FieldDetail, FieldGPUName, FieldCertSubject} {
		_ = observeAndTruncate(field, "non-empty", 100)
	}
	calls := cap.snapshot()
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls, got %d: %+v", len(calls), calls)
	}
	for i, want := range []string{FieldDetail, FieldGPUName, FieldCertSubject} {
		if calls[i].Field != want {
			t.Errorf("call[%d].field = %q, want %q", i, calls[i].Field, want)
		}
		if calls[i].Truncated {
			t.Errorf("call[%d] truncated unexpectedly; cap=100, input=9", i)
		}
		if calls[i].Runes != 9 {
			t.Errorf("call[%d].runes = %d, want 9 (rune count of \"non-empty\")", i, calls[i].Runes)
		}
	}
}

// TestObserveAndTruncate_SkipsEmptyField guarantees the
// "empty inputs do not contribute to the observed denominator"
// invariant documented on observeAndTruncate. Folding empty
// fields into the denominator would skew the truncation rate
// because HMAC-only paths legitimately leave CertSubject
// empty (and vice versa) — those zero-length fields are not
// "we got close to the cap" signal, so they must not raise
// the count.
func TestObserveAndTruncate_SkipsEmptyField(t *testing.T) {
	cap := withCaptureRecorder(t)
	got := observeAndTruncate(FieldDetail, "", 100)
	if got != "" {
		t.Errorf("empty input must round-trip to \"\", got %q", got)
	}
	if calls := cap.snapshot(); len(calls) != 0 {
		t.Errorf("empty input fired the recorder %d times: %+v", len(calls), calls)
	}
}

// TestObserveAndTruncate_RuneCountIsPreTruncation locks the
// most operationally-important contract: the metric layer
// MUST see the FULL pre-truncation rune count. Operators
// tune the cap by rate-quotienting truncated/observed; if
// the recorder only ever saw the post-cap count (i.e. 200
// for an input of 1000) the rate would be uninformative
// because every observed value would already be at or below
// the cap.
func TestObserveAndTruncate_RuneCountIsPreTruncation(t *testing.T) {
	cap := withCaptureRecorder(t)
	long := strings.Repeat("A", 250) // 250 runes, cap is 200
	out := observeAndTruncate(FieldDetail, long, 200)
	calls := cap.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Runes != 250 {
		t.Errorf("recorder saw runes=%d, want 250 (the pre-truncation count)", calls[0].Runes)
	}
	if !calls[0].Truncated {
		t.Errorf("truncated flag must be true when runes > cap")
	}
	// Output is truncated (200 runes + ellipsis).
	if got := len([]rune(out)); got != 201 {
		t.Errorf("output rune length = %d, want 201 (cap + ellipsis)", got)
	}
}

// TestObserveAndTruncate_TruncatedFlagOnlyOnOverflow checks
// the boundary at exactly the cap: a value of 200 runes for
// the Detail cap (200) must NOT set truncated=true. Off-by-one
// regressions in the comparator (>= vs >) surface here.
func TestObserveAndTruncate_TruncatedFlagOnlyOnOverflow(t *testing.T) {
	cap := withCaptureRecorder(t)

	// Exactly at cap — must NOT mark truncated.
	atCap := strings.Repeat("B", 200)
	if got := observeAndTruncate(FieldDetail, atCap, 200); got != atCap {
		t.Errorf("at-cap input modified: len=%d (want 200, no ellipsis)", len([]rune(got)))
	}

	// One rune over — MUST mark truncated.
	overCap := strings.Repeat("C", 201)
	_ = observeAndTruncate(FieldDetail, overCap, 200)

	calls := cap.snapshot()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Truncated {
		t.Errorf("at-cap call: truncated=true, want false")
	}
	if !calls[1].Truncated {
		t.Errorf("over-cap call: truncated=false, want true")
	}
}

// TestObserveAndTruncate_DefaultRecorderIsNoop verifies the
// "package without monitoring import" path: with no
// SetMetricsRecorder call ever made (the default state at
// process start in pure-recentrejects unit tests), calling
// observeAndTruncate MUST NOT panic. The default recorder is
// installed in init() and is a structural no-op.
func TestObserveAndTruncate_DefaultRecorderIsNoop(t *testing.T) {
	// Reset to the package default explicitly — defensive in
	// case a sibling test in the same `go test` invocation
	// installed a fake and forgot to clean it up.
	SetMetricsRecorder(nil)
	out := observeAndTruncate(FieldDetail, "hello", 100)
	if out != "hello" {
		t.Errorf("default recorder altered the string: got %q, want %q", out, "hello")
	}
}

// TestSetMetricsRecorder_NilRevertsToNoop locks the
// documented "pass nil to detach" behaviour. Without this
// branch a test that swapped in a fake but called
// SetMetricsRecorder(nil) on cleanup would leave the global
// in a nil-interface state, causing currentMetricsRecorder
// to return the no-op fallback only by accident — making the
// behaviour unreliable for downstream callers like
// pkg/monitoring's adapter.
func TestSetMetricsRecorder_NilRevertsToNoop(t *testing.T) {
	cap := withCaptureRecorder(t)
	SetMetricsRecorder(nil)
	_ = observeAndTruncate(FieldDetail, "after-detach", 100)
	if got := cap.snapshot(); len(got) != 0 {
		t.Errorf("recorder still firing after SetMetricsRecorder(nil): %+v", got)
	}
}

// -----------------------------------------------------------------------------
// Store.Record integration
// -----------------------------------------------------------------------------

// TestStoreRecord_FiresMetricsForAllThreeFields drives the
// whole hot path end-to-end: a single Record() with all three
// observable fields populated must produce three ObserveField
// calls in (Detail, GPUName, CertSubject) order.
func TestStoreRecord_FiresMetricsForAllThreeFields(t *testing.T) {
	cap := withCaptureRecorder(t)
	s := NewStore(0, nil)
	s.Record(Rejection{
		Kind:        KindArchSpoofGPUNameMismatch,
		Detail:      "short detail",
		GPUName:     "NVIDIA H100 80GB HBM3",
		CertSubject: "CN=NVIDIA H100 PCIe",
	})

	calls := cap.snapshot()
	if len(calls) != 3 {
		t.Fatalf("expected 3 ObserveField calls, got %d: %+v", len(calls), calls)
	}
	want := []string{FieldDetail, FieldGPUName, FieldCertSubject}
	for i, w := range want {
		if calls[i].Field != w {
			t.Errorf("call[%d].field = %q, want %q (order is store-stable)",
				i, calls[i].Field, w)
		}
		if calls[i].Truncated {
			t.Errorf("call[%d] (%s) truncated unexpectedly", i, calls[i].Field)
		}
	}
}

// TestStoreRecord_SkipsAbsentFields locks the parsimony
// contract on the production hot path. Most §4.6 rejections
// are HMAC-only or CC-only — only one of GPUName /
// CertSubject is populated, never both. Firing a recorder
// call for the empty field would silently double the
// observed denominator on every non-CC rejection, halving
// the apparent truncation rate of the populated field.
func TestStoreRecord_SkipsAbsentFields(t *testing.T) {
	cap := withCaptureRecorder(t)
	s := NewStore(0, nil)
	// HMAC-style rejection: GPUName populated, CertSubject
	// empty.
	s.Record(Rejection{
		Kind:    KindArchSpoofGPUNameMismatch,
		Detail:  "step 8: gpu_name vs gpu_arch",
		GPUName: "NVIDIA H100 80GB HBM3",
	})

	got := map[string]int{}
	for _, c := range cap.snapshot() {
		got[c.Field]++
	}
	if got[FieldDetail] != 1 {
		t.Errorf("FieldDetail fires = %d, want 1", got[FieldDetail])
	}
	if got[FieldGPUName] != 1 {
		t.Errorf("FieldGPUName fires = %d, want 1", got[FieldGPUName])
	}
	if got[FieldCertSubject] != 0 {
		t.Errorf("FieldCertSubject fires = %d, want 0 (CertSubject was empty)",
			got[FieldCertSubject])
	}
}

// TestStoreRecord_TruncationFlagsMatchStoreCaps is the
// cap-pressure test: Detail at 200, GPUName / CertSubject at
// 256. Inputs at exactly cap+1 trigger the truncated branch
// for that field only; the other two stay clean.
func TestStoreRecord_TruncationFlagsMatchStoreCaps(t *testing.T) {
	cap := withCaptureRecorder(t)
	s := NewStore(0, nil)
	s.Record(Rejection{
		Kind:        KindArchSpoofCCSubjectMismatch,
		Detail:      strings.Repeat("d", maxDetailRunes+1),      // truncated
		GPUName:     strings.Repeat("g", maxGPUNameRunes),       // at cap, not truncated
		CertSubject: strings.Repeat("c", maxCertSubjectRunes+5), // truncated
	})

	got := map[string]captureCall{}
	for _, c := range cap.snapshot() {
		got[c.Field] = c
	}
	if !got[FieldDetail].Truncated {
		t.Errorf("Detail (cap+1) must be truncated; got runes=%d truncated=%v",
			got[FieldDetail].Runes, got[FieldDetail].Truncated)
	}
	if got[FieldGPUName].Truncated {
		t.Errorf("GPUName (at cap) must NOT be truncated; got runes=%d truncated=%v",
			got[FieldGPUName].Runes, got[FieldGPUName].Truncated)
	}
	if !got[FieldCertSubject].Truncated {
		t.Errorf("CertSubject (cap+5) must be truncated; got runes=%d truncated=%v",
			got[FieldCertSubject].Runes, got[FieldCertSubject].Truncated)
	}
}

// TestStoreRecord_NoRecorderInstalled_NoPanic is the
// "blank-build" smoke test: a recentrejects-only build (no
// pkg/monitoring imported by the binary, e.g. a unit test
// that only depends on this package) must keep working with
// the package-default no-op recorder.
func TestStoreRecord_NoRecorderInstalled_NoPanic(t *testing.T) {
	SetMetricsRecorder(nil) // explicit reset to default
	s := NewStore(0, nil)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Store.Record panicked with default recorder: %v", r)
		}
	}()
	s.Record(Rejection{
		Kind:        KindArchSpoofUnknown,
		Detail:      "x",
		GPUName:     "y",
		CertSubject: "z",
	})
}

// TestSetMetricsRecorder_ConcurrentSwapSmoke asserts the
// atomic.Value-backed swap is safe under contention: a
// goroutine flipping the recorder while another goroutine
// drives Store.Record must not panic and the in-flight
// observations must all land somewhere (no lost calls).
func TestSetMetricsRecorder_ConcurrentSwapSmoke(t *testing.T) {
	original := &captureRecorder{}
	swapped := &captureRecorder{}
	SetMetricsRecorder(original)
	t.Cleanup(func() { SetMetricsRecorder(nil) })

	var ops atomic.Int64
	done := make(chan struct{})

	// Producer: bombards the store with records.
	go func() {
		defer close(done)
		s := NewStore(1024, nil)
		for i := 0; i < 1000; i++ {
			s.Record(Rejection{
				Kind:    KindArchSpoofUnknown,
				Detail:  "d",
				GPUName: "g",
			})
			ops.Add(1)
		}
	}()

	// Swapper: alternates the recorder.
	for i := 0; i < 50; i++ {
		if i%2 == 0 {
			SetMetricsRecorder(swapped)
		} else {
			SetMetricsRecorder(original)
		}
	}
	<-done

	got := len(original.snapshot()) + len(swapped.snapshot())
	wantMin := 1000 * 2 // 2 non-empty fields per record
	if got < wantMin {
		t.Errorf("lost ObserveField calls under contention: got %d, want >= %d",
			got, wantMin)
	}
}
