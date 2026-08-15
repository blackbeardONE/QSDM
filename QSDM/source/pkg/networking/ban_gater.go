package networking

import (
	"sync"

	"github.com/libp2p/go-libp2p/core/control"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// banGater refuses transport connections to and from banned peers.
//
// Until this existed the ban was advisory: each of the four gossip ingresses
// dropped a banned peer's payloads individually, but the connection stayed
// open, the peer stayed in the peerstore, and it kept costing bandwidth,
// stream slots and pubsub fan-out. Every new ingress had to remember to check
// again, and one that forgot re-opened the hole -- which is exactly what
// happened to POL gossip, and to the tx path before that.
//
// This moves the decision to the transport, where it applies once and cannot
// be forgotten by a later ingress.
//
// The tracker is attached AFTER construction because the host is built before
// the reputation tracker exists in cmd/qsdm. A nil tracker gates nothing, so
// the gater is inert until wired -- which is the safe direction: a node that
// never calls SetTracker behaves exactly as it did before this file.
type banGater struct {
	mu  sync.RWMutex
	rep *ReputationTracker
}

// SetTracker attaches (or clears) the tracker whose bans this gater enforces.
func (g *banGater) SetTracker(rt *ReputationTracker) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rep = rt
}

func (g *banGater) banned(p peer.ID) bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	rep := g.rep
	g.mu.RUnlock()
	return rep != nil && rep.IsBanned(p.String())
}

// InterceptPeerDial blocks outbound dials to a banned peer.
func (g *banGater) InterceptPeerDial(p peer.ID) bool { return !g.banned(p) }

// InterceptAddrDial blocks a dial to a specific address of a banned peer.
func (g *banGater) InterceptAddrDial(p peer.ID, _ multiaddr.Multiaddr) bool { return !g.banned(p) }

// InterceptAccept allows every inbound connection at the raw-socket stage.
//
// The remote peer's identity is not known until the security handshake
// completes, so there is nothing to match a ban against here. Rejecting on
// address alone would ban whole NATs and shared hosts. The real check is in
// InterceptSecured.
func (g *banGater) InterceptAccept(_ network.ConnMultiaddrs) bool { return true }

// InterceptSecured blocks a connection in either direction once the peer's
// identity is known. This is the check that matters for inbound traffic.
func (g *banGater) InterceptSecured(_ network.Direction, p peer.ID, _ network.ConnMultiaddrs) bool {
	return !g.banned(p)
}

// InterceptUpgraded allows anything that survived InterceptSecured.
func (g *banGater) InterceptUpgraded(_ network.Conn) (bool, control.DisconnectReason) {
	return true, 0
}

// SetReputationGate wires a tracker into the transport-level ban gate and
// arranges for an existing connection to be closed the moment its peer is
// banned.
//
// Two halves, and both are needed:
//
//   - The gater refuses FUTURE connections. On its own it would let an
//     already-connected peer keep its connection for as long as it behaved
//     well enough not to be redialled -- and a peer that is spamming is
//     already connected by definition.
//   - The OnBan hook closes the CURRENT connection. On its own it would not
//     stop the peer reconnecting immediately.
//
// The hook fires outside the tracker's lock (see SetOnBan), because ClosePeer
// can drive disconnect notifications back into code that reads the tracker.
func (n *Network) SetReputationGate(rt *ReputationTracker) {
	if n == nil {
		return
	}
	n.mu.Lock()
	gater := n.banGater
	n.mu.Unlock()
	if gater != nil {
		gater.SetTracker(rt)
	}
	if rt == nil || n.Host == nil {
		return
	}
	host := n.Host
	logger := n.logger
	rt.SetOnBan(func(peerID string) {
		pid, err := peer.Decode(peerID)
		if err != nil {
			// Reputation keys are peer.ID strings everywhere in this package,
			// so a decode failure means a caller scored something else. Do not
			// guess -- a wrong ID here would disconnect an innocent peer.
			if logger != nil {
				logger.Warn("ban gate: peer id not decodable; connection not closed",
					"peer_id", peerID, "error", err.Error())
			}
			return
		}
		if err := host.Network().ClosePeer(pid); err != nil && logger != nil {
			logger.Warn("ban gate: closing banned peer failed",
				"peer_id", peerID, "error", err.Error())
		}
	})
}
