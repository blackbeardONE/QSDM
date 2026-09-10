package networking

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestLiveBFTRelayUsesStrictSignPublisherIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live P2P test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	hostOrigin, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("origin host: %v", err)
	}
	defer hostOrigin.Close()
	hostRelay, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("relay host: %v", err)
	}
	defer hostRelay.Close()
	hostReceiver, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("receiver host: %v", err)
	}
	defer hostReceiver.Close()
	if err := hostRelay.Connect(ctx, peer.AddrInfo{ID: hostOrigin.ID(), Addrs: hostOrigin.Addrs()}); err != nil {
		t.Fatalf("connect origin to relay: %v", err)
	}
	if err := hostReceiver.Connect(ctx, peer.AddrInfo{ID: hostRelay.ID(), Addrs: hostRelay.Addrs()}); err != nil {
		t.Fatalf("connect relay to receiver: %v", err)
	}

	psOrigin, err := pubsub.NewGossipSub(ctx, hostOrigin, pubsub.WithMessageSignaturePolicy(DefaultPubsubSignaturePolicy))
	if err != nil {
		t.Fatalf("origin pubsub: %v", err)
	}
	psRelay, err := pubsub.NewGossipSub(ctx, hostRelay, pubsub.WithMessageSignaturePolicy(DefaultPubsubSignaturePolicy))
	if err != nil {
		t.Fatalf("relay pubsub: %v", err)
	}
	psReceiver, err := pubsub.NewGossipSub(ctx, hostReceiver, pubsub.WithMessageSignaturePolicy(DefaultPubsubSignaturePolicy))
	if err != nil {
		t.Fatalf("receiver pubsub: %v", err)
	}
	topicOrigin, err := psOrigin.Join(BFTTopicName)
	if err != nil {
		t.Fatalf("join origin topic: %v", err)
	}
	subOrigin, err := topicOrigin.Subscribe()
	if err != nil {
		t.Fatalf("subscribe origin topic: %v", err)
	}
	defer subOrigin.Cancel()
	topicRelay, err := psRelay.Join(BFTTopicName)
	if err != nil {
		t.Fatalf("join relay topic: %v", err)
	}
	subRelay, err := topicRelay.Subscribe()
	if err != nil {
		t.Fatalf("subscribe relay topic: %v", err)
	}
	defer subRelay.Cancel()

	signer, _, err := chain.LoadOrCreateBFTSigner(filepath.Join(t.TempDir(), "consensus.json"))
	if err != nil {
		t.Fatal(err)
	}
	membership := chain.ConsensusMembership{
		SchemaVersion:   chain.ConsensusMembershipSchemaVersion,
		NetworkID:       "qsdm-live-relay-peer-origin-test",
		EffectiveHeight: 10,
		Members: []chain.ConsensusMember{{
			Address:               signer.Address(),
			ConsensusPublicKeyHex: hex.EncodeToString(signer.GetPublicKey()),
			P2PPeerID:             hostOrigin.ID().String(),
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
	peerOriginPolicy, err := chain.NewBFTPeerOriginPolicy(membershipPolicy)
	if err != nil {
		t.Fatal(err)
	}
	authExec := chain.NewBFTExecutor(chain.NewBFTConsensus(chain.NewValidatorSet(chain.DefaultValidatorSetConfig()), chain.DefaultConsensusConfig()))
	authExec.SetRequireSignedVotes(true)
	authExec.SetSignedVoteActivationHeight(10)
	authExec.SetMembershipPolicy(membershipPolicy)
	ingress := NewBFTGossipIngress(DefaultBFTGossipConfig(), nil)
	ingress.SetAuthenticationExecutor(authExec)
	ingress.SetPeerOriginPolicy(peerOriginPolicy)
	relayReceiver, err := NewBFTP2PRelay(&psJoiner{ps: psReceiver}, ingress, hostReceiver.ID().String())
	if err != nil {
		t.Fatalf("receiver relay: %v", err)
	}
	defer relayReceiver.Close()
	// Reuse the relay's already-joined topic. This pubsub version rejects a
	// second Join for the same topic, while additional subscriptions are safe.
	recvSub, err := relayReceiver.topic.Subscribe()
	if err != nil {
		t.Fatalf("subscribe receiver inspection topic: %v", err)
	}
	defer recvSub.Cancel()

	// The topology has no direct origin-to-receiver connection. Give GossipSub
	// time to build an origin -> relay -> receiver mesh before publishing.
	time.Sleep(750 * time.Millisecond)
	root, err := membership.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	message := chain.BFTWirePrevoteMsg{Height: 10, Round: 1, Validator: signer.Address(), BlockHash: "state-root", MembershipRoot: root}
	if err := chain.SignPrevote(&message, signer); err != nil {
		t.Fatal(err)
	}
	payload, err := chain.MarshalBFTWire(chain.BFTWirePrevote, message)
	if err != nil {
		t.Fatal(err)
	}
	if err := topicOrigin.Publish(ctx, payload); err != nil {
		t.Fatalf("publish: %v", err)
	}

	observed := false
	deadline := time.After(12 * time.Second)
	for !observed {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for receiver observation, stats=%+v", ingress.Stats())
		default:
		}
		message, err := recvSub.Next(ctx)
		if err != nil {
			continue
		}
		if message.GetFrom() != hostOrigin.ID() {
			continue
		}
		if message.ReceivedFrom != hostRelay.ID() {
			t.Fatalf("received from = %s, want relay %s; publisher=%s", message.ReceivedFrom, hostRelay.ID(), message.GetFrom())
		}
		observed = true
	}

	deadline = time.After(12 * time.Second)
	for {
		stats := ingress.Stats()
		if stats.IngressOK == 1 {
			if stats.PublisherRejected != 0 || stats.ApplyErrors != 0 {
				t.Fatalf("relay ingress stats = %+v", stats)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for StrictSign publisher validation, stats=%+v", ingress.Stats())
		case <-time.After(50 * time.Millisecond):
		}
	}
	if stats := authExec.AuthStats(); stats.SignedAccepted == 0 {
		t.Fatalf("expected the signed vote to reach authentication, got %+v", stats)
	}
}
