package mining

// verifier_recentrejects_test.go: producer-side coverage for
// the §4.6 recent-rejections ring. Validates that the
// outer-Verifier hot path actually invokes
// currentRejectionRecorder().Record(...) on each of the
// rejection sites it owns:
//
//   - archcheck.ValidateOuterArch failure → archspoof_unknown_arch
//   - archcheck.ValidateClaimedHashrate failure → hashrate_out_of_band
//   - per-type AttestationVerifier returning an arch-sentinel
//     error → archspoof_gpu_name_mismatch (HMAC-path) /
//     archspoof_cc_subject_mismatch (CC-path)
//
// Why the unit-test layer is the right home for this:
//
//   - The dispatch from RejectError → store.Record(...) is one
//     atomic.Load + interface call; testing it against a fake
//     recorder gives byte-exact coverage without a full v2wiring
//     rig.
//   - Integration coverage of "the same store landed under both
//     producer and consumer adapters" lives in
//     internal/v2wiring/v2wiring_recentrejects_test.go.

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining/attest/archcheck"
)

// captureRecorder is a thread-safe RejectionRecorder fake. The
// verifier hot path may run on multiple goroutines in
// production, but this file's tests are single-threaded; the
// mutex is paranoia not necessity.
type captureRecorder struct {
	mu     sync.Mutex
	events []RejectionEvent
}

func (c *captureRecorder) Record(ev RejectionEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}

func (c *captureRecorder) snapshot() []RejectionEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]RejectionEvent, len(c.events))
	copy(out, c.events)
	return out
}

// installCaptureRecorder swaps the package recorder for a fresh
// capture and restores the prior on cleanup. Mirrors the
// resetForkV2 helper's posture — no shared state across tests.
func installCaptureRecorder(t *testing.T) *captureRecorder {
	t.Helper()
	c := &captureRecorder{}
	SetRejectionRecorder(c)
	t.Cleanup(func() { SetRejectionRecorder(nil) })
	return c
}

// TestRecorder_FiresOnUnknownArch asserts that an
// out-of-allowlist gpu_arch lands a RejectionEvent with kind
// archspoof_unknown_arch on the recorder, with the raw
// (rejected) gpu_arch string preserved.
func TestRecorder_FiresOnUnknownArch(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(50)
	cap := installCaptureRecorder(t)

	stub := &recordingVerifier{
		onVerify: func(_ Proof, _ time.Time) error { return nil },
	}
	v, err := NewVerifier(VerifierConfig{
		EpochParams:      NewEpochParams(),
		DifficultyParams: NewDifficultyAdjusterParams(),
		Chain:            &fakeChain{tip: 200, headers: map[uint64][32]byte{100: {0x01}}},
		Addresses:        permissiveAddr{},
		Dedup:            NewProofIDSet(16),
		Quarantine:       NewQuarantineSet(),
		DAGProvider:      func(uint64) (DAG, error) { return nil, errors.New("unused") },
		WorkSetProvider:  func(uint64) (WorkSet, error) { return WorkSet{}, errors.New("unused") },
		DifficultyAt:     func(uint64) (*big.Int, error) { return nil, errors.New("unused") },
		Attestation:      stub,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	p := buildV2Proof(t, AttestationTypeHMAC)
	p.Attestation.GPUArch = "future-arch-2099"
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	_, _ = v.Verify(raw, 150)

	events := cap.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 captured event, got %d: %+v", len(events), events)
	}
	got := events[0]
	if got.Kind != RejectionKindArchSpoofUnknown {
		t.Errorf("kind: got %q, want %q", got.Kind, RejectionKindArchSpoofUnknown)
	}
	if got.Reason != ArchSpoofRejectReasonUnknownArch {
		t.Errorf("reason: got %q", got.Reason)
	}
	if got.Arch != "future-arch-2099" {
		t.Errorf("arch (raw): got %q", got.Arch)
	}
	if got.Height != p.Height {
		t.Errorf("height: got %d, want %d", got.Height, p.Height)
	}
	if got.MinerAddr != p.MinerAddr {
		t.Errorf("miner_addr: got %q, want %q", got.MinerAddr, p.MinerAddr)
	}
}

// TestRecorder_FiresOnHashrateOutOfBand asserts that a hashrate-
// band rejection lands a RejectionEvent with kind
// hashrate_out_of_band against the canonical arch.
func TestRecorder_FiresOnHashrateOutOfBand(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(50)
	cap := installCaptureRecorder(t)

	stub := &recordingVerifier{
		onVerify: func(_ Proof, _ time.Time) error { return nil },
	}
	v, err := NewVerifier(VerifierConfig{
		EpochParams:      NewEpochParams(),
		DifficultyParams: NewDifficultyAdjusterParams(),
		Chain:            &fakeChain{tip: 200, headers: map[uint64][32]byte{100: {0x01}}},
		Addresses:        permissiveAddr{},
		Dedup:            NewProofIDSet(16),
		Quarantine:       NewQuarantineSet(),
		DAGProvider:      func(uint64) (DAG, error) { return nil, errors.New("unused") },
		WorkSetProvider:  func(uint64) (WorkSet, error) { return WorkSet{}, errors.New("unused") },
		DifficultyAt:     func(uint64) (*big.Int, error) { return nil, errors.New("unused") },
		Attestation:      stub,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	p := buildV2Proof(t, AttestationTypeHMAC)
	p.Attestation.GPUArch = "hopper"
	// Way above the hopper band ceiling — guaranteed reject.
	p.Attestation.ClaimedHashrateHPS = 1<<63 - 1
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	_, _ = v.Verify(raw, 150)

	events := cap.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 captured event, got %d: %+v", len(events), events)
	}
	got := events[0]
	if got.Kind != RejectionKindHashrateOutOfBand {
		t.Errorf("kind: got %q", got.Kind)
	}
	// Hashrate kind does not populate Reason — the arch label
	// carries the bucket information on its own series.
	if got.Reason != "" {
		t.Errorf("reason: got %q, want empty", got.Reason)
	}
	if got.Arch != "hopper" {
		t.Errorf("arch (canonical): got %q", got.Arch)
	}
}

// TestRecorder_FiresOnGPUNameMismatch asserts that a per-type
// verifier returning archcheck.ErrArchGPUNameMismatch is
// captured as kind archspoof_gpu_name_mismatch.
func TestRecorder_FiresOnGPUNameMismatch(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(50)
	cap := installCaptureRecorder(t)

	stub := &recordingVerifier{
		onVerify: func(_ Proof, _ time.Time) error {
			// Wrap the sentinel — same shape as the HMAC
			// verifier's step-8 rejection in production.
			return archcheck.ErrArchGPUNameMismatch
		},
	}
	v, err := NewVerifier(VerifierConfig{
		EpochParams:      NewEpochParams(),
		DifficultyParams: NewDifficultyAdjusterParams(),
		Chain:            &fakeChain{tip: 200, headers: map[uint64][32]byte{100: {0x01}}},
		Addresses:        permissiveAddr{},
		Dedup:            NewProofIDSet(16),
		Quarantine:       NewQuarantineSet(),
		DAGProvider:      func(uint64) (DAG, error) { return nil, errors.New("unused") },
		WorkSetProvider:  func(uint64) (WorkSet, error) { return WorkSet{}, errors.New("unused") },
		DifficultyAt:     func(uint64) (*big.Int, error) { return nil, errors.New("unused") },
		Attestation:      stub,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	p := buildV2Proof(t, AttestationTypeHMAC)
	p.Attestation.GPUArch = "hopper"
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	_, _ = v.Verify(raw, 150)

	events := cap.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 captured event, got %d: %+v", len(events), events)
	}
	got := events[0]
	if got.Kind != RejectionKindArchSpoofGPUNameMismatch {
		t.Errorf("kind: got %q", got.Kind)
	}
	if got.Reason != ArchSpoofRejectReasonGPUNameMismatch {
		t.Errorf("reason: got %q", got.Reason)
	}
}

// TestRecorder_FiresOnCCSubjectMismatch asserts that a per-type
// verifier returning archcheck.ErrArchCertSubjectMismatch is
// captured as kind archspoof_cc_subject_mismatch.
func TestRecorder_FiresOnCCSubjectMismatch(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(50)
	cap := installCaptureRecorder(t)

	stub := &recordingVerifier{
		onVerify: func(_ Proof, _ time.Time) error {
			return archcheck.ErrArchCertSubjectMismatch
		},
	}
	v, err := NewVerifier(VerifierConfig{
		EpochParams:      NewEpochParams(),
		DifficultyParams: NewDifficultyAdjusterParams(),
		Chain:            &fakeChain{tip: 200, headers: map[uint64][32]byte{100: {0x01}}},
		Addresses:        permissiveAddr{},
		Dedup:            NewProofIDSet(16),
		Quarantine:       NewQuarantineSet(),
		DAGProvider:      func(uint64) (DAG, error) { return nil, errors.New("unused") },
		WorkSetProvider:  func(uint64) (WorkSet, error) { return WorkSet{}, errors.New("unused") },
		DifficultyAt:     func(uint64) (*big.Int, error) { return nil, errors.New("unused") },
		Attestation:      stub,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	p := buildV2Proof(t, AttestationTypeCC)
	p.Attestation.GPUArch = "hopper"
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	_, _ = v.Verify(raw, 150)

	events := cap.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 captured event, got %d: %+v", len(events), events)
	}
	if events[0].Kind != RejectionKindArchSpoofCCSubjectMismatch {
		t.Errorf("kind: got %q", events[0].Kind)
	}
	if events[0].Reason != ArchSpoofRejectReasonCCSubjectMismatch {
		t.Errorf("reason: got %q", events[0].Reason)
	}
}

// TestRecorder_DoesNotFireOnGenericCryptoError asserts that an
// unrelated per-type verifier failure (e.g. HMAC tag mismatch)
// does NOT bucket into the recent-rejections ring. The metrics
// counter has the same posture; this test pins the parity.
func TestRecorder_DoesNotFireOnGenericCryptoError(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(50)
	cap := installCaptureRecorder(t)

	stub := &recordingVerifier{
		onVerify: func(_ Proof, _ time.Time) error {
			return errors.New("hmac: tag mismatch")
		},
	}
	v, err := NewVerifier(VerifierConfig{
		EpochParams:      NewEpochParams(),
		DifficultyParams: NewDifficultyAdjusterParams(),
		Chain:            &fakeChain{tip: 200, headers: map[uint64][32]byte{100: {0x01}}},
		Addresses:        permissiveAddr{},
		Dedup:            NewProofIDSet(16),
		Quarantine:       NewQuarantineSet(),
		DAGProvider:      func(uint64) (DAG, error) { return nil, errors.New("unused") },
		WorkSetProvider:  func(uint64) (WorkSet, error) { return WorkSet{}, errors.New("unused") },
		DifficultyAt:     func(uint64) (*big.Int, error) { return nil, errors.New("unused") },
		Attestation:      stub,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	p := buildV2Proof(t, AttestationTypeHMAC)
	p.Attestation.GPUArch = "hopper"
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	_, _ = v.Verify(raw, 150)

	events := cap.snapshot()
	if len(events) != 0 {
		t.Errorf("generic crypto err must not bucket; got %d events: %+v",
			len(events), events)
	}
}

// TestRecorder_ExtractsGPUNameFromHMACDetail validates the
// structured-detail extraction added with the
// archcheck.RejectionDetail wrapper migration. A per-type
// verifier returning a real archcheck.RejectionDetail (the
// shape ValidateBundleArchConsistencyHMAC now returns) MUST
// surface its GPUName onto the captured RejectionEvent. This
// closes the long-standing "GPUName empty end-to-end" gap
// flagged in the previous commit's TODO.
func TestRecorder_ExtractsGPUNameFromHMACDetail(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(50)
	cap := installCaptureRecorder(t)

	// Build the EXACT shape the production HMAC verifier
	// produces: archcheck.ValidateBundleArchConsistencyHMAC
	// already returns *RejectionDetail, then the verifier
	// wraps with mining.ErrAttestationSignatureInvalid via
	// fmt.Errorf("hmac: %w: %w", err, sentinel).
	stub := &recordingVerifier{
		onVerify: func(_ Proof, _ time.Time) error {
			inner := archcheck.ValidateBundleArchConsistencyHMAC(
				archcheck.ArchHopper, "NVIDIA GeForce RTX 4090")
			return fmt.Errorf("hmac: %w: %w", inner, ErrAttestationSignatureInvalid)
		},
	}
	v, err := NewVerifier(VerifierConfig{
		EpochParams:      NewEpochParams(),
		DifficultyParams: NewDifficultyAdjusterParams(),
		Chain:            &fakeChain{tip: 200, headers: map[uint64][32]byte{100: {0x01}}},
		Addresses:        permissiveAddr{},
		Dedup:            NewProofIDSet(16),
		Quarantine:       NewQuarantineSet(),
		DAGProvider:      func(uint64) (DAG, error) { return nil, errors.New("unused") },
		WorkSetProvider:  func(uint64) (WorkSet, error) { return WorkSet{}, errors.New("unused") },
		DifficultyAt:     func(uint64) (*big.Int, error) { return nil, errors.New("unused") },
		Attestation:      stub,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	p := buildV2Proof(t, AttestationTypeHMAC)
	p.Attestation.GPUArch = "hopper"
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	_, _ = v.Verify(raw, 150)

	events := cap.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(events), events)
	}
	got := events[0]
	if got.Kind != RejectionKindArchSpoofGPUNameMismatch {
		t.Errorf("kind: got %q", got.Kind)
	}
	if got.GPUName != "NVIDIA GeForce RTX 4090" {
		t.Errorf("GPUName must surface from RejectionDetail; got %q", got.GPUName)
	}
	if got.CertSubject != "" {
		t.Errorf("CertSubject must stay empty on HMAC path; got %q", got.CertSubject)
	}
}

// TestRecorder_ExtractsCertSubjectFromCCDetail mirrors the
// HMAC-side test for the CC path.
func TestRecorder_ExtractsCertSubjectFromCCDetail(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(50)
	cap := installCaptureRecorder(t)

	stub := &recordingVerifier{
		onVerify: func(_ Proof, _ time.Time) error {
			inner := archcheck.ValidateBundleArchConsistencyCC(
				archcheck.ArchHopper, "NVIDIA GeForce RTX 4090")
			return fmt.Errorf("cc: arch consistency: %w: %w",
				inner, ErrAttestationSignatureInvalid)
		},
	}
	v, err := NewVerifier(VerifierConfig{
		EpochParams:      NewEpochParams(),
		DifficultyParams: NewDifficultyAdjusterParams(),
		Chain:            &fakeChain{tip: 200, headers: map[uint64][32]byte{100: {0x01}}},
		Addresses:        permissiveAddr{},
		Dedup:            NewProofIDSet(16),
		Quarantine:       NewQuarantineSet(),
		DAGProvider:      func(uint64) (DAG, error) { return nil, errors.New("unused") },
		WorkSetProvider:  func(uint64) (WorkSet, error) { return WorkSet{}, errors.New("unused") },
		DifficultyAt:     func(uint64) (*big.Int, error) { return nil, errors.New("unused") },
		Attestation:      stub,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	p := buildV2Proof(t, AttestationTypeCC)
	p.Attestation.GPUArch = "hopper"
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	_, _ = v.Verify(raw, 150)

	events := cap.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(events), events)
	}
	got := events[0]
	if got.Kind != RejectionKindArchSpoofCCSubjectMismatch {
		t.Errorf("kind: got %q", got.Kind)
	}
	if got.CertSubject != "NVIDIA GeForce RTX 4090" {
		t.Errorf("CertSubject must surface from RejectionDetail; got %q", got.CertSubject)
	}
	if got.GPUName != "" {
		t.Errorf("GPUName must stay empty on CC path; got %q", got.GPUName)
	}
}

// TestRecorder_ExtractsRawArchFromOuterUnknown validates the
// archspoof_unknown_arch path: ValidateOuterArch returns a
// *RejectionDetail with the raw rejected arch in GPUArch (not
// GPUName). The recorder still populates ev.Arch from the proof
// envelope (already covered) and the event detail string
// captures the formatted error — but the key invariant locked
// here is that the structured-detail wrapper is REACHABLE on
// this path too, so a future enhancement (e.g. surfacing the
// allowed list to dashboards) doesn't have to retrofit a
// separate plumbing.
func TestRecorder_ExtractsRawArchFromOuterUnknown(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(50)
	cap := installCaptureRecorder(t)

	stub := &recordingVerifier{
		onVerify: func(_ Proof, _ time.Time) error { return nil },
	}
	v, err := NewVerifier(VerifierConfig{
		EpochParams:      NewEpochParams(),
		DifficultyParams: NewDifficultyAdjusterParams(),
		Chain:            &fakeChain{tip: 200, headers: map[uint64][32]byte{100: {0x01}}},
		Addresses:        permissiveAddr{},
		Dedup:            NewProofIDSet(16),
		Quarantine:       NewQuarantineSet(),
		DAGProvider:      func(uint64) (DAG, error) { return nil, errors.New("unused") },
		WorkSetProvider:  func(uint64) (WorkSet, error) { return WorkSet{}, errors.New("unused") },
		DifficultyAt:     func(uint64) (*big.Int, error) { return nil, errors.New("unused") },
		Attestation:      stub,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	p := buildV2Proof(t, AttestationTypeHMAC)
	p.Attestation.GPUArch = "future-arch-2099"
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	_, _ = v.Verify(raw, 150)

	events := cap.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(events), events)
	}
	if events[0].Kind != RejectionKindArchSpoofUnknown {
		t.Errorf("kind: got %q", events[0].Kind)
	}
	if events[0].Arch != "future-arch-2099" {
		t.Errorf("arch (raw): got %q", events[0].Arch)
	}
	// The detail string carries the formatted rejection — make
	// sure the wrapper round-tripped its allowed-list suffix.
	if !strings.Contains(events[0].Detail, "allowed:") {
		t.Errorf("detail must include allowed list; got %q", events[0].Detail)
	}
}

// TestSetRejectionRecorder_NilFallsBackToNoop asserts that
// passing nil to SetRejectionRecorder reverts to the no-op
// default rather than crashing the verifier on the next
// dispatch.
func TestSetRejectionRecorder_NilFallsBackToNoop(t *testing.T) {
	SetRejectionRecorder(nil)
	t.Cleanup(func() { SetRejectionRecorder(nil) })

	// A non-nil dispatch on the no-op recorder must not panic
	// or block.
	currentRejectionRecorder().Record(RejectionEvent{
		Kind:   RejectionKindArchSpoofUnknown,
		Reason: ArchSpoofRejectReasonUnknownArch,
	})
}
