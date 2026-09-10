package networking

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestBFTGossipIngressRequiresScheduledPublisherWhenEnabled(t *testing.T) {
	policy, signer, address, publisher, membershipRoot := newBFTGossipPeerOriginFixture(t)
	authExec := chain.NewBFTExecutor(chain.NewBFTConsensus(chain.NewValidatorSet(chain.DefaultValidatorSetConfig()), chain.DefaultConsensusConfig()))
	authExec.SetRequireSignedVotes(true)
	authExec.SetSignedVoteActivationHeight(10)
	ingress := NewBFTGossipIngress(DefaultBFTGossipConfig(), nil)
	ingress.SetAuthenticationExecutor(authExec)
	ingress.SetPeerOriginPolicy(policy)

	goodPayload := signedPeerOriginPrevote(t, signer, address, 10, "state-root-a", membershipRoot)
	if err := ingress.HandlePeerMessageFromPublisher("relay-a", publisher, goodPayload); err != nil {
		t.Fatalf("scheduled publisher should be accepted: %v", err)
	}

	wrongPayload := signedPeerOriginPrevote(t, signer, address, 11, "state-root-b", membershipRoot)
	if err := ingress.HandlePeerMessageFromPublisher("relay-b", testIngressPeerID(t), wrongPayload); !errors.Is(err, chain.ErrBFTPeerOriginUnauthorized) {
		t.Fatalf("wrong publisher error = %v, want ErrBFTPeerOriginUnauthorized", err)
	}

	missingPayload := signedPeerOriginPrevote(t, signer, address, 12, "state-root-c", membershipRoot)
	if err := ingress.HandlePeerMessage("relay-c", missingPayload); !errors.Is(err, chain.ErrBFTPeerOriginMissing) {
		t.Fatalf("legacy ingress without publisher error = %v, want ErrBFTPeerOriginMissing", err)
	}

	stats := ingress.Stats()
	if stats.IngressOK != 1 || stats.PublisherRejected != 2 || stats.ApplyErrors != 0 {
		t.Fatalf("ingress stats = %+v, want one accept, two publisher rejections, and no executor apply errors", stats)
	}
}

func newBFTGossipPeerOriginFixture(t *testing.T) (*chain.BFTPeerOriginPolicy, chain.BFTSigner, string, string, string) {
	t.Helper()
	signer, _, err := chain.LoadOrCreateBFTSigner(filepath.Join(t.TempDir(), "consensus.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, publicKey, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := peer.IDFromPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	membership := chain.ConsensusMembership{
		SchemaVersion:   chain.ConsensusMembershipSchemaVersion,
		NetworkID:       "qsdm-networking-peer-origin-test",
		EffectiveHeight: 10,
		Members: []chain.ConsensusMember{{
			Address:               signer.Address(),
			ConsensusPublicKeyHex: hex.EncodeToString(signer.GetPublicKey()),
			P2PPeerID:             publisher.String(),
			VotingPower:           1,
		}},
	}
	schedule, err := chain.NewConsensusMembershipSchedule([]chain.ConsensusMembership{membership})
	if err != nil {
		t.Fatal(err)
	}
	membershipPolicy, err := chain.NewBFTMembershipPolicy(schedule, 10)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := chain.NewBFTPeerOriginPolicy(membershipPolicy)
	if err != nil {
		t.Fatal(err)
	}
	root, err := membership.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	return policy, signer, signer.Address(), publisher.String(), root
}

func signedPeerOriginPrevote(t *testing.T, signer chain.BFTSigner, address string, height uint64, blockHash, membershipRoot string) []byte {
	t.Helper()
	message := chain.BFTWirePrevoteMsg{Height: height, Round: 1, Validator: address, BlockHash: blockHash, MembershipRoot: membershipRoot}
	if err := chain.SignPrevote(&message, signer); err != nil {
		t.Fatal(err)
	}
	payload, err := chain.MarshalBFTWire(chain.BFTWirePrevote, message)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func testIngressPeerID(t *testing.T) string {
	t.Helper()
	_, publicKey, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}
