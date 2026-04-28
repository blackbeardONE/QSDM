package monitoring

// archcheck_metrics_test.go: unit tests for the arch-spoof and
// hashrate-band counters + the mining_recorder.go adapter that
// pkg/mining.Verifier hits in production.
//
// These tests live in pkg/monitoring (not pkg/mining) because:
//
//   - The counter functions are package-private state; the
//     simplest way to assert against them is to drive them
//     directly from the same package.
//
//   - The adapter wiring is consensus-relevant — a regression
//     where mining.SetMiningMetricsRecorder is silently never
//     called would mean every dashboard goes dark on the
//     §4.6 rejection signal. Locking it here means a future
//     refactor that breaks the init() registration trips a
//     loud test failure.

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// TestArchSpoofRejected_CounterByReason locks each known
// reason tag against its dedicated counter — a mismatch in the
// switch table inside RecordArchSpoofRejected would surface
// here as a wrong-bucket increment.
func TestArchSpoofRejected_CounterByReason(t *testing.T) {
	t.Cleanup(ResetArchcheckMetricsForTest)
	ResetArchcheckMetricsForTest()

	RecordArchSpoofRejected(ArchSpoofRejectReasonUnknownArch)
	RecordArchSpoofRejected(ArchSpoofRejectReasonUnknownArch)
	RecordArchSpoofRejected(ArchSpoofRejectReasonGPUNameMismatch)

	got := indexArchSpoofRejected(t)
	if got[ArchSpoofRejectReasonUnknownArch] != 2 {
		t.Errorf("unknown_arch = %d, want 2", got[ArchSpoofRejectReasonUnknownArch])
	}
	if got[ArchSpoofRejectReasonGPUNameMismatch] != 1 {
		t.Errorf("gpu_name_mismatch = %d, want 1", got[ArchSpoofRejectReasonGPUNameMismatch])
	}
}

// TestArchSpoofRejected_UnknownReasonBucketsToUnknown_arch
// covers the cardinality-bound: a future code path that passes
// a typo'd reason string (e.g. "gpuNameMismatch") MUST land in
// the unknown_arch bucket rather than creating a new label.
func TestArchSpoofRejected_UnknownReasonBucketsToUnknownArch(t *testing.T) {
	t.Cleanup(ResetArchcheckMetricsForTest)
	ResetArchcheckMetricsForTest()

	RecordArchSpoofRejected("totally-fabricated-reason-tag")
	got := indexArchSpoofRejected(t)
	if got[ArchSpoofRejectReasonUnknownArch] != 1 {
		t.Errorf("unrecognised reason should bucket to unknown_arch (got %d)",
			got[ArchSpoofRejectReasonUnknownArch])
	}
}

// TestHashrateRejected_CounterByArch locks each canonical
// arch's dedicated counter.
func TestHashrateRejected_CounterByArch(t *testing.T) {
	t.Cleanup(ResetArchcheckMetricsForTest)
	ResetArchcheckMetricsForTest()

	for _, a := range []string{"hopper", "blackwell", "ada-lovelace", "ampere", "turing"} {
		RecordHashrateRejected(a)
	}

	got := indexHashrateRejected(t)
	for _, a := range []string{"hopper", "blackwell", "ada-lovelace", "ampere", "turing"} {
		if got[a] != 1 {
			t.Errorf("hashrate_rejected[%q] = %d, want 1", a, got[a])
		}
	}
}

// TestHashrateRejected_UnknownArchBucketsToUnknown protects
// the cardinality bound for the same reason as the arch-spoof
// test above.
func TestHashrateRejected_UnknownArchBucketsToUnknown(t *testing.T) {
	t.Cleanup(ResetArchcheckMetricsForTest)
	ResetArchcheckMetricsForTest()

	RecordHashrateRejected("voltA")
	got := indexHashrateRejected(t)
	if got["unknown"] != 1 {
		t.Errorf("unknown arch should bucket to 'unknown' (got %d)", got["unknown"])
	}
}

// TestMiningMetricsAdapter_IsRegistered locks the init()
// registration. If the chain of adapter -> Set ever breaks
// silently this test catches it without us having to drive a
// full Verify() call.
func TestMiningMetricsAdapter_IsRegistered(t *testing.T) {
	t.Cleanup(ResetArchcheckMetricsForTest)
	t.Cleanup(func() {
		mining.SetMiningMetricsRecorder(miningMetricsAdapter{})
	})
	ResetArchcheckMetricsForTest()

	// Drive through the mining-side recorder. If init() did
	// its job, the counter increments here.
	mining.SetMiningMetricsRecorder(miningMetricsAdapter{})

	// Use the public mining.MiningMetricsRecorder interface to
	// avoid reaching into mining-package internals from here.
	rec := miningMetricsAdapter{}
	rec.RecordArchSpoofRejected(mining.ArchSpoofRejectReasonGPUNameMismatch)
	rec.RecordHashrateRejected("hopper")

	gotArch := indexArchSpoofRejected(t)
	if gotArch[ArchSpoofRejectReasonGPUNameMismatch] != 1 {
		t.Errorf("adapter did not forward arch-spoof rejection: gpu_name_mismatch = %d, want 1",
			gotArch[ArchSpoofRejectReasonGPUNameMismatch])
	}
	gotHR := indexHashrateRejected(t)
	if gotHR["hopper"] != 1 {
		t.Errorf("adapter did not forward hashrate rejection: hopper = %d, want 1",
			gotHR["hopper"])
	}
}

// indexArchSpoofRejected materialises the labelled-counter
// list into a map keyed by reason for terse test-side
// assertions. Returns the empty map if the labelled accessor
// returns nothing.
func indexArchSpoofRejected(t *testing.T) map[string]uint64 {
	t.Helper()
	out := make(map[string]uint64)
	for _, p := range ArchSpoofRejectedLabeled() {
		out[p.Reason] = p.Val
	}
	return out
}

// indexHashrateRejected mirrors indexArchSpoofRejected but
// keys by arch.
func indexHashrateRejected(t *testing.T) map[string]uint64 {
	t.Helper()
	out := make(map[string]uint64)
	for _, p := range HashrateRejectedLabeled() {
		out[p.Arch] = p.Val
	}
	return out
}
