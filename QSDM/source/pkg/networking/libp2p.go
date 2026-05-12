package networking

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/monitoring/netmetrics"
	libp2p "github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
)

type DiscoveryNotifee struct {
	h      host.Host
	logger *logging.Logger
}

func (n *DiscoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
	n.logger.Info("Discovered new peer", "peerID", pi.ID.String())
	if err := n.h.Connect(context.Background(), pi); err != nil {
		n.logger.Error("Failed to connect to peer", "peerID", pi.ID.String(), "error", err)
	}
}

type Network struct {
	Host       host.Host
	PubSub     *pubsub.PubSub
	Topic      *pubsub.Topic
	Sub        *pubsub.Subscription
	ctx        context.Context
	cancel     context.CancelFunc
	msgHandler func(msg []byte)
	txGossip   *TxGossipIngress
	mu         sync.Mutex
	logger     *logging.Logger

	peerStatusMu sync.Mutex
	peerStatus   map[peer.ID]time.Time
	peerMsgCount map[peer.ID]int64 // pubsub messages received per peer (qsdm-transactions)
}

// SetupLibP2P creates a libp2p host that listens on an ephemeral port (useful for tests).
// Production callers should use SetupLibP2PWithPort to bind a stable TCP port so
// firewall rules and peer dial strings are deterministic.
func SetupLibP2P(ctx context.Context, logger *logging.Logger) (*Network, error) {
	return SetupLibP2PWithPortAndKey(ctx, logger, 0, "")
}

// SetupLibP2PWithPort creates a libp2p host listening on the given TCP port on all
// IPv4/IPv6 interfaces. Pass 0 to bind an ephemeral port. The host identity is
// ephemeral (generated fresh on every call) — production deploys should use
// SetupLibP2PWithPortAndKey so the libp2p peer.ID is stable across qsdm.service
// restarts (see hostkey.go for the on-disk format).
func SetupLibP2PWithPort(ctx context.Context, logger *logging.Logger, port int) (*Network, error) {
	return SetupLibP2PWithPortAndKey(ctx, logger, port, "")
}

// SetupLibP2PWithPortAndKey creates a libp2p host listening on the given TCP
// port and identified by the libp2p PrivateKey loaded from (or, on first run,
// generated and persisted to) hostKeyPath. An empty hostKeyPath preserves the
// legacy ephemeral-identity behaviour. See pkg/networking/hostkey.go for the
// on-disk file format.
//
// Persisting the host key keeps `peer.ID` stable across qsdm.service restarts
// so:
//   - Pre-restart `trust/attestations/*` rows submitted under the old node_id
//     remain valid across the next 15-minute freshness window.
//   - Bootstrap allowlists and operator metrics don't see the same node as
//     a different peer after each restart.
//   - The /api/v1/status `node_id` field is stable for dashboards and
//     external probes.
func SetupLibP2PWithPortAndKey(ctx context.Context, logger *logging.Logger, port int, hostKeyPath string) (*Network, error) {
	var opts []libp2p.Option
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid libp2p port: %d", port)
	}
	opts = append(opts, libp2p.ListenAddrStrings(
		fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port),
		fmt.Sprintf("/ip6/::/tcp/%d", port),
	))
	if hostKeyPath != "" {
		priv, kerr := loadOrCreateHostKey(hostKeyPath)
		if kerr != nil {
			return nil, fmt.Errorf("libp2p host key: %w", kerr)
		}
		if priv != nil {
			opts = append(opts, libp2p.Identity(priv))
		}
	}
	h, err := libp2p.New(opts...)
	if err != nil {
		return nil, err
	}

	// Setup mDNS discovery to find local peers
	notifee := &DiscoveryNotifee{h: h, logger: logger}
	mdnsService := mdns.NewMdnsService(h, "qsdm-mdns", notifee)
	if mdnsService == nil {
		return nil, fmt.Errorf("failed to start mDNS service")
	}

	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		return nil, fmt.Errorf("failed to create pubsub: %w", err)
	}

	topic, err := ps.Join("qsdm-transactions")
	if err != nil {
		return nil, fmt.Errorf("failed to join pubsub topic: %w", err)
	}

	sub, err := topic.Subscribe()
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to pubsub topic: %w", err)
	}

	networkCtx, cancel := context.WithCancel(ctx)

	net := &Network{
		Host:         h,
		PubSub:       ps,
		Topic:        topic,
		Sub:          sub,
		ctx:          networkCtx,
		cancel:       cancel,
		logger:       logger,
		peerStatus:   make(map[peer.ID]time.Time),
		peerMsgCount: make(map[peer.ID]int64),
	}

	// Wire the live network into the monitoring layer so
	// qsdm_p2p_peers_connected{provider="live"} reflects
	// this host's peer count at scrape time. Goes through
	// the netmetrics leaf to avoid an import cycle with
	// pkg/monitoring (which itself imports pkg/networking
	// for TopologyMonitor).
	netmetrics.RegisterNetworkProvider(net)

	go net.handleMessages()
	go net.monitorPeers()

	logger.Info("LibP2P host created", "hostID", h.ID().String())
	return net, nil
}

// PeerCount implements netmetrics.NetworkProvider for this
// host. Counts only fully-connected libp2p peers (matches the
// semantic the alerts in qsdm-p2p assume).
func (n *Network) PeerCount() int {
	if n == nil || n.Host == nil {
		return 0
	}
	return len(n.Host.Network().Peers())
}

func (n *Network) handleMessages() {
	for {
		msg, err := n.Sub.Next(n.ctx)
		if err != nil {
			if n.ctx.Err() != nil {
				return
			}
			n.logger.Error("Error reading pubsub message", "error", err)
			continue
		}
		if msg.ReceivedFrom == n.Host.ID() {
			continue // Ignore messages from self
		}
		// Increment qsdm_p2p_messages_total{direction="in"}
		// for every non-self pubsub message — gives the
		// scrape a publish-rate signal for gossip-stall
		// alerts.
		netmetrics.RecordGossipMessage(netmetrics.DirectionIn)
		n.peerStatusMu.Lock()
		n.peerStatus[msg.ReceivedFrom] = time.Now()
		n.peerMsgCount[msg.ReceivedFrom]++
		n.peerStatusMu.Unlock()

		n.mu.Lock()
		txIng := n.txGossip
		handler := n.msgHandler
		n.mu.Unlock()
		if txIng != nil && txIng.TryConsumeGossip(msg.ReceivedFrom.String(), msg.Data) {
			continue
		}
		n.mu.Lock()
		if handler != nil {
			handler(msg.Data)
		}
		n.mu.Unlock()
	}
}

func (n *Network) SetMessageHandler(handler func(msg []byte)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.msgHandler = handler
}

// SetTxGossipIngress optionally routes qsdm-transactions pubsub payloads through signed-tx
// gossip validation first; accepted or quarantined payloads skip the legacy message handler.
func (n *Network) SetTxGossipIngress(ing *TxGossipIngress) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.txGossip = ing
}

func (n *Network) Broadcast(msg []byte) error {
	if err := n.Topic.Publish(n.ctx, msg); err != nil {
		return err
	}
	// Only count successful publishes — the counter then
	// reflects payloads the gossip layer actually accepted,
	// not those that errored out at the libp2p boundary.
	netmetrics.RecordGossipMessage(netmetrics.DirectionOut)
	return nil
}

func (n *Network) Close() error {
	n.cancel()
	return n.Host.Close()
}

// JoinTopic joins a new pubsub topic and returns the topic handle and subscription.
// Callers are responsible for reading from the subscription in a goroutine.
func (n *Network) JoinTopic(name string) (*pubsub.Topic, *pubsub.Subscription, error) {
	t, err := n.PubSub.Join(name)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to join topic %s: %w", name, err)
	}
	s, err := t.Subscribe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to subscribe to topic %s: %w", name, err)
	}
	return t, s, nil
}

// monitorPeers periodically checks peer connectivity and attempts reconnection.
func (n *Network) monitorPeers() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			n.peerStatusMu.Lock()
			for pid, lastSeen := range n.peerStatus {
				if time.Since(lastSeen) > 1*time.Minute {
					n.logger.Warn("Peer inactive, attempting reconnect", "peerID", pid.String())
					pi := peer.AddrInfo{ID: pid}
					err := n.Host.Connect(n.ctx, pi)
					if err != nil {
						n.logger.Error("Failed to reconnect to peer", "peerID", pid.String(), "error", err)
					} else {
						n.logger.Info("Reconnected to peer", "peerID", pid.String())
						n.peerStatus[pid] = time.Now()
					}
				}
			}
			n.peerStatusMu.Unlock()
		case <-n.ctx.Done():
			return
		}
	}
}

// UpdatePeerStatus updates the last seen time for a peer.
func (n *Network) UpdatePeerStatus(pid peer.ID) {
	n.peerStatusMu.Lock()
	defer n.peerStatusMu.Unlock()
	n.peerStatus[pid] = time.Now()
}

// PeerInfo contains information about a peer
type PeerInfo struct {
	ID        string        `json:"id"`
	Addresses []string      `json:"addresses"`
	LastSeen  time.Time     `json:"last_seen"`
	Connected bool          `json:"connected"`
	Latency   time.Duration `json:"latency,omitempty"`
}

// ConnectionQuality represents the quality of a peer connection
type ConnectionQuality struct {
	PeerID       string        `json:"peer_id"`
	Latency      time.Duration `json:"latency"`
	LastSeen     time.Time     `json:"last_seen"`
	Status       string        `json:"status"` // "connected", "disconnected", "reconnecting"
	MessageCount int64         `json:"message_count"`
}

// GetPeerInfo returns information about all connected peers
func (n *Network) GetPeerInfo() []PeerInfo {
	n.peerStatusMu.Lock()
	defer n.peerStatusMu.Unlock()

	var peers []PeerInfo
	connectedPeers := n.Host.Network().Peers()

	for _, pid := range connectedPeers {
		info := PeerInfo{
			ID:        pid.String(),
			Addresses: []string{},
			Connected: true,
		}

		// Get addresses
		addrs := n.Host.Network().Peerstore().Addrs(pid)
		for _, addr := range addrs {
			info.Addresses = append(info.Addresses, addr.String())
		}

		// Get last seen time
		if lastSeen, ok := n.peerStatus[pid]; ok {
			info.LastSeen = lastSeen
		}

		peers = append(peers, info)
	}

	// Also include peers we've seen but aren't currently connected
	for pid, lastSeen := range n.peerStatus {
		found := false
		for _, p := range peers {
			if p.ID == pid.String() {
				found = true
				break
			}
		}
		if !found {
			peers = append(peers, PeerInfo{
				ID:        pid.String(),
				Addresses: []string{},
				LastSeen:  lastSeen,
				Connected: false,
			})
		}
	}

	return peers
}

// GetConnectionQuality returns connection quality metrics for all peers
func (n *Network) GetConnectionQuality() []ConnectionQuality {
	n.peerStatusMu.Lock()
	defer n.peerStatusMu.Unlock()

	var qualities []ConnectionQuality
	connectedPeers := n.Host.Network().Peers()

	for _, pid := range connectedPeers {
		lastSeen, ok := n.peerStatus[pid]
		if !ok {
			lastSeen = time.Now()
		}

		status := "connected"
		if time.Since(lastSeen) > 1*time.Minute {
			status = "disconnected"
		} else if time.Since(lastSeen) > 30*time.Second {
			status = "reconnecting"
		}

		qualities = append(qualities, ConnectionQuality{
			PeerID:       pid.String(),
			LastSeen:     lastSeen,
			Status:       status,
			MessageCount: n.peerMsgCount[pid],
		})
	}

	return qualities
}

// GetNetworkTopology returns the network topology for visualization
func (n *Network) GetNetworkTopology() map[string]interface{} {
	peers := n.GetPeerInfo()
	qualities := n.GetConnectionQuality()

	// Build topology graph
	nodes := []map[string]interface{}{}
	edges := []map[string]interface{}{}

	// Add self as central node
	nodes = append(nodes, map[string]interface{}{
		"id":    n.Host.ID().String(),
		"label": "Self",
		"type":  "self",
	})

	// Add peer nodes
	for _, peer := range peers {
		nodeType := "peer"
		if !peer.Connected {
			nodeType = "disconnected"
		}

		nodes = append(nodes, map[string]interface{}{
			"id":    peer.ID,
			"label": peer.ID[:12] + "...", // Shortened ID
			"type":  nodeType,
		})

		// Add edge from self to peer
		edgeStatus := "connected"
		if !peer.Connected {
			edgeStatus = "disconnected"
		}

		edges = append(edges, map[string]interface{}{
			"from":   n.Host.ID().String(),
			"to":     peer.ID,
			"status": edgeStatus,
		})
	}

	return map[string]interface{}{
		"nodes":     nodes,
		"edges":     edges,
		"peerCount": len(peers),
		"connectedCount": func() int {
			count := 0
			for _, p := range peers {
				if p.Connected {
					count++
				}
			}
			return count
		}(),
		"qualities": qualities,
	}
}
