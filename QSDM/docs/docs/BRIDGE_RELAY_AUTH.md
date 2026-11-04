# Bridge relay authentication (`qsdm-bridge`)

Nodes share cross-chain bridge locks and atomic swaps with each other as
events on the `qsdm-bridge` gossip topic. A node used to apply every such
event it received from a peer without checking who sent it. Any peer could
insert locks and swaps with new IDs, without limit, or mark existing ones as
redeemed, refunded or completed, and the bridge auto-saver then wrote that
state to `qsdm_bridge_state.json`. The relay starts on every node that runs
the bridge or the atomic-swap protocol, so every such node was exposed.

A node now applies an event from a peer only when the event is signed with
ML-DSA-87 by a key on the operator's list of trusted bridge relayer keys. The
list is empty by default, so a default node applies no bridge event from its
peers.

## 1. What changes

- **No remote bridge event is applied unless it carries a valid ML-DSA-87
  signature from a key on the operator's allowlist of bridge relayer public
  keys.** The allowlist is empty by default, so a default node ignores every
  bridge event from peers. It still counts them (see [Counters](#4-counters))
  and logs one line the first time.
- **Local bridge operations are unchanged.** The bridge HTTP API handlers
  (`pkg/api/handlers_bridge.go`) change local state directly, as before, and
  still publish the event on the topic.
- **Locally published events are signed when a relayer key is configured.**
  Without one they go out unsigned, as before. Peers that enforce
  authentication ignore them.
- **Authenticated events are also checked against local state** before they
  are applied:

  | Event | Applied only when | Otherwise |
  |---|---|---|
  | `lock_created` | the ID is 1 to 128 characters, the status is `locked`, and no lock with that ID exists. The pre-image is dropped. | rejected; a no-op if the lock already exists (an existing lock is never replaced) |
  | `lock_redeemed` / `lock_refunded` | the lock exists locally and is `locked` | rejected (unknown lock, or already redeemed, refunded or expired). A repeat of the same transition is a no-op. |
  | `swap_initiated` | the ID is valid, the status is `initiated`, and no swap with that ID exists. Secrets are dropped. | rejected; a no-op if the swap already exists (an existing swap is never replaced) |
  | `swap_participated` | the swap exists and is `initiated` | rejected, or a no-op if already participated or completed |
  | `swap_completed` | the swap exists and is `initiated` or `participated` | rejected (unknown, refunded or expired swap), or a no-op if already completed |
  | `swap_refunded`, unknown kinds | never applied from gossip | no-op (as before; this kind was never handled) |

  So even a trusted relayer cannot create a final state (redeemed, refunded,
  completed) for a lock or swap that this node does not already know, or move
  a final state to another one. The old code let `lock_refunded` overwrite any
  status, including `redeemed`.
- Status events are matched by the object's `ID` key, which is what
  `PublishLockEvent` and `PublishSwapEvent` actually send (the full object).
  The old handler only read `lock_id` / `swap_id`, so real redeem and complete
  events from the API never matched. Both forms are accepted now.

Nothing in consensus, block production, `pkg/chain`, the mempool or the other
gossip topics changed. A signed event is about 7 KiB.

## 2. Wire format

The JSON envelope gains two optional fields. The old fields and their meaning
are unchanged.

```json
{
  "kind": "lock_created",
  "payload": { ... },
  "origin_node": "<libp2p peer ID of the publisher>",
  "ts": "2026-10-16T08:00:00Z",
  "signer_key_id": "<hex SHA-256 of the relayer's ML-DSA-87 public key>",
  "sig": "<base64 ML-DSA-87 signature>"
}
```

The signature is FIPS 204 ML-DSA-87 with an empty context: the same
primitive, library (circl `mldsa87`) and key encoding as the validator
consensus key (`pkg/chain/bft_signer_key.go`). It covers this byte string,
where every field is prefixed by its length as a 4-byte big-endian integer:

```
"QSDM-BRIDGE-EVENT-v1" | kind | origin_node | ts | signer_key_id | payload (raw JSON bytes)
```

The domain tag keeps a bridge signature from ever being valid as any other
message, and the reverse. Changing any field, including `kind` or the
payload, invalidates the signature. `signer_key_id` is the hex SHA-256 of the
signer's packed public key, the same form as a validator address.

A receiver with a non-empty allowlist checks, in this order:

1. both signature fields are present;
2. `signer_key_id` is on the allowlist;
3. the signature verifies under that key;
4. `ts` is RFC 3339, no older than `bridge_relay_max_event_age` (default
   10 minutes), and at most 2 minutes in the future.

Only then do the state checks in section 1 run. Events whose `origin_node` is
this node, and messages this node published itself, are skipped.

## 3. Configuration

All keys go under `[network]` (TOML) or `network:` (YAML). Environment
variables override the file.

| Key | Env | Default | Meaning |
|---|---|---|---|
| `bridge_relay_trusted_keys` | `QSDM_BRIDGE_RELAY_TRUSTED_KEYS` (comma-separated) | empty | hex ML-DSA-87 public keys (2592 bytes, optional `0x`) whose signed events are applied |
| `bridge_relay_trusted_keys_file` | `QSDM_BRIDGE_RELAY_TRUSTED_KEYS_FILE` | unset | a file with more keys: one hex key per line, `#` starts a comment, at most 1 MiB. Merged with the inline list; duplicates are ignored. |
| `bridge_relay_signer_key_path` | `QSDM_BRIDGE_RELAY_SIGNER_KEY_PATH` | unset | an ML-DSA-87 key file in the consensus signer format. When set, locally published events are signed with it. |
| `bridge_relay_max_event_age` | `QSDM_BRIDGE_RELAY_MAX_EVENT_AGE` | `10m` | the maximum age of an accepted remote event |

Rules:

- Every key is optional. Leave them all unset and the node publishes unsigned
  events and applies no remote events. **This is the recommended setting for
  public nodes.**
- A configured but unusable key stops the node at startup: bad hex, wrong
  length, a key that does not unpack, an unreadable or oversized keys file, or
  a signer file that is missing or does not load. A typo can never leave the
  node running with a different trust set than the operator intended. Config
  validation also rejects bad hex and negative ages.
- `QSDM_BRIDGE_RELAY_TRUSTED_KEYS` replaces the inline list from the config
  file; the keys file is still read. A malformed
  `QSDM_BRIDGE_RELAY_MAX_EVENT_AGE` is ignored, like the other environment
  overrides, but a malformed `bridge_relay_max_event_age` in the config file
  stops the node.
- The bridge signer must be a **different key from the consensus signer**. The
  node refuses to start if the configured paths are the same, or if the loaded
  keys are the same.
- The startup log line `Bridge P2P relay started on topic qsdm-bridge` reports
  `trusted_relayer_keys`, `signing` and whether remote events are applied or
  ignored.

Example: a relayer node that signs, and a follower that trusts it.

```toml
# relayer
[network]
bridge_relay_signer_key_path = "/opt/qsdm/bridge_relayer_key.json"

# follower
[network]
bridge_relay_trusted_keys_file = "/opt/qsdm/bridge_relayers.txt"
```

### Creating a relayer key

The relayer key file has the same format as the validator consensus key file
(`[consensus] signer_key_path`): a JSON file with mode `0600`.

```json
{
  "version": 1,
  "algorithm": "ml-dsa-87",
  "public_key": "<hex packed ML-DSA-87 public key, 2592 bytes>",
  "private_key": "<hex packed ML-DSA-87 private key, 4896 bytes>"
}
```

The node creates its consensus key on first start, but it never creates the
bridge relayer key: create the file before you set
`bridge_relay_signer_key_path`. Use a new key, never the consensus key.

This repository has no separate command for it. One way is a few lines of Go
that write the file with the node's own key code. In a source checkout, save
this as `QSDM/source/bridgekeygen/main.go` (do not commit it):

```go
package main

import (
	"fmt"
	"os"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func main() {
	path := os.Args[1]
	if _, err := os.Lstat(path); err == nil {
		fmt.Fprintln(os.Stderr, "refusing to overwrite", path)
		os.Exit(1)
	}
	key, _, err := chain.LoadOrCreateBFTSigner(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("key ID:", key.Address())
}
```

Then, from `QSDM/source`, on the relayer host:

```
go run -tags dilithium_circl ./bridgekeygen /opt/qsdm/bridge_relayer_key.json
```

The directory must already exist. The command prints the key ID that peers
will see in `signer_key_id`.

Give the followers the public half only: copy the `public_key` field of the
key file (hex) into their `bridge_relayers.txt`, with a comment naming the
relayer. Never copy the key file itself.

To rotate a key, add the new public key to every follower, switch the relayer
to the new key file, then remove the old public key. Events in flight under
the old key are accepted until it is removed.

## 4. Counters

Remote events are counted, not logged one by one, so a flood cannot fill the
log. The counters are part of the dashboard's Prometheus metrics
(`GET /api/metrics/prometheus`, collector `bridge_relay`):

- `qsdm_bridge_relay_remote_events_total{result=...}`, where `result` is one
  of `applied`, `noop`, `malformed`, `no_trusted_keys`, `unsigned`,
  `untrusted_signer`, `bad_signature`, `stale` or `bad_transition`. Only
  `applied` changed local state.
- `qsdm_bridge_relay_trusted_keys`: the size of the allowlist.
- `qsdm_bridge_relay_signing`: 1 when local events are signed.

On a default node, a rising `no_trusted_keys` is expected while peers still
publish bridge events. On a follower, `untrusted_signer`, `bad_signature` or
`bad_transition` mean that someone is publishing events this node refuses.
Applied events are logged as
`[bridge-relay] applied <kind> from <origin> (signer <key ID prefix>)`.

## 5. Code

- `QSDM/source/pkg/bridge/relay_auth.go`: `RelayAuthConfig`, the signing
  bytes, sign and verify, freshness, `RelayStats`,
  `ParseBridgeRelayerPublicKey`, `BridgeKeyID`.
- `QSDM/source/pkg/bridge/relay.go`: the envelope fields,
  `NewP2PRelayWithAuth` (`NewP2PRelay` has no keys and no signer),
  `processRemote` (decode, verify, apply, count), the state checks in
  `handleRemoteEvent`, and signing in `publish`.
- `QSDM/source/pkg/config/bridge_relay.go`, plus small hooks in `config.go`
  and `config_toml.go`: parsing, environment overrides and `Validate`.
- `QSDM/source/cmd/qsdm/bridge_relay_auth.go`: config to `RelayAuthConfig`,
  the keys-file reader, loading the signer with the consensus key loader
  (a missing file is refused, never created), and the metrics. `main.go`
  builds the relay with it and registers the collector.

## 6. Tests

From `QSDM/source`, with the same settings as CI:

```
CGO_ENABLED=1 go test -count=1 -tags dilithium_circl ./pkg/bridge/ ./pkg/config/ ./cmd/qsdm/ ./pkg/networking/
```

- `pkg/bridge/relay_auth_test.go`: the default relay ignores signed and
  unsigned events; trusted signed events are applied (including the
  full-object status form the API publishes); unsigned, untrusted,
  relabelled-key-ID, tampered-payload, tampered-kind, garbage-signature and
  malformed events are rejected and counted; stale and future timestamps are
  rejected; no final state is created for unknown objects; no object is
  created in a later state; final states are not overwritten; existing objects
  are not replaced; `swap_refunded` is not applied; self-origin events are
  skipped; config validation works.
- `pkg/bridge/relay_live_test.go`: real libp2p hosts. Signed lock create and
  redeem, and signed swaps, reach a trusting node. A default node and a node
  trusting another key ignore a hostile node's signed and unsigned events
  (including a refund of an existing lock). In a three-node star, the hub
  forwards signed events without applying them, and the publisher does not
  apply its own echo.
- `pkg/networking/bridge_auth_test.go`: three nodes on the node's own libp2p
  setup (`SetupLibP2PWithPortBindAndKey`: signed GossipSub and the ban gater)
  with relays from `bridge.NewP2PRelayWithAuth`. A lock created through
  `LockAsset` and its redemption reach the trusting node. An untrusted
  signer's refund and fake lock do not. Nodes with no trusted keys apply
  nothing.
- `pkg/config/bridge_relay_test.go`: TOML and YAML parsing, unset keys stay
  empty, a bad duration, environment overrides, and `Validate` (bad hex, wrong
  length, negative age, consensus key path reuse).
- `cmd/qsdm/bridge_relay_auth_test.go`: the default is closed; inline and file
  keys are merged and deduplicated; bad or missing inputs are errors and a
  missing signer key is not created; the signer loads; the consensus key is
  refused as signer; the key ID equals the key's validator address; metrics.

## 7. Limits and remaining risks

- **Trust is delegated, not verified.** A trusted relayer key can still create
  `locked` locks and `initiated` swaps with any amount, and complete or redeem
  objects this node knows about. The checks bound *which transitions* happen.
  They do not prove that the redemption pre-image was revealed (it is stripped
  before gossip) or that assets moved on another chain. Keep the allowlist
  small and treat the relayer key like the bridge operator's authority.
- **Replay inside the age window.** Within `bridge_relay_max_event_age` a
  captured signed event can be replayed. Every transition only moves forward
  and creation never replaces an object, so a replay is a no-op. The exception
  is a lock or swap that was removed locally (for example, by restoring an
  older state file) and is recreated by a replayed `lock_created` or
  `swap_initiated` event. There is no per-event nonce cache.
- **Clock dependence.** Freshness needs roughly synchronised clocks (tolerance:
  the maximum age in the past, 2 minutes in the future).
- **The swap state machine from gossip is incomplete.** `swap_refunded` is
  still not applied from gossip (it never was). A follower can keep a swap as
  `initiated` or `participated` after the relayer refunded it, and a later
  `swap_completed` would then still apply. Local API calls are unaffected.
- **Unsigned publishing is still the default.** Nodes without a relayer key
  keep publishing unsigned events. That is harmless to enforcing peers, but it
  still uses gossip bandwidth. The relay is not disabled entirely, because
  local API flows still call it.
- **Local state files are trusted.** `LoadState` on startup trusts the JSON
  state file. Anything already persisted from the old, unauthenticated relay
  stays. Operators upgrading a node that was exposed should inspect or discard
  `qsdm_bridge_state.json`.
- **A data race that predates this change is not fixed here.** `LockAsset`
  sets `Status = locked` after releasing the map lock, and `GetLock` and
  `ListLocks` return shared pointers. The new tests synchronise on the relay's
  atomic counters to avoid it.
- **Key file format reuse.** The signer key file uses the consensus key format
  and loader, so loader error messages say "consensus signer key". Both the
  path and the key are checked to be different from the consensus key.
