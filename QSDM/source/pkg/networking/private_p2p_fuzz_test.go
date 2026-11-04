package networking

import (
	"net"
	"net/netip"
	"testing"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// fuzzPrivateRanges is an independent statement of the ranges private P2P
// mode admits: loopback, RFC 1918, 100.64.0.0/10 and fc00::/7.
// pkg/privatenet implements the same rule with net.IP methods.
var fuzzPrivateRanges = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
}

func fuzzInPrivateRange(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	for _, p := range fuzzPrivateRanges {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// fuzzPrivateAddrOK is what private mode must admit: an address that is not
// relayed and whose IP is in a private range.
func fuzzPrivateAddrOK(a multiaddr.Multiaddr) bool {
	for _, c := range a {
		if c.Protocol().Code == multiaddr.P_CIRCUIT {
			return false
		}
	}
	ip, err := manet.ToIP(a)
	return err == nil && fuzzInPrivateRange(ip)
}

// FuzzPrivateGaterAddress feeds the connection gater a remote address the
// way a peer presents it (the binary multiaddr form, or the text form when
// asText is set) and a peer ID. The gater has private mode on with one
// listed peer, and the unlisted peer is exempt from bans, as a bootstrap
// peer would be. Whatever the input:
//   - with private mode off, every address and peer passes, as before;
//   - with it on, an inbound connection and a dial to the listed peer are
//     admitted exactly when the address is not relayed and its IP is in an
//     independently listed private range;
//   - a peer that is not listed is never dialled and never passes the
//     handshake check, whatever its address and its ban exemption.
func FuzzPrivateGaterAddress(f *testing.F) {
	for _, s := range []string{
		"/ip4/10.0.0.5/tcp/1", "/ip4/100.70.1.2/tcp/14001", "/ip4/127.0.0.1/tcp/4001", "/ip4/172.31.255.254/udp/1/quic-v1",
		"/ip6/fd7a:115c:a1e0::9/tcp/1", "/ip6/::1/tcp/1", "/ip6/::ffff:10.0.0.1/tcp/1", "/ip6/::ffff:8.8.8.8/tcp/1",
		"/ip4/8.8.8.8/tcp/4001", "/ip4/100.128.0.1/tcp/1", "/ip4/0.0.0.0/tcp/1", "/ip4/169.254.1.1/tcp/1",
		"/ip4/224.0.0.1/udp/1", "/ip6/::/tcp/1", "/ip6/2001:db8::1/tcp/4001", "/ip6zone/eth0/ip6/fe80::1/tcp/1",
		"/dns4/node.example/tcp/4001", "/ip4/10.0.0.5/tcp/1/p2p/12D3KooWHT2APYkMStuaiLZvto7ZmrPmUag4TStT7MryGn69feum/p2p-circuit",
	} {
		a := multiaddr.StringCast(s)
		f.Add(a.Bytes(), false, []byte("listed"))
		f.Add([]byte(s), true, []byte("stranger"))
	}
	f.Add([]byte{}, false, []byte{})
	f.Add([]byte("/ip4/10.0.0.1"), true, []byte("listed\x00"))

	const listed = peer.ID("listed")
	f.Fuzz(func(t *testing.T, raw []byte, asText bool, pid []byte) {
		var a multiaddr.Multiaddr
		var err error
		if asText {
			a, err = multiaddr.NewMultiaddr(string(raw))
		} else {
			a, err = multiaddr.NewMultiaddrBytes(raw)
		}
		if err != nil {
			return
		}
		other := peer.ID(pid)
		conn := connAddrs{remote: a}

		off := &banGater{}
		if !off.InterceptAccept(conn) || !off.InterceptAddrDial(other, a) || !off.InterceptPeerDial(other) ||
			!off.InterceptSecured(network.DirInbound, other, conn) {
			t.Fatalf("private mode off refused %s from %q", a, other)
		}

		on := &banGater{allow: map[peer.ID]struct{}{listed: {}}}
		if other != listed {
			on.SetExemptPeers([]string{other.String()})
		}
		want := fuzzPrivateAddrOK(a)
		if got := on.InterceptAccept(conn); got != want {
			t.Fatalf("InterceptAccept(%s) = %v, want %v", a, got, want)
		}
		if got := on.InterceptAddrDial(listed, a); got != want {
			t.Fatalf("InterceptAddrDial(listed, %s) = %v, want %v", a, got, want)
		}
		isListed := other == listed
		if got := on.InterceptAddrDial(other, a); got != (isListed && want) {
			t.Fatalf("InterceptAddrDial(%q, %s) = %v", other, a, got)
		}
		if got := on.InterceptPeerDial(other); got != isListed {
			t.Fatalf("InterceptPeerDial(%q) = %v", other, got)
		}
		for _, dir := range []network.Direction{network.DirInbound, network.DirOutbound} {
			if got := on.InterceptSecured(dir, other, conn); got != isListed {
				t.Fatalf("InterceptSecured(%v, %q) = %v", dir, other, got)
			}
		}
		if on.InterceptAccept(nil) {
			t.Fatal("private mode accepted a connection with no address")
		}
	})
}
