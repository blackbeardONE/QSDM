package main

// pq_only_wiring.go: the node-process surface of the Ed25519 retirement
// (pkg/chain/txsig.go and pkg/chain/stream_ed25519_retirement.go):
// qsdm_tx_gossip_ed25519_rejected_total and
// qsdm_stream_ed25519_retirement_height.

import (
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// pqOnlyMetricsCollector exports the Ed25519 SignedTx refusals and the
// configured stream session-key retirement height.
func pqOnlyMetricsCollector() monitoring.MetricCollector {
	return func() []monitoring.Metric {
		return []monitoring.Metric{
			{Name: "qsdm_tx_gossip_ed25519_rejected_total", Type: monitoring.MetricCounter,
				Help:  "SignedTx refused because Ed25519 transaction signatures are retired (ML-DSA-87 only)",
				Value: float64(chain.TxSigEd25519Rejected())},
			{Name: "qsdm_stream_ed25519_retirement_height", Type: monitoring.MetricGauge,
				Help:  "Configured height from which CELL stream opens (Ed25519 session keys) are refused (0 = not scheduled)",
				Value: float64(chain.StreamEd25519RetirementHeight())},
		}
	}
}
