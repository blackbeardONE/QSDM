package chain

import "testing"

const bftMembershipStagingHeight uint64 = 500

type bftMembershipStagingNode struct {
	address   string
	consensus *BFTConsensus
	executor  *BFTExecutor
}

func TestBFTMembershipFourNodeStagingClusterCommitsAfterFailover(t *testing.T) {
	signers := make([]BFTSigner, 0, 4)
	for range 4 {
		signer, _ := newBFTKey(t)
		signers = append(signers, signer)
	}
	membership := membershipForBFTSigners(t, bftMembershipStagingHeight, signers...)
	voting, err := NewConsensusVotingSet(membership)
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := NewConsensusMembershipSchedule([]ConsensusMembership{membership})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBFTMembershipPolicy(schedule, bftMembershipStagingHeight)
	if err != nil {
		t.Fatal(err)
	}
	root, err := policy.MembershipRootForHeight(bftMembershipStagingHeight)
	if err != nil {
		t.Fatal(err)
	}
	nodes, signersByAddress := newBFTMembershipStagingNodes(t, membership, policy, signers)

	for round := uint32(0); round < 2; round++ {
		expected, err := voting.MemberForRound(round)
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range nodes {
			proposer, err := node.consensus.ProposerForRound(round)
			if err != nil {
				t.Fatal(err)
			}
			if proposer != expected.Address {
				t.Fatalf("round %d proposer for %s = %s, want %s", round, node.address, proposer, expected.Address)
			}
		}
	}

	first, err := voting.MemberForRound(0)
	if err != nil {
		t.Fatal(err)
	}
	relayBFTMembershipStagingPropose(t, nodes, signersByAddress[first.Address], root, bftMembershipStagingHeight, 0, first.Address, "abandoned-state-root")
	for _, node := range nodes {
		if err := node.consensus.FailRound(bftMembershipStagingHeight); err != nil {
			t.Fatalf("fail round 0 on %s: %v", node.address, err)
		}
		if got := node.consensus.NextRoundAfterTimeout(bftMembershipStagingHeight); got != 1 {
			t.Fatalf("next round on %s = %d, want 1", node.address, got)
		}
	}

	second, err := voting.MemberForRound(1)
	if err != nil {
		t.Fatal(err)
	}
	if second.Address == first.Address {
		t.Fatal("staging rotation did not select a new proposer after failover")
	}
	const committedHash = "reproposed-state-root"
	relayBFTMembershipStagingPropose(t, nodes, signersByAddress[second.Address], root, bftMembershipStagingHeight, 1, second.Address, committedHash)
	for _, member := range voting.Members() {
		relayBFTMembershipStagingPrevote(t, nodes, signersByAddress[member.Address], root, bftMembershipStagingHeight, 1, member.Address, committedHash)
	}
	for _, member := range voting.Members() {
		relayBFTMembershipStagingPrecommit(t, nodes, signersByAddress[member.Address], root, bftMembershipStagingHeight, 1, member.Address, committedHash)
	}

	for _, node := range nodes {
		if !node.consensus.IsCommitted(bftMembershipStagingHeight) {
			t.Fatalf("node %s did not commit after a four-member signed quorum", node.address)
		}
		committed, ok := node.consensus.GetCommitted(bftMembershipStagingHeight)
		if !ok {
			t.Fatalf("node %s reports commit without a committed round", node.address)
		}
		if committed.Round != 1 || committed.Proposer != second.Address || committed.BlockHash != committedHash {
			t.Fatalf("node %s committed %+v, want round 1 proposer %s hash %s", node.address, committed, second.Address, committedHash)
		}
		stats := node.executor.MembershipStats()
		if !stats.Enabled || stats.Accepted != 10 || stats.Rejected != 0 {
			t.Fatalf("node %s membership stats = %+v, want ten accepted and zero rejected messages", node.address, stats)
		}
	}
}

func newBFTMembershipStagingNodes(t *testing.T, membership ConsensusMembership, policy *BFTMembershipPolicy, signers []BFTSigner) ([]bftMembershipStagingNode, map[string]BFTSigner) {
	t.Helper()
	signersByAddress := make(map[string]BFTSigner, len(signers))
	for _, signer := range signers {
		signersByAddress[BFTValidatorAddress(signer.GetPublicKey())] = signer
	}
	nodes := make([]bftMembershipStagingNode, 0, len(signers))
	for _, signer := range signers {
		validators := NewValidatorSet(DefaultValidatorSetConfig())
		for _, member := range membership.Members {
			if err := validators.Register(member.Address, 100); err != nil {
				t.Fatalf("register %s: %v", member.Address, err)
			}
		}
		executor := NewBFTExecutor(NewBFTConsensus(validators, DefaultConsensusConfig()))
		executor.SetMembershipPolicy(policy)
		executor.SetRequireSignedVotes(true)
		executor.SetSignedVoteActivationHeight(bftMembershipStagingHeight)
		executor.SetVoteSigner(signer)
		nodes = append(nodes, bftMembershipStagingNode{
			address:   BFTValidatorAddress(signer.GetPublicKey()),
			consensus: executor.bc,
			executor:  executor,
		})
	}
	return nodes, signersByAddress
}

func relayBFTMembershipStagingPropose(t *testing.T, nodes []bftMembershipStagingNode, signer BFTSigner, root string, height uint64, round uint32, proposer, blockHash string) {
	t.Helper()
	message := BFTWireProposeMsg{Height: height, Round: round, Proposer: proposer, BlockHash: blockHash, MembershipRoot: root}
	if err := SignPropose(&message, signer); err != nil {
		t.Fatal(err)
	}
	payload, err := MarshalBFTWire(BFTWirePropose, message)
	if err != nil {
		t.Fatal(err)
	}
	relayBFTMembershipStagingPayload(t, nodes, payload)
}

func relayBFTMembershipStagingPrevote(t *testing.T, nodes []bftMembershipStagingNode, signer BFTSigner, root string, height uint64, round uint32, validator, blockHash string) {
	t.Helper()
	message := BFTWirePrevoteMsg{Height: height, Round: round, Validator: validator, BlockHash: blockHash, MembershipRoot: root}
	if err := SignPrevote(&message, signer); err != nil {
		t.Fatal(err)
	}
	payload, err := MarshalBFTWire(BFTWirePrevote, message)
	if err != nil {
		t.Fatal(err)
	}
	relayBFTMembershipStagingPayload(t, nodes, payload)
}

func relayBFTMembershipStagingPrecommit(t *testing.T, nodes []bftMembershipStagingNode, signer BFTSigner, root string, height uint64, round uint32, validator, blockHash string) {
	t.Helper()
	message := BFTWirePrecommitMsg{Height: height, Round: round, Validator: validator, BlockHash: blockHash, MembershipRoot: root}
	if err := SignPrecommit(&message, signer); err != nil {
		t.Fatal(err)
	}
	payload, err := MarshalBFTWire(BFTWirePrecommit, message)
	if err != nil {
		t.Fatal(err)
	}
	relayBFTMembershipStagingPayload(t, nodes, payload)
}

func relayBFTMembershipStagingPayload(t *testing.T, nodes []bftMembershipStagingNode, payload []byte) {
	t.Helper()
	for _, node := range nodes {
		if err := node.executor.ApplyInbound(payload); err != nil {
			t.Fatalf("relay to %s: %v", node.address, err)
		}
	}
}
