package chainparams

// authority_test.go: behavioural coverage for the
// AuthorityVoteStore reference implementation.
//
// The integration tests in internal/v2wiring/v2wiring_gov_test.go
// drive end-to-end lifecycle through the chain applier; this
// file pins the in-memory store's invariants so a regression
// in ordering / threshold / drop semantics surfaces before
// reaching that integration layer.

import (
	"errors"
	"testing"
)

// -----------------------------------------------------------------------------
// AuthorityThreshold
// -----------------------------------------------------------------------------

func TestAuthorityThreshold_TableDriven(t *testing.T) {
	cases := []struct {
		n, want int
	}{
		{0, 0}, // governance disabled
		{1, 1}, // bootstrap: single authority can act
		{2, 2}, // unanimity
		{3, 2}, // simple majority
		{4, 3},
		{5, 3},
		{6, 4},
		{7, 4},
		{8, 5},
		{9, 5},
	}
	for _, tc := range cases {
		got := AuthorityThreshold(tc.n)
		if got != tc.want {
			t.Errorf("AuthorityThreshold(%d) = %d, want %d", tc.n, got, tc.want)
		}
	}
}

// -----------------------------------------------------------------------------
// RecordVote
// -----------------------------------------------------------------------------

func TestRecordVote_FirstVoteCreatesProposal(t *testing.T) {
	s := NewInMemoryAuthorityVoteStore()
	key := AuthorityVoteKey{
		Op:              AuthorityOpAdd,
		Address:         "qsdm1new",
		EffectiveHeight: 100,
	}
	prop, crossed, err := s.RecordVote(key,
		AuthorityVote{Voter: "alice", SubmittedAtHeight: 50},
		3,
	)
	if err != nil {
		t.Fatalf("RecordVote: %v", err)
	}
	if crossed {
		t.Fatal("first vote crossed threshold for N=3 (want threshold=2)")
	}
	if len(prop.Voters) != 1 || prop.Voters[0].Voter != "alice" {
		t.Errorf("voters = %+v, want [alice]", prop.Voters)
	}
	if prop.Crossed {
		t.Error("Crossed=true after first vote of three")
	}
}

func TestRecordVote_SecondVoteCrossesAtN3(t *testing.T) {
	s := NewInMemoryAuthorityVoteStore()
	key := AuthorityVoteKey{Op: AuthorityOpAdd, Address: "qsdm1new", EffectiveHeight: 100}

	if _, _, err := s.RecordVote(key,
		AuthorityVote{Voter: "alice", SubmittedAtHeight: 50}, 3); err != nil {
		t.Fatalf("first vote: %v", err)
	}
	prop, crossed, err := s.RecordVote(key,
		AuthorityVote{Voter: "bob", SubmittedAtHeight: 51}, 3)
	if err != nil {
		t.Fatalf("second vote: %v", err)
	}
	if !crossed {
		t.Fatal("second vote did NOT cross threshold for N=3")
	}
	if !prop.Crossed {
		t.Error("Crossed=false on returned proposal")
	}
	if prop.CrossedAtHeight != 51 {
		t.Errorf("CrossedAtHeight=%d, want 51", prop.CrossedAtHeight)
	}
}

func TestRecordVote_DuplicateVoteRejected(t *testing.T) {
	s := NewInMemoryAuthorityVoteStore()
	key := AuthorityVoteKey{Op: AuthorityOpAdd, Address: "qsdm1new", EffectiveHeight: 100}

	if _, _, err := s.RecordVote(key,
		AuthorityVote{Voter: "alice"}, 3); err != nil {
		t.Fatalf("first vote: %v", err)
	}
	_, _, err := s.RecordVote(key, AuthorityVote{Voter: "alice"}, 3)
	if !errors.Is(err, ErrDuplicateVote) {
		t.Fatalf("err = %v, want ErrDuplicateVote", err)
	}
}

func TestRecordVote_EmptyVoterRejected(t *testing.T) {
	s := NewInMemoryAuthorityVoteStore()
	_, _, err := s.RecordVote(
		AuthorityVoteKey{Op: AuthorityOpAdd, Address: "x", EffectiveHeight: 1},
		AuthorityVote{Voter: ""}, 3)
	if !errors.Is(err, ErrPayloadInvalid) {
		t.Fatalf("err = %v, want ErrPayloadInvalid", err)
	}
}

func TestRecordVote_DoesNotUncrossOnNoiseVotes(t *testing.T) {
	s := NewInMemoryAuthorityVoteStore()
	key := AuthorityVoteKey{Op: AuthorityOpAdd, Address: "qsdm1new", EffectiveHeight: 100}
	for _, v := range []string{"alice", "bob", "carol"} {
		if _, _, err := s.RecordVote(key, AuthorityVote{Voter: v}, 3); err != nil {
			t.Fatalf("vote %s: %v", v, err)
		}
	}
	// dave votes after threshold already crossed; Crossed
	// must remain true and crossed=false (already-crossed
	// path).
	prop, crossed, err := s.RecordVote(key,
		AuthorityVote{Voter: "dave"}, 4)
	if err != nil {
		t.Fatalf("dave vote: %v", err)
	}
	if crossed {
		t.Error("crossed=true returned after threshold already passed (want false)")
	}
	if !prop.Crossed {
		t.Error("Crossed=false on returned proposal (want true; once crossed, sticky)")
	}
	if len(prop.Voters) != 4 {
		t.Errorf("voters len = %d, want 4", len(prop.Voters))
	}
}

// -----------------------------------------------------------------------------
// Promote
// -----------------------------------------------------------------------------

func TestPromote_OnlyCrossedAndDue(t *testing.T) {
	s := NewInMemoryAuthorityVoteStore()
	keyDueCrossed := AuthorityVoteKey{Op: AuthorityOpAdd, Address: "due-crossed", EffectiveHeight: 100}
	keyDueOpen := AuthorityVoteKey{Op: AuthorityOpAdd, Address: "due-open", EffectiveHeight: 100}
	keyFutureCrossed := AuthorityVoteKey{Op: AuthorityOpRemove, Address: "future-crossed", EffectiveHeight: 200}

	// due-crossed: 2 votes at N=3 → crossed.
	_, _, _ = s.RecordVote(keyDueCrossed, AuthorityVote{Voter: "alice"}, 3)
	_, _, _ = s.RecordVote(keyDueCrossed, AuthorityVote{Voter: "bob"}, 3)
	// due-open: 1 vote at N=3 → not crossed.
	_, _, _ = s.RecordVote(keyDueOpen, AuthorityVote{Voter: "alice"}, 3)
	// future-crossed: 2 votes at N=3 but EffectiveHeight=200.
	_, _, _ = s.RecordVote(keyFutureCrossed, AuthorityVote{Voter: "alice"}, 3)
	_, _, _ = s.RecordVote(keyFutureCrossed, AuthorityVote{Voter: "bob"}, 3)

	out := s.Promote(150)
	if len(out) != 1 {
		t.Fatalf("Promote returned %d proposals, want 1", len(out))
	}
	if out[0].Address != "due-crossed" {
		t.Errorf("promoted address = %q, want due-crossed", out[0].Address)
	}
	// open and future stay.
	if _, ok := s.Lookup(keyDueOpen); !ok {
		t.Error("due-open proposal disappeared after Promote")
	}
	if _, ok := s.Lookup(keyFutureCrossed); !ok {
		t.Error("future-crossed proposal disappeared after Promote")
	}
}

func TestPromote_DeterministicOrder(t *testing.T) {
	s := NewInMemoryAuthorityVoteStore()
	for _, addr := range []string{"zzz", "aaa", "mmm"} {
		k := AuthorityVoteKey{Op: AuthorityOpAdd, Address: addr, EffectiveHeight: 50}
		_, _, _ = s.RecordVote(k, AuthorityVote{Voter: "v1"}, 1)
	}
	out := s.Promote(50)
	if len(out) != 3 {
		t.Fatalf("len(out) = %d, want 3", len(out))
	}
	if out[0].Address != "aaa" || out[1].Address != "mmm" || out[2].Address != "zzz" {
		t.Errorf("Promote did not sort by address asc: %+v", out)
	}
}

// -----------------------------------------------------------------------------
// DropVotesByAuthority
// -----------------------------------------------------------------------------

func TestDropVotesByAuthority_RemovesVotesFromOpenProposalsOnly(t *testing.T) {
	s := NewInMemoryAuthorityVoteStore()
	openKey := AuthorityVoteKey{Op: AuthorityOpAdd, Address: "x", EffectiveHeight: 100}
	crossedKey := AuthorityVoteKey{Op: AuthorityOpAdd, Address: "y", EffectiveHeight: 100}
	_, _, _ = s.RecordVote(openKey, AuthorityVote{Voter: "alice"}, 3)
	_, _, _ = s.RecordVote(openKey, AuthorityVote{Voter: "bob"}, 3) // 2/3 threshold = crossed!
	// reset openKey to truly open by using N=4 (threshold=3).
	s = NewInMemoryAuthorityVoteStore()
	_, _, _ = s.RecordVote(openKey, AuthorityVote{Voter: "alice"}, 4)
	_, _, _ = s.RecordVote(openKey, AuthorityVote{Voter: "bob"}, 4)
	_, _, _ = s.RecordVote(crossedKey, AuthorityVote{Voter: "alice"}, 1) // single-auth
	_, _, _ = s.RecordVote(crossedKey, AuthorityVote{Voter: "bob"}, 1)

	if openProp, _ := s.Lookup(openKey); openProp.Crossed {
		t.Fatalf("openKey unexpectedly crossed")
	}
	if crossedProp, _ := s.Lookup(crossedKey); !crossedProp.Crossed {
		t.Fatalf("crossedKey unexpectedly open")
	}

	changed := s.DropVotesByAuthority("alice")
	if len(changed) != 1 {
		t.Fatalf("changed proposals = %d, want 1 (only the open one)", len(changed))
	}
	openAfter, ok := s.Lookup(openKey)
	if !ok {
		t.Fatal("open proposal disappeared")
	}
	if len(openAfter.Voters) != 1 || openAfter.Voters[0].Voter != "bob" {
		t.Errorf("open voters after drop = %+v, want [bob]", openAfter.Voters)
	}
	crossedAfter, _ := s.Lookup(crossedKey)
	if len(crossedAfter.Voters) != 2 {
		t.Errorf("crossed voters mutated = %+v", crossedAfter.Voters)
	}
}

func TestDropVotesByAuthority_DeletesProposalAfterLastVoter(t *testing.T) {
	s := NewInMemoryAuthorityVoteStore()
	k := AuthorityVoteKey{Op: AuthorityOpAdd, Address: "x", EffectiveHeight: 100}
	_, _, _ = s.RecordVote(k, AuthorityVote{Voter: "alice"}, 4)

	abandoned := s.DropVotesByAuthority("alice")
	if len(abandoned) != 1 || len(abandoned[0].Voters) != 0 {
		t.Fatalf("abandoned = %+v, want one entry with empty Voters", abandoned)
	}
	if _, ok := s.Lookup(k); ok {
		t.Error("proposal not deleted after dropping last voter")
	}
}

// -----------------------------------------------------------------------------
// RecomputeCrossed
// -----------------------------------------------------------------------------

func TestRecomputeCrossed_CrossesUnderSmallerN(t *testing.T) {
	s := NewInMemoryAuthorityVoteStore()
	k := AuthorityVoteKey{Op: AuthorityOpAdd, Address: "x", EffectiveHeight: 100}
	// 2 voters under N=4 (threshold=3) → not crossed.
	_, _, _ = s.RecordVote(k, AuthorityVote{Voter: "alice"}, 4)
	_, _, _ = s.RecordVote(k, AuthorityVote{Voter: "bob"}, 4)
	if p, _ := s.Lookup(k); p.Crossed {
		t.Fatal("unexpectedly crossed at N=4")
	}
	// AuthorityList shrinks to 3 → threshold=2 → 2 voters cross.
	got := s.RecomputeCrossed(3, 999)
	if len(got) != 1 {
		t.Fatalf("newlyCrossed = %d, want 1", len(got))
	}
	p, _ := s.Lookup(k)
	if !p.Crossed {
		t.Fatal("RecomputeCrossed didn't flip Crossed=true")
	}
	if p.CrossedAtHeight != 999 {
		t.Errorf("CrossedAtHeight = %d, want 999", p.CrossedAtHeight)
	}
}

func TestRecomputeCrossed_NoOpForCrossed(t *testing.T) {
	s := NewInMemoryAuthorityVoteStore()
	k := AuthorityVoteKey{Op: AuthorityOpAdd, Address: "x", EffectiveHeight: 100}
	_, _, _ = s.RecordVote(k, AuthorityVote{Voter: "alice"}, 1)
	if p, _ := s.Lookup(k); !p.Crossed {
		t.Fatal("expected pre-crossed setup")
	}
	got := s.RecomputeCrossed(1, 12345)
	if len(got) != 0 {
		t.Errorf("RecomputeCrossed returned %d for already-crossed proposals", len(got))
	}
}

// -----------------------------------------------------------------------------
// AllProposals + cloneProposal isolation
// -----------------------------------------------------------------------------

func TestAllProposals_ReturnsIndependentCopy(t *testing.T) {
	s := NewInMemoryAuthorityVoteStore()
	k := AuthorityVoteKey{Op: AuthorityOpAdd, Address: "x", EffectiveHeight: 100}
	_, _, _ = s.RecordVote(k, AuthorityVote{Voter: "alice"}, 3)

	out := s.AllProposals()
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	out[0].Voters[0].Voter = "TAMPERED"
	out[0].Crossed = true

	again := s.AllProposals()
	if again[0].Voters[0].Voter != "alice" {
		t.Error("store mutated by caller (Voters not deep-copied)")
	}
	if again[0].Crossed {
		t.Error("store mutated by caller (Crossed flag tampered)")
	}
}

// -----------------------------------------------------------------------------
// markCrossedForTesting (exercised via load path normally)
// -----------------------------------------------------------------------------

func TestMarkCrossedForTesting_NoOpOnUnknownKey(t *testing.T) {
	s := NewInMemoryAuthorityVoteStore()
	s.markCrossedForTesting(
		AuthorityVoteKey{Op: AuthorityOpAdd, Address: "ghost", EffectiveHeight: 1},
		42,
	)
	if len(s.AllProposals()) != 0 {
		t.Error("markCrossedForTesting created a proposal out of thin air")
	}
}
