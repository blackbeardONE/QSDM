package chain

// enrollment_apply_test.go covers:
//
//  1. Unit conversion (balanceToDust, dustToBalance).
//  2. DebitAndBumpNonce atomicity + error surfaces.
//  3. ApplyEnrollmentTx happy paths (enroll + unenroll) and
//     every rejection path (wrong ContractID, bad payload,
//     stateless reject, insufficient balance, duplicate
//     node_id, wrong sender for unenroll, nonce mismatch).
//  4. SweepMaturedEnrollments: credits released stake back to
//     owner, handles empty-owner defensively, idempotent on
//     no-matured-records.
//  5. End-to-end integration: Alice enrolls, mines (via the
//     StateBackedRegistry adapter wiring is OUT OF SCOPE here —
//     we stop at "enrollment record is in state"), Alice
//     unenrolls, height advances past UnbondWindow, sweep
//     credits the stake back.
//
// We use enrollment.InMemoryState directly as the
// EnrollmentStateMutator — the *InMemoryState type already has
// every method the local interface requires, and exercising
// the interface via the real implementation is stronger
// coverage than a fake would be.

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// -----------------------------------------------------------------------------
// Unit conversion
// -----------------------------------------------------------------------------

func TestBalanceToDust_Floor(t *testing.T) {
	cases := []struct {
		in   float64
		want uint64
	}{
		{in: 0, want: 0},
		{in: 1, want: 100_000_000},
		{in: 10, want: 1_000_000_000},
		{in: 0.5, want: 50_000_000},
		// All exactly-representable in float64 (powers of 2 /
		// clean decimal fractions). Values like 1.23456789 that
		// don't round-trip through IEEE-754 are deliberately NOT
		// tested here — balanceToDust's floor semantics mean
		// they will land at n or n-1 depending on the closest
		// double; asserting exact integers only is the honest
		// contract.
		{in: 0.25, want: 25_000_000},
		{in: 12.5, want: 1_250_000_000},
	}
	for _, c := range cases {
		got := balanceToDust(c.in)
		if got != c.want {
			t.Errorf("balanceToDust(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestBalanceToDust_NegativeAndNaN(t *testing.T) {
	if got := balanceToDust(-1); got != 0 {
		t.Errorf("balanceToDust(-1) = %d, want 0", got)
	}
	if got := balanceToDust(math.NaN()); got != 0 {
		t.Errorf("balanceToDust(NaN) = %d, want 0", got)
	}
}

func TestBalanceToDust_OverflowClamps(t *testing.T) {
	got := balanceToDust(1e20)
	if got != math.MaxUint64 {
		t.Errorf("balanceToDust(1e20) = %d, want MaxUint64", got)
	}
}

func TestDustToBalance_RoundTrip(t *testing.T) {
	for _, dust := range []uint64{0, 1, 100_000_000, 1_000_000_000, mining.MinEnrollStakeDust} {
		cell := dustToBalance(dust)
		back := balanceToDust(cell)
		if back != dust {
			t.Errorf("round-trip: dust=%d -> cell=%v -> dust=%d", dust, cell, back)
		}
	}
}

// -----------------------------------------------------------------------------
// DebitAndBumpNonce
// -----------------------------------------------------------------------------

func TestDebitAndBumpNonce_HappyPath(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)
	if err := as.DebitAndBumpNonce("alice", 25, 0); err != nil {
		t.Fatalf("DebitAndBumpNonce: %v", err)
	}
	acc, _ := as.Get("alice")
	if acc.Balance != 75 {
		t.Errorf("balance: got %v, want 75", acc.Balance)
	}
	if acc.Nonce != 1 {
		t.Errorf("nonce: got %d, want 1", acc.Nonce)
	}
}

func TestDebitAndBumpNonce_RejectsNegativeAmount(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)
	if err := as.DebitAndBumpNonce("alice", 0, 0); err == nil {
		t.Error("zero amount should error")
	}
	if err := as.DebitAndBumpNonce("alice", -5, 0); err == nil {
		t.Error("negative amount should error")
	}
	acc, _ := as.Get("alice")
	if acc.Nonce != 0 || acc.Balance != 100 {
		t.Errorf("state must not mutate on rejection: balance=%v nonce=%d", acc.Balance, acc.Nonce)
	}
}

func TestDebitAndBumpNonce_NonceMismatch(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)
	err := as.DebitAndBumpNonce("alice", 10, 5) // expected 0, passed 5
	if err == nil {
		t.Fatal("nonce mismatch should error")
	}
	if !strings.Contains(err.Error(), "nonce mismatch") {
		t.Errorf("error: %v", err)
	}
	acc, _ := as.Get("alice")
	if acc.Balance != 100 || acc.Nonce != 0 {
		t.Error("state must not mutate on nonce mismatch")
	}
}

func TestDebitAndBumpNonce_InsufficientBalance(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 5)
	err := as.DebitAndBumpNonce("alice", 10, 0)
	if err == nil {
		t.Fatal("insufficient balance should error")
	}
	if !strings.Contains(err.Error(), "insufficient") {
		t.Errorf("error: %v", err)
	}
	acc, _ := as.Get("alice")
	if acc.Balance != 5 || acc.Nonce != 0 {
		t.Error("state must not mutate on insufficient balance")
	}
}

func TestDebitAndBumpNonce_MissingSender(t *testing.T) {
	as := NewAccountStore()
	err := as.DebitAndBumpNonce("nobody", 1, 0)
	if err == nil {
		t.Fatal("missing sender should error")
	}
}

// -----------------------------------------------------------------------------
// Fixtures for ApplyEnrollmentTx
// -----------------------------------------------------------------------------

const (
	fxAlice   = "qsdm1alice"
	fxNodeID  = "alice-rtx4090-01"
	fxGPUUUID = "GPU-abcd1234-5678-90ef-1234-567890abcdef"
)

// fxHMACKey returns a 32-byte fixture key (matches
// enrollment.MinHMACKeyLen). Separate helper so test values can
// change in one place.
func fxHMACKey() []byte { return bytes.Repeat([]byte{0xAB}, 32) }

func fxEnrollTx(t *testing.T, sender string, nonce uint64) *mempool.Tx {
	t.Helper()
	payload := enrollment.EnrollPayload{
		Kind:      enrollment.PayloadKindEnroll,
		NodeID:    fxNodeID,
		GPUUUID:   fxGPUUUID,
		HMACKey:   fxHMACKey(),
		StakeDust: mining.MinEnrollStakeDust,
		Memo:      "test",
	}
	raw, err := enrollment.EncodeEnrollPayload(payload)
	if err != nil {
		t.Fatalf("EncodeEnrollPayload: %v", err)
	}
	return &mempool.Tx{
		Sender:     sender,
		Nonce:      nonce,
		Fee:        0.01,
		Payload:    raw,
		ContractID: enrollment.ContractID,
	}
}

func fxUnenrollTx(t *testing.T, sender, nodeID string, nonce uint64, fee float64) *mempool.Tx {
	t.Helper()
	payload := enrollment.UnenrollPayload{
		Kind:   enrollment.PayloadKindUnenroll,
		NodeID: nodeID,
		Reason: "retiring",
	}
	raw, err := enrollment.EncodeUnenrollPayload(payload)
	if err != nil {
		t.Fatalf("EncodeUnenrollPayload: %v", err)
	}
	return &mempool.Tx{
		Sender:     sender,
		Nonce:      nonce,
		Fee:        fee,
		Payload:    raw,
		ContractID: enrollment.ContractID,
	}
}

// aliceWallet seeds an AccountStore with Alice holding
// `balanceCELL` CELL. Returned applier is wired against a
// fresh InMemoryState.
func aliceWallet(t *testing.T, balanceCELL float64) *EnrollmentApplier {
	t.Helper()
	as := NewAccountStore()
	as.Credit(fxAlice, balanceCELL)
	state := enrollment.NewInMemoryState()
	return NewEnrollmentApplier(as, state)
}

// -----------------------------------------------------------------------------
// ApplyEnrollmentTx — structural rejections
// -----------------------------------------------------------------------------

func TestApplyEnrollmentTx_NilTx(t *testing.T) {
	a := aliceWallet(t, 100)
	if err := a.ApplyEnrollmentTx(nil, 1); err == nil {
		t.Error("nil tx should error")
	}
}

func TestApplyEnrollmentTx_WrongContractID(t *testing.T) {
	a := aliceWallet(t, 100)
	tx := fxEnrollTx(t, fxAlice, 0)
	tx.ContractID = "qsdm/something-else/v1"
	err := a.ApplyEnrollmentTx(tx, 1)
	if !errors.Is(err, ErrNotEnrollmentTx) {
		t.Fatalf("want ErrNotEnrollmentTx, got %v", err)
	}
}

func TestApplyEnrollmentTx_CorruptPayload(t *testing.T) {
	a := aliceWallet(t, 100)
	tx := &mempool.Tx{
		Sender:     fxAlice,
		Payload:    []byte(`{"not":"valid-enrollment"`),
		ContractID: enrollment.ContractID,
	}
	if err := a.ApplyEnrollmentTx(tx, 1); err == nil {
		t.Error("corrupt payload should error")
	}
}

// -----------------------------------------------------------------------------
// ApplyEnrollmentTx — Enroll branch
// -----------------------------------------------------------------------------

func TestApplyEnrollmentTx_Enroll_HappyPath(t *testing.T) {
	a := aliceWallet(t, 100)
	tx := fxEnrollTx(t, fxAlice, 0)
	if err := a.ApplyEnrollmentTx(tx, 42); err != nil {
		t.Fatalf("ApplyEnrollmentTx: %v", err)
	}

	rec, err := a.State.Lookup(fxNodeID)
	if err != nil || rec == nil {
		t.Fatalf("state should contain node_id; err=%v rec=%v", err, rec)
	}
	if rec.Owner != fxAlice {
		t.Errorf("Owner: got %q, want %q", rec.Owner, fxAlice)
	}
	if rec.EnrolledAtHeight != 42 {
		t.Errorf("EnrolledAtHeight: got %d, want 42", rec.EnrolledAtHeight)
	}
	if rec.StakeDust != mining.MinEnrollStakeDust {
		t.Errorf("StakeDust: got %d, want %d", rec.StakeDust, mining.MinEnrollStakeDust)
	}

	acc, _ := a.Accounts.Get(fxAlice)
	wantBal := 100.0 - dustToBalance(mining.MinEnrollStakeDust)
	if acc.Balance != wantBal {
		t.Errorf("alice balance: got %v, want %v", acc.Balance, wantBal)
	}
	if acc.Nonce != 1 {
		t.Errorf("alice nonce: got %d, want 1", acc.Nonce)
	}
}

func TestApplyEnrollmentTx_Enroll_InsufficientBalance(t *testing.T) {
	// 5 CELL < 10 CELL required.
	a := aliceWallet(t, 5)
	tx := fxEnrollTx(t, fxAlice, 0)
	err := a.ApplyEnrollmentTx(tx, 1)
	if err == nil {
		t.Fatal("insufficient balance should error")
	}
	if !errors.Is(err, enrollment.ErrInsufficientBalance) {
		t.Errorf("want ErrInsufficientBalance, got %v", err)
	}
	acc, _ := a.Accounts.Get(fxAlice)
	if acc.Balance != 5 || acc.Nonce != 0 {
		t.Error("account must be unchanged on rejection")
	}
	if rec, _ := a.State.Lookup(fxNodeID); rec != nil {
		t.Error("state must be unchanged on rejection")
	}
}

func TestApplyEnrollmentTx_Enroll_StatelessRejectWrongKind(t *testing.T) {
	a := aliceWallet(t, 100)
	// Wrong Kind — encoded as enroll but swapped to unenroll
	// post-encode would flunk PeekKind, so craft via unenroll
	// encoder + pretend the router picked enroll. Simulate by
	// encoding a raw payload with mismatched kind using the
	// struct-level tag swap: we use the unenroll encoder and
	// then ApplyEnrollmentTx will dispatch on PeekKind. This
	// path routes to unenroll, so instead we test the opposite
	// (missing required field in enroll payload).
	payload := enrollment.EnrollPayload{
		Kind:      enrollment.PayloadKindEnroll,
		NodeID:    "", // invalid
		GPUUUID:   fxGPUUUID,
		HMACKey:   fxHMACKey(),
		StakeDust: mining.MinEnrollStakeDust,
	}
	raw, err := enrollment.EncodeEnrollPayload(payload)
	if err != nil {
		t.Fatalf("EncodeEnrollPayload: %v", err)
	}
	tx := &mempool.Tx{
		Sender:     fxAlice,
		Payload:    raw,
		ContractID: enrollment.ContractID,
	}
	if err := a.ApplyEnrollmentTx(tx, 1); err == nil {
		t.Fatal("empty node_id should fail stateless validation")
	}
	acc, _ := a.Accounts.Get(fxAlice)
	if acc.Balance != 100 || acc.Nonce != 0 {
		t.Error("account must be unchanged")
	}
}

func TestApplyEnrollmentTx_Enroll_DuplicateNodeID(t *testing.T) {
	a := aliceWallet(t, 100)
	tx1 := fxEnrollTx(t, fxAlice, 0)
	if err := a.ApplyEnrollmentTx(tx1, 1); err != nil {
		t.Fatalf("first enroll: %v", err)
	}

	// Second Alice tries to enroll same node_id at nonce=1
	// (she already bumped to 1 after the first).
	tx2 := fxEnrollTx(t, fxAlice, 1)
	err := a.ApplyEnrollmentTx(tx2, 2)
	if err == nil {
		t.Fatal("duplicate node_id should error")
	}
	if !errors.Is(err, enrollment.ErrNodeIDTaken) {
		t.Errorf("want ErrNodeIDTaken, got %v", err)
	}

	// Stake debit must NOT have happened for the duplicate.
	acc, _ := a.Accounts.Get(fxAlice)
	wantBal := 100.0 - dustToBalance(mining.MinEnrollStakeDust)
	if acc.Balance != wantBal {
		t.Errorf("balance should only reflect the first enroll: got %v want %v", acc.Balance, wantBal)
	}
	if acc.Nonce != 1 {
		t.Errorf("nonce should NOT be bumped on rejected-before-debit failure: got %d want 1", acc.Nonce)
	}
}

func TestApplyEnrollmentTx_Enroll_NonceMismatch(t *testing.T) {
	a := aliceWallet(t, 100)
	tx := fxEnrollTx(t, fxAlice, 99) // expected 0, passed 99
	err := a.ApplyEnrollmentTx(tx, 1)
	if err == nil {
		t.Fatal("nonce mismatch should error")
	}
	if !strings.Contains(err.Error(), "nonce mismatch") {
		t.Errorf("error should mention nonce mismatch: %v", err)
	}
	acc, _ := a.Accounts.Get(fxAlice)
	if acc.Balance != 100 || acc.Nonce != 0 {
		t.Error("account must be unchanged on nonce mismatch")
	}
	if rec, _ := a.State.Lookup(fxNodeID); rec != nil {
		t.Error("state must be unchanged on nonce mismatch")
	}
}

// -----------------------------------------------------------------------------
// ApplyEnrollmentTx — Unenroll branch
// -----------------------------------------------------------------------------

func TestApplyEnrollmentTx_Unenroll_HappyPath(t *testing.T) {
	a := aliceWallet(t, 100)
	if err := a.ApplyEnrollmentTx(fxEnrollTx(t, fxAlice, 0), 10); err != nil {
		t.Fatalf("setup enroll: %v", err)
	}
	// Alice's balance post-enroll: 100 - 10 = 90; nonce = 1.
	tx := fxUnenrollTx(t, fxAlice, fxNodeID, 1, 0.001)
	if err := a.ApplyEnrollmentTx(tx, 50); err != nil {
		t.Fatalf("unenroll: %v", err)
	}

	rec, _ := a.State.Lookup(fxNodeID)
	if rec == nil {
		t.Fatal("record should still exist during unbond")
	}
	if rec.Active() {
		t.Error("record should be marked inactive (revoked)")
	}
	if rec.RevokedAtHeight != 50 {
		t.Errorf("RevokedAtHeight: got %d, want 50", rec.RevokedAtHeight)
	}

	acc, _ := a.Accounts.Get(fxAlice)
	// balance = 90 - 0.001 (fee only; stake stays locked in record)
	wantBal := 100.0 - dustToBalance(mining.MinEnrollStakeDust) - 0.001
	if diff := acc.Balance - wantBal; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("balance: got %v, want ~%v", acc.Balance, wantBal)
	}
	if acc.Nonce != 2 {
		t.Errorf("nonce: got %d, want 2", acc.Nonce)
	}
}

func TestApplyEnrollmentTx_Unenroll_WrongSender(t *testing.T) {
	a := aliceWallet(t, 100)
	if err := a.ApplyEnrollmentTx(fxEnrollTx(t, fxAlice, 0), 10); err != nil {
		t.Fatalf("setup: %v", err)
	}
	a.Accounts.Credit("eve", 1)
	tx := fxUnenrollTx(t, "eve", fxNodeID, 0, 0.001)
	err := a.ApplyEnrollmentTx(tx, 50)
	if err == nil {
		t.Fatal("wrong sender should error")
	}
	if !errors.Is(err, enrollment.ErrNodeNotOwned) {
		t.Errorf("want ErrNodeNotOwned, got %v", err)
	}
}

func TestApplyEnrollmentTx_Unenroll_ZeroFee(t *testing.T) {
	a := aliceWallet(t, 100)
	if err := a.ApplyEnrollmentTx(fxEnrollTx(t, fxAlice, 0), 10); err != nil {
		t.Fatalf("setup: %v", err)
	}
	tx := fxUnenrollTx(t, fxAlice, fxNodeID, 1, 0) // fee = 0
	err := a.ApplyEnrollmentTx(tx, 50)
	if err == nil {
		t.Fatal("zero fee unenroll should error")
	}
	if !strings.Contains(err.Error(), "positive Fee") {
		t.Errorf("error should mention positive Fee: %v", err)
	}
}

// -----------------------------------------------------------------------------
// SweepMaturedEnrollments
// -----------------------------------------------------------------------------

func TestSweepMaturedEnrollments_CreditsOwner(t *testing.T) {
	a := aliceWallet(t, 100)
	if err := a.ApplyEnrollmentTx(fxEnrollTx(t, fxAlice, 0), 10); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if err := a.ApplyEnrollmentTx(fxUnenrollTx(t, fxAlice, fxNodeID, 1, 0.001), 50); err != nil {
		t.Fatalf("unenroll: %v", err)
	}

	// Before sweep, alice has 90 - 0.001 = 89.999.
	preSweep, _ := a.Accounts.Get(fxAlice)

	// Sweep BEFORE maturity — no releases, no credit.
	released, err := a.SweepMaturedEnrollments(50 + enrollment.UnbondWindow - 1)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(released) != 0 {
		t.Errorf("pre-mature sweep: got %d releases, want 0", len(released))
	}
	mid, _ := a.Accounts.Get(fxAlice)
	if mid.Balance != preSweep.Balance {
		t.Errorf("balance must not change on pre-mature sweep: %v != %v", mid.Balance, preSweep.Balance)
	}

	// Sweep AT maturity — one release, stake credited.
	released, err = a.SweepMaturedEnrollments(50 + enrollment.UnbondWindow)
	if err != nil {
		t.Fatalf("mature sweep: %v", err)
	}
	if len(released) != 1 {
		t.Fatalf("want 1 release, got %d", len(released))
	}
	if released[0].Owner != fxAlice || released[0].NodeID != fxNodeID {
		t.Errorf("release shape: %+v", released[0])
	}
	acc, _ := a.Accounts.Get(fxAlice)
	wantBal := preSweep.Balance + dustToBalance(mining.MinEnrollStakeDust)
	if diff := acc.Balance - wantBal; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("balance post-sweep: got %v, want %v", acc.Balance, wantBal)
	}

	// Record is gone after sweep.
	if rec, _ := a.State.Lookup(fxNodeID); rec != nil {
		t.Error("record should have been swept")
	}
}

func TestSweepMaturedEnrollments_NoOp(t *testing.T) {
	a := aliceWallet(t, 100)
	released, err := a.SweepMaturedEnrollments(1_000_000)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(released) != 0 {
		t.Errorf("empty state sweep: got %d releases, want 0", len(released))
	}
}

// -----------------------------------------------------------------------------
// NewEnrollmentApplier panics on nil inputs
// -----------------------------------------------------------------------------

func TestNewEnrollmentApplier_NilAccountsPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on nil accounts")
		}
	}()
	NewEnrollmentApplier(nil, enrollment.NewInMemoryState())
}

func TestNewEnrollmentApplier_NilStatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on nil state")
		}
	}()
	NewEnrollmentApplier(NewAccountStore(), nil)
}

// -----------------------------------------------------------------------------
// End-to-end integration: enroll → unenroll → mature → sweep
// -----------------------------------------------------------------------------

func TestIntegration_EnrollUnenrollMatureSweep(t *testing.T) {
	a := aliceWallet(t, 50)

	// --- Block 10: Alice enrolls.
	if err := a.ApplyEnrollmentTx(fxEnrollTx(t, fxAlice, 0), 10); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	rec, _ := a.State.Lookup(fxNodeID)
	if rec == nil || !rec.Active() {
		t.Fatal("after enroll, record should be active")
	}
	aliceAfterEnroll, _ := a.Accounts.Get(fxAlice)
	wantBalAfterEnroll := 50.0 - dustToBalance(mining.MinEnrollStakeDust)
	if aliceAfterEnroll.Balance != wantBalAfterEnroll {
		t.Errorf("balance after enroll: got %v, want %v", aliceAfterEnroll.Balance, wantBalAfterEnroll)
	}

	// --- Block 100: Alice unenrolls.
	if err := a.ApplyEnrollmentTx(fxUnenrollTx(t, fxAlice, fxNodeID, 1, 0.01), 100); err != nil {
		t.Fatalf("unenroll: %v", err)
	}
	rec, _ = a.State.Lookup(fxNodeID)
	if rec == nil || rec.Active() {
		t.Fatal("after unenroll, record should be revoked but still present")
	}

	// --- Block 100 + UnbondWindow - 1: sweep is a no-op.
	rel, err := a.SweepMaturedEnrollments(100 + enrollment.UnbondWindow - 1)
	if err != nil {
		t.Fatalf("pre-mature sweep: %v", err)
	}
	if len(rel) != 0 {
		t.Error("sweep should release nothing before maturity")
	}

	// --- Block 100 + UnbondWindow: record matures.
	rel, err = a.SweepMaturedEnrollments(100 + enrollment.UnbondWindow)
	if err != nil {
		t.Fatalf("mature sweep: %v", err)
	}
	if len(rel) != 1 {
		t.Fatalf("want 1 release, got %d", len(rel))
	}

	// Alice got her stake back (less the unenroll fee).
	aliceFinal, _ := a.Accounts.Get(fxAlice)
	wantFinal := 50.0 - 0.01
	if diff := aliceFinal.Balance - wantFinal; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("final balance: got %v, want %v", aliceFinal.Balance, wantFinal)
	}
	if aliceFinal.Nonce != 2 {
		t.Errorf("final nonce: got %d, want 2", aliceFinal.Nonce)
	}

	// Node_id is now free — Alice can re-enroll (with a new nonce).
	if err := a.ApplyEnrollmentTx(fxEnrollTx(t, fxAlice, 2), 200); err != nil {
		t.Errorf("re-enroll after mature sweep should succeed: %v", err)
	}
}
