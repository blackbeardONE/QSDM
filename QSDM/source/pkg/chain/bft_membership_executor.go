package chain

import "fmt"

// BFTMembershipStats exposes the configured membership gate and its inbound
// decisions. It is an operations aid only; the policy is not installed by the
// current node runtime yet.
type BFTMembershipStats struct {
	Enabled          bool
	ActivationHeight uint64
	NetworkID        string
	SnapshotHeights  []uint64
	Accepted         uint64
	Rejected         uint64
}

// SetMembershipPolicy installs an immutable, height-activated membership
// policy. Passing nil disables the gate, which is the default until every
// validator has a matching schedule and a coordinated activation height.
func (e *BFTExecutor) SetMembershipPolicy(policy *BFTMembershipPolicy) {
	if e == nil {
		return
	}
	e.membershipPolicy.Store(policy)
}

// MembershipPolicy returns the installed immutable membership policy, if any.
func (e *BFTExecutor) MembershipPolicy() *BFTMembershipPolicy {
	if e == nil {
		return nil
	}
	return e.membershipPolicy.Load()
}

// MembershipStats returns a snapshot of membership-gate configuration and
// decisions. SnapshotHeights is always a defensive copy.
func (e *BFTExecutor) MembershipStats() BFTMembershipStats {
	if e == nil {
		return BFTMembershipStats{}
	}
	policy := e.MembershipPolicy()
	stats := BFTMembershipStats{
		Accepted: e.membershipAccepted.Load(),
		Rejected: e.membershipRejected.Load(),
	}
	if policy == nil {
		return stats
	}
	stats.Enabled = true
	stats.ActivationHeight = policy.ActivationHeight()
	stats.NetworkID = policy.NetworkID()
	stats.SnapshotHeights = policy.SnapshotHeights()
	return stats
}

func (e *BFTExecutor) membershipRootForOutbound(height uint64) (string, error) {
	policy := e.MembershipPolicy()
	if policy == nil {
		return "", nil
	}
	return policy.MembershipRootForHeight(height)
}

func (e *BFTExecutor) validateOutboundMembership(height uint64, validator, membershipRoot string, auth BFTWireAuth) error {
	policy := e.MembershipPolicy()
	if policy == nil || !policy.EnabledAt(height) {
		return nil
	}
	if err := policy.ValidateSignedMember(height, membershipRoot, validator, auth); err != nil {
		return fmt.Errorf("chain: outbound BFT membership validation: %w", err)
	}
	return nil
}

func (e *BFTExecutor) checkInboundMembership(height uint64, validator, membershipRoot string, auth BFTWireAuth) error {
	policy := e.MembershipPolicy()
	if policy == nil || !policy.EnabledAt(height) {
		return nil
	}
	if err := policy.ValidateSignedMember(height, membershipRoot, validator, auth); err != nil {
		e.membershipRejected.Add(1)
		return err
	}
	e.membershipAccepted.Add(1)
	return nil
}
