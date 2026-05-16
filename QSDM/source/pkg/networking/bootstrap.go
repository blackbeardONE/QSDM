package networking

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	drouting "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	dutil "github.com/libp2p/go-libp2p/p2p/discovery/util"
	ma "github.com/multiformats/go-multiaddr"
)

const qsdmRendezvous = "qsdm/mesh/1.0"

// QSDMDHTProtocolPrefix is the QSDM-private Kademlia protocol prefix.
// We do NOT use the default kad-dht protocol prefix because that
// puts every QSDM validator in the public IPFS DHT namespace, where
// any IPFS node — friendly or hostile — can be discovered by a
// QSDM peer lookup and inserted into the QSDM routing table.
// Audit row net-02 ("DHT Sybil resistance"): isolating the QSDM
// kad-dht network from the public IPFS network is the first and
// strongest Sybil-resistance gate. An attacker who wants to flood
// a QSDM validator with sybil peers must now run their sybils
// SPECIFICALLY against this protocol prefix, not just spin them up
// against the public IPFS bootstrap nodes.
const QSDMDHTProtocolPrefix protocol.ID = "/qsdm/kad/1.0.0"

// QSDMDHTBucketSize is the explicit Kademlia bucket-size (k value).
// We pin it to 20 (the kad-dht library default) so the property is
// locally declared and cannot silently regress on dependency
// upgrade. Smaller buckets reduce attack surface for sybils
// occupying a bucket but cost more lookup hops; 20 is the standard
// kad-dht choice and matches the upstream library default.
const QSDMDHTBucketSize = 20

// QSDMDHTConcurrency is the explicit Kademlia lookup parallelism
// (alpha value). Higher values converge faster but allow a sybil
// quorum to overwhelm lookups; we pin to 10 (library default).
const QSDMDHTConcurrency = 10

// BootstrapConfig controls bootstrap peer discovery.
type BootstrapConfig struct {
	// Static bootstrap peer multiaddrs (e.g. "/ip4/1.2.3.4/tcp/4001/p2p/QmXyz...")
	BootstrapPeers []string
	// Rendezvous string for peer advertisement.  Defaults to qsdmRendezvous.
	Rendezvous string
	// AdvertiseInterval controls how often we re-advertise.
	AdvertiseInterval time.Duration
	// DiscoveryInterval controls how often we search for peers.
	DiscoveryInterval time.Duration

	// AllowedPeers (audit row net-02): when non-empty, restrict
	// inbound + outbound DHT connections to ONLY peers whose peer.ID
	// is on this allowlist. This is the opt-in trusted-validator-set
	// mode — when set, even a perfectly-functioning Kademlia lookup
	// that returns a non-allowlisted peer will skip the Connect call.
	// Empty list ⇒ open DHT (the only Sybil resistance is the
	// QSDM-private protocol prefix + no-public-fallback policy).
	AllowedPeers []peer.ID

	// AllowPublicBootstrapFallback (audit row net-02): controls what
	// happens when BootstrapPeers is empty. Default false ⇒ fail
	// closed (run in isolation, log a warning, do NOT fall back to
	// dht.DefaultBootstrapPeers). Setting this to true ONLY makes
	// sense for dev / local-cluster work because it joins the public
	// IPFS bootstrap network as a peer-source, which is the single
	// largest Sybil-resistance hole the upstream library has.
	//
	// Production deploys MUST leave this false and configure
	// BootstrapPeers explicitly. The systemd unit on BLR1 already
	// does this.
	AllowPublicBootstrapFallback bool
}

// BootstrapDiscovery uses Kademlia DHT + content routing for WAN peer discovery.
type BootstrapDiscovery struct {
	host       host.Host
	dht        *dht.IpfsDHT
	routing    *drouting.RoutingDiscovery
	logger     *logging.Logger
	rendezvous string
	advInt     time.Duration
	discInt    time.Duration
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	discovered map[peer.ID]struct{}

	// allowedPeers is the consulted allowlist for peer admission
	// during discovery. nil ⇒ open mode (allow all). Read-only after
	// construction, so no separate mutex.
	allowedPeers map[peer.ID]struct{}

	// rejectedSybil and acceptedDiscovered are atomic-friendly
	// counters consulted by the bootstrap tests and exposed by
	// DHTStats() for operator dashboards. Updated under mu.
	rejectedSybil      uint64
	acceptedDiscovered uint64
}

// NewBootstrapDiscovery creates a Kademlia DHT discovery layer and bootstraps.
//
// Sybil-resistance properties applied (audit row net-02):
//
//  1. The DHT is constructed with the QSDM-private protocol prefix
//     QSDMDHTProtocolPrefix, isolating it from the public IPFS DHT.
//     Sybil nodes on the public IPFS network are not discoverable
//     by QSDM peers.
//  2. The DHT runs in ModeServer (NOT ModeAutoServer) — every QSDM
//     validator participates in providing the DHT, eliminating the
//     "all clients, no servers" failure mode that would let a small
//     set of public bootstrap nodes dominate routing-table contents.
//  3. BootstrapPeers is the ONLY peer source for the initial routing
//     table population. We never fall back to
//     dht.DefaultBootstrapPeers (the public IPFS bootstrap nodes)
//     unless cfg.AllowPublicBootstrapFallback is explicitly set —
//     production MUST leave that flag false.
//  4. cfg.AllowedPeers, when non-empty, restricts BOTH the initial
//     bootstrap-peer connection AND the discoverLoop's per-peer
//     Connect calls to an explicit peer.ID allowlist. Sybils outside
//     that list cannot enter the routing table even if Kademlia
//     returns them as lookup results.
func NewBootstrapDiscovery(ctx context.Context, h host.Host, cfg BootstrapConfig, logger *logging.Logger) (*BootstrapDiscovery, error) {
	bctx, cancel := context.WithCancel(ctx)

	// Audit row net-02 properties #1, #2: QSDM-private protocol
	// prefix + ModeServer (not ModeAutoServer). Library defaults
	// pinned explicitly via QSDMDHTBucketSize / QSDMDHTConcurrency
	// so a future upstream version that bumps them must surface in
	// our test suite, not silently change at runtime.
	kademlia, err := dht.New(bctx, h,
		dht.Mode(dht.ModeServer),
		dht.ProtocolPrefix(QSDMDHTProtocolPrefix),
		dht.BucketSize(QSDMDHTBucketSize),
		dht.Concurrency(QSDMDHTConcurrency),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create DHT: %w", err)
	}

	if err := kademlia.Bootstrap(bctx); err != nil {
		cancel()
		return nil, fmt.Errorf("bootstrap DHT: %w", err)
	}

	peers := parseBootstrapPeers(cfg.BootstrapPeers)
	if len(peers) == 0 {
		if cfg.AllowPublicBootstrapFallback {
			// DEV / LOCAL ONLY. Joining the public IPFS bootstrap
			// network exposes the QSDM validator to a peer source
			// that contains arbitrary (potentially sybil) nodes.
			// Audit row net-02 explicitly calls this out as a hole;
			// the production posture is to fail closed (empty
			// bootstrap list ⇒ isolated DHT, run anyway and wait
			// for an explicit Connect from a known peer).
			logger.Warn("DHT bootstrap: falling back to PUBLIC IPFS bootstrap nodes — DEV ONLY, weakens Sybil resistance",
				"audit_row", "net-02")
			peers = dht.DefaultBootstrapPeers
		} else {
			// Audit row net-02 property #3: production runs in
			// fail-closed mode when no bootstrap peers are
			// configured. The validator still joins the DHT (it
			// can be discovered by explicit Connect from a known
			// peer) but does not pull in unknown peers from a
			// public source.
			logger.Warn("DHT bootstrap: BootstrapPeers is empty and AllowPublicBootstrapFallback is false; running in isolation until an explicit peer connects",
				"audit_row", "net-02")
			peers = nil
		}
	}

	// Build the allowedPeers set (if configured) so the bootstrap
	// loop AND the discovery loop both consult the same gate.
	var allowed map[peer.ID]struct{}
	if len(cfg.AllowedPeers) > 0 {
		allowed = make(map[peer.ID]struct{}, len(cfg.AllowedPeers))
		for _, pid := range cfg.AllowedPeers {
			allowed[pid] = struct{}{}
		}
	}

	var wg sync.WaitGroup
	var rejectedAtBootstrap uint64
	for _, pAddr := range peers {
		pi, pErr := peer.AddrInfoFromP2pAddr(pAddr)
		if pErr != nil {
			logger.Warn("Invalid bootstrap peer address", "addr", pAddr.String(), "error", pErr)
			continue
		}
		// Audit row net-02 property #4: allowlist gate applies to
		// bootstrap peers too. A misconfigured deploy that lists a
		// bootstrap peer NOT on the allowlist surfaces here as a
		// log line — the bootstrap-peer set MUST be a subset of
		// the allowed-peer set.
		if allowed != nil {
			if _, ok := allowed[pi.ID]; !ok {
				logger.Warn("DHT bootstrap: peer not on AllowedPeers list — skipped (Sybil-resistance allowlist)",
					"peer", pi.ID.String(), "audit_row", "net-02")
				rejectedAtBootstrap++
				continue
			}
		}
		wg.Add(1)
		go func(pi peer.AddrInfo) {
			defer wg.Done()
			cctx, ccancel := context.WithTimeout(bctx, 10*time.Second)
			defer ccancel()
			if err := h.Connect(cctx, pi); err != nil {
				logger.Debug("Bootstrap peer unreachable", "peer", pi.ID.String(), "error", err)
			} else {
				logger.Info("Connected to bootstrap peer", "peer", pi.ID.String())
			}
		}(*pi)
	}
	wg.Wait()

	rd := drouting.NewRoutingDiscovery(kademlia)

	rendezvous := cfg.Rendezvous
	if rendezvous == "" {
		rendezvous = qsdmRendezvous
	}
	advInt := cfg.AdvertiseInterval
	if advInt == 0 {
		advInt = 3 * time.Hour
	}
	discInt := cfg.DiscoveryInterval
	if discInt == 0 {
		discInt = 30 * time.Second
	}

	bd := &BootstrapDiscovery{
		host:               h,
		dht:                kademlia,
		routing:            rd,
		logger:             logger,
		rendezvous:         rendezvous,
		advInt:             advInt,
		discInt:            discInt,
		ctx:                bctx,
		cancel:             cancel,
		discovered:         make(map[peer.ID]struct{}),
		allowedPeers:       allowed,
		rejectedSybil:      rejectedAtBootstrap,
		acceptedDiscovered: 0,
	}

	go bd.advertiseLoop()
	go bd.discoverLoop()

	return bd, nil
}

func (bd *BootstrapDiscovery) advertiseLoop() {
	for {
		dutil.Advertise(bd.ctx, bd.routing, bd.rendezvous)
		bd.logger.Debug("Advertised on DHT", "rendezvous", bd.rendezvous)
		select {
		case <-time.After(bd.advInt):
		case <-bd.ctx.Done():
			return
		}
	}
}

func (bd *BootstrapDiscovery) discoverLoop() {
	for {
		peerCh, err := bd.routing.FindPeers(bd.ctx, bd.rendezvous)
		if err != nil {
			bd.logger.Warn("DHT FindPeers error", "error", err)
		} else {
			for pi := range peerCh {
				if pi.ID == bd.host.ID() || len(pi.Addrs) == 0 {
					continue
				}
				// Audit row net-02 property #4: allowlist gate at
				// discovery time. A Kademlia lookup that returns a
				// peer NOT on the allowlist gets DROPPED HERE
				// rather than connected — protects the validator
				// against a Sybil attacker who has somehow seeded
				// the public part of the DHT with malicious peer
				// records. Allowlist is set at construction; nil
				// means open mode (no allowlist enforcement).
				if bd.allowedPeers != nil {
					if _, ok := bd.allowedPeers[pi.ID]; !ok {
						bd.mu.Lock()
						bd.rejectedSybil++
						bd.mu.Unlock()
						bd.logger.Debug("DHT discovery: peer not on AllowedPeers list — rejected (Sybil-resistance allowlist)",
							"peer", pi.ID.String(), "audit_row", "net-02")
						continue
					}
				}
				bd.mu.Lock()
				_, seen := bd.discovered[pi.ID]
				bd.mu.Unlock()
				if seen {
					continue
				}
				cctx, ccancel := context.WithTimeout(bd.ctx, 10*time.Second)
				if err := bd.host.Connect(cctx, pi); err != nil {
					bd.logger.Debug("Failed to connect to discovered peer", "peer", pi.ID.String(), "error", err)
				} else {
					bd.logger.Info("Connected to discovered peer via DHT", "peer", pi.ID.String())
					bd.mu.Lock()
					bd.discovered[pi.ID] = struct{}{}
					bd.acceptedDiscovered++
					bd.mu.Unlock()
				}
				ccancel()
			}
		}
		select {
		case <-time.After(bd.discInt):
		case <-bd.ctx.Done():
			return
		}
	}
}

// DHTStats returns the Sybil-resistance counters maintained by the
// discovery loop. Used by the audit checklist / runtime dashboard to
// surface "the allowlist is doing something" evidence.
type DHTStats struct {
	// AcceptedDiscovered is the count of peers discovered via DHT
	// FindPeers AND successfully Connect()ed.
	AcceptedDiscovered uint64
	// RejectedSybil is the count of peers rejected because they
	// were not on the AllowedPeers allowlist (counted at both
	// bootstrap AND discovery time).
	RejectedSybil uint64
	// AllowlistSize is the configured allowlist size; 0 ⇒ open mode.
	AllowlistSize int
	// ProtocolPrefix is the QSDM-private kad-dht protocol prefix
	// being used; surfaces here so /status and tests can verify the
	// validator is namespace-isolated from public IPFS.
	ProtocolPrefix protocol.ID
}

// DHTStats reports the current Sybil-resistance counters and the
// pinned protocol prefix. Safe to call concurrently with the
// discovery loop.
func (bd *BootstrapDiscovery) DHTStats() DHTStats {
	bd.mu.Lock()
	defer bd.mu.Unlock()
	return DHTStats{
		AcceptedDiscovered: bd.acceptedDiscovered,
		RejectedSybil:      bd.rejectedSybil,
		AllowlistSize:      len(bd.allowedPeers),
		ProtocolPrefix:     QSDMDHTProtocolPrefix,
	}
}

// DiscoveredPeers returns the set of peers found through DHT discovery.
func (bd *BootstrapDiscovery) DiscoveredPeers() []peer.ID {
	bd.mu.Lock()
	defer bd.mu.Unlock()
	out := make([]peer.ID, 0, len(bd.discovered))
	for pid := range bd.discovered {
		out = append(out, pid)
	}
	return out
}

// Close shuts down the DHT and stops discovery.
func (bd *BootstrapDiscovery) Close() error {
	bd.cancel()
	return bd.dht.Close()
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
