package chain

import (
	"sync"
	"testing"
)

// countingRecorder counts the dashboard-facing consensus events.
type countingRecorder struct {
	noopRecorder
	mu        sync.Mutex
	proposals int
	votes     int
}

func (c *countingRecorder) RecordConsensusProposal() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.proposals++
}

func (c *countingRecorder) RecordConsensusVote() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.votes++
}

func (c *countingRecorder) counts() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.proposals, c.votes
}

// TestConsensus_feedsDashboardCounters is the regression test for the
// operator dashboard showing 0 proposals and 0 votes on a node that had
// produced hundreds of thousands of blocks.
//
// Metrics.IncrementProposalsCreated / IncrementVotesCast /
// IncrementQuarantinesTriggered were all defined, and GetStats surfaced
// their values to the dashboard, but a tree-wide search for production
// callers returned ZERO for all three. The figures could not be anything
// but 0 no matter how much work the node did.
func TestConsensus_feedsDashboardCounters(t *testing.T) {
	rec := &countingRecorder{}
	SetChainMetricsRecorder(rec)
	t.Cleanup(func() { SetChainMetricsRecorder(noopRecorder{}) })

	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register("v1", 1000); err != nil {
		t.Fatal(err)
	}
	bc := NewBFTConsensus(vs, DefaultConsensusConfig())

	proposer, err := bc.ProposerForRound(0)
	if err != nil {
		t.Fatalf("ProposerForRound: %v", err)
	}
	if _, err := bc.Propose(1, 0, proposer, "block-hash-1"); err != nil {
		t.Fatalf("Propose: %v", err)
	}

	p, v := rec.counts()
	if p != 1 {
		t.Fatalf("a proposal must be counted, got %d", p)
	}
	if v != 0 {
		t.Fatalf("no votes cast yet, got %d", v)
	}

	if err := bc.PreVote(1, "v1", "block-hash-1"); err != nil {
		t.Fatalf("PreVote: %v", err)
	}
	if _, v = rec.counts(); v != 1 {
		t.Fatalf("a prevote must be counted, got %d", v)
	}

	if err := bc.PreCommit(1, "v1", "block-hash-1"); err != nil {
		t.Fatalf("PreCommit: %v", err)
	}
	if _, v = rec.counts(); v != 2 {
		t.Fatalf("a precommit must also count as a vote, got %d", v)
	}
}

// Retried gossip must not inflate the counters: an idempotent re-propose
// and a duplicate vote are both rejected or absorbed upstream, and neither
// should move a figure the operator reads as real activity.
func TestConsensus_countersIgnoreDuplicates(t *testing.T) {
	rec := &countingRecorder{}
	SetChainMetricsRecorder(rec)
	t.Cleanup(func() { SetChainMetricsRecorder(noopRecorder{}) })

	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register("v1", 1000); err != nil {
		t.Fatal(err)
	}
	bc := NewBFTConsensus(vs, DefaultConsensusConfig())
	proposer, _ := bc.ProposerForRound(0)

	if _, err := bc.Propose(1, 0, proposer, "h"); err != nil {
		t.Fatal(err)
	}
	// Identical re-propose is idempotent and returns the existing round.
	if _, err := bc.Propose(1, 0, proposer, "h"); err != nil {
		t.Fatal(err)
	}
	if p, _ := rec.counts(); p != 1 {
		t.Fatalf("an idempotent re-propose must not double-count, got %d", p)
	}

	if err := bc.PreVote(1, "v1", "h"); err != nil {
		t.Fatal(err)
	}
	// Second prevote from the same validator is rejected.
	if err := bc.PreVote(1, "v1", "h"); err == nil {
		t.Fatal("a duplicate prevote should be rejected")
	}
	if _, v := rec.counts(); v != 1 {
		t.Fatalf("a rejected duplicate vote must not be counted, got %d", v)
	}
}
