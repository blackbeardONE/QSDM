package api

// handlers_spec_anomalies.go exposes the Tier-2 telemetry
// advisory output (pkg/mining/telemetrycheck) over the
// public HTTP API. The endpoint is read-only and returns
// the most-recent N spec-anomalies that fired during proof
// acceptance — mismatches and unknown-SKU events. It is
// distinct from /api/v1/receipts and the slash-receipt
// endpoint because spec anomalies are NON-CONSENSUS: they
// do not cause rejection, do not affect rewards (yet), and
// are advisory in nature.
//
// Wiring lives in cmd/qsdm/main.go: the validator
// constructs a SpecAnomaliesProbe (typically a closure
// that reads from a telemetrycheck.HMACAdapter) and calls
// SetSpecAnomaliesProbe. The HTTP route is registered in
// pkg/api/handlers.go alongside the other mining
// endpoints.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
)

// SpecAnomaliesProbe is what the validator implements
// (see cmd/qsdm/spec_check.go's specAnomaliesProbe). The
// API layer holds the interface, not a struct, so a
// future cluster mode could supply a multi-host probe
// without changing this file.
//
// Snapshot returns the current counters in the order the
// /metrics emitter expects: (catalog_total,
// catalog_signers, catalog_skus, checked, matched,
// mismatched, unknown_sku, skipped). uint64 across the
// board for wire stability.
//
// RecentAnomalies returns the newest n records (newest
// first). Implementations SHOULD cap n at the in-memory
// ring size; the handler also caps it server-side so a
// pathological "limit=2147483647" request can't blow the
// response buffer.
type SpecAnomaliesProbe interface {
	Snapshot() SpecAnomaliesSnapshot
	RecentAnomalies(n int) []SpecAnomalyView
}

// SpecPenaltyProbe is the optional Tier-3 hook. When the
// validator has opted into reward downgrade
// (QSDM_SPEC_PENALTY_ENABLED), this probe gives the public
// API access to the per-miner sliding-window state so a
// flagged miner can curl /api/v1/mining/penalty?addr=...
// and see exactly why their rewards dropped.
//
// AllPenaltySnapshots returns one PenaltyView per miner
// the engine has observed, sorted by miner_addr. Used by
// the dashboard list view + the /api/v1/mining/penalty
// (no-arg) snapshot.
//
// PenaltyForMiner returns the per-miner explanation, or
// (zero, false) when the miner is not known. The handler
// surfaces "not found" as 200 with a zero-state view —
// the dashboard treats that as "fully clean".
type SpecPenaltyProbe interface {
	PenaltyForMiner(addr string) (PenaltyView, bool)
	AllPenaltySnapshots() []PenaltyView
	PenalisedCount() int
	Config() PenaltyConfigView
}

// PenaltyView is the public-facing per-miner snapshot.
// Mirrors telemetrycheck.PenaltySnapshot but lives here
// so the wire layout is owned by the api package.
type PenaltyView struct {
	MinerAddr        string  `json:"miner_addr"`
	WindowSize       int     `json:"window_size"`
	WindowFilled     int     `json:"window_filled"`
	MismatchCount    int     `json:"mismatch_count"`
	UnknownSKUCount  int     `json:"unknown_sku_count"`
	MatchCount       int     `json:"match_count"`
	MismatchPct      float64 `json:"mismatch_pct"`
	ThresholdPct     float64 `json:"threshold_pct"`
	OverThreshold    bool    `json:"over_threshold"`
	BelowMinObs      bool    `json:"below_min_observations"`
	Multiplier       float64 `json:"multiplier"`
	LastObservedAt   int64   `json:"last_observed_at,omitempty"`
}

// PenaltyConfigView is the resolved Tier-3 config the
// validator advertises so the dashboard can label the
// threshold line on the graph.
type PenaltyConfigView struct {
	WindowSize           int     `json:"window_size"`
	MismatchThresholdPct float64 `json:"threshold_pct"`
	PenaltyMultiplier    float64 `json:"multiplier"`
	MinObservations      int     `json:"min_observations"`
}

// SpecAnomaliesSnapshot is the counter half of the
// public payload. Counts are cumulative since process
// start; ring size is the in-memory cap.
type SpecAnomaliesSnapshot struct {
	CatalogTotal       int    `json:"catalog_total_entries"`
	CatalogSigners     int    `json:"catalog_signers"`
	CatalogSKUs        int    `json:"catalog_skus"`
	Checked            uint64 `json:"checked_total"`
	Matched            uint64 `json:"matched_total"`
	Mismatched         uint64 `json:"mismatched_total"`
	UnknownSKU         uint64 `json:"unknown_sku_total"`
	Skipped            uint64 `json:"skipped_total"`
	RingCap            int    `json:"ring_cap"`
	RingSize           int    `json:"ring_size"`
	MismatchesByField  map[string]uint64 `json:"mismatches_by_field,omitempty"`
}

// SpecAnomalyView is the public-facing shape of one
// anomaly. Mirrors telemetrycheck.SpecAnomaly but lives
// in this package so the wire layout is owned here, not
// in the consensus tree.
type SpecAnomalyView struct {
	ObservedAt        int64    `json:"observed_at"`
	AttestationType   string   `json:"attestation_type"`
	NodeID            string   `json:"node_id"`
	GPUUUID           string   `json:"gpu_uuid"`
	GPUName           string   `json:"gpu_name"`
	GPUArch           string   `json:"gpu_arch"`
	ComputeCap        string   `json:"compute_cap"`
	DriverVer         string   `json:"driver_ver"`
	MinerAddr         string   `json:"miner_addr"`
	Height            uint64   `json:"height"`
	Verdict           string   `json:"verdict"`
	MismatchedFields  []string `json:"mismatched_fields,omitempty"`
	HasMajor          bool     `json:"has_major"`
	MatchedReferences []string `json:"matched_references,omitempty"`
}

// SpecAnomaliesResponse is the GET body. List + summary in
// one payload so the dashboard fetches once.
type SpecAnomaliesResponse struct {
	Snapshot  SpecAnomaliesSnapshot `json:"snapshot"`
	Anomalies []SpecAnomalyView     `json:"anomalies"`
}

// SpecAnomaliesMaxLimit is the server-side cap on ?limit=.
// Picked to keep response size below ~256 KB at typical
// record sizes. Exposed as a constant so the dashboard can
// hit the cap directly without round-tripping a default.
const SpecAnomaliesMaxLimit = 500

type specAnomaliesProbeHolder struct {
	mu    sync.RWMutex
	probe SpecAnomaliesProbe
}

var specAnomaliesProbeRegistry = &specAnomaliesProbeHolder{}

// SetSpecAnomaliesProbe installs (or removes, when
// probe==nil) the process-wide Tier-2 anomaly probe.
// Idempotent. Called once at validator boot from
// cmd/qsdm/main.go after buildSpecCheckWiring succeeds.
// Calling with nil disables the endpoint (returns 503).
func SetSpecAnomaliesProbe(probe SpecAnomaliesProbe) {
	specAnomaliesProbeRegistry.mu.Lock()
	defer specAnomaliesProbeRegistry.mu.Unlock()
	specAnomaliesProbeRegistry.probe = probe
}

func currentSpecAnomaliesProbe() SpecAnomaliesProbe {
	specAnomaliesProbeRegistry.mu.RLock()
	defer specAnomaliesProbeRegistry.mu.RUnlock()
	return specAnomaliesProbeRegistry.probe
}

// Same RWMutex-guarded slot pattern for the optional
// Tier-3 penalty probe. Kept separate from the anomalies
// probe so a deployment can run Tier-2 alone without
// having to nil-check inside the anomalies probe.
type specPenaltyProbeHolder struct {
	mu    sync.RWMutex
	probe SpecPenaltyProbe
}

var specPenaltyProbeRegistry = &specPenaltyProbeHolder{}

// SetSpecPenaltyProbe installs the process-wide Tier-3
// reward-downgrade probe. Idempotent. Nil disables the
// /api/v1/mining/penalty endpoint (returns 503). Wiring
// happens at validator boot only when both
// QSDM_SPEC_CHECK_ENABLED and QSDM_SPEC_PENALTY_ENABLED
// are truthy.
func SetSpecPenaltyProbe(probe SpecPenaltyProbe) {
	specPenaltyProbeRegistry.mu.Lock()
	defer specPenaltyProbeRegistry.mu.Unlock()
	specPenaltyProbeRegistry.probe = probe
}

func currentSpecPenaltyProbe() SpecPenaltyProbe {
	specPenaltyProbeRegistry.mu.RLock()
	defer specPenaltyProbeRegistry.mu.RUnlock()
	return specPenaltyProbeRegistry.probe
}

// SpecPenaltyResponse is the GET body for
// /api/v1/mining/penalty (without ?address). One miner
// per row, ordered by addr; plus the resolved policy
// config so the dashboard can draw the threshold line.
type SpecPenaltyResponse struct {
	Config           PenaltyConfigView `json:"config"`
	PenalisedCount   int               `json:"penalised_count"`
	TrackedMiners    int               `json:"tracked_miners"`
	Miners           []PenaltyView     `json:"miners"`
}

// SpecPenaltyHandler serves GET /api/v1/mining/penalty.
//
// Query params:
//
//	?address=<addr>  — return one miner. 200 always; an
//	                    unknown miner returns the zero
//	                    snapshot with multiplier=1.0.
//	(no params)      — return all tracked miners + config.
//
// 503 when the validator did not opt into Tier-3 via
// QSDM_SPEC_PENALTY_ENABLED.
func (h *Handlers) SpecPenaltyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	probe := currentSpecPenaltyProbe()
	if probe == nil {
		writeMiningUnavailable(w, "penalty probe not configured (set QSDM_SPEC_PENALTY_ENABLED=1)")
		return
	}

	if addr := r.URL.Query().Get("address"); addr != "" {
		view, _ := probe.PenaltyForMiner(addr)
		if view.MinerAddr == "" {
			view.MinerAddr = addr
		}
		if view.Multiplier == 0 {
			view.Multiplier = 1.0
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(view)
		return
	}

	all := probe.AllPenaltySnapshots()
	if all == nil {
		all = []PenaltyView{}
	}
	resp := SpecPenaltyResponse{
		Config:         probe.Config(),
		PenalisedCount: probe.PenalisedCount(),
		TrackedMiners:  len(all),
		Miners:         all,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// SpecAnomaliesHandler serves GET
// /api/v1/mining/spec-anomalies. Returns 503 when the
// validator has not opted into Tier-2 telemetry checking
// (the regular pre-Tier-2 posture; QSDM_SPEC_CHECK_ENABLED
// not set).
//
// Query params:
//
//	?limit=<n>   default 50, capped at SpecAnomaliesMaxLimit
//
// The response is always a JSON object (never a bare list)
// so future fields (filter args, error context) extend
// without breaking dashboard parsers.
func (h *Handlers) SpecAnomaliesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	probe := currentSpecAnomaliesProbe()
	if probe == nil {
		writeMiningUnavailable(w, "spec-anomalies probe not configured (set QSDM_SPEC_CHECK_ENABLED=1)")
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			http.Error(w, "limit must be a non-negative integer", http.StatusBadRequest)
			return
		}
		if v <= 0 {
			http.Error(w, "limit must be > 0", http.StatusBadRequest)
			return
		}
		if v > SpecAnomaliesMaxLimit {
			v = SpecAnomaliesMaxLimit
		}
		limit = v
	}
	resp := SpecAnomaliesResponse{
		Snapshot:  probe.Snapshot(),
		Anomalies: probe.RecentAnomalies(limit),
	}
	if resp.Anomalies == nil {
		resp.Anomalies = []SpecAnomalyView{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
