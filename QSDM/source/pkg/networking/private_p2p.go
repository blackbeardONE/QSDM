package networking

import (
	"fmt"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/privatenet"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// Private P2P mode (QSDM/docs/docs/PRIVATE_P2P.md).
//
// When SetupOptions.AllowedPeers is non-empty the host:
//   - must bind one literal private address (validatePrivateListen);
//   - never enables NAT port mapping or hole punching;
//   - runs no mDNS discovery (it would announce the node on every LAN the
//     host is attached to);
//   - gates every connection through banGater: an unlisted peer ID is refused
//     at InterceptSecured (both directions) and InterceptPeerDial/AddrDial,
//     and a non-private remote address is refused at InterceptAccept and
//     InterceptAddrDial.
//
// Empty AllowedPeers leaves all of this off and the host behaves exactly as
// before.

// parseAllowedPeers decodes the configured peer IDs. Every entry must decode;
// a typo must fail the start rather than silently shrink the list.
func parseAllowedPeers(ids []string) (map[peer.ID]struct{}, error) {
	var out map[peer.ID]struct{}
	for i, raw := range ids {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		id, err := peer.Decode(s)
		if err != nil {
			return nil, fmt.Errorf("private p2p: allowed peer [%d] %q: %w", i, s, err)
		}
		if out == nil {
			out = make(map[peer.ID]struct{}, len(ids))
		}
		out[id] = struct{}{}
	}
	return out, nil
}

// validatePrivateListen refuses an all-interfaces or public bind in private
// mode. It repeats the pkg/config check so the networking API is safe on its
// own (tests and any future caller that skips config.Validate).
func validatePrivateListen(bindAddress string) error {
	b := strings.TrimSpace(bindAddress)
	if b == "" {
		return fmt.Errorf("private p2p: a private bind address is required (empty binds every interface)")
	}
	ip := privatenet.ParseIP(b)
	if ip == nil {
		return fmt.Errorf("private p2p: bind address %q is not a literal IP", b)
	}
	if !privatenet.IsPrivate(ip) {
		return fmt.Errorf("private p2p: refusing public bind address %q", b)
	}
	return nil
}

// privateMode reports whether the allowlist is active.
func (g *banGater) privateMode() bool { return g != nil && g.allow != nil }

// peerAllowed reports whether p passes the private allowlist (always true
// when private mode is off).
func (g *banGater) peerAllowed(p peer.ID) bool {
	if !g.privateMode() {
		return true
	}
	_, ok := g.allow[p]
	return ok
}

// addrAllowed reports whether a remote address passes the private mode
// address rule: a literal private IP. Relayed and name-based addresses do not
// pass. Always true when private mode is off.
func (g *banGater) addrAllowed(a multiaddr.Multiaddr) bool {
	if !g.privateMode() {
		return true
	}
	if a == nil {
		return false
	}
	if _, err := a.ValueForProtocol(multiaddr.P_CIRCUIT); err == nil {
		return false
	}
	ip, err := manet.ToIP(a)
	if err != nil {
		return false
	}
	return privatenet.IsPrivate(ip)
}

// PrivateMode reports whether this host was built with a peer allowlist.
func (n *Network) PrivateMode() bool {
	if n == nil {
		return false
	}
	return n.banGater.privateMode()
}

// AllowedPeers returns the private-mode allowlist (nil when off).
func (n *Network) AllowedPeers() []string {
	if n == nil || !n.banGater.privateMode() {
		return nil
	}
	out := make([]string, 0, len(n.banGater.allow))
	for id := range n.banGater.allow {
		out = append(out, id.String())
	}
	return out
}
