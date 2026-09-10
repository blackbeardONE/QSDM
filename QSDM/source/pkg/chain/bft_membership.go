package chain

import (
	"bytes"
	"errors"
	"fmt"
)

// Membership enforcement is deliberately a separate, opt-in layer. The
// current ValidatorSet still controls the legacy runtime quorum calculation;
// this policy binds authenticated gossip messages to the deterministic
// membership artifacts introduced in consensus_membership.go. It must not be
// enabled on a live network until every validator has the same schedule and
// activation height.

var (
	// ErrBFTMembershipUnknown is returned when no configured membership
	// snapshot applies at an enforced height.
	ErrBFTMembershipUnknown = errors.New("chain: bft membership is unknown at this height")
	// ErrBFTMembershipRoot is returned when a signed wire message names a
	// membership fingerprint other than the deterministic snapshot root.
	ErrBFTMembershipRoot = errors.New("chain: bft membership root mismatch")
	// ErrBFTMembershipUnauthorized is returned when the claimed validator is
	// absent from the active membership snapshot.
	ErrBFTMembershipUnauthorized = errors.New("chain: bft validator is not in the active membership")
	// ErrBFTMembershipKeyMismatch is returned when a listed validator's
	// authenticated public key differs from the key committed by membership.
	ErrBFTMembershipKeyMismatch = errors.New("chain: bft validator public key does not match membership")
)

// BFTMembershipPolicy makes a validated membership schedule available to a
// BFTExecutor. It is immutable after construction and safe to share between
// concurrent executor readers.
type BFTMembershipPolicy struct {
	schedule         *ConsensusMembershipSchedule
	activationHeight uint64
}

// NewBFTMembershipPolicy creates an immutable, height-activated membership
// policy. The schedule is defensively reconstructed, so a caller cannot alter
// an installed policy by retaining its original schedule.
func NewBFTMembershipPolicy(schedule *ConsensusMembershipSchedule, activationHeight uint64) (*BFTMembershipPolicy, error) {
	if schedule == nil {
		return nil, fmt.Errorf("chain: BFT membership policy requires a schedule")
	}
	cloned, err := NewConsensusMembershipSchedule(schedule.Snapshots())
	if err != nil {
		return nil, fmt.Errorf("chain: clone BFT membership schedule: %w", err)
	}
	if _, ok := cloned.ForHeight(activationHeight); !ok {
		return nil, fmt.Errorf("%w: no snapshot is active at configured activation height %d", ErrBFTMembershipUnknown, activationHeight)
	}
	return &BFTMembershipPolicy{
		schedule:         cloned,
		activationHeight: activationHeight,
	}, nil
}

// ActivationHeight reports the first block height at which this policy is
// enforced. Height zero is valid only when the schedule contains a genesis
// snapshot.
func (p *BFTMembershipPolicy) ActivationHeight() uint64 {
	if p == nil {
		return 0
	}
	return p.activationHeight
}

// EnabledAt reports whether a policy applies at height.
func (p *BFTMembershipPolicy) EnabledAt(height uint64) bool {
	return p != nil && height >= p.activationHeight
}

// NetworkID returns the policy's membership network ID.
func (p *BFTMembershipPolicy) NetworkID() string {
	if p == nil || p.schedule == nil {
		return ""
	}
	return p.schedule.NetworkID()
}

// SnapshotHeights returns the policy's configured activation schedule.
func (p *BFTMembershipPolicy) SnapshotHeights() []uint64 {
	if p == nil || p.schedule == nil {
		return nil
	}
	return p.schedule.SnapshotHeights()
}

// MembershipRootForHeight returns the fingerprint an outbound wire message
// must commit to. It returns an empty root before enforcement starts, retaining
// wire compatibility during a coordinated rollout.
func (p *BFTMembershipPolicy) MembershipRootForHeight(height uint64) (string, error) {
	if !p.EnabledAt(height) {
		return "", nil
	}
	membership, ok := p.schedule.ForHeight(height)
	if !ok {
		return "", fmt.Errorf("%w: height %d", ErrBFTMembershipUnknown, height)
	}
	root, err := membership.Fingerprint()
	if err != nil {
		return "", fmt.Errorf("chain: fingerprint active BFT membership: %w", err)
	}
	return root, nil
}

// ValidateSignedMember verifies that a signed BFT message uses the exact
// membership root and public key authorized for its claimed validator. The
// caller must verify the signature itself; this method binds that verified
// identity to the active membership artifact.
func (p *BFTMembershipPolicy) ValidateSignedMember(height uint64, membershipRoot, validator string, auth BFTWireAuth) error {
	if !p.EnabledAt(height) {
		return nil
	}
	if !auth.Signed() {
		return ErrBFTUnsigned
	}
	membership, ok := p.schedule.ForHeight(height)
	if !ok {
		return fmt.Errorf("%w: height %d", ErrBFTMembershipUnknown, height)
	}
	wantRoot, err := membership.Fingerprint()
	if err != nil {
		return fmt.Errorf("chain: fingerprint active BFT membership: %w", err)
	}
	if membershipRoot != wantRoot {
		return fmt.Errorf("%w: height %d got %q want %q", ErrBFTMembershipRoot, height, membershipRoot, wantRoot)
	}
	for _, member := range membership.Members {
		if member.Address != validator {
			continue
		}
		wantKey, err := decodeCanonicalMembershipPublicKey(member.ConsensusPublicKeyHex)
		if err != nil {
			return fmt.Errorf("chain: active BFT membership contains invalid public key: %w", err)
		}
		if !bytes.Equal(auth.PublicKey, wantKey) {
			return fmt.Errorf("%w: validator %s", ErrBFTMembershipKeyMismatch, validator)
		}
		return nil
	}
	return fmt.Errorf("%w: validator %s", ErrBFTMembershipUnauthorized, validator)
}
