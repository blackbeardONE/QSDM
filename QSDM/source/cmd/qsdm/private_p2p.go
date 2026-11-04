package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/networking"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// p2pStartupMode is how main builds the libp2p host.
type p2pStartupMode string

const (
	// p2pModeDefault: the historical host (loopback or one interface, no NAT).
	p2pModeDefault p2pStartupMode = "default"
	// p2pModePublic: all-interface bind with NAT mapping and hole punching.
	p2pModePublic p2pStartupMode = "public"
	// p2pModePrivate: one private address, peer-ID allowlist, no NAT, no mDNS
	// (QSDM/docs/docs/PRIVATE_P2P.md).
	p2pModePrivate p2pStartupMode = "private"
)

// resolveP2PStartupMode picks the host mode. Private mode wins only when it is
// switched on, and then it refuses every combination that would expose the
// node: a public or all-interface bind (config.Validate already refused it;
// this is the second line) and the public bootstrap fallback.
func resolveP2PStartupMode(bindAddress string, priv config.PrivateP2PConfig, publicBootstrapFallback bool) (p2pStartupMode, error) {
	if !priv.Enabled {
		if isPublicP2PBind(bindAddress) {
			return p2pModePublic, nil
		}
		return p2pModeDefault, nil
	}
	if isPublicP2PBind(bindAddress) {
		return "", fmt.Errorf("private_p2p = true refuses public bind_address %q", bindAddress)
	}
	if err := config.ValidatePrivateBind(bindAddress); err != nil {
		return "", err
	}
	if len(priv.AllowedPeers) == 0 {
		return "", fmt.Errorf("private_p2p = true requires private_p2p_allowed_peers")
	}
	if publicBootstrapFallback {
		return "", fmt.Errorf("private_p2p = true cannot be combined with QSDM_ALLOW_PUBLIC_DHT_FALLBACK=1")
	}
	return p2pModePrivate, nil
}

// SetupNetworkPrivate builds the private-mode host: bound to one private
// address, accepting and dialling only allowedPeers over private addresses,
// with no NAT mapping, hole punching or mDNS.
func SetupNetworkPrivate(ctx context.Context, logger *logging.Logger, port int, bindAddress string, hostKeyPath string, limits networking.P2PLimits, allowedPeers []string) (*networking.Network, error) {
	if len(allowedPeers) == 0 {
		return nil, fmt.Errorf("private p2p: no allowed peers")
	}
	return networking.SetupLibP2PWithOptions(ctx, logger, networking.SetupOptions{
		Port: port, BindAddress: bindAddress, HostKeyPath: hostKeyPath, Limits: limits,
		AllowedPeers: allowedPeers,
	})
}

// producerAllowlistAdvice explains a stale [consensus]
// authorized_block_producers. While a producer transition is configured the
// allowlist is not consulted (TryAppendExternalBlock checks the transition
// instead), so a list that still names an earlier producer is
// inert -- but it becomes the only gate if the transition table is ever
// removed, and it misleads anyone copying the file to a follower. The result
// is advice for a WARN line; it never changes which blocks are accepted.
// Empty means nothing to say.
func producerAllowlistAdvice(authorized []string, t *producerpolicy.Transition) string {
	if t == nil {
		return ""
	}
	final := strings.TrimSpace(t.ReplacementProducer)
	if final == "" {
		return ""
	}
	var pinned []string
	for _, id := range authorized {
		if id = strings.TrimSpace(id); id != "" {
			pinned = append(pinned, id)
		}
	}
	if len(pinned) == 0 {
		return ""
	}
	for _, id := range pinned {
		if id == final {
			return ""
		}
	}
	return fmt.Sprintf("authorized_block_producers %s does not include the current producer %s from the producer transition; the list is inert while the transition is configured, but pin %s (and keep the transition table) so a copied or follower config stays correct",
		strings.Join(pinned, ","), final, final)
}
