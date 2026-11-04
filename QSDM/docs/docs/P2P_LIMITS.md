# P2P limits and gossip review

A node talks to other nodes over libp2p and GossipSub. Until these limits,
it used the library defaults for everything: a connection manager and a
resource manager sized to the host, a 1 MiB cap on a gossip message, and
no limit per topic or per peer. A peer could open hundreds of connections,
send a message of up to 1 MiB on any topic, and send messages as fast as
the node could read them. Most topic handlers decode the JSON, and some
check signatures, before they look at who sent a message or how often.

This page describes the limits a node now applies, how to change them, and
a review of the six gossip topics with the risks that remain. The public
block producer runs these limits.

## 1. What changes

| Layer | Before | Now |
|---|---|---|
| Connection manager | libp2p default: trims from 192 connections down to 160, grace period 1 minute | trims from 96 connections down to 64, grace period 1 minute. Configured bootstrap peers are protected (tag `qsdm-bootstrap`) and never trimmed. |
| Resource manager | libp2p default, scaled to the host (about 1,000 or more connections on an 8 GB host), 8 connections per IP address | fixed limits: 192 connections (128 inbound) and 2,048 streams (1,024 inbound) in total; per peer 8 connections and 256 streams (128 inbound); 8 connections per IPv4 address and per IPv6 /56 (64 per /48). Memory, file descriptor, transient and service limits keep the library's scaled defaults. Loopback (`127.0.0.0/8`, `::1`) has no per-IP limit. |
| Largest gossip message | 1 MiB (library default) | 1 MiB, now explicit and configurable |
| Size limit per topic | none | the gossip guard rejects a message from another peer that is larger than its topic's limit (table in [section 3](#3-the-six-gossip-topics)) |
| Message rate per peer | only inside some topic handlers, after JSON decoding (for prevote-lock proofs and evidence, after the signature check) | the gossip guard accepts 100 messages per second per topic from each forwarding peer, with a burst of 1,000, before anything is decoded. Extra messages are ignored: dropped, not forwarded, and not held against the peer's score. |
| Peer scoring | off | on. A peer's score drops for messages the guard rejects (too large), for IP colocation (loopback exempt) and for GossipSub protocol misbehaviour. Thresholds: gossip −500, publish −1000, graylist −2500. |
| Validation queue and throttle | 32 and 8,192 (library defaults) | 256 and 2,048 |

The gossip guard is a GossipSub validator that runs on every topic before
the topic's own handler, so a message it rejects or ignores is never
decoded.

### A node without peers is unaffected

- The gossip guard accepts every message the node published itself,
  whatever its size. Local publishes (blocks, BFT messages, prevote-lock
  proofs, evidence, the transaction relay) pass exactly as before.
- The 1 MiB cap on a gossip message is the same value as before, so no
  publish that worked before can fail now.
- The connection and stream limits are far above what a node without peers
  uses, and loopback has no per-IP limit.
- Scoring, rate limits and size limits act only on messages from other
  peers. No consensus, block production or validation code changed, and the
  topic handlers in `pkg/chain` and `pkg/bridge` are unchanged.
- `p2p_limits_disabled = true` (or `QSDM_P2P_LIMITS_DISABLED=1`) builds the
  host exactly as before: libp2p defaults, no gossip guard and no scoring.

## 2. Configuration

All keys go under `[network]` (TOML) or `network:` (YAML). Environment
variables override the file. Every key is optional; 0 or an unset key means
the default.

| Key | Env | Default | Meaning |
|---|---|---|---|
| `p2p_limits_disabled` | `QSDM_P2P_LIMITS_DISABLED` | `false` | `true` turns every limit on this page off (libp2p library defaults, no gossip guard, no scoring). Escape hatch only. |
| `conn_low_water` | `QSDM_P2P_CONN_LOW_WATER` | 64 | the connection manager trims down to this many connections; must be below `conn_high_water` |
| `conn_high_water` | `QSDM_P2P_CONN_HIGH_WATER` | 96 | trimming starts above this many connections; must be at most `max_conns` |
| `conn_grace_period` | `QSDM_P2P_CONN_GRACE_PERIOD` | `1m` | a new connection is not trimmed for this long (Go duration) |
| `max_conns` | `QSDM_P2P_MAX_CONNS` | 192 | connections in total, inbound and outbound |
| `max_inbound_conns` | `QSDM_P2P_MAX_INBOUND_CONNS` | 128 | inbound connections in total; at most `max_conns` |
| `max_streams` | `QSDM_P2P_MAX_STREAMS` | 2048 | streams in total |
| `max_inbound_streams` | `QSDM_P2P_MAX_INBOUND_STREAMS` | 1024 | inbound streams in total; at most `max_streams` |
| `max_conns_per_peer` | `QSDM_P2P_MAX_CONNS_PER_PEER` | 8 | connections to one peer (inbound, outbound and in total) |
| `max_streams_per_peer` | `QSDM_P2P_MAX_STREAMS_PER_PEER` | 256 | streams to one peer |
| `max_inbound_streams_per_peer` | `QSDM_P2P_MAX_INBOUND_STREAMS_PER_PEER` | 128 | inbound streams from one peer; at most `max_streams_per_peer` |
| `max_conns_per_ip` | `QSDM_P2P_MAX_CONNS_PER_IP` | 8 | connections from one IPv4 address or one IPv6 /56 (eight times this from one /48). Loopback is exempt. |
| `gossip_max_message_bytes` | `QSDM_P2P_GOSSIP_MAX_MESSAGE_BYTES` | 1048576 (1 MiB) | the largest gossip message, on every topic, including the node's own publishes |
| `[network.gossip_topic_max_bytes]` (a table of topic = bytes) | `QSDM_P2P_GOSSIP_TOPIC_MAX_BYTES` (`topic=bytes,topic=bytes`) | see [section 3](#3-the-six-gossip-topics) | the largest message from another peer on that topic. Entries are merged over the defaults, and a value above `gossip_max_message_bytes` is lowered to it. A topic not in the table is limited by `gossip_max_message_bytes` only. |
| `gossip_peer_rate` | `QSDM_P2P_GOSSIP_PEER_RATE` | 100 | messages per second accepted per topic from one forwarding peer. A negative value turns the rate limit off. |
| `gossip_peer_burst` | `QSDM_P2P_GOSSIP_PEER_BURST` | 1000 | how many messages a forwarding peer can send at once before the rate applies |
| `gossip_peer_scoring_disabled` | `QSDM_P2P_GOSSIP_PEER_SCORING_DISABLED` | `false` | `true` turns peer scoring off; the size and rate limits stay |
| `gossip_validate_queue` | `QSDM_P2P_GOSSIP_VALIDATE_QUEUE` | 256 | gossip messages waiting for validation; when the queue is full, new inbound messages are dropped |
| `gossip_validate_throttle` | `QSDM_P2P_GOSSIP_VALIDATE_THROTTLE` | 2048 | validations running at the same time |

Rules:

- The node refuses to start with limits it cannot use. Config validation
  refuses a negative count or size (every key except `gossip_peer_rate`),
  a negative `conn_grace_period`,
  `conn_low_water` not below `conn_high_water` when both are set, and a
  topic entry with an empty name or a size of 0 or less. When the host is
  built, the limits are checked again with the defaults filled in:
  `conn_low_water` must be below `conn_high_water`, `max_conns` must be at
  least `conn_high_water` (so the connection manager trims before the
  resource manager refuses), and each inbound limit must be at most its
  total (`max_inbound_conns`, `max_inbound_streams`,
  `max_inbound_streams_per_peer`).
- A malformed `conn_grace_period` in the file stops the node. A malformed
  environment value (not a number, not a duration, or a topic list that
  does not parse) is ignored, like the node's other numeric environment
  overrides.
- The boolean environment variables accept `1`, `true`, `yes` or `on`;
  anything else set means `false`.
- With `p2p_limits_disabled = true` the other keys are neither checked nor
  used.

Example: fewer inbound peers, a lower gossip rate, and a smaller
transaction limit.

```toml
[network]
max_inbound_conns = 64
gossip_peer_rate = 20.0

[network.gossip_topic_max_bytes]
"qsdm-transactions" = 131072
```

The same in YAML:

```yaml
network:
  max_inbound_conns: 64
  gossip_peer_rate: 20.0
  gossip_topic_max_bytes:
    qsdm-transactions: 131072
```

## 3. The six gossip topics

GossipSub already gives every topic three protections: StrictSign (every
message carries its author's signature, checked against the author's peer
ID), a seen-cache that drops repeats of the same author and sequence
number, and the 1 MiB cap. The table shows what the limits add.
"Forwarding peer" is the neighbour that relayed a message to this node,
which is not always its author.

| Topic | Messages | Size limit | Work a message caused before any rate check, without the limits | Rate limited by |
|---|---|---|---|---|
| `qsdm-transactions` | signed transactions; signed enrollment envelopes | 256 KiB | up to two JSON decodes, then a post-quantum signature check, nonce and fee checks, and mempool admission | the guard (100 per second per peer). Before, inbound messages had no limit; only the outbound relay was limited (80 per second, with deduplication). Reputation bans apply after decoding. |
| `qsdm-bft` | `bft_propose` (carries a full block), `bft_prevote`, `bft_precommit` | 1 MiB (a proposal carries a block) | a JSON decode of the envelope and a SHA-256, then deduplication and a per-peer window (256 per minute) **before** the signature check | the handler (256 per minute per peer) and the guard |
| `qsdm-blocks` | `new_block`, `block_request`, `block_response` (up to 64 blocks) | 1 MiB | a full JSON decode, the block hash and the producer signature are checked **before** the seen-hash check; a `block_request` makes the node read up to 64 blocks and publish them | the guard only; there was nothing before |
| `qsdm-evidence` | consensus evidence (an equivocation proof: two signed votes) | 256 KiB | a JSON decode, then the signature checks, **before** deduplication and the per-peer window | the handler (64 per minute per peer, after the checks) and the guard (before decoding) |
| `qsdm-prevote-lock` | prevote-lock proofs and round certificates | 512 KiB (one signature per validator) | a JSON decode and the proof or certificate check **before** deduplication and the per-peer window | the handler (128 per minute per peer, after the check) and the guard |
| `qsdm-bridge` | bridge lock and swap events | 64 KiB | a JSON decode; an event is applied only when it is signed by a trusted relayer key ([Bridge relay authentication](BRIDGE_RELAY_AUTH.md)) | the guard only |

How the size limits were chosen: each is the largest message the code can
legitimately produce, rounded up generously.

- BFT and blocks keep the full 1 MiB. A proposal carries a block and a block
  response carries up to 64 blocks, so they can never be smaller than what
  the producer sends.
- Transactions: the protocol has no size limit for a transaction. The API
  accepts request bodies of up to 1 MiB, but a realistic ML-DSA-87 signed
  transaction is tens of KiB (a recovery-capsule payload is at most 32 KiB),
  so 256 KiB leaves a wide margin. A larger transaction submitted to one
  node's API does not reach other nodes over gossip any more. Raise the
  `qsdm-transactions` entry if such transactions are wanted.
- Evidence: two votes with their signatures and keys, about 20 to 40 KiB.
- Prevote-lock proofs: a round certificate grows with the number of
  validators (one today). 512 KiB holds several dozen ML-DSA-87
  signatures.
- Bridge events are small JSON objects: 64 KiB.

Rate and burst: the legitimate peak from one forwarding peer is far below
100 messages per second. The producer sends one block per slot, a node that
is behind asks for blocks every 10 seconds, and the transaction relay sends
at most 80 per second. The rate is a coarse bound against floods, not
traffic shaping. Extra messages are ignored rather than rejected, so an
honest peer that relays someone else's flood is not penalised.

Peer scoring: only messages the guard rejects (too large) and GossipSub
protocol misbehaviour lower a score, so an honest peer stays at 0. The
penalty is −10 × n², where n counts the peer's rejected messages, so about
8 oversized messages close together bring a peer to the gossip threshold
(the node no longer exchanges gossip metadata with it) and about 16 to the
graylist (the node ignores everything it sends). The count fades over about
10 minutes.

## 4. Counters and logs

- At startup the node logs `LibP2P host created` with a `p2p_limits` field
  that shows the effective limits, for example:

  ```
  connmgr=64/96 grace=1m0s conns=192(in 128) streams=2048(in 1024) per_peer_conns=8 per_peer_streams=256(in 128) per_ip_conns=8 gossip_max=1048576 topics=[...] peer_rate=100/s burst=1000 scoring=true
  ```

  or `disabled (libp2p library defaults)`.
- The gossip guard counts the messages from other peers that it handles:
  `Accepted`, `RejectedOversize` (larger than the topic's limit) and
  `RateLimited` (over the rate, ignored). Programs that embed the node read
  them with `Network.GossipGuardStats()` in `pkg/networking`. They are not
  yet exported as Prometheus metrics or in the status API.
- A connection or stream that the resource manager refuses fails with a
  libp2p resource-limit error. The node does not count these.

## 5. Code

- `QSDM/source/pkg/networking/p2p_limits.go`: `P2PLimits`, the defaults,
  `WithDefaults`, `Validate`, the resource manager and connection manager
  options, the GossipSub options and the peer-score parameters.
- `QSDM/source/pkg/networking/gossip_guard.go`: the gossip guard (the size
  and rate checks) and its counters.
- `QSDM/source/pkg/networking/libp2p.go`: `SetupLibP2PWithOptions` builds
  the host with the limits; the older `SetupLibP2PWith*` functions call it
  with the defaults.
- `QSDM/source/pkg/networking/bootstrap.go`: protects the configured
  bootstrap peers in the connection manager.
- `QSDM/source/pkg/config/p2p_limits.go`, plus small hooks in `config.go`
  and `config_toml.go`: parsing, environment overrides and `Validate`.
- `QSDM/source/cmd/qsdm/main.go`: `p2pLimitsFromConfig` maps the config
  onto the networking limits, and `SetupNetwork` and `SetupNetworkPublic`
  pass them on.

## 6. Tests

From `QSDM/source`, with the same settings as CI:

```
CGO_ENABLED=1 go test -count=1 -tags dilithium_circl ./pkg/networking/ ./pkg/config/ ./cmd/qsdm/
```

- `pkg/networking/p2p_limits_test.go`: the defaults and validation; the
  resource manager gets the configured values; the resource manager refuses
  a third connection from one IP address and a connection beyond
  `max_inbound_conns`, while loopback is exempt; a real host with
  `max_inbound_conns = 1` keeps only one inbound peer; GossipSub accepts the
  score parameters (including a non-zero `TimeInMeshQuantum`: a zero value
  makes the router panic); local publishes are unaffected; invalid limits
  are refused; the disabled mode installs no guard; between two hosts an
  oversized payload is rejected and an in-limit one is delivered; bootstrap
  peers are protected in the connection manager.
- `pkg/networking/gossip_guard_test.go`: size limits, the node's own
  messages, the rate per topic and peer, refill, removal of idle peers, and
  turning the rate off with a negative value.
- `pkg/networking/p2p_limits_topics_test.go`: the size table covers all six
  topic names, including the ones defined in `pkg/chain` and `pkg/bridge`.
- `pkg/config/p2p_limits_test.go`: TOML and YAML parsing of every key,
  unset keys stay zero, a bad duration, environment overrides (including
  merging topic entries), malformed environment values are ignored, and
  `Validate`.
- `cmd/qsdm/p2p_limits_test.go`: the mapping from config to networking
  copies every field, and a zero config maps to exactly the defaults.

## 7. Remaining risks

1. **`qsdm-bridge` events.** Without authentication any peer could create
   bridge locks and swaps or change their state, and the node saved that
   state. This review found it; it is fixed separately: a node applies a
   bridge event only when it is signed by a trusted relayer key, and none
   is trusted by default ([Bridge relay authentication](BRIDGE_RELAY_AUTH.md)).
2. **`block_request` amplification.** One small request makes every node
   with a block store publish a response of up to 1 MiB to the whole topic.
   The guard limits each forwarding peer to 100 messages per second, but
   that still lets one abusive neighbour pull about 100 MiB per second of
   responses out of a node. Options: answer catch-up requests on a direct
   stream with its own limits, rely on HTTP sync on public nodes, or give
   the block propagator a response budget per peer.
3. **Verification before deduplication** on blocks, evidence and
   prevote-lock proofs. A valid message is fully verified before the
   handler's seen check, so a peer can re-sign copies of a valid block,
   proof or evidence item (with a new sequence number) and make each copy
   cost a signature check. The guard caps this at 100 per second per peer
   and topic. Checking "hash, then seen, then rate, then signature", as the
   BFT handler already does, would remove it.
4. **The block propagator's seen set is never pruned.** It only stores the
   hashes of blocks that pass verification, so it grows with the chain, not
   with an attacker, but it is unbounded for the life of the process.
5. **Rate limits apply per forwarding peer, not per author.** A set of peers
   on different addresses gets that many times the budget. This is bounded
   by `max_inbound_conns` and `max_conns_per_ip`.
6. **Transaction gossip cost.** 100 well-formed but invalid signed
   transactions per second per peer still cost one post-quantum signature
   check each. The reputation tracker bans repeat offenders. Lower the
   `qsdm-transactions` rate if public peers become numerous; today
   `gossip_peer_rate` is the same for every topic.
7. **mDNS discovery** dials any peer it finds on the local network. That is
   harmless on a server, and the limits apply to those peers too.
8. **The defaults are estimates, not load-tested.** Measure them on a
   private test network before relying on them: peers, streams, gossip
   messages per second, the guard counters and the resource manager's
   refusals. Exporting the guard counters as metrics is a small follow-up.
