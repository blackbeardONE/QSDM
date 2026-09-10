package chain

import (
	"errors"
	"testing"
)

func TestValidateBFTWirePeerOriginBindsWireToScheduledPublisher(t *testing.T) {
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
	payload, err := MarshalBFTWire(BFTWirePrevote, message)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateBFTWirePeerOrigin(policy, membership.Members[0].P2PPeerID, payload); err != nil {
		t.Fatalf("scheduled publisher should verify: %v", err)
	}
	if err := ValidateBFTWirePeerOrigin(policy, testPeerID(99), payload); !errors.Is(err, ErrBFTPeerOriginUnauthorized) {
		t.Fatalf("wrong publisher error = %v, want ErrBFTPeerOriginUnauthorized", err)
	}
	if err := ValidateBFTWirePeerOrigin(policy, "", payload); !errors.Is(err, ErrBFTPeerOriginMissing) {
		t.Fatalf("missing publisher error = %v, want ErrBFTPeerOriginMissing", err)
	}

	message.Auth.Signature[0] ^= 0xff
	badPayload, err := MarshalBFTWire(BFTWirePrevote, message)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateBFTWirePeerOrigin(policy, membership.Members[0].P2PPeerID, badPayload); !errors.Is(err, ErrBFTBadSignature) {
		t.Fatalf("bad vote signature error = %v, want ErrBFTBadSignature", err)
	}
}

func TestValidateBFTWirePeerOriginRetainsPreActivationCompatibility(t *testing.T) {
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
	payload, err := MarshalBFTWire(BFTWirePrevote, BFTWirePrevoteMsg{
		Height: 49, Round: 1, Validator: address, BlockHash: "legacy-root",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateBFTWirePeerOrigin(policy, "", payload); err != nil {
		t.Fatalf("pre-activation payload should retain compatibility: %v", err)
	}
}
