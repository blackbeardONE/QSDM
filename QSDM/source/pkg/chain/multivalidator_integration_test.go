package chain

import (
	"testing"
	"time"
)

// Multi-validator integration properties.
//
// These cover the guarantees a real two-node network depends on, which the
// per-component unit tests do not: two independently-constructed nodes must
// agree on WHO the validators are, agree on WHOSE TURN it is, and between
// them elect exactly one proposer per round — with duty rotating when the
// current proposer fails to seal.
//
// Getting any of these wrong produces a chain that either halts (nobody
// believes it is their turn) or forks (everybody does).

// twoNodeState is bonded stake as both nodes would read it from committed
// chain state.
func twoNodeState() fakeBonded {
	return fakeBonded{
		"node-a": 500,
		"node-b": 300,
	}
}

func setFromState(t *testing.T, src ValidatorMembershipSource) *ValidatorSet {
	t.Helper()
	cfg := DefaultValidatorSetConfig()
	cfg.MinStake = 100
	vs, err := ValidatorSetFromChainState(cfg, src)
	if err != nil {
		t.Fatalf("derive validator set: %v", err)
	}
	return vs
}

// Both nodes must elect the SAME proposer for a given round. If they
// disagree, two nodes seal at the same height and the chain forks.
func TestMultiValidator_nodesAgreeOnProposer(t *testing.T) {
	state := twoNodeState()
	nodeA := NewBFTConsensus(setFromState(t, state), DefaultConsensusConfig())
	nodeB := NewBFTConsensus(setFromState(t, state), DefaultConsensusConfig())

	for round := uint32(0); round < 8; round++ {
		a, errA := nodeA.ProposerForRound(round)
		b, errB := nodeB.ProposerForRound(round)
		if errA != nil || errB != nil {
			t.Fatalf("round %d: errA=%v errB=%v", round, errA, errB)
		}
		if a != b {
			t.Fatalf("round %d: nodes disagree on proposer (%s vs %s) — this forks the chain",
				round, a, b)
		}
	}
}

// Exactly one validator may consider itself proposer in any round.
// Zero means the chain halts; more than one means it forks.
func TestMultiValidator_exactlyOneProposerPerRound(t *testing.T) {
	state := twoNodeState()
	bc := NewBFTConsensus(setFromState(t, state), DefaultConsensusConfig())

	for round := uint32(0); round < 8; round++ {
		proposer, err := bc.ProposerForRound(round)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		claims := 0
		for _, me := range []string{"node-a", "node-b"} {
			if proposer == me {
				claims++
			}
		}
		if claims != 1 {
			t.Fatalf("round %d: %d validators claim proposership (want exactly 1)", round, claims)
		}
	}
}

// TestMultiValidator_dutyRotatesOnTimeout is the failover property, and the
// reason the production gate must consult the CURRENT round rather than 0.
//
// proposerForRoundLocked selects idx = round % len(active), so round 0
// always resolves to the highest-staked validator. A gate hardcoding round 0
// would mean only that node ever produces — and if it went offline the chain
// would halt with every other validator politely declining forever, which is
// the single point of failure this whole line of work exists to remove.
func TestMultiValidator_dutyRotatesOnTimeout(t *testing.T) {
	state := twoNodeState()
	bc := NewBFTConsensus(setFromState(t, state), DefaultConsensusConfig())

	// Round 0 goes to the highest stake.
	first, err := bc.ProposerForRound(0)
	if err != nil {
		t.Fatal(err)
	}
	if first != "node-a" {
		t.Fatalf("round 0 should go to the highest-staked validator, got %s", first)
	}

	// Round 1 must go to someone else, or failover cannot work.
	second, err := bc.ProposerForRound(1)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("round 1 must rotate to a different validator, or a stalled " +
			"proposer halts the chain permanently")
	}
}

// A stalled proposer must actually escalate the round, so the gate's
// NextRoundAfterTimeout lookup returns something new to act on.
func TestMultiValidator_timeoutEscalatesRound(t *testing.T) {
	state := twoNodeState()
	cfg := DefaultConsensusConfig()
	cfg.RoundTimeout = 10 * time.Millisecond
	bc := NewBFTConsensus(setFromState(t, state), cfg)

	const height = uint64(1)
	if bc.NextRoundAfterTimeout(height) != 0 {
		t.Fatal("a fresh height should start at round 0")
	}

	proposer, err := bc.ProposerForRound(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bc.Propose(height, 0, proposer, "block-hash"); err != nil {
		t.Fatalf("propose: %v", err)
	}

	// Let the round expire without a commit, then tick timeouts.
	timedOut := bc.TickRoundTimeouts(time.Now().Add(time.Second))
	if len(timedOut) == 0 {
		t.Fatal("an expired round should be reported as timed out")
	}

	next := bc.NextRoundAfterTimeout(height)
	if next == 0 {
		t.Fatal("a timed-out round must escalate, or proposer duty never rotates")
	}

	// And the escalated round must name a different proposer.
	newProposer, err := bc.ProposerForRound(next)
	if err != nil {
		t.Fatal(err)
	}
	if newProposer == proposer {
		t.Fatalf("escalated round %d still names the stalled proposer %s", next, newProposer)
	}
}

// A single bonded node must be able to produce alone — the "one home PC
// keeps the chain alive" case. With one active validator every round
// resolves to it.
func TestMultiValidator_singleNodeIsAlwaysProposer(t *testing.T) {
	bc := NewBFTConsensus(setFromState(t, fakeBonded{"home-pc": 250}), DefaultConsensusConfig())
	for round := uint32(0); round < 5; round++ {
		p, err := bc.ProposerForRound(round)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if p != "home-pc" {
			t.Fatalf("a lone validator must be proposer every round, got %s at round %d", p, round)
		}
	}
}

// Membership changes must not desynchronise the two nodes: after the same
// reconcile, they still agree on the proposer.
func TestMultiValidator_agreementSurvivesMembershipChange(t *testing.T) {
	state := twoNodeState()
	setA := setFromState(t, state)
	setB := setFromState(t, state)

	// A third validator bonds in on both nodes.
	grown := fakeBonded{"node-a": 500, "node-b": 300, "node-c": 900}
	if _, _, err := ReconcileValidatorMembership(setA, grown); err != nil {
		t.Fatalf("reconcile A: %v", err)
	}
	if _, _, err := ReconcileValidatorMembership(setB, grown); err != nil {
		t.Fatalf("reconcile B: %v", err)
	}

	nodeA := NewBFTConsensus(setA, DefaultConsensusConfig())
	nodeB := NewBFTConsensus(setB, DefaultConsensusConfig())

	for round := uint32(0); round < 6; round++ {
		a, _ := nodeA.ProposerForRound(round)
		b, _ := nodeB.ProposerForRound(round)
		if a != b {
			t.Fatalf("after membership change, round %d disagrees: %s vs %s", round, a, b)
		}
	}

	// The new highest stake should now lead round 0.
	if p, _ := nodeA.ProposerForRound(0); p != "node-c" {
		t.Fatalf("round 0 should follow the new highest stake, got %s", p)
	}
}

// TestAdoptExternalCommit_unblocksASyncedNode covers the defect that stopped
// a fully-synced node from ever producing.
//
// The seal gate refuses to extend unless IsCommitted(tipHeight). A node that
// gets its tip from sync never voted that height through, so its committed
// map has no entry and the gate blocks it forever. Observed live as
// "BFT extension blocked until the current tip height is committed in BFT"
// repeating every tick on a node whose chain was perfectly in sync, meaning
// only a validator present since genesis could ever seal.
func TestAdoptExternalCommit_unblocksASyncedNode(t *testing.T) {
	bc := NewBFTConsensus(setFromState(t, fakeBonded{"home-pc": 250}), DefaultConsensusConfig())

	const syncedTip = uint64(464829)
	if bc.IsCommitted(syncedTip) {
		t.Fatal("precondition: a synced height must not already be committed")
	}

	bc.AdoptExternalCommit(syncedTip, "state-root-abc", "remote-producer")

	if !bc.IsCommitted(syncedTip) {
		t.Fatal("an adopted block must count as committed, or the node can never extend")
	}
	got, ok := bc.GetCommitted(syncedTip)
	if !ok || got.BlockHash != "state-root-abc" {
		t.Fatalf("adopted commit should record the block, got %+v", got)
	}
}

// Adoption must never overwrite a commit this node voted through itself.
func TestAdoptExternalCommit_doesNotOverwriteLocalCommit(t *testing.T) {
	bc := NewBFTConsensus(setFromState(t, fakeBonded{"home-pc": 250}), DefaultConsensusConfig())

	const height = uint64(1)
	proposer, err := bc.ProposerForRound(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bc.Propose(height, 0, proposer, "locally-voted"); err != nil {
		t.Fatal(err)
	}
	if err := bc.PreVote(height, "home-pc", "locally-voted"); err != nil {
		t.Fatal(err)
	}
	if err := bc.PreCommit(height, "home-pc", "locally-voted"); err != nil {
		t.Fatal(err)
	}
	if !bc.IsCommitted(height) {
		t.Fatal("precondition: the lone validator should have committed locally")
	}

	bc.AdoptExternalCommit(height, "adopted-different", "someone-else")

	got, _ := bc.GetCommitted(height)
	if got.BlockHash != "locally-voted" {
		t.Fatalf("adoption must not overwrite a locally-voted commit, got %q", got.BlockHash)
	}
}
