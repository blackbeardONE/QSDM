package chain

import (
	"context"
	"sync"
	"testing"
	"time"
)

func loopTestConsensus(t *testing.T, timeout time.Duration) (*BFTConsensus, ConsensusConfig) {
	t.Helper()
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	for _, a := range []string{"v1", "v2", "v3"} {
		if err := vs.Register(a, 100); err != nil {
			t.Fatalf("register %s: %v", a, err)
		}
	}
	cfg := DefaultConsensusConfig()
	cfg.RoundTimeout = timeout
	return NewBFTConsensus(vs, cfg), cfg
}

// The loop must actually expire a round on its own, driven by nothing but the
// passage of time.
//
// Every other round-timeout test calls TickRoundTimeouts directly with a
// synthetic clock, so all of them pass against a build where the production
// loop was deleted. This one starts the real loop and waits. It is the only
// test on this branch that would fail if the wiring in cmd/qsdm's main() were
// removed, which is what a whole-branch review flagged as missing.
func TestStartRoundTimeoutLoop_expiresARoundOnItsOwn(t *testing.T) {
	bc, cfg := loopTestConsensus(t, 60*time.Millisecond)

	prop, err := bc.ProposerForRound(0)
	if err != nil {
		t.Fatalf("proposer: %v", err)
	}
	if _, err := bc.Propose(9, 0, prop, "hash-r0"); err != nil {
		t.Fatalf("propose: %v", err)
	}
	if got := bc.NextRoundAfterTimeout(9); got != 0 {
		t.Fatalf("precondition: height 9 should have no retired round yet, got %d", got)
	}

	var mu sync.Mutex
	var seenHeights []uint64
	var seenNext uint32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go StartRoundTimeoutLoop(ctx, bc, cfg, func(h []uint64, next uint32) {
		mu.Lock()
		defer mu.Unlock()
		seenHeights = append(seenHeights, h...)
		seenNext = next
	})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if bc.NextRoundAfterTimeout(9) > 0 {
			mu.Lock()
			heights, next := append([]uint64(nil), seenHeights...), seenNext
			mu.Unlock()
			if len(heights) == 0 || heights[0] != 9 {
				t.Fatalf("observer should have been told height 9 expired, got %v", heights)
			}
			if next == 0 {
				t.Fatalf("observer received next_round=0 for an expired round")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the round-timeout loop never expired height 9: an expired round stays " +
		"proposable forever, which is the stuck-round bug this wiring exists to prevent")
}

// Cancelling the context must stop the loop, or a node shutdown leaks a
// goroutine that keeps mutating consensus state after the rest of the process
// has torn down.
func TestStartRoundTimeoutLoop_stopsOnContextCancel(t *testing.T) {
	bc, cfg := loopTestConsensus(t, 30*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		StartRoundTimeoutLoop(ctx, bc, cfg, nil)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("loop did not return after context cancellation; shutdown would leak it")
	}
}

// A zero or negative RoundTimeout must not panic time.NewTicker. The floor is
// the only thing standing between a misconfigured timeout and a crash at boot.
func TestStartRoundTimeoutLoop_survivesZeroTimeout(t *testing.T) {
	bc, cfg := loopTestConsensus(t, 0)
	cfg.RoundTimeout = 0

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("zero RoundTimeout panicked: %v", r)
			}
			close(done)
		}()
		StartRoundTimeoutLoop(ctx, bc, cfg, nil)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("loop did not return")
	}
}

// A nil consensus must not panic. main() constructs the instance before this
// call today, but a future reordering that starts the loop first should degrade
// to doing nothing rather than crashing the node at boot.
func TestStartRoundTimeoutLoop_nilConsensusReturns(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		StartRoundTimeoutLoop(context.Background(), nil, DefaultConsensusConfig(), nil)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("nil consensus should return immediately, not block")
	}
}
