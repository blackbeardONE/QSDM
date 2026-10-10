package chain

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	// ErrBFTPeerOriginMissing means an active peer-origin policy was not given
	// the authenticated libp2p publisher identity for an inbound BFT message.
	ErrBFTPeerOriginMissing = errors.New("chain: bft authenticated publisher identity is required")
	// ErrBFTPeerOriginUnauthorized means the authenticated libp2p publisher is
	// not the peer ID committed for the validator that signed the BFT message.
	ErrBFTPeerOriginUnauthorized = errors.New("chain: bft authenticated publisher does not match membership")
)

// BFTPeerOriginPolicy binds an authenticated libp2p publisher to the same
// validator identity authorized by a BFT membership policy. It is immutable
// after construction.
//
// The publisher must be supplied from a libp2p StrictSign-verified
// pubsub.Message.GetFrom value. ReceivedFrom is only the immediate gossip
// relay and must never be used as this policy's validator identity input.
type BFTPeerOriginPolicy struct {
	membershipPolicy *BFTMembershipPolicy
}

// NewBFTPeerOriginPolicy derives an immutable publisher-origin policy from a
// validated BFT membership policy. The membership policy is cloned so callers
// cannot alter the schedule by retaining their original input.
func NewBFTPeerOriginPolicy(membershipPolicy *BFTMembershipPolicy) (*BFTPeerOriginPolicy, error) {
	if membershipPolicy == nil || membershipPolicy.schedule == nil {
		return nil, fmt.Errorf("chain: BFT peer origin policy requires a membership policy")
	}
	cloned, err := NewBFTMembershipPolicy(membershipPolicy.schedule, membershipPolicy.activationHeight)
	if err != nil {
		return nil, fmt.Errorf("chain: clone BFT peer origin membership policy: %w", err)
	}
	return &BFTPeerOriginPolicy{membershipPolicy: cloned}, nil
}

// EnabledAt reports whether peer-origin enforcement applies at height.
func (p *BFTPeerOriginPolicy) EnabledAt(height uint64) bool {
	return p != nil && p.membershipPolicy != nil && p.membershipPolicy.EnabledAt(height)
}

// ActivationHeight reports the first enforced height, or zero for a nil
// policy.
func (p *BFTPeerOriginPolicy) ActivationHeight() uint64 {
	if p == nil || p.membershipPolicy == nil {
		return 0
	}
	return p.membershipPolicy.ActivationHeight()
}

// NetworkID returns the configured membership network ID, or an empty string
// for a nil policy.
func (p *BFTPeerOriginPolicy) NetworkID() string {
	if p == nil || p.membershipPolicy == nil {
		return ""
	}
	return p.membershipPolicy.NetworkID()
}

// SnapshotHeights returns a defensive copy of the policy activation schedule.
func (p *BFTPeerOriginPolicy) SnapshotHeights() []uint64 {
	if p == nil || p.membershipPolicy == nil {
		return nil
	}
	return p.membershipPolicy.SnapshotHeights()
}

// ValidateSignedOrigin verifies a signed BFT validator and checks that the
// authenticated libp2p publisher is the exact peer ID committed for it in the
// active membership snapshot.
func (p *BFTPeerOriginPolicy) ValidateSignedOrigin(height uint64, membershipRoot, validator string, auth BFTWireAuth, publisherPeerID string) error {
	if !p.EnabledAt(height) {
		return nil
	}
	if publisherPeerID == "" {
		return ErrBFTPeerOriginMissing
	}
	if err := p.membershipPolicy.ValidateSignedMember(height, membershipRoot, validator, auth); err != nil {
		return err
	}
	membership, ok := p.membershipPolicy.schedule.ForHeight(height)
	if !ok {
		return fmt.Errorf("%w: height %d", ErrBFTMembershipUnknown, height)
	}
	for _, member := range membership.Members {
		if member.Address != validator {
			continue
		}
		if member.P2PPeerID != publisherPeerID {
			return fmt.Errorf("%w: validator %s", ErrBFTPeerOriginUnauthorized, validator)
		}
		return nil
	}
	return fmt.Errorf("%w: validator %s", ErrBFTMembershipUnauthorized, validator)
}

// ValidateBFTWirePeerOrigin verifies the signed BFT payload and, when the
// policy is active at the message height, checks its authenticated libp2p
// publisher against the active membership snapshot. publisherPeerID must come
// from a libp2p StrictSign-verified pubsub.Message.GetFrom value.
func ValidateBFTWirePeerOrigin(policy *BFTPeerOriginPolicy, publisherPeerID string, payload []byte) error {
	if policy == nil {
		return nil
	}
	kind, raw, err := UnmarshalBFTWire(payload)
	if err != nil {
		return err
	}
	switch kind {
	case BFTWirePropose:
		var message BFTWireProposeMsg
		if err := json.Unmarshal(raw, &message); err != nil {
			return err
		}
		if !policy.EnabledAt(message.Height) {
			return nil
		}
		if err := validateInboundProposeBlock(&message); err != nil {
			return err
		}
		if err := VerifyPropose(message); err != nil {
			return err
		}
		return policy.ValidateSignedOrigin(message.Height, message.MembershipRoot, message.Proposer, message.Auth, publisherPeerID)
	case BFTWirePrevote:
		var message BFTWirePrevoteMsg
		if err := json.Unmarshal(raw, &message); err != nil {
			return err
		}
		if !policy.EnabledAt(message.Height) {
			return nil
		}
		if err := VerifyPrevote(message); err != nil {
			return err
		}
		return policy.ValidateSignedOrigin(message.Height, message.MembershipRoot, message.Validator, message.Auth, publisherPeerID)
	case BFTWirePrecommit:
		var message BFTWirePrecommitMsg
		if err := json.Unmarshal(raw, &message); err != nil {
			return err
		}
		if !policy.EnabledAt(message.Height) {
			return nil
		}
		if err := VerifyPrecommit(message); err != nil {
			return err
		}
		return policy.ValidateSignedOrigin(message.Height, message.MembershipRoot, message.Validator, message.Auth, publisherPeerID)
	default:
		return fmt.Errorf("bft wire: unknown kind %q", kind)
	}
}
