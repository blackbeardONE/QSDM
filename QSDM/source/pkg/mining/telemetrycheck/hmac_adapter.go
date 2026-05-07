package telemetrycheck

// hmac_adapter glues the pkg/mining/attest/hmac.Bundle wire
// type into our internal Claim shape, plus an Anomaly
// recorder ring buffer. Lives in this package (not in
// pkg/mining/attest/hmac) because:
//
//   - keeping all telemetry-check logic in one tree means
//     a future Tier-3 reward downgrade can find every
//     spec-anomaly path without crossing package
//     boundaries,
//   - the hmac package should not depend on this package;
//     the dependency direction is hmac → mining (consensus)
//     and (separately) hmac → ... → telemetrycheck (via the
//     callback the validator binary wires up at boot).
//
// This file deliberately re-imports pkg/mining/attest/hmac
// only at the type level (Bundle struct). No call to
// hmac.* is made from the checker hot path.

import (
	"sort"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
)

// HMACAdapter wraps a Checker so it can be plugged
// directly into a hmac.Verifier.OnAccept slot. It also
// owns an in-memory ring of recent anomalies that the
// validator's HTTP layer surfaces at
// /api/v1/mining/spec-anomalies.
//
// One adapter per validator. Safe for concurrent calls
// from any number of verifier goroutines.
type HMACAdapter struct {
	checker *Checker

	// ring is a fixed-capacity buffer of the most-recent
	// anomalies (any verdict that is NOT VerdictMatch and
	// NOT VerdictSkipped). FIFO eviction. Capacity sized
	// at construction; default 256 in NewHMACAdapter.
	mu       sync.RWMutex
	ring     []SpecAnomaly
	ringHead int
	ringSize int
	ringCap  int
}

// SpecAnomaly is what the operator endpoint serves. A
// flattened, JSON-stable record of one verdict that was
// neither match nor skipped. Field order matches what
// /api/v1/mining/spec-anomalies emits.
type SpecAnomaly struct {
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

// NewHMACAdapter constructs an adapter bound to checker.
// ringCap caps the in-memory anomaly buffer; pass 0 for
// the default of 256 (≈25 KB at 100 B/record).
func NewHMACAdapter(checker *Checker, ringCap int) *HMACAdapter {
	if checker == nil {
		panic("telemetrycheck.NewHMACAdapter: nil checker")
	}
	if ringCap <= 0 {
		ringCap = 256
	}
	return &HMACAdapter{
		checker: checker,
		ring:    make([]SpecAnomaly, ringCap),
		ringCap: ringCap,
	}
}

// OnHMACAccept is the function value to assign to
// hmac.Verifier.OnAccept (or to ProductionConfig.
// HMACOnAccept). Constructs a Claim from bundle + p,
// runs the checker, and stores anomalies in the ring.
//
// MUST NOT panic, MUST NOT block, MUST NOT error — these
// constraints are inherited from the OnAccept contract.
func (a *HMACAdapter) OnHMACAccept(bundle hmac.Bundle, p mining.Proof, now time.Time) {
	claim := Claim{
		AttestationType: p.Attestation.Type,
		NodeID:          bundle.NodeID,
		GPUUUID:         bundle.GPUUUID,
		GPUName:         bundle.GPUName,
		GPUArch:         p.Attestation.GPUArch,
		ComputeCap:      bundle.ComputeCap,
		DriverVer:       bundle.DriverVer,
		CUDAVer:         bundle.CUDAVersion,
		MinerAddr:       p.MinerAddr,
		Height:          p.Height,
		SubmittedAt:     now.Unix(),
	}
	verdict := a.checker.Check(claim)
	if verdict.Kind == VerdictMatch || verdict.Kind == VerdictSkipped {
		return
	}
	a.recordAnomaly(claim, verdict, now)
}

// Checker exposes the underlying *Checker. Used by the
// metrics emitter so the adapter is the one-stop wiring
// point for Tier-2 observability.
func (a *HMACAdapter) Checker() *Checker {
	return a.checker
}

// RecentAnomalies returns the most-recent N anomalies
// in newest-first order. cap N at the ring's capacity.
// Returned slice is a fresh copy — safe to mutate.
func (a *HMACAdapter) RecentAnomalies(n int) []SpecAnomaly {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if n <= 0 || a.ringSize == 0 {
		return nil
	}
	if n > a.ringSize {
		n = a.ringSize
	}
	out := make([]SpecAnomaly, 0, n)
	// ringHead points at the slot AFTER the newest entry.
	// Walk backwards.
	idx := a.ringHead
	for i := 0; i < n; i++ {
		idx--
		if idx < 0 {
			idx = a.ringCap - 1
		}
		out = append(out, a.ring[idx])
	}
	return out
}

// AnomalyCount returns the total number of anomalies
// observed (NOT capped to ring size — this counter never
// resets). Cheap, atomic-equivalent (delegates to checker
// counters).
func (a *HMACAdapter) AnomalyCount() uint64 {
	_, _, mismatched, unknown, _ := a.checker.Counters()
	return mismatched + unknown
}

// recordAnomaly appends a new SpecAnomaly to the ring,
// evicting the oldest if at capacity. Ordering inside
// MismatchedFields and MatchedReferences is sorted for
// reproducibility on the wire.
func (a *HMACAdapter) recordAnomaly(claim Claim, v Verdict, now time.Time) {
	mfs := v.MismatchedFields()
	if len(mfs) > 0 {
		sort.Strings(mfs)
	}
	refs := v.MatchedReferences
	if len(refs) > 0 {
		// already sorted by Catalog, but defensive
		sort.Strings(refs)
	}
	rec := SpecAnomaly{
		ObservedAt:        now.Unix(),
		AttestationType:   claim.AttestationType,
		NodeID:            claim.NodeID,
		GPUUUID:           claim.GPUUUID,
		GPUName:           claim.GPUName,
		GPUArch:           claim.GPUArch,
		ComputeCap:        claim.ComputeCap,
		DriverVer:         claim.DriverVer,
		MinerAddr:         claim.MinerAddr,
		Height:            claim.Height,
		Verdict:           string(v.Kind),
		MismatchedFields:  mfs,
		HasMajor:          v.HasMajor(),
		MatchedReferences: refs,
	}

	a.mu.Lock()
	a.ring[a.ringHead] = rec
	a.ringHead = (a.ringHead + 1) % a.ringCap
	if a.ringSize < a.ringCap {
		a.ringSize++
	}
	a.mu.Unlock()
}
