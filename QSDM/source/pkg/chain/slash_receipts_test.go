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

// -----------------------------------------------------------------------------
// List() — paginated listing for the dashboard tile (2026-05-01)
// -----------------------------------------------------------------------------

// listTestStore is a helper that pre-populates a store with a
// canned set of receipts at known (synthetic) wall-clock
// timestamps spaced 1 hour apart so the SinceUnixSec tests
// have deterministic boundaries.
//
// Returns the store + the deterministic clock so tests can
// assert on RecordedAt.Unix() values precisely.
func listTestStore(t *testing.T, max int) (*SlashReceiptStore, *time.Time) {
	t.Helper()
	clock := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	cur := clock
	store := NewSlashReceiptStore(max, func() time.Time {
		return cur
	})
	// Three different evidence kinds × three timestamps + a
	// rejected outcome at the very end so all filters have
	// representative rows.
	publish := func(txID, outcome string, kind slashing.EvidenceKind) {
		store.PublishMiningSlash(MiningSlashEvent{
			TxID:         txID,
			Outcome:      outcome,
			Height:       1,
			Slasher:      "alice",
			NodeID:       "rig-" + txID,
			EvidenceKind: kind,
			SlashedDust:  100_000_000,
		})
		cur = cur.Add(time.Hour)
	}
	publish("tx-old-forged", SlashOutcomeApplied, slashing.EvidenceKindForgedAttestation)
	publish("tx-mid-double", SlashOutcomeApplied, slashing.EvidenceKindDoubleMining)
	publish("tx-newer-fresh", SlashOutcomeApplied, slashing.EvidenceKindFreshnessCheat)
	publish("tx-rejected", SlashOutcomeRejected, slashing.EvidenceKindForgedAttestation)
	return store, &clock
}

func TestSlashReceiptStore_List_NewestFirstOrdering(t *testing.T) {
	store, _ := listTestStore(t, 0)

	page := store.List(SlashReceiptListOptions{Limit: 10})
	if got := len(page.Records); got != 4 {
		t.Fatalf("len(Records) = %d, want 4", got)
	}
	// Newest insertion was tx-rejected; oldest was
	// tx-old-forged. The page must reverse insertion order.
	wantOrder := []string{"tx-rejected", "tx-newer-fresh", "tx-mid-double", "tx-old-forged"}
	for i, rec := range page.Records {
		if rec.TxID != wantOrder[i] {
			t.Errorf("page[%d].TxID = %q, want %q", i, rec.TxID, wantOrder[i])
		}
	}
}

func TestSlashReceiptStore_List_Empty(t *testing.T) {
	store := NewSlashReceiptStore(0, nil)
	page := store.List(SlashReceiptListOptions{})
	if got := len(page.Records); got != 0 {
		t.Errorf("len(Records) on empty store = %d, want 0", got)
	}
	if page.TotalMatches != 0 {
		t.Errorf("TotalMatches on empty store = %d, want 0", page.TotalMatches)
	}
	if page.HasMore {
		t.Error("HasMore on empty store = true, want false")
	}
}

func TestSlashReceiptStore_List_FilterOutcome(t *testing.T) {
	store, _ := listTestStore(t, 0)

	page := store.List(SlashReceiptListOptions{Outcome: SlashOutcomeApplied})
	if got := len(page.Records); got != 3 {
		t.Errorf("len(Records) for Outcome=applied = %d, want 3", got)
	}
	for _, rec := range page.Records {
		if rec.Outcome != SlashOutcomeApplied {
			t.Errorf("filter leaked: got Outcome=%q in Outcome=applied page", rec.Outcome)
		}
	}

	page = store.List(SlashReceiptListOptions{Outcome: SlashOutcomeRejected})
	if got := len(page.Records); got != 1 {
		t.Errorf("len(Records) for Outcome=rejected = %d, want 1", got)
	}
	if page.Records[0].TxID != "tx-rejected" {
		t.Errorf("rejected filter returned %q, want tx-rejected", page.Records[0].TxID)
	}
}

func TestSlashReceiptStore_List_FilterEvidenceKind(t *testing.T) {
	store, _ := listTestStore(t, 0)

	page := store.List(SlashReceiptListOptions{EvidenceKind: string(slashing.EvidenceKindForgedAttestation)})
	if got := len(page.Records); got != 2 {
		t.Errorf("len(Records) for EvidenceKind=forged-attestation = %d, want 2 (one applied + one rejected)", got)
	}

	page = store.List(SlashReceiptListOptions{EvidenceKind: string(slashing.EvidenceKindDoubleMining)})
	if got := len(page.Records); got != 1 {
		t.Errorf("len(Records) for EvidenceKind=double-mining = %d, want 1", got)
	}

	page = store.List(SlashReceiptListOptions{EvidenceKind: "nonexistent-kind"})
	if got := len(page.Records); got != 0 {
		t.Errorf("len(Records) for unknown EvidenceKind = %d, want 0", got)
	}
}

func TestSlashReceiptStore_List_FilterSinceUnixSec(t *testing.T) {
	store, base := listTestStore(t, 0)

	// Cutoff just before tx-newer-fresh (the third
	// publication, RecordedAt = base + 2h). SinceUnixSec
	// should drop tx-old-forged and tx-mid-double, keep
	// tx-newer-fresh and tx-rejected.
	cutoff := base.Add(2 * time.Hour).Unix()
	page := store.List(SlashReceiptListOptions{SinceUnixSec: cutoff})
	if got := len(page.Records); got != 2 {
		t.Fatalf("len(Records) for since=%d = %d, want 2", cutoff, got)
	}
	wantTxIDs := map[string]bool{"tx-rejected": true, "tx-newer-fresh": true}
	for _, rec := range page.Records {
		if !wantTxIDs[rec.TxID] {
			t.Errorf("post-cutoff page included %q (RecordedAt %v); cutoff %v",
				rec.TxID, rec.RecordedAt, time.Unix(cutoff, 0).UTC())
		}
	}
}

func TestSlashReceiptStore_List_LimitClamping(t *testing.T) {
	store, _ := listTestStore(t, 0)

	// Negative / zero limit selects DefaultSlashReceiptListLimit.
	page := store.List(SlashReceiptListOptions{Limit: 0})
	if got := len(page.Records); got != 4 {
		t.Errorf("Limit=0 (default) returned %d records on a 4-record store, want 4", got)
	}

	// Over-cap limit clamps to MaxSlashReceiptListLimit but
	// still returns all 4 (only 4 exist).
	page = store.List(SlashReceiptListOptions{Limit: 100_000})
	if got := len(page.Records); got != 4 {
		t.Errorf("Limit=100000 (over-cap) returned %d records, want 4", got)
	}

	// Tight limit returns exactly that many AND signals HasMore.
	page = store.List(SlashReceiptListOptions{Limit: 2})
	if got := len(page.Records); got != 2 {
		t.Errorf("Limit=2 returned %d records, want 2", got)
	}
	if !page.HasMore {
		t.Error("Limit=2 on 4-record store: HasMore=false, want true")
	}
	// TotalMatches with HasMore: documented as "matches in
	// this page + at least one more if HasMore", so it is
	// the page count + the one match that triggered the
	// HasMore break (3, not 4).
	if page.TotalMatches != 3 {
		t.Errorf("Limit=2 TotalMatches = %d, want 3 (page + the trigger)", page.TotalMatches)
	}
}

func TestSlashReceiptStore_List_FiltersAreANDed(t *testing.T) {
	store, _ := listTestStore(t, 0)

	// Outcome=applied AND EvidenceKind=forged-attestation
	// matches only tx-old-forged (the only applied+forged
	// receipt).
	page := store.List(SlashReceiptListOptions{
		Outcome:      SlashOutcomeApplied,
		EvidenceKind: string(slashing.EvidenceKindForgedAttestation),
	})
	if got := len(page.Records); got != 1 {
		t.Fatalf("len(Records) for AND filter = %d, want 1", got)
	}
	if page.Records[0].TxID != "tx-old-forged" {
		t.Errorf("AND filter returned %q, want tx-old-forged", page.Records[0].TxID)
	}
}

func TestSlashReceiptStore_List_NilStoreSafe(t *testing.T) {
	var s *SlashReceiptStore
	page := s.List(SlashReceiptListOptions{Limit: 10})
	if page.Records != nil || page.TotalMatches != 0 || page.HasMore {
		t.Errorf("nil store List should return zero-value page; got %+v", page)
	}
}

func TestSlashReceiptStore_List_AfterFIFOEviction(t *testing.T) {
	// Cap=2 store: insertions 3+4 evict 1+2. List must
	// return only the surviving 2, NEWEST FIRST.
	store, _ := listTestStore(t, 2)

	page := store.List(SlashReceiptListOptions{Limit: 10})
	if got := len(page.Records); got != 2 {
		t.Fatalf("len(Records) post-eviction = %d, want 2", got)
	}
	wantOrder := []string{"tx-rejected", "tx-newer-fresh"}
	for i, rec := range page.Records {
		if rec.TxID != wantOrder[i] {
			t.Errorf("post-eviction page[%d].TxID = %q, want %q", i, rec.TxID, wantOrder[i])
		}
	}
}

