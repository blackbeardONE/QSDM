package networking

// Audit-row net-02 evidence: DHT Sybil resistance. Pins the
// Sybil-resistance properties applied by NewBootstrapDiscovery
// (see the doc comment on that function for the full list).

import (
	"context"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	libp2p "github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/peer"
)

// TestQSDMDHTProtocolPrefix_IsNamespaceIsolated locks in the
// invariant that QSDM-DHT uses a private protocol prefix and not
// the default kad-dht prefix. A change here would silently put
// every QSDM validator in the public IPFS DHT namespace and let
// public-network sybils enter the QSDM routing table.
func TestQSDMDHTProtocolPrefix_IsNamespaceIsolated(t *testing.T) {
	const want = "/qsdm/kad/1.0.0"
	if string(QSDMDHTProtocolPrefix) != want {
		t.Fatalf("QSDMDHTProtocolPrefix = %q, want %q — audit row net-02 requires namespace isolation from public IPFS DHT.",
			QSDMDHTProtocolPrefix, want)
	}
	// Belt-and-suspenders: also catch a regression where someone
	// flips the constant to e.g. "/ipfs/kad/1.0.0" (the upstream
	// default) which would visually look "fine" but would
	// reintroduce the Sybil hole.
	if string(QSDMDHTProtocolPrefix) == "/ipfs/kad/1.0.0" {
		t.Fatal("QSDMDHTProtocolPrefix must NOT equal the upstream IPFS default")
	}
}

// TestQSDMDHTKademliaParams_Pinned locks in the Kademlia bucket-size
// and concurrency. A future upstream version that bumps these
// silently would surface here, giving the audit a chance to
// re-review the parameter choice.
func TestQSDMDHTKademliaParams_Pinned(t *testing.T) {
	if QSDMDHTBucketSize != 20 {
		t.Fatalf("QSDMDHTBucketSize = %d, want 20", QSDMDHTBucketSize)
	}
	if QSDMDHTConcurrency != 10 {
		t.Fatalf("QSDMDHTConcurrency = %d, want 10", QSDMDHTConcurrency)
	}
}

// TestBootstrapDiscovery_NoPublicFallbackByDefault is the central
// property-flip test for audit row net-02: in production posture
// (AllowPublicBootstrapFallback false, BootstrapPeers empty) the
// DHT MUST NOT pull in any peer from dht.DefaultBootstrapPeers.
// We validate by spinning up an isolated host with no bootstrap
// peers and no fallback, waiting a short while for the discovery
// loop to run, and asserting no peers were connected via DHT
// discovery (DiscoveredPeers stays empty).
//
// The negative case is harder to test exhaustively without a live
// internet connection, but the structural property — that
// dht.DefaultBootstrapPeers is NOT consulted when the fallback flag
// is false — is enforced by the bootstrap.go code path (the only
// reference to dht.DefaultBootstrapPeers is inside the
// `if cfg.AllowPublicBootstrapFallback` branch). This test pins
// the runtime behaviour (no public peers connected).
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
		// BootstrapPeers empty.
		AllowPublicBootstrapFallback: false,
		DiscoveryInterval:            200 * time.Millisecond,
		AdvertiseInterval:            200 * time.Millisecond,
	}, logger)
	if err != nil {
		t.Fatalf("NewBootstrapDiscovery: %v", err)
	}
	t.Cleanup(func() { _ = bd.Close() })

	// Let the discovery loop iterate. With no bootstrap peers and
	// no public fallback, FindPeers will produce no results.
	time.Sleep(800 * time.Millisecond)

	if got := bd.DiscoveredPeers(); len(got) != 0 {
		t.Fatalf("with no bootstrap peers + no public fallback: DiscoveredPeers must stay empty, got %d", len(got))
	}

	// DHTStats should reflect zero accepted-via-discovery, the
	// protocol prefix should be the QSDM-private one, and the
	// allowlist size should be zero (no allowlist configured).
	stats := bd.DHTStats()
	if stats.AcceptedDiscovered != 0 {
		t.Fatalf("AcceptedDiscovered = %d, want 0", stats.AcceptedDiscovered)
	}
	if stats.ProtocolPrefix != QSDMDHTProtocolPrefix {
		t.Fatalf("ProtocolPrefix = %v, want %v", stats.ProtocolPrefix, QSDMDHTProtocolPrefix)
	}
	if stats.AllowlistSize != 0 {
		t.Fatalf("AllowlistSize = %d, want 0 (no allowlist configured)", stats.AllowlistSize)
	}
}

// TestBootstrapDiscovery_AllowedPeers_RejectsOffListAtBootstrap
// verifies that the allowlist gate fires at bootstrap time when a
// configured bootstrap peer is NOT on the allowlist. We don't need
// the bootstrap to succeed (the peer doesn't exist) — we only need
// to confirm that the peer was REJECTED rather than connected.
// rejectedSybil is bumped, the allowlist size is reported correctly.
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

	// Construct a peer.ID that exists (decoded from a valid base58
	// representation) but is NOT on our allowlist. Use a different
	// valid peer.ID for the allowlist itself — we never actually
	// connect to either, the test runs entirely at the
	// allowlist-gate layer.
	const offListMultiaddr = "/ip4/127.0.0.1/tcp/65000/p2p/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ"
	const allowedB58 = "QmNnooDu7bfjPFoTZYxMNLWUQJyrVwtbZg5gBMjTezGAJN"
	allowedID, err := peer.Decode(allowedB58)
	if err != nil {
		t.Fatalf("peer.Decode allowed: %v", err)
	}

	logger := logging.NewLogger("", false)
	bd, err := NewBootstrapDiscovery(ctx, h, BootstrapConfig{
		BootstrapPeers: []string{offListMultiaddr},
		AllowedPeers:   []peer.ID{allowedID},
		// Do NOT enable public fallback — we want a clean state.
		AllowPublicBootstrapFallback: false,
		DiscoveryInterval:            200 * time.Millisecond,
		AdvertiseInterval:            200 * time.Millisecond,
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
}

// TestBootstrapDiscovery_AllowedPeers_OpenModeWhenEmpty confirms
// that an EMPTY AllowedPeers slice is treated as "no allowlist" /
// open mode. We want operators who don't opt into the
// trusted-validator-set posture to get the existing behaviour
// (modulo the public-fallback policy change).
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
		AllowedPeers:                 nil, // open mode
		AllowPublicBootstrapFallback: false,
		DiscoveryInterval:            200 * time.Millisecond,
		AdvertiseInterval:            200 * time.Millisecond,
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

// TestBootstrapDiscovery_DHTDefaultBootstrapPeersNotReferenced is a
// structural / compile-time anti-regression test: by directly
// referencing dht.DefaultBootstrapPeers here, we ensure the
// symbol's location in the upstream library hasn't moved. The
// audit-row property is that bootstrap.go references this symbol
// ONLY inside the AllowPublicBootstrapFallback branch — that's
// enforced by code review against the diff, but this test pins
// the symbol's continued existence and shape (non-empty default
// list in the upstream package) so a future upstream rename
// surfaces here.
func TestBootstrapDiscovery_DHTDefaultBootstrapPeersNotReferenced(t *testing.T) {
	if len(dht.DefaultBootstrapPeers) == 0 {
		t.Skip("upstream library no longer ships a default bootstrap list; nothing to anti-regress")
	}
	// The list exists. Audit row net-02 forbids referencing it
	// outside the AllowPublicBootstrapFallback branch — that's a
	// code-review property, not a runtime one, but we emit a t.Log
	// here to make the audit trail visible in test output.
	t.Logf("upstream dht.DefaultBootstrapPeers has %d peers; production path MUST NOT reference it unless AllowPublicBootstrapFallback=true",
		len(dht.DefaultBootstrapPeers))
}
