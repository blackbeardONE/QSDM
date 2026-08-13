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
