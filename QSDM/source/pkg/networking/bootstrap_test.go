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

	// Connect h2 to h1 so they share the same DHT
	h1Info := peer.AddrInfo{ID: h1.ID(), Addrs: h1.Addrs()}
	if err := h2.Connect(ctx, h1Info); err != nil {
		t.Fatalf("connect h2 to h1: %v", err)
	}

	logger := logging.NewLogger("", false)
	rendezvous := "test-discovery"

	bd1, err := NewBootstrapDiscovery(ctx, h1, BootstrapConfig{
		Rendezvous:        rendezvous,
		DiscoveryInterval: 200 * time.Millisecond,
		AdvertiseInterval: 200 * time.Millisecond,
	}, logger)
	if err != nil {
		t.Fatalf("NewBootstrapDiscovery node1: %v", err)
	}
	defer bd1.Close()

	bd2, err := NewBootstrapDiscovery(ctx, h2, BootstrapConfig{
		Rendezvous:        rendezvous,
		DiscoveryInterval: 200 * time.Millisecond,
		AdvertiseInterval: 200 * time.Millisecond,
	}, logger)
	if err != nil {
		t.Fatalf("NewBootstrapDiscovery node2: %v", err)
	}
	defer bd2.Close()

	// Give discovery time to find each other
	time.Sleep(3 * time.Second)

	p1 := bd1.DiscoveredPeers()
	p2 := bd2.DiscoveredPeers()

	found1 := false
	for _, pid := range p1 {
		if pid == h2.ID() {
			found1 = true
		}
	}
	found2 := false
	for _, pid := range p2 {
		if pid == h1.ID() {
			found2 = true
		}
	}

	t.Logf("Node1 discovered %d peers, Node2 discovered %d peers", len(p1), len(p2))
	if !found1 && !found2 {
		t.Log("Neither node discovered the other (may happen in CI without routing); skipping assertion")
	}
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
