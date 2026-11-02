package main

// poe_wiring.go: the node-process surfaces of Proof-of-Entanglement (the
// consensus rules themselves live in pkg/chain/poe.go):
//
//   - GET /api/v1/chain/parents reads recent committed transaction IDs from
//     the block producer, clamped to the durable tip like every other chain
//     read surface (HL1 W5), so a wallet never names a block that a crash
//     could still discard;
//   - qsdm_poe_* metrics: enforcement rejections by reason, the shadow count
//     of what enforcement would reject while an activation height is
//     configured but not reached, refused external blocks, and the size of
//     the committed-history index.

import (
	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/blackbeardONE/QSDM/pkg/poe"
)

// poeParentsProbe adapts the block producer to api.PoEParentSource.
type poeParentsProbe struct {
	producer *chain.BlockProducer
	durable  *hl1DurableTip
}

func (p poeParentsProbe) RecentParents(n int) (uint64, []api.PoEParentView, bool) {
	if p.producer == nil {
		return 0, nil, false
	}
	tip, ok := p.durable.Load()
	if !ok {
		return 0, nil, false
	}
	refs := p.producer.PoEParentCandidates(tip, n)
	out := make([]api.PoEParentView, 0, len(refs))
	for _, r := range refs {
		out = append(out, api.PoEParentView{ID: r.ID, Height: r.Height})
	}
	return tip, out, true
}

// poeMetricsCollector exports the PoE counters. Every reason is exported
// (zero included) so rate() over them starts at zero.
func poeMetricsCollector(p *chain.BlockProducer) monitoring.MetricCollector {
	return func() []monitoring.Metric {
		s := chain.PoEStats()
		counter := func(name, help string, v uint64, labels map[string]string) monitoring.Metric {
			return monitoring.Metric{Name: name, Help: help, Type: monitoring.MetricCounter, Value: float64(v), Labels: labels}
		}
		gauge := func(name, help string, v float64) monitoring.Metric {
			return monitoring.Metric{Name: name, Help: help, Type: monitoring.MetricGauge, Value: v}
		}
		out := []monitoring.Metric{
			gauge("qsdm_poe_activation_height", "Configured Proof-of-Entanglement activation height (0 = not scheduled)", float64(s.ActivationHeight)),
			gauge("qsdm_poe_history_ids", "Committed transaction IDs in the PoE reference-window index", float64(p.PoEHistorySize())),
			counter("qsdm_poe_shadow_checked_total", "Admitted wallet transfers evaluated against the PoE rules before activation", s.ShadowChecked, nil),
			counter("qsdm_poe_external_blocks_refused_total", "External blocks refused because a wallet transfer broke a PoE rule", s.ExternalBlocksRefused, nil),
			counter("qsdm_poe_history_rebuilds_total", "Times the PoE reference-window index was rebuilt from the chain", s.HistoryRebuilds, nil),
		}
		for _, reason := range poe.Reasons {
			out = append(out, counter("qsdm_poe_rejected_total",
				"Wallet transfers refused at admission, dropped in production, or invalidating an external block, by PoE rule",
				s.Rejected[reason], map[string]string{"reason": reason}))
		}
		for _, reason := range poe.Reasons {
			out = append(out, counter("qsdm_poe_shadow_would_reject_total",
				"Admitted wallet transfers that would fail the PoE rules once active, by rule",
				s.ShadowWouldReject[reason], map[string]string{"reason": reason}))
		}
		return out
	}
}
