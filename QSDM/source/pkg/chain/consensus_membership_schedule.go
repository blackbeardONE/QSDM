package chain

import (
	"fmt"
	"sort"
)

// ConsensusMembershipSchedule resolves the membership snapshot that applies at
// a chain height. It is immutable after construction so replay, recovery, and
// concurrent readers cannot observe a partially changed validator set.
//
// The schedule is deliberately separate from ValidatorSet. ValidatorSet is
// still the current runtime-only implementation; this schedule is the
// deterministic per-height input a later chain-committed consensus transition
// must use before it can safely replace that behavior.
type ConsensusMembershipSchedule struct {
	networkID string
	snapshots []ConsensusMembership
}

// NewConsensusMembershipSchedule validates and snapshots a proposed sequence
// of membership artifacts. Input ordering does not matter; snapshots are
// internally ordered by EffectiveHeight. Two snapshots cannot activate at the
// same height, and every snapshot must name the same network.
func NewConsensusMembershipSchedule(snapshots []ConsensusMembership) (*ConsensusMembershipSchedule, error) {
	if len(snapshots) == 0 {
		return nil, fmt.Errorf("chain: consensus membership schedule has no snapshots")
	}

	cloned := make([]ConsensusMembership, len(snapshots))
	for index, snapshot := range snapshots {
		if err := snapshot.Validate(); err != nil {
			return nil, fmt.Errorf("chain: consensus membership snapshot %d: %w", index, err)
		}
		cloned[index] = cloneConsensusMembership(snapshot)
	}
	sort.Slice(cloned, func(i, j int) bool {
		return cloned[i].EffectiveHeight < cloned[j].EffectiveHeight
	})

	networkID := cloned[0].NetworkID
	for index, snapshot := range cloned {
		if snapshot.NetworkID != networkID {
			return nil, fmt.Errorf("chain: consensus membership snapshot %d has network ID %q, want %q", index, snapshot.NetworkID, networkID)
		}
		if index > 0 && snapshot.EffectiveHeight == cloned[index-1].EffectiveHeight {
			return nil, fmt.Errorf("chain: consensus membership snapshots %d and %d both activate at height %d", index-1, index, snapshot.EffectiveHeight)
		}
	}

	return &ConsensusMembershipSchedule{
		networkID: networkID,
		snapshots: cloned,
	}, nil
}

// NetworkID returns the shared network ID of all snapshots, or an empty string
// for a nil schedule.
func (s *ConsensusMembershipSchedule) NetworkID() string {
	if s == nil {
		return ""
	}
	return s.networkID
}

// SnapshotHeights returns a defensive copy of scheduled activation heights in
// ascending order.
func (s *ConsensusMembershipSchedule) SnapshotHeights() []uint64 {
	if s == nil {
		return nil
	}
	heights := make([]uint64, len(s.snapshots))
	for index, snapshot := range s.snapshots {
		heights[index] = snapshot.EffectiveHeight
	}
	return heights
}

// Snapshots returns defensive copies of all scheduled memberships in
// activation order. It is intended for status and audit surfaces, not for
// mutation.
func (s *ConsensusMembershipSchedule) Snapshots() []ConsensusMembership {
	if s == nil {
		return nil
	}
	cloned := make([]ConsensusMembership, len(s.snapshots))
	for index, snapshot := range s.snapshots {
		cloned[index] = cloneConsensusMembership(snapshot)
	}
	return cloned
}

// ForHeight returns the most recent snapshot whose EffectiveHeight is no
// greater than height. It returns false when no scheduled membership is yet
// active at that height. The returned membership is a defensive copy.
func (s *ConsensusMembershipSchedule) ForHeight(height uint64) (ConsensusMembership, bool) {
	if s == nil || len(s.snapshots) == 0 {
		return ConsensusMembership{}, false
	}
	firstFuture := sort.Search(len(s.snapshots), func(index int) bool {
		return s.snapshots[index].EffectiveHeight > height
	})
	if firstFuture == 0 {
		return ConsensusMembership{}, false
	}
	return cloneConsensusMembership(s.snapshots[firstFuture-1]), true
}

func cloneConsensusMembership(value ConsensusMembership) ConsensusMembership {
	copy := value
	copy.Members = append([]ConsensusMember(nil), value.Members...)
	return copy
}
