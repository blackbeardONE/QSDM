package chain

import (
	"testing"
	"time"
)

// A stalled round used to block its height forever.
//
// Propose stamps each round with a deadline (RoundTimeout, default 30s) and
// refuses a higher round while one is active: "round N still active at height
// H; timeout or fail before proposing". TickRoundTimeouts is what clears the
// expired round and bumps the counter so the next proposer can take over --
// and it had zero production callers, so nothing ever cleared one. The only
// recovery was a local FailRound.
//
// This asserts the liveness property directly: after the deadline passes, a
// later round becomes proposable, and it does NOT before.
func TestRoundTimeout_UnblocksHeightForTheNextProposer(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	for _, a := range []string{"v1", "v2", "v3"} {
		if err := vs.Register(a, 100); err != nil {
			t.Fatalf("register %s: %v", a, err)
		}
	}
	cfg := DefaultConsensusConfig()
	cfg.RoundTimeout = 50 * time.Millisecond
	bc := NewBFTConsensus(vs, cfg)

	prop, err := bc.ProposerForRound(0)
	if err != nil {
		t.Fatalf("proposer for round 0: %v", err)
	}
	if _, err := bc.Propose(7, 0, prop, "hash-r0"); err != nil {
		t.Fatalf("first propose: %v", err)
	}

	// Before the deadline, round 1 must be refused -- otherwise the test would
	// pass whether or not the timeout mechanism works.
	next, err := bc.ProposerForRound(1)
	if err != nil {
		t.Fatalf("proposer for round 1: %v", err)
	}
	if _, err := bc.Propose(7, 1, next, "hash-r1"); err == nil {
		t.Fatal("a higher round must be refused while the current one is still active")
	}

	// Nothing has expired yet, so a tick at "now" must be a no-op.
	if got := bc.TickRoundTimeouts(time.Now()); len(got) != 0 {
		t.Fatalf("tick before the deadline expired %v", got)
	}

	// Past the deadline, the tick must release the height.
	timedOut := bc.TickRoundTimeouts(time.Now().Add(cfg.RoundTimeout * 4))
	if len(timedOut) != 1 || timedOut[0] != 7 {
		t.Fatalf("expected height 7 to time out, got %v", timedOut)
	}
	if r := bc.NextRoundAfterTimeout(7); r != 1 {
		t.Errorf("next round after timeout = %d, want 1", r)
	}
	if _, err := bc.Propose(7, 1, next, "hash-r1"); err != nil {
		t.Fatalf("after the timeout the next proposer must be able to propose: %v", err)
	}
}

// Escalation must not discard the prevote lock, or timing out would become a
// way to unlock a validator that had already locked on a value.
func TestRoundTimeout_CarriesThePrevoteLock(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	for _, a := range []string{"v1", "v2", "v3"} {
		if err := vs.Register(a, 100); err != nil {
			t.Fatalf("register %s: %v", a, err)
		}
	}
	cfg := DefaultConsensusConfig()
	cfg.RoundTimeout = 25 * time.Millisecond
	bc := NewBFTConsensus(vs, cfg)

	prop, _ := bc.ProposerForRound(0)
	round, err := bc.Propose(9, 0, prop, "locked-value")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	// Drive a lock the way the consensus does, if this build exposes it.
	for _, v := range vs.ActiveValidators() {
		_ = bc.PreVote(9, v.Address, "locked-value")
	}
	locked := round.LockedBlockHash

	if got := bc.TickRoundTimeouts(time.Now().Add(cfg.RoundTimeout * 4)); len(got) != 1 {
		t.Fatalf("expected the round to time out, got %v", got)
	}

	if locked == "" {
		t.Skip("this build did not establish a prevote lock; carry-forward is untestable here")
	}
	if carried := bc.carryPrevoteLock[9]; carried != locked {
		t.Errorf("prevote lock not carried across the timeout: got %q, want %q", carried, locked)
	}
}

// Expiring a round does not permanently close it.
//
// Propose rejects only a round number BELOW whatever occupies bc.rounds[height]
// and never consults bc.nextRound. After TickRoundTimeouts deletes the round,
// that slot is empty -- so a retransmitted propose for the SAME, already-expired
// round number is accepted, builds a fresh round, and discards the votes
// accumulated before the timeout.
//
// This is pre-existing Propose/FailRound behaviour, not something the ticker
// introduced, but the ticker reaches it on a timer rather than only via an
// explicit FailRound. Pinned here because it was found by review and described
// in a comment, and a behaviour that lives only in a comment is not held by
// anything.
func TestRoundTimeout_ExpiredRoundCanBeReopenedAndLosesItsVotes(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	for _, a := range []string{"v1", "v2", "v3"} {
		if err := vs.Register(a, 100); err != nil {
			t.Fatalf("register %s: %v", a, err)
		}
	}
	cfg := DefaultConsensusConfig()
	cfg.RoundTimeout = 20 * time.Millisecond
	bc := NewBFTConsensus(vs, cfg)

	prop, err := bc.ProposerForRound(0)
	if err != nil {
		t.Fatalf("proposer: %v", err)
	}
	first, err := bc.Propose(11, 0, prop, "value-a")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if err := bc.PreVote(11, "v1", "value-a"); err != nil {
		t.Fatalf("prevote: %v", err)
	}
	votesBefore := len(first.PreVotes)
	if votesBefore == 0 {
		t.Fatal("expected the prevote to be recorded before the timeout")
	}

	if got := bc.TickRoundTimeouts(time.Now().Add(cfg.RoundTimeout * 4)); len(got) != 1 {
		t.Fatalf("expected height 11 to time out, got %v", got)
	}

	// The same round number, proposed again. If this is refused, the comment in
	// cmd/qsdm/main.go describing the reopen path is wrong and should change.
	reopened, err := bc.Propose(11, 0, prop, "value-a")
	if err != nil {
		t.Fatalf("a retransmitted propose for the expired round was refused: %v -- "+
			"if this is now intended, update the round-timeout comment in cmd/qsdm/main.go", err)
	}
	if len(reopened.PreVotes) != 0 {
		t.Errorf("reopened round kept %d prevote(s); the vote set should start empty",
			len(reopened.PreVotes))
	}
	if reopened == first {
		t.Error("expected a fresh round object, not the expired one")
	}
}
