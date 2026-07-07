package networking

// Audit-row net-02 evidence: explicit bootstrap peer admission. Pins the
// post-Kad-DHT posture used by NewBootstrapDiscovery.

import (
	"context"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestQSDMBootstrapProtocolID_StaticBootstrap(t *testing.T) {
	const want = "/qsdm/static-bootstrap/1.0.0"
	if string(QSDMBootstrapProtocolID) != want {
		t.Fatalf("QSDMBootstrapProtocolID = %q, want %q", QSDMBootstrapProtocolID, want)
	}
}

// TestBootstrapDiscovery_NoPublicFallbackByDefault is the central
// property-flip test for audit row net-02: with no configured bootstrap peers,
// the node MUST NOT consult any public DHT fallback. It should simply run in
// isolation and report no discovered peers.
func TestBootstrapDiscovery_NoPublicFallbackByDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("libp2p host setup is slow")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	logger := logging.NewLogger("", false)
	bd, err := NewBootstrapDiscovery(ctx, h, BootstrapConfig{
		AllowPublicBootstrapFallback: false,
		DiscoveryInterval:            200 * time.Millisecond,
	}, logger)
	if err != nil {
		t.Fatalf("NewBootstrapDiscovery: %v", err)
	}
	t.Cleanup(func() { _ = bd.Close() })

	time.Sleep(500 * time.Millisecond)

	if got := bd.DiscoveredPeers(); len(got) != 0 {
		t.Fatalf("with no bootstrap peers: DiscoveredPeers must stay empty, got %d", len(got))
	}

	stats := bd.DHTStats()
	if stats.AcceptedDiscovered != 0 {
		t.Fatalf("AcceptedDiscovered = %d, want 0", stats.AcceptedDiscovered)
	}
	if stats.ProtocolPrefix != QSDMBootstrapProtocolID {
		t.Fatalf("ProtocolPrefix = %v, want %v", stats.ProtocolPrefix, QSDMBootstrapProtocolID)
	}
	if stats.AllowlistSize != 0 {
		t.Fatalf("AllowlistSize = %d, want 0 (no allowlist configured)", stats.AllowlistSize)
	}
}

// TestBootstrapDiscovery_AllowedPeers_RejectsOffListAtBootstrap verifies that
// the allowlist gate fires before dialing when a configured bootstrap peer is
// not on the allowlist.
func TestBootstrapDiscovery_AllowedPeers_RejectsOffListAtBootstrap(t *testing.T) {
	if testing.Short() {
		t.Skip("libp2p host setup is slow")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	const offListMultiaddr = "/ip4/127.0.0.1/tcp/65000/p2p/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ"
	const allowedB58 = "QmNnooDu7bfjPFoTZYxMNLWUQJyrVwtbZg5gBMjTezGAJN"
	allowedID, err := peer.Decode(allowedB58)
	if err != nil {
		t.Fatalf("peer.Decode allowed: %v", err)
	}

	logger := logging.NewLogger("", false)
	bd, err := NewBootstrapDiscovery(ctx, h, BootstrapConfig{
		BootstrapPeers:    []string{offListMultiaddr},
		AllowedPeers:      []peer.ID{allowedID},
		DiscoveryInterval: 200 * time.Millisecond,
	}, logger)
	if err != nil {
		t.Fatalf("NewBootstrapDiscovery: %v", err)
	}
	t.Cleanup(func() { _ = bd.Close() })

	stats := bd.DHTStats()
	if stats.RejectedSybil < 1 {
		t.Fatalf("expected at least 1 RejectedSybil from the off-list bootstrap peer; got %d", stats.RejectedSybil)
	}
	if stats.AllowlistSize != 1 {
		t.Fatalf("AllowlistSize = %d, want 1", stats.AllowlistSize)
	}
	if stats.AcceptedDiscovered != 0 {
		t.Fatalf("AcceptedDiscovered = %d, want 0", stats.AcceptedDiscovered)
	}
}

// TestBootstrapDiscovery_AllowedPeers_OpenModeWhenEmpty confirms that an empty
// AllowedPeers slice is treated as "no allowlist" / open mode.
func TestBootstrapDiscovery_AllowedPeers_OpenModeWhenEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("libp2p host setup is slow")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	logger := logging.NewLogger("", false)
	bd, err := NewBootstrapDiscovery(ctx, h, BootstrapConfig{
		AllowedPeers:      nil,
		DiscoveryInterval: 200 * time.Millisecond,
	}, logger)
	if err != nil {
		t.Fatalf("NewBootstrapDiscovery: %v", err)
	}
	t.Cleanup(func() { _ = bd.Close() })

	stats := bd.DHTStats()
	if stats.AllowlistSize != 0 {
		t.Fatalf("AllowlistSize = %d, want 0 in open mode", stats.AllowlistSize)
	}
	if stats.RejectedSybil != 0 {
		t.Fatalf("RejectedSybil = %d, want 0 in open mode (nothing to reject)", stats.RejectedSybil)
	}
}
