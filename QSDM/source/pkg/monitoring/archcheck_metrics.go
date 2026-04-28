package monitoring

// archcheck_metrics.go: counters for the v2 attestation
// arch-spoof rejection layer (MINING_PROTOCOL_V2 §4.6).
//
// Two distinct counter families:
//
//   qsdm_attest_archspoof_rejected_total{reason}
//
//     Increments on every proof rejected by the
//     pkg/mining/attest/archcheck validators. Reason values:
//       - "unknown_arch":      Attestation.GPUArch not in the
//                              closed-enum allowlist (or
//                              empty post-fork).
//       - "gpu_name_mismatch": HMAC bundle's gpu_name does not
//                              match any pattern for the
//                              claimed arch (the "lazy spoof"
//                              catch).
//
//   qsdm_attest_hashrate_rejected_total{arch}
//
//     Increments on every proof rejected by
//     archcheck.ValidateClaimedHashrate. Arch label values are
//     the canonical Architecture names (hopper, blackwell,
//     ada-lovelace, ampere, turing). Bounded cardinality.
//
// Cardinality: ≤ 2 + 5 = 7 distinct (counter, label) pairs.
// Well under any Prometheus best-practice ceiling.

import "sync/atomic"

// ---------- arch-spoof rejection ----------

var (
	archSpoofRejectUnknownArch     atomic.Uint64
	archSpoofRejectGPUNameMismatch atomic.Uint64
)

// Archspoof reject reason tags. Kept narrow so cardinality
// stays bounded and reasons map 1:1 to the rejection branches
// in pkg/mining/verifier.go and pkg/mining/attest/hmac/verifier.go.
const (
	ArchSpoofRejectReasonUnknownArch     = "unknown_arch"
	ArchSpoofRejectReasonGPUNameMismatch = "gpu_name_mismatch"
)

// RecordArchSpoofRejected increments the arch-spoof reject
// counter for the supplied reason. Unknown reasons silently
// fall into the "unknown_arch" bucket so cardinality stays
// bounded if a future code path passes a typo.
func RecordArchSpoofRejected(reason string) {
	switch reason {
	case ArchSpoofRejectReasonGPUNameMismatch:
		archSpoofRejectGPUNameMismatch.Add(1)
	default:
		archSpoofRejectUnknownArch.Add(1)
	}
}

// ArchSpoofRejectedLabeled returns (reason, count) pairs in
// stable order for Prometheus exposition.
func ArchSpoofRejectedLabeled() []struct {
	Reason string
	Val    uint64
} {
	return []struct {
		Reason string
		Val    uint64
	}{
		{ArchSpoofRejectReasonUnknownArch, archSpoofRejectUnknownArch.Load()},
		{ArchSpoofRejectReasonGPUNameMismatch, archSpoofRejectGPUNameMismatch.Load()},
	}
}

// ---------- hashrate-band rejection ----------

var (
	hashrateRejectHopper      atomic.Uint64
	hashrateRejectBlackwell   atomic.Uint64
	hashrateRejectAdaLovelace atomic.Uint64
	hashrateRejectAmpere      atomic.Uint64
	hashrateRejectTuring      atomic.Uint64
	hashrateRejectUnknown     atomic.Uint64
)

// RecordHashrateRejected increments the hashrate-out-of-band
// counter for the supplied canonical arch. Unknown arches
// fall into the "unknown" bucket — should never happen on the
// happy path since the verifier canonicalises before calling.
func RecordHashrateRejected(arch string) {
	switch arch {
	case "hopper":
		hashrateRejectHopper.Add(1)
	case "blackwell":
		hashrateRejectBlackwell.Add(1)
	case "ada-lovelace":
		hashrateRejectAdaLovelace.Add(1)
	case "ampere":
		hashrateRejectAmpere.Add(1)
	case "turing":
		hashrateRejectTuring.Add(1)
	default:
		hashrateRejectUnknown.Add(1)
	}
}

// HashrateRejectedLabeled returns (arch, count) pairs in
// stable order for Prometheus exposition.
func HashrateRejectedLabeled() []struct {
	Arch string
	Val  uint64
} {
	return []struct {
		Arch string
		Val  uint64
	}{
		{"hopper", hashrateRejectHopper.Load()},
		{"blackwell", hashrateRejectBlackwell.Load()},
		{"ada-lovelace", hashrateRejectAdaLovelace.Load()},
		{"ampere", hashrateRejectAmpere.Load()},
		{"turing", hashrateRejectTuring.Load()},
		{"unknown", hashrateRejectUnknown.Load()},
	}
}

// ---------- test reset ----------

// ResetArchcheckMetricsForTest clears every counter in this
// file. Tests-only; production code MUST NOT call this.
func ResetArchcheckMetricsForTest() {
	archSpoofRejectUnknownArch.Store(0)
	archSpoofRejectGPUNameMismatch.Store(0)
	hashrateRejectHopper.Store(0)
	hashrateRejectBlackwell.Store(0)
	hashrateRejectAdaLovelace.Store(0)
	hashrateRejectAmpere.Store(0)
	hashrateRejectTuring.Store(0)
	hashrateRejectUnknown.Store(0)
}
