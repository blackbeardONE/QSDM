// Package netmetrics is a zero-dependency leaf for libp2p
// peer-graph observability. It exists separately from the
// pkg/monitoring root package because pkg/monitoring already
// imports pkg/networking (for TopologyMonitor) and we cannot
// have pkg/networking import pkg/monitoring without an import
// cycle. Splitting the registration + push counters into a
// leaf means pkg/networking depends only on this leaf, and
// the root pkg/monitoring depends on this leaf too — fan-in
// rather than a cycle.
//
// The leaf has zero non-stdlib imports.
package netmetrics

import (
	"sync"
	"sync/atomic"
)

// NetworkProvider is implemented by pkg/networking.Network.
// PeerCount returns the current count of fully-connected
// libp2p peers (NOT including disconnected/reconnecting).
// Implementations must be safe to call concurrently.
type NetworkProvider interface {
	PeerCount() int
}

var (
	providerMu sync.RWMutex
	provider   NetworkProvider
)

// RegisterNetworkProvider wires the live libp2p host into
// the metrics layer. Idempotent — a second call replaces
// the first, useful for test re-init. Pass nil to detach.
func RegisterNetworkProvider(p NetworkProvider) {
	providerMu.Lock()
	defer providerMu.Unlock()
	provider = p
}

// CurrentProvider returns the registered provider, or nil.
// Exposed so the monitoring root package can render the
// scrape and so tests can verify wiring.
func CurrentProvider() NetworkProvider {
	providerMu.RLock()
	defer providerMu.RUnlock()
	return provider
}

// Direction labels for qsdm_p2p_messages_total.
const (
	DirectionIn  = "in"
	DirectionOut = "out"
)

var (
	messagesIn  atomic.Uint64
	messagesOut atomic.Uint64
)

// RecordGossipMessage increments the per-direction counter.
// Unknown directions silently drop (defensive against
// callers passing arbitrary strings).
func RecordGossipMessage(direction string) {
	switch direction {
	case DirectionIn:
		messagesIn.Add(1)
	case DirectionOut:
		messagesOut.Add(1)
	}
}

// GossipCounts returns (in, out). Snapshot-consistent —
// the two loads can race relative to each other but each
// individual value is atomic.
func GossipCounts() (in, out uint64) {
	return messagesIn.Load(), messagesOut.Load()
}
