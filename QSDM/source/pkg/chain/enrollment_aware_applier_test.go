package chain

// enrollment_aware_applier_test.go exercises the router + Sweep
// shim that sits between mempool.Tx and the EnrollmentApplier.
//
// Three layers of coverage:
//
//  1. Unit-level ApplyTx routing: transfer path, enrollment
//     path, unwired / misconfigured surfaces.
//  2. StateRoot / accessor plumbing (mostly for forward-compat:
//     if Accounts() ever returns a different instance, receipts
//     diverge).
//  3. End-to-end via a real *BlockProducer + *Mempool, proving
//     the shim composes with the existing producer without any
//     producer-side changes. This is the contract that future
//     "block-apply wiring" commits must continue to honour.
//
// Fixtures (fxEnrollTx, fxUnenrollTx, fxAlice, fxNodeID, etc.)
// are reused from enrollment_apply_test.go so rejection paths
// stay in sync with the underlying EnrollmentApplier tests.

import (
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// -----------------------------------------------------------------------------
// Unit: ApplyTx routing
// -----------------------------------------------------------------------------

// awareFixture returns (aware, accounts, ea) wired together
// with Alice holding `aliceCELL` CELL, and `heightFn` installed.
func awareFixture(t *testing.T, aliceCELL float64, heightFn func() uint64) (*EnrollmentAwareApplier, *AccountStore, *EnrollmentApplier) {
	t.Helper()
	accounts := NewAccountStore()
	accounts.Credit(fxAlice, aliceCELL)
	state := enrollment.NewInMemoryState()
	ea := NewEnrollmentApplier(accounts, state)
	aware := NewEnrollmentAwareApplier(accounts, ea)
	if heightFn != nil {
		aware.SetHeightFn(heightFn)
	}
	return aware, accounts, ea
}

func TestNewEnrollmentAwareApplier_PanicsOnNilAccounts(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil accounts")
		}
	}()
	_ = NewEnrollmentAwareApplier(nil, nil)
}

func TestEnrollmentAwareApplier_ApplyTx_TransferPath(t *testing.T) {
	aware, accounts, _ := awareFixture(t, 100, func() uint64 { return 1 })
	tx := &mempool.Tx{
		ID:        "tx-transfer-1",
		Sender:    fxAlice,
		Recipient: "bob",
		Amount:    10,
		Fee:       0.1,
		Nonce:     0,
	}
	if err := aware.ApplyTx(tx); err != nil {
		t.Fatalf("transfer ApplyTx: %v", err)
	}
	alice, _ := accounts.Get(fxAlice)
	if alice.Balance != 100-10-0.1 {
		t.Errorf("alice balance: got %v, want %v", alice.Balance, 100-10-0.1)
	}
	if alice.Nonce != 1 {
		t.Errorf("alice nonce: got %d, want 1", alice.Nonce)
	}
	bob, _ := accounts.Get("bob")
	if bob.Balance != 10 {
		t.Errorf("bob balance: got %v, want 10", bob.Balance)
	}
}

func TestEnrollmentAwareApplier_ApplyTx_EmptyContractIDIsTransfer(t *testing.T) {
	// Explicit: empty ContractID must route to transfer, not
	// to enrollment. This is what protects a v1-only network
	// from accidentally routing through an enrollment applier
	// the operator never wired.
	aware, accounts, _ := awareFixture(t, 100, nil)
	tx := &mempool.Tx{
		ID:        "tx-empty-contract",
		Sender:    fxAlice,
		Recipient: "bob",
		Amount:    1,
		Fee:       0.01,
		Nonce:     0,
		// ContractID intentionally unset.
	}
	if err := aware.ApplyTx(tx); err != nil {
		t.Fatalf("empty-ContractID transfer: %v", err)
	}
	alice, _ := accounts.Get(fxAlice)
	if alice.Nonce != 1 {
		t.Errorf("nonce not bumped via transfer path: got %d", alice.Nonce)
	}
}

func TestEnrollmentAwareApplier_ApplyTx_EnrollmentPath(t *testing.T) {
	called := uint64(0)
	aware, accounts, ea := awareFixture(t, 100, func() uint64 {
		called++
		return 42
	})

	tx := fxEnrollTx(t, fxAlice, 0)
	if err := aware.ApplyTx(tx); err != nil {
		t.Fatalf("enroll ApplyTx: %v", err)
	}
	if called == 0 {
		t.Error("HeightFn was not consulted for enrollment tx")
	}

	rec, err := ea.State.Lookup(fxNodeID)
	if err != nil || rec == nil {
		t.Fatalf("enrollment record missing: err=%v rec=%v", err, rec)
	}
	if rec.EnrolledAtHeight != 42 {
		t.Errorf("EnrolledAtHeight: got %d, want 42 (the HeightFn value)", rec.EnrolledAtHeight)
	}

	alice, _ := accounts.Get(fxAlice)
	// 100 - 10 (stake) - 0.01 (enroll fee burned).
	wantBalance := float64(100) - dustToBalance(mining.MinEnrollStakeDust) - 0.01
	if !approxEqual(alice.Balance, wantBalance) {
		t.Errorf("alice balance: got %v, want %v", alice.Balance, wantBalance)
	}
	if alice.Nonce != 1 {
		t.Errorf("alice nonce: got %d, want 1", alice.Nonce)
	}
}

func TestEnrollmentAwareApplier_ApplyTx_EnrollmentRejectsWithoutApplier(t *testing.T) {
	// Shim constructed WITHOUT an EnrollmentApplier: an
	// enrollment-tagged tx must be rejected loudly, never
	// silently delegated to AccountStore (which would ignore
	// Payload and apply an Amount=0/Nonce=0 no-op transfer).
	accounts := NewAccountStore()
	accounts.Credit(fxAlice, 100)
	aware := NewEnrollmentAwareApplier(accounts, nil)
	aware.SetHeightFn(func() uint64 { return 1 })

	err := aware.ApplyTx(fxEnrollTx(t, fxAlice, 0))
	if err != ErrEnrollmentNotWired {
		t.Fatalf("want ErrEnrollmentNotWired, got %v", err)
	}

	// AccountStore must not have been touched.
	alice, _ := accounts.Get(fxAlice)
	if alice.Balance != 100 {
		t.Errorf("alice balance mutated: %v", alice.Balance)
	}
	if alice.Nonce != 0 {
		t.Errorf("alice nonce mutated: %d", alice.Nonce)
	}
}

func TestEnrollmentAwareApplier_ApplyTx_EnrollmentRejectsWithoutHeightFn(t *testing.T) {
	// No HeightFn: enrollment routing MUST fail with a wiring
	// error rather than stamp a record at height 0 (which
	// would collide with genesis / look forged on replay).
	accounts := NewAccountStore()
	accounts.Credit(fxAlice, 100)
	ea := NewEnrollmentApplier(accounts, enrollment.NewInMemoryState())
	aware := NewEnrollmentAwareApplier(accounts, ea)
	// Deliberately skip SetHeightFn.

	err := aware.ApplyTx(fxEnrollTx(t, fxAlice, 0))
	if err != ErrEnrollmentHeightUnset {
		t.Fatalf("want ErrEnrollmentHeightUnset, got %v", err)
	}
}

func TestEnrollmentAwareApplier_ApplyTx_NilInputs(t *testing.T) {
	aware, _, _ := awareFixture(t, 100, func() uint64 { return 1 })
	if err := aware.ApplyTx(nil); err == nil {
		t.Error("nil tx should error")
	}
	var nilShim *EnrollmentAwareApplier
	if err := nilShim.ApplyTx(&mempool.Tx{}); err == nil {
		t.Error("nil receiver should error, not panic")
	}
}

// -----------------------------------------------------------------------------
// StateRoot + accessors
// -----------------------------------------------------------------------------

func TestEnrollmentAwareApplier_StateRoot_DelegatesToAccounts(t *testing.T) {
	aware, accounts, _ := awareFixture(t, 100, func() uint64 { return 1 })
	if got, want := aware.StateRoot(), accounts.StateRoot(); got != want {
		t.Errorf("StateRoot: got %q, want %q (accounts)", got, want)
	}
	accounts.Credit("carol", 5)
	if got, want := aware.StateRoot(), accounts.StateRoot(); got != want {
		t.Errorf("StateRoot after mutation: got %q, want %q", got, want)
	}
}

func TestEnrollmentAwareApplier_Accessors(t *testing.T) {
	aware, accounts, ea := awareFixture(t, 100, nil)
	if aware.Accounts() != accounts {
		t.Error("Accounts() did not return the wired store")
	}
	if aware.EnrollmentApplier() != ea {
		t.Error("EnrollmentApplier() did not return the wired applier")
	}

	// Nil receivers on accessors must not panic.
	var nilShim *EnrollmentAwareApplier
	if nilShim.Accounts() != nil || nilShim.EnrollmentApplier() != nil {
		t.Error("nil receiver accessors should return nil, not panic")
	}
	if got := nilShim.StateRoot(); got != "" {
		t.Errorf("nil receiver StateRoot: got %q, want empty", got)
	}
}

// -----------------------------------------------------------------------------
// Sweep plumbing
// -----------------------------------------------------------------------------

func TestEnrollmentAwareApplier_Sweep_NoEnrollmentWired(t *testing.T) {
	accounts := NewAccountStore()
	aware := NewEnrollmentAwareApplier(accounts, nil)
	released, err := aware.Sweep(1_000_000)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if released != nil {
		t.Errorf("expected nil releases, got %+v", released)
	}

	var nilShim *EnrollmentAwareApplier
	if rel, err := nilShim.Sweep(1); err != nil || rel != nil {
		t.Errorf("nil receiver sweep: got (%v, %v), want (nil, nil)", rel, err)
	}
}

func TestEnrollmentAwareApplier_Sweep_CreditsOwnerAtMaturity(t *testing.T) {
	// Height provider is mutable here so we can advance the
	// clock between txs.
	var currentHeight uint64 = 10
	aware, accounts, _ := awareFixture(t, 50, func() uint64 { return currentHeight })

	if err := aware.ApplyTx(fxEnrollTx(t, fxAlice, 0)); err != nil {
		t.Fatalf("enroll: %v", err)
	}

	currentHeight = 100
	if err := aware.ApplyTx(fxUnenrollTx(t, fxAlice, fxNodeID, 1, 0.01)); err != nil {
		t.Fatalf("unenroll: %v", err)
	}

	// Pre-maturity sweep: no release.
	currentHeight = 100 + enrollment.UnbondWindow - 1
	released, err := aware.Sweep(currentHeight)
	if err != nil {
		t.Fatalf("pre-mature sweep: %v", err)
	}
	if len(released) != 0 {
		t.Fatalf("pre-mature should release nothing, got %+v", released)
	}

	// Matured sweep: release credits Alice.
	preBalance, _ := accounts.Get(fxAlice)
	currentHeight = 100 + enrollment.UnbondWindow
	released, err = aware.Sweep(currentHeight)
	if err != nil {
		t.Fatalf("mature sweep: %v", err)
	}
	if len(released) != 1 {
		t.Fatalf("mature should release one record, got %d", len(released))
	}

	postBalance, _ := accounts.Get(fxAlice)
	want := preBalance.Balance + dustToBalance(mining.MinEnrollStakeDust)
	if postBalance.Balance != want {
		t.Errorf("alice balance after sweep: got %v, want %v", postBalance.Balance, want)
	}
}

// -----------------------------------------------------------------------------
// End-to-end integration via *BlockProducer
// -----------------------------------------------------------------------------

// TestIntegration_BlockProducer_RoutesBothTxShapes produces a
// real block that contains BOTH a transfer tx and an enrollment
// tx. Verifies:
//
//   - BlockProducer accepts EnrollmentAwareApplier (the shim
//     implements StateApplier).
//   - Transfer and enrollment txs are both included.
//   - Post-block: alice's balance reflects the transfer debit
//     AND the stake debit; bob is credited; enrollment record
//     is live at the correct height.
//   - Sweep called at a post-unenroll maturity height credits
//     the stake back.
//
// This is the contract that future producer-side wiring (a
// typed post-seal hook, BFT replay cloning, etc.) must keep
// working without regression.
func TestIntegration_BlockProducer_RoutesBothTxShapes(t *testing.T) {
	accounts := NewAccountStore()
	accounts.Credit(fxAlice, 100)
	state := enrollment.NewInMemoryState()
	ea := NewEnrollmentApplier(accounts, state)
	aware := NewEnrollmentAwareApplier(accounts, ea)

	pool := mempool.New(mempool.DefaultConfig())
	defer pool.Stop()

	bp := NewBlockProducer(pool, aware, DefaultProducerConfig())
	// Canonical post-construction wiring: lock-free tip read.
	// Uses bp.TipHeight (atomic) — NOT bp.ChainHeight, which
	// would deadlock on bp.mu (already held by ProduceBlock
	// when the enrollment tx is routed through ApplyTx).
	aware.SetHeightFn(func() uint64 {
		h := bp.TipHeight()
		if !bp.HasTip() {
			// Pre-genesis: the first block will be at height 0,
			// so the enrollment record for the first block is
			// stamped at height 0. Matches the existing producer
			// behaviour where the very first sealed block sits
			// at Height=0 and PrevHash="".
			return h
		}
		return h + 1
	})

	// --- Admit a transfer tx + an enroll tx into the pool.
	transfer := &mempool.Tx{
		ID:        "tx-xfer",
		Sender:    fxAlice,
		Recipient: "bob",
		Amount:    5,
		Fee:       0.1,
		Nonce:     0,
		AddedAt:   time.Now(),
	}
	if err := pool.Add(transfer); err != nil {
		t.Fatalf("add transfer: %v", err)
	}
	// Nonce=1 because the transfer above will have bumped alice's nonce.
	enrol := fxEnrollTx(t, fxAlice, 1)
	enrol.ID = "tx-enroll"
	enrol.AddedAt = time.Now()
	if err := pool.Add(enrol); err != nil {
		t.Fatalf("add enroll: %v", err)
	}

	// --- Produce a block.
	blk, err := bp.ProduceBlock()
	if err != nil {
		t.Fatalf("ProduceBlock: %v", err)
	}
	if got := len(blk.Transactions); got != 2 {
		t.Fatalf("block included %d txs, want 2", got)
	}

	// Verify final state. applyEnroll now debits stake AND
	// tx.Fee atomically (Phase 2c-ix), matching transfer-tx
	// fee semantics — fees burned on acceptance.
	alice, _ := accounts.Get(fxAlice)
	stakeCELL := dustToBalance(mining.MinEnrollStakeDust)
	wantAlice := 100 - 5 - 0.1 - stakeCELL - enrol.Fee
	if approxEqual(alice.Balance, wantAlice) == false {
		t.Errorf("alice balance: got %v, want %v", alice.Balance, wantAlice)
	}
	if alice.Nonce != 2 {
		t.Errorf("alice nonce: got %d, want 2", alice.Nonce)
	}
	bob, _ := accounts.Get("bob")
	if bob == nil || bob.Balance != 5 {
		t.Errorf("bob balance: got %+v, want 5", bob)
	}
	rec, lookupErr := state.Lookup(fxNodeID)
	if lookupErr != nil || rec == nil {
		t.Fatalf("enrollment record missing after block: err=%v rec=%v", lookupErr, rec)
	}
	// blk.Height is 0 for the first produced block. At apply
	// time, HasTip() is false (pre-genesis), so HeightFn
	// returns TipHeight()+0 = 0. The stamped EnrolledAtHeight
	// therefore equals blk.Height (= 0) for the first block.
	// Subsequent blocks, where HasTip() is true, will see
	// HeightFn return TipHeight()+1 matching blk.Height.
	if rec.EnrolledAtHeight != blk.Height {
		t.Errorf("EnrolledAtHeight: got %d, want %d (blk.Height for first block)", rec.EnrolledAtHeight, blk.Height)
	}

	// --- Unenroll in a subsequent block + advance to maturity.
	unenrol := fxUnenrollTx(t, fxAlice, fxNodeID, 2, 0.01)
	unenrol.ID = "tx-unenroll"
	unenrol.AddedAt = time.Now()
	if err := pool.Add(unenrol); err != nil {
		t.Fatalf("add unenroll: %v", err)
	}
	blk2, err := bp.ProduceBlock()
	if err != nil {
		t.Fatalf("ProduceBlock (unenroll): %v", err)
	}
	if len(blk2.Transactions) != 1 {
		t.Fatalf("unenroll block included %d txs, want 1", len(blk2.Transactions))
	}

	// Stake is still locked until maturity.
	aliceMid, _ := accounts.Get(fxAlice)
	if aliceMid.Balance >= wantAlice+stakeCELL {
		t.Errorf("stake credited too early: balance=%v", aliceMid.Balance)
	}

	// Simulated post-block sweep at the matured height. The
	// production wiring is BlockProducer.OnSealedBlock =
	// aware.SealedBlockHook(...), exercised by
	// TestEnrollmentAwareApplier_SealedBlockHook_AutoSweep.
	// Here we call Sweep directly to keep this integration
	// test focused on tx-routing, not block-finalisation hooks.
	matureHeight := blk2.Height + enrollment.UnbondWindow
	released, err := aware.Sweep(matureHeight)
	if err != nil {
		t.Fatalf("sweep at maturity: %v", err)
	}
	if len(released) != 1 {
		t.Fatalf("matured sweep released %d records, want 1", len(released))
	}

	aliceFinal, _ := accounts.Get(fxAlice)
	wantFinal := wantAlice - unenrol.Fee + stakeCELL
	if approxEqual(aliceFinal.Balance, wantFinal) == false {
		t.Errorf("alice final balance: got %v, want %v", aliceFinal.Balance, wantFinal)
	}
}

// TestEnrollmentAwareApplier_SealedBlockHook_AutoSweep verifies
// that wiring `bp.OnSealedBlock = aware.SealedBlockHook(...)`
// causes matured unbonds to be released automatically without
// any explicit Sweep call from the operator. The hook contract:
//
//   - Fires on every sealed block (post-mu, post-OnSealed).
//   - Receives the just-sealed *Block.
//   - Calls aware.Sweep(blk.Height); pre-maturity blocks return
//     zero releases and are no-ops.
//   - On error from Sweep, calls the operator-supplied onErr,
//     which mirrors the legacy log-and-continue semantics.
//
// Without this hook, stake would remain locked indefinitely
// after unbonding because nothing in the producer drives Sweep.
func TestEnrollmentAwareApplier_SealedBlockHook_AutoSweep(t *testing.T) {
	accounts := NewAccountStore()
	accounts.Credit(fxAlice, 100)
	state := enrollment.NewInMemoryState()
	ea := NewEnrollmentApplier(accounts, state)
	aware := NewEnrollmentAwareApplier(accounts, ea)

	pool := mempool.New(mempool.DefaultConfig())
	defer pool.Stop()

	bp := NewBlockProducer(pool, aware, DefaultProducerConfig())
	aware.SetHeightFn(func() uint64 {
		h := bp.TipHeight()
		if !bp.HasTip() {
			return h
		}
		return h + 1
	})
	// Install the auto-sweep hook. This is the canonical
	// production wiring exercised by this test.
	var sweepErrs []error
	bp.OnSealedBlock = aware.SealedBlockHook(func(_ uint64, err error) {
		sweepErrs = append(sweepErrs, err)
	})

	// Block 0: enroll.
	enrol := fxEnrollTx(t, fxAlice, 0)
	enrol.ID = "tx-enroll"
	enrol.AddedAt = time.Now()
	if err := pool.Add(enrol); err != nil {
		t.Fatalf("add enroll: %v", err)
	}
	blk0, err := bp.ProduceBlock()
	if err != nil {
		t.Fatalf("ProduceBlock(enroll): %v", err)
	}

	// Block 1: unenroll.
	unenrol := fxUnenrollTx(t, fxAlice, fxNodeID, 1, 0.01)
	unenrol.ID = "tx-unenroll"
	unenrol.AddedAt = time.Now()
	if err := pool.Add(unenrol); err != nil {
		t.Fatalf("add unenroll: %v", err)
	}
	blk1, err := bp.ProduceBlock()
	if err != nil {
		t.Fatalf("ProduceBlock(unenroll): %v", err)
	}
	if blk1.Height != blk0.Height+1 {
		t.Fatalf("block heights not contiguous: blk0=%d blk1=%d", blk0.Height, blk1.Height)
	}

	// Pre-sweep assertion: stake is still locked after the
	// unenroll block landed (the hook for blk1 ran but the
	// unbond window has not matured).
	stakeCELL := dustToBalance(mining.MinEnrollStakeDust)
	aliceMid, _ := accounts.Get(fxAlice)
	wantMid := 100.0 - stakeCELL - enrol.Fee - unenrol.Fee
	if !approxEqual(aliceMid.Balance, wantMid) {
		t.Fatalf("pre-sweep balance: got %v, want %v (stake should still be locked)", aliceMid.Balance, wantMid)
	}
	if rec, _ := state.Lookup(fxNodeID); rec == nil || rec.Active() {
		t.Fatalf("record should be revoked-but-locked; rec=%+v", rec)
	}

	// UnbondWindow is ~201600 blocks (7 days @ 3s blocks); we
	// can't drive that many in a unit test. Instead, simulate
	// the producer's hook firing on a future, matured block by
	// invoking the SAME function that BlockProducer would call.
	// This proves the hook wiring is correct — anything that
	// goes wrong here would also go wrong in production.
	matureHeight := blk1.Height + enrollment.UnbondWindow
	syntheticMaturedBlock := &Block{Height: matureHeight}
	if bp.OnSealedBlock == nil {
		t.Fatal("OnSealedBlock hook should have been set above")
	}
	bp.OnSealedBlock(syntheticMaturedBlock)

	aliceFinal, _ := accounts.Get(fxAlice)
	// Hook re-credited stake; fees stay burned.
	wantFinal := 100.0 - enrol.Fee - unenrol.Fee
	if !approxEqual(aliceFinal.Balance, wantFinal) {
		t.Errorf("post-auto-sweep balance: got %v, want %v", aliceFinal.Balance, wantFinal)
	}
	if rec, _ := state.Lookup(fxNodeID); rec != nil {
		t.Errorf("record should have been auto-swept; rec=%+v", rec)
	}
	if len(sweepErrs) != 0 {
		t.Errorf("auto-sweep produced errors: %v", sweepErrs)
	}

	// And: a hook firing on a pre-maturity block must be a
	// no-op (no double-credit, no spurious errors).
	preMatureBalance := aliceFinal.Balance
	bp.OnSealedBlock(&Block{Height: matureHeight + 1})
	aliceAfter, _ := accounts.Get(fxAlice)
	if aliceAfter.Balance != preMatureBalance {
		t.Errorf("post-maturity hook re-credited: %v -> %v", preMatureBalance, aliceAfter.Balance)
	}
}

// TestEnrollmentAwareApplier_SealedBlockHook_NilEnrollmentApplier
// confirms the hook is a safe no-op when constructed without an
// EnrollmentApplier (v1-only nodes can install the wiring
// unconditionally).
func TestEnrollmentAwareApplier_SealedBlockHook_NilEnrollmentApplier(t *testing.T) {
	accounts := NewAccountStore()
	aware := NewEnrollmentAwareApplier(accounts, nil)
	called := false
	hook := aware.SealedBlockHook(func(uint64, error) { called = true })
	hook(&Block{Height: 42})
	if called {
		t.Error("onErr should not be invoked when no enrollment applier is wired")
	}
}

// approxEqual compares two float64 CELL balances within a
// generous epsilon. We do NOT use strict equality because tx
// fees (0.1, 0.01) aren't exactly representable; the producer
// carries them through as-is so the end-of-block balance can
// accumulate a sub-ulp drift. Checking against 1e-9 CELL (=0.1
// dust) is well below any on-chain rounding and exposes real
// bugs without flaking on float arithmetic.
func approxEqual(a, b float64) bool {
	const eps = 1e-9
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < eps
}
