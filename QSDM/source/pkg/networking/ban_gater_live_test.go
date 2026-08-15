package networking

import (
	"context"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Everything in ban_gater_test.go exercises the gater's predicates directly.
// None of it touches SetReputationGate's closure -- the peer.Decode into
// host.Network().ClosePeer that is the actual production wiring. A reviewer
// proved the gap by corrupting that peer.Decode call so every ban became a
// silent no-op at the transport: the whole package stayed green, all six unit
// tests included.
//
// The unit fixtures also cannot catch a peer-ID format mismatch, because
// testPeer builds IDs from arbitrary strings. peer.ID("bad-peer").String() is
// base58 of those raw bytes and does NOT round-trip through peer.Decode, which
// wants a multihash. The unit tests never notice because they compare
// p.String() to itself or pass literals straight to RecordEvent.
//
// So this test uses two real libp2p hosts and real peer IDs, and asserts the
// observable end state: a banned peer's live connection is gone.
func TestBanGate_closesLiveConnectionOnBan(t *testing.T) {
	if testing.Short() {
		t.Skip("two-host libp2p ban-gate integration is slow")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h1.Close() })
	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h2.Close() })

	if err := h1.Connect(ctx, peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if h1.Network().Connectedness(h2.ID()) != network.Connected {
		t.Fatal("precondition: hosts should be connected before the ban")
	}

	rt := NewReputationTracker(DefaultReputationConfig())
	net := &Network{Host: h1, banGater: &banGater{}}
	net.SetReputationGate(rt)

	// A REAL peer ID string, which is what every ingress keys on
	// (msg.ReceivedFrom.String()). If the hook's peer.Decode could not parse
	// this, the connection would survive and this test would fail -- which is
	// exactly the corruption the unit tests missed.
	target := h2.ID().String()
	for i := 0; i < 40 && !rt.IsBanned(target); i++ {
		rt.RecordEvent(target, EventInvalidTx, 0)
	}
	if !rt.IsBanned(target) {
		t.Fatal("precondition: peer should be banned")
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if h1.Network().Connectedness(h2.ID()) != network.Connected {
			return // closed, as required
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("banning a peer must close its live connection: the OnBan hook either " +
		"never fired, or its peer.Decode failed and ClosePeer was never called")
}

// The gater must refuse the reconnect too, or closing the connection just makes
// the peer redial. This drives the real InterceptSecured/InterceptPeerDial path
// through libp2p rather than calling the predicates directly.
func TestBanGate_refusesReconnectFromBannedPeer(t *testing.T) {
	if testing.Short() {
		t.Skip("two-host libp2p ban-gate integration is slow")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	gater := &banGater{}
	h1, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"),
		libp2p.ConnectionGater(gater),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h1.Close() })
	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h2.Close() })

	rt := NewReputationTracker(DefaultReputationConfig())
	gater.SetTracker(rt)

	target := h2.ID().String()
	for i := 0; i < 40 && !rt.IsBanned(target); i++ {
		rt.RecordEvent(target, EventInvalidTx, 0)
	}
	if !rt.IsBanned(target) {
		t.Fatal("precondition: peer should be banned")
	}

	if err := h1.Connect(ctx, peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()}); err == nil {
		t.Fatal("dialling a banned peer must be refused by the connection gater")
	}
}

// An unbanned peer must still connect through the gater, or the two tests above
// are satisfied by a gate that refuses everything.
func TestBanGate_allowsUnbannedPeerThroughRealDial(t *testing.T) {
	if testing.Short() {
		t.Skip("two-host libp2p ban-gate integration is slow")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	gater := &banGater{}
	gater.SetTracker(NewReputationTracker(DefaultReputationConfig()))
	h1, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"),
		libp2p.ConnectionGater(gater),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h1.Close() })
	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h2.Close() })

	if err := h1.Connect(ctx, peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()}); err != nil {
		t.Fatalf("an unbanned peer must still be dialable through the gater: %v", err)
	}
}
