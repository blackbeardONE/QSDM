package networking

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	libp2p "github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

func TestDefaultP2PLimits_ValuesAndValid(t *testing.T) {
	l := DefaultP2PLimits()
	if l.ConnLowWater != 64 || l.ConnHighWater != 96 || l.ConnGracePeriod != time.Minute {
		t.Fatalf("connmgr defaults: %+v", l)
	}
	if l.MaxConns != 192 || l.MaxInboundConns != 128 || l.MaxStreams != 2048 || l.MaxInboundStreams != 1024 {
		t.Fatalf("system defaults: %+v", l)
	}
	if l.MaxConnsPerPeer != 8 || l.MaxStreamsPerPeer != 256 || l.MaxInboundStreamsPerPeer != 128 || l.MaxConnsPerIP != 8 {
		t.Fatalf("per-peer defaults: %+v", l)
	}
	if l.GossipMaxMessageBytes != pubsub.DefaultMaxMessageSize {
		t.Fatalf("global gossip max must stay at the library default 1 MiB, got %d", l.GossipMaxMessageBytes)
	}
	if l.GossipPeerRate != 100 || l.GossipPeerBurst != 1000 || l.GossipPeerScoringDisabled {
		t.Fatalf("gossip defaults: %+v", l)
	}
	want := map[string]int{
		"qsdm-transactions": 256 << 10,
		"qsdm-bft":          1 << 20,
		"qsdm-blocks":       1 << 20,
		"qsdm-evidence":     256 << 10,
		"qsdm-prevote-lock": 512 << 10,
		"qsdm-bridge":       64 << 10,
	}
	if len(l.GossipTopicMaxBytes) != len(want) {
		t.Fatalf("topic table: %v", l.GossipTopicMaxBytes)
	}
	for k, v := range want {
		if l.GossipTopicMaxBytes[k] != v {
			t.Fatalf("topic %s: got %d want %d", k, l.GossipTopicMaxBytes[k], v)
		}
	}
	if err := l.Validate(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(l.String(), "connmgr=64/96") {
		t.Fatalf("String: %s", l.String())
	}
}

func TestP2PLimits_WithDefaults_TopicOverrideAndClamp(t *testing.T) {
	l := P2PLimits{
		GossipMaxMessageBytes: 512 << 10,
		GossipTopicMaxBytes:   map[string]int{"qsdm-bridge": 4096, "custom": 999, "neg": -1},
	}.WithDefaults()
	if l.GossipTopicMaxBytes["qsdm-bridge"] != 4096 || l.GossipTopicMaxBytes["custom"] != 999 {
		t.Fatalf("override lost: %v", l.GossipTopicMaxBytes)
	}
	if _, ok := l.GossipTopicMaxBytes["neg"]; ok {
		t.Fatal("non-positive topic size must be ignored")
	}
	if l.GossipTopicMaxBytes["qsdm-blocks"] != 512<<10 {
		t.Fatalf("topic size not clamped to global: %d", l.GossipTopicMaxBytes["qsdm-blocks"])
	}
}

func TestP2PLimits_ValidateRejects(t *testing.T) {
	cases := map[string]P2PLimits{
		"low>=high":         {ConnLowWater: 96, ConnHighWater: 96},
		"maxconns<high":     {MaxConns: 50},
		"inbound>max":       {MaxInboundConns: 500},
		"negative":          {MaxConnsPerIP: -1},
		"inboundstreams>":   {MaxInboundStreams: 4096},
		"peerinbound>peer":  {MaxInboundStreamsPerPeer: 1000},
		"negative grace":    {ConnGracePeriod: -time.Second},
		"negative validate": {GossipValidateQueue: -5},
	}
	for name, l := range cases {
		if err := l.Validate(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if err := (P2PLimits{Disabled: true, ConnLowWater: 9, ConnHighWater: 1}).Validate(); err != nil {
		t.Fatalf("disabled limits must not be validated: %v", err)
	}
}

func TestP2PLimits_ResourceConfigApplied(t *testing.T) {
	l := P2PLimits{MaxConns: 300, MaxInboundConns: 100, MaxStreams: 999, MaxInboundStreams: 500,
		MaxConnsPerPeer: 3, MaxStreamsPerPeer: 77, MaxInboundStreamsPerPeer: 33}.WithDefaults()
	p := l.resourceLimitConfig().ToPartialLimitConfig()
	if p.System.Conns != 300 || p.System.ConnsInbound != 100 || p.System.ConnsOutbound != 300 {
		t.Fatalf("system conns: %+v", p.System)
	}
	if p.System.Streams != 999 || p.System.StreamsInbound != 500 {
		t.Fatalf("system streams: %+v", p.System)
	}
	if p.PeerDefault.Conns != 3 || p.PeerDefault.ConnsInbound != 3 || p.PeerDefault.Streams != 77 || p.PeerDefault.StreamsInbound != 33 {
		t.Fatalf("peer default: %+v", p.PeerDefault)
	}
	if p.System.Memory <= 0 {
		t.Fatalf("memory limit should keep the autoscaled default, got %v", p.System.Memory)
	}
}

// The resource manager itself refuses connections beyond the per-IP and
// system-inbound limits, while loopback stays exempt from the per-IP cap.
func TestP2PLimits_ResourceManagerRejectsBeyondLimits(t *testing.T) {
	l := P2PLimits{ConnLowWater: 2, ConnHighWater: 4, MaxConns: 6, MaxInboundConns: 5, MaxConnsPerIP: 2}.WithDefaults()
	mgr, _, err := l.newResourceManager()
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	open := func(addr string) (network.ConnManagementScope, error) {
		return mgr.OpenConnection(network.DirInbound, true, ma.StringCast(addr))
	}
	var scopes []network.ConnManagementScope
	defer func() {
		for _, s := range scopes {
			s.Done()
		}
	}()
	// Per-IP: 2 allowed from 203.0.113.7, the third refused.
	for i := 0; i < 2; i++ {
		s, err := open("/ip4/203.0.113.7/tcp/4001")
		if err != nil {
			t.Fatalf("per-ip conn %d refused early: %v", i, err)
		}
		scopes = append(scopes, s)
	}
	if s, err := open("/ip4/203.0.113.7/tcp/4001"); err == nil {
		s.Done()
		t.Fatal("third connection from one IP should be refused")
	}
	// System inbound: 5 total; 2 used, 3 more from distinct IPs, then refused.
	for i, ip := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		s, err := open("/ip4/" + ip + "/tcp/4001")
		if err != nil {
			t.Fatalf("system conn %d refused early: %v", i, err)
		}
		scopes = append(scopes, s)
	}
	if s, err := open("/ip4/198.51.100.4/tcp/4001"); err == nil {
		s.Done()
		t.Fatal("connection beyond max_inbound_conns should be refused")
	}
	// Free everything, then loopback is not bound by the per-IP cap.
	for _, s := range scopes {
		s.Done()
	}
	scopes = nil
	for i := 0; i < 4; i++ {
		s, err := open("/ip4/127.0.0.1/tcp/4001")
		if err != nil {
			t.Fatalf("loopback conn %d refused: %v", i, err)
		}
		scopes = append(scopes, s)
	}
}

// End-to-end: a host built with the limits refuses an inbound connection
// beyond max_inbound_conns from a real libp2p peer.
func TestP2PLimits_HostRefusesInboundBeyondLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-host libp2p test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	l := P2PLimits{ConnLowWater: 1, ConnHighWater: 2, MaxConns: 2, MaxInboundConns: 1}.WithDefaults()
	limitOpts, err := l.hostOptions()
	if err != nil {
		t.Fatal(err)
	}
	a, err := libp2p.New(append([]libp2p.Option{libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0")}, limitOpts...)...)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	newPeer := func() (interface {
		Connect(context.Context, peer.AddrInfo) error
		Close() error
	}, error) {
		return libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	}
	b, err := newPeer()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	c, err := newPeer()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ai := peer.AddrInfo{ID: a.ID(), Addrs: a.Addrs()}
	if err := b.Connect(ctx, ai); err != nil {
		t.Fatalf("first inbound connection should succeed: %v", err)
	}
	_ = c.Connect(ctx, ai) // expected to fail (or be torn down by a)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(a.Network().Peers()) <= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n := len(a.Network().Peers()); n != 1 {
		t.Fatalf("host with max_inbound_conns=1 has %d peers", n)
	}
}

func TestP2PLimits_PeerScoreParamsValid(t *testing.T) {
	params, thresholds := gossipPeerScoreParams(DefaultGossipTopicMaxBytes())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, err := libp2p.New(libp2p.NoListenAddrs)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	// NewGossipSub validates the score parameters and thresholds.
	if _, err := pubsub.NewGossipSub(ctx, h, pubsub.WithPeerScore(params, thresholds)); err != nil {
		t.Fatalf("peer score params rejected by gossipsub: %v", err)
	}
	if len(params.Topics) != 6 {
		t.Fatalf("expected score params for 6 topics, got %d", len(params.Topics))
	}
	// gossipsub divides by TimeInMeshQuantum for every in-mesh peer even when
	// P1 is weighted 0; a zero quantum panics the router (integer divide).
	for topic, tp := range params.Topics {
		if tp.TimeInMeshQuantum <= 0 {
			t.Fatalf("topic %s: TimeInMeshQuantum must be > 0", topic)
		}
	}
}

// Single-node behaviour: a node with no peers can still publish (and receive
// on its own subscription) payloads larger than the per-topic ceilings, so a
// loopback-only producer is unaffected by the inbound limits.
func TestSetupLibP2PWithOptions_LocalPublishUnaffected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n, err := SetupLibP2PWithOptions(ctx, logging.NewSilentLogger(), SetupOptions{BindAddress: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	if n.Limits().Disabled || n.Limits().MaxConns != DefaultP2PMaxConns {
		t.Fatalf("defaults not applied: %+v", n.Limits())
	}

	bigTx := make([]byte, 600<<10) // > 256 KiB tx ceiling
	if err := n.Broadcast(bigTx); err != nil {
		t.Fatalf("local tx publish above the inbound ceiling must succeed: %v", err)
	}
	bt, bs, err := n.JoinTopic(BFTTopicName)
	if err != nil {
		t.Fatal(err)
	}
	bigBFT := make([]byte, 900<<10)
	if err := bt.Publish(ctx, bigBFT); err != nil {
		t.Fatalf("local BFT publish must succeed: %v", err)
	}
	msg, err := bs.Next(ctx)
	if err != nil || len(msg.Data) != len(bigBFT) {
		t.Fatalf("own subscription did not receive local publish: %v", err)
	}
	st := n.GossipGuardStats()
	if st.RejectedOversize != 0 || st.RateLimited != 0 {
		t.Fatalf("guard must not act on local messages: %+v", st)
	}
}

func TestSetupLibP2PWithOptions_InvalidLimitsRefused(t *testing.T) {
	_, err := SetupLibP2PWithOptions(context.Background(), logging.NewSilentLogger(),
		SetupOptions{BindAddress: "127.0.0.1", Limits: P2PLimits{ConnLowWater: 10, ConnHighWater: 5}})
	if err == nil {
		t.Fatal("expected invalid limits to be refused")
	}
}

func TestSetupLibP2PWithOptions_DisabledKeepsLibraryDefaults(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n, err := SetupLibP2PWithOptions(ctx, logging.NewSilentLogger(), SetupOptions{BindAddress: "127.0.0.1", Limits: P2PLimits{Disabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	if n.guard != nil {
		t.Fatal("disabled limits must not install the gossip guard")
	}
}

// Two hosts: an oversized payload from a remote peer is rejected by the
// receiver's guard, an in-limit payload is delivered.
func TestSetupLibP2PWithOptions_RemoteOversizeRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("two-host libp2p pubsub test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	const topic = "qsdm-p2p-limits-test"
	lim := P2PLimits{GossipTopicMaxBytes: map[string]int{topic: 1024}}
	a, err := SetupLibP2PWithOptions(ctx, logging.NewSilentLogger(), SetupOptions{BindAddress: "127.0.0.1", Limits: lim})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := SetupLibP2PWithOptions(ctx, logging.NewSilentLogger(), SetupOptions{BindAddress: "127.0.0.1", Limits: lim})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	ta, sa, err := a.JoinTopic(topic)
	if err != nil {
		t.Fatal(err)
	}
	tb, _, err := b.JoinTopic(topic)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Host.Connect(ctx, peer.AddrInfo{ID: a.Host.ID(), Addrs: a.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	// Wait until BOTH sides know the other's subscription; a message sent
	// before the receiver has registered the sender is dropped by gossipsub
	// itself (independent of the P2P limits).
	for len(tb.ListPeers()) == 0 || len(ta.ListPeers()) == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("topic peering never formed")
		case <-time.After(50 * time.Millisecond):
		}
	}
	time.Sleep(500 * time.Millisecond)

	if err := tb.Publish(ctx, make([]byte, 4096)); err != nil {
		t.Fatalf("sender's own oversize publish must pass its local guard: %v", err)
	}
	small := []byte("ok-small-payload")
	got := make(chan error, 1)
	go func() {
		for {
			msg, err := sa.Next(ctx)
			if err != nil {
				got <- err
				return
			}
			if len(msg.Data) > 1024 {
				got <- fmt.Errorf("oversized payload (%d bytes) was delivered", len(msg.Data))
				return
			}
			if string(msg.Data) == string(small) {
				got <- nil
				return
			}
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	if err := tb.Publish(ctx, small); err != nil {
		t.Fatal(err)
	}
wait:
	for {
		select {
		case err := <-got:
			if err != nil {
				t.Fatalf("in-limit payload: %v (receiver guard %+v)", err, a.GossipGuardStats())
			}
			break wait
		case <-tick.C:
			// gossipsub dedupes by (from, seqno); a re-publish is a new message.
			_ = tb.Publish(ctx, small)
		}
	}
	// The first oversize publish can go out before the mesh is ready and be
	// dropped by gossipsub itself, so publish it again until the receiver's
	// guard has counted a rejection.
	republishUntil(t, ctx, "the receiver guard to record the oversize rejection",
		func() { _ = tb.Publish(ctx, make([]byte, 4096)) },
		func() bool { return a.GossipGuardStats().RejectedOversize > 0 })
}

// republishUntil calls publish every 500 ms until cond holds or ctx ends.
// GossipSub can drop a message published right after two hosts connect, so
// a live test that waits for one delivery must publish again.
func republishUntil(t *testing.T, ctx context.Context, what string, publish func(), cond func() bool) {
	t.Helper()
	next := time.Now()
	for !cond() {
		if !time.Now().Before(next) {
			publish()
			next = time.Now().Add(500 * time.Millisecond)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timeout waiting for %s", what)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Configured bootstrap peers are protected from connection-manager trimming.
func TestBootstrapPeersProtectedFromTrimming(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	n, err := SetupLibP2PWithOptions(ctx, logging.NewSilentLogger(), SetupOptions{BindAddress: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	other, err := libp2p.New(libp2p.NoListenAddrs)
	if err != nil {
		t.Fatal(err)
	}
	pid := other.ID()
	_ = other.Close()
	bd, err := NewBootstrapDiscovery(ctx, n.Host, BootstrapConfig{
		BootstrapPeers: []string{"/ip4/127.0.0.1/tcp/1/p2p/" + pid.String()},
	}, logging.NewSilentLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()
	if !n.Host.ConnManager().IsProtected(pid, BootstrapProtectTag) {
		t.Fatal("bootstrap peer not protected in the connection manager")
	}
}
