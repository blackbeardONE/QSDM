package api

import (
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// These tests poke TrustAggregator's internal cache directly rather
// than driving Refresh(), because the collector's job is to *surface*
// whatever Summary() returns — the aggregator's merging / freshness
// logic is exhaustively covered by handlers_trust_test.go and we don't
// want to rehearse it here. Direct injection keeps the collector
// contract sharp: "for any cached summary, emit exactly these gauges
// with exactly these values".

// metricByName walks a []monitoring.Metric slice and returns the first
// metric with the requested name. Fails the test if not found.
func metricByName(t *testing.T, ms []monitoring.Metric, name string) monitoring.Metric {
	t.Helper()
	for _, m := range ms {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("expected metric %q in collector output; got %d metrics", name, len(ms))
	return monitoring.Metric{}
}

// fixedAggregator constructs a TrustAggregator whose cached fields are
// prepopulated — no Refresh() is invoked. The test is in package api
// so the unexported `cached` and `warm` fields are directly settable.
// If the struct layout ever changes, this helper breaks loudly, which
// is the right failure mode: the collector must keep up with the
// aggregator's storage.
func fixedAggregator(summary TrustSummary, warm bool) *TrustAggregator {
	agg := &TrustAggregator{cached: summary}
	if warm {
		agg.warm.Store(true)
	}
	return agg
}

func TestTrustMetricsCollector_NilAggregatorYieldsEmpty(t *testing.T) {
	// The collector must tolerate a disabled-trust node (agg==nil) by
	// emitting *no* metrics, not a pile of zero gauges. Zeroes would
	// falsely imply the floor assertions have a denominator.
	if ms := TrustMetricsCollector(nil)(); len(ms) != 0 {
		t.Fatalf("expected empty slice for nil aggregator; got %d metrics", len(ms))
	}
}

func TestTrustMetricsCollector_BasicShape(t *testing.T) {
	lastAt := "2026-04-23T12:25:48Z"
	checkedAt := "2026-04-23T12:30:35Z"
	summary := TrustSummary{
		Attested:         3,
		TotalPublic:      4,
		Ratio:            0.75,
		FreshWithin:      "15m0s",
		LastAttestedAt:   &lastAt,
		LastCheckedAt:    checkedAt,
		NGCServiceStatus: "healthy",
		ScopeNote:        "whatever",
	}
	ms := TrustMetricsCollector(fixedAggregator(summary, true))()

	// Exactly the seven gauges the collector documents, no more, no
	// less. If someone adds a new gauge they must also extend this
	// test so CI fails loudly before the Grafana dashboard drifts.
	expected := []string{
		"qsdm_trust_attested",
		"qsdm_trust_total_public",
		"qsdm_trust_ratio",
		"qsdm_trust_ngc_service_healthy",
		"qsdm_trust_last_attested_seconds",
		"qsdm_trust_last_checked_seconds",
		"qsdm_trust_warm",
	}
	if len(ms) != len(expected) {
		names := make([]string, len(ms))
		for i, m := range ms {
			names[i] = m.Name
		}
		t.Fatalf("want %d metrics, got %d: %v", len(expected), len(ms), names)
	}
	for _, name := range expected {
		m := metricByName(t, ms, name)
		if m.Type != monitoring.MetricGauge {
			t.Errorf("metric %q must be a gauge; got %q", name, m.Type)
		}
		if m.Help == "" {
			t.Errorf("metric %q missing HELP text", name)
		}
	}

	if got := metricByName(t, ms, "qsdm_trust_attested").Value; got != 3 {
		t.Errorf("attested = %v, want 3", got)
	}
	if got := metricByName(t, ms, "qsdm_trust_total_public").Value; got != 4 {
		t.Errorf("total_public = %v, want 4", got)
	}
	if got := metricByName(t, ms, "qsdm_trust_ratio").Value; got != 0.75 {
		t.Errorf("ratio = %v, want 0.75", got)
	}
	if got := metricByName(t, ms, "qsdm_trust_ngc_service_healthy").Value; got != 1 {
		t.Errorf("ngc_service_healthy = %v, want 1 for healthy status", got)
	}
	if got := metricByName(t, ms, "qsdm_trust_warm").Value; got != 1 {
		t.Errorf("warm = %v, want 1", got)
	}

	// RFC3339 → unix seconds round-trip for last_attested_seconds.
	wantLast, _ := time.Parse(time.RFC3339, lastAt)
	if got := metricByName(t, ms, "qsdm_trust_last_attested_seconds").Value; got != float64(wantLast.Unix()) {
		t.Errorf("last_attested_seconds = %v, want %v", got, wantLast.Unix())
	}
	wantChecked, _ := time.Parse(time.RFC3339, checkedAt)
	if got := metricByName(t, ms, "qsdm_trust_last_checked_seconds").Value; got != float64(wantChecked.Unix()) {
		t.Errorf("last_checked_seconds = %v, want %v", got, wantChecked.Unix())
	}
}

func TestTrustMetricsCollector_NGCServiceStatusMapping(t *testing.T) {
	for _, status := range []string{"degraded", "outage", "", "SomethingElse"} {
		summary := TrustSummary{NGCServiceStatus: status}
		ms := TrustMetricsCollector(fixedAggregator(summary, true))()
		if got := metricByName(t, ms, "qsdm_trust_ngc_service_healthy").Value; got != 0 {
			t.Errorf("status=%q: ngc_service_healthy = %v, want 0", status, got)
		}
	}

	// Only the literal string "healthy" flips it to 1. Any
	// capitalisation drift (e.g. "Healthy") must NOT count — matches
	// the §8.5.3 enum which is case-sensitive lowercase.
	summary := TrustSummary{NGCServiceStatus: "healthy"}
	ms := TrustMetricsCollector(fixedAggregator(summary, true))()
	if got := metricByName(t, ms, "qsdm_trust_ngc_service_healthy").Value; got != 1 {
		t.Errorf("status=healthy: ngc_service_healthy = %v, want 1", got)
	}
}

func TestTrustMetricsCollector_WarmFlipsAfterRefresh(t *testing.T) {
	// Pre-warm: all gauges must be 0 so any Grafana alert gated on
	// qsdm_trust_warm == 1 stays silent through a restart.
	agg := fixedAggregator(TrustSummary{}, false)
	pre := TrustMetricsCollector(agg)()
	if metricByName(t, pre, "qsdm_trust_warm").Value != 0 {
		t.Fatal("qsdm_trust_warm should be 0 before warm-up completes")
	}

	// Post-warm: even with an all-zero summary (no attestations yet),
	// warm must be 1 so alerts can distinguish "no data ever" from
	// "zero attested".
	agg.warm.Store(true)
	post := TrustMetricsCollector(agg)()
	if metricByName(t, post, "qsdm_trust_warm").Value != 1 {
		t.Fatal("qsdm_trust_warm should be 1 after first Refresh()")
	}
	if metricByName(t, post, "qsdm_trust_attested").Value != 0 {
		t.Fatal("attested should still be 0 when summary is zero-valued")
	}
}

func TestTrustMetricsCollector_NilLastAttestedYieldsZero(t *testing.T) {
	// last_attested_at is *string per §8.5.3 — nil means "no
	// attestation has ever been seen". The collector must render this
	// as 0, not crash and not emit some sentinel like math.NaN that
	// would poison Grafana rate() queries.
	summary := TrustSummary{LastAttestedAt: nil}
	ms := TrustMetricsCollector(fixedAggregator(summary, true))()
	if got := metricByName(t, ms, "qsdm_trust_last_attested_seconds").Value; got != 0 {
		t.Errorf("last_attested_seconds = %v, want 0 for nil LastAttestedAt", got)
	}
}

func TestTrustMetricsCollector_GarbledTimestampsYieldZero(t *testing.T) {
	// Parse failures must degrade gracefully. A corrupted cache
	// (impossible in normal operation, but defensible) should not
	// panic or surface a nonsense numeric value.
	garbage := "not-an-rfc3339-string"
	summary := TrustSummary{
		LastAttestedAt: &garbage,
		LastCheckedAt:  garbage,
	}
	ms := TrustMetricsCollector(fixedAggregator(summary, true))()
	if got := metricByName(t, ms, "qsdm_trust_last_attested_seconds").Value; got != 0 {
		t.Errorf("last_attested_seconds should be 0 on parse failure; got %v", got)
	}
	if got := metricByName(t, ms, "qsdm_trust_last_checked_seconds").Value; got != 0 {
		t.Errorf("last_checked_seconds should be 0 on parse failure; got %v", got)
	}
}
