package monitoring

// libp2p peer-graph Prometheus exposition.
//
// The actual registration/counter primitives live in the
// pkg/monitoring/netmetrics leaf so pkg/networking can
// import them without an import cycle (this root package
// imports pkg/networking via topology.go). This file is
// the exposition surface only.
//
// Re-exported for backwards-compat with callers that wired
// to pkg/monitoring directly:
//
//   monitoring.NetworkProvider        — interface
//   monitoring.RegisterNetworkProvider — wire the libp2p host
//   monitoring.CurrentNetworkProvider  — read the registered host
//   monitoring.RecordGossipMessage     — push direction counter
//   monitoring.GossipMessageCounts     — read direction counters
//   monitoring.GossipDirectionIn       — "in" label value
//   monitoring.GossipDirectionOut      — "out" label value

import "github.com/blackbeardONE/QSDM/pkg/monitoring/netmetrics"

// NetworkProvider is the interface networking implementations
// satisfy so monitoring can pull peer state on demand.
type NetworkProvider = netmetrics.NetworkProvider

// RegisterNetworkProvider wires the live libp2p Network into
// the monitoring layer. Idempotent.
func RegisterNetworkProvider(p NetworkProvider) {
	netmetrics.RegisterNetworkProvider(p)
}

// CurrentNetworkProvider returns the registered provider.
func CurrentNetworkProvider() NetworkProvider {
	return netmetrics.CurrentProvider()
}

// RecordGossipMessage increments
// qsdm_p2p_messages_total{direction=direction}.
func RecordGossipMessage(direction string) {
	netmetrics.RecordGossipMessage(direction)
}

// GossipMessageCounts returns (in, out).
func GossipMessageCounts() (in, out uint64) {
	return netmetrics.GossipCounts()
}

// Re-exported direction label values.
const (
	GossipDirectionIn  = netmetrics.DirectionIn
	GossipDirectionOut = netmetrics.DirectionOut
)

// networkPrometheusMetrics is the collector hook registered
// with the global scrape exporter. Emits:
//
//   qsdm_p2p_peers_connected{provider="live|none"}
//   qsdm_p2p_messages_total{direction="in"}
//   qsdm_p2p_messages_total{direction="out"}
//
// The provider="live|none" label lets alert queries filter
// to provider="live" so cold-start / unit-test nodes (which
// have no provider wired) don't false-fire the no-peers
// alert.
func networkPrometheusMetrics() []Metric {
	provider := CurrentNetworkProvider()

	peers := 0
	providerLabel := "none"
	if provider != nil {
		peers = provider.PeerCount()
		providerLabel = "live"
	}

	in, out := GossipMessageCounts()

	return []Metric{
		{
			Name:   "qsdm_p2p_peers_connected",
			Help:   "Currently-connected libp2p peer count, pulled at scrape time from the registered NetworkProvider. provider=\"live\" when a libp2p Network has been wired in (production); provider=\"none\" when no provider is registered (unit-test or pre-init scrape) — alert queries should filter to provider=\"live\" to avoid false-firing on test nodes.",
			Type:   MetricGauge,
			Value:  float64(peers),
			Labels: map[string]string{"provider": providerLabel},
		},
		{
			Name:   "qsdm_p2p_messages_total",
			Help:   "Pubsub messages observed at the libp2p layer by direction. direction=\"in\" counts non-self messages received; direction=\"out\" counts successful Broadcast() publishes. A flatlining direction=\"in\" while peers > 0 is a strong gossip-stall signal; see NETWORKING_INCIDENT.md.",
			Type:   MetricCounter,
			Value:  float64(in),
			Labels: map[string]string{"direction": GossipDirectionIn},
		},
		{
			Name:   "qsdm_p2p_messages_total",
			Help:   "Pubsub messages observed at the libp2p layer by direction. direction=\"out\" counts successful Broadcast() publishes; flatlining while local txs are produced indicates a publish-side stall.",
			Type:   MetricCounter,
			Value:  float64(out),
			Labels: map[string]string{"direction": GossipDirectionOut},
		},
	}
}
