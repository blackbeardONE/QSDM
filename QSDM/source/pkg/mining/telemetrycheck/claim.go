// Package telemetrycheck implements the Tier-2 advisory layer
// of the QSDM Reference Telemetry Oracle. The Oracle (see
// pkg/telemetry) publishes signed reference profiles of what
// real GPUs report to nvidia-smi; this package CONSUMES those
// profiles and uses them to scrutinise miners' claimed GPU
// specs at proof-acceptance time.
//
// IMPORTANT: This package is non-consensus by design.
// Verdicts surface as logs, /metrics counters, and a public
// /api/v1/mining/spec-anomalies endpoint — they do NOT cause
// proof rejection. Tier-3 enforcement (downgrade reward tier
// on persistent mismatch) is left to a separate package so a
// bug here can never tank the chain.
//
// Design separation:
//
//   pkg/telemetry          — publishes reference data
//   pkg/mining/telemetrycheck — compares claims vs reference
//   internal/v2wiring      — bootstraps the catalog at validator boot
//   internal/miningsvc     — connects the checker to proof acceptance
//
// What gets checked (all advisory):
//
//   1. GPU name presence: bundle.gpu_name should appear in
//      at least one catalog entry. If not → "unknown_sku"
//      (this is normal during early operation when the
//      catalog has not yet seen this hardware).
//   2. Compute capability consistency: the (gpu_name,
//      compute_cap) pair must agree with at least one
//      catalog entry. A "RTX 3050 with CC 9.0" claim flags
//      because no real 3050 reports CC 9.0.
//   3. Architecture consistency: the outer Attestation.GPUArch
//      ("ampere", "hopper", etc.) must be derivable from the
//      compute_cap the bundle reports. A "ampere" claim with
//      compute_cap 9.0 (Hopper) is impossible.
//   4. Driver version sanity: when the catalog has driver
//      versions for this SKU, the bundle's driver_ver should
//      either match an observed version or look like a
//      plausible same-family version (e.g. starts with the
//      same major). Today: weak check (just "vendor format" —
//      digits + dots), not a hard match, because driver
//      releases ship faster than the catalog observes them.
//
// What is NOT checked here (left for Tier-3 / future):
//
//   - Memory size, TDP, PCIe gen — these depend on bundle
//     extension fields the v1 hmac bundle does not carry.
//     Adding them to the bundle is a forward-compatible
//     change that lands in a follow-up commit.
//   - Live benchmark fingerprints. Requires the validator
//     to challenge the miner with a canonical kernel and
//     compare timing distributions; that's its own protocol.
//
// Concurrency: Catalog and Checker are safe for concurrent
// reads. Catalog mutation (Apply / Replace) is gated by an
// internal mutex; the checker holds only RLocks during
// Check, so a steady-state load + sporadic refresh is the
// expected workload pattern.
package telemetrycheck

import (
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/telemetry"
)

// Claim is the slimmed-down view of a v2 proof's attestation
// payload that the checker actually reads. Built by the
// per-attestation-type verifier (today: pkg/mining/attest/hmac)
// AFTER all consensus checks pass; the checker never sees a
// rejected proof.
//
// Field names mirror Bundle field names where possible so a
// glance at the struct is enough to remember what the
// nvidia-hmac-v1 bundle carries. Future attestation types
// (cc-v1) populate the same struct from their own payload.
type Claim struct {
	// AttestationType is the wire identifier — "nvidia-hmac-v1"
	// today, "nvidia-cc-v1" once Hopper attestation lands.
	// Used in the Verdict so an operator looking at /metrics
	// or /spec-anomalies can tell which family of GPU
	// produced the warning.
	AttestationType string

	// NodeID is the bundle's node_id. Lets the operator
	// trace anomalies back to the registered enrollment.
	NodeID string

	// GPUUUID is the bundle's gpu_uuid. Same caveat as
	// NodeID: identity, not capability.
	GPUUUID string

	// GPUName is the bundle's gpu_name (e.g.
	// "NVIDIA GeForce RTX 3050"). Trimmed/case-folded
	// at lookup time inside Catalog.
	GPUName string

	// GPUArch is the outer Attestation.GPUArch (e.g.
	// "ampere"). The verifier already enforces this is one
	// of the consensus-accepted strings; the checker just
	// uses it for Tier-2 sanity checks.
	GPUArch string

	// ComputeCap is the bundle's compute_cap (e.g. "8.6").
	// Empty string skips the compute-cap consistency rule.
	ComputeCap string

	// DriverVer is the bundle's driver_ver
	// (e.g. "576.28"). Empty string skips the driver-version
	// rule.
	DriverVer string

	// CUDAVer is the bundle's cuda_version (e.g. "12.9").
	// Reserved for a future rule; not currently checked.
	CUDAVer string

	// MinerAddr is the proof's miner_addr. Used in the
	// emitted SpecAnomaly receipt so an operator can tell
	// which wallet's submissions are flagged.
	MinerAddr string

	// Height is the proof's height. Used in receipts.
	Height uint64

	// SubmittedAt is the wall-clock unix-seconds when the
	// proof was accepted. Set by the caller (so tests can
	// inject a deterministic value).
	SubmittedAt int64
}

// canonicalGPUName returns a lowercase trimmed copy of n.
// Used as the lookup key inside Catalog so vendor capitalisation
// drift ("NVIDIA GeForce RTX 3050" vs "Nvidia GeForce RTX 3050"
// vs "NVIDIA RTX 3050") does not desync identical hardware.
func canonicalGPUName(n string) string {
	return strings.TrimSpace(strings.ToLower(n))
}

// Empty reports whether the claim has nothing meaningful to
// check. Lets the caller short-circuit before constructing a
// Verdict — saves a Lock acquisition on the hot path when
// the dispatcher fired with an attestation type that doesn't
// populate Claim fields yet.
func (c Claim) Empty() bool {
	return c.GPUName == "" && c.ComputeCap == "" &&
		c.DriverVer == "" && c.GPUUUID == ""
}

// claimMatchesObservation tests whether one observation is
// consistent with the claim. Used by Catalog to filter
// candidates. "Consistent" means:
//
//   - canonical names match exactly, OR
//   - both empty (the catalog entry was published without a
//     name — vanishingly rare but defensively allowed)
//
// Compute capability comparison is exact-string (NVIDIA
// publishes these with one decimal point e.g. "8.6", and the
// canonical form is byte-stable across drivers).
func claimMatchesObservation(c Claim, o telemetry.GPUObservation) bool {
	cn := canonicalGPUName(c.GPUName)
	on := canonicalGPUName(o.Name)
	if cn != "" && on != "" && cn != on {
		return false
	}
	if c.ComputeCap != "" && o.ComputeCapability != "" && c.ComputeCap != o.ComputeCapability {
		return false
	}
	return true
}
