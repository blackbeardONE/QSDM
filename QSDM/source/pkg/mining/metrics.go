package mining

// metrics.go: dependency-inverted metrics recorder for the
// v2 attestation verifier's arch-spoof and hashrate rejection
// paths (MINING_PROTOCOL_V2 §4.6 / §3.3 step 8).
//
// Why dependency inversion (mirror of pkg/chain/events.go):
//
//   pkg/mining MUST NOT import pkg/monitoring — monitoring
//   already imports pkg/mining (and several mining/* sub-
//   packages) for state-provider adapters. The reverse import
//   would close an obvious cycle.
//
// So pkg/mining declares this narrow interface + a no-op
// default; pkg/monitoring's chain_recorder.go registers a
// Prometheus-backed implementation via SetMiningMetricsRecorder
// at init() time. Anything that imports pkg/mining AND
// pkg/monitoring (i.e. every production binary) gets real
// counters; pure unit tests of pkg/mining run with the no-op
// recorder.

import (
	"errors"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/pkg/mining/attest/archcheck"
)

// MiningMetricsRecorder is the narrow surface
// pkg/mining/verifier.go calls into when an arch-spoof or
// hashrate-band rejection fires. Implementations must be safe
// for concurrent use; the production adapter in pkg/monitoring
// uses sync/atomic.
type MiningMetricsRecorder interface {
	// RecordArchSpoofRejected increments the
	// `qsdm_attest_archspoof_rejected_total{reason}` counter.
	// Reason MUST come from the
	// monitoring.ArchSpoofRejectReason* enum (or the package
	// will silently bucket it into "unknown_arch").
	RecordArchSpoofRejected(reason string)

	// RecordHashrateRejected increments the
	// `qsdm_attest_hashrate_rejected_total{arch}` counter.
	// arch is the canonical wire form (e.g. "hopper").
	RecordHashrateRejected(arch string)
}

// Mirror constants for the canonical reason tags so
// pkg/mining doesn't import pkg/monitoring just for the
// strings. Kept in sync with monitoring.ArchSpoofRejectReason*
// by the cross-package test (TODO).
const (
	ArchSpoofRejectReasonUnknownArch     = "unknown_arch"
	ArchSpoofRejectReasonGPUNameMismatch = "gpu_name_mismatch"
)

// noopMiningRecorder is the package-default. Every method is a
// no-op so pkg/mining unit tests run without monitoring wiring.
type noopMiningRecorder struct{}

func (noopMiningRecorder) RecordArchSpoofRejected(string) {}
func (noopMiningRecorder) RecordHashrateRejected(string)  {}

// miningRecorderHolder satisfies atomic.Value's "all stored
// values must share an identical concrete type" constraint,
// the standard idiom for atomic.Value of an interface.
type miningRecorderHolder struct {
	r MiningMetricsRecorder
}

var miningMetricsRecorder atomic.Value // holds miningRecorderHolder

func init() {
	miningMetricsRecorder.Store(miningRecorderHolder{r: noopMiningRecorder{}})
}

// SetMiningMetricsRecorder installs the recorder.
// pkg/monitoring calls this from its init() with a real
// Prometheus-backed adapter; tests can call it with a fake.
// Pass nil to detach (recorder reverts to the no-op default).
//
// Safe for concurrent use with the read path
// (atomic.Value.Store / Load).
func SetMiningMetricsRecorder(r MiningMetricsRecorder) {
	if r == nil {
		miningMetricsRecorder.Store(miningRecorderHolder{r: noopMiningRecorder{}})
		return
	}
	miningMetricsRecorder.Store(miningRecorderHolder{r: r})
}

// miningMetrics returns the active recorder, never nil. Hot
// path: a single atomic.Load + interface dispatch per proof
// rejection.
func miningMetrics() MiningMetricsRecorder {
	v := miningMetricsRecorder.Load()
	if v == nil {
		return noopMiningRecorder{}
	}
	h, ok := v.(miningRecorderHolder)
	if !ok || h.r == nil {
		return noopMiningRecorder{}
	}
	return h.r
}

// recordArchSpoofRejection inspects err for known archcheck
// sentinels and records the matching counter. Called from the
// post-fork branch of Verifier.Verify on any error returned by
// archcheck.ValidateOuterArch or the per-type
// AttestationVerifier (which may itself return
// ErrArchGPUNameMismatch via the HMAC verifier's step 8).
//
// Errors that don't wrap one of the recognised sentinels are
// not recorded here — they're either generic crypto failures
// (already counted by the HMAC / CC verifier's own metrics if
// any) or unrelated rejection paths.
func recordArchSpoofRejection(err error) {
	switch {
	case errors.Is(err, archcheck.ErrArchUnknown):
		miningMetrics().RecordArchSpoofRejected(ArchSpoofRejectReasonUnknownArch)
	case errors.Is(err, archcheck.ErrArchGPUNameMismatch):
		miningMetrics().RecordArchSpoofRejected(ArchSpoofRejectReasonGPUNameMismatch)
	default:
		// Not an archcheck rejection; do not bucket into the
		// "unknown_arch" counter (which would conflate
		// "claimed an unknown arch" with "HMAC failed for an
		// unrelated reason"). Drop silently — the outer
		// verifier still emits the RejectError to the caller.
	}
}

// recordHashrateRejection records a hashrate-out-of-band
// rejection against the canonical arch the validator
// canonicalised to. Always called after
// archcheck.ValidateClaimedHashrate returns non-nil, so the
// arch is always a valid Architecture.
func recordHashrateRejection(arch archcheck.Architecture) {
	miningMetrics().RecordHashrateRejected(string(arch))
}
