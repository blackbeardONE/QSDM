package chain

import (
	"errors"
	"fmt"
)

var (
	// ErrConsensusVotingSnapshotUnavailable is returned when a schedule has no
	// membership active at the requested height.
	ErrConsensusVotingSnapshotUnavailable = errors.New("chain: no consensus voting snapshot is active at this height")
	// ErrConsensusVotingMemberUnknown is returned when a vote names a member
	// that is not present in the immutable voting set.
	ErrConsensusVotingMemberUnknown = errors.New("chain: voter is not in the consensus voting set")
	// ErrConsensusVotingMemberDuplicate is returned when one voter appears more
	// than once in an attempted quorum calculation.
	ErrConsensusVotingMemberDuplicate = errors.New("chain: duplicate voter in consensus quorum calculation")
)

// ConsensusVotingSet is an immutable, integer-only view of one validated
// membership snapshot. It is intended for multi-validator staging and later
// runtime integration; the current BFTConsensus still uses ValidatorSet until
// a chain-committed transition is implemented.
type ConsensusVotingSet struct {
	membership  ConsensusMembership
	fingerprint string
	members     []ConsensusMember
	byAddress   map[string]ConsensusMember
	totalPower  uint64
	quorumPower uint64
}

// NewConsensusVotingSet constructs a deterministic voting view from one
// validated membership snapshot. The caller's snapshot is cloned, so later
// mutation cannot change vote accounting or round order.
func NewConsensusVotingSet(membership ConsensusMembership) (*ConsensusVotingSet, error) {
	if err := membership.Validate(); err != nil {
		return nil, fmt.Errorf("chain: validate consensus voting membership: %w", err)
	}
	fingerprint, err := membership.Fingerprint()
	if err != nil {
		return nil, fmt.Errorf("chain: fingerprint consensus voting membership: %w", err)
	}
	totalPower, err := membership.TotalVotingPower()
	if err != nil {
		return nil, fmt.Errorf("chain: total consensus voting power: %w", err)
	}
	quorumPower, err := membership.RequiredQuorumVotingPower()
	if err != nil {
		return nil, fmt.Errorf("chain: required consensus voting quorum: %w", err)
	}

	cloned := cloneConsensusMembership(membership)
	members := cloned.CanonicalMembers()
	byAddress := make(map[string]ConsensusMember, len(members))
	for _, member := range members {
		byAddress[member.Address] = member
	}
	return &ConsensusVotingSet{
		membership:  cloned,
		fingerprint: fingerprint,
		members:     members,
		byAddress:   byAddress,
		totalPower:  totalPower,
		quorumPower: quorumPower,
	}, nil
}

// NewConsensusVotingSetAtHeight resolves the active membership from a
// validated schedule and returns its immutable voting view.
func NewConsensusVotingSetAtHeight(schedule *ConsensusMembershipSchedule, height uint64) (*ConsensusVotingSet, error) {
	if schedule == nil {
		return nil, ErrConsensusVotingSnapshotUnavailable
	}
	membership, ok := schedule.ForHeight(height)
	if !ok {
		return nil, fmt.Errorf("%w: height %d", ErrConsensusVotingSnapshotUnavailable, height)
	}
	return NewConsensusVotingSet(membership)
}

// Membership returns a defensive copy of the validated source snapshot.
func (s *ConsensusVotingSet) Membership() ConsensusMembership {
	if s == nil {
		return ConsensusMembership{}
	}
	return cloneConsensusMembership(s.membership)
}

// Fingerprint returns the deterministic membership root used by this voting
// view. It is empty for a nil set.
func (s *ConsensusVotingSet) Fingerprint() string {
	if s == nil {
		return ""
	}
	return s.fingerprint
}

// Members returns canonical address-sorted member copies.
func (s *ConsensusVotingSet) Members() []ConsensusMember {
	if s == nil {
		return nil
	}
	return append([]ConsensusMember(nil), s.members...)
}

// Member returns the member for an exact canonical address.
func (s *ConsensusVotingSet) Member(address string) (ConsensusMember, bool) {
	if s == nil {
		return ConsensusMember{}, false
	}
	member, ok := s.byAddress[address]
	return member, ok
}

// TotalVotingPower returns the validated integer total.
func (s *ConsensusVotingSet) TotalVotingPower() uint64 {
	if s == nil {
		return 0
	}
	return s.totalPower
}

// RequiredQuorumVotingPower returns the smallest integer strictly greater
// than two thirds of the total voting power.
func (s *ConsensusVotingSet) RequiredQuorumVotingPower() uint64 {
	if s == nil {
		return 0
	}
	return s.quorumPower
}

// VotingPowerFor sums distinct canonical member addresses. Unknown and
// duplicate voters are errors rather than zero-weight inputs, so callers
// cannot quietly hide a malformed vote set.
func (s *ConsensusVotingSet) VotingPowerFor(voters []string) (uint64, error) {
	if s == nil {
		return 0, ErrConsensusVotingSnapshotUnavailable
	}
	seen := make(map[string]struct{}, len(voters))
	var power uint64
	for _, address := range voters {
		if _, duplicate := seen[address]; duplicate {
			return 0, fmt.Errorf("%w: %s", ErrConsensusVotingMemberDuplicate, address)
		}
		member, ok := s.byAddress[address]
		if !ok {
			return 0, fmt.Errorf("%w: %s", ErrConsensusVotingMemberUnknown, address)
		}
		seen[address] = struct{}{}
		power += member.VotingPower
	}
	return power, nil
}

// HasQuorum returns whether the distinct supplied members meet the exact
// integer voting threshold.
func (s *ConsensusVotingSet) HasQuorum(voters []string) (bool, error) {
	power, err := s.VotingPowerFor(voters)
	if err != nil {
		return false, err
	}
	return power >= s.RequiredQuorumVotingPower(), nil
}

// MemberForRound returns the deterministic address-order rotation used by the
// upcoming multi-node staging harness. It deliberately does not claim to be a
// final weighted production proposer algorithm; that policy must be committed
// and activated with the membership transition itself.
func (s *ConsensusVotingSet) MemberForRound(round uint32) (ConsensusMember, error) {
	if s == nil || len(s.members) == 0 {
		return ConsensusMember{}, ErrConsensusVotingSnapshotUnavailable
	}
	index := uint64(round) % uint64(len(s.members))
	return s.members[int(index)], nil
}
