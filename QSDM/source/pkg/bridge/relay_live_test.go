package bridge

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// liveNode is one libp2p host with GossipSub and a bridge relay on the
// bridge topic, wired the way NewP2PRelayWithAuth wires a real node.
type liveNode struct {
	host  host.Host
	relay *P2PRelay
	bp    *BridgeProtocol
	asp   *AtomicSwapProtocol
}

func newLiveNode(t *testing.T, ctx context.Context, auth RelayAuthConfig) *liveNode {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	t.Cleanup(func() { h.Close() })
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		t.Fatalf("pubsub: %v", err)
	}
	topic, err := ps.Join(BridgeTopicName)
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	sub, err := topic.Subscribe()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	a, err := newRelayAuth(auth)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	rctx, rcancel := context.WithCancel(ctx)
	t.Cleanup(rcancel)
	n := &liveNode{
		host: h,
		bp:   &BridgeProtocol{locks: make(map[string]*Lock)},
		asp:  &AtomicSwapProtocol{swaps: make(map[string]*Swap)},
	}
	n.relay = &P2PRelay{topic: topic, sub: sub, bridge: n.bp, swap: n.asp, ctx: rctx, cancel: rcancel, auth: a}
	go n.relay.readLoop(h.ID().String())
	return n
}

func (n *liveNode) connect(t *testing.T, ctx context.Context, to *liveNode) {
	t.Helper()
	if err := n.host.Connect(ctx, peer.AddrInfo{ID: to.host.ID(), Addrs: to.host.Addrs()}); err != nil {
		t.Fatalf("connect: %v", err)
	}
}

func (n *liveNode) id() string { return n.host.ID().String() }

func (n *liveNode) lock(id string) (Lock, bool) {
	n.bp.mu.RLock()
	defer n.bp.mu.RUnlock()
	l, ok := n.bp.locks[id]
	if !ok {
		return Lock{}, false
	}
	return *l, true
}

// publishUntil calls publish, and again every 500 ms, until cond holds or the
// deadline passes. GossipSub can drop a message published just after two
// hosts connect, and every event these tests publish is idempotent: a repeat
// is a no-op on the receiver.
func publishUntil(t *testing.T, d time.Duration, what string, publish func(), cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	next := time.Now()
	for time.Now().Before(deadline) {
		if !time.Now().Before(next) {
			publish()
			next = time.Now().Add(500 * time.Millisecond)
		}
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// TestLiveP2PRelay_LockPropagation boots two real libp2p nodes. Node A signs
// with a relayer key that node B trusts; a lock and its redemption published
// on A are applied on B.
func TestLiveP2PRelay_LockPropagation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live P2P test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	key := newTestSigner(t)
	a := newLiveNode(t, ctx, RelayAuthConfig{Signer: key})
	b := newLiveNode(t, ctx, RelayAuthConfig{TrustedKeys: [][]byte{key.GetPublicKey()}})
	b.connect(t, ctx, a)
	time.Sleep(500 * time.Millisecond) // let GossipSub form the mesh

	lock := Lock{
		ID: "live-lock-001", SourceChain: "chain-alpha", TargetChain: "chain-beta", Asset: "TOKEN",
		Amount: 42.0, SecretHash: "abc123", Secret: "should-be-stripped", Status: LockStatusLocked,
	}
	publishUntil(t, 10*time.Second, "lock on B",
		func() { a.relay.PublishLockEvent("lock_created", &lock, a.id()) },
		func() bool { _, ok := b.lock("live-lock-001"); return ok })

	l, _ := b.lock("live-lock-001")
	if l.Amount != 42.0 || l.Secret != "" || l.SecretHash != "abc123" {
		t.Fatalf("propagated lock = %+v", l)
	}

	publishUntil(t, 10*time.Second, "redeem on B",
		func() { a.relay.PublishLockEvent("lock_redeemed", &lock, a.id()) },
		func() bool {
			l, _ := b.lock("live-lock-001")
			return l.Status == LockStatusRedeemed
		})
	if st := b.relay.Stats(); st.Applied != 2 {
		t.Fatalf("B stats = %+v, want 2 applied", st)
	}
}

// TestLiveP2PRelay_UntrustedEventsIgnored: a default node (no trusted keys)
// and a node that trusts a different key both ignore A's events, signed or
// not, and count them.
func TestLiveP2PRelay_UntrustedEventsIgnored(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live P2P test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	attackerKey := newTestSigner(t)
	trustedKey := newTestSigner(t)
	a := newLiveNode(t, ctx, RelayAuthConfig{Signer: attackerKey})
	unsignedPub := newLiveNode(t, ctx, RelayAuthConfig{})
	def := newLiveNode(t, ctx, RelayAuthConfig{})
	strict := newLiveNode(t, ctx, RelayAuthConfig{TrustedKeys: [][]byte{trustedKey.GetPublicKey()}})
	def.connect(t, ctx, a)
	strict.connect(t, ctx, a)
	unsignedPub.connect(t, ctx, def)
	unsignedPub.connect(t, ctx, strict)
	time.Sleep(700 * time.Millisecond)

	// Pre-existing local lock on the strict node that an attacker tries to finish.
	strict.bp.mu.Lock()
	strict.bp.locks["victim"] = &Lock{ID: "victim", Status: LockStatusLocked}
	strict.bp.mu.Unlock()

	evil := Lock{ID: "evil-lock", Amount: 1e9, Status: LockStatusLocked}
	victim := Lock{ID: "victim"}
	publishUntil(t, 10*time.Second, "rejections counted",
		func() {
			a.relay.PublishLockEvent("lock_created", &evil, a.id())
			a.relay.PublishLockEvent("lock_refunded", &victim, a.id())
			unsignedPub.relay.PublishLockEvent("lock_redeemed", &victim, unsignedPub.id())
		},
		func() bool {
			d, s := def.relay.Stats(), strict.relay.Stats()
			return d.NoTrustedKeys >= 3 && s.UntrustedSigner >= 2 && s.Unsigned >= 1
		})
	if _, ok := def.lock("evil-lock"); ok {
		t.Fatal("default node applied an untrusted lock")
	}
	if _, ok := strict.lock("evil-lock"); ok {
		t.Fatal("strict node applied an untrusted lock")
	}
	if l, _ := strict.lock("victim"); l.Status != LockStatusLocked {
		t.Fatalf("victim lock status = %s, want locked", l.Status)
	}
	if def.relay.Stats().Applied != 0 || strict.relay.Stats().Applied != 0 {
		t.Fatal("untrusted events applied")
	}
}

// TestLiveP2PRelay_SwapPropagation verifies signed swap events propagate.
func TestLiveP2PRelay_SwapPropagation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live P2P test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	key := newTestSigner(t)
	a := newLiveNode(t, ctx, RelayAuthConfig{Signer: key})
	b := newLiveNode(t, ctx, RelayAuthConfig{TrustedKeys: [][]byte{key.GetPublicKey()}})
	b.connect(t, ctx, a)
	time.Sleep(500 * time.Millisecond)

	swap := Swap{
		ID: "live-swap-001", InitiatorChain: "chain-x", ParticipantChain: "chain-y",
		InitiatorAmount: 100, ParticipantAmount: 200, Status: SwapStatusInitiated,
	}
	publishUntil(t, 10*time.Second, "swap on B",
		func() { a.relay.PublishSwapEvent("swap_initiated", &swap, a.id()) },
		func() bool {
			b.asp.mu.RLock()
			defer b.asp.mu.RUnlock()
			s, ok := b.asp.swaps["live-swap-001"]
			return ok && s.InitiatorAmount == 100
		})
}

// TestLiveP2PRelay_ThreeNodes: signed lock events cross a star topology; the
// hub forwards them without trusting the key itself.
func TestLiveP2PRelay_ThreeNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live P2P test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	key := newTestSigner(t)
	trust := RelayAuthConfig{TrustedKeys: [][]byte{key.GetPublicKey()}}
	n0 := newLiveNode(t, ctx, RelayAuthConfig{})
	n1 := newLiveNode(t, ctx, trust)
	n2 := newLiveNode(t, ctx, RelayAuthConfig{TrustedKeys: [][]byte{key.GetPublicKey()}, Signer: key})
	n1.connect(t, ctx, n0)
	n2.connect(t, ctx, n0)
	time.Sleep(700 * time.Millisecond)

	lock := Lock{ID: "3node-lock", Amount: 77, SecretHash: "h3", Status: LockStatusLocked}
	publishUntil(t, 10*time.Second, "lock on node 1 via hub",
		func() { n2.relay.PublishLockEvent("lock_created", &lock, n2.id()) },
		func() bool { _, ok := n1.lock("3node-lock"); return ok })
	if _, ok := n0.lock("3node-lock"); ok {
		t.Fatal("hub without trusted keys applied the lock")
	}
	if _, ok := n2.lock("3node-lock"); ok {
		t.Fatal("publisher applied its own echoed event")
	}
}
