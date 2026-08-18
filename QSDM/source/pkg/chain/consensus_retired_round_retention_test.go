package chain

import (
	"errors"
	"testing"
	"time"
)

// The retired-round guard must survive a LATER height committing.
//
// bc.nextRound looks like bookkeeping and is not: consensus.go:187 reads it to
// return ErrBFTRoundRetired, and the comment at :168-186 records why. bc.rounds
// is keyed by height rather than (height, round), so without consulting
// nextRound a propose for an already-retired round rebuilds that round with an
// empty vote slate. Today that discards votes; the moment this node originates
// its own votes it is the same validator voting twice for one
// (validator, height, round) with different values -- self-generated
// equivocation. The critique of the vote-origination design called this a
// prerequisite for that work, not a follow-up cleanup.
//
// Audit §9b records that nextRound and carryPrevoteLock grow without bound,
// because an abandoned height's entry is only removed when THAT height commits.
// The obvious fix is to sweep entries below the newest committed height. That
// fix would delete the guard for exactly the heights a late or replayed propose
// targets, silently reverting the protection above.
//
// So this test pins the retention, not the leak. If someone bounds those maps,
// this fails and tells them which vulnerability they are about to reopen. The
// existing coverage in round_timeout_liveness_test.go only refuses a retired
// round at the same height immediately, which a sweep would leave passing.
func TestBFT_retiredRoundGuardSurvivesLaterHeightCommit(t *testing.T) {
	bc, _ := setupBFT(t)
	cfg := bc.cfg

	// Height 13 times out at round 0, so round 0 is retired there.
	prop, err := bc.ProposerForRound(0)
	if err != nil {
		t.Fatalf("proposer: %v", err)
	}
	if _, err := bc.Propose(13, 0, prop, "v"); err != nil {
		t.Fatalf("propose 13: %v", err)
	}
	bc.TickRoundTimeouts(time.Now().Add(cfg.RoundTimeout * 4))

	if got := bc.NextRoundAfterTimeout(13); got == 0 {
		t.Fatalf("precondition: height 13 should have a retired round recorded, got next=%d", got)
	}

	// A LATER height runs to commit. consensus.go:390 deletes only height 14's
	// own entries; height 13's must be untouched.
	if _, err := bc.Propose(14, 0, prop, "hash-14"); err != nil {
		t.Fatalf("propose 14: %v", err)
	}
	for _, v := range []string{"v1", "v2", "v3"} {
		if err := bc.PreVote(14, v, "hash-14"); err != nil {
			t.Fatalf("prevote 14 by %s: %v", v, err)
		}
	}
	for _, v := range []string{"v1", "v2", "v3"} {
		if err := bc.PreCommit(14, v, "hash-14"); err != nil {
			t.Fatalf("precommit 14 by %s: %v", v, err)
		}
	}
	if !bc.IsCommitted(14) {
		t.Fatal("precondition: height 14 must commit for this test to exercise the delete path")
	}

	// The point of the test.
	if got := bc.NextRoundAfterTimeout(13); got == 0 {
		t.Fatal("height 13's retired-round record was dropped once height 14 committed. " +
			"If this is a fix for the unbounded growth in audit §9b, it reopens the " +
			"retired-round hole: consensus.go:187 can no longer refuse a stale round 0 at " +
			"height 13, and a propose rebuilds that round with an empty vote slate. Bound " +
			"bc.committed instead -- it is never deleted from at all and holds a whole " +
			"*ConsensusRound per height, so it is the larger leak.")
	}
	if _, err := bc.Propose(13, 0, prop, "v"); !errors.Is(err, ErrBFTRoundRetired) {
		t.Fatalf("a retired round at an older height must still be refused after a later "+
			"height commits; got %v", err)
	}
}

// carryPrevoteLock must survive a later height committing too, and for a
// different reason than nextRound.
//
// A reviewer caught that the test above pins only one of the two maps audit §9b
// names. carryPrevoteLock is the POL-style lock carry: TickRoundTimeouts stores
// a round's LockedBlockHash at consensus.go:257, and a round>0 Propose seeds
// cr.LockedBlockHash from it at :221-223. validatePreCommitAgainstLock (:544) is
// a NO-OP when LockedBlockHash is empty, so losing the carry does not error --
// it silently stops checking. A precommit quorum could then form on a value the
// lock should have refused, which is a safety violation rather than the
// liveness/equivocation cost of losing nextRound.
//
// The reviewer demonstrated the gap by sweeping carryPrevoteLock alone, leaving
// nextRound intact: the entire pkg/chain suite passed, this file included.
func TestBFT_prevoteLockCarrySurvivesLaterHeightCommit(t *testing.T) {
	bc, _ := setupBFT(t)
	cfg := bc.cfg
	const locked = "locked-at-13"

	prop0, err := bc.ProposerForRound(0)
	if err != nil {
		t.Fatalf("proposer round 0: %v", err)
	}
	if _, err := bc.Propose(13, 0, prop0, locked); err != nil {
		t.Fatalf("propose 13: %v", err)
	}
	// A prevote polka is what creates the lock in the first place.
	for _, v := range []string{"v1", "v2"} {
		if err := bc.PreVote(13, v, locked); err != nil {
			t.Fatalf("prevote 13 by %s: %v", v, err)
		}
	}
	if cr, ok := bc.GetRound(13); !ok || cr.LockedBlockHash != locked {
		t.Fatalf("precondition: a 2/3 prevote polka must lock the value, got %+v (found=%v)", cr, ok)
	}
	bc.TickRoundTimeouts(time.Now().Add(cfg.RoundTimeout * 4))

	// A later height commits, exercising the delete path at consensus.go:388-391.
	if _, err := bc.Propose(14, 0, prop0, "hash-14"); err != nil {
		t.Fatalf("propose 14: %v", err)
	}
	for _, v := range []string{"v1", "v2", "v3"} {
		if err := bc.PreVote(14, v, "hash-14"); err != nil {
			t.Fatalf("prevote 14 by %s: %v", v, err)
		}
	}
	for _, v := range []string{"v1", "v2", "v3"} {
		if err := bc.PreCommit(14, v, "hash-14"); err != nil {
			t.Fatalf("precommit 14 by %s: %v", v, err)
		}
	}
	if !bc.IsCommitted(14) {
		t.Fatal("precondition: height 14 must commit for this test to exercise the delete path")
	}

	// The point of the test: round 1 at height 13 must still inherit the lock.
	// Asserted through the behaviour that depends on it rather than by reading
	// the map, so the test still means something if the carry is refactored.
	prop1, err := bc.ProposerForRound(1)
	if err != nil {
		t.Fatalf("proposer round 1: %v", err)
	}
	cr, err := bc.Propose(13, 1, prop1, "some-other-value")
	if err != nil {
		t.Fatalf("propose 13 round 1: %v", err)
	}
	if cr.LockedBlockHash != locked {
		t.Fatalf("round 1 at height 13 lost the carried prevote lock (got %q, want %q) after "+
			"height 14 committed. If this is a fix for the unbounded growth in audit §9b, it is "+
			"a SAFETY regression, not just a liveness one: validatePreCommitAgainstLock "+
			"(consensus.go:544) is a no-op on an empty LockedBlockHash, so it stops refusing "+
			"precommits that conflict with the locked value instead of erroring. Bound "+
			"bc.committed instead -- it is never deleted from at all.", cr.LockedBlockHash, locked)
	}
}
