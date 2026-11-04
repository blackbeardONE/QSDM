package networking

import (
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	pb "github.com/libp2p/go-libp2p-pubsub/pb"
	"github.com/libp2p/go-libp2p/core/peer"
)

func guardMsg(topic string, n int) *pubsub.Message {
	t := topic
	return &pubsub.Message{Message: &pb.Message{Topic: &t, Data: make([]byte, n)}}
}

func TestGossipGuard_SizeAndSelf(t *testing.T) {
	self, remote := peer.ID("self"), peer.ID("remote")
	g := newGossipGuard(self, P2PLimits{}.WithDefaults())
	if r := g.validate(nil, remote, guardMsg(BridgeTopicNameForTest, 64<<10)); r != pubsub.ValidationAccept {
		t.Fatalf("at-limit bridge msg: %v", r)
	}
	if r := g.validate(nil, remote, guardMsg(BridgeTopicNameForTest, 64<<10+1)); r != pubsub.ValidationReject {
		t.Fatalf("oversize bridge msg: %v", r)
	}
	if r := g.validate(nil, self, guardMsg(BridgeTopicNameForTest, 900<<10)); r != pubsub.ValidationAccept {
		t.Fatalf("local oversize msg must be accepted: %v", r)
	}
	// Unknown topic falls back to the global ceiling.
	if r := g.validate(nil, remote, guardMsg("other", 1<<20)); r != pubsub.ValidationAccept {
		t.Fatalf("unknown topic at global max: %v", r)
	}
	if r := g.validate(nil, remote, guardMsg("other", 1<<20+1)); r != pubsub.ValidationReject {
		t.Fatalf("unknown topic above global max: %v", r)
	}
	if st := g.Stats(); st.RejectedOversize != 2 || st.Accepted != 2 {
		t.Fatalf("stats: %+v", st)
	}
}

const BridgeTopicNameForTest = bridgeTopicNameForLimits

func TestGossipGuard_RateLimitPerPeerPerTopic(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := newGossipGuard("self", P2PLimits{GossipPeerRate: 2, GossipPeerBurst: 3}.WithDefaults())
	g.now = func() time.Time { return now }
	a, b := peer.ID("a"), peer.ID("b")
	for i := 0; i < 3; i++ {
		if r := g.validate(nil, a, guardMsg(TxTopicName, 10)); r != pubsub.ValidationAccept {
			t.Fatalf("burst msg %d: %v", i, r)
		}
	}
	if r := g.validate(nil, a, guardMsg(TxTopicName, 10)); r != pubsub.ValidationIgnore {
		t.Fatalf("over-burst msg should be ignored (not rejected): %v", r)
	}
	// Other peer and other topic have independent buckets.
	if r := g.validate(nil, b, guardMsg(TxTopicName, 10)); r != pubsub.ValidationAccept {
		t.Fatalf("peer b: %v", r)
	}
	if r := g.validate(nil, a, guardMsg(BFTTopicName, 10)); r != pubsub.ValidationAccept {
		t.Fatalf("peer a other topic: %v", r)
	}
	// Self is never rate limited.
	for i := 0; i < 10; i++ {
		if r := g.validate(nil, "self", guardMsg(TxTopicName, 10)); r != pubsub.ValidationAccept {
			t.Fatalf("self msg %d: %v", i, r)
		}
	}
	// Refill: 1s at 2/s gives 2 tokens.
	now = now.Add(time.Second)
	for i := 0; i < 2; i++ {
		if r := g.validate(nil, a, guardMsg(TxTopicName, 10)); r != pubsub.ValidationAccept {
			t.Fatalf("refilled msg %d: %v", i, r)
		}
	}
	if r := g.validate(nil, a, guardMsg(TxTopicName, 10)); r != pubsub.ValidationIgnore {
		t.Fatalf("after refill exhausted: %v", r)
	}
	if st := g.Stats(); st.RateLimited != 2 {
		t.Fatalf("stats: %+v", st)
	}
	// Idle buckets are garbage collected.
	now = now.Add(2 * gossipGuardIdleTTL)
	g.validate(nil, b, guardMsg(BridgeTopicNameForTest, 1))
	g.mu.Lock()
	n := len(g.buckets)
	g.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected idle buckets pruned, have %d", n)
	}
}

func TestGossipGuard_NegativeRateDisables(t *testing.T) {
	g := newGossipGuard("self", P2PLimits{GossipPeerRate: -1, GossipPeerBurst: 1}.WithDefaults())
	for i := 0; i < 50; i++ {
		if r := g.validate(nil, "a", guardMsg(TxTopicName, 1)); r != pubsub.ValidationAccept {
			t.Fatalf("msg %d: %v", i, r)
		}
	}
}
