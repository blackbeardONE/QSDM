package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/envcompat"
	"github.com/blackbeardONE/QSDM/pkg/privatenet"
)

// PrivateP2PTOML is the file form of the private P2P mode
// (QSDM/docs/docs/PRIVATE_P2P.md). It is embedded in [network]:
//
//	[network]
//	port = 14001
//	bind_address = "10.0.0.2"              # this node's private address
//	bootstrap_peers = ["/ip4/10.0.0.1/tcp/14001/p2p/12D3KooW..."]
//	private_p2p = true
//	private_p2p_allowed_peers = ["12D3KooW..."]
//
// Off (the default) changes nothing. On, the node:
//   - must bind P2P to one literal private address (loopback, RFC 1918,
//     100.64.0.0/10 or fc00::/7); an empty, 0.0.0.0, :: or public bind_address
//     is refused at startup, so public mode (NAT mapping, hole punching) can
//     never be selected together with private mode;
//   - accepts and dials only the libp2p peer IDs listed here (a connection
//     gater in pkg/networking), and only over private addresses;
//   - runs no mDNS discovery;
//   - requires every bootstrap peer to be a literal private /ip4 or /ip6
//     address whose /p2p/ ID is in the list.
type PrivateP2PTOML struct {
	PrivateP2P             bool     `toml:"private_p2p" yaml:"private_p2p"`
	PrivateP2PAllowedPeers []string `toml:"private_p2p_allowed_peers" yaml:"private_p2p_allowed_peers"`
}

// PrivateP2PConfig is the parsed form held on Config.
type PrivateP2PConfig struct {
	Enabled      bool
	AllowedPeers []string
}

func (t PrivateP2PTOML) toConfig() PrivateP2PConfig {
	return PrivateP2PConfig{Enabled: t.PrivateP2P, AllowedPeers: trimNonEmpty(t.PrivateP2PAllowedPeers)}
}

func trimNonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// applyPrivateP2PEnv applies QSDM_P2P_PRIVATE (bool) and
// QSDM_P2P_PRIVATE_ALLOWED_PEERS (comma-separated peer IDs, replaces the file
// list).
func applyPrivateP2PEnv(c *PrivateP2PConfig) {
	if strings.TrimSpace(os.Getenv("QSDM_P2P_PRIVATE")) != "" {
		c.Enabled = envcompat.Truthy("QSDM_P2P_PRIVATE", "QSDM_P2P_PRIVATE")
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_P2P_PRIVATE_ALLOWED_PEERS")); v != "" {
		c.AllowedPeers = trimNonEmpty(strings.Split(v, ","))
	}
}

// peerIDPattern is the textual shape of a libp2p peer ID: base58btc, either an
// identity-hashed Ed25519 key (12D3KooW..., the format hostkey.go creates) or
// a SHA-256 multihash (Qm...). pkg/networking decodes each entry fully when
// the host is built; this check catches typos at config load.
var peerIDPattern = regexp.MustCompile(`^(12D3KooW[1-9A-HJ-NP-Za-km-z]{44}|Qm[1-9A-HJ-NP-Za-km-z]{44})$`)

// ValidPeerIDSyntax reports whether s looks like a libp2p peer ID.
func ValidPeerIDSyntax(s string) bool { return peerIDPattern.MatchString(s) }

// Validate checks the private P2P keys against the bind address and the
// bootstrap peers. Off with no allowed peers is always valid (the default).
func (c PrivateP2PConfig) Validate(bindAddress string, bootstrapPeers []string) error {
	if !c.Enabled {
		if len(c.AllowedPeers) > 0 {
			return fmt.Errorf("[network] private_p2p_allowed_peers is set but private_p2p is false: the list is enforced only in private mode; set private_p2p = true or clear the list")
		}
		return nil
	}
	if len(c.AllowedPeers) == 0 {
		return fmt.Errorf("[network] private_p2p = true requires at least one private_p2p_allowed_peers entry (env QSDM_P2P_PRIVATE_ALLOWED_PEERS)")
	}
	allowed := make(map[string]struct{}, len(c.AllowedPeers))
	for i, id := range c.AllowedPeers {
		if !ValidPeerIDSyntax(id) {
			return fmt.Errorf("[network] private_p2p_allowed_peers[%d] %q is not a libp2p peer ID (want 12D3KooW... or Qm...)", i, id)
		}
		allowed[id] = struct{}{}
	}
	if err := ValidatePrivateBind(bindAddress); err != nil {
		return err
	}
	for i, addr := range bootstrapPeers {
		if strings.TrimSpace(addr) == "" {
			continue
		}
		id, err := privateBootstrapPeerID(addr)
		if err != nil {
			return fmt.Errorf("[network] bootstrap_peers[%d] %q: %w", i, addr, err)
		}
		if _, ok := allowed[id]; !ok {
			return fmt.Errorf("[network] bootstrap_peers[%d]: peer %s is not in private_p2p_allowed_peers; the gater would refuse it", i, id)
		}
	}
	return nil
}

// ValidatePrivateBind refuses any bind address the private mode must not use:
// empty (all interfaces), 0.0.0.0, ::, a host name, or a public address.
func ValidatePrivateBind(bindAddress string) error {
	b := strings.TrimSpace(bindAddress)
	if b == "" {
		return fmt.Errorf("[network] private_p2p = true requires bind_address to be this node's private (e.g. Tailscale 100.x) address; empty binds every interface")
	}
	ip := privatenet.ParseIP(b)
	if ip == nil {
		return fmt.Errorf("[network] private_p2p = true: bind_address %q must be a literal IP address", b)
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("[network] private_p2p = true refuses public bind_address %q (all interfaces)", b)
	}
	if !privatenet.IsPrivate(ip) {
		return fmt.Errorf("[network] private_p2p = true refuses bind_address %q: not loopback, RFC 1918, 100.64.0.0/10 or fc00::/7", b)
	}
	return nil
}

// privateBootstrapPeerID checks that a bootstrap multiaddr is a direct
// connection to a literal private address and returns its peer ID.
func privateBootstrapPeerID(addr string) (string, error) {
	parts := strings.Split(strings.TrimSpace(addr), "/")
	if len(parts) < 3 || parts[0] != "" {
		return "", fmt.Errorf("not a multiaddr")
	}
	switch parts[1] {
	case "ip4", "ip6":
	default:
		return "", fmt.Errorf("private mode needs a literal /ip4 or /ip6 address (got /%s); names are resolved outside the private network", parts[1])
	}
	ip := privatenet.ParseIP(parts[2])
	if ip == nil || !privatenet.IsPrivate(ip) {
		return "", fmt.Errorf("address %q is not private", parts[2])
	}
	id := ""
	for i := 3; i < len(parts); i++ {
		switch parts[i] {
		case "p2p-circuit":
			return "", fmt.Errorf("relayed addresses are not allowed in private mode")
		case "p2p", "ipfs":
			if i+1 < len(parts) {
				id = parts[i+1]
				i++
			}
		}
	}
	if id == "" {
		return "", fmt.Errorf("missing /p2p/<peer id>")
	}
	if !ValidPeerIDSyntax(id) {
		return "", fmt.Errorf("%q is not a libp2p peer ID", id)
	}
	return id, nil
}
