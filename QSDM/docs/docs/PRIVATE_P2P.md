# Private P2P mode

By default a node's libp2p host accepts a connection from any peer that can
reach its port, and dials any peer it learns about. Private P2P mode limits
the host to a private network and a fixed list of peers. It is meant for a
small set of nodes run by one operator, for example a block producer and
the followers it feeds, joined over a private network or a VPN (such as
WireGuard or Tailscale) instead of the internet.

Private mode is off by default. With its keys unset the node builds exactly
the same host as before.

## 1. What changes

| Piece | Where | Default |
|---|---|---|
| Config keys `private_p2p` and `private_p2p_allowed_peers` under `[network]`; environment `QSDM_P2P_PRIVATE` and `QSDM_P2P_PRIVATE_ALLOWED_PEERS` | `pkg/config/private_p2p.go` | off, no change |
| Checks at startup (bind address, peer IDs, bootstrap peers) | `PrivateP2PConfig.Validate`, called from `Config.Validate` | only when on |
| A connection gater that admits only the listed peer IDs, and only from and to private addresses | `pkg/networking/private_p2p.go`, `ban_gater.go` | only when on |
| A host with no NAT port mapping, no hole punching and no mDNS discovery | `SetupOptions.AllowedPeers` in `pkg/networking/libp2p.go` | only when on |
| The choice of host mode at startup, which refuses a public bind and the public bootstrap fallback in private mode | `cmd/qsdm/private_p2p.go` (`resolveP2PStartupMode`), called from `main.go` | the same choice as before |
| A warning for a stale `authorized_block_producers` list | `producerAllowlistAdvice`, logged at startup | a log line only |
| One rule for "private address", shared by the config checks and the gater | `pkg/privatenet` | none |

No consensus, block production, block acceptance or gossip-topic code
changed. With both keys unset the node builds the same host as before: the
same listen addresses, the same choice of public mode for `0.0.0.0` and
`::`, mDNS, and a connection gater that checks only bans.

## 2. Configuration

All keys go under `[network]` (TOML) or `network:` (YAML). Environment
variables override the file.

| Key | Env | Default | Meaning |
|---|---|---|---|
| `private_p2p` | `QSDM_P2P_PRIVATE` (`1`, `true`, `yes` or `on` mean true; any other value means false) | `false` | `true` switches private mode on |
| `private_p2p_allowed_peers` | `QSDM_P2P_PRIVATE_ALLOWED_PEERS` (comma-separated; replaces the list from the file) | empty | the libp2p peer IDs (`12D3KooW...` or `Qm...`) this node may connect to or accept |

A node in private mode, with placeholders for its own values:

```toml
[network]
port = 14001
bind_address = "<this node's private address>"
bootstrap_peers = ["/ip4/<the other node's private address>/tcp/14001/p2p/<the other node's peer ID>"]
private_p2p = true
private_p2p_allowed_peers = ["<the other node's peer ID>"]
```

When `private_p2p = true`, the node refuses to start (`invalid
configuration`) unless all of the following hold:

- `bind_address` is one literal private address: loopback (`127.0.0.0/8`,
  `::1`; `localhost` counts as `127.0.0.1`), RFC 1918 (`10.0.0.0/8`,
  `172.16.0.0/12`, `192.168.0.0/16`), the shared address space
  `100.64.0.0/10` (which Tailscale uses), or IPv6 unique local addresses
  (`fc00::/7`). An empty value, `0.0.0.0`, `::`, `[::]`, a host name and
  every public address are refused. Only `0.0.0.0` and `::` select public
  mode (NAT port mapping and hole punching), so public mode can never be
  combined with private mode; the node checks this again when it starts.
- `private_p2p_allowed_peers` has at least one entry, and every entry looks
  like a libp2p peer ID. When the host is built every entry is also fully
  decoded, so a typo stops the node instead of silently shortening the
  list.
- Every `bootstrap_peers` entry has the form
  `/ip4/<private address>/tcp/<port>/p2p/<peer ID>` (or `/ip6/...`), and its
  peer ID is in the allowlist. `/dns4`, `/dns6` and other name-based
  addresses, and relayed (`/p2p-circuit`) addresses, are refused.
- `QSDM_ALLOW_PUBLIC_DHT_FALLBACK=1` is not set.

Setting `private_p2p_allowed_peers` while `private_p2p = false` is refused
too, so nobody believes a list is enforced when it is not.

Private mode does not switch on the [P2P limits](P2P_LIMITS.md). They are
independent: set `p2p_limits_enabled = true` as well to have both.

## 3. What the gater enforces

The node's connection gater, which already refuses banned peers, gets a
fixed allowlist when the host is built. It never changes while the node
runs.

| Check | Rule in private mode |
|---|---|
| Accepting a connection (`InterceptAccept`) | The remote address must be a literal private IP. This is checked before the handshake. |
| After the handshake, in both directions (`InterceptSecured`) | The peer ID must be in the list. The handshake proves the peer's identity. |
| Dialling a peer (`InterceptPeerDial`) | The peer must be in the list. |
| Dialling an address (`InterceptAddrDial`) | The peer must be in the list and the address must be a literal private IP, not relayed. A listed peer's public addresses, learned from the peer itself, are therefore never dialled. |

Bans still apply on top. The ban exemption for bootstrap peers never admits
a peer that is not in the list. Gossip messages are still signed by their
author (StrictSign).

## 4. The stale producer-allowlist warning

`[consensus] authorized_block_producers` lists the producers whose blocks
the node accepts from the `qsdm-blocks` topic. While
`[consensus.producer_transition]` is configured, the node checks the
transition table instead and does not read the list, so a list that still
names an earlier producer has no effect. It becomes the only check if the
transition table is ever removed, though, and it misleads anyone who copies
the file to set up a follower.

The node therefore logs `Stale external block producer allowlist` at WARN
when it starts, if a producer transition is configured and
`authorized_block_producers` is not empty but does not contain the
transition's current producer: `replacement_producer` in a version 1 table,
the last epoch's `producer_id` in a version 2 table (see
[Producer transition](PRODUCER_TRANSITION.md)). The message names the
producer to pin. Which blocks are accepted does not change.

A follower should pin `authorized_block_producers` to the transition's
current producer and carry the producer's
`[consensus.producer_transition]` table unchanged.

## 5. Setting up a follower in private mode

The same steps apply to any pair of nodes; here node A produces blocks and
node B follows it.

1. **Peer IDs.** A node's libp2p peer ID is the `node_id` in its
   `/api/v1/status`. It stays the same across restarts only when
   `host_key_path` is set, so set it on both nodes first. If the key file
   is lost, the peer ID changes and the other node's list must be updated.
2. **The follower (B).** Copy A's `[consensus]` table, so that every
   activation height is identical, but keep B's own `signer_key_path`. Set
   `authorized_block_producers` to the current producer (the transition's
   `replacement_producer`, or the last epoch's `producer_id` in a version 2
   table). In `[network]`, set `bind_address` to B's
   private address, one bootstrap peer with A's private address and peer
   ID, `private_p2p = true`, and `private_p2p_allowed_peers` to A's peer
   ID. Keep HTTP sync configured (`QSDM_CHAIN_SYNC_URLS`) as a fallback,
   and keep `QSDM_NETWORK_BLOCK_PRODUCER` unset or `0`: a follower never
   produces. Until A listens on its private address, B's dial simply fails.
3. **The producer (A).** In `[network]`, set `bind_address` to A's private
   address, `private_p2p = true`, and `private_p2p_allowed_peers` to B's
   peer ID. Keep `bootstrap_peers = []`.
4. **Firewall.** On A, allow the P2P port only on the private interface and
   only from B's private address. On a VPN, restrict it there too.
5. **Check.** The startup log shows `Private P2P mode enabled` and
   `LibP2P host created` with `private_p2p=true`. A's status API shows
   `peers` 1, and B's `qsdm_p2p_peers_connected` metric is 1. B receives
   blocks by gossip and stays within a block or two of A. B logs no
   `Stale external block producer allowlist` warning.

To undo it on the producer, set `private_p2p = false`, remove the list, set
`bind_address = "127.0.0.1"` and restart. The follower falls back to HTTP
sync.

**Before installing an older binary:** the TOML and YAML loaders ignore
keys they do not know. A binary without private mode ignores `private_p2p`
and `private_p2p_allowed_peers` but still binds `bind_address`, so it would
listen on the private address with **no** peer allowlist. Set
`bind_address = "127.0.0.1"` first, or rely on the firewall alone,
knowingly.

## 6. Logs and status

- `Private P2P mode enabled` at startup, with `bind_address`,
  `network_port`, `allowed_peers`, `nat_port_mapping=false` and
  `mdns=false`.
- `LibP2P host created` reports `private_p2p` (true or false) and
  `allowed_peers` (how many).
- `Stale external block producer allowlist` at WARN, as described in
  [section 4](#4-the-stale-producer-allowlist-warning).
- The status API's `peers` and the `qsdm_p2p_peers_connected` metric count
  the connected peers. Refused connections are not counted.
- Programs that embed the node can read `Network.PrivateMode()` and
  `Network.AllowedPeers()` in `pkg/networking`.

## 7. Tests

From `QSDM/source`, with the same settings as CI:

```
CGO_ENABLED=1 go test -count=1 -tags dilithium_circl ./pkg/privatenet/ ./pkg/config/ ./pkg/networking/ ./cmd/qsdm/
```

- `pkg/privatenet`: which addresses count as private, and parsing.
- `pkg/config` `TestPrivateP2P_*`: TOML and YAML parsing, environment
  overrides, the default staying unchanged, every refusal, the
  `Config.Validate` wiring, and a follower config in private mode
  (`testdata/follower_core.toml`) loading with the node's config loader.
- `pkg/networking` `TestPrivateGater_*` and `TestSetupPrivate_*`: the gater
  rules, the bootstrap ban exemption not widening the list, refused setup
  options, real hosts on loopback (the listed peer can connect in and be
  dialled out; another peer is refused both ways), and two private nodes
  that list each other exchanging a `qsdm-blocks` message through the
  node's own GossipSub setup while a third host cannot connect to either.
- `cmd/qsdm` `TestResolveP2PStartupMode_*`,
  `TestSetupNetworkPrivate_requiresPeers`, `TestProducerAllowlistAdvice` and
  `TestProducerAllowlistAdviceVersion2` (the warning names the last epoch's
  producer of a version 2 table).
- `pkg/config/live_shape_test.go` and `cmd/qsdm/live_shape_test.go`: a
  config shaped like the block producer's from before private mode keeps
  private mode off and the historical host mode, and gets the stale-list
  warning because it pins the earlier producer.
- `pkg/networking/private_p2p_fuzz_test.go`: `FuzzPrivateGaterAddress`
  gives the gater any remote address, in binary or text form, and any peer
  ID. With private mode off everything passes; with it on, an address is
  admitted exactly when it is not relayed and its IP is in an
  independently listed private range, and a peer that is not listed never
  passes, whatever its ban exemption. Its seeds run with the other tests,
  and the nightly fuzz workflow fuzzes it for five minutes.

## 8. Remaining risks

- The private network decides who can reach the port, not who is trusted.
  Peers are still authenticated by their libp2p identity, and blocks by
  ML-DSA-87 signatures and the producer transition. A compromised machine
  on the private network can reach the port, but the gater refuses its
  handshake unless its peer ID is listed.
- The allowlist holds libp2p transport identities. If a node's host key is
  lost, its peer ID changes and the other node's list must be updated.
- The gater refuses connections from public addresses. A peer that reaches
  the node through a NAT that rewrites its address to a public one is
  refused. That is the intended failure.
- A downgrade to a binary without these keys silently drops the allowlist
  (see the warning in [section 5](#5-setting-up-a-follower-in-private-mode)).
  Keep the firewall rule as a second layer.
