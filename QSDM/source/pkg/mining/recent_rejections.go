package mining

// recent_rejections.go: dependency-inverted publisher for the
// v2 attestation §4.6 rejection ring buffer.
//
// Rationale (mirrors metrics.go):
//
//	pkg/mining MUST NOT import pkg/mining/attest/recentrejects
//	directly without dependency inversion. The store is itself a
//	pkg/mining/attest/* package — under Go's strict acyclicity
//	rules a direct import would close pkg/mining → recentrejects
//	→ pkg/mining (for, e.g., archcheck.Architecture types). Even
//	when the cycle is technically severable, dependency inversion
//	keeps the verifier's hot path free of any concrete store
//	implementation: tests and synthetic builds run with the
//	default no-op publisher; production binaries install the
//	bounded ring via SetRejectionRecorder at boot.
//
// The interface is deliberately narrow — one Record method
// taking a value-typed RejectionEvent. Adding a new method is a
// breaking change for every implementer (the no-op default
// included), so the surface stays minimal.

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining/attest/archcheck"
)

// RejectionEvent is the structured payload the verifier ships
// to the recorder on every §4.6 rejection. Fields are an
// abstract view of recentrejects.Rejection — pkg/mining cannot
// reference the concrete type without re-introducing the cycle
// the dependency inversion was designed to break.
//
// Field names mirror the wire shape of api.RecentRejectionView
// for grep-discoverability, but pkg/mining keeps no such
// dependency: the concrete recorder is responsible for
// translating between the three layers (verifier → mining
// publisher → store).
type RejectionEvent struct {
	// Kind is one of the RejectionKind* constants below
	// (mirrored from recentrejects to avoid the import).
	Kind RejectionKind

	// Reason is the metrics-bucket label for archspoof_*
	// kinds; "" for hashrate (the arch label is on Arch).
	Reason string

	// Arch is the canonical arch string. Raw operator-supplied
	// for ArchSpoofUnknown; canonicalised for everything else.
	Arch string

	// RecordedAt is the wall-clock time of the rejection. Zero
	// means "let the store fill it".
	RecordedAt time.Time

	// Height is the proof's claimed chain height (0 if
	// unavailable).
	Height uint64

	// MinerAddr is the proof's miner_addr (empty if the
	// envelope did not parse far enough).
	MinerAddr string

	// GPUName is the bundle-reported GPU name (HMAC paths only).
	GPUName string

	// CertSubject is the leaf cert Subject.CommonName (CC paths
	// only).
	CertSubject string

	// Detail is the verifier's RejectError detail. The store
	// truncates this defensively; passing the full string is
	// safe.
	Detail string
}

// RejectionKind is the wire enum for the §4.6 rejection sites.
// String values MUST match recentrejects.RejectionKind* —
// kept in sync by the cross-package test in
// pkg/mining/attest/recentrejects/recentrejects_kind_test.go
// (TODO).
type RejectionKind string

const (
	RejectionKindArchSpoofUnknown        RejectionKind = "archspoof_unknown_arch"
	RejectionKindArchSpoofGPUNameMismatch RejectionKind = "archspoof_gpu_name_mismatch"
	RejectionKindArchSpoofCCSubjectMismatch RejectionKind = "archspoof_cc_subject_mismatch"
	RejectionKindHashrateOutOfBand       RejectionKind = "hashrate_out_of_band"
)

// RejectionRecorder is the narrow surface the verifier calls
// into on every §4.6 rejection. Implementations must be safe
// for concurrent use; the production adapter wraps the
// pkg/mining/attest/recentrejects.Store with its own RWMutex.
type RejectionRecorder interface {
	// Record appends an event. Must not block — the verifier
	// hot path calls this synchronously.
	Record(ev RejectionEvent)
}

// noopRejectionRecorder is the package-default. Pure unit
// tests of pkg/mining run with this so they never accumulate
// store state across runs.
type noopRejectionRecorder struct{}

func (noopRejectionRecorder) Record(RejectionEvent) {}

// rejectionRecorderHolder satisfies atomic.Value's "all stored
// values must share an identical concrete type" constraint.
type rejectionRecorderHolder struct {
	r RejectionRecorder
}

var rejectionRecorder atomic.Value // holds rejectionRecorderHolder

func init() {
	rejectionRecorder.Store(rejectionRecorderHolder{r: noopRejectionRecorder{}})
}

// SetRejectionRecorder installs the recorder. internal/v2wiring
// calls this at boot with a bounded recentrejects.Store wrapper;
// tests can call it with a fake. Pass nil to detach (recorder
// reverts to the no-op default).
//
// Safe for concurrent use with the read path
// (atomic.Value.Store / Load).
func SetRejectionRecorder(r RejectionRecorder) {
	if r == nil {
		rejectionRecorder.Store(rejectionRecorderHolder{r: noopRejectionRecorder{}})
		return
	}
	rejectionRecorder.Store(rejectionRecorderHolder{r: r})
}

// currentRejectionRecorder returns the active recorder, never
// nil. One atomic.Load + interface dispatch per rejection on
// the verifier hot path — same posture as miningMetrics().
func currentRejectionRecorder() RejectionRecorder {
	v := rejectionRecorder.Load()
	if v == nil {
		return noopRejectionRecorder{}
	}
	h, ok := v.(rejectionRecorderHolder)
	if !ok || h.r == nil {
		return noopRejectionRecorder{}
	}
	return h.r
}

// rejectionEventFromArchSpoof inspects err for known archcheck
// sentinels and synthesises the matching event for the ring.
// Returns ok=false (and a zero event) if err is not an
// archcheck rejection — those are not bucketed into the store
// for the same reason recordArchSpoofRejection drops them from
// the metrics counter (avoid conflating "claimed an unknown
// arch" with "HMAC failed for an unrelated reason").
//
// The verifier passes the full Proof so we can populate
// MinerAddr/Height/Arch without a second walk. GPU name and
// cert subject come from the per-type verifier as side
// channels; the verifier supplies them through the
// RejectionEvent literal at the call site (this helper picks
// the Kind/Reason/Arch fields).
func rejectionEventFromArchSpoof(err error, p *Proof) (RejectionEvent, bool) {
	if err == nil || p == nil {
		return RejectionEvent{}, false
	}
	ev := RejectionEvent{
		Arch:      p.Attestation.GPUArch,
		Height:    p.Height,
		MinerAddr: p.MinerAddr,
		Detail:    err.Error(),
	}
	switch {
	case errors.Is(err, archcheck.ErrArchUnknown):
		ev.Kind = RejectionKindArchSpoofUnknown
		ev.Reason = ArchSpoofRejectReasonUnknownArch
	case errors.Is(err, archcheck.ErrArchGPUNameMismatch):
		ev.Kind = RejectionKindArchSpoofGPUNameMismatch
		ev.Reason = ArchSpoofRejectReasonGPUNameMismatch
	case errors.Is(err, archcheck.ErrArchCertSubjectMismatch):
		ev.Kind = RejectionKindArchSpoofCCSubjectMismatch
		ev.Reason = ArchSpoofRejectReasonCCSubjectMismatch
	default:
		return RejectionEvent{}, false
	}
	return ev, true
}

// rejectionEventFromHashrate builds the structured event for a
// hashrate-band rejection. arch is the canonical
// architecture the validator resolved to (always non-empty
// because the caller computed it via archcheck.ValidateOuterArch
// before this point).
func rejectionEventFromHashrate(arch archcheck.Architecture, p *Proof, err error) RejectionEvent {
	ev := RejectionEvent{
		Kind:   RejectionKindHashrateOutOfBand,
		Arch:   string(arch),
		Detail: "",
	}
	if p != nil {
		ev.Height = p.Height
		ev.MinerAddr = p.MinerAddr
	}
	if err != nil {
		ev.Detail = err.Error()
	}
	return ev
}

// recordRejectionForArchSpoof is the verifier-side hot-path
// helper. Synthesises the event and forwards to the active
// recorder; silently drops if err is not a known archcheck
// sentinel (matches recordArchSpoofRejection's posture of "we
// only label things we recognise").
//
// gpuName / certSubject are optional — pass "" when the caller
// (outer verifier, before per-type dispatch) does not have them.
// The HMAC verifier could in principle plumb gpuName through a
// future side channel; today the bundle is parsed inside
// pkg/mining/attest/hmac and re-extracting it here would
// re-introduce the import boundary the dependency inversion is
// designed to break. The CertSubject path is the same shape
// for cc/. A future enhancement can add an interface method on
// the per-type AttestationVerifier to expose these strings;
// until then the ring captures the empty-string defaults and
// dashboards key on Reason/Arch instead.
func recordRejectionForArchSpoof(err error, p *Proof, gpuName, certSubject string) {
	ev, ok := rejectionEventFromArchSpoof(err, p)
	if !ok {
		return
	}
	if gpuName != "" {
		ev.GPUName = gpuName
	}
	if certSubject != "" {
		ev.CertSubject = certSubject
	}
	currentRejectionRecorder().Record(ev)
}

// recordRejectionForHashrate is the hashrate-band counterpart
// to recordRejectionForArchSpoof. Always records (the caller
// guards against a non-canonical arch upstream).
func recordRejectionForHashrate(arch archcheck.Architecture, p *Proof, err error) {
	currentRejectionRecorder().Record(rejectionEventFromHashrate(arch, p, err))
}
