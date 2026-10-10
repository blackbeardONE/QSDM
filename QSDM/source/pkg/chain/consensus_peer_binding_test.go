package chain

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestConsensusPeerBindingProvesBothCommittedKeys(t *testing.T) {
	membership, address, signer, p2pPrivateKey := newConsensusPeerBindingFixture(t)
	binding, err := CreateConsensusPeerBinding(membership, address, signer, p2pPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyConsensusPeerBinding(membership, binding); err != nil {
		t.Fatalf("honest cross-key binding should verify: %v", err)
	}

	root, err := membership.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if binding.MembershipRoot != root || binding.Address != address || binding.P2PPeerID != membership.Members[0].P2PPeerID {
		t.Fatalf("binding identity = %+v, want membership-bound identity", binding)
	}
}

func TestConsensusPeerBindingRejectsTamperingAndWrongKeys(t *testing.T) {
	membership, address, signer, p2pPrivateKey := newConsensusPeerBindingFixture(t)
	binding, err := CreateConsensusPeerBinding(membership, address, signer, p2pPrivateKey)
	if err != nil {
		t.Fatal(err)
	}

	wrongRoot := binding
	wrongRoot.MembershipRoot = "0000000000000000000000000000000000000000000000000000000000000000"
	if err := VerifyConsensusPeerBinding(membership, wrongRoot); !errors.Is(err, ErrConsensusPeerBindingRoot) {
		t.Fatalf("wrong root error = %v, want ErrConsensusPeerBindingRoot", err)
	}

	otherSigner, _ := newBFTKey(t)
	member, root, err := consensusPeerBindingMember(membership, address)
	if err != nil {
		t.Fatal(err)
	}
	p2pPublicKey, err := libp2pcrypto.UnmarshalPublicKey(mustDecodeHex(t, binding.P2PPublicKeyHex))
	if err != nil {
		t.Fatal(err)
	}
	p2pPublicKeyBytes, err := libp2pcrypto.MarshalPublicKey(p2pPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := consensusPeerBindingDigest(root, member, p2pPublicKeyBytes)
	if err != nil {
		t.Fatal(err)
	}
	otherAuth, err := signAuth(otherSigner, digest)
	if err != nil {
		t.Fatal(err)
	}
	wrongConsensusKey := binding
	wrongConsensusKey.ConsensusAuth = otherAuth
	if err := VerifyConsensusPeerBinding(membership, wrongConsensusKey); !errors.Is(err, ErrConsensusPeerBindingConsensusKey) {
		t.Fatalf("wrong consensus key error = %v, want ErrConsensusPeerBindingConsensusKey", err)
	}

	_, otherP2PPublicKey, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherP2PPublicKeyBytes, err := libp2pcrypto.MarshalPublicKey(otherP2PPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	wrongP2PKey := binding
	wrongP2PKey.P2PPublicKeyHex = hex.EncodeToString(otherP2PPublicKeyBytes)
	if err := VerifyConsensusPeerBinding(membership, wrongP2PKey); !errors.Is(err, ErrConsensusPeerBindingP2PKey) {
		t.Fatalf("wrong libp2p key error = %v, want ErrConsensusPeerBindingP2PKey", err)
	}

	badP2PSignature := binding
	badP2PSignature.P2PSignatureHex = mutateLowerHex(badP2PSignature.P2PSignatureHex)
	if err := VerifyConsensusPeerBinding(membership, badP2PSignature); !errors.Is(err, ErrConsensusPeerBindingSignature) {
		t.Fatalf("bad libp2p signature error = %v, want ErrConsensusPeerBindingSignature", err)
	}
}

func TestConsensusPeerBindingCreationRejectsUncommittedKeys(t *testing.T) {
	membership, address, signer, _ := newConsensusPeerBindingFixture(t)
	otherSigner, _ := newBFTKey(t)
	if _, err := CreateConsensusPeerBinding(membership, address, otherSigner, nil); !errors.Is(err, ErrConsensusPeerBindingConsensusKey) {
		t.Fatalf("wrong consensus signer error = %v, want ErrConsensusPeerBindingConsensusKey", err)
	}

	otherP2PPrivateKey, _, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateConsensusPeerBinding(membership, address, signer, otherP2PPrivateKey); !errors.Is(err, ErrConsensusPeerBindingP2PKey) {
		t.Fatalf("wrong libp2p private key error = %v, want ErrConsensusPeerBindingP2PKey", err)
	}
}

func newConsensusPeerBindingFixture(t *testing.T) (ConsensusMembership, string, BFTSigner, libp2pcrypto.PrivKey) {
	t.Helper()
	signer, address := newBFTKey(t)
	p2pPrivateKey, p2pPublicKey, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p2pPeerID, err := peer.IDFromPublicKey(p2pPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	membership := ConsensusMembership{
		SchemaVersion:   ConsensusMembershipSchemaVersion,
		NetworkID:       "qsdm-peer-binding-test",
		EffectiveHeight: 700,
		Members: []ConsensusMember{{
			Address:               address,
			ConsensusPublicKeyHex: hex.EncodeToString(signer.GetPublicKey()),
			P2PPeerID:             p2pPeerID.String(),
			VotingPower:           1,
		}},
	}
	if err := membership.Validate(); err != nil {
		t.Fatal(err)
	}
	return membership, address, signer, p2pPrivateKey
}

func mustDecodeHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func mutateLowerHex(value string) string {
	if value[0] == '0' {
		return "1" + value[1:]
	}
	return "0" + value[1:]
}
