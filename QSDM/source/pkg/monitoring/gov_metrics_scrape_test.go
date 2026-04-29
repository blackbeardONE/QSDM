package monitoring

// gov_metrics_scrape_test.go: locks down that the
// authority-rotation counters and gauge defined in
// gov_metrics.go are actually emitted by corePrometheusMetrics()
// — the bug we caught in 2026-04-29 was exactly this kind of
// silent gap (record functions wired, but the scrape exposition
// never iterated over GovAuthority*Labeled() / didn't add the
// gauge). A future refactor that drops one of these calls will
// trip this test rather than going undetected until an
// alert silently never fires.

import (
	"strings"
	"testing"
)

// TestCorePrometheusMetrics_EmitsGovAuthoritySeries verifies
// every series this group is alerted on
// (alerts_qsdm.example.yml::qsdm-v2-governance) appears in the
// scrape output after at least one record-call.
func TestCorePrometheusMetrics_EmitsGovAuthoritySeries(t *testing.T) {
	// Drive each per-op counter once. We do this BEFORE
	// calling corePrometheusMetrics so the labelled snapshots
	// actually carry rows. Without these calls, the labelled
	// accessors return empty slices and the for-range loops
	// below would emit nothing — which is correct behaviour
	// (empty counters don't appear), but means the test must
	// prime the state.
	RecordGovAuthorityVoted("add")
	RecordGovAuthorityVoted("remove")
	RecordGovAuthorityCrossed("add")
	RecordGovAuthorityActivated("add", 3)

	// Reset the gauge to a known non-default value so the
	// "qsdm_gov_authority_count" assertion is conclusive even
	// in test runs where the package init or other tests left
	// the gauge sitting at its default zero.
	SetAuthorityCountGauge(7)

	metrics := corePrometheusMetrics()

	wantNames := []string{
		"qsdm_gov_authority_voted_total",
		"qsdm_gov_authority_crossed_total",
		"qsdm_gov_authority_activated_total",
		"qsdm_gov_authority_count",
	}
	got := make(map[string]bool, len(metrics))
	for _, m := range metrics {
		got[m.Name] = true
	}
	for _, w := range wantNames {
		if !got[w] {
			t.Errorf("metric %q missing from corePrometheusMetrics(); "+
				"alerts_qsdm.example.yml::qsdm-v2-governance "+
				"depends on this name", w)
		}
	}
}

// TestCorePrometheusMetrics_GovAuthorityCountIsGauge locks the
// metric type for the count series — a regression that emitted
// it as a counter would still pass the rule check but break the
// `< 2` alert (counter values monotonically rise, so `< 2`
// would never re-fire after the first crossing).
func TestCorePrometheusMetrics_GovAuthorityCountIsGauge(t *testing.T) {
	SetAuthorityCountGauge(5)
	metrics := corePrometheusMetrics()
	for _, m := range metrics {
		if m.Name != "qsdm_gov_authority_count" {
			continue
		}
		if m.Type != MetricGauge {
			t.Errorf("qsdm_gov_authority_count type = %v, want MetricGauge "+
				"(alert QSDMGovAuthorityCountTooLow uses `< 2` which "+
				"requires a gauge)", m.Type)
		}
		if m.Value != 5 {
			t.Errorf("qsdm_gov_authority_count value = %v, want 5", m.Value)
		}
		return
	}
	t.Fatal("qsdm_gov_authority_count not present in corePrometheusMetrics() output")
}

// TestCorePrometheusMetrics_GovAuthorityOpLabelPresent locks
// that the per-op counters carry the `op` label the alert
// templates reference via {{ $labels.op }}.
func TestCorePrometheusMetrics_GovAuthorityOpLabelPresent(t *testing.T) {
	RecordGovAuthorityVoted("add")
	RecordGovAuthorityVoted("remove")

	metrics := corePrometheusMetrics()
	seen := map[string]bool{} // op label values seen on the voted counter
	for _, m := range metrics {
		if m.Name != "qsdm_gov_authority_voted_total" {
			continue
		}
		if op, ok := m.Labels["op"]; ok {
			seen[op] = true
		}
	}
	for _, want := range []string{"add", "remove"} {
		if !seen[want] {
			t.Errorf("qsdm_gov_authority_voted_total{op=%q} not found "+
				"(alert QSDMGovAuthorityVoteRecorded references "+
				"{{ $labels.op }})", want)
		}
	}
}

// TestCorePrometheusMetrics_EmitsArchspoofAndHashrateSeries
// covers the §4.6 attestation alert dependencies with the same
// shape as the gov test above. arch labels and reason labels
// are referenced by alert annotations; a wiring regression
// would silently break the templates, not fail any rule check.
func TestCorePrometheusMetrics_EmitsArchspoofAndHashrateSeries(t *testing.T) {
	t.Cleanup(ResetArchcheckMetricsForTest)
	ResetArchcheckMetricsForTest()

	// Drive at least one increment per labelled counter so the
	// snapshot accessor returns a row. The labelled accessors
	// for these counters always return a fixed set of rows
	// (see archcheck_metrics.go ArchSpoofRejectedLabeled /
	// HashrateRejectedLabeled — they iterate over the closed
	// reason / arch enum), so a zero value is still emitted —
	// but the test is more obviously hermetic if we drive the
	// counters first.
	RecordArchSpoofRejected(ArchSpoofRejectReasonUnknownArch)
	RecordArchSpoofRejected(ArchSpoofRejectReasonGPUNameMismatch)
	RecordArchSpoofRejected(ArchSpoofRejectReasonCCSubjectMismatch)
	RecordHashrateRejected("hopper")
	RecordHashrateRejected("ada-lovelace")

	metrics := corePrometheusMetrics()

	// Assert names exist.
	gotName := make(map[string]bool)
	for _, m := range metrics {
		gotName[m.Name] = true
	}
	for _, w := range []string{
		"qsdm_attest_archspoof_rejected_total",
		"qsdm_attest_hashrate_rejected_total",
	} {
		if !gotName[w] {
			t.Errorf("metric %q missing from corePrometheusMetrics() "+
				"(alerts_qsdm.example.yml::qsdm-v2-attest-* depends on this name)", w)
		}
	}

	// Assert the alert-template labels are present on at least
	// one row of each counter.
	gotReasons := make(map[string]bool)
	gotArches := make(map[string]bool)
	for _, m := range metrics {
		if m.Name == "qsdm_attest_archspoof_rejected_total" {
			if r, ok := m.Labels["reason"]; ok {
				gotReasons[r] = true
			}
		}
		if m.Name == "qsdm_attest_hashrate_rejected_total" {
			if a, ok := m.Labels["arch"]; ok {
				gotArches[a] = true
			}
		}
	}
	wantReasons := []string{
		ArchSpoofRejectReasonUnknownArch,
		ArchSpoofRejectReasonGPUNameMismatch,
		ArchSpoofRejectReasonCCSubjectMismatch,
	}
	for _, r := range wantReasons {
		if !gotReasons[r] {
			t.Errorf("qsdm_attest_archspoof_rejected_total{reason=%q} "+
				"not found in scrape output", r)
		}
	}
	wantArches := []string{"hopper", "ada-lovelace"}
	for _, a := range wantArches {
		if !gotArches[a] {
			t.Errorf("qsdm_attest_hashrate_rejected_total{arch=%q} "+
				"not found in scrape output", a)
		}
	}
}

// TestPrometheusExposition_GovAuthorityRendersOpenMetrics is a
// belt-and-braces guard: the OpenMetrics text format itself is
// the actual operator-visible artefact, so we render once and
// assert the new series appear in the text. Catches a
// regression in the formatter (e.g. dropped labels) that the
// per-row tests above wouldn't see.
func TestPrometheusExposition_GovAuthorityRendersOpenMetrics(t *testing.T) {
	RecordGovAuthorityVoted("add")
	SetAuthorityCountGauge(3)

	out := PrometheusExposition()
	for _, want := range []string{
		`qsdm_gov_authority_voted_total{op="add"}`,
		`qsdm_gov_authority_count`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("PrometheusExposition() missing %q\nfull output:\n%s",
				want, out)
		}
	}
}
