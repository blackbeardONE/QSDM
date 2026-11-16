package networking

// End to end through the real relay loop: attacker A is connected only to the honest forwarder F, and F
// only to the victim V. A publishes one anchor propose and three conflicting proposes at one (height, round). V's BFT
// relay must charge A (the StrictSign author), never F (the mesh neighbour that delivered them). Before the fix V
// banned F at the third conflict, as in the report.

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func TestLiveBFTRelay_2qx4_ForwarderNotBanned(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live P2P test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	newHost := func(name string) host.Host {
		h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
		if err != nil {
			t.Fatalf("host %s: %v", name, err)
		}
		t.Cleanup(func() { h.Close() })
		return h
	}
	hA, hF, hV := newHost("A"), newHost("F"), newHost("V")
	if err := hF.Connect(ctx, peer.AddrInfo{ID: hV.ID(), Addrs: hV.Addrs()}); err != nil {
		t.Fatalf("F->V: %v", err)
	}
	if err := hA.Connect(ctx, peer.AddrInfo{ID: hF.ID(), Addrs: hF.Addrs()}); err != nil {
		t.Fatalf("A->F: %v", err)
	}

	psA, err := pubsub.NewGossipSub(ctx, hA)
	if err != nil {
		t.Fatal(err)
	}
	psF, err := pubsub.NewGossipSub(ctx, hF)
	if err != nil {
		t.Fatal(err)
	}
	psV, err := pubsub.NewGossipSub(ctx, hV)
	if err != nil {
		t.Fatal(err)
	}

	// F is an honest node that only forwards (it subscribes, so it is in the mesh).
	topicF, err := psF.Join(BFTTopicName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := topicF.Subscribe(); err != nil {
		t.Fatal(err)
	}

	// V: the real BFT relay with an executor and a reputation tracker.
	vs := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
	for _, a := range []string{"v1", "v2", "v3"} {
		if err := vs.Register(a, 100); err != nil {
			t.Fatal(err)
		}
	}
	bc := chain.NewBFTConsensus(vs, chain.DefaultConsensusConfig())
	rep := NewReputationTracker(DefaultReputationConfig())
	ing := NewBFTGossipIngress(DefaultBFTGossipConfig(), chain.NewBFTExecutor(bc))
	ing.SetReputationTracker(rep)
	relayV, err := NewBFTP2PRelay(&psJoiner{ps: psV}, ing, hV.ID().String())
	if err != nil {
		t.Fatal(err)
	}
	defer relayV.Close()

	topicA, err := psA.Join(BFTTopicName)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second) // mesh formation

	prop, _ := bc.ProposerForRound(0)
	for _, h := range []string{"anchor", "c1", "c2", "c3"} {
		b, err := chain.MarshalBFTWire(chain.BFTWirePropose, chain.BFTWireProposeMsg{Height: 7, Round: 0, Proposer: prop, BlockHash: h})
		if err != nil {
			t.Fatal(err)
		}
		if err := topicA.Publish(ctx, b); err != nil {
			t.Fatalf("publish %s: %v", h, err)
		}
		time.Sleep(700 * time.Millisecond)
	}

	attacker, forwarder := hA.ID().String(), hF.ID().String()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if rec, ok := rep.GetPeer(attacker); ok && rec.Violations >= 2 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if rec, ok := rep.GetPeer(attacker); !ok || rec.Violations < 2 {
		t.Fatalf("the attacker's equivocations did not reach V or were not charged to the author (record %+v, found %v)", rec, ok)
	}
	if rep.IsBanned(forwarder) {
		t.Fatal("V banned the honest forwarder for content it only relayed")
	}
	if rec, ok := rep.GetPeer(forwarder); ok && rec.Violations != 0 {
		t.Fatalf("the honest forwarder got %d violations", rec.Violations)
	}
}
