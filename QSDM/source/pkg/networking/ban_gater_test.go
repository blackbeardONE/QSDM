package networking

import (
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// testPeer returns a peer.ID whose String() form is what the tracker is keyed
// on, so the ban and the gate agree on identity.
func testPeer(raw string) peer.ID { return peer.ID(raw) }

func banGaterFor(t *testing.T, banned string) (*banGater, *ReputationTracker) {
	t.Helper()
	rt := NewReputationTracker(DefaultReputationConfig())
	p := testPeer(banned)
	for i := 0; i < 40 && !rt.IsBanned(p.String()); i++ {
		rt.RecordEvent(p.String(), EventInvalidTx, 0)
	}
	if !rt.IsBanned(p.String()) {
		t.Fatalf("precondition: %q should be banned", p.String())
	}
	g := &banGater{}
	g.SetTracker(rt)
	return g, rt
}

// A banned peer must be refused at dial and at the point its identity becomes
// known on an inbound connection. Before this the ban was advisory: each
// ingress dropped payloads individually while the connection, its stream slots
// and its pubsub fan-out all survived.
func TestBanGater_refusesBannedPeer(t *testing.T) {
	g, _ := banGaterFor(t, "bad-peer")
	p := testPeer("bad-peer")

	if g.InterceptPeerDial(p) {
		t.Error("must not dial a banned peer")
	}
	if g.InterceptAddrDial(p, nil) {
		t.Error("must not dial any address of a banned peer")
	}
	if g.InterceptSecured(network.DirInbound, p, nil) {
		t.Error("must refuse an inbound connection from a banned peer once identified")
	}
	if g.InterceptSecured(network.DirOutbound, p, nil) {
		t.Error("must refuse an outbound connection to a banned peer")
	}
}

// InterceptAccept runs before the security handshake, so no peer identity is
// available. It must allow, or the node would reject connections on address
// alone -- banning whole NATs and shared hosts along with the offender.
func TestBanGater_acceptsBeforeIdentityIsKnown(t *testing.T) {
	g, _ := banGaterFor(t, "bad-peer")
	if !g.InterceptAccept(nil) {
		t.Error("InterceptAccept must allow: the peer is not identified yet, so " +
			"refusing here would gate on address rather than identity")
	}
}

// The honest path must survive, or the gate is satisfied by refusing everything.
func TestBanGater_allowsUnbannedPeer(t *testing.T) {
	g, _ := banGaterFor(t, "bad-peer")
	good := testPeer("good-peer")

	if !g.InterceptPeerDial(good) {
		t.Error("an unbanned peer must be dialable")
	}
	if !g.InterceptSecured(network.DirInbound, good, nil) {
		t.Error("an unbanned peer's inbound connection must be allowed")
	}
}

// A gater with no tracker must gate nothing. The host is built before the
// tracker exists, so the gater is constructed inert and attached later; if the
// inert state blocked anything, every node would fail to connect during the
// window before SetReputationGate runs -- and permanently on any binary that
// never calls it.
func TestBanGater_inertWithoutTracker(t *testing.T) {
	g := &banGater{}
	p := testPeer("anyone")

	if !g.InterceptPeerDial(p) || !g.InterceptSecured(network.DirInbound, p, nil) || !g.InterceptAccept(nil) {
		t.Fatal("a gater with no tracker must allow everything; blocking here would " +
			"isolate any node that has not wired one")
	}
}

// The ban hook must fire exactly once, on the transition -- not on every
// subsequent event from an already-banned peer. Firing repeatedly would close
// the same connection on every message a banned peer sends.
func TestSetOnBan_firesOnceOnTransition(t *testing.T) {
	rt := NewReputationTracker(DefaultReputationConfig())
	var mu sync.Mutex
	var calls []string
	rt.SetOnBan(func(peerID string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, peerID)
	})

	for i := 0; i < 40; i++ {
		rt.RecordEvent("bad-peer", EventInvalidTx, 0)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("ban hook must fire once on the transition, got %d calls: %v", len(calls), calls)
	}
	if calls[0] != "bad-peer" {
		t.Errorf("hook received the wrong peer: %q", calls[0])
	}
}

// The hook must run with the tracker's lock RELEASED.
//
// This is the property the whole SetOnBan/recordEvent split exists for. The
// real callback calls host.Network().ClosePeer, which can synchronously drive
// disconnect notifications back into code that reads this tracker. If the hook
// ran under the write lock held by RecordEvent, the first ban would deadlock
// the receive path.
//
// Reading the tracker from inside the hook reproduces that exactly: it takes
// the read lock, which cannot be granted while the writer holds it.
func TestSetOnBan_runsWithoutHoldingTheLock(t *testing.T) {
	rt := NewReputationTracker(DefaultReputationConfig())
	done := make(chan bool, 1)

	rt.SetOnBan(func(peerID string) {
		// Would block forever if the write lock were still held.
		done <- rt.IsBanned(peerID)
	})

	go func() {
		for i := 0; i < 40; i++ {
			rt.RecordEvent("bad-peer", EventInvalidTx, 0)
		}
	}()

	select {
	case banned := <-done:
		if !banned {
			t.Error("the hook should observe the peer as banned by the time it runs")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ban hook did not deliver within 5s. Either it never fired, or it is " +
			"running while the tracker's write lock is held and so cannot read the " +
			"tracker it was called about -- both are failures, and the second is the " +
			"deadlock this split exists to prevent")
	}
}

// A ban is permanent for the process lifetime: DecayAll moves the score but
// never clears the Banned flag, and Unban has no production caller. Before the
// transport gate that only meant payloads were dropped. Now it severs the
// connection and refuses the redial -- so a false positive against a bootstrap
// peer would partition the node until restart. Bootstrap peers are therefore
// exempt from the transport gate specifically.
func TestBanGater_exemptPeerIsNotRefusedAtTransport(t *testing.T) {
	g, _ := banGaterFor(t, "bad-peer")
	p := testPeer("bad-peer")

	// Still banned in the tracker -- the exemption is transport-only.
	if !g.rep.IsBanned(p.String()) {
		t.Fatal("precondition: peer should still be banned in the tracker")
	}
	if g.InterceptPeerDial(p) {
		t.Fatal("precondition: a banned, non-exempt peer must be refused")
	}

	g.SetExemptPeers([]string{p.String()})

	if !g.InterceptPeerDial(p) {
		t.Error("an exempt peer must remain dialable despite being banned")
	}
	if !g.InterceptSecured(network.DirInbound, p, nil) {
		t.Error("an exempt peer's inbound connection must be allowed despite the ban")
	}
	if !g.rep.IsBanned(p.String()) {
		t.Error("exemption must not clear the ban itself: every ingress should still " +
			"drop this peer's payloads")
	}
}

// The exemption must not become a blanket bypass: a peer that is not on the
// list is still refused after SetExemptPeers is called.
func TestBanGater_exemptionIsScopedToListedPeers(t *testing.T) {
	g, _ := banGaterFor(t, "bad-peer")
	g.SetExemptPeers([]string{testPeer("someone-else").String()})

	if g.InterceptPeerDial(testPeer("bad-peer")) {
		t.Error("a banned peer that is not exempt must still be refused")
	}
}
