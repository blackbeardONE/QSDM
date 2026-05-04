package monitoring

// Stub-active metrics: bridges the leaf pkg/monitoring/stubactive
// registry into the OpenMetrics scrape surface as
// qsdm_stub_active{kind="..."}.
//
// Why a bridge instead of stubactive owning the metric directly?
// stubactive is a leaf package with zero external imports so any
// stub file (in pkg/consensus, pkg/crypto, pkg/wallet, …) can
// import it without creating an import cycle through the
// monitoring → mining → consensus chain. The metric exposition
// itself uses pkg/monitoring's own Metric type, so it lives here.
//
// The gauge surfaces every kind in stubactive.AllKinds() on
// every scrape (value 0 or 1) so the alerting expression
// `qsdm_stub_active == 1` evaluates against a populated time
// series rather than missing-data — a "Stub active for kind X"
// alert that depends on a metric appearing only AFTER the stub
// is loaded has the obvious bootstrap problem (the alert can't
// fire if the metric was never created).

import (
	"github.com/blackbeardONE/QSDM/pkg/monitoring/stubactive"
)

func stubActiveMetrics() []Metric {
	snap := stubactive.Snapshot()
	out := make([]Metric, 0, len(snap))
	for _, kind := range stubactive.AllKinds() {
		v := snap[kind]
		out = append(out, Metric{
			Name: "qsdm_stub_active",
			Help: "1 when a stub-shipped code path for the given kind is active in the running binary; 0 otherwise. Stubs are intentional but not safe in production. See QSDM/docs/docs/runbooks/STUB_DEPLOYMENT_INCIDENT.md for kind-specific remediation (rebuild with CGO, switch the CC verifier, register a real slashing evidence verifier, etc.).",
			Type: MetricGauge,
			Value: float64(v),
			Labels: map[string]string{"kind": kind},
		})
	}
	// Forward-compatibility: any kind registered at runtime that
	// stubactive's canonical AllKinds() doesn't know about (e.g. a
	// future stub) still surfaces in the snapshot. Emit those too,
	// so a new stub becomes visible without simultaneously editing
	// AllKinds().
	for kind, v := range snap {
		if !canonicalKind(kind) {
			out = append(out, Metric{
				Name: "qsdm_stub_active",
				Help: "1 when a stub-shipped code path for the given kind is active in the running binary; 0 otherwise.",
				Type: MetricGauge,
				Value: float64(v),
				Labels: map[string]string{"kind": kind},
			})
		}
	}
	return out
}

func canonicalKind(k string) bool {
	for _, c := range stubactive.AllKinds() {
		if c == k {
			return true
		}
	}
	return false
}
