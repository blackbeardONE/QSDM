package networking

import (
	"os"
	"strings"
	"testing"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	pb "github.com/libp2p/go-libp2p-pubsub/pb"
	"github.com/libp2p/go-libp2p/core/peer"
)

func attrMsg(author, forwarder peer.ID) *pubsub.Message {
	m := &pb.Message{Data: []byte("x")}
	if author != "" {
		m.From = []byte(author)
	}
	return &pubsub.Message{Message: m, ReceivedFrom: forwarder}
}

// GHSA-2qx4: a message authored by one peer and forwarded by another is
// attributed to the author.
func TestGossipAttribution_AuthorNotForwarder(t *testing.T) {
	author, fwd := peer.ID("author-peer"), peer.ID("honest-forwarder")
	id, skip := gossipAttribution(attrMsg(author, fwd), "self")
	if skip || id != author.String() {
		t.Fatalf("want author %q, got %q skip=%v", author.String(), id, skip)
	}
}

func TestGossipAttribution_DirectAuthor(t *testing.T) {
	a := peer.ID("direct")
	id, skip := gossipAttribution(attrMsg(a, a), "self")
	if skip || id != a.String() {
		t.Fatalf("got %q skip=%v", id, skip)
	}
}

func TestGossipAttribution_NoAuthorFallsBackToForwarder(t *testing.T) {
	fwd := peer.ID("fwd")
	id, skip := gossipAttribution(attrMsg("", fwd), "self")
	if skip || id != fwd.String() {
		t.Fatalf("got %q skip=%v", id, skip)
	}
}

func TestGossipAttribution_SkipsOwnMessages(t *testing.T) {
	self := peer.ID("me")
	if _, skip := gossipAttribution(attrMsg("other", self), self.String()); !skip {
		t.Fatal("a message received from self must be skipped")
	}
	if _, skip := gossipAttribution(attrMsg(self, "fwd"), self.String()); !skip {
		t.Fatal("a message authored by self and echoed back must be skipped")
	}
	if _, skip := gossipAttribution(nil, self.String()); !skip {
		t.Fatal("nil message must be skipped")
	}
}

// The penalty-bearing relay loops must attribute through gossipAttribution,
// never through msg.ReceivedFrom directly (the GHSA-2qx4 pattern).
func TestGossipAttribution_RelayLoopsUseAuthor(t *testing.T) {
	for _, f := range []string{"bft_relay.go", "evidence_relay.go", "pol_relay.go", "libp2p.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if !strings.Contains(src, "gossipAttribution(msg,") {
			t.Errorf("%s does not attribute through gossipAttribution", f)
		}
		if strings.Contains(src, "peer := msg.ReceivedFrom.String()") ||
			strings.Contains(src, "TryConsumeGossip(msg.ReceivedFrom.String()") {
			t.Errorf("%s still attributes content to the forwarder", f)
		}
	}
}
