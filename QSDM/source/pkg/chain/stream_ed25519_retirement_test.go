package chain

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

func setStreamEd25519RetirementForTest(t *testing.T, h uint64) {
	t.Helper()
	prev := StreamEd25519RetirementHeight()
	SetStreamEd25519RetirementHeight(h)
	t.Cleanup(func() { SetStreamEd25519RetirementHeight(prev) })
}

func TestStreamEd25519RetiredAt(t *testing.T) {
	setStreamEd25519RetirementForTest(t, 0)
	if StreamEd25519SessionKeysRetiredAt(0) || StreamEd25519SessionKeysRetiredAt(1<<62) {
		t.Fatal("height 0 must never activate")
	}
	SetStreamEd25519RetirementHeight(100)
	if StreamEd25519SessionKeysRetiredAt(99) || !StreamEd25519SessionKeysRetiredAt(100) || !StreamEd25519SessionKeysRetiredAt(101) {
		t.Fatal("rule must govern exactly heights >= 100")
	}
}

// Through the block applier (production, validation and replay of external
// blocks): an open below H is accepted, an open at H is refused without
// charging or bumping the nonce, and the pre-H stream can still take
// receipts and be closed at and after H.
func TestStreamEd25519Retirement_ApplierHeightGate(t *testing.T) {
	setStreamEd25519RetirementForTest(t, 100)
	payer := newStreamTestWallet(t)
	provider := newStreamTestWallet(t)
	sessionPublic, sessionPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	accounts := NewAccountStore()
	accounts.Credit(payer.address, 10)
	aware := NewEnrollmentAwareApplier(accounts, nil)
	aware.SetStreamStateStore(NewStreamStateStore())
	height := uint64(99)
	aware.SetHeightFn(func() uint64 { return height })

	open := openStreamAction(t, payer, provider, sessionPublic, 2*DustPerCell)
	if err := aware.ApplyTx(signedStreamTx(t, payer, open)); err != nil {
		t.Fatalf("open at H-1 must be accepted: %v", err)
	}

	height = 100
	second := openStreamAction(t, payer, provider, sessionPublic, 2*DustPerCell)
	second.ID, second.StreamID, second.Nonce = "stream-open-2", "vpn-device-2", 1
	rootBefore := aware.StateRoot()
	payerBefore, _ := accounts.Get(payer.address)
	if err := aware.ApplyTx(signedStreamTx(t, payer, second)); !errors.Is(err, ErrStreamEd25519SessionKeyRetired) {
		t.Fatalf("open at H must be refused with ErrStreamEd25519SessionKeyRetired, got %v", err)
	}
	if _, ok := aware.StreamStateStore().GetStream(second.StreamID); ok {
		t.Fatal("refused open created stream state")
	}
	payerAfter, _ := accounts.Get(payer.address)
	if payerAfter.Balance != payerBefore.Balance || payerAfter.Nonce != payerBefore.Nonce || aware.StateRoot() != rootBefore {
		t.Fatalf("refused open changed state: %+v -> %+v", payerBefore, payerAfter)
	}

	receipt := signedUsageReceipt(t, sessionPrivate, StreamUsageReceipt{
		StreamID: open.StreamID, Sequence: 1, CumulativeActiveSeconds: 60, ObservedAt: "2026-08-01T00:01:00Z",
	})
	receiptAction := StreamAction{
		ID: "stream-receipt-1", StreamID: open.StreamID, Action: StreamActionReceipt,
		Receipt: &receipt, Nonce: 0, Timestamp: "2026-08-01T00:01:01Z",
	}
	if err := aware.ApplyTx(signedStreamTx(t, provider, receiptAction)); err != nil {
		t.Fatalf("receipt at H for a stream opened below H must be accepted: %v", err)
	}
	height = 150
	closeAction := StreamAction{
		ID: "stream-close", StreamID: open.StreamID, Action: StreamActionClose,
		Nonce: 1, Timestamp: "2026-08-01T00:05:00Z",
	}
	if err := aware.ApplyTx(signedStreamTx(t, payer, closeAction)); err != nil {
		t.Fatalf("close after H for a stream opened below H must be accepted: %v", err)
	}
	if state, _ := aware.StreamStateStore().GetStream(open.StreamID); state.Status != StreamStatusClosed {
		t.Fatalf("stream not closed: %+v", state)
	}
}

// With a height configured, an open on an applier with no height source is
// refused (fail closed) rather than evaluated at height 0.
func TestStreamEd25519Retirement_ApplierWithoutHeightFailsClosed(t *testing.T) {
	setStreamEd25519RetirementForTest(t, 100)
	payer := newStreamTestWallet(t)
	provider := newStreamTestWallet(t)
	sessionPublic, _, _ := ed25519.GenerateKey(rand.Reader)
	accounts := NewAccountStore()
	accounts.Credit(payer.address, 10)
	aware := NewEnrollmentAwareApplier(accounts, nil)
	aware.SetStreamStateStore(NewStreamStateStore())
	open := openStreamAction(t, payer, provider, sessionPublic, 2*DustPerCell)
	if err := aware.ApplyTx(signedStreamTx(t, payer, open)); !errors.Is(err, ErrEnrollmentHeightUnset) {
		t.Fatalf("open without a height source must fail closed, got %v", err)
	}
}

// With no height configured (the default) nothing changes: an open is
// accepted at any height, and on an applier with no height source, exactly as
// before the rule existed.
func TestStreamEd25519Retirement_DisabledByDefault(t *testing.T) {
	setStreamEd25519RetirementForTest(t, 0)
	payer := newStreamTestWallet(t)
	provider := newStreamTestWallet(t)
	sessionPublic, _, _ := ed25519.GenerateKey(rand.Reader)
	open := openStreamAction(t, payer, provider, sessionPublic, 2*DustPerCell)
	tx := signedStreamTx(t, payer, open)

	accounts := NewAccountStore()
	accounts.Credit(payer.address, 10)
	aware := NewEnrollmentAwareApplier(accounts, nil) // no height source
	aware.SetStreamStateStore(NewStreamStateStore())
	if err := aware.ApplyTx(tx); err != nil {
		t.Fatalf("open with the rule off and no height source: %v", err)
	}

	accounts2 := NewAccountStore()
	accounts2.Credit(payer.address, 10)
	aware2 := NewEnrollmentAwareApplier(accounts2, nil)
	aware2.SetStreamStateStore(NewStreamStateStore())
	aware2.SetHeightFn(func() uint64 { return 1 << 40 })
	if err := aware2.ApplyTx(tx); err != nil {
		t.Fatalf("open with the rule off at a high height: %v", err)
	}
	if aware.StateRoot() != aware2.StateRoot() {
		t.Fatal("state root depends on height while the rule is off")
	}

	if err := NewStreamStateStore().ApplyHistoricalTx(tx, 1<<40); err != nil {
		t.Fatalf("historical open with the rule off: %v", err)
	}
}

// The boot replay of the stream projection applies the rule at each
// committed block's height, and a history whose opens are all below H
// replays to the same stream root as with the rule off.
func TestStreamEd25519Retirement_HistoricalReplay(t *testing.T) {
	payer := newStreamTestWallet(t)
	provider := newStreamTestWallet(t)
	sessionPublic, _, _ := ed25519.GenerateKey(rand.Reader)
	open := openStreamAction(t, payer, provider, sessionPublic, 2*DustPerCell)
	tx := signedStreamTx(t, payer, open)

	setStreamEd25519RetirementForTest(t, 0)
	off := NewStreamStateStore()
	if err := off.ApplyHistoricalTx(tx, 99); err != nil {
		t.Fatalf("replay with the rule off: %v", err)
	}

	SetStreamEd25519RetirementHeight(100)
	on := NewStreamStateStore()
	if err := on.ApplyHistoricalTx(tx, 99); err != nil {
		t.Fatalf("replay of an open at H-1: %v", err)
	}
	if on.StateRoot() != off.StateRoot() {
		t.Fatalf("pre-H history replays differently: %s vs %s", on.StateRoot(), off.StateRoot())
	}
	if err := NewStreamStateStore().ApplyHistoricalTx(tx, 100); !errors.Is(err, ErrStreamEd25519SessionKeyRetired) {
		t.Fatalf("replay of an open at H must be refused, got %v", err)
	}
}
