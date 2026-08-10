package chain

import (
	"sync"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
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

// blockRecorder counts sealed blocks and their transactions.
type blockRecorder struct {
	noopRecorder
	mu     sync.Mutex
	blocks int
	txs    int
}

func (b *blockRecorder) RecordBlockSealed(txCount int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.blocks++
	b.txs += txCount
}

// TestBlockProducer_recordsSealedBlocks covers the last of the four
// dashboard figures that had no producer: pkg/chain/block.go and
// internal/blockdriver touched no metric at all, so a node that had sealed
// 464k blocks reported nothing about any of them.
//
// Kept separate from the Transactions* counters on purpose. Those fire on
// the inbound-gossip / wallet-ingest path, so reusing them here would
// double-count any transaction that both arrived over gossip and landed in
// a block — a confidently wrong number, which is worse than a missing one.
func TestBlockProducer_recordsSealedBlocks(t *testing.T) {
	rec := &blockRecorder{}
	SetChainMetricsRecorder(rec)
	t.Cleanup(func() { SetChainMetricsRecorder(noopRecorder{}) })

	as := NewAccountStore()
	as.Credit("alice", 1000)
	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, as, DefaultProducerConfig())

	pool.Add(&mempool.Tx{ID: "t1", Sender: "alice", Recipient: "bob", Amount: 1, Fee: 0, Nonce: 0})
	if _, err := bp.ProduceBlock(); err != nil {
		t.Fatalf("ProduceBlock: %v", err)
	}

	rec.mu.Lock()
	blocks, txs := rec.blocks, rec.txs
	rec.mu.Unlock()
	if blocks != 1 {
		t.Fatalf("a sealed block must be counted, got %d", blocks)
	}
	if txs != 1 {
		t.Fatalf("the block's transactions must be counted, got %d", txs)
	}
}

// A production attempt that fails must not register as a sealed block.
func TestBlockProducer_doesNotCountFailedProduction(t *testing.T) {
	rec := &blockRecorder{}
	SetChainMetricsRecorder(rec)
	t.Cleanup(func() { SetChainMetricsRecorder(noopRecorder{}) })

	as := NewAccountStore()
	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, as, DefaultProducerConfig())

	// Empty pool -> no block.
	if _, err := bp.ProduceBlock(); err == nil {
		t.Fatal("expected production to fail with an empty pool")
	}

	rec.mu.Lock()
	blocks := rec.blocks
	rec.mu.Unlock()
	if blocks != 0 {
		t.Fatalf("a failed production must not count as sealed, got %d", blocks)
	}
}
