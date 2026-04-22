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
	drouting "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	dutil "github.com/libp2p/go-libp2p/p2p/discovery/util"
	ma "github.com/multiformats/go-multiaddr"
)

const qsdmRendezvous = "qsdm/mesh/1.0"

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
}

// NewBootstrapDiscovery creates a Kademlia DHT discovery layer and bootstraps.
func NewBootstrapDiscovery(ctx context.Context, h host.Host, cfg BootstrapConfig, logger *logging.Logger) (*BootstrapDiscovery, error) {
	bctx, cancel := context.WithCancel(ctx)

	mode := dht.ModeAutoServer
	kademlia, err := dht.New(bctx, h, dht.Mode(mode))
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
		peers = dht.DefaultBootstrapPeers
	}

	var wg sync.WaitGroup
	for _, pAddr := range peers {
		pi, pErr := peer.AddrInfoFromP2pAddr(pAddr)
		if pErr != nil {
			logger.Warn("Invalid bootstrap peer address", "addr", pAddr.String(), "error", pErr)
			continue
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
		host:       h,
		dht:        kademlia,
		routing:    rd,
		logger:     logger,
		rendezvous: rendezvous,
		advInt:     advInt,
		discInt:    discInt,
		ctx:        bctx,
		cancel:     cancel,
		discovered: make(map[peer.ID]struct{}),
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
