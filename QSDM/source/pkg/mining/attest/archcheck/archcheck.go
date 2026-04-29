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
// # CC-path cert subject check
//
// The CC path has its own consistency function,
// ValidateBundleArchConsistencyCC, that mirrors the HMAC
// gpu_name idea but evidence-based: if the leaf cert's Subject
// (e.g. CommonName) mentions a known NVIDIA product, the
// claimed arch must match the longest-matching pattern; if the
// Subject is opaque (test-only CN, corporate label, OID-based
// model encoding) we pass through and let the cert-chain pin +
// AIK signature carry the cryptographic guarantee. See the
// function's doc-comment for the full design rationale and
// MINING_PROTOCOL_V2.md §4.6.5 for the spec text.
//
// # Hashrate-band plausibility
//
// `Attestation.ClaimedHashrateHPS` is operator-supplied and
// ungated by any cryptographic check — the miner can put any
// number they want there. It feeds the leaderboard / pool
// telemetry surface, not consensus, so the worst case is
// reputational manipulation rather than block-acceptance
// manipulation. Even so, an obviously-implausible value is a
// strong signal that the rest of the attestation is suspect:
//
//   - A claimed 100 MH/s on `gpu_arch=turing` (T4 peaks ~0.5
//     MH/s for the v2 mixin) almost certainly means the
//     miner is lying about something.
//   - A claimed 0.05 MH/s on `gpu_arch=hopper` (H100 should be
//     ~20-40 MH/s) suggests CPU mining with a forged
//     attestation.
//
// Bounds are intentionally GENEROUS (≈100x range per arch)
// so legitimate variation across a product family doesn't
// trip false positives. The check rejects only the obvious
// lies; subtler manipulation is left to off-chain analysis.
// `ClaimedHashrateHPS == 0` is treated as "not asserted" and
// passes through — see `ValidateClaimedHashrate` for why.
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

// HashrateBand is the inclusive [Min, Max] range, in
// hashes-per-second of the v2 PoW mixin, that a self-reported
// `Attestation.ClaimedHashrateHPS` is permitted to fall within
// for a given Architecture. Bounds are deliberately wide — the
// goal is to catch obvious lies (RTX 4090 claiming 200 MH/s)
// without rejecting legitimate variation across a product
// family (RTX 4060 to RTX 4090 spans ~10x).
type HashrateBand struct {
	// Min is the lower inclusive bound. Below this is treated
	// as "implausibly slow" — typically a CPU mining attempt
	// dressed up as a GPU proof.
	Min uint64

	// Max is the upper inclusive bound. Above this is treated
	// as "implausibly fast" — typically a leaderboard-stuffing
	// attempt or a confused unit.
	Max uint64
}

// hashrateBands associates each canonical Architecture with
// its accepted ClaimedHashrateHPS range. Numbers are derived
// from the §4.4 "Miner cost" estimates rounded out to ~100x
// range per arch:
//
//   - Turing  (T4 ~0.5 MH/s):       [10 KH/s,    5 MH/s]
//   - Ampere  (RTX 30 ~1 MH/s,
//              A100 ~5 MH/s):       [50 KH/s,   50 MH/s]
//   - Ada     (RTX 4090 ~5 MH/s,
//              L40S  ~6-8 MH/s):    [100 KH/s,  50 MH/s]
//   - Hopper  (H100 ~20-40 MH/s):   [1 MH/s,   200 MH/s]
//   - Blackw. (B200 ~40-80 MH/s,
//              GB200 NVL72 higher): [5 MH/s,   500 MH/s]
//
// Updates to these numbers are consensus-affecting in the same
// sense the gpu_name patterns are: tightening either end can
// reject proofs the rest of the network accepts. Loosen freely;
// tighten only by spec amendment + matching test bump.
var hashrateBands = map[Architecture]HashrateBand{
	ArchTuring:      {Min: 10_000, Max: 5_000_000},
	ArchAmpere:      {Min: 50_000, Max: 50_000_000},
	ArchAdaLovelace: {Min: 100_000, Max: 50_000_000},
	ArchHopper:      {Min: 1_000_000, Max: 200_000_000},
	ArchBlackwell:   {Min: 5_000_000, Max: 500_000_000},
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
// gpu_name pattern AND a HashrateBand. A programmer-error
// mismatch (adding a new arch but forgetting either half)
// would otherwise silently reject every honest proof of that
// arch. Crash at boot is the right failure mode here.
func init() {
	for _, a := range canonical {
		if len(gpuNamePatterns[a]) == 0 {
			panic(fmt.Sprintf("archcheck: Architecture %q has no gpu_name patterns", a))
		}
		band, ok := hashrateBands[a]
		if !ok {
			panic(fmt.Sprintf("archcheck: Architecture %q has no HashrateBand", a))
		}
		if band.Min > band.Max {
			panic(fmt.Sprintf("archcheck: Architecture %q HashrateBand %v has Min > Max", a, band))
		}
		if band.Min == 0 {
			// A zero Min would make the "claimed_hashrate_hps == 0
			// = not asserted" sentinel ambiguous (is 0 inside the
			// band, or is it the sentinel?). Forbid a zero Min so
			// the sentinel semantics stay clean.
			panic(fmt.Sprintf("archcheck: Architecture %q has Min=0 which collides with the not-asserted sentinel", a))
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

// ErrHashrateOutOfBand is returned by ValidateClaimedHashrate
// when ClaimedHashrateHPS is non-zero and falls outside the
// per-arch HashrateBand. Distinct sentinel from
// ErrArchGPUNameMismatch so dashboards and metrics can tell
// "lying about gpu_name" apart from "lying about hashrate".
var ErrHashrateOutOfBand = errors.New("archcheck: claimed_hashrate_hps outside per-arch band")

// ErrArchCertSubjectMismatch is returned by
// ValidateBundleArchConsistencyCC when the CC-path leaf cert's
// Subject contains positive NVIDIA product evidence (e.g.
// "H100" in the CN) that contradicts the claimed Architecture.
// Distinct sentinel from ErrArchGPUNameMismatch so dashboards
// can split "HMAC-path lazy spoof" from "CC-path leaf-cert
// contradiction" — the two attack shapes have different
// remediation playbooks.
var ErrArchCertSubjectMismatch = errors.New("archcheck: leaf cert subject does not match claimed gpu_arch")

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
		// Structured detail: surfaces the raw (rejected) arch
		// string on the wire view via *RejectionDetail.GPUArch.
		// errors.Is(err, ErrArchUnknown) still matches because
		// RejectionDetail.Unwrap() returns the sentinel.
		return "", newOuterArchUnknown(gpuArch, allowedNamesForError())
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
		// Empty gpu_name is itself a rejection — emit the
		// structured detail with the (canonical) arch but no
		// gpu_name (matches the prior message body when
		// rendered via Error()). The recent-rejections ring
		// will populate GPUArch but leave GPUName empty.
		return newGPUNameMismatchEmpty(arch)
	}
	for _, p := range patterns {
		if strings.Contains(name, p) {
			return nil
		}
	}
	// Structured detail: GPUName carries the actual offending
	// value (un-normalised — operators want to see what the
	// driver emitted, not the lowercased form). Patterns is
	// the substring set we tried, so the operator log line
	// can answer "what would have been accepted".
	return newGPUNameMismatch(arch, gpuName, patterns)
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

// HashrateBandFor returns the [Min, Max] hashrate range for
// the given Architecture, or (zero, false) if the arch is not
// in the canonical set. Exposed so dashboards and the CLI's
// `qsdmcli gov params` listing can surface the live band
// without re-deriving the table.
func HashrateBandFor(arch Architecture) (HashrateBand, bool) {
	band, ok := hashrateBands[arch]
	return band, ok
}

// ValidateClaimedHashrate enforces the per-arch HashrateBand on
// a self-reported `Attestation.ClaimedHashrateHPS`. Returns nil
// in two cases:
//
//   - claimed == 0. The protocol treats zero as "miner declined
//     to assert" (existing test fixtures predate this check use
//     0 idiomatically, and the wire format gives no other
//     unambiguous way to mean "absent" for a uint64). Tightening
//     to require non-zero is a future fork concern; for now,
//     0 passes the band check.
//
//   - claimed is in the band [Min, Max] inclusive on both ends.
//
// Returns ErrHashrateOutOfBand wrapped with both endpoints so
// the operator log line tells them what they claimed against
// what the band allows. Returns ErrArchUnknown if `arch` is
// not in the canonical set (programmer error — caller should
// have called Canonicalise first).
//
// This is informational integrity, not a security boundary:
// ClaimedHashrateHPS feeds telemetry / leaderboards, not block
// acceptance arithmetic. So the bounds err on the side of
// generosity (~100x range per arch) and the failure mode
// (ErrHashrateOutOfBand) wraps a soft sentinel rather than the
// hard `mining.ErrAttestationSignatureInvalid`. Wiring sites
// choose whether to reject the proof or merely log; the outer
// verifier rejects post-fork because anything failing here is
// almost certainly compounding another lie.
func ValidateClaimedHashrate(arch Architecture, claimedHPS uint64) error {
	band, ok := hashrateBands[arch]
	if !ok {
		return fmt.Errorf("%w: arch %q has no HashrateBand", ErrArchUnknown, arch)
	}
	if claimedHPS == 0 {
		// "Not asserted" sentinel — passes through.
		return nil
	}
	if claimedHPS < band.Min || claimedHPS > band.Max {
		return fmt.Errorf("%w: arch=%q claimed=%d band=[%d, %d]",
			ErrHashrateOutOfBand, arch, claimedHPS, band.Min, band.Max)
	}
	return nil
}

// ValidateBundleArchConsistencyCC enforces the §4.6.5 CC-path
// "leaf cert subject ↔ gpu_arch" consistency check.
//
// # Evidence-based, not strict
//
// Unlike the HMAC path's gpu_name check (§4.6.2) — where every
// honest miner emits a known nvidia-smi product string and an
// empty value is itself a rejection signal — the CC path's
// device certificate Subject does NOT have a universally fixed
// shape. NVIDIA's production CC chains may encode the GPU
// model in:
//
//   - Subject.CommonName as a free-form product string
//     ("NVIDIA H100 80GB HBM3"), OR
//   - Subject.OrganizationalUnit (less common), OR
//   - A custom OID extension we do not yet parse, OR
//   - Nowhere on the leaf at all (the model is only inferable
//     from the issuing intermediate's name).
//
// We DON'T know which shape the production NVIDIA chain uses
// until we have a real fixture, and we don't want a strict
// "must contain a known product string" rule to false-reject
// honest validators because their cert format doesn't include
// the model in CN. So this function uses an EVIDENCE-BASED
// rule:
//
//  1. Normalise the candidate subject string (lowercase,
//     collapse whitespace; the same shape as the HMAC path's
//     normaliseGPUName).
//  2. Scan the normalised string for ANY product substring
//     across ALL canonical architectures.
//
//     - If NONE found: the cert subject contains no product
//       evidence. Pass through. The cert chain root pin
//       (§3.2 step 3) and the AIK signature (§3.2 step 4)
//       remain the cryptographic locks; this layer is
//       supplementary soft verification.
//
//     - If at least one found: the cert subject is making a
//       positive claim about the hardware. Verify the
//       evidence is consistent with `arch`. Returns
//       ErrArchCertSubjectMismatch on contradiction.
//
// # Why this isn't a backdoor
//
// "Pass through on no evidence" sounds like a free pass for an
// attacker who controls the cert content. It is not, because
// step 3 (cert chain rooted in pinned NVIDIA CA) means the
// attacker cannot mint a leaf at all unless they ALREADY have
// an NVIDIA-issued AIK — which is bound to a specific physical
// device by NVIDIA's manufacturing process. So the worst case
// here is an honest leaf whose CN happens to be opaque, which
// we accept; the attacker shape "spoof an arch with a
// fabricated leaf" is locked out one layer up.
//
// # Why we reuse the HMAC patterns
//
// The substring patterns are the same NVIDIA product strings
// nvidia-smi emits and the same strings any reasonable cert
// subject would carry — there's no need to maintain a second
// table. Both paths therefore stay consistent in what they
// consider "evidence of arch X".
//
// Production wiring callers pass `cert.Subject.CommonName`
// here today; future revisions can pass the full Subject.String()
// or join CN + OU once we have real fixtures showing where the
// model is encoded.
func ValidateBundleArchConsistencyCC(arch Architecture, certSubject string) error {
	if _, ok := gpuNamePatterns[arch]; !ok {
		return fmt.Errorf("%w: arch %q has no patterns", ErrArchUnknown, arch)
	}
	subject := normaliseGPUName(certSubject)
	if subject == "" {
		// No subject content to evaluate — pass through (the
		// chain pin + AIK signature are the locks).
		return nil
	}
	// Scan for product evidence across every canonical arch.
	// "Evidence" = a substring from some arch's gpu_name
	// pattern table appears in `subject`.
	//
	// Pattern overlap matters: e.g. "rtx 6000 ada" (Ada)
	// CONTAINS "rtx 6000" (Turing's Quadro RTX 6000) as a
	// substring. A subject "RTX 6000 Ada Generation" therefore
	// matches BOTH the Ada and Turing pattern tables. Naïvely
	// accepting "any matched arch" would let a Turing spoof
	// slip past an honest Ada cert.
	//
	// Resolution rule: longest-pattern match wins. The pattern
	// "rtx 6000 ada" (12 chars) is more specific than "rtx
	// 6000" (8 chars), so the Ada attribution dominates. This
	// mirrors how a human reads the subject — the more
	// specific token reveals the arch — and falls back to
	// "list everything that tied for the longest match" for
	// genuinely ambiguous strings.
	type match struct {
		arch    Architecture
		pattern string
	}
	var matches []match
	for _, a := range canonical {
		for _, p := range gpuNamePatterns[a] {
			if strings.Contains(subject, p) {
				matches = append(matches, match{a, p})
			}
		}
	}
	if len(matches) == 0 {
		// No NVIDIA product mentioned anywhere in the subject.
		// Common case for test fixtures and corporate-style
		// CN's like "NVIDIA Confidential Computing AIK".
		return nil
	}
	maxLen := 0
	for _, m := range matches {
		if len(m.pattern) > maxLen {
			maxLen = len(m.pattern)
		}
	}
	bestArches := make(map[Architecture]struct{}, 2)
	bestPatterns := make([]string, 0, 2)
	for _, m := range matches {
		if len(m.pattern) == maxLen {
			bestArches[m.arch] = struct{}{}
			bestPatterns = append(bestPatterns, fmt.Sprintf("%s(%q)", m.arch, m.pattern))
		}
	}
	if _, ok := bestArches[arch]; ok {
		return nil
	}
	// Structured detail: CertSubject carries the un-normalised
	// raw operator-supplied subject (operators want to see what
	// the cert actually contains). Patterns carries the
	// strongest-match labels for log readability; consumers
	// reading the wire view typically only consume CertSubject
	// + GPUArch.
	return newCertSubjectMismatch(arch, certSubject, bestPatterns)
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
