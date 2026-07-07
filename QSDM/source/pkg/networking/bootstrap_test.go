package networking

import (
	"context"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestBootstrapDiscovery_StartsAndCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	defer h.Close()

	logger := logging.NewLogger("", false)

	bd, err := NewBootstrapDiscovery(ctx, h, BootstrapConfig{
		DiscoveryInterval: 500 * time.Millisecond,
		AdvertiseInterval: 500 * time.Millisecond,
	}, logger)
	if err != nil {
		t.Fatalf("NewBootstrapDiscovery: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	peers := bd.DiscoveredPeers()
	if peers == nil {
		t.Fatal("DiscoveredPeers returned nil")
	}

	if err := bd.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestBootstrapDiscovery_TwoNodesDiscover(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host1: %v", err)
	}
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host2: %v", err)
	}
	defer h2.Close()

	h1Info := peer.AddrInfo{ID: h1.ID(), Addrs: h1.Addrs()}
	h1Addrs, err := peer.AddrInfoToP2pAddrs(&h1Info)
	if err != nil {
		t.Fatalf("h1 p2p addrs: %v", err)
	}
	h2Info := peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()}
	h2Addrs, err := peer.AddrInfoToP2pAddrs(&h2Info)
	if err != nil {
		t.Fatalf("h2 p2p addrs: %v", err)
	}

	logger := logging.NewLogger("", false)
	rendezvous := "test-discovery"

	bd1, err := NewBootstrapDiscovery(ctx, h1, BootstrapConfig{
		BootstrapPeers:    []string{h2Addrs[0].String()},
		Rendezvous:        rendezvous,
		DiscoveryInterval: 200 * time.Millisecond,
		AdvertiseInterval: 200 * time.Millisecond,
	}, logger)
	if err != nil {
		t.Fatalf("NewBootstrapDiscovery node1: %v", err)
	}
	defer bd1.Close()

	bd2, err := NewBootstrapDiscovery(ctx, h2, BootstrapConfig{
		BootstrapPeers:    []string{h1Addrs[0].String()},
		Rendezvous:        rendezvous,
		DiscoveryInterval: 200 * time.Millisecond,
		AdvertiseInterval: 200 * time.Millisecond,
	}, logger)
	if err != nil {
		t.Fatalf("NewBootstrapDiscovery node2: %v", err)
	}
	defer bd2.Close()

	deadline := time.Now().Add(5 * time.Second)
	var p1, p2 []peer.ID
	var found1, found2 bool
	for time.Now().Before(deadline) {
		p1 = bd1.DiscoveredPeers()
		p2 = bd2.DiscoveredPeers()

		found1 = hasPeerID(p1, h2.ID())
		found2 = hasPeerID(p2, h1.ID())
		if found1 && found2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !found1 {
		t.Fatalf("node1 did not record node2 as a bootstrap peer; discovered=%v", p1)
	}
	if !found2 {
		t.Fatalf("node2 did not record node1 as a bootstrap peer; discovered=%v", p2)
	}
}

func hasPeerID(peers []peer.ID, want peer.ID) bool {
	for _, pid := range peers {
		if pid == want {
			return true
		}
	}
	return false
}

func TestParseBootstrapPeers(t *testing.T) {
	addrs := parseBootstrapPeers([]string{
		"/ip4/127.0.0.1/tcp/4001/p2p/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ",
		"invalid-addr",
		"/ip4/10.0.0.1/tcp/4001/p2p/QmNnooDu7bfjPFoTZYxMNLWUQJyrVwtbZg5gBMjTezGAJN",
	})
	if len(addrs) != 2 {
		t.Fatalf("expected 2 valid addrs, got %d", len(addrs))
	}
}
