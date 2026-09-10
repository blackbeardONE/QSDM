package chain

import (
	"errors"
	"reflect"
	"sort"
	"testing"
)

func TestConsensusVotingSetUsesExactIntegerQuorum(t *testing.T) {
	membership := testConsensusMembership(1, 1, 1, 1)
	membership.EffectiveHeight = 10
	set, err := NewConsensusVotingSet(membership)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := set.TotalVotingPower(), uint64(4); got != want {
		t.Fatalf("total voting power = %d, want %d", got, want)
	}
	if got, want := set.RequiredQuorumVotingPower(), uint64(3); got != want {
		t.Fatalf("required quorum = %d, want %d", got, want)
	}

	members := set.Members()
	if len(members) != 4 {
		t.Fatalf("member count = %d, want 4", len(members))
	}
	addresses := []string{members[0].Address, members[1].Address}
	if has, err := set.HasQuorum(addresses); err != nil || has {
		t.Fatalf("two of four quorum = (%t, %v), want (false, nil)", has, err)
	}
	addresses = append(addresses, members[2].Address)
	if has, err := set.HasQuorum(addresses); err != nil || !has {
		t.Fatalf("three of four quorum = (%t, %v), want (true, nil)", has, err)
	}
	if _, err := set.VotingPowerFor([]string{members[0].Address, members[0].Address}); !errors.Is(err, ErrConsensusVotingMemberDuplicate) {
		t.Fatalf("duplicate voter error = %v, want ErrConsensusVotingMemberDuplicate", err)
	}
	if _, err := set.VotingPowerFor([]string{"not-a-member"}); !errors.Is(err, ErrConsensusVotingMemberUnknown) {
		t.Fatalf("unknown voter error = %v, want ErrConsensusVotingMemberUnknown", err)
	}
}

func TestConsensusVotingSetIsDeterministicAndDefensive(t *testing.T) {
	membership := testConsensusMembership(5, 2, 9)
	membership.EffectiveHeight = 20
	permuted := cloneTestMembership(membership)
	permuted.Members[0], permuted.Members[2] = permuted.Members[2], permuted.Members[0]

	left, err := NewConsensusVotingSet(membership)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewConsensusVotingSet(permuted)
	if err != nil {
		t.Fatal(err)
	}
	if left.Fingerprint() != right.Fingerprint() {
		t.Fatalf("equivalent membership fingerprints differ: %s != %s", left.Fingerprint(), right.Fingerprint())
	}

	leftMembers := left.Members()
	addresses := make([]string, len(leftMembers))
	for index, member := range leftMembers {
		addresses[index] = member.Address
	}
	if !sort.StringsAreSorted(addresses) {
		t.Fatalf("members are not canonical address order: %v", addresses)
	}
	for round := uint32(0); round < 12; round++ {
		leftMember, err := left.MemberForRound(round)
		if err != nil {
			t.Fatal(err)
		}
		rightMember, err := right.MemberForRound(round)
		if err != nil {
			t.Fatal(err)
		}
		if leftMember != rightMember {
			t.Fatalf("round %d member differs: %+v != %+v", round, leftMember, rightMember)
		}
	}

	leftMembers[0].Address = "changed"
	if member, ok := left.Member(addresses[0]); !ok || member.Address != addresses[0] {
		t.Fatalf("Members returned aliases to immutable view: %+v, %t", member, ok)
	}
	membershipCopy := left.Membership()
	membershipBeforeMutation := left.Membership()
	membershipCopy.Members[0].Address = "changed-again"
	if !reflect.DeepEqual(left.Membership(), membershipBeforeMutation) {
		t.Fatal("Membership returned aliases to immutable view")
	}
}

func TestConsensusVotingSetResolvesScheduleAtHeight(t *testing.T) {
	genesis := testConsensusMembership(1, 1, 1)
	genesis.EffectiveHeight = 10
	upgrade := testConsensusMembership(2, 3, 5, 7)
	upgrade.EffectiveHeight = 40
	schedule, err := NewConsensusMembershipSchedule([]ConsensusMembership{upgrade, genesis})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewConsensusVotingSetAtHeight(schedule, 9); !errors.Is(err, ErrConsensusVotingSnapshotUnavailable) {
		t.Fatalf("pre-activation voting set error = %v, want ErrConsensusVotingSnapshotUnavailable", err)
	}
	before, err := NewConsensusVotingSetAtHeight(schedule, 39)
	if err != nil {
		t.Fatal(err)
	}
	after, err := NewConsensusVotingSetAtHeight(schedule, 40)
	if err != nil {
		t.Fatal(err)
	}
	if before.Fingerprint() == after.Fingerprint() {
		t.Fatal("height-indexed voting set did not switch at the scheduled membership upgrade")
	}
}
