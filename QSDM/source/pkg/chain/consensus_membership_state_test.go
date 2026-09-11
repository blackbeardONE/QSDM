package chain

import (
	"errors"
	"testing"
)

func TestConsensusMembershipStateActivatesAtExactHeightWithOldSetQuorum(t *testing.T) {
	state, active, next, signers := membershipTransitionFixture(t)
	proposal := mustMembershipTransitionProposal(t, active, next, signers[0])
	if err := state.RecordProposal(proposal, 10); err != nil {
		t.Fatal(err)
	}
	for _, signer := range signers[:3] {
		vote := mustMembershipTransitionVote(t, proposal, signer)
		if err := state.RecordVote(proposal.ID, vote, 10); err != nil {
			t.Fatal(err)
		}
	}

	if err := state.FinalizeAtHeight(19); err != nil {
		t.Fatalf("early finalization: %v", err)
	}
	before, _ := state.ActiveMembership().Fingerprint()
	activeRoot, _ := active.Fingerprint()
	if before != activeRoot {
		t.Fatalf("active membership changed before activation: %s != %s", before, activeRoot)
	}

	if err := state.FinalizeAtHeight(20); err != nil {
		t.Fatal(err)
	}
	got, _ := state.ActiveMembership().Fingerprint()
	want, _ := next.Fingerprint()
	if got != want {
		t.Fatalf("active membership root = %s, want %s", got, want)
	}
	if pending := state.Pending(); len(pending) != 0 {
		t.Fatalf("pending transitions = %d, want 0", len(pending))
	}
	finalized := state.Finalized()
	if len(finalized) != 1 || finalized[0].Outcome != ConsensusMembershipTransitionActivated {
		t.Fatalf("finalization = %+v, want one activated transition", finalized)
	}
	if finalized[0].ApprovedVotingPower != 3 || finalized[0].RequiredVotingPower != 3 {
		t.Fatalf("finalization power = %d/%d, want 3/3", finalized[0].ApprovedVotingPower, finalized[0].RequiredVotingPower)
	}
}

func TestConsensusMembershipStateRejectsUnauthorizedDuplicateAndStaleVotes(t *testing.T) {
	state, active, next, signers := membershipTransitionFixture(t)
	proposal := mustMembershipTransitionProposal(t, active, next, signers[0])
	if err := state.RecordProposal(proposal, 10); err != nil {
		t.Fatal(err)
	}

	outsider, _ := newBFTKey(t)
	outsiderVote := mustMembershipTransitionVote(t, proposal, outsider)
	if err := state.RecordVote(proposal.ID, outsiderVote, 10); !errors.Is(err, ErrConsensusMembershipTransitionUnauthorized) {
		t.Fatalf("outsider vote error = %v, want unauthorized", err)
	}

	forged := mustMembershipTransitionVote(t, proposal, signers[0])
	forged.Signer = BFTValidatorAddress(signers[1].GetPublicKey())
	if err := state.RecordVote(proposal.ID, forged, 10); !errors.Is(err, ErrConsensusMembershipTransitionUnauthorized) {
		t.Fatalf("forged vote error = %v, want unauthorized", err)
	}

	valid := mustMembershipTransitionVote(t, proposal, signers[0])
	if err := state.RecordVote(proposal.ID, valid, 10); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordVote(proposal.ID, valid, 10); !errors.Is(err, ErrConsensusMembershipTransitionDuplicate) {
		t.Fatalf("duplicate vote error = %v, want duplicate", err)
	}

	stale := mustMembershipTransitionVote(t, proposal, signers[1])
	if err := state.RecordVote(proposal.ID, stale, proposal.ActivationHeight); !errors.Is(err, ErrConsensusMembershipTransitionStale) {
		t.Fatalf("stale vote error = %v, want stale", err)
	}
}

func TestConsensusMembershipStateRejectsOutsiderProposal(t *testing.T) {
	state, active, next, _ := membershipTransitionFixture(t)
	outsider, _ := newBFTKey(t)
	proposal := mustMembershipTransitionProposal(t, active, next, outsider)
	if err := state.RecordProposal(proposal, 10); !errors.Is(err, ErrConsensusMembershipTransitionUnauthorized) {
		t.Fatalf("outsider proposal error = %v, want unauthorized", err)
	}
}

func TestConsensusMembershipStateRootIsIndependentOfVoteArrivalOrder(t *testing.T) {
	_, active, next, signers := membershipTransitionFixture(t)
	proposal := mustMembershipTransitionProposal(t, active, next, signers[0])
	votes := []ConsensusMembershipTransitionVote{
		mustMembershipTransitionVote(t, proposal, signers[0]),
		mustMembershipTransitionVote(t, proposal, signers[1]),
		mustMembershipTransitionVote(t, proposal, signers[2]),
	}

	left, err := NewConsensusMembershipState(active)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewConsensusMembershipState(active)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []*ConsensusMembershipState{left, right} {
		if err := state.RecordProposal(proposal, 10); err != nil {
			t.Fatal(err)
		}
	}
	for _, vote := range votes {
		if err := left.RecordVote(proposal.ID, vote, 10); err != nil {
			t.Fatal(err)
		}
	}
	for index := len(votes) - 1; index >= 0; index-- {
		if err := right.RecordVote(proposal.ID, votes[index], 10); err != nil {
			t.Fatal(err)
		}
	}
	leftRoot, err := left.StateRoot()
	if err != nil {
		t.Fatal(err)
	}
	rightRoot, err := right.StateRoot()
	if err != nil {
		t.Fatal(err)
	}
	if leftRoot != rightRoot {
		t.Fatalf("vote arrival order changed state root: %s != %s", leftRoot, rightRoot)
	}
}

func TestConsensusMembershipStateCloneAndRestoreAreIsolated(t *testing.T) {
	state, active, next, signers := membershipTransitionFixture(t)
	proposal := mustMembershipTransitionProposal(t, active, next, signers[0])
	if err := state.RecordProposal(proposal, 10); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordVote(proposal.ID, mustMembershipTransitionVote(t, proposal, signers[0]), 10); err != nil {
		t.Fatal(err)
	}

	originalRoot, err := state.StateRoot()
	if err != nil {
		t.Fatal(err)
	}
	clone := state.Clone()
	if err := clone.RecordVote(proposal.ID, mustMembershipTransitionVote(t, proposal, signers[1]), 10); err != nil {
		t.Fatal(err)
	}
	cloneRoot, err := clone.StateRoot()
	if err != nil {
		t.Fatal(err)
	}
	if cloneRoot == originalRoot {
		t.Fatal("mutating a clone did not change its root")
	}
	afterOriginal, _ := state.StateRoot()
	if afterOriginal != originalRoot {
		t.Fatal("mutating a clone changed the original state")
	}

	restored, err := NewConsensusMembershipState(active)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.RestoreFrom(clone); err != nil {
		t.Fatal(err)
	}
	restoredRoot, _ := restored.StateRoot()
	if restoredRoot != cloneRoot {
		t.Fatalf("restored root = %s, want %s", restoredRoot, cloneRoot)
	}
	if err := clone.RecordVote(proposal.ID, mustMembershipTransitionVote(t, proposal, signers[2]), 10); err != nil {
		t.Fatal(err)
	}
	afterRestore, _ := restored.StateRoot()
	if afterRestore != restoredRoot {
		t.Fatal("mutating source snapshot changed restored state")
	}
}

func TestConsensusMembershipStateRefusesLateActivation(t *testing.T) {
	state, active, next, signers := membershipTransitionFixture(t)
	proposal := mustMembershipTransitionProposal(t, active, next, signers[0])
	if err := state.RecordProposal(proposal, 10); err != nil {
		t.Fatal(err)
	}
	before, _ := state.ActiveMembership().Fingerprint()
	if err := state.FinalizeAtHeight(proposal.ActivationHeight + 1); !errors.Is(err, ErrConsensusMembershipTransitionAdvanceSkipped) {
		t.Fatalf("late activation error = %v, want skipped activation", err)
	}
	after, _ := state.ActiveMembership().Fingerprint()
	if after != before {
		t.Fatal("late finalization changed active membership")
	}
	if pending := state.Pending(); len(pending) != 1 {
		t.Fatalf("late finalization removed pending proposal: %d", len(pending))
	}
}

func membershipTransitionFixture(t *testing.T) (*ConsensusMembershipState, ConsensusMembership, ConsensusMembership, []BFTSigner) {
	t.Helper()
	first, _ := newBFTKey(t)
	second, _ := newBFTKey(t)
	third, _ := newBFTKey(t)
	fourth, _ := newBFTKey(t)
	fifth, _ := newBFTKey(t)
	sixth, _ := newBFTKey(t)
	signers := []BFTSigner{first, second, third, fourth}
	active := membershipForBFTSigners(t, 10, signers...)
	next := membershipForBFTSigners(t, 20, fifth, sixth)
	state, err := NewConsensusMembershipState(active)
	if err != nil {
		t.Fatal(err)
	}
	return state, active, next, signers
}

func mustMembershipTransitionProposal(t *testing.T, active, next ConsensusMembership, signer BFTSigner) ConsensusMembershipTransitionProposal {
	t.Helper()
	proposal, err := NewConsensusMembershipTransitionProposal(active, next, signer)
	if err != nil {
		t.Fatal(err)
	}
	return proposal
}

func mustMembershipTransitionVote(t *testing.T, proposal ConsensusMembershipTransitionProposal, signer BFTSigner) ConsensusMembershipTransitionVote {
	t.Helper()
	vote, err := NewConsensusMembershipTransitionVote(proposal, signer)
	if err != nil {
		t.Fatal(err)
	}
	return vote
}
