package chain

import "testing"

// This staging regression keeps the in-memory relay deterministic while
// exercising a real restart boundary: the replacement executor starts with no
// round state, receives signed gossip from the post-failover round, and must
// commit the same value as its peers.
func TestBFTMembershipStagingRestartedValidatorReplaysSignedFailoverRound(t *testing.T) {
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

	first, err := voting.MemberForRound(0)
	if err != nil {
		t.Fatal(err)
	}
	roundZero := mustBFTMembershipRestartPropose(t, signersByAddress[first.Address], root, bftMembershipStagingHeight, 0, first.Address, "abandoned-root")
	relayBFTMembershipStagingPayload(t, nodes, roundZero)
	for _, node := range nodes {
		if err := node.consensus.FailRound(bftMembershipStagingHeight); err != nil {
			t.Fatalf("fail round zero on %s: %v", node.address, err)
		}
	}

	second, err := voting.MemberForRound(1)
	if err != nil {
		t.Fatal(err)
	}
	var missing bftMembershipStagingNode
	survivors := make([]bftMembershipStagingNode, 0, len(nodes)-1)
	for _, node := range nodes {
		if missing.address == "" && node.address != second.Address {
			missing = node
			continue
		}
		survivors = append(survivors, node)
	}
	if missing.address == "" || len(survivors) != 3 {
		t.Fatalf("could not select a non-proposer validator to restart: missing=%q survivors=%d", missing.address, len(survivors))
	}
	const committedHash = "restart-replayed-root"
	replayed := [][]byte{
		mustBFTMembershipRestartPropose(t, signersByAddress[second.Address], root, bftMembershipStagingHeight, 1, second.Address, committedHash),
	}
	for _, node := range survivors {
		replayed = append(replayed, mustBFTMembershipRestartPrevote(t, signersByAddress[node.address], root, bftMembershipStagingHeight, 1, node.address, committedHash))
	}
	for _, node := range survivors {
		replayed = append(replayed, mustBFTMembershipRestartPrecommit(t, signersByAddress[node.address], root, bftMembershipStagingHeight, 1, node.address, committedHash))
	}

	// One non-proposer node disappears after round-zero failure. The surviving three
	// establish the exact signed round that a replacement must be able to replay.
	for _, payload := range replayed {
		relayBFTMembershipStagingPayload(t, survivors, payload)
	}
	for _, node := range survivors {
		assertBFTMembershipRestartCommit(t, node, second.Address, committedHash)
	}

	restarted := newBFTMembershipRestartNode(t, membership, policy, signersByAddress[missing.address])
	for _, payload := range replayed {
		if err := restarted.executor.ApplyInbound(payload); err != nil {
			t.Fatalf("replay to restarted validator: %v", err)
		}
	}
	assertBFTMembershipRestartCommit(t, restarted, second.Address, committedHash)
	stats := restarted.executor.MembershipStats()
	if !stats.Enabled || stats.Accepted != uint64(len(replayed)) || stats.Rejected != 0 {
		t.Fatalf("restarted membership stats = %+v, want %d accepted and zero rejected", stats, len(replayed))
	}
}

func newBFTMembershipRestartNode(t *testing.T, membership ConsensusMembership, policy *BFTMembershipPolicy, signer BFTSigner) bftMembershipStagingNode {
	t.Helper()
	validators := NewValidatorSet(DefaultValidatorSetConfig())
	for _, member := range membership.Members {
		if err := validators.Register(member.Address, 100); err != nil {
			t.Fatal(err)
		}
	}
	executor := NewBFTExecutor(NewBFTConsensus(validators, DefaultConsensusConfig()))
	executor.SetMembershipPolicy(policy)
	executor.SetRequireSignedVotes(true)
	executor.SetSignedVoteActivationHeight(bftMembershipStagingHeight)
	executor.SetVoteSigner(signer)
	return bftMembershipStagingNode{
		address: BFTValidatorAddress(signer.GetPublicKey()), consensus: executor.Consensus(), executor: executor,
	}
}

func mustBFTMembershipRestartPropose(t *testing.T, signer BFTSigner, root string, height uint64, round uint32, proposer, hash string) []byte {
	t.Helper()
	message := BFTWireProposeMsg{Height: height, Round: round, Proposer: proposer, BlockHash: hash, MembershipRoot: root}
	if err := SignPropose(&message, signer); err != nil {
		t.Fatal(err)
	}
	payload, err := MarshalBFTWire(BFTWirePropose, message)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func mustBFTMembershipRestartPrevote(t *testing.T, signer BFTSigner, root string, height uint64, round uint32, validator, hash string) []byte {
	t.Helper()
	message := BFTWirePrevoteMsg{Height: height, Round: round, Validator: validator, BlockHash: hash, MembershipRoot: root}
	if err := SignPrevote(&message, signer); err != nil {
		t.Fatal(err)
	}
	payload, err := MarshalBFTWire(BFTWirePrevote, message)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func mustBFTMembershipRestartPrecommit(t *testing.T, signer BFTSigner, root string, height uint64, round uint32, validator, hash string) []byte {
	t.Helper()
	message := BFTWirePrecommitMsg{Height: height, Round: round, Validator: validator, BlockHash: hash, MembershipRoot: root}
	if err := SignPrecommit(&message, signer); err != nil {
		t.Fatal(err)
	}
	payload, err := MarshalBFTWire(BFTWirePrecommit, message)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func assertBFTMembershipRestartCommit(t *testing.T, node bftMembershipStagingNode, proposer, hash string) {
	t.Helper()
	committed, ok := node.consensus.GetCommitted(bftMembershipStagingHeight)
	if !ok {
		t.Fatalf("node %s did not commit", node.address)
	}
	if committed.Round != 1 || committed.Proposer != proposer || committed.BlockHash != hash {
		t.Fatalf("node %s committed %+v, want round 1 proposer %s hash %s", node.address, committed, proposer, hash)
	}
}
