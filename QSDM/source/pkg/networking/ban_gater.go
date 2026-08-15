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
	mu     sync.RWMutex
	rep    *ReputationTracker
	exempt map[string]struct{}
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

// SetExemptPeers marks peers that must never be refused at the transport.
//
// A ban is permanent for the life of the process: nothing decays it (DecayAll
// moves the score, never the Banned flag) and Unban has no production caller.
// Before this file that was tolerable, because a ban only dropped payloads. Now
// it severs the connection and refuses the redial -- so a false positive
// against the peers a node needs in order to be on the network at all is not a
// degraded node, it is a partitioned one, recoverable only by restarting the
// process to clear in-memory reputation.
//
// Bootstrap peers are therefore exempt from the TRANSPORT gate specifically.
// They are not exempt from anything else: every ingress still consults the same
// ban and still drops their payloads, so a misbehaving bootstrap peer is
// ignored exactly as before, it just cannot lock the node out of the network
// while doing it.
func (g *banGater) SetExemptPeers(peerIDs []string) {
	if g == nil {
		return
	}
	ex := make(map[string]struct{}, len(peerIDs))
	for _, id := range peerIDs {
		if id != "" {
			ex[id] = struct{}{}
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.exempt = ex
}

// isExempt reports whether a peer is protected from the transport gate.
func (g *banGater) isExempt(peerID string) bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.exempt[peerID]
	return ok
}

func (g *banGater) banned(p peer.ID) bool {
	if g == nil {
		return false
	}
	id := p.String()
	g.mu.RLock()
	rep := g.rep
	_, isExempt := g.exempt[id]
	g.mu.RUnlock()
	if isExempt {
		return false
	}
	return rep != nil && rep.IsBanned(id)
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
func (n *Network) SetReputationGate(rt *ReputationTracker, exemptPeerIDs ...string) {
	if n == nil {
		return
	}
	n.mu.Lock()
	gater := n.banGater
	n.mu.Unlock()
	if gater != nil {
		gater.SetTracker(rt)
		gater.SetExemptPeers(exemptPeerIDs)
	}
	if rt == nil || n.Host == nil {
		return
	}
	host := n.Host
	logger := n.logger
	rt.SetOnBan(func(peerID string) {
		if gater != nil && gater.isExempt(peerID) {
			// Exempt at the transport: do not sever the connection either, or
			// the gate would let it straight back and the node would flap.
			return
		}
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

// BootstrapPeerIDs extracts the peer.ID string from each configured bootstrap
// multiaddr, skipping any that do not parse.
//
// Exported so cmd/qsdm can pass the same list it uses for discovery into
// SetReputationGate without re-implementing the parse, and so the exemption is
// derived from the operator's actual bootstrap configuration rather than a
// second, drifting list.
func BootstrapPeerIDs(bootstrapPeers []string) []string {
	var out []string
	for _, pAddr := range parseBootstrapPeers(bootstrapPeers) {
		pi, err := peer.AddrInfoFromP2pAddr(pAddr)
		if err != nil {
			continue
		}
		out = append(out, pi.ID.String())
	}
	return out
}
