package chain

import (
	"sync"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

// enrollmentContractIDForTest exposes the enrollment contract
// id without making the test file aware of the import path
// (mirrors how the rest of the test files do it).
func enrollmentContractIDForTest() string {
	return enrollment.ContractID
}

// events_test.go: end-to-end coverage of metrics + event
// emission for both SlashApplier and EnrollmentApplier.
//
// Strategy: install a recordingRecorder + recordingPublisher,
// drive the applier through one of each outcome (applied,
// rejected, sweep), and assert the side-effect counts. We
// don't tie the assertions to the production pkg/monitoring
// counters because (a) those are global and any other test
// can race them, and (b) the chain.MetricsRecorder interface
// is the contract — pkg/monitoring is just one impl.

// recordingRecorder is a chain.MetricsRecorder that captures
// every Record* call into a slice for inspection. Safe for
// concurrent use; tests serialize through it but the locking
// keeps -race happy.
type recordingRecorder struct {
	mu sync.Mutex

	slashApplied       []slashAppliedRec
	slashRewards       []slashRewardRec
	slashRejected      []string
	slashAutoRevoked   []string
	enrollApplied      int
	unenrollApplied    int
	enrollRejected     []string
	unenrollRejected   []string
	enrollSwept        uint64
}

type slashAppliedRec struct {
	Kind         string
	DrainedDust  uint64
}

type slashRewardRec struct {
	Rewarded uint64
	Burned   uint64
}

func (r *recordingRecorder) RecordSlashApplied(kind string, drainedDust uint64) {
	r.mu.Lock()
	r.slashApplied = append(r.slashApplied, slashAppliedRec{Kind: kind, DrainedDust: drainedDust})
	r.mu.Unlock()
}

func (r *recordingRecorder) RecordSlashReward(rewardedDust, burnedDust uint64) {
	r.mu.Lock()
	r.slashRewards = append(r.slashRewards, slashRewardRec{Rewarded: rewardedDust, Burned: burnedDust})
	r.mu.Unlock()
}

func (r *recordingRecorder) RecordSlashRejected(reason string) {
	r.mu.Lock()
	r.slashRejected = append(r.slashRejected, reason)
	r.mu.Unlock()
}

func (r *recordingRecorder) RecordSlashAutoRevoke(reason string) {
	r.mu.Lock()
	r.slashAutoRevoked = append(r.slashAutoRevoked, reason)
	r.mu.Unlock()
}

func (r *recordingRecorder) RecordEnrollmentApplied() {
	r.mu.Lock()
	r.enrollApplied++
	r.mu.Unlock()
}

func (r *recordingRecorder) RecordUnenrollmentApplied() {
	r.mu.Lock()
	r.unenrollApplied++
	r.mu.Unlock()
}

func (r *recordingRecorder) RecordEnrollmentRejected(reason string) {
	r.mu.Lock()
	r.enrollRejected = append(r.enrollRejected, reason)
	r.mu.Unlock()
}

func (r *recordingRecorder) RecordUnenrollmentRejected(reason string) {
	r.mu.Lock()
	r.unenrollRejected = append(r.unenrollRejected, reason)
	r.mu.Unlock()
}

func (r *recordingRecorder) RecordEnrollmentUnbondSwept(count uint64) {
	r.mu.Lock()
	r.enrollSwept += count
	r.mu.Unlock()
}

// recordingPublisher captures every event for inspection.
type recordingPublisher struct {
	mu     sync.Mutex
	slash  []MiningSlashEvent
	enroll []EnrollmentEvent
}

func (p *recordingPublisher) PublishMiningSlash(ev MiningSlashEvent) {
	p.mu.Lock()
	p.slash = append(p.slash, ev)
	p.mu.Unlock()
}

func (p *recordingPublisher) PublishEnrollment(ev EnrollmentEvent) {
	p.mu.Lock()
	p.enroll = append(p.enroll, ev)
	p.mu.Unlock()
}

// withRecordingRecorder swaps in a recorder for the duration
// of the test, restoring the previous one (typically the
// pkg/monitoring adapter) on cleanup. Returns the recorder so
// the caller can read it back.
func withRecordingRecorder(t *testing.T) *recordingRecorder {
	t.Helper()
	prev := metrics()
	r := &recordingRecorder{}
	SetChainMetricsRecorder(r)
	t.Cleanup(func() { SetChainMetricsRecorder(prev) })
	return r
}

// ---- SlashApplier metrics + event tests --------------------

func TestSlashApplier_Emits_AppliedMetricsAndEvent(t *testing.T) {
	rec := withRecordingRecorder(t)
	pub := &recordingPublisher{}

	// Fixture seeds a 10 CELL bond. Verifier cap 50 CELL is
	// generous so the actual slash is clamped by the bond at
	// 10 CELL → full drain → auto-revoke. Reward at 5000 bps
	// = 5 CELL, burn = 5 CELL.
	const bondDust uint64 = 10 * 100_000_000
	fx := buildSlashFixture(t, 5000, 50*100_000_000)
	fx.slasher.Publisher = pub

	payload := slashing.SlashPayload{
		NodeID:          fx.nodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("evidence-events-applied"),
		SlashAmountDust: 50 * 100_000_000,
	}
	if err := fx.slasher.ApplySlashTx(buildSlashTx("slasher-addr", 0, 0.001, payload), 101); err != nil {
		t.Fatalf("slash apply: %v", err)
	}

	if len(rec.slashApplied) != 1 {
		t.Fatalf("slashApplied len: got %d, want 1", len(rec.slashApplied))
	}
	if got, want := rec.slashApplied[0].Kind, "forged-attestation"; got != want {
		t.Errorf("slashApplied[0].Kind: %q want %q", got, want)
	}
	if got, want := rec.slashApplied[0].DrainedDust, bondDust; got != want {
		t.Errorf("slashApplied[0].DrainedDust: %d want %d", got, want)
	}
	if len(rec.slashRewards) != 1 {
		t.Fatalf("slashRewards len: got %d", len(rec.slashRewards))
	}
	if rec.slashRewards[0].Rewarded == 0 || rec.slashRewards[0].Burned == 0 {
		t.Errorf("expected non-zero reward and burn, got %+v", rec.slashRewards[0])
	}
	if len(rec.slashAutoRevoked) != 1 || rec.slashAutoRevoked[0] != SlashAutoRevokeReasonFullDrain {
		t.Errorf("slashAutoRevoked: got %v, want [%q]", rec.slashAutoRevoked, SlashAutoRevokeReasonFullDrain)
	}

	if len(pub.slash) != 1 {
		t.Fatalf("publisher slash events: got %d, want 1", len(pub.slash))
	}
	ev := pub.slash[0]
	if ev.Outcome != SlashOutcomeApplied {
		t.Errorf("Outcome: %q want %q", ev.Outcome, SlashOutcomeApplied)
	}
	if ev.SlashedDust != bondDust {
		t.Errorf("SlashedDust: %d want %d", ev.SlashedDust, bondDust)
	}
	if ev.RewardedDust+ev.BurnedDust != ev.SlashedDust {
		t.Errorf("reward+burn != slashed: %+v", ev)
	}
	if !ev.AutoRevoked {
		t.Error("expected AutoRevoked=true on full-drain")
	}
}

func TestSlashApplier_Emits_RejectedMetricsAndEvent(t *testing.T) {
	rec := withRecordingRecorder(t)
	pub := &recordingPublisher{}

	fx := buildSlashFixture(t, 0, 1*100_000_000)
	fx.slasher.Publisher = pub

	// Wrong contract id → reject path.
	tx := buildSlashTx("slasher-addr", 0, 0.001, slashing.SlashPayload{})
	tx.ContractID = "qsdm/not-slash/v1"
	err := fx.slasher.ApplySlashTx(tx, 101)
	if err == nil {
		t.Fatal("expected wrong-contract error")
	}

	if len(rec.slashRejected) != 1 || rec.slashRejected[0] != SlashRejectReasonWrongContract {
		t.Errorf("slashRejected: got %v, want [%q]", rec.slashRejected, SlashRejectReasonWrongContract)
	}
	if len(rec.slashApplied) != 0 {
		t.Errorf("slashApplied should be empty on reject, got %d", len(rec.slashApplied))
	}
	if len(pub.slash) != 1 {
		t.Fatalf("publisher slash events: got %d, want 1", len(pub.slash))
	}
	if pub.slash[0].Outcome != SlashOutcomeRejected {
		t.Errorf("Outcome: %q want %q", pub.slash[0].Outcome, SlashOutcomeRejected)
	}
	if pub.slash[0].RejectReason != SlashRejectReasonWrongContract {
		t.Errorf("RejectReason: %q", pub.slash[0].RejectReason)
	}
	if pub.slash[0].Err == nil {
		t.Error("expected Err on reject event")
	}
}

func TestSlashApplier_RejectedDecode_RecordsDecodeReason(t *testing.T) {
	rec := withRecordingRecorder(t)
	pub := &recordingPublisher{}

	fx := buildSlashFixture(t, 0, 1*100_000_000)
	fx.slasher.Publisher = pub

	tx := &mempool.Tx{
		Sender:     "slasher-addr",
		ContractID: slashing.ContractID,
		Payload:    []byte(`{not-json`),
		Nonce:      0,
		Fee:        0.001,
	}
	if err := fx.slasher.ApplySlashTx(tx, 101); err == nil {
		t.Fatal("expected decode error")
	}
	if len(rec.slashRejected) != 1 || rec.slashRejected[0] != SlashRejectReasonDecode {
		t.Errorf("slashRejected: got %v, want [%q]", rec.slashRejected, SlashRejectReasonDecode)
	}
}

// ---- EnrollmentApplier event tests -------------------------

func TestEnrollmentApplier_Emits_AppliedMetricsAndEvent(t *testing.T) {
	rec := withRecordingRecorder(t)
	pub := &recordingPublisher{}

	a := aliceWallet(t, 100)
	a.Publisher = pub
	tx := fxEnrollTx(t, fxAlice, 0)
	if err := a.ApplyEnrollmentTx(tx, 100); err != nil {
		t.Fatalf("enroll apply: %v", err)
	}

	if rec.enrollApplied != 1 {
		t.Errorf("enrollApplied: got %d, want 1", rec.enrollApplied)
	}
	if len(pub.enroll) != 1 {
		t.Fatalf("publisher enroll events: got %d, want 1", len(pub.enroll))
	}
	if pub.enroll[0].Kind != EnrollmentEventEnrollApplied {
		t.Errorf("Kind: %q", pub.enroll[0].Kind)
	}
	if pub.enroll[0].Sender != fxAlice {
		t.Errorf("Sender: got %q, want %q", pub.enroll[0].Sender, fxAlice)
	}
	if pub.enroll[0].StakeDust == 0 {
		t.Error("StakeDust not populated on applied event")
	}
}

func TestEnrollmentApplier_Emits_RejectedDecodeReason(t *testing.T) {
	rec := withRecordingRecorder(t)
	pub := &recordingPublisher{}

	a := aliceWallet(t, 100)
	a.Publisher = pub

	tx := &mempool.Tx{
		Sender:     fxAlice,
		Payload:    []byte(`{"not":"valid-enrollment"`),
		ContractID: enrollmentContractIDForTest(),
	}
	if err := a.ApplyEnrollmentTx(tx, 100); err == nil {
		t.Fatal("expected decode failure")
	}
	if len(rec.enrollRejected) != 1 || rec.enrollRejected[0] != EnrollRejectReasonDecode {
		t.Errorf("enrollRejected: got %v, want [%q]", rec.enrollRejected, EnrollRejectReasonDecode)
	}
	if len(pub.enroll) != 1 || pub.enroll[0].Kind != EnrollmentEventEnrollRejected {
		t.Errorf("expected one enroll-rejected event, got %+v", pub.enroll)
	}
}
