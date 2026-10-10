package chain

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestBFTExecutorOutOfRoundVotesCannotMutateConsensus(t *testing.T) {
	for _, kind := range []string{BFTWirePrevote, BFTWirePrecommit} {
		for _, scenario := range []struct {
			name    string
			round   uint32
			timeout bool
		}{
			{name: "retired", round: 0},
			{name: "timed-out", round: 0, timeout: true},
			{name: "future", round: 2},
		} {
			for _, value := range []string{"proposal-root", NilVoteHash} {
				t.Run(kind+"/"+scenario.name+"/"+value, func(t *testing.T) {
					const height uint64 = 50
					bc, executor, signers, addresses := newVoteRoundFixture(t)
					proposer, err := bc.ProposerForRound(0)
					if err != nil {
						t.Fatal(err)
					}
					first, err := bc.Propose(height, 0, proposer, "proposal-root")
					if err != nil {
						t.Fatal(err)
					}
					if scenario.timeout {
						if retired := bc.TickRoundTimeouts(first.Deadline.Add(time.Second)); len(retired) != 1 {
							t.Fatalf("timed out heights = %v, want height %d", retired, height)
						}
					} else if err := bc.FailRound(height); err != nil {
						t.Fatal(err)
					}
					proposer, err = bc.ProposerForRound(1)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := bc.Propose(height, 1, proposer, "proposal-root"); err != nil {
						t.Fatal(err)
					}
					if kind == BFTWirePrecommit {
						for i := 0; i < 3; i++ {
							payload := signedVoteRoundPayload(t, BFTWirePrevote, height, 1, addresses[i], "proposal-root", signers[i])
							if err := executor.ApplyInbound(payload); err != nil {
								t.Fatal(err)
							}
						}
					}

					for i := 0; i < 3; i++ {
						payload := signedVoteRoundPayload(t, kind, height, scenario.round, addresses[i], value, signers[i])
						if err := executor.ApplyInbound(payload); err != nil {
							t.Fatalf("out-of-round delivery should be ignored without a transport error: %v", err)
						}
					}
					if bc.IsCommitted(height) {
						t.Fatal("votes signed for another round committed the active round")
					}
					active, ok := bc.GetRound(height)
					if !ok || active.Round != 1 || len(active.Commits) != 0 {
						t.Fatalf("out-of-round votes changed round state: %+v", active)
					}
					if kind == BFTWirePrevote && (len(active.PreVotes) != 0 || active.LockedBlockHash != "" || active.Status != StatusProposed) {
						t.Fatalf("out-of-round prevotes changed vote count or lock: %+v", active)
					}
					if kind == BFTWirePrecommit && (len(active.PreVotes) != 3 || active.LockedBlockHash != "proposal-root" || active.Status != StatusPreVoted) {
						t.Fatalf("out-of-round precommits changed the prevote lock: %+v", active)
					}

					for i := 0; i < 3; i++ {
						payload := signedVoteRoundPayload(t, kind, height, 1, addresses[i], value, signers[i])
						if err := executor.ApplyInbound(payload); err != nil {
							t.Fatalf("active-round vote rejected: %v", err)
						}
					}
					if kind == BFTWirePrecommit && value != NilVoteHash {
						committed, ok := bc.GetCommitted(height)
						if !ok || committed.Round != 1 || len(committed.Commits) != 3 || committed.BlockHash != value {
							t.Fatalf("active-round quorum did not commit: %+v", committed)
						}
						return
					}
					active, ok = bc.GetRound(height)
					if !ok || bc.IsCommitted(height) {
						t.Fatal("expected an active round")
					}
					if kind == BFTWirePrevote && (len(active.PreVotes) != 3 || active.LockedBlockHash != value) {
						t.Fatalf("active-round prevotes were not counted: %+v", active)
					}
					if kind == BFTWirePrecommit && len(active.Commits) != 3 {
						t.Fatalf("active-round nil precommits were not counted: %+v", active)
					}
				})
			}
		}
	}
}

func TestBFTVoteRoundBinding(t *testing.T) {
	bc, _ := setupBFT(t)
	voteMethods := []func(uint64, uint32, string, string) error{bc.PreVoteForRound, bc.PreCommitForRound}
	for _, vote := range voteMethods {
		if err := vote(1, 1, "v1", "root"); err == nil || !isBenignBFTErr(err) {
			t.Fatalf("vote without active round = %v, want benign rejection", err)
		}
	}
	if _, err := bc.Propose(1, 1, "v2", "root"); err != nil {
		t.Fatal(err)
	}
	for _, vote := range voteMethods {
		for _, round := range []uint32{0, 2} {
			if err := vote(1, round, "v1", "root"); !errors.Is(err, ErrBFTVoteRoundMismatch) || !isBenignBFTErr(err) {
				t.Fatalf("round %d vote = %v, want benign round mismatch", round, err)
			}
		}
	}
	for _, vote := range voteMethods {
		for _, validator := range []string{"v1", "v2"} {
			if err := vote(1, 1, validator, "root"); err != nil {
				t.Fatalf("current-round vote: %v", err)
			}
		}
	}
	committed, ok := bc.GetCommitted(1)
	if !ok || committed.Round != 1 || len(committed.Commits) != 2 {
		t.Fatalf("current-round quorum did not commit: %+v", committed)
	}
	for _, round := range []uint32{0, 2} {
		if err := bc.PreCommitForRound(1, round, "v3", "root"); !errors.Is(err, ErrBFTVoteRoundMismatch) {
			t.Fatalf("late round %d precommit = %v, want round mismatch", round, err)
		}
	}
	for _, validator := range []string{"v1", "v3"} {
		if err := bc.PreCommitForRound(1, 1, validator, "root"); err != nil {
			t.Fatalf("matching late precommit: %v", err)
		}
	}
	if len(committed.Commits) != 2 {
		t.Fatal("late precommits mutated the committed vote set")
	}
}

func TestBFTExecutorUnsignedVotesRemainRoundBound(t *testing.T) {
	bc, _ := setupBFT(t)
	executor := NewBFTExecutor(bc)
	if _, err := bc.Propose(1, 1, "v2", "root"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{BFTWirePrevote, BFTWirePrecommit} {
		for _, round := range []uint32{0, 1} {
			for _, validator := range []string{"v1", "v2"} {
				var message any = BFTWirePrevoteMsg{Height: 1, Round: round, Validator: validator, BlockHash: "root"}
				if kind == BFTWirePrecommit {
					message = BFTWirePrecommitMsg{Height: 1, Round: round, Validator: validator, BlockHash: "root"}
				}
				payload, err := MarshalBFTWire(kind, message)
				if err != nil {
					t.Fatal(err)
				}
				if err := executor.ApplyInbound(payload); err != nil {
					t.Fatal(err)
				}
			}
			if round == 0 {
				active, ok := bc.GetRound(1)
				if !ok || len(active.Commits) != 0 || (kind == BFTWirePrevote && len(active.PreVotes) != 0) {
					t.Fatalf("unsigned out-of-round %s changed consensus: %+v", kind, active)
				}
			}
		}
	}
	if !bc.IsCommitted(1) {
		t.Fatal("matching unsigned votes no longer commit in compatibility mode")
	}
}

func TestBFTVoteRoundBindingConcurrentAdvance(t *testing.T) {
	for _, kind := range []string{BFTWirePrevote, BFTWirePrecommit} {
		t.Run(kind, func(t *testing.T) {
			for iteration := 0; iteration < 100; iteration++ {
				bc, _ := setupBFT(t)
				if _, err := bc.Propose(1, 0, "v1", "root"); err != nil {
					t.Fatal(err)
				}
				vote := bc.PreVoteForRound
				if kind == BFTWirePrecommit {
					vote = bc.PreCommitForRound
					for _, validator := range []string{"v1", "v2"} {
						if err := bc.PreVoteForRound(1, 0, validator, "root"); err != nil {
							t.Fatal(err)
						}
					}
				}
				start := make(chan struct{})
				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					<-start
					if err := vote(1, 0, "v1", "root"); !isBenignBFTErr(err) {
						t.Errorf("concurrent vote: %v", err)
					}
				}()
				go func() {
					defer wg.Done()
					<-start
					if err := bc.FailRound(1); err != nil {
						t.Errorf("retire round: %v", err)
						return
					}
					if _, err := bc.Propose(1, 1, "v2", "root"); err != nil {
						t.Errorf("advance round: %v", err)
						return
					}
					if kind == BFTWirePrecommit {
						for _, validator := range []string{"v1", "v2"} {
							if err := bc.PreVoteForRound(1, 1, validator, "root"); err != nil {
								t.Errorf("current-round prevote: %v", err)
							}
						}
					}
				}()
				close(start)
				wg.Wait()
				active, ok := bc.GetRound(1)
				if !ok || active.Round != 1 || len(active.Commits) != 0 || (kind == BFTWirePrevote && len(active.PreVotes) != 0) {
					t.Fatalf("old vote crossed a concurrent round transition: %+v", active)
				}
			}
		})
	}
}

func newVoteRoundFixture(t *testing.T) (*BFTConsensus, *BFTExecutor, []BFTSigner, []string) {
	t.Helper()
	validators := NewValidatorSet(DefaultValidatorSetConfig())
	var signers []BFTSigner
	var addresses []string
	for i := 0; i < 4; i++ {
		signer, address := newBFTKey(t)
		if err := validators.Register(address, 100); err != nil {
			t.Fatal(err)
		}
		signers = append(signers, signer)
		addresses = append(addresses, address)
	}
	bc := NewBFTConsensus(validators, DefaultConsensusConfig())
	executor := NewBFTExecutor(bc)
	executor.SetRequireSignedVotes(true)
	return bc, executor, signers, addresses
}

func signedVoteRoundPayload(t *testing.T, kind string, height uint64, round uint32, address, value string, signer BFTSigner) []byte {
	t.Helper()
	var message any
	switch kind {
	case BFTWirePrevote:
		vote := BFTWirePrevoteMsg{Height: height, Round: round, Validator: address, BlockHash: value}
		if err := SignPrevote(&vote, signer); err != nil {
			t.Fatal(err)
		}
		if err := VerifyPrevote(vote); err != nil {
			t.Fatal(err)
		}
		message = vote
	case BFTWirePrecommit:
		vote := BFTWirePrecommitMsg{Height: height, Round: round, Validator: address, BlockHash: value}
		if err := SignPrecommit(&vote, signer); err != nil {
			t.Fatal(err)
		}
		if err := VerifyPrecommit(vote); err != nil {
			t.Fatal(err)
		}
		message = vote
	default:
		t.Fatalf("unsupported vote kind %s", kind)
	}
	payload, err := MarshalBFTWire(kind, message)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
