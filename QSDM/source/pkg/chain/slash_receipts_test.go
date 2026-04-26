package chain

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

func TestSlashReceiptStore_PublishStoresAndLookup(t *testing.T) {
	frozen := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	store := NewSlashReceiptStore(0, func() time.Time { return frozen })

	ev := MiningSlashEvent{
		TxID:                    "tx-1",
		Outcome:                 SlashOutcomeApplied,
		Height:                  42,
		Slasher:                 "alice",
		NodeID:                  "rig-77",
		EvidenceKind:            slashing.EvidenceKindForgedAttestation,
		SlashedDust:             500_000_000,
		RewardedDust:            10_000_000,
		BurnedDust:              490_000_000,
		AutoRevoked:             true,
		AutoRevokeRemainingDust: 100_000_000,
	}
	store.PublishMiningSlash(ev)

	rec, ok := store.Lookup("tx-1")
	if !ok {
		t.Fatal("expected receipt for tx-1")
	}
	if rec.TxID != "tx-1" || rec.Outcome != SlashOutcomeApplied ||
		rec.Height != 42 || rec.Slasher != "alice" ||
		rec.NodeID != "rig-77" ||
		rec.EvidenceKind != slashing.EvidenceKindForgedAttestation ||
		rec.SlashedDust != 500_000_000 ||
		rec.RewardedDust != 10_000_000 ||
		rec.BurnedDust != 490_000_000 ||
		!rec.AutoRevoked ||
		rec.AutoRevokeRemainingDust != 100_000_000 {
		t.Errorf("receipt fields mismatched: %+v", rec)
	}
	if !rec.RecordedAt.Equal(frozen) {
		t.Errorf("RecordedAt: got %v, want %v", rec.RecordedAt, frozen)
	}
}

func TestSlashReceiptStore_RejectionPathStoresErrorAsString(t *testing.T) {
	store := NewSlashReceiptStore(0, nil)
	ev := MiningSlashEvent{
		TxID:         "tx-rejected",
		Outcome:      SlashOutcomeRejected,
		Height:       7,
		Slasher:      "bob",
		NodeID:       "rig-99",
		EvidenceKind: slashing.EvidenceKindDoubleMining,
		RejectReason: SlashRejectReasonVerifier,
		Err:          errors.New("verifier said no"),
	}
	store.PublishMiningSlash(ev)

	rec, ok := store.Lookup("tx-rejected")
	if !ok {
		t.Fatal("expected receipt for rejected tx")
	}
	if rec.RejectReason != SlashRejectReasonVerifier {
		t.Errorf("reject_reason: %q", rec.RejectReason)
	}
	if rec.Err != "verifier said no" {
		t.Errorf("Err string: %q", rec.Err)
	}
}

func TestSlashReceiptStore_LookupMissing(t *testing.T) {
	store := NewSlashReceiptStore(0, nil)
	if _, ok := store.Lookup("never-published"); ok {
		t.Error("Lookup for unknown tx_id returned ok=true")
	}
}

func TestSlashReceiptStore_LookupEmpty(t *testing.T) {
	store := NewSlashReceiptStore(0, nil)
	if _, ok := store.Lookup(""); ok {
		t.Error("Lookup for empty tx_id should return ok=false")
	}
}

func TestSlashReceiptStore_DropsEmptyTxID(t *testing.T) {
	store := NewSlashReceiptStore(0, nil)
	store.PublishMiningSlash(MiningSlashEvent{TxID: "", Outcome: SlashOutcomeApplied})
	if store.Len() != 0 {
		t.Errorf("expected 0 receipts; got %d", store.Len())
	}
}

func TestSlashReceiptStore_DuplicateTxIDPreservesRecordedAt(t *testing.T) {
	t1 := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 26, 11, 0, 0, 0, time.UTC)
	step := 0
	store := NewSlashReceiptStore(0, func() time.Time {
		step++
		if step == 1 {
			return t1
		}
		return t2
	})

	store.PublishMiningSlash(MiningSlashEvent{
		TxID: "dup", Outcome: SlashOutcomeRejected, Height: 1,
		RejectReason: SlashRejectReasonVerifier,
	})
	store.PublishMiningSlash(MiningSlashEvent{
		TxID: "dup", Outcome: SlashOutcomeApplied, Height: 2,
		SlashedDust: 1, // changed body
	})

	rec, ok := store.Lookup("dup")
	if !ok {
		t.Fatal("missing receipt")
	}
	if !rec.RecordedAt.Equal(t1) {
		t.Errorf("RecordedAt should preserve first timestamp; got %v", rec.RecordedAt)
	}
	if rec.Outcome != SlashOutcomeApplied || rec.Height != 2 || rec.SlashedDust != 1 {
		t.Errorf("body should reflect latest publish: %+v", rec)
	}
	if store.Len() != 1 {
		t.Errorf("len: got %d, want 1", store.Len())
	}
}

func TestSlashReceiptStore_FIFOEvictionAtCap(t *testing.T) {
	store := NewSlashReceiptStore(3, nil)

	for i := 0; i < 5; i++ {
		store.PublishMiningSlash(MiningSlashEvent{
			TxID:    fmt.Sprintf("tx-%d", i),
			Outcome: SlashOutcomeApplied,
			Height:  uint64(i),
		})
	}
	if store.Len() != 3 {
		t.Errorf("len after 5 inserts cap=3: got %d, want 3", store.Len())
	}
	for _, evicted := range []string{"tx-0", "tx-1"} {
		if _, ok := store.Lookup(evicted); ok {
			t.Errorf("expected %s to be evicted", evicted)
		}
	}
	for _, kept := range []string{"tx-2", "tx-3", "tx-4"} {
		if _, ok := store.Lookup(kept); !ok {
			t.Errorf("expected %s to still be present", kept)
		}
	}
}

func TestSlashReceiptStore_PublishEnrollmentIsNoop(t *testing.T) {
	store := NewSlashReceiptStore(0, nil)
	store.PublishEnrollment(EnrollmentEvent{Kind: EnrollmentEventEnrollApplied, NodeID: "rig"})
	if store.Len() != 0 {
		t.Errorf("expected 0 receipts (enrollment events are no-op); got %d", store.Len())
	}
}

func TestSlashReceiptStore_NilSafety(t *testing.T) {
	var s *SlashReceiptStore
	s.PublishMiningSlash(MiningSlashEvent{TxID: "tx"})
	if _, ok := s.Lookup("tx"); ok {
		t.Error("nil store should return ok=false")
	}
	if s.Len() != 0 {
		t.Error("nil store should report Len()=0")
	}
}

func TestSlashReceiptStore_DefaultsApplied(t *testing.T) {
	store := NewSlashReceiptStore(-5, nil)
	if store.max != DefaultMaxSlashReceipts {
		t.Errorf("max should default to %d; got %d", DefaultMaxSlashReceipts, store.max)
	}
	if store.nowFn == nil {
		t.Error("nowFn should default to time.Now")
	}
}

// SlashReceiptStore must satisfy ChainEventPublisher so it can
// be composed via NewCompositePublisher.
func TestSlashReceiptStore_SatisfiesChainEventPublisher(t *testing.T) {
	var _ ChainEventPublisher = (*SlashReceiptStore)(nil)
}
