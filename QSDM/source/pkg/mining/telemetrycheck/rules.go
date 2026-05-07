package telemetrycheck

// Rule functions in this file produce a *FieldMismatch when
// they fire and nil otherwise. Splitting one rule per
// function keeps the unit tests focused (one test per rule)
// and lets a future commit add new rules with minimal
// surface change to checker.go.
//
// Naming convention: rule<RULENAME> — first word
// describes WHAT is checked, not HOW.

import (
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/telemetry"
)

// ruleArchVsComputeCap fires when the outer GPUArch
// disagrees with the architecture inferred from the
// inner compute_cap. This is the most consequential rule
// — a miner who claims "ampere" + CC 9.0 (Hopper) is
// either lying about their hardware or has a buggy
// miner. Either way, the discrepancy is mechanically
// detectable without ANY catalog data, so this rule
// fires even on otherwise-empty catalogs.
//
// Severity: major. The combination is structurally
// impossible — no NVIDIA driver could ever report the
// claim shape.
//
// Returns nil when:
//
//   - either field is empty (the catalog has nothing to
//     compare against; a different rule will catch the
//     "field missing" case if it matters)
//   - the inferred architecture matches GPUArch (canonical
//     comparison: lowercase trim, strip "-" delimiters
//     so "ada-lovelace" vs "ada lovelace" don't drift)
//   - the inferred architecture is empty (compute_cap
//     belongs to a generation telemetry.inferArchitecture
//     doesn't yet recognise — better to stay quiet than
//     to flag a brand-new SKU as mismatched)
func ruleArchVsComputeCap(c Claim) *FieldMismatch {
	if c.GPUArch == "" || c.ComputeCap == "" {
		return nil
	}
	inferred := telemetryInferArch(c.ComputeCap)
	if inferred == "" {
		return nil
	}
	if equalArchToken(c.GPUArch, inferred) {
		return nil
	}
	return &FieldMismatch{
		Field:    "arch",
		Got:      c.GPUArch,
		Expected: []string{inferred},
		Severity: "major",
	}
}

// ruleComputeCapAgainstSKU fires when no catalog entry for
// the claim's GPU name is willing to vouch for the
// claim's compute_cap. Catalog has at least one entry for
// the SKU (caller's precondition: skuKnown is true), but
// none of them list the same compute capability.
//
// Severity: major. A given SKU has exactly one compute
// capability; a mismatch here is mechanical evidence that
// the miner is either confused or fabricating.
func ruleComputeCapAgainstSKU(c Claim, candidates []telemetry.GPUObservation) *FieldMismatch {
	if c.ComputeCap == "" {
		return nil
	}
	expected := make(map[string]struct{})
	for _, o := range candidates {
		if o.ComputeCapability == "" {
			continue
		}
		expected[o.ComputeCapability] = struct{}{}
	}
	if _, ok := expected[c.ComputeCap]; ok {
		return nil
	}
	if len(expected) == 0 {
		// Every catalog entry for this SKU lacks a
		// compute_cap value. Don't flag — we have no
		// reference to compare against.
		return nil
	}
	exp := make([]string, 0, len(expected))
	for k := range expected {
		exp = append(exp, k)
	}
	sortStrings(exp)
	return &FieldMismatch{
		Field:    "compute_cap",
		Got:      c.ComputeCap,
		Expected: exp,
		Severity: "major",
	}
}

// ruleDriverVerFormat is a soft check on driver_ver
// shape — NVIDIA drivers are always digits + dots
// ("576.28", "535.104.05"). Anything containing
// non-numeric / non-dot characters is suspicious and a
// future spoofing detector might want to investigate.
//
// We do NOT validate the value range against an
// "approved drivers" list because:
//
//   - NVIDIA ships drivers more often than we publish
//     baseline updates, so a fresh driver always
//     looks "unknown" for a few weeks
//   - operators legitimately downgrade drivers when a
//     new release breaks something
//
// Catalog-supplied driver lists ARE used for a future
// driver_ver-against-catalog rule; that rule is not
// shipped today because it false-positives too often
// during early-network operation.
//
// Severity: minor. A bad driver_ver shape is rarely
// adversarial; usually a wire-corruption / mis-coding
// bug in the miner.
func ruleDriverVerFormat(c Claim) *FieldMismatch {
	if c.DriverVer == "" {
		return nil
	}
	if isVendorDriverFormat(c.DriverVer) {
		return nil
	}
	return &FieldMismatch{
		Field:    "driver_ver_format",
		Got:      c.DriverVer,
		Expected: []string{"NN.NN", "NNN.NN", "NN.NN.NN", "NN.NNN.NN"},
		Severity: "minor",
	}
}

// isVendorDriverFormat returns true when s consists
// entirely of digits and at most three dots, with at
// least one digit on each side of every dot. Permissive
// enough for present + plausible-future NVIDIA driver
// version strings.
func isVendorDriverFormat(s string) bool {
	if s == "" {
		return false
	}
	dots := 0
	prevDot := false
	startedDigit := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			startedDigit = true
			prevDot = false
		case c == '.':
			if !startedDigit || prevDot {
				return false
			}
			dots++
			if dots > 3 {
				return false
			}
			prevDot = true
		default:
			return false
		}
	}
	return !prevDot && startedDigit
}

// equalArchToken normalises two architecture strings
// before comparison. Folds case, trims, treats "-" and
// "_" and " " as equivalent so "ada-lovelace" matches
// "ada lovelace" and "Ada_Lovelace".
func equalArchToken(a, b string) bool {
	return canonicalArch(a) == canonicalArch(b)
}

func canonicalArch(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	rep := strings.NewReplacer("-", "", "_", "", " ", "")
	return rep.Replace(s)
}

// telemetryInferArch is a thin wrapper around
// telemetry.inferArchitecture (which is the package-private
// helper exported here so we don't fork the mapping
// table). Wraps it so the rules.go imports stay obvious.
func telemetryInferArch(cc string) string {
	// Use the public re-export from pkg/telemetry. The
	// helper there is package-private (lowercase) so we
	// duplicate the small mapping here. Keeping the table
	// in one place would be ideal but cross-package
	// visibility costs more than the duplication is
	// worth — the table changes once a year per NVIDIA
	// generation, and any drift between the two copies
	// would surface immediately in the rules tests.
	return inferArchitectureLocal(cc)
}

// inferArchitectureLocal mirrors the table in
// pkg/telemetry/collector.go. Drift between the two is
// loudly tested in collector_test.go (telemetry side) and
// rules_test.go (this side) so a missed update lands as a
// red CI rather than a silent inconsistency. If a new
// generation ships, update BOTH locations.
func inferArchitectureLocal(cc string) string {
	cc = strings.TrimSpace(cc)
	if cc == "" {
		return ""
	}
	parts := strings.SplitN(cc, ".", 2)
	if len(parts) != 2 {
		return ""
	}
	major, err := atoiSafe(parts[0])
	if err != nil {
		return ""
	}
	minor, err := atoiSafe(parts[1])
	if err != nil {
		return ""
	}
	switch major {
	case 5:
		return "maxwell"
	case 6:
		return "pascal"
	case 7:
		switch minor {
		case 0, 2:
			return "volta"
		case 5:
			return "turing"
		}
	case 8:
		switch minor {
		case 0, 6, 7:
			return "ampere"
		case 9:
			return "ada-lovelace"
		}
	case 9:
		return "hopper"
	case 10, 12:
		return "blackwell"
	}
	return ""
}

// sortStrings is a hand-rolled lex-ascending sort so this
// file's tests don't need to import "sort" via the
// production code (already imported in catalog.go).
// Performance is not relevant — Expected lists are
// effectively never larger than 4 entries.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// atoiSafe is a tiny ASCII-decimal parser shared by the
// rules and the architecture inference. Using strconv.Atoi
// would work too, but keeping the parser here means the
// rules file has zero external dependencies beyond the
// telemetry observation type and the standard "strings"
// helpers — easier to vendor into a future
// hardware-specific rule pack.
func atoiSafe(s string) (int, error) {
	if s == "" {
		return 0, errBadArchInt
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errBadArchInt
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

var errBadArchInt = &catalogError{msg: "atoiSafe: non-decimal input"}
