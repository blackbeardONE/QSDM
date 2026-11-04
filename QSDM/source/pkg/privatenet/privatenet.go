// Package privatenet classifies IP addresses for the private P2P mode
// (QSDM/docs/docs/PRIVATE_P2P.md).
//
// It is a leaf package so pkg/config (bind/bootstrap validation) and
// pkg/networking (the connection gater) apply exactly the same rule.
package privatenet

import (
	"net"
	"strings"
)

// cgnat is 100.64.0.0/10 (RFC 6598). Tailscale assigns node addresses from it.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0).To4(), Mask: net.CIDRMask(10, 32)}

// IsPrivate reports whether ip is an address the private P2P mode may bind to,
// accept from or dial: loopback, RFC 1918 (10/8, 172.16/12, 192.168/16),
// shared address space 100.64.0.0/10 (Tailscale), or IPv6 unique local
// fc00::/7 (Tailscale's fd7a:115c:a1e0::/48 is inside it).
//
// Unspecified (0.0.0.0, ::), link-local, multicast and every public unicast
// address are not private.
func IsPrivate(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		return cgnat.Contains(v4)
	}
	return false
}

// ParseIP parses a literal IP address, accepting "localhost" as 127.0.0.1 and
// a bracketed IPv6 literal. It returns nil when s is not a literal address.
func ParseIP(s string) net.IP {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "localhost") {
		return net.IPv4(127, 0, 0, 1)
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	return net.ParseIP(s)
}
