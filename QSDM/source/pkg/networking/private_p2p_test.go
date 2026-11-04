package networking

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

func privTestHost(t *testing.T) host.Host {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

type connAddrs struct{ local, remote multiaddr.Multiaddr }

func (c connAddrs) LocalMultiaddr() multiaddr.Multiaddr  { return c.local }
func (c connAddrs) RemoteMultiaddr() multiaddr.Multiaddr { return c.remote }

func pma(t *testing.T, s string) multiaddr.Multiaddr {
	t.Helper()
	a, err := multiaddr.NewMultiaddr(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// Private mode off: the gater keeps its historical behaviour for every peer
// and every address (every node without private mode runs this way).
func TestPrivateGater_offAllowsEverything(t *testing.T) {
	g := &banGater{}
	p := testPeer("anyone")
	if !g.InterceptPeerDial(p) || !g.InterceptSecured(network.DirInbound, p, nil) {
		t.Fatal("private mode off must not gate peers")
	}
	if !g.InterceptAddrDial(p, pma(t, "/ip4/8.8.8.8/tcp/4001")) {
		t.Fatal("private mode off must not gate public addresses")
	}
	if !g.InterceptAccept(connAddrs{remote: pma(t, "/ip4/8.8.8.8/tcp/4001")}) {
		t.Fatal("private mode off must accept public remotes")
	}
}

func TestPrivateGater_onlyListedPeersAndPrivateAddresses(t *testing.T) {
	ok := testPeer("listed")
	bad := testPeer("unlisted")
	g := &banGater{allow: map[peer.ID]struct{}{ok: {}}}

	if !g.InterceptPeerDial(ok) || !g.InterceptSecured(network.DirInbound, ok, nil) ||
		!g.InterceptSecured(network.DirOutbound, ok, nil) {
		t.Fatal("a listed peer must pass")
	}
	if g.InterceptPeerDial(bad) || g.InterceptSecured(network.DirInbound, bad, nil) ||
		g.InterceptSecured(network.DirOutbound, bad, nil) {
		t.Fatal("an unlisted peer must be refused in both directions")
	}
	for _, s := range []string{"/ip4/100.70.1.2/tcp/14001", "/ip4/127.0.0.1/tcp/1", "/ip4/10.0.0.5/tcp/1", "/ip6/fd7a:115c:a1e0::9/tcp/1"} {
		if !g.InterceptAddrDial(ok, pma(t, s)) {
			t.Errorf("listed peer at private %s must be dialable", s)
		}
		if !g.InterceptAccept(connAddrs{remote: pma(t, s)}) {
			t.Errorf("private remote %s must be accepted", s)
		}
	}
	for _, s := range []string{"/ip4/8.8.8.8/tcp/4001", "/ip6/2001:db8::1/tcp/4001", "/dns4/vps.example/tcp/4001",
		"/ip4/100.70.1.2/tcp/14001/p2p/12D3KooWHT2APYkMStuaiLZvto7ZmrPmUag4TStT7MryGn69feum/p2p-circuit"} {
		if g.InterceptAddrDial(ok, pma(t, s)) {
			t.Errorf("address %s must not be dialled in private mode", s)
		}
		if g.InterceptAccept(connAddrs{remote: pma(t, s)}) {
			t.Errorf("remote %s must be refused in private mode", s)
		}
	}
	if g.InterceptAddrDial(bad, pma(t, "/ip4/127.0.0.1/tcp/1")) {
		t.Fatal("an unlisted peer must not be dialled even at a private address")
	}
	if g.InterceptAccept(nil) {
		t.Fatal("private mode must refuse an accept with no address")
	}
}

// The ban exemption for bootstrap peers must never widen the allowlist.
func TestPrivateGater_exemptionDoesNotOverrideAllowlist(t *testing.T) {
	ok := testPeer("listed")
	bad := testPeer("unlisted")
	g := &banGater{allow: map[peer.ID]struct{}{ok: {}}}
	g.SetExemptPeers([]string{bad.String()})
	if g.InterceptSecured(network.DirInbound, bad, nil) {
		t.Fatal("ban exemption must not admit an unlisted peer")
	}
}

func TestSetupPrivate_refusesUnsafeOptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := logging.NewLogger("", false)
	listed := privTestHost(t).ID().String()
	cases := []struct {
		name string
		so   SetupOptions
		want string
	}{
		{"empty bind", SetupOptions{AllowedPeers: []string{listed}}, "bind address is required"},
		{"all ipv4", SetupOptions{BindAddress: "0.0.0.0", AllowedPeers: []string{listed}}, "public bind"},
		{"all ipv6", SetupOptions{BindAddress: "::", AllowedPeers: []string{listed}}, "public bind"},
		{"public ip", SetupOptions{BindAddress: "8.8.8.8", AllowedPeers: []string{listed}}, "public bind"},
		{"nat", SetupOptions{BindAddress: "127.0.0.1", EnableNAT: true, AllowedPeers: []string{listed}}, "NAT"},
		{"bad id", SetupOptions{BindAddress: "127.0.0.1", AllowedPeers: []string{"12D3KooWnotapeer"}}, "allowed peer"},
	}
	for _, c := range cases {
		n, err := SetupLibP2PWithOptions(ctx, logger, c.so)
		if err == nil {
			_ = n.Close()
			t.Errorf("%s: expected an error", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not mention %q", c.name, err, c.want)
		}
	}
}

// Real hosts on loopback: the private node accepts and dials the listed peer
// only. An unlisted peer can neither connect in nor be dialled out.
func TestSetupPrivate_liveAllowlist(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-host libp2p test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	logger := logging.NewLogger("", false)

	listed := privTestHost(t)
	stranger := privTestHost(t)
	priv, err := SetupLibP2PWithOptions(ctx, logger, SetupOptions{
		BindAddress: "127.0.0.1", AllowedPeers: []string{listed.ID().String()},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = priv.Close() })
	if !priv.PrivateMode() || len(priv.AllowedPeers()) != 1 || priv.AllowedPeers()[0] != listed.ID().String() {
		t.Fatalf("private mode not reported: %v %v", priv.PrivateMode(), priv.AllowedPeers())
	}
	for _, a := range priv.Host.Addrs() {
		if !strings.HasPrefix(a.String(), "/ip4/127.0.0.1/") {
			t.Fatalf("private host listens on %s, want only 127.0.0.1", a)
		}
	}
	privInfo := peer.AddrInfo{ID: priv.Host.ID(), Addrs: priv.Host.Addrs()}

	// Inbound from the listed peer works.
	if err := listed.Connect(ctx, privInfo); err != nil {
		t.Fatalf("listed peer inbound: %v", err)
	}
	waitConnected(t, priv.Host, listed.ID(), true)

	// Inbound from an unlisted peer is refused.
	_ = stranger.Connect(ctx, privInfo)
	waitConnected(t, priv.Host, stranger.ID(), false)

	// Outbound to an unlisted peer is refused by the gater.
	err = priv.Host.Connect(ctx, peer.AddrInfo{ID: stranger.ID(), Addrs: stranger.Addrs()})
	if err == nil {
		t.Fatal("dial to an unlisted peer must fail")
	}
	waitConnected(t, priv.Host, stranger.ID(), false)

	// Outbound to the listed peer works (fresh connection after closing).
	_ = priv.Host.Network().ClosePeer(listed.ID())
	if err := priv.Host.Connect(ctx, peer.AddrInfo{ID: listed.ID(), Addrs: listed.Addrs()}); err != nil {
		t.Fatalf("dial to the listed peer: %v", err)
	}
	waitConnected(t, priv.Host, listed.ID(), true)
}

// waitConnected polls until the peer is connected (want true), or checks after
// a short settle time that it is not (want false).
func waitConnected(t *testing.T, h host.Host, p peer.ID, want bool) {
	t.Helper()
	if !want {
		time.Sleep(300 * time.Millisecond)
		if h.Network().Connectedness(p) == network.Connected {
			t.Fatalf("peer %s must not be connected", p)
		}
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for h.Network().Connectedness(p) != network.Connected {
		if time.Now().After(deadline) {
			t.Fatalf("peer %s did not connect", p)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A rehearsal on loopback: two private nodes that list each other
// (identities fixed up front through persisted host keys, as on the real
// hosts) connect, and a block-topic message published by one is delivered to
// the other through the production GossipSub stack (StrictSign, limits,
// gossip guard). A third, unlisted node cannot join either of them.
func TestSetupPrivate_twoNodeBlockGossip(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-host libp2p gossip test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	logger := logging.NewLogger("", false)
	dir := t.TempDir()

	keyID := func(name string) (string, string) {
		p := filepath.Join(dir, name)
		priv, err := loadOrCreateHostKey(p)
		if err != nil {
			t.Fatal(err)
		}
		id, err := peer.IDFromPrivateKey(priv)
		if err != nil {
			t.Fatal(err)
		}
		return p, id.String()
	}
	prodKey, prodID := keyID("producer_host_key")
	follKey, follID := keyID("follower_host_key")

	producer, err := SetupLibP2PWithOptions(ctx, logger, SetupOptions{
		BindAddress: "127.0.0.1", HostKeyPath: prodKey, AllowedPeers: []string{follID},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = producer.Close() })
	follower, err := SetupLibP2PWithOptions(ctx, logger, SetupOptions{
		BindAddress: "127.0.0.1", HostKeyPath: follKey, AllowedPeers: []string{prodID},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = follower.Close() })
	if producer.Host.ID().String() != prodID || follower.Host.ID().String() != follID {
		t.Fatal("hosts must use the persisted identities")
	}

	pt, err := producer.PubSub.Join(blockTopicNameForLimits)
	if err != nil {
		t.Fatal(err)
	}
	ft, err := follower.PubSub.Join(blockTopicNameForLimits)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := ft.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	// The follower dials the producer, as a bootstrap peer would.
	if err := follower.Host.Connect(ctx, peer.AddrInfo{ID: producer.Host.ID(), Addrs: producer.Host.Addrs()}); err != nil {
		t.Fatalf("follower -> producer: %v", err)
	}
	waitConnected(t, producer.Host, follower.Host.ID(), true)

	stranger := privTestHost(t)
	_ = stranger.Connect(ctx, peer.AddrInfo{ID: producer.Host.ID(), Addrs: producer.Host.Addrs()})
	_ = stranger.Connect(ctx, peer.AddrInfo{ID: follower.Host.ID(), Addrs: follower.Host.Addrs()})
	waitConnected(t, producer.Host, stranger.ID(), false)
	waitConnected(t, follower.Host, stranger.ID(), false)

	payload := []byte("private-p2p-block-payload")
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := pt.Publish(ctx, payload); err != nil {
			t.Fatal(err)
		}
		rctx, rcancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		msg, err := sub.Next(rctx)
		rcancel()
		if err == nil {
			if string(msg.Data) != string(payload) || msg.ReceivedFrom != producer.Host.ID() {
				t.Fatalf("unexpected message from %s: %q", msg.ReceivedFrom, msg.Data)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("block-topic message not delivered over the private link")
		}
	}
}
