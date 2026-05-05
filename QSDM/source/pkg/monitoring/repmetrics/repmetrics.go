// Package repmetrics is a zero-dependency leaf for peer-
// reputation observability. Mirrors the netmetrics leaf
// pattern: kept separate from pkg/monitoring (root) because
// pkg/monitoring already imports pkg/networking via
// topology.go and we cannot have pkg/networking import
// pkg/monitoring without an import cycle. The leaf has zero
// non-stdlib imports.
//
// pkg/networking.ReputationTracker implements ReputationProvider
// and registers itself by tracker name (e.g. "tx", "evidence")
// so the scrape can render multi-tracker gauges:
//
//   qsdm_reputation_peers_total{tracker="tx|evidence"}
//   qsdm_reputation_peers_banned{tracker="tx|evidence"}
//   qsdm_reputation_score_min{tracker="tx|evidence"}
//   qsdm_reputation_score_max{tracker="tx|evidence"}
//   qsdm_reputation_score_avg{tracker="tx|evidence"}
package repmetrics

import "sync"

// ReputationSnapshot is the per-tracker view the scrape pulls
// at evaluation time. All fields are computed under the
// tracker's read lock and returned by value, so the scrape
// renders a coherent snapshot without holding any lock past
// the call.
type ReputationSnapshot struct {
	TotalPeers  int     // total tracked peer records
	BannedPeers int     // currently in Banned=true state
	MinScore    float64 // lowest score across all tracked peers; 0 if no peers
	MaxScore    float64 // highest score across all tracked peers; 0 if no peers
	AvgScore    float64 // arithmetic mean across all tracked peers; 0 if no peers
}

// ReputationProvider is the interface networking implements
// so the monitoring layer can pull tracker state on demand.
// Implementations must be safe to call concurrently.
type ReputationProvider interface {
	Snapshot() ReputationSnapshot
}

var (
	mu        sync.RWMutex
	providers = map[string]ReputationProvider{}
)

// RegisterReputationProvider wires a tracker into the metrics
// layer under the given name. Names are user-visible label
// values on the emitted gauges, so keep them short and stable
// (current callers: "tx", "evidence"). Idempotent: a second
// call with the same name replaces the prior registration.
// Pass nil to unregister.
func RegisterReputationProvider(tracker string, p ReputationProvider) {
	mu.Lock()
	defer mu.Unlock()
	if p == nil {
		delete(providers, tracker)
		return
	}
	providers[tracker] = p
}

// Providers returns a copy of the current registration map.
// Exposed for the monitoring root package to render the
// scrape and for tests to verify wiring.
func Providers() map[string]ReputationProvider {
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]ReputationProvider, len(providers))
	for k, v := range providers {
		out[k] = v
	}
	return out
}
