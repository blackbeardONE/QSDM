package chain

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

// ----- helpers -----------------------------------------------------

// testAcceptVerifier is an EvidenceVerifier that accepts every
// payload and returns a configurable cap. Only for unit tests —
// production code MUST register real verifiers.
type testAcceptVerifier struct {
	kind slashing.EvidenceKind
	cap  uint64
}

func (v testAcceptVerifier) Kind() slashing.EvidenceKind { return v.kind }
func (v testAcceptVerifier) Verify(_ slashing.SlashPayload, _ uint64) (uint64, error) {
	return v.cap, nil
}

// testRejectVerifier always rejects.
type testRejectVerifier struct{ kind slashing.EvidenceKind }

func (v testRejectVerifier) Kind() slashing.EvidenceKind { return v.kind }
func (v testRejectVerifier) Verify(_ slashing.SlashPayload, _ uint64) (uint64, error) {
	return 0, fmt.Errorf("%w: unit-test reject", slashing.ErrEvidenceVerification)
}

type slashFixture struct {
	accounts *AccountStore
	state    *enrollment.InMemoryState
	enrollAp *EnrollmentApplier
	slasher  *SlashApplier
	disp     *slashing.Dispatcher
	offender string
	nodeID   string
}

func buildSlashFixture(t *testing.T, rewardBPS uint16, verifierCap uint64) *slashFixture {
	t.Helper()
	accounts := NewAccountStore()
	// Offender (owner of the enrolled record).
	accounts.Credit("offender-addr", 100.0)
	// Slasher — posts the slash tx.
	accounts.Credit("slasher-addr", 10.0)

	state := enrollment.NewInMemoryState()
	enrollAp := NewEnrollmentApplier(accounts, state)

	disp := slashing.NewDispatcher()
	disp.Register(testAcceptVerifier{
		kind: slashing.EvidenceKindForgedAttestation,
		cap:  verifierCap,
	})

	slasher := NewSlashApplier(accounts, state, disp, rewardBPS)

	// Seed an enrollment for node_id "rig-77" with a 10 CELL stake.
	stakeDust := uint64(10 * 100_000_000)
	payload := enrollment.EnrollPayload{
		Kind:      enrollment.PayloadKindEnroll,
		NodeID:    "rig-77",
		GPUUUID:   "GPU-1234",
		HMACKey:   make([]byte, 32),
		StakeDust: stakeDust,
	}
	for i := range payload.HMACKey {
		payload.HMACKey[i] = 0xAB
	}
	raw, err := enrollment.EncodeEnrollPayload(payload)
	if err != nil {
		t.Fatalf("encode enroll: %v", err)
	}
	enrollTx := &mempool.Tx{
		Sender:     "offender-addr",
		Nonce:      0,
		Fee:        0.01,
		ContractID: enrollment.ContractID,
		Payload:    raw,
	}
	if err := enrollAp.ApplyEnrollmentTx(enrollTx, 100); err != nil {
		t.Fatalf("apply enroll: %v", err)
	}

	return &slashFixture{
		accounts: accounts,
		state:    state,
		enrollAp: enrollAp,
		slasher:  slasher,
		disp:     disp,
		offender: "offender-addr",
		nodeID:   "rig-77",
	}
}

func buildSlashTx(sender string, nonce uint64, fee float64, p slashing.SlashPayload) *mempool.Tx {
	raw, err := slashing.EncodeSlashPayload(p)
	if err != nil {
		panic(err)
	}
	return &mempool.Tx{
		Sender:     sender,
		Nonce:      nonce,
		Fee:        fee,
		ContractID: slashing.ContractID,
		Payload:    raw,
	}
}

// ----- tests -------------------------------------------------------

func TestSlashApplier_ApplySlashTx_HappyPath(t *testing.T) {
	// 50% reward, verifier says the offence is worth 5 CELL.
	verifierCap := uint64(5 * 100_000_000)
	fx := buildSlashFixture(t, 5000, verifierCap)

	// Payload proposes 10 CELL slash, but verifier clamps to 5 CELL.
	payload := slashing.SlashPayload{
		NodeID:          fx.nodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("evidence-v1"),
		SlashAmountDust: 10 * 100_000_000,
	}
	tx := buildSlashTx("slasher-addr", 0, 0.001, payload)

	// Slasher starts at 10.0 CELL, offender enrolled with 10 CELL
	// stake. After slash: offender stake = 5 CELL, slasher gets
	// 2.5 CELL reward (50% of 5 CELL forfeited) minus 0.001 fee.
	if err := fx.slasher.ApplySlashTx(tx, 101); err != nil {
		t.Fatalf("slash apply: %v", err)
	}

	rec, err := fx.state.Lookup(fx.nodeID)
	if err != nil || rec == nil {
		t.Fatalf("record lookup: rec=%v err=%v", rec, err)
	}
	if got, want := rec.StakeDust, verifierCap; got != want {
		t.Errorf("offender stake: got %d dust, want %d dust", got, want)
	}

	slasherAcc, ok := fx.accounts.Get("slasher-addr")
	if !ok {
		t.Fatalf("slasher account missing")
	}
	wantSlasher := 10.0 - 0.001 + 2.5
	if absDiff(slasherAcc.Balance, wantSlasher) > 1e-9 {
		t.Errorf("slasher balance: got %.8f, want %.8f", slasherAcc.Balance, wantSlasher)
	}
	if slasherAcc.Nonce != 1 {
		t.Errorf("slasher nonce: got %d, want 1", slasherAcc.Nonce)
	}
}

func TestSlashApplier_ApplySlashTx_ReplayRejected(t *testing.T) {
	fx := buildSlashFixture(t, 0, uint64(5*100_000_000))
	payload := slashing.SlashPayload{
		NodeID:          fx.nodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("evidence-v1"),
		SlashAmountDust: 100_000_000,
	}
	tx1 := buildSlashTx("slasher-addr", 0, 0.001, payload)
	if err := fx.slasher.ApplySlashTx(tx1, 101); err != nil {
		t.Fatalf("first slash: %v", err)
	}
	tx2 := buildSlashTx("slasher-addr", 1, 0.001, payload)
	err := fx.slasher.ApplySlashTx(tx2, 102)
	if err == nil {
		t.Fatal("replay should have been rejected")
	}
	// The rejection can happen via either the EvidenceSeen
	// pre-check ("already seen") or the MarkEvidenceSeen race
	// ("raced"). Both exercise the replay defence.
	msg := err.Error()
	if !containsAny(msg, []string{"already seen", "already accepted"}) {
		t.Errorf("expected replay-protection error, got %v", err)
	}
}

func TestSlashApplier_ApplySlashTx_VerifierRejects(t *testing.T) {
	fx := buildSlashFixture(t, 0, 0)
	// Replace the accept verifier with a reject one.
	disp := slashing.NewDispatcher()
	disp.Register(testRejectVerifier{kind: slashing.EvidenceKindForgedAttestation})
	fx.slasher = NewSlashApplier(fx.accounts, fx.state, disp, 0)

	payload := slashing.SlashPayload{
		NodeID:          fx.nodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("evidence-v1"),
		SlashAmountDust: 100_000_000,
	}
	tx := buildSlashTx("slasher-addr", 0, 0.001, payload)
	err := fx.slasher.ApplySlashTx(tx, 101)
	if err == nil {
		t.Fatal("reject verifier should error")
	}
	if !errors.Is(err, slashing.ErrEvidenceVerification) {
		t.Errorf("err should wrap ErrEvidenceVerification, got %v", err)
	}

	// Verifier rejection should leave the slasher's nonce & fee
	// untouched (we charge fee AFTER verifier passes).
	acc, _ := fx.accounts.Get("slasher-addr")
	if acc.Nonce != 0 || absDiff(acc.Balance, 10.0) > 1e-9 {
		t.Errorf("verifier reject should not debit: balance=%.8f nonce=%d",
			acc.Balance, acc.Nonce)
	}

	// And the offender's stake must be untouched.
	rec, _ := fx.state.Lookup(fx.nodeID)
	if rec.StakeDust != 10*100_000_000 {
		t.Errorf("offender stake mutated on verifier reject: %d", rec.StakeDust)
	}
}

func TestSlashApplier_ApplySlashTx_UnknownNode(t *testing.T) {
	fx := buildSlashFixture(t, 0, 100_000_000)
	payload := slashing.SlashPayload{
		NodeID:          "does-not-exist",
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("e"),
		SlashAmountDust: 100_000_000,
	}
	tx := buildSlashTx("slasher-addr", 0, 0.001, payload)
	err := fx.slasher.ApplySlashTx(tx, 101)
	if err == nil || !errors.Is(err, slashing.ErrNodeNotEnrolled) {
		t.Errorf("expected ErrNodeNotEnrolled, got %v", err)
	}
}

func TestSlashApplier_ApplySlashTx_WrongContractID(t *testing.T) {
	fx := buildSlashFixture(t, 0, 100_000_000)
	tx := &mempool.Tx{
		Sender:     "slasher-addr",
		Nonce:      0,
		Fee:        0.001,
		ContractID: "wrong/contract/v1",
		Payload:    []byte(`{}`),
	}
	err := fx.slasher.ApplySlashTx(tx, 101)
	if err == nil || !errors.Is(err, ErrNotSlashTx) {
		t.Errorf("expected ErrNotSlashTx, got %v", err)
	}
}

func TestSlashApplier_ApplySlashTx_ZeroFeeRejected(t *testing.T) {
	fx := buildSlashFixture(t, 0, 100_000_000)
	payload := slashing.SlashPayload{
		NodeID:          fx.nodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("e"),
		SlashAmountDust: 100_000_000,
	}
	tx := buildSlashTx("slasher-addr", 0, 0.0, payload)
	err := fx.slasher.ApplySlashTx(tx, 101)
	if err == nil {
		t.Fatal("zero-fee slash should be rejected")
	}
	// Slasher's account must be untouched.
	acc, _ := fx.accounts.Get("slasher-addr")
	if acc.Nonce != 0 || absDiff(acc.Balance, 10.0) > 1e-9 {
		t.Errorf("zero-fee reject mutated slasher: balance=%.8f nonce=%d",
			acc.Balance, acc.Nonce)
	}
}

func TestSlashApplier_ApplySlashTx_ClampsToAvailableStake(t *testing.T) {
	// Offender has 10 CELL staked. Verifier caps at 50 CELL.
	// Payload asks 50 CELL. Actual slash must clamp at 10 CELL.
	fx := buildSlashFixture(t, 0, 50*100_000_000)
	payload := slashing.SlashPayload{
		NodeID:          fx.nodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("e"),
		SlashAmountDust: 50 * 100_000_000,
	}
	tx := buildSlashTx("slasher-addr", 0, 0.001, payload)
	if err := fx.slasher.ApplySlashTx(tx, 101); err != nil {
		t.Fatalf("slash apply: %v", err)
	}
	rec, _ := fx.state.Lookup(fx.nodeID)
	if rec.StakeDust != 0 {
		t.Errorf("offender stake should be fully drained, got %d", rec.StakeDust)
	}
}

func TestSlashApplier_RewardBPSCap(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("RewardBPS > SlashRewardCap should panic")
		}
	}()
	accounts := NewAccountStore()
	state := enrollment.NewInMemoryState()
	disp := slashing.NewDispatcher()
	_ = NewSlashApplier(accounts, state, disp, SlashRewardCap+1)
}

func TestSlashApplier_NewSlashApplier_NilFieldsPanic(t *testing.T) {
	disp := slashing.NewDispatcher()
	tests := []struct {
		name string
		fn   func()
	}{
		{"nil accounts", func() { _ = NewSlashApplier(nil, enrollment.NewInMemoryState(), disp, 0) }},
		{"nil state", func() { _ = NewSlashApplier(NewAccountStore(), nil, disp, 0) }},
		{"nil dispatcher", func() { _ = NewSlashApplier(NewAccountStore(), enrollment.NewInMemoryState(), nil, 0) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Error("expected panic")
				}
			}()
			tt.fn()
		})
	}
}

func TestSlashApplier_EvidenceFingerprint_Deterministic(t *testing.T) {
	p := slashing.SlashPayload{
		NodeID:          "rig-77",
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("abc"),
		SlashAmountDust: 1,
	}
	h1 := evidenceFingerprint(p)
	h2 := evidenceFingerprint(p)
	if h1 != h2 {
		t.Error("fingerprint should be deterministic")
	}
	// Kind+blob concatenation with a delimiter: changing the
	// split between kind/blob should produce a different hash.
	p2 := slashing.SlashPayload{
		NodeID:          "rig-77",
		EvidenceKind:    slashing.EvidenceKindForgedAttestation + "a",
		EvidenceBlob:    []byte("bc"),
		SlashAmountDust: 1,
	}
	h3 := evidenceFingerprint(p2)
	if h1 == h3 {
		t.Error("fingerprint delimiter should prevent kind|blob boundary collision")
	}
	// Sanity: the hash matches a hand-computed SHA-256 over
	// kind||0x00||blob.
	want := sha256.Sum256(append(append([]byte("forged-attestation"), 0x00), []byte("abc")...))
	if h1 != want {
		t.Errorf("fingerprint: got %x, want %x", h1, want)
	}
}

func TestSlashApplier_ConcurrentSlashRace(t *testing.T) {
	// Two goroutines try to apply the SAME slash evidence from
	// the SAME slasher (different nonces). Exactly one must
	// succeed; the loser sees a raced/seen rejection.
	fx := buildSlashFixture(t, 5000, 2*100_000_000)
	payload := slashing.SlashPayload{
		NodeID:          fx.nodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("evidence-race"),
		SlashAmountDust: 100_000_000,
	}

	// Seed two nonces for the slasher by pre-bumping via no-op
	// transfer isn't possible here; instead use two fresh
	// fixtures... simpler: fire sequentially since the
	// replay-defence path is the interesting bit.
	tx1 := buildSlashTx("slasher-addr", 0, 0.001, payload)
	tx2 := buildSlashTx("slasher-addr", 1, 0.001, payload)

	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0] = fx.slasher.ApplySlashTx(tx1, 101)
	}()
	go func() {
		defer wg.Done()
		results[1] = fx.slasher.ApplySlashTx(tx2, 102)
	}()
	wg.Wait()

	success := 0
	for _, e := range results {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Errorf("exactly one concurrent slash should succeed, got success=%d errs=%v",
			success, results)
	}
}

// ----- utilities ---------------------------------------------------

func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	// tiny strings.Contains substitute, keeps imports lean
	if len(sub) == 0 {
		return 0
	}
	if len(sub) > len(s) {
		return -1
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
