package networking_test

import (
	"context"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/bridge"
	"github.com/blackbeardONE/QSDM/pkg/crypto"
	"github.com/blackbeardONE/QSDM/pkg/networking"
	"github.com/libp2p/go-libp2p/core/peer"
)

type bridgeNode struct {
	net   *networking.Network
	bp    *bridge.BridgeProtocol
	relay *bridge.P2PRelay
}

func newBridgeNode(t *testing.T, ctx context.Context, auth bridge.RelayAuthConfig) *bridgeNode {
	t.Helper()
	n, err := networking.SetupLibP2PWithPortBindAndKey(ctx, logging.NewSilentLogger(), 0, "127.0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.Close() })
	bp, err := bridge.NewBridgeProtocol()
	if err != nil {
		t.Fatal(err)
	}
	asp, err := bridge.NewAtomicSwapProtocol()
	if err != nil {
		t.Fatal(err)
	}
	r, err := bridge.NewP2PRelayWithAuth(n, bp, asp, n.Host.ID().String(), auth)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return &bridgeNode{net: n, bp: bp, relay: r}
}

func (b *bridgeNode) id() string { return b.net.Host.ID().String() }

func (b *bridgeNode) status(lockID string) bridge.LockStatus {
	for _, l := range b.bp.ListLocks() {
		if l.ID == lockID {
			return l.Status
		}
	}
	return ""
}

// until re-runs publish every 500 ms until cond holds (gossipsub may drop a
// message sent before the mesh is up; every event here is idempotent).
func until(t *testing.T, ctx context.Context, what string, publish func(), cond func() bool) {
	t.Helper()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	publish()
	for !cond() {
		select {
		case <-ctx.Done():
			t.Fatalf("timeout: %s", what)
		case <-tick.C:
			publish()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// TestBridgeRelayAuth_TwoHostProductionStack runs the bridge relay on the
// node's real libp2p stack (signed GossipSub and the ban gater). A trusts
// nothing and signs with the relayer key; B trusts that key; C is a peer
// with its own, untrusted key. Only A's signed events change B's state, and
// nothing changes A's or C's.
func TestBridgeRelayAuth_TwoHostProductionStack(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-host libp2p pubsub test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	relayerKey := crypto.NewDilithium()
	otherKey := crypto.NewDilithium()
	if relayerKey == nil || otherKey == nil {
		t.Fatal("NewDilithium returned nil")
	}
	a := newBridgeNode(t, ctx, bridge.RelayAuthConfig{Signer: relayerKey})
	b := newBridgeNode(t, ctx, bridge.RelayAuthConfig{TrustedKeys: [][]byte{relayerKey.GetPublicKey()}})
	c := newBridgeNode(t, ctx, bridge.RelayAuthConfig{Signer: otherKey})
	for _, pair := range [][2]*bridgeNode{{b, a}, {c, a}, {c, b}} {
		if err := pair[0].net.Host.Connect(ctx, peer.AddrInfo{ID: pair[1].net.Host.ID(), Addrs: pair[1].net.Host.Addrs()}); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []*bridgeNode{a, b, c} {
		for len(n.net.PubSub.ListPeers(bridge.BridgeTopicName)) < 2 {
			select {
			case <-ctx.Done():
				t.Fatal("bridge topic peering never formed")
			case <-time.After(50 * time.Millisecond):
			}
		}
	}

	// A creates a lock through the normal local API path and publishes it.
	lock, err := a.bp.LockAsset(ctx, "chain-a", "chain-b", "TOKEN", 10, "recipient", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	until(t, ctx, "lock applied on B",
		func() { a.relay.PublishLockEvent("lock_created", lock, a.id()) },
		func() bool { return b.relay.Stats().Applied >= 1 })
	// The counter is bumped after the state change, so reading state after
	// observing it is race-free.
	if got := b.status(lock.ID); got != bridge.LockStatusLocked {
		t.Fatalf("B lock status = %q, want locked", got)
	}

	// C (untrusted key) tries to refund it on B, and creates a lock of its own.
	fake := &bridge.Lock{ID: "c-fake", Amount: 1e9, Status: bridge.LockStatusLocked}
	until(t, ctx, "untrusted events counted on B",
		func() {
			c.relay.PublishLockEvent("lock_refunded", lock, c.id())
			c.relay.PublishLockEvent("lock_created", fake, c.id())
		},
		func() bool { return b.relay.Stats().UntrustedSigner >= 2 })
	if got := b.status(lock.ID); got != bridge.LockStatusLocked {
		t.Fatalf("B lock status after untrusted refund = %s, want locked", got)
	}
	if b.status("c-fake") != "" {
		t.Fatal("B applied a lock signed by an untrusted key")
	}

	// A redeems; B follows.
	until(t, ctx, "redeem applied on B",
		func() { a.relay.PublishLockEvent("lock_redeemed", lock, a.id()) },
		func() bool { return b.relay.Stats().Applied >= 2 })
	if got := b.status(lock.ID); got != bridge.LockStatusRedeemed {
		t.Fatalf("B lock status = %q, want redeemed", got)
	}

	// A and C trust no key: they never applied a remote event.
	if a.relay.Stats().Applied != 0 || c.relay.Stats().Applied != 0 {
		t.Fatalf("default nodes applied remote events: A %+v C %+v", a.relay.Stats(), c.relay.Stats())
	}
	if c.status(lock.ID) != "" || a.status("c-fake") != "" {
		t.Fatal("default node state changed by remote events")
	}
	if c.relay.Stats().NoTrustedKeys == 0 || a.relay.Stats().NoTrustedKeys == 0 {
		t.Fatalf("ignored remote events not counted: A %+v C %+v", a.relay.Stats(), c.relay.Stats())
	}
	if st := b.relay.Stats(); st.BadSignature != 0 || st.Unsigned != 0 {
		t.Fatalf("B stats = %+v", st)
	}
}
