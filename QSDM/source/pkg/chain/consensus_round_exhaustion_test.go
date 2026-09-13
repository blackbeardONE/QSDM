package chain

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestBFT_RoundExhaustionCannotReopenRetiredRounds(t *testing.T) {
	for _, method := range []string{"fail", "timeout"} {
		t.Run(method, func(t *testing.T) {
			bc, _ := setupBFT(t)
			const height = 13
			retire := func() {
				t.Helper()
				if method == "fail" {
					if err := bc.FailRound(height); err != nil {
						t.Fatal(err)
					}
					return
				}
				cr, ok := bc.GetRound(height)
				if !ok {
					t.Fatal("missing round before timeout")
				}
				if got := bc.TickRoundTimeouts(cr.Deadline.Add(time.Second)); len(got) != 1 || got[0] != height {
					t.Fatalf("timed out heights = %v", got)
				}
			}

			// The last representable successor must remain usable and retain its lock.
			proposer, err := bc.ProposerForRound(math.MaxUint32 - 1)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := bc.Propose(height, math.MaxUint32-1, proposer, "locked"); err != nil {
				t.Fatal(err)
			}
			for _, validator := range []string{"v1", "v2"} {
				if err := bc.PreVote(height, validator, "locked"); err != nil {
					t.Fatal(err)
				}
			}
			retire()
			if got := bc.NextRoundAfterTimeout(height); got != math.MaxUint32 {
				t.Fatalf("last successor = %d, want %d", got, uint32(math.MaxUint32))
			}
			proposer, err = bc.ProposerForRound(math.MaxUint32)
			if err != nil {
				t.Fatal(err)
			}
			cr, err := bc.Propose(height, math.MaxUint32, proposer, "locked")
			if err != nil {
				t.Fatal(err)
			}
			if cr.LockedBlockHash != "locked" {
				t.Fatalf("last round lost carried lock: %q", cr.LockedBlockHash)
			}
			retire()
			if got := bc.NextRoundAfterTimeout(height); got != math.MaxUint32 {
				t.Errorf("exhausted successor wrapped to %d; want saturation at %d", got, uint32(math.MaxUint32))
			}

			assertRetired := func() {
				t.Helper()
				for _, round := range []uint32{0, 1, math.MaxUint32 - 1, math.MaxUint32} {
					proposer, err := bc.ProposerForRound(round)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := bc.Propose(height, round, proposer, "conflict"); !errors.Is(err, ErrBFTRoundRetired) {
						t.Fatalf("exhausted height accepted retired round %d: %v", round, err)
					}
				}
				if _, ok := bc.GetRound(height); ok {
					t.Fatal("replay recreated an active round")
				}
			}
			assertRetired()
			if got := bc.TickRoundTimeouts(time.Now().Add(time.Hour)); len(got) != 0 {
				t.Fatalf("exhausted height timed out again: %v", got)
			}
			if err := bc.FailRound(height); err == nil {
				t.Fatal("exhausted height still has an active round")
			}

			// Committing a different height must neither clear exhaustion nor stall.
			if _, err := bc.Propose(height+1, 0, "v1", "next-height"); err != nil {
				t.Fatal(err)
			}
			for _, validator := range []string{"v1", "v2"} {
				if err := bc.PreVote(height+1, validator, "next-height"); err != nil {
					t.Fatal(err)
				}
			}
			for _, validator := range []string{"v1", "v2"} {
				if err := bc.PreCommit(height+1, validator, "next-height"); err != nil {
					t.Fatal(err)
				}
			}
			if !bc.IsCommitted(height + 1) {
				t.Fatal("exhaustion blocked a different height from committing")
			}
			assertRetired()
		})
	}
}

func TestBFT_RoundExhaustionLastRoundCanCommit(t *testing.T) {
	bc, _ := setupBFT(t)
	proposer, err := bc.ProposerForRound(math.MaxUint32)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bc.Propose(1, math.MaxUint32, proposer, "last-round"); err != nil {
		t.Fatal(err)
	}
	for _, validator := range []string{"v1", "v2"} {
		if err := bc.PreVote(1, validator, "last-round"); err != nil {
			t.Fatal(err)
		}
	}
	for _, validator := range []string{"v1", "v2"} {
		if err := bc.PreCommit(1, validator, "last-round"); err != nil {
			t.Fatal(err)
		}
	}
	if !bc.IsCommitted(1) {
		t.Fatal("last round did not commit")
	}
	if _, err := bc.Propose(1, 0, "v1", "conflict"); err == nil {
		t.Fatal("committed height reopened")
	}
}

func TestBFTExecutor_RoundExhaustionIgnoresWireReplay(t *testing.T) {
	bc, _ := setupBFT(t)
	ex := NewBFTExecutor(bc)
	send := func(round uint32) {
		t.Helper()
		proposer, err := bc.ProposerForRound(round)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := MarshalBFTWire(BFTWirePropose, BFTWireProposeMsg{
			Height: 1, Round: round, Proposer: proposer, BlockHash: "value",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := ex.ApplyInbound(payload); err != nil {
			t.Fatalf("round %d gossip should be accepted or benignly ignored: %v", round, err)
		}
	}
	send(math.MaxUint32)
	if err := bc.FailRound(1); err != nil {
		t.Fatal(err)
	}
	for _, round := range []uint32{0, math.MaxUint32} {
		send(round)
		if _, ok := bc.GetRound(1); ok {
			t.Fatalf("round %d wire replay reopened an exhausted height", round)
		}
	}
}
