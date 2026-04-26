package enrollment

import (
	"fmt"
	"sort"
	"testing"
)

// seedListState builds an InMemoryState pre-loaded with a
// fixture set of records covering every lifecycle phase. Used
// across the List() tests so phase-filter behaviour is
// asserted against a single shared fixture (one source of
// truth for the wire-vs-registry contract).
func seedListState(t *testing.T) *InMemoryState {
	t.Helper()
	s := NewInMemoryState()

	type seed struct {
		id        string
		gpu       string
		stake     uint64
		revokedAt uint64
		drained   bool // forces StakeDust=0 to model a "revoked" phase
	}
	seeds := []seed{
		{id: "rig-active-01", gpu: "GPU-AAAAAAAA-0001", stake: 10_000_000_000},
		{id: "rig-active-02", gpu: "GPU-AAAAAAAA-0002", stake: 12_000_000_000},
		{id: "rig-active-03", gpu: "GPU-AAAAAAAA-0003", stake: 11_000_000_000},
		{id: "rig-pending-01", gpu: "GPU-PPPPPPPP-0001", stake: 9_000_000_000, revokedAt: 100},
		{id: "rig-pending-02", gpu: "GPU-PPPPPPPP-0002", stake: 8_000_000_000, revokedAt: 110},
		{id: "rig-revoked-01", gpu: "GPU-RRRRRRRR-0001", stake: 0, revokedAt: 50, drained: true},
	}
	for _, sd := range seeds {
		rec := EnrollmentRecord{
			NodeID:           sd.id,
			Owner:            "owner-" + sd.id,
			GPUUUID:          sd.gpu,
			HMACKey:          []byte("hot-secret-bytes-padding-32-here"),
			StakeDust:        sd.stake,
			EnrolledAtHeight: 10,
		}
		if err := s.ApplyEnroll(rec); err != nil {
			t.Fatalf("seed ApplyEnroll(%s): %v", sd.id, err)
		}
		if sd.revokedAt != 0 {
			if err := s.ApplyUnenroll(sd.id, sd.revokedAt); err != nil {
				t.Fatalf("seed ApplyUnenroll(%s): %v", sd.id, err)
			}
			if sd.drained {
				if _, err := s.SlashStake(sd.id, sd.stake); err != nil {
					t.Fatalf("seed SlashStake(%s): %v", sd.id, err)
				}
			}
		}
	}
	return s
}

func TestList_AllPhases_FullPage(t *testing.T) {
	s := seedListState(t)
	page := s.List(ListOptions{})
	if len(page.Records) != 6 {
		t.Errorf("len(Records): got %d, want 6", len(page.Records))
	}
	if page.TotalMatches != 6 {
		t.Errorf("TotalMatches: got %d, want 6", page.TotalMatches)
	}
	if page.HasMore {
		t.Error("HasMore should be false on full page")
	}
	if page.NextCursor != "" {
		t.Errorf("NextCursor on terminal page should be empty; got %q", page.NextCursor)
	}
	// Lexicographic order assertion.
	ids := make([]string, len(page.Records))
	for i, r := range page.Records {
		ids[i] = r.NodeID
	}
	if !sort.StringsAreSorted(ids) {
		t.Errorf("records not lexicographically sorted: %v", ids)
	}
}

func TestList_PhaseActive_FiltersCorrectly(t *testing.T) {
	s := seedListState(t)
	page := s.List(ListOptions{Phase: PhaseActive})
	if len(page.Records) != 3 {
		t.Errorf("len(Records): got %d, want 3", len(page.Records))
	}
	if page.TotalMatches != 3 {
		t.Errorf("TotalMatches: got %d, want 3", page.TotalMatches)
	}
	for _, r := range page.Records {
		if !r.Active() {
			t.Errorf("phase=active returned non-active record %s", r.NodeID)
		}
	}
}

func TestList_PhasePendingUnbond_FiltersCorrectly(t *testing.T) {
	s := seedListState(t)
	page := s.List(ListOptions{Phase: PhasePendingUnbond})
	if len(page.Records) != 2 {
		t.Errorf("len(Records): got %d, want 2", len(page.Records))
	}
	for _, r := range page.Records {
		if r.Active() || r.StakeDust == 0 {
			t.Errorf("phase=pending_unbond returned %s with active=%v stake=%d",
				r.NodeID, r.Active(), r.StakeDust)
		}
	}
}

func TestList_PhaseRevoked_FiltersCorrectly(t *testing.T) {
	s := seedListState(t)
	page := s.List(ListOptions{Phase: PhaseRevoked})
	if len(page.Records) != 1 {
		t.Errorf("len(Records): got %d, want 1", len(page.Records))
	}
	if r := page.Records[0]; r.Active() || r.StakeDust != 0 {
		t.Errorf("phase=revoked returned %s with active=%v stake=%d",
			r.NodeID, r.Active(), r.StakeDust)
	}
}

func TestList_UnknownPhase_ReturnsEmpty(t *testing.T) {
	s := seedListState(t)
	page := s.List(ListOptions{Phase: ListPhase("unknown-phase")})
	if len(page.Records) != 0 {
		t.Errorf("unknown phase should return empty; got %d records", len(page.Records))
	}
	if page.TotalMatches != 0 {
		t.Errorf("TotalMatches for unknown phase: got %d, want 0", page.TotalMatches)
	}
}

func TestList_EmptyState_ReturnsEmptyPage(t *testing.T) {
	s := NewInMemoryState()
	page := s.List(ListOptions{})
	if len(page.Records) != 0 || page.HasMore || page.NextCursor != "" || page.TotalMatches != 0 {
		t.Errorf("empty state list: %+v", page)
	}
}

func TestList_LimitClampsToPage(t *testing.T) {
	s := seedListState(t)
	page := s.List(ListOptions{Limit: 2})
	if len(page.Records) != 2 {
		t.Errorf("len(Records): got %d, want 2", len(page.Records))
	}
	if !page.HasMore {
		t.Error("HasMore should be true when limit < total")
	}
	if page.NextCursor == "" {
		t.Error("NextCursor should be populated when HasMore is true")
	}
	if page.TotalMatches != 6 {
		t.Errorf("TotalMatches: got %d, want 6", page.TotalMatches)
	}
}

func TestList_CursorWalksToCompletion(t *testing.T) {
	s := seedListState(t)
	const pageSize = 2

	seen := make(map[string]bool)
	cursor := ""
	pages := 0
	for {
		page := s.List(ListOptions{Cursor: cursor, Limit: pageSize})
		pages++
		for _, r := range page.Records {
			if seen[r.NodeID] {
				t.Errorf("duplicate record across pages: %s", r.NodeID)
			}
			seen[r.NodeID] = true
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
		if pages > 100 {
			t.Fatal("walk did not terminate")
		}
	}
	if len(seen) != 6 {
		t.Errorf("walked %d records, expected 6 (missing? %v)", len(seen), seen)
	}
	if pages != 3 {
		t.Errorf("pages traversed: got %d, want 3 (6 records / 2 per page)", pages)
	}
}

func TestList_CursorBeyondEnd_ReturnsEmpty(t *testing.T) {
	s := seedListState(t)
	page := s.List(ListOptions{Cursor: "zzz-after-everything"})
	if len(page.Records) != 0 {
		t.Errorf("cursor past end should yield empty page; got %d", len(page.Records))
	}
	if page.HasMore {
		t.Error("HasMore should be false")
	}
}

func TestList_CursorExactlyOnRecord_AdvancesPastIt(t *testing.T) {
	s := seedListState(t)
	// Cursor on an exact node_id must be exclusive — the next
	// page starts with the *strictly greater* node_id.
	page := s.List(ListOptions{Cursor: "rig-active-02", Limit: 1})
	if len(page.Records) != 1 {
		t.Fatalf("len(Records): got %d, want 1", len(page.Records))
	}
	if got := page.Records[0].NodeID; got == "rig-active-02" {
		t.Errorf("cursor was inclusive; first record after cursor=%q should NOT be that id", got)
	}
	if got, want := page.Records[0].NodeID, "rig-active-03"; got != want {
		t.Errorf("first record after cursor=rig-active-02: got %q, want %q", got, want)
	}
}

func TestList_DefaultLimitApplied(t *testing.T) {
	s := NewInMemoryState()
	for i := 0; i < DefaultListLimit+10; i++ {
		rec := EnrollmentRecord{
			NodeID:    fmt.Sprintf("rig-%04d", i),
			Owner:     "alice",
			GPUUUID:   fmt.Sprintf("GPU-%012d", i),
			StakeDust: 10_000_000_000,
		}
		if err := s.ApplyEnroll(rec); err != nil {
			t.Fatalf("ApplyEnroll: %v", err)
		}
	}
	page := s.List(ListOptions{}) // Limit zero → default
	if len(page.Records) != DefaultListLimit {
		t.Errorf("default limit: got %d records, want %d", len(page.Records), DefaultListLimit)
	}
	if !page.HasMore {
		t.Error("HasMore should be true (more than DefaultListLimit records)")
	}
}

func TestList_MaxLimitClamp(t *testing.T) {
	s := seedListState(t)
	page := s.List(ListOptions{Limit: MaxListLimit + 100})
	// Only 6 fixture records total; clamp doesn't visibly
	// affect this case but must not crash.
	if len(page.Records) != 6 {
		t.Errorf("clamped limit: got %d records, want 6", len(page.Records))
	}
}

func TestList_PhaseFilterPlusCursor(t *testing.T) {
	s := seedListState(t)

	// Walk only PhaseActive in 1-record pages and assert we
	// see exactly the three active rigs in lexicographic order.
	want := []string{"rig-active-01", "rig-active-02", "rig-active-03"}
	got := make([]string, 0, 3)
	cursor := ""
	for {
		page := s.List(ListOptions{Phase: PhaseActive, Cursor: cursor, Limit: 1})
		for _, r := range page.Records {
			got = append(got, r.NodeID)
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if len(got) != len(want) {
		t.Fatalf("got %d active records, want %d: %v", len(got), len(want), got)
	}
	for i, id := range want {
		if got[i] != id {
			t.Errorf("page[%d]: got %q, want %q", i, got[i], id)
		}
	}
}

func TestList_RecordsAreCopies(t *testing.T) {
	s := seedListState(t)
	page := s.List(ListOptions{Limit: 1})
	if len(page.Records) == 0 {
		t.Fatal("page is empty")
	}
	page.Records[0].StakeDust = 0xDEAD
	page.Records[0].Owner = "attacker"

	rec, _ := s.Lookup(page.Records[0].NodeID)
	if rec.StakeDust == 0xDEAD || rec.Owner == "attacker" {
		t.Errorf("List() returned aliased record; mutation leaked into registry: %+v", rec)
	}
}
