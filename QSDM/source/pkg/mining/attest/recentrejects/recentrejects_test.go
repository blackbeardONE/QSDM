package recentrejects

import (
	"strings"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// constructor
// -----------------------------------------------------------------------------

func TestNewStore_DefaultsApplied(t *testing.T) {
	s := NewStore(0, nil)
	if s.Cap() != DefaultMaxRejections {
		t.Errorf("cap: got %d, want %d", s.Cap(), DefaultMaxRejections)
	}
	if s.Len() != 0 {
		t.Errorf("len: got %d, want 0", s.Len())
	}
}

func TestNewStore_NegativeMaxFallsBackToDefault(t *testing.T) {
	s := NewStore(-7, nil)
	if s.Cap() != DefaultMaxRejections {
		t.Errorf("cap: got %d, want %d", s.Cap(), DefaultMaxRejections)
	}
}

// -----------------------------------------------------------------------------
// Record / sequencing
// -----------------------------------------------------------------------------

func TestRecord_AssignsMonotonicSeq(t *testing.T) {
	s := NewStore(0, nil)
	for i := 1; i <= 5; i++ {
		seq := s.Record(Rejection{Kind: KindArchSpoofUnknown})
		if int(seq) != i {
			t.Errorf("record %d: seq=%d, want %d", i, seq, i)
		}
	}
	if s.Len() != 5 {
		t.Errorf("len: got %d, want 5", s.Len())
	}
}

func TestRecord_FillsRecordedAtFromNowFn(t *testing.T) {
	fixed := time.Date(2026, 4, 29, 7, 0, 0, 0, time.UTC)
	s := NewStore(0, func() time.Time { return fixed })
	seq := s.Record(Rejection{Kind: KindArchSpoofUnknown})
	page := s.List(ListOptions{Cursor: seq - 1, Limit: 1})
	if len(page.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(page.Records))
	}
	if !page.Records[0].RecordedAt.Equal(fixed) {
		t.Errorf("recorded_at: got %v, want %v", page.Records[0].RecordedAt, fixed)
	}
}

func TestRecord_PreservesCallerSuppliedRecordedAt(t *testing.T) {
	stored := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	s := NewStore(0, func() time.Time { return time.Now() })
	s.Record(Rejection{Kind: KindHashrateOutOfBand, RecordedAt: stored})
	page := s.List(ListOptions{Limit: 1})
	if !page.Records[0].RecordedAt.Equal(stored) {
		t.Errorf("recorded_at: got %v, want %v", page.Records[0].RecordedAt, stored)
	}
}

// -----------------------------------------------------------------------------
// FIFO eviction
// -----------------------------------------------------------------------------

func TestRecord_EvictsOldestWhenCapReached(t *testing.T) {
	s := NewStore(3, nil)
	for i := 0; i < 5; i++ {
		s.Record(Rejection{Kind: KindArchSpoofUnknown, Detail: "rec-" + intToStr(i)})
	}
	if s.Len() != 3 {
		t.Errorf("len: got %d, want 3", s.Len())
	}
	page := s.List(ListOptions{Limit: 10})
	// After 5 inserts with cap=3 we expect Seq 3, 4, 5 retained.
	wantSeqs := []uint64{3, 4, 5}
	if len(page.Records) != len(wantSeqs) {
		t.Fatalf("records: got %d, want %d", len(page.Records), len(wantSeqs))
	}
	for i, want := range wantSeqs {
		if page.Records[i].Seq != want {
			t.Errorf("record[%d].seq: got %d, want %d", i, page.Records[i].Seq, want)
		}
	}
}

// -----------------------------------------------------------------------------
// truncation
// -----------------------------------------------------------------------------

func TestRecord_TruncatesDetailAt200Runes(t *testing.T) {
	long := strings.Repeat("A", 250)
	s := NewStore(0, nil)
	s.Record(Rejection{Kind: KindArchSpoofUnknown, Detail: long})
	page := s.List(ListOptions{Limit: 1})
	got := page.Records[0].Detail
	gotRunes := []rune(got)
	if len(gotRunes) != 201 { // 200 + ellipsis
		t.Errorf("detail rune len: got %d, want 201", len(gotRunes))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("detail must end with ellipsis: got %q", got[len(got)-3:])
	}
}

func TestRecord_TruncatesGPUNameAt256Runes(t *testing.T) {
	long := strings.Repeat("X", 300)
	s := NewStore(0, nil)
	s.Record(Rejection{Kind: KindArchSpoofGPUNameMismatch, GPUName: long})
	page := s.List(ListOptions{Limit: 1})
	got := []rune(page.Records[0].GPUName)
	if len(got) != 257 {
		t.Errorf("gpu_name rune len: got %d, want 257", len(got))
	}
}

// -----------------------------------------------------------------------------
// List filters
// -----------------------------------------------------------------------------

func TestList_FilterByKind(t *testing.T) {
	s := NewStore(0, nil)
	s.Record(Rejection{Kind: KindArchSpoofUnknown})
	s.Record(Rejection{Kind: KindHashrateOutOfBand})
	s.Record(Rejection{Kind: KindArchSpoofGPUNameMismatch})

	page := s.List(ListOptions{Kind: KindArchSpoofUnknown, Limit: 10})
	if len(page.Records) != 1 {
		t.Fatalf("expected 1 record, got %d: %+v", len(page.Records), page.Records)
	}
	if page.Records[0].Kind != KindArchSpoofUnknown {
		t.Errorf("kind: got %q", page.Records[0].Kind)
	}
}

func TestList_FilterByReason(t *testing.T) {
	s := NewStore(0, nil)
	s.Record(Rejection{Kind: KindArchSpoofUnknown, Reason: "unknown_arch"})
	s.Record(Rejection{Kind: KindArchSpoofGPUNameMismatch, Reason: "gpu_name_mismatch"})

	page := s.List(ListOptions{Reason: "gpu_name_mismatch", Limit: 10})
	if len(page.Records) != 1 || page.Records[0].Reason != "gpu_name_mismatch" {
		t.Errorf("filter mismatch: %+v", page.Records)
	}
}

func TestList_FilterByArch(t *testing.T) {
	s := NewStore(0, nil)
	s.Record(Rejection{Kind: KindHashrateOutOfBand, Arch: "hopper"})
	s.Record(Rejection{Kind: KindHashrateOutOfBand, Arch: "blackwell"})
	s.Record(Rejection{Kind: KindArchSpoofUnknown, Arch: "rubin"})

	page := s.List(ListOptions{Arch: "hopper", Limit: 10})
	if len(page.Records) != 1 || page.Records[0].Arch != "hopper" {
		t.Errorf("filter mismatch: %+v", page.Records)
	}
}

func TestList_FilterBySince(t *testing.T) {
	s := NewStore(0, nil)
	cutoff := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)
	s.Record(Rejection{Kind: KindArchSpoofUnknown, RecordedAt: cutoff.Add(-time.Hour)})
	s.Record(Rejection{Kind: KindArchSpoofUnknown, RecordedAt: cutoff.Add(time.Hour)})

	page := s.List(ListOptions{SinceUnixSec: cutoff.Unix(), Limit: 10})
	if len(page.Records) != 1 {
		t.Fatalf("since filter: got %d records, want 1: %+v", len(page.Records), page.Records)
	}
	if page.Records[0].RecordedAt.Before(cutoff) {
		t.Errorf("included pre-cutoff record")
	}
}

func TestList_FilterCombined(t *testing.T) {
	s := NewStore(0, nil)
	s.Record(Rejection{Kind: KindHashrateOutOfBand, Arch: "hopper"})
	s.Record(Rejection{Kind: KindHashrateOutOfBand, Arch: "blackwell"})
	s.Record(Rejection{Kind: KindArchSpoofUnknown, Arch: "hopper"})

	page := s.List(ListOptions{
		Kind:  KindHashrateOutOfBand,
		Arch:  "hopper",
		Limit: 10,
	})
	if len(page.Records) != 1 {
		t.Fatalf("combined filter: got %d records, want 1", len(page.Records))
	}
}

// -----------------------------------------------------------------------------
// List cursor pagination
// -----------------------------------------------------------------------------

func TestList_CursorPagination(t *testing.T) {
	s := NewStore(0, nil)
	for i := 0; i < 10; i++ {
		s.Record(Rejection{Kind: KindArchSpoofUnknown})
	}

	page1 := s.List(ListOptions{Limit: 4})
	if len(page1.Records) != 4 {
		t.Fatalf("page1: got %d records, want 4", len(page1.Records))
	}
	if !page1.HasMore {
		t.Error("page1 has_more should be true")
	}
	if page1.NextCursor != 4 {
		t.Errorf("page1 next_cursor: got %d, want 4", page1.NextCursor)
	}

	page2 := s.List(ListOptions{Cursor: page1.NextCursor, Limit: 4})
	if len(page2.Records) != 4 {
		t.Fatalf("page2: got %d records, want 4", len(page2.Records))
	}
	if !page2.HasMore {
		t.Error("page2 has_more should be true")
	}
	if page2.Records[0].Seq != 5 {
		t.Errorf("page2 first seq: got %d, want 5", page2.Records[0].Seq)
	}

	page3 := s.List(ListOptions{Cursor: page2.NextCursor, Limit: 4})
	if len(page3.Records) != 2 {
		t.Fatalf("page3: got %d records, want 2", len(page3.Records))
	}
	if page3.HasMore {
		t.Error("page3 has_more should be false (drained)")
	}
}

func TestList_LimitClampedToMax(t *testing.T) {
	s := NewStore(0, nil)
	s.Record(Rejection{Kind: KindArchSpoofUnknown})
	page := s.List(ListOptions{Limit: 99999})
	// Limit is clamped silently; the test only validates no
	// panic and the single record returns. The clamp is
	// observable via TotalMatches+HasMore on a saturated store
	// — covered by other tests.
	if len(page.Records) != 1 {
		t.Errorf("got %d records, want 1", len(page.Records))
	}
}

func TestList_EmptyStore_NoRecords(t *testing.T) {
	s := NewStore(0, nil)
	page := s.List(ListOptions{Limit: 10})
	if len(page.Records) != 0 {
		t.Errorf("empty store: got %d records", len(page.Records))
	}
	if page.HasMore {
		t.Error("empty store: has_more should be false")
	}
}

func TestList_NilStore_SafeReturn(t *testing.T) {
	var s *Store
	page := s.List(ListOptions{})
	if len(page.Records) != 0 {
		t.Errorf("nil store: got %d records", len(page.Records))
	}
	if got := s.Len(); got != 0 {
		t.Errorf("nil store len: got %d", got)
	}
	if got := s.Cap(); got != 0 {
		t.Errorf("nil store cap: got %d", got)
	}
	if seq := s.Record(Rejection{}); seq != 0 {
		t.Errorf("nil store record seq: got %d", seq)
	}
}

// -----------------------------------------------------------------------------
// concurrency smoke
// -----------------------------------------------------------------------------

func TestRecord_ConcurrentSafe(t *testing.T) {
	s := NewStore(1024, nil)
	const writers = 8
	const each = 100
	done := make(chan struct{})
	for i := 0; i < writers; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < each; j++ {
				s.Record(Rejection{Kind: KindArchSpoofUnknown})
			}
		}()
	}
	for i := 0; i < writers; i++ {
		<-done
	}
	if s.Len() != writers*each {
		t.Errorf("len: got %d, want %d", s.Len(), writers*each)
	}
	// Verify Seq monotonicity against a snapshot list.
	page := s.List(ListOptions{Limit: MaxListLimit})
	for i := 1; i < len(page.Records); i++ {
		if page.Records[i].Seq <= page.Records[i-1].Seq {
			t.Errorf("seq non-monotonic at %d: %d <= %d",
				i, page.Records[i].Seq, page.Records[i-1].Seq)
		}
	}
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	n := i
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
