// Package archcheck implements the §4.6 / §3.3-step-8
// "arch-spoof rejection" cross-checks for v2 mining proofs.
//
// # Why this package exists
//
// The original protocol draft (MINING_PROTOCOL_V2.md §4.6, pre
// 2026-04-29) said:
//
//	"the matmul rounding fingerprint differs"
//
// suggesting that the verifier could detect arch-spoof attempts
// (e.g. an RTX 4090 claiming to be an H100) by inspecting
// architecture-specific FP16 rounding differences in the
// Tensor-Core mix digest.
//
// That claim was wrong. The 2026-04-26 ratification of byte-exact
// FP16 round-to-nearest-even (`pkg/mining/pow/v2/fp16.go` +
// matmul accumulation order) DELIBERATELY locks the digest to be
// identical across every architecture that can run the spec.
// That is a conformance bar — without it, byte-exact validation
// across heterogeneous hardware would be impossible. So there
// IS no rounding-fingerprint to lean on.
//
// What we CAN do is what this package does: enforce
// out-of-band consistency between the proof's self-reported
// arch and the parts of the attestation surface the operator
// cannot freely swap without re-signing:
//
//  1. Closed-enum allowlist for Attestation.GPUArch. An unknown
//     arch string (typo, future-arch sneak attempt, garbage)
//     hard-rejects.
//
//  2. arch ↔ bundle.gpu_name consistency (HMAC path). The HMAC
//     bundle's gpu_name is HMAC-bound under the operator's
//     enrollment-time secret, so an attacker cannot post-hoc
//     swap it without resigning the bundle. This catches the
//     "lazy spoof" — an attacker who flips gpu_arch=hopper but
//     forgets to also lie about the nvidia-smi name on their
//     consumer Ada card. A determined attacker who lies about
//     BOTH is still trapped by the on-chain registry's
//     (gpu_uuid, hmac_key) pairing — and economically by the
//     §5.4 stake bond plus §8 slashing surface.
//
// # Aliases
//
// The codebase ships with both `"ada"` and `"ada-lovelace"`
// in flight (qsdmminer-console emits the short form, the
// protocol doc shows the long form, the test suite uses both).
// To avoid a flag-day cutover, this package accepts BOTH and
// canonicalises to the long form internally. `Canonicalise()`
// is the single source of truth callers use; the validator
// then matches against the canonical name.
//
// # Out of scope
//
//   - The CC path's certificate-subject ↔ arch cross-check.
//     X.509 subject parsing is implementation-specific and
//     belongs in pkg/mining/attest/cc; this package exposes
//     ValidateBundleArchConsistencyCC (a no-op stub) so the
//     wiring point is reserved.
//
//   - Hashrate plausibility per arch (claimed_hashrate_hps vs
//     known per-arch peak FP16 throughput). Belongs in a
//     separate hashrate-band check; arch-spoof is sufficient
//     to reject the more egregious spoofs by itself.
package archcheck

import (
	"errors"
	"fmt"
	"strings"
)

// Architecture is the canonical wire form of a GPU
// architecture. Defined as a string alias so the closed-enum
// allowlist and the alias map share one type without per-call
// casts.
type Architecture string

// String returns the canonical wire form. Implements fmt.Stringer
// so log lines and error messages print the bare name.
func (a Architecture) String() string { return string(a) }

const (
	// ArchHopper is the Hopper datacenter family (H100, H200,
	// H800). SM 9.0. Confidential-Computing capable; expected
	// to use Attestation.Type == nvidia-cc-v1, but this
	// package does NOT enforce that mapping (the A100 / Ampere
	// counterexample shows the matrix is not 1:1 — see package
	// doc).
	ArchHopper Architecture = "hopper"

	// ArchBlackwell is the Blackwell datacenter / consumer
	// family (B100, B200, GB200, RTX 50-series). SM 10.0.
	ArchBlackwell Architecture = "blackwell"

	// ArchAdaLovelace is the consumer Ada Lovelace family
	// (RTX 40-series, L4, L40, L40S, RTX 6000 Ada). SM 8.9.
	// The wire form `"ada"` is accepted as an alias and
	// canonicalises to `"ada-lovelace"` here.
	ArchAdaLovelace Architecture = "ada-lovelace"

	// ArchAmpere is the Ampere family. Spans both datacenter
	// (A100, A40, A30, A10, A2) and consumer (RTX 30-series,
	// RTX A-series workstation) cards. SM 8.0 / 8.6.
	ArchAmpere Architecture = "ampere"

	// ArchTuring is the Turing family (RTX 20-series,
	// GTX 16-series, T4, Tesla T-series, RTX 6000 (non-Ada)).
	// SM 7.5. Oldest arch on the v2 allowlist; older arches
	// (Volta, Pascal, Maxwell, Kepler) are intentionally OFF
	// the allowlist because their compute-capability and
	// driver-version floors no longer satisfy the per-arch
	// minimum (§5.1) reliably.
	ArchTuring Architecture = "turing"
)

// canonical lists every Architecture in protocol-spec order.
// Used by KnownArchitectures() and as the master set for the
// closed-enum allowlist.
var canonical = []Architecture{
	ArchHopper,
	ArchBlackwell,
	ArchAdaLovelace,
	ArchAmpere,
	ArchTuring,
}

// aliases maps non-canonical wire forms to their canonical
// Architecture. Acceptance is case-INSENSITIVE; the Canonicalise()
// function lowercases the input before lookup, so callers do
// NOT need to lowercase first.
//
// New aliases land here ONLY by protocol amendment. Adding an
// alias is consensus-affecting (it shifts which strings the
// network accepts) and must follow the same review bar as
// adding a new ParamSpec.
var aliases = map[string]Architecture{
	// "ada" is the qsdmminer-console-emitted short form. Long-
	// term cleanup will tighten miner output to the canonical
	// "ada-lovelace" but that's a separate cross-binary
	// migration; until then both are accepted.
	"ada": ArchAdaLovelace,
}

// gpuNamePatterns associates each canonical Architecture with
// a list of case-insensitive substring patterns that should
// appear in bundle.gpu_name for an honest miner. A miner
// whose claimed arch is X but whose gpu_name does NOT contain
// any of X's patterns is rejected with ErrArchGPUNameMismatch.
//
// These patterns are deliberately CONSERVATIVE — every entry
// is a real shipping NVIDIA product line. If a product
// substring is absent from this table, the verifier rejects
// rather than silently passes; we'd rather force a spec
// amendment for a new sub-arch than implicitly accept what
// could be a spoof attempt. Add new patterns by amendment
// (see "Why this is closed-enum" in the package doc) and
// update MINING_PROTOCOL_V2.md §4.6 in the same change.
//
// Patterns are checked AFTER the bundle's gpu_name is normalised
// (whitespace trimmed, case-folded). nvidia-smi can emit names
// like "NVIDIA H100 80GB HBM3" or "Tesla H100" or
// "NVIDIA H100 PCIe", so substring match — not exact match — is
// the right rule.
var gpuNamePatterns = map[Architecture][]string{
	ArchHopper: {
		"h100", "h200", "h800",
	},
	ArchBlackwell: {
		"b100", "b200", "gb200",
		"rtx 50",
	},
	ArchAdaLovelace: {
		"rtx 40",
		"l4", "l40",
		"rtx 6000 ada", "rtx 5000 ada", "rtx 4500 ada",
		"rtx 4000 ada", "rtx 2000 ada",
	},
	ArchAmpere: {
		"a100", "a40", "a30", "a16", "a10",
		"a2",
		"rtx 30",
		"rtx a", // RTX A6000, A5000, A4000, A2000 etc.
	},
	ArchTuring: {
		"rtx 20",
		"gtx 16",
		"t4",
		"quadro rtx",
		"rtx 8000", "rtx 6000",
	},
}

// init verifies every canonical Architecture has at least one
// gpu_name pattern. A programmer-error mismatch (adding a new
// arch but forgetting the patterns) would otherwise silently
// reject every honest proof of that arch. Crash at boot is
// the right failure mode here.
func init() {
	for _, a := range canonical {
		if len(gpuNamePatterns[a]) == 0 {
			panic(fmt.Sprintf("archcheck: Architecture %q has no gpu_name patterns", a))
		}
	}
	// Every alias must point to a canonical arch.
	for alias, target := range aliases {
		var found bool
		for _, a := range canonical {
			if a == target {
				found = true
				break
			}
		}
		if !found {
			panic(fmt.Sprintf("archcheck: alias %q -> %q points to non-canonical arch", alias, target))
		}
	}
}

// ErrArchUnknown is returned by ValidateOuterArch when
// Attestation.GPUArch is empty or not in the allowlist (after
// alias canonicalisation). Wraps callers' chosen consensus
// sentinel.
var ErrArchUnknown = errors.New("archcheck: unknown gpu_arch")

// ErrArchGPUNameMismatch is returned by
// ValidateBundleArchConsistencyHMAC when the bundle's
// gpu_name does not contain any of the patterns associated
// with the proof's claimed Architecture. The "spoof was caught"
// signal.
var ErrArchGPUNameMismatch = errors.New("archcheck: gpu_name does not match claimed gpu_arch")

// KnownArchitectures returns the closed-enum allowlist of
// canonical Architecture values, in protocol-spec order. Used
// by docs / dashboards / the qsdmcli help output.
func KnownArchitectures() []Architecture {
	out := make([]Architecture, len(canonical))
	copy(out, canonical)
	return out
}

// Canonicalise turns a wire-form gpu_arch string into its
// canonical Architecture, applying the alias map and
// lowercase-folding. Returns (zero, false) if the input is
// empty or matches neither a canonical name nor an alias.
//
// This is the single point at which input-format laxness is
// resolved. Every other function in this package operates on
// the canonical form exclusively.
func Canonicalise(s string) (Architecture, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", false
	}
	for _, a := range canonical {
		if string(a) == s {
			return a, true
		}
	}
	if a, ok := aliases[s]; ok {
		return a, true
	}
	return "", false
}

// ValidateOuterArch checks that the Attestation.GPUArch field
// of a v2 proof is either a canonical Architecture or one of
// its accepted aliases. Returns ErrArchUnknown wrapped with
// the offending value when the check fails.
//
// Called by pkg/mining/verifier.go in the post-fork branch,
// AFTER the dispatcher's per-type VerifyAttestation returns
// nil. Running the cheap arch enum check after the
// (relatively expensive) HMAC / CC crypto check is fine
// because the consensus-relevant signal is "did this proof
// pass the FULL gauntlet" — short-circuiting on a free check
// after a passing crypto check trades nothing for clarity.
//
// Pre-fork callers MUST NOT call this function: a v1 proof
// has no GPUArch field by spec, so a missing/empty value is
// not a bug at v1.
func ValidateOuterArch(gpuArch string) (Architecture, error) {
	a, ok := Canonicalise(gpuArch)
	if !ok {
		return "", fmt.Errorf("%w: %q (allowed: %s)",
			ErrArchUnknown, gpuArch, allowedNamesForError())
	}
	return a, nil
}

// ValidateBundleArchConsistencyHMAC checks that bundle.gpu_name
// contains at least one of the substring patterns associated
// with the canonical arch. Case-insensitive, whitespace-
// tolerant on the input. Returns ErrArchGPUNameMismatch
// wrapped with both values on failure so the operator-facing
// log line tells them WHICH substring they were claiming
// against WHAT actual hardware string.
//
// Called by pkg/mining/attest/hmac/verifier.go as the §3.3
// step-8 cross-check. The bundle's gpu_name is HMAC-bound
// (the bundle's HMAC field covers it via CanonicalForMAC), so
// an attacker who has just successfully forged the HMAC
// cannot also flip gpu_name post-hoc — they'd have to choose
// at sign time, which means the operator who knows the HMAC
// key is colluding. That collusion is what stake bonding +
// slashing attacks (§5.4 + §8) economically deter.
func ValidateBundleArchConsistencyHMAC(arch Architecture, gpuName string) error {
	patterns, ok := gpuNamePatterns[arch]
	if !ok {
		// Programmer error: caller passed an Architecture
		// that's not in the canonical set. A bug, but
		// returning an error is safer than panicking on
		// the consensus path.
		return fmt.Errorf("%w: arch %q has no patterns", ErrArchUnknown, arch)
	}
	name := normaliseGPUName(gpuName)
	if name == "" {
		return fmt.Errorf("%w: empty gpu_name (claimed arch %q)",
			ErrArchGPUNameMismatch, arch)
	}
	for _, p := range patterns {
		if strings.Contains(name, p) {
			return nil
		}
	}
	return fmt.Errorf("%w: gpu_name=%q does not match arch=%q (patterns: %s)",
		ErrArchGPUNameMismatch, gpuName, arch, strings.Join(patterns, ", "))
}

// normaliseGPUName lowercases, trims, and collapses internal
// whitespace runs in the input so pattern matching is robust
// against whatever weirdness a driver version chooses to emit.
// Verified against the standard nvidia-smi output ("NVIDIA H100
// 80GB HBM3", "NVIDIA GeForce RTX 4090", "Tesla T4") — none of
// which have unusual whitespace, but we normalise defensively
// because a single whitespace anomaly should not be the
// difference between accept and reject on the consensus path.
func normaliseGPUName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	// Collapse internal whitespace runs to single spaces.
	// strings.Fields() handles every whitespace class
	// (regular space, tab, newline, etc.) so we don't
	// need a Unicode-aware regex.
	return strings.Join(strings.Fields(s), " ")
}

// ValidateBundleArchConsistencyCC is the placeholder hook for
// the CC path's certificate-subject ↔ arch cross-check. Today
// the CC verifier already binds the device certificate chain
// to a specific physical Hopper / Blackwell GPU at the
// cryptographic level (§3.2 step 1), so the device cert
// itself is the consistency check. This function exists so
// pkg/mining/attest/cc has a fixed wiring point for future
// strict cert-subject parsing without re-shaping the package
// boundary.
//
// Currently always nil. A future revision parses
// `cert.Subject.CommonName` for "H100" / "B200" patterns and
// verifies the result against `arch`.
func ValidateBundleArchConsistencyCC(arch Architecture, certSubject string) error {
	_ = arch
	_ = certSubject
	return nil
}

// allowedNamesForError returns a comma-joined list of
// canonical names + aliases for embedding in an error
// message. Centralised here so a registry change automatically
// updates every error-message reader.
func allowedNamesForError() string {
	parts := make([]string, 0, len(canonical)+len(aliases))
	for _, a := range canonical {
		parts = append(parts, string(a))
	}
	for alias := range aliases {
		parts = append(parts, alias+"=alias")
	}
	return strings.Join(parts, ", ")
}
