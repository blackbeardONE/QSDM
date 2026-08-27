package chain

import (
	"context"
	"time"
)

// RoundTimeoutObserver is called once per tick that expired at least one round.
// heights are the expired heights; nextRound is the round number the first of
// them has been advanced to.
type RoundTimeoutObserver func(heights []uint64, nextRound uint32)

// StartRoundTimeoutLoop polls TickRoundTimeouts until ctx is cancelled.
//
// This was an anonymous goroutine inside cmd/qsdm's main(). It was correct, but
// it was also unreachable by any test: every round-timeout test constructs a
// BFTConsensus directly and calls TickRoundTimeouts with a synthetic time, so a
// refactor that gated this loop behind a config flag, or dropped it entirely,
// would compile and leave the whole suite green while silently restoring the
// stuck-round bug the loop exists to prevent. A whole-branch review flagged
// that gap; extracting the loop is what makes it testable at all.
//
// The interval is RoundTimeout/3 rather than RoundTimeout so a round is
// observed as expired within a third of its own deadline, instead of up to a
// full deadline late. The floor exists because a zero or negative RoundTimeout
// would otherwise panic time.NewTicker.
//
// Deliberately takes the same ConsensusConfig VALUE the caller built its
// BFTConsensus with, rather than calling DefaultConsensusConfig() again.
// Re-deriving it happens to produce an identical struct today, but nothing tied
// the two together, so making RoundTimeout configurable at one site and not the
// other would silently desync the tick from the deadline it polls.
//
// Blocks until ctx is done; callers run it in a goroutine.
func StartRoundTimeoutLoop(ctx context.Context, bc *BFTConsensus, cfg ConsensusConfig, observe RoundTimeoutObserver) {
	if bc == nil {
		return
	}
	interval := cfg.RoundTimeout / 3
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if timedOut := bc.TickRoundTimeouts(time.Now()); len(timedOut) > 0 && observe != nil {
				observe(timedOut, bc.NextRoundAfterTimeout(timedOut[0]))
			}
		case <-ctx.Done():
			return
		}
	}
}
