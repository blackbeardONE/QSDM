package main

// HL2 WP-H metrics: the version 2 Guard's counters (legacymining.GuardStats),
// the Ledger's owner epoch in aggregate (OwnerEpochStats), and the S14b
// operator-key report. The collector is registered only with a version 2
// config, so a v1 (HL1) boot exports exactly the HL1 metrics.
//
// Label cardinality is bounded by construction: reject kinds and cooldown
// causes are fixed enums, the alarms are three, and nothing is labelled by
// owner. Per-owner amounts are in the logs (cooldown and epoch-cap lines)
// and in the offline hl-audit report.

import (
	"sort"
	"sync"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// hl2CooldownCauses are the per-owner cooldown causes (GuardStats keys),
// always exported so a rate() over them starts at zero.
var hl2CooldownCauses = []string{
	legacymining.CauseRateBurst,
	legacymining.CauseDuplicates,
	legacymining.CauseBadSubmissions,
}

// hl2OperatorKeysReport is the last S14b report (hl2HydrateOperatorKeys).
var hl2OperatorKeysReport struct {
	mu         sync.Mutex
	set        bool
	owners     int
	withoutKey int
	rep        legacymining.OperatorKeyReport
}

func hl2RecordOperatorKeys(owners, withoutKey int, rep legacymining.OperatorKeyReport) {
	r := &hl2OperatorKeysReport
	r.mu.Lock()
	r.set, r.owners, r.withoutKey, r.rep = true, owners, withoutKey, rep
	r.mu.Unlock()
}

// hl2IsV2 reports whether c runs a version 2 config.
func hl2IsV2(c *hl1CanaryParts) bool {
	return c.enabled() && c.guard.Config().Version == legacymining.ConfigVersion2
}

// hl2MetricsCollector exports the HL2 metrics of c. It returns no metrics
// unless c runs a version 2 config.
func hl2MetricsCollector(c *hl1CanaryParts) monitoring.MetricCollector {
	return func() []monitoring.Metric {
		if !hl2IsV2(c) {
			return nil
		}
		return hl2Metrics(c.guard.Stats(), c.ledger.OwnerEpochStats(), c.guard.Config())
	}
}

func hl2Metrics(gs legacymining.GuardStats, es legacymining.OwnerEpochStats, cfg legacymining.Config) []monitoring.Metric {
	counter := func(name, help string, v uint64, labels map[string]string) monitoring.Metric {
		return monitoring.Metric{Name: name, Help: help, Type: monitoring.MetricCounter, Value: float64(v), Labels: labels}
	}
	gauge := func(name, help string, v float64) monitoring.Metric {
		return monitoring.Metric{Name: name, Help: help, Type: monitoring.MetricGauge, Value: v}
	}
	var out []monitoring.Metric

	// Every reject kind, in kind order (zero included).
	for k := legacymining.KindAdmissionClosed; k <= legacymining.KindOwnerCooldown; k++ {
		out = append(out, counter("hl2_guard_rejections_total",
			"Submissions the v2 guard rejected, by reject kind (bad-operator-sig and not-enrolled are unattributable)",
			gs.Rejections[k.String()], map[string]string{"kind": k.String()}))
	}
	out = append(out, counter("hl2_guard_unattributable_total",
		"Precheck rejections made before an owner was known; they touch no owner's state", gs.Unattributable, nil))

	causes := append([]string(nil), hl2CooldownCauses...)
	for cause := range gs.CooldownsStarted {
		known := false
		for _, c := range hl2CooldownCauses {
			known = known || c == cause
		}
		if !known {
			causes = append(causes, cause)
		}
	}
	sort.Strings(causes[len(hl2CooldownCauses):])
	for _, cause := range causes {
		out = append(out, counter("hl2_guard_cooldowns_started_total",
			"Per-owner cooldowns started, by cause", gs.CooldownsStarted[cause], map[string]string{"cause": cause}))
	}
	out = append(out, gauge("hl2_guard_owners_in_cooldown", "Owners in a per-owner cooldown now", float64(gs.OwnersInCooldown)))
	for _, a := range []struct {
		name string
		v    uint64
	}{{"rate-burst", gs.RateBurstAlarms}, {"duplicates", gs.DuplicateAlarms}, {"unattributable", gs.UnattributableAlarms}} {
		out = append(out, counter("hl2_guard_alarms_total",
			"Log-only global alarms that replace HL1's ADMISSION_STOP triggers in v2", a.v, map[string]string{"alarm": a.name}))
	}

	out = append(out,
		gauge("hl2_owner_epoch", "Current owner epoch (8640-block windows from height 0)", float64(es.Epoch)),
		gauge("hl2_owner_epoch_cap_cell", "owner_epoch_cap_cell of the config", float64(cfg.OwnerEpochCapCell)),
		gauge("hl2_owner_epoch_owners", "Owners with a positive emitted amount in the current owner epoch", float64(es.Owners)),
		gauge("hl2_owner_epoch_owners_at_cap", "Owners at or above owner_epoch_cap_cell in the current owner epoch (admission held)", float64(es.AtCap)),
		gauge("hl2_owner_epoch_emitted_cell_sum", "CELL emitted to all owners in the current owner epoch (gross)", es.Total),
		gauge("hl2_owner_epoch_emitted_cell_max", "CELL emitted to the largest owner in the current owner epoch (gross)", es.Max),
	)

	r := &hl2OperatorKeysReport
	r.mu.Lock()
	if r.set {
		out = append(out,
			gauge("hl2_operator_keys_owners", "Enrollment owners at the S14b operator-key step", float64(r.owners)),
			gauge("hl2_operator_keys_without_key", "Enrollment owners without an operator key at S14b (they cannot pass require_operator_sig)", float64(r.withoutKey)),
			gauge("hl2_operator_keys_bad_rows", "operator_keys rows ignored at S14b because the key does not hash to the owner", float64(r.rep.BadRows)),
		)
	}
	r.mu.Unlock()
	return out
}
