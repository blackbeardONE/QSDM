package chain

import (
	"errors"
	"testing"
	"time"
)

func setupBFT(t *testing.T) (*BFTConsensus, *ValidatorSet) {
	t.Helper()
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	vs.Register("v1", 100)
	vs.Register("v2", 100)
	vs.Register("v3", 100)

	bc := NewBFTConsensus(vs, DefaultConsensusConfig())
	return bc, vs
}

func TestBFT_ProposeAndCommit(t *testing.T) {
	bc, _ := setupBFT(t)

	// Propose
	cr, err := bc.Propose(1, 0, "v1", "blockhash-1")
	if err != nil {
		t.Fatal(err)
	}
	if cr.Status != StatusProposed {
		t.Fatal("expected proposed status")
	}

	// PreVote from 2/3+ validators
	bc.PreVote(1, "v1", "blockhash-1")
	bc.PreVote(1, "v2", "blockhash-1")

	round, _ := bc.GetRound(1)
	if round.Status != StatusPreVoted {
		t.Fatalf("expected prevoted after 2/3 prevotes, got %s", round.Status)
	}

	// PreCommit from 2/3+ validators
	bc.PreCommit(1, "v1", "blockhash-1")
	bc.PreCommit(1, "v2", "blockhash-1")

	if !bc.IsCommitted(1) {
		t.Fatal("expected block committed after 2/3 precommits")
	}
}

func TestBFT_InsufficientPreVotes(t *testing.T) {
	bc, _ := setupBFT(t)
	bc.Propose(1, 0, "v1", "hash-1")

	// Only 1/3 prevote — not quorum
	bc.PreVote(1, "v1", "hash-1")

	round, _ := bc.GetRound(1)
	if round.Status != StatusProposed {
		t.Fatal("should still be proposed with only 1/3 prevotes")
	}
}

func TestBFT_PreCommitRequiresPrevoteQuorum(t *testing.T) {
	bc, _ := setupBFT(t)
	bc.Propose(1, 0, "v1", "hash-1")

	// Try to precommit without prevote quorum
	err := bc.PreCommit(1, "v1", "hash-1")
	if err == nil {
		t.Fatal("precommit should fail without prevote quorum")
	}
}

func TestBFT_DuplicatePreVote(t *testing.T) {
	bc, _ := setupBFT(t)
	bc.Propose(1, 0, "v1", "hash-1")
	bc.PreVote(1, "v1", "hash-1")

	err := bc.PreVote(1, "v1", "hash-1")
	if err == nil {
		t.Fatal("duplicate prevote should be rejected")
	}
}

func TestBFT_DuplicatePreCommit(t *testing.T) {
	bc, _ := setupBFT(t)
	bc.Propose(1, 0, "v1", "hash-1")
	bc.PreVote(1, "v1", "hash-1")
	bc.PreVote(1, "v2", "hash-1")
	bc.PreCommit(1, "v1", "hash-1")

	err := bc.PreCommit(1, "v1", "hash-1")
	if err == nil {
		t.Fatal("duplicate precommit should be rejected")
	}
}

func TestBFT_NonValidatorCantVote(t *testing.T) {
	bc, _ := setupBFT(t)
	bc.Propose(1, 0, "v1", "hash-1")

	err := bc.PreVote(1, "imposter", "hash-1")
	if err == nil {
		t.Fatal("non-validator should not be able to prevote")
	}
}

func TestBFT_NonValidatorCantPropose(t *testing.T) {
	bc, _ := setupBFT(t)
	_, err := bc.Propose(1, 0, "imposter", "hash-1")
	if err == nil {
		t.Fatal("non-validator should not be able to propose")
	}
}

func TestBFT_DoublePropose(t *testing.T) {
	bc, _ := setupBFT(t)
	bc.Propose(1, 0, "v1", "hash-1")

	// Commit the block
	bc.PreVote(1, "v1", "hash-1")
	bc.PreVote(1, "v2", "hash-1")
	bc.PreCommit(1, "v1", "hash-1")
	bc.PreCommit(1, "v2", "hash-1")

	// Try to re-propose at same height
	_, err := bc.Propose(1, 1, "v2", "hash-2")
	if err == nil {
		t.Fatal("should not allow proposal at already committed height")
	}
}

func TestBFT_FailRound(t *testing.T) {
	bc, _ := setupBFT(t)
	bc.Propose(1, 0, "v1", "hash-1")

	err := bc.FailRound(1)
	if err != nil {
		t.Fatalf("FailRound: %v", err)
	}

	_, ok := bc.GetRound(1)
	if ok {
		t.Fatal("failed round should be removed")
	}
}

func TestBFT_CommittedHeights(t *testing.T) {
	bc, _ := setupBFT(t)

	for _, h := range []uint64{1, 3, 2} {
		bc.Propose(h, 0, "v1", "hash")
		bc.PreVote(h, "v1", "hash")
		bc.PreVote(h, "v2", "hash")
		bc.PreCommit(h, "v1", "hash")
		bc.PreCommit(h, "v2", "hash")
	}

	heights := bc.CommittedHeights()
	if len(heights) != 3 {
		t.Fatalf("expected 3 committed, got %d", len(heights))
	}
	if heights[0] != 1 || heights[1] != 2 || heights[2] != 3 {
		t.Fatal("expected ascending order")
	}
}

func TestBFT_GetCommitted(t *testing.T) {
	bc, _ := setupBFT(t)
	bc.Propose(1, 0, "v1", "hash-1")
	bc.PreVote(1, "v1", "hash-1")
	bc.PreVote(1, "v2", "hash-1")
	bc.PreCommit(1, "v1", "hash-1")
	bc.PreCommit(1, "v2", "hash-1")

	cr, ok := bc.GetCommitted(1)
	if !ok {
		t.Fatal("should find committed round")
	}
	if cr.BlockHash != "hash-1" {
		t.Fatal("committed hash mismatch")
	}
	if cr.EndTime.IsZero() {
		t.Fatal("end time should be set")
	}
}

func TestBFT_StakeWeightedQuorum(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	vs.Register("big", 1000)   // 2/3 of total stake
	vs.Register("small1", 250)
	vs.Register("small2", 250)

	bc := NewBFTConsensus(vs, DefaultConsensusConfig())
	bc.Propose(1, 0, "big", "hash-1")

	// Big validator alone has 1000/1500 = 66.7% — right at 2/3 threshold
	bc.PreVote(1, "big", "hash-1")

	round, _ := bc.GetRound(1)
	if round.Status != StatusPreVoted {
		t.Fatal("big validator alone should reach 2/3 quorum")
	}
}

func TestBFT_NoRound(t *testing.T) {
	bc, _ := setupBFT(t)

	err := bc.PreVote(99, "v1", "hash")
	if err == nil {
		t.Fatal("prevote on non-existent round should fail")
	}

	err = bc.PreCommit(99, "v1", "hash")
	if err == nil {
		t.Fatal("precommit on non-existent round should fail")
	}

	err = bc.FailRound(99)
	if err == nil {
		t.Fatal("fail on non-existent round should fail")
	}
}

func TestBFT_CommittedCount(t *testing.T) {
	bc, _ := setupBFT(t)
	if bc.CommittedCount() != 0 {
		t.Fatal("expected 0 committed initially")
	}

	bc.Propose(1, 0, "v1", "h")
	bc.PreVote(1, "v1", "h")
	bc.PreVote(1, "v2", "h")
	bc.PreCommit(1, "v1", "h")
	bc.PreCommit(1, "v2", "h")

	if bc.CommittedCount() != 1 {
		t.Fatal("expected 1 committed")
	}
}

func TestBFT_TickRoundTimeoutsAndEscalation(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	vs.Register("v1", 100)
	vs.Register("v2", 100)
	vs.Register("v3", 100)
	cfg := DefaultConsensusConfig()
	cfg.RoundTimeout = 20 * time.Millisecond
	bc := NewBFTConsensus(vs, cfg)

	if _, err := bc.Propose(1, 0, "v1", "hash-1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	timed := bc.TickRoundTimeouts(time.Now())
	if len(timed) != 1 || timed[0] != 1 {
		t.Fatalf("expected timeout at height 1, got %v", timed)
	}
	if bc.NextRoundAfterTimeout(1) != 1 {
		t.Fatalf("expected next round 1, got %d", bc.NextRoundAfterTimeout(1))
	}
	if _, ok := bc.GetRound(1); ok {
		t.Fatal("round should be cleared after timeout")
	}
	p, err := bc.ProposerForRound(1)
	if err != nil || p != "v2" {
		t.Fatalf("expected round-1 proposer v2, got %s %v", p, err)
	}
	if _, err := bc.Propose(1, 1, "v2", "hash-2"); err != nil {
		t.Fatalf("re-propose after timeout: %v", err)
	}
}

func TestBFT_ProposerMismatchRejected(t *testing.T) {
	bc, _ := setupBFT(t)
	_, err := bc.Propose(1, 0, "v2", "h")
	if err == nil {
		t.Fatal("expected proposer mismatch for round 0")
	}
}

func TestBFT_RoundCertificate(t *testing.T) {
	bc, _ := setupBFT(t)
	if _, err := bc.Propose(1, 0, "v1", "h"); err != nil {
		t.Fatal(err)
	}
	bc.PreVote(1, "v1", "h")
	bc.PreVote(1, "v2", "h")
	bc.PreCommit(1, "v1", "h")
	bc.PreCommit(1, "v2", "h")
	cert, err := bc.BuildRoundCertificate(1)
	if err != nil {
		t.Fatal(err)
	}
	if cert.Height != 1 || cert.BlockHash != "h" || cert.CommitDigest == "" {
		t.Fatalf("unexpected cert: %+v", cert)
	}
}

func TestBFT_BuildPrevoteLockProof(t *testing.T) {
	bc, _ := setupBFT(t)
	if _, err := bc.Propose(1, 0, "v1", "h"); err != nil {
		t.Fatal(err)
	}
	if err := bc.PreVote(1, "v1", "h"); err != nil {
		t.Fatal(err)
	}
	if err := bc.PreVote(1, "v2", "h"); err != nil {
		t.Fatal(err)
	}
	proof, err := bc.BuildPrevoteLockProof(1)
	if err != nil {
		t.Fatal(err)
	}
	if proof.Height != 1 || proof.LockedBlockHash != "h" || len(proof.Prevotes) != 2 {
		t.Fatalf("unexpected proof: %+v", proof)
	}
}

func TestBFT_PreCommitRejectsMismatchingLock(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	vs.Register("big", 1000)
	vs.Register("small1", 250)
	vs.Register("small2", 250)
	bc := NewBFTConsensus(vs, DefaultConsensusConfig())
	if _, err := bc.Propose(1, 0, "big", "good"); err != nil {
		t.Fatal(err)
	}
	if err := bc.PreVote(1, "big", "evil"); err != nil {
		t.Fatal(err)
	}
	round, _ := bc.GetRound(1)
	if round.LockedBlockHash != "evil" {
		t.Fatalf("expected locked evil, got %q", round.LockedBlockHash)
	}
	if err := bc.PreCommit(1, "small1", "good"); err == nil {
		t.Fatal("precommit for proposal hash should fail when locked on different value")
	}
	if err := bc.PreCommit(1, "small1", "evil"); err != nil {
		t.Fatalf("precommit matching lock: %v", err)
	}
}

func TestBFT_DuplicateProposeSameRoundEquivocation(t *testing.T) {
	bc, _ := setupBFT(t)
	if _, err := bc.Propose(1, 0, "v1", "h"); err != nil {
		t.Fatal(err)
	}
	_, err := bc.Propose(1, 0, "v1", "h2")
	if err == nil {
		t.Fatal("expected equivocation error")
	}
	if !errors.Is(err, ErrBFTEquivocation) {
		t.Fatalf("expected ErrBFTEquivocation, got %v", err)
	}
	var pe *ProposerEquivocationError
	if !errors.As(err, &pe) || pe.ExistingHash != "h" || pe.NewHash != "h2" {
		t.Fatalf("expected ProposerEquivocationError fields, got %#v", err)
	}
}

func TestBFT_DuplicateProposeSameRoundIdempotent(t *testing.T) {
	bc, _ := setupBFT(t)
	cr1, err := bc.Propose(1, 0, "v1", "h")
	if err != nil {
		t.Fatal(err)
	}
	cr2, err := bc.Propose(1, 0, "v1", "h")
	if err != nil {
		t.Fatalf("identical re-propose: %v", err)
	}
	if cr1 != cr2 {
		t.Fatal("expected same round pointer on idempotent propose")
	}
}

func TestBFT_NilPolkaClearsCarryPrevoteLock(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	_ = vs.Register("v2", 100)
	_ = vs.Register("v3", 100)
	bc := NewBFTConsensus(vs, DefaultConsensusConfig())
	if _, err := bc.Propose(5, 0, "v1", "prop"); err != nil {
		t.Fatal(err)
	}
	_ = bc.PreVote(5, "v1", "prop")
	_ = bc.PreVote(5, "v2", "prop")
	if err := bc.FailRound(5); err != nil {
		t.Fatal(err)
	}
	if _, err := bc.Propose(5, 1, "v2", "prop2"); err != nil {
		t.Fatal(err)
	}
	_ = bc.PreVote(5, "v1", NilVoteHash)
	_ = bc.PreVote(5, "v2", NilVoteHash)
	r, _ := bc.GetRound(5)
	if r.LockedBlockHash != NilVoteHash {
		t.Fatalf("expected nil lock, got %q", r.LockedBlockHash)
	}
	bc.mu.RLock()
	_, carried := bc.carryPrevoteLock[5]
	bc.mu.RUnlock()
	if carried {
		t.Fatal("carry map should be cleared after nil polka")
	}
}

func TestBFT_CarryPrevoteLockAfterFailRound(t *testing.T) {
	bc, _ := setupBFT(t)
	if _, err := bc.Propose(2, 0, "v1", "prop-h"); err != nil {
		t.Fatal(err)
	}
	if err := bc.PreVote(2, "v1", "prop-h"); err != nil {
		t.Fatal(err)
	}
	if err := bc.PreVote(2, "v2", "prop-h"); err != nil {
		t.Fatal(err)
	}
	r0, _ := bc.GetRound(2)
	if r0.LockedBlockHash != "prop-h" {
		t.Fatalf("expected lock on proposal, got %q", r0.LockedBlockHash)
	}
	if err := bc.FailRound(2); err != nil {
		t.Fatal(err)
	}
	if _, err := bc.Propose(2, 1, "v2", "prop-2"); err != nil {
		t.Fatal(err)
	}
	r1, _ := bc.GetRound(2)
	if r1.LockedBlockHash != "prop-h" {
		t.Fatalf("expected carried lock prop-h, got %q", r1.LockedBlockHash)
	}
}

func TestBFT_HigherRoundProposeWhileLowerRoundActiveRejected(t *testing.T) {
	bc, _ := setupBFT(t)
	if _, err := bc.Propose(1, 0, "v1", "h"); err != nil {
		t.Fatal(err)
	}
	if _, err := bc.Propose(1, 1, "v2", "h2"); err == nil {
		t.Fatal("expected higher round propose to fail while lower round is active")
	}
}

func TestBFT_NilPrevoteQuorumOutweighsProposalLocksNil(t *testing.T) {
	bc, _ := setupBFT(t)
	if _, err := bc.Propose(1, 0, "v1", "proposal-hash"); err != nil {
		t.Fatal(err)
	}
	if err := bc.PreVote(1, "v1", NilVoteHash); err != nil {
		t.Fatal(err)
	}
	if err := bc.PreVote(1, "v2", NilVoteHash); err != nil {
		t.Fatal(err)
	}
	round, _ := bc.GetRound(1)
	if round.LockedBlockHash != NilVoteHash {
		t.Fatalf("expected nil lock when nil prevotes outweigh proposal stake, got %q", round.LockedBlockHash)
	}
}

func TestBFT_RoundCertificateWithNilPreCommit(t *testing.T) {
	bc, _ := setupBFT(t)
	if _, err := bc.Propose(1, 0, "v1", "h"); err != nil {
		t.Fatal(err)
	}
	bc.PreVote(1, "v1", "h")
	bc.PreVote(1, "v2", "h")
	_ = bc.PreCommitNil(1, "v1")
	_ = bc.PreCommit(1, "v2", "h")
	_ = bc.PreCommit(1, "v3", "h")
	if !bc.IsCommitted(1) {
		t.Fatal("expected commit with nil + two matching hashes")
	}
	cert, err := bc.BuildRoundCertificate(1)
	if err != nil {
		t.Fatal(err)
	}
	if cert.NilCommitCount != 1 || cert.CommitCount != 3 {
		t.Fatalf("expected 1 nil commit in cert, got %+v", cert)
	}
}
