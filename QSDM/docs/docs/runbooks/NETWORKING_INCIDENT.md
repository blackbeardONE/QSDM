# libp2p Peer-Graph — Operator Runbook

Two-mode runbook for the validator's libp2p peer graph.
Mode A catches **full islanding** (zero connected peers
for ≥5m); Mode B catches the more subtle case of
**peers-but-no-inbound-gossip** (one-way partition or a
silently-dropped pubsub subscription).

| Alert | Severity | Default `for:` | Anchor |
|---|---|---|---|
| `QSDMP2PNoPeers`               | warning | 5m  | [§3.1](#31-mode-a--qsdmp2pnopeers)               |
| `QSDMP2PGossipIngressStalled`  | warning | 10m | [§3.2](#32-mode-b--qsdmp2pgossipingressstalled)  |

> **What this runbook closes.** Before this commit,
> `pkg/networking` had **zero** Prometheus instrumentation.
> Peer count, gossip volume, and connection churn were all
> log-only. The legacy `Metrics.NetworkMessagesSent` /
> `NetworkMessagesRecv` fields existed but were never
> incremented from the libp2p path AND were never exposed
> in the OpenMetrics scrape. The new
> `qsdm_p2p_peers_connected{provider}` gauge plus the
> `qsdm_p2p_messages_total{direction}` counter pair (in
> `pkg/monitoring/network_metrics.go` + the
> `pkg/monitoring/netmetrics` leaf) close that gap.

---

## 1. Glossary (60-second skim)

- **libp2p peer** — a host the validator has a fully-
  established TCP/QUIC connection to; appears in
  `Network.Host.Network().Peers()`.
- **`qsdm_p2p_peers_connected{provider}`** — gauge,
  pulled at scrape time from the registered
  `NetworkProvider`. `provider="live"` when a libp2p host
  is wired in (production); `provider="none"` when no
  provider has been registered (unit-test or pre-init
  scrape). All alert queries filter to
  `provider="live"` to avoid false-firing on dev/test
  nodes.
- **`qsdm_p2p_messages_total{direction}`** — counter,
  push-incremented from the libp2p send/receive hot paths.
  `direction="in"` counts non-self pubsub messages
  received via `Subscription.Next()` (excludes self-loops).
  `direction="out"` counts successful `Topic.Publish()`
  invocations from `Network.Broadcast()`.
- **Pubsub topic** — `qsdm-transactions` is the canonical
  topic for transaction gossip. Other topics (BFT, PoL,
  evidence, PEX) ride the same libp2p host but are not
  currently distinguished in `qsdm_p2p_messages_total`.
- **NetworkProvider** — interface defined in
  `pkg/monitoring/netmetrics`; the libp2p Network
  registers itself as the provider on construction so the
  scrape can pull `PeerCount()` on demand without locking
  in a periodic ticker.

---

## 2. Pre-flight: where in the network stack is the failure?

```promql
qsdm_p2p_peers_connected{provider="live"}
```

- Value is `0` for ≥5m → Mode A, the validator is islanded.
- Value > 0 → check `rate(qsdm_p2p_messages_total{direction="in"}[10m])`
  for the same instance:
  - 0 for ≥10m → Mode B, peers exist but gossip ingress
    is silent.
  - Non-zero → no Mode-A or Mode-B incident; escalate to
    the protocol-layer runbooks
    ([`MINING_LIVENESS.md`](MINING_LIVENESS.md),
     [`SUBMESH_POLICY_INCIDENT.md`](SUBMESH_POLICY_INCIDENT.md))
    for application-level diagnosis.

---

## 3. Per-mode triage

### 3.1 Mode A — `QSDMP2PNoPeers`

**Severity:** warning. **Default `for:`** 5m.

**Fires when**: `qsdm_p2p_peers_connected{provider="live"} == 0`
sustained for ≥5m.

**Why this matters**: full islanding. The validator has no
inbound gossip and no outbound publish reachability; mining /
consensus participation is effectively dead until the peer
graph is restored.

**Triage**:

1. **Confirm the listener is bound and accepting**.
   On the node:
   ```sh
   ss -tnp | grep <libp2p-port>
   ```
   - No matching line → the libp2p host died but the
     process is still up. Restart the validator. If
     `Network.Close()` was called from a test/admin hook
     and never replaced, this is the smoking gun.
   - Line exists but in `LISTEN` only → no peers have
     dialed in. Move to step 2.
2. **Try a known-good peer dial in the reverse direction**.
   From a healthy peer, attempt to dial this host
   directly (use the validator's advertised libp2p
   multiaddr). If the dial is rejected → a firewall /
   network-policy change is the cause. If the dial
   succeeds but the alert keeps firing, the issue is in
   the bootstrap/discovery list (we don't know about any
   peers to dial outbound).
3. **Inspect bootstrap configuration**:
   - On dev/test deploys, if the only discovery mechanism
     is mDNS (see `SetupLibP2PWithPort` — it always
     starts mDNS), the validator needs a peer on the
     same broadcast domain. Single-host k8s pods or
     isolated VMs will hit this.
   - On production deploys, the bootstrap peer list
     should come from
     `pkg/networking/bootstrap.go` /
     `pkg/networking/pex.go`. Verify the configured
     peers are reachable.
4. **Cross-fleet check**:
   ```promql
   count(qsdm_p2p_peers_connected{provider="live"} == 0)
     /
   count(qsdm_p2p_peers_connected{provider="live"})
   ```
   - Close to 1 → fleet-wide outage (deploy-side bug,
     bootstrap-list rot, network-layer config push
     gone wrong).
   - Single-instance → host-specific issue (firewall,
     networking, host crash recovery).

**Companions:**
[`MINING_LIVENESS.md`](MINING_LIVENESS.md)
(`QSDMMiningChainStuck` will follow within ~30m if a
majority of validators hit Mode A together — full
chain stall),
[`QUARANTINE_INCIDENT.md`](QUARANTINE_INCIDENT.md)
(`QSDMQuarantineMajorityIsolated` distinguishes
"isolated by submesh policy" from "isolated by
network failure" — Mode A is the network-failure
side, Quarantine is the policy side).

---

### 3.2 Mode B — `QSDMP2PGossipIngressStalled`

**Severity:** warning. **Default `for:`** 10m.

**Fires when**: `qsdm_p2p_peers_connected{provider="live"} > 0`
**and** `rate(qsdm_p2p_messages_total{direction="in"}[10m]) == 0`
sustained for ≥10m.

**Why this matters**: peers are visible but no pubsub
messages are landing. This is more subtle than Mode A
because the host metrics look healthy (peers connected,
listener bound, dials succeeding) but the application
layer is starved of gossip.

**Triage**:

1. **Cross-check the quarantine sentinel**:
   - `QSDMQuarantineAnySubmesh` co-firing → our peer
     set has been muted by submesh policy. This is a
     policy decision, not a network failure. Read
     [`QUARANTINE_INCIDENT.md` §3.1](QUARANTINE_INCIDENT.md#31-mode-a--qsdmquarantineanysubmesh).
   - Not co-firing → the failure is at the libp2p /
     pubsub layer.
2. **Cheapest recovery first: bounce the validator**.
   `handleMessages` in `pkg/networking/libp2p.go`
   re-binds the qsdm-transactions topic subscription
   cleanly on startup. A wedged subscription (e.g. a
   context cancellation that left the handler
   nominally "running") clears on a fresh start. If the
   alert re-fires within 10m of restart, the failure
   is at the network layer, not the goroutine.
3. **Inspect the publish side from peers**. From a
   peer that's known to be publishing, watch
   `qsdm_p2p_messages_total{direction="out"}` rate:
   - Non-zero → peers are publishing; the failure is
     ingress-only. Likely an asymmetric firewall / NAT
     pinhole on the affected node.
   - Zero across the fleet → the entire network has
     stopped publishing. Check for a recent deploy that
     might have changed the topic name, the gossipsub
     parameters, or the subscription wiring.
4. **Topic-membership audit**. The current metric does
   not distinguish topics; if you suspect topic
   subscription drift, read
   `pkg/networking/libp2p.go` lines 81-89 (the topic
   join + subscribe sequence) and verify against
   recently-merged PRs that the topic name is still
   `qsdm-transactions`.

**Companions:**
[`QUARANTINE_INCIDENT.md`](QUARANTINE_INCIDENT.md)
(when peers are muted by policy rather than network
failure),
[`OPERATOR_HYGIENE_INCIDENT.md`](OPERATOR_HYGIENE_INCIDENT.md)
(`QSDMNoTransactionsStored` will follow Mode B within
~30m if the validator is also the only ingress path),
[`MINING_LIVENESS.md`](MINING_LIVENESS.md)
(`QSDMMiningMempoolBacklog` may follow if the local
validator is publishing locally-submitted txs but
none are arriving via gossip).

---

## 4. Cross-references

- `pkg/monitoring/netmetrics/netmetrics.go` — leaf
  package with `NetworkProvider` interface,
  `RegisterNetworkProvider`, `RecordGossipMessage`.
  Zero non-stdlib imports.
- `pkg/monitoring/network_metrics.go` — Prometheus
  exposition wrapper. Re-exports the netmetrics
  primitives at `monitoring.RegisterNetworkProvider` /
  `monitoring.RecordGossipMessage` for backwards-compat.
- `pkg/networking/libp2p.go` —
  `Network.PeerCount()` implements
  `netmetrics.NetworkProvider`; `SetupLibP2PWithPort`
  registers the provider; `handleMessages` and
  `Broadcast` push the direction counters.
- `QSDM/deploy/prometheus/alerts_qsdm.example.yml` —
  `qsdm-p2p` group with the two alerts.
- `QSDM/deploy/grafana/dashboards/qsdm-runbook-networking-incident.json`
  — auto-generated panel.
- [`QUARANTINE_INCIDENT.md`](QUARANTINE_INCIDENT.md)
  (submesh-policy isolation; the policy-side companion
  to Mode A's network-side islanding).
- [`MINING_LIVENESS.md`](MINING_LIVENESS.md)
  (downstream chain-stall risk when a majority of
  validators hit Mode A or Mode B together).
- [`OPERATOR_HYGIENE_INCIDENT.md`](OPERATOR_HYGIENE_INCIDENT.md)
  (`QSDMNoTransactionsStored` follows when the gossip
  layer is starved AND the local node has no
  ingress).
- [`SUBMESH_POLICY_INCIDENT.md`](SUBMESH_POLICY_INCIDENT.md)
  (when the network is fine but submesh-policy
  rejects are dominating — orthogonal to this
  runbook).
