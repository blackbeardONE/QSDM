package networking

import (
	"context"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
)

const qsdmRendezvous = "qsdm/mesh/1.0"

// QSDMBootstrapProtocolID labels the current WAN bootstrap strategy in
// DHTStats. QSDM no longer starts a Kademlia DHT for peer discovery; it only
// dials operator-configured bootstrap peers and relies on pubsub/mDNS after
// connection.
const QSDMBootstrapProtocolID protocol.ID = "/qsdm/static-bootstrap/1.0.0"

// BootstrapConfig controls explicit bootstrap peer dialing.
type BootstrapConfig struct {
	// Static bootstrap peer multiaddrs (e.g. "/ip4/1.2.3.4/tcp/4001/p2p/QmXyz...").
	BootstrapPeers []string
	// Rendezvous is retained for config compatibility. Static bootstrap does
	// not advertise into a global rendezvous table.
	Rendezvous string
	// AdvertiseInterval is retained for config compatibility.
	AdvertiseInterval time.Duration
	// DiscoveryInterval controls how often configured bootstrap peers are retried.
	DiscoveryInterval time.Duration

	// AllowedPeers, when non-empty, restricts outbound bootstrap dialing to
	// only peers whose peer.ID is on this allowlist. Empty list means open mode.
	AllowedPeers []peer.ID

	// AllowPublicBootstrapFallback is kept for backwards config compatibility.
	// The Kad-DHT public fallback has been removed, so this flag is ignored
	// except for a warning.
	AllowPublicBootstrapFallback bool
}

// BootstrapDiscovery dials explicit bootstrap peers and records successful
// peer connections. The type name is kept stable for callers that already use
// NewBootstrapDiscovery.
type BootstrapDiscovery struct {
	host           host.Host
	bootstrapPeers []peer.AddrInfo
	logger         *logging.Logger
	rendezvous     string
	discInt        time.Duration
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.Mutex
	discovered     map[peer.ID]struct{}

	// allowedPeers is the consulted allowlist for peer admission during
	// bootstrap dialing. nil means open mode.
	allowedPeers map[peer.ID]struct{}

	// Names are retained for API compatibility with existing dashboards/tests.
	// In the static bootstrap model, acceptedDiscovered means a configured
	// bootstrap peer was successfully dialed, and rejectedSybil means a
	// configured peer was rejected by the allowlist.
	rejectedSybil      uint64
	acceptedDiscovered uint64
}

// NewBootstrapDiscovery creates the explicit bootstrap dialer.
//
// Security posture:
//  1. No Kad-DHT is created, so QSDM no longer imports the IPFS/Kad-DHT
//     provider-record path covered by GO-2024-3218.
//  2. BootstrapPeers is the only WAN peer source. If it is empty, the node
//     runs isolated until a known peer connects inbound or mDNS finds a local
//     peer.
//  3. AllowedPeers, when non-empty, gates every configured bootstrap peer
//     before any dial is attempted.
func NewBootstrapDiscovery(ctx context.Context, h host.Host, cfg BootstrapConfig, logger *logging.Logger) (*BootstrapDiscovery, error) {
	bctx, cancel := context.WithCancel(ctx)

	if logger == nil {
		logger = logging.NewLogger("", false)
	}
	if cfg.AllowPublicBootstrapFallback {
		logger.Warn("Static bootstrap: public DHT fallback is no longer supported; ignoring AllowPublicBootstrapFallback",
			"audit_row", "net-02")
	}

	rendezvous := cfg.Rendezvous
	if rendezvous == "" {
		rendezvous = qsdmRendezvous
	}
	discInt := cfg.DiscoveryInterval
	if discInt == 0 {
		discInt = 30 * time.Second
	}

	var allowed map[peer.ID]struct{}
	if len(cfg.AllowedPeers) > 0 {
		allowed = make(map[peer.ID]struct{}, len(cfg.AllowedPeers))
		for _, pid := range cfg.AllowedPeers {
			allowed[pid] = struct{}{}
		}
	}

	bd := &BootstrapDiscovery{
		host:         h,
		logger:       logger,
		rendezvous:   rendezvous,
		discInt:      discInt,
		ctx:          bctx,
		cancel:       cancel,
		discovered:   make(map[peer.ID]struct{}),
		allowedPeers: allowed,
	}

	for _, pAddr := range parseBootstrapPeers(cfg.BootstrapPeers) {
		pi, pErr := peer.AddrInfoFromP2pAddr(pAddr)
		if pErr != nil {
			logger.Warn("Invalid bootstrap peer address", "addr", pAddr.String(), "error", pErr)
			continue
		}
		if pi.ID == h.ID() {
			continue
		}
		if allowed != nil {
			if _, ok := allowed[pi.ID]; !ok {
				logger.Warn("Static bootstrap: peer not on AllowedPeers list; skipped",
					"peer", pi.ID.String(), "audit_row", "net-02")
				bd.rejectedSybil++
				continue
			}
		}
		bd.bootstrapPeers = append(bd.bootstrapPeers, *pi)
	}

	if len(cfg.BootstrapPeers) == 0 {
		logger.Warn("Static bootstrap: BootstrapPeers is empty; running in isolation until a peer connects",
			"audit_row", "net-02")
	}

	go bd.discoverLoop()

	return bd, nil
}

func (bd *BootstrapDiscovery) discoverLoop() {
	bd.connectBootstrapPeers()

	ticker := time.NewTicker(bd.discInt)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			bd.connectBootstrapPeers()
		case <-bd.ctx.Done():
			return
		}
	}
}

func (bd *BootstrapDiscovery) connectBootstrapPeers() {
	var wg sync.WaitGroup
	for _, pi := range bd.bootstrapPeers {
		pi := pi
		wg.Add(1)
		go func() {
			defer wg.Done()
			if delay := bootstrapDialDelay(bd.host.ID(), pi.ID); delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-timer.C:
				case <-bd.ctx.Done():
					timer.Stop()
					return
				}
			}
			if bd.host.Network().Connectedness(pi.ID) == network.Connected {
				bd.recordConnectedBootstrapPeer(pi.ID)
				bd.logger.Info("Connected to bootstrap peer", "peer", pi.ID.String())
				return
			}
			cctx, ccancel := context.WithTimeout(bd.ctx, 10*time.Second)
			defer ccancel()
			if err := bd.host.Connect(cctx, pi); err != nil {
				if bd.host.Network().Connectedness(pi.ID) == network.Connected {
					bd.recordConnectedBootstrapPeer(pi.ID)
					bd.logger.Info("Connected to bootstrap peer", "peer", pi.ID.String())
					return
				}
				bd.logger.Debug("Bootstrap peer unreachable", "peer", pi.ID.String(), "error", err.Error())
				return
			}
			bd.recordConnectedBootstrapPeer(pi.ID)
			bd.logger.Info("Connected to bootstrap peer", "peer", pi.ID.String())
		}()
	}
	wg.Wait()
}

func bootstrapDialDelay(local, remote peer.ID) time.Duration {
	if local == "" || remote == "" || local == remote || string(local) < string(remote) {
		return 0
	}
	return 250 * time.Millisecond
}

func (bd *BootstrapDiscovery) recordConnectedBootstrapPeer(pid peer.ID) {
	bd.mu.Lock()
	defer bd.mu.Unlock()
	if _, seen := bd.discovered[pid]; !seen {
		bd.acceptedDiscovered++
	}
	bd.discovered[pid] = struct{}{}
}

// DHTStats returns bootstrap admission counters. The name is kept for
// dashboard/API compatibility.
type DHTStats struct {
	// AcceptedDiscovered is the count of configured bootstrap peers
	// successfully dialed.
	AcceptedDiscovered uint64
	// RejectedSybil is the count of configured bootstrap peers rejected because
	// they were not on the AllowedPeers allowlist.
	RejectedSybil uint64
	// AllowlistSize is the configured allowlist size; 0 means open mode.
	AllowlistSize int
	// ProtocolPrefix identifies the active bootstrap strategy.
	ProtocolPrefix protocol.ID
}

// DHTStats reports the current bootstrap counters. Safe to call concurrently
// with the retry loop.
func (bd *BootstrapDiscovery) DHTStats() DHTStats {
	bd.mu.Lock()
	defer bd.mu.Unlock()
	return DHTStats{
		AcceptedDiscovered: bd.acceptedDiscovered,
		RejectedSybil:      bd.rejectedSybil,
		AllowlistSize:      len(bd.allowedPeers),
		ProtocolPrefix:     QSDMBootstrapProtocolID,
	}
}

// DiscoveredPeers returns the set of peers connected through configured
// bootstrap addresses.
func (bd *BootstrapDiscovery) DiscoveredPeers() []peer.ID {
	bd.mu.Lock()
	defer bd.mu.Unlock()
	out := make([]peer.ID, 0, len(bd.discovered))
	for pid := range bd.discovered {
		out = append(out, pid)
	}
	return out
}

// Close stops the bootstrap retry loop.
func (bd *BootstrapDiscovery) Close() error {
	bd.cancel()
	return nil
}

func parseBootstrapPeers(addrs []string) []ma.Multiaddr {
	var out []ma.Multiaddr
	for _, a := range addrs {
		maddr, err := ma.NewMultiaddr(a)
		if err == nil {
			out = append(out, maddr)
		}
	}
	return out
}
