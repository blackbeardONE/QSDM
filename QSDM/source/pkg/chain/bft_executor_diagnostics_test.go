package chain

import "testing"

func TestBFTExecutorCountsCommitCompletedByInboundPeerVote(t *testing.T) {
	_, address := newBFTKey(t)
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register(address, DefaultValidatorSetConfig().MinStake); err != nil {
		t.Fatal(err)
	}
	exec := NewBFTExecutor(NewBFTConsensus(vs, DefaultConsensusConfig()))

	propose, err := MarshalBFTWire(BFTWirePropose, BFTWireProposeMsg{
		Height: 1, Round: 0, Proposer: address, BlockHash: "state-root",
	})
	if err != nil {
		t.Fatal(err)
	}
	prevote, err := MarshalBFTWire(BFTWirePrevote, BFTWirePrevoteMsg{
		Height: 1, Round: 0, Validator: address, BlockHash: "state-root",
	})
	if err != nil {
		t.Fatal(err)
	}
	precommit, err := MarshalBFTWire(BFTWirePrecommit, BFTWirePrecommitMsg{
		Height: 1, Round: 0, Validator: address, BlockHash: "state-root",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, payload := range [][]byte{propose, prevote, precommit} {
		if err := exec.ApplyInbound(payload); err != nil {
			t.Fatal(err)
		}
	}
	if got := exec.PeerVoteCommitCount(); got != 1 {
		t.Fatalf("PeerVoteCommitCount() = %d, want 1", got)
	}
	exec.NotifyFromConsensus(1)
	if got := exec.PeerVoteCommitCount(); got != 1 {
		t.Fatalf("duplicate notification changed peer commit count to %d", got)
	}
}
