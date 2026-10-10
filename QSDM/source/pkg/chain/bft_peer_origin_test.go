package chain

import (
	"errors"
	"testing"
)

func TestBFTPeerOriginPolicyAcceptsScheduledAuthenticatedPublisher(t *testing.T) {
	signer, address := newBFTKey(t)
	membership := membershipForBFTSigners(t, 50, signer)
	schedule, err := NewConsensusMembershipSchedule([]ConsensusMembership{membership})
	if err != nil {
		t.Fatal(err)
	}
	membershipPolicy, err := NewBFTMembershipPolicy(schedule, 50)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBFTPeerOriginPolicy(membershipPolicy)
	if err != nil {
		t.Fatal(err)
	}
	root, err := membership.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	message := BFTWirePrevoteMsg{Height: 50, Round: 2, Validator: address, BlockHash: "state-root", MembershipRoot: root}
	if err := SignPrevote(&message, signer); err != nil {
		t.Fatal(err)
	}
	if err := policy.ValidateSignedOrigin(message.Height, message.MembershipRoot, message.Validator, message.Auth, membership.Members[0].P2PPeerID); err != nil {
		t.Fatalf("scheduled authenticated publisher should verify: %v", err)
	}
	if got := policy.ActivationHeight(); got != 50 {
		t.Fatalf("activation height = %d, want 50", got)
	}
	if got := policy.NetworkID(); got != membership.NetworkID {
		t.Fatalf("network ID = %q, want %q", got, membership.NetworkID)
	}
}

func TestBFTPeerOriginPolicyRejectsMissingWrongAndUnsignedPublishers(t *testing.T) {
	signer, address := newBFTKey(t)
	membership := membershipForBFTSigners(t, 50, signer)
	schedule, err := NewConsensusMembershipSchedule([]ConsensusMembership{membership})
	if err != nil {
		t.Fatal(err)
	}
	membershipPolicy, err := NewBFTMembershipPolicy(schedule, 50)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBFTPeerOriginPolicy(membershipPolicy)
	if err != nil {
		t.Fatal(err)
	}
	root, err := membership.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	message := BFTWirePrevoteMsg{Height: 50, Round: 2, Validator: address, BlockHash: "state-root", MembershipRoot: root}
	if err := SignPrevote(&message, signer); err != nil {
		t.Fatal(err)
	}
	if err := policy.ValidateSignedOrigin(message.Height, message.MembershipRoot, message.Validator, message.Auth, ""); !errors.Is(err, ErrBFTPeerOriginMissing) {
		t.Fatalf("missing publisher error = %v, want ErrBFTPeerOriginMissing", err)
	}
	if err := policy.ValidateSignedOrigin(message.Height, message.MembershipRoot, message.Validator, message.Auth, testPeerID(99)); !errors.Is(err, ErrBFTPeerOriginUnauthorized) {
		t.Fatalf("wrong publisher error = %v, want ErrBFTPeerOriginUnauthorized", err)
	}
	if err := policy.ValidateSignedOrigin(message.Height, message.MembershipRoot, message.Validator, BFTWireAuth{}, membership.Members[0].P2PPeerID); !errors.Is(err, ErrBFTUnsigned) {
		t.Fatalf("unsigned message error = %v, want ErrBFTUnsigned", err)
	}
}
