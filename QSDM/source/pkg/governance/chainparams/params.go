package chainparams

// params.go defines the registry of governance-tunable
// parameters. Adding a new tunable means appending to this
// table — every other layer (admission, applier, ParamStore,
// SlashApplier reads, CLI help) is automatically aware.
//
// # Why a registry instead of one struct field per param
//
// Three reasons:
//
//  1. Bounds discoverability. Each parameter declares its own
//     (Min, Max) so admission can reject out-of-band proposals
//     before they consume validator work. Centralising the
//     bounds here means CLI / spec / chain agree on the same
//     numbers.
//
//  2. Versioning. A future fork that wants to RELAX a bound
//     (e.g. raise SlashRewardCap from 5000 to 7500) lands as a
//     code change in this table; the registry's own version is
//     bumped via the FORK_V2_TC_HEIGHT-style mechanism. Without
//     the registry every reader of the bound has to be touched.
//
//  3. Dynamic CLI help. `qsdmcli gov propose-param --help` can
//     enumerate the live registry without any code-gen step.
//
// # Adding a parameter
//
//   - Pick a name. snake_case, ASCII-only, ≤32 bytes.
//   - Decide the bounds. Inclusive on both ends.
//   - Decide the chain-side reader. SlashApplier already reads
//     RewardBPS / AutoRevokeMinStakeDust through
//     chain.ParamStore; new params need either an extension to
//     ParamStore or a new reader interface.
//   - Append to the Registry slice below.
//   - Update MINING_PROTOCOL_V2.md §10 to document the new
//     parameter (default, bounds, reader path).

import (
	"errors"
	"fmt"
	"strings"
)

// ParamName is the canonical wire form of a parameter
// identifier. Defined as a string alias so the Registry table
// and the wire payload share a single type without the noise
// of per-call casts.
type ParamName string

// String returns the canonical wire form. Implements fmt.Stringer
// so log lines and error messages print the bare name.
func (p ParamName) String() string { return string(p) }

const (
	// ParamRewardBPS is SlashApplier.RewardBPS. Inclusive
	// upper bound of 5000 mirrors chain.SlashRewardCap (50%);
	// raising it requires both a code change and a deliberate
	// registry-revision bump because the cap exists to prevent
	// captured-governance "100% reward" attacks.
	ParamRewardBPS ParamName = "reward_bps"

	// ParamAutoRevokeMinStakeDust is
	// SlashApplier.AutoRevokeMinStakeDust. Bounded between
	// 1 CELL (one full coin's worth of dust) and
	// MIN_ENROLL_STAKE (10 CELL = the protocol minimum to
	// hold a record at all). A value above MIN_ENROLL_STAKE
	// would auto-revoke perfectly-bonded operators which is
	// nonsensical.
	ParamAutoRevokeMinStakeDust ParamName = "auto_revoke_min_stake_dust"
)

// dustPerCELL mirrors pkg/chain.dustPerCELL. Duplicated here
// rather than imported because pkg/governance/chainparams MUST
// NOT import pkg/chain (that's the import cycle the chain ←
// chainparams ParamStore interface is designed to avoid).
const dustPerCELL uint64 = 100_000_000

// minEnrollStakeDust mirrors mining.MinEnrollStakeDust = 10 CELL
// for the same import-cycle reason as dustPerCELL. The chain
// applier validates this matches at startup.
const minEnrollStakeDust uint64 = 10 * dustPerCELL

// ParamSpec describes one entry in the parameter registry.
// Every field is consensus-critical: a validator that disagrees
// on bounds for any registered parameter would accept proposals
// the rest of the network rejects (or vice versa), forking the
// chain. Treat changes here as fork-grade.
type ParamSpec struct {
	// Name is the canonical wire name. Must be unique across
	// the registry.
	Name ParamName

	// Description is human-readable documentation surfaced by
	// the CLI's `qsdmcli gov params` listing. Not consensus-
	// critical but kept here so callers don't need a separate
	// docs index.
	Description string

	// MinValue and MaxValue bound proposed values inclusively
	// on both ends. A proposal with Value < MinValue or
	// Value > MaxValue rejects with ErrValueOutOfBounds at
	// stateless admission time.
	MinValue uint64
	MaxValue uint64

	// DefaultValue is the value the chain reads when no
	// governance proposal has yet activated. Mirrors the
	// existing construction-time defaults in chain.SlashApplier.
	DefaultValue uint64

	// Unit is a free-form display string (e.g. "bps", "dust",
	// "blocks"). Not consensus-critical.
	Unit string
}

// Validate reports a configuration error in the spec itself.
// Called at package init time so a programming mistake (e.g.
// MinValue > MaxValue) crashes the binary at boot rather than
// silently accepting impossible proposals.
func (s ParamSpec) Validate() error {
	if s.Name == "" {
		return errors.New("chainparams: ParamSpec.Name is empty")
	}
	if !validParamName(string(s.Name)) {
		return fmt.Errorf(
			"chainparams: ParamSpec.Name = %q must be ASCII snake_case, ≤32 bytes",
			s.Name)
	}
	if s.MinValue > s.MaxValue {
		return fmt.Errorf(
			"chainparams: ParamSpec %q has MinValue=%d > MaxValue=%d",
			s.Name, s.MinValue, s.MaxValue)
	}
	if s.DefaultValue < s.MinValue || s.DefaultValue > s.MaxValue {
		return fmt.Errorf(
			"chainparams: ParamSpec %q DefaultValue=%d outside [%d, %d]",
			s.Name, s.DefaultValue, s.MinValue, s.MaxValue)
	}
	return nil
}

// CheckBounds returns nil iff `value` is within the registered
// (MinValue, MaxValue) range inclusive. Returns
// ErrValueOutOfBounds wrapped with the actual numbers so the
// failing operator sees both their value and the cap.
func (s ParamSpec) CheckBounds(value uint64) error {
	if value < s.MinValue || value > s.MaxValue {
		return fmt.Errorf("%w: param=%q value=%d range=[%d, %d]",
			ErrValueOutOfBounds, s.Name, value,
			s.MinValue, s.MaxValue)
	}
	return nil
}

// registry is the package-private master table. Exposed via
// Registry() / Lookup() to keep callers from mutating it.
var registry = []ParamSpec{
	{
		Name:         ParamRewardBPS,
		Description:  "Slasher reward share, in basis points of forfeited stake. Mirrors SlashApplier.RewardBPS.",
		MinValue:     0,
		MaxValue:     5000, // chain.SlashRewardCap
		DefaultValue: 0,    // genesis = burn-everything; v2wiring may override
		Unit:         "bps",
	},
	{
		Name: ParamAutoRevokeMinStakeDust,
		Description: "Stake threshold (in dust) below which a post-slash record is auto-revoked into the unbond window. " +
			"Bounded between 1 CELL and MIN_ENROLL_STAKE.",
		MinValue:     dustPerCELL,
		MaxValue:     minEnrollStakeDust,
		DefaultValue: minEnrollStakeDust,
		Unit:         "dust",
	},
}

// init validates the static registry at package load. Any
// error here is a programmer mistake that would silently
// corrupt consensus, so we panic.
func init() {
	seen := make(map[ParamName]bool, len(registry))
	for i := range registry {
		spec := registry[i]
		if err := spec.Validate(); err != nil {
			panic(err)
		}
		if seen[spec.Name] {
			panic(fmt.Sprintf("chainparams: duplicate ParamSpec.Name = %q", spec.Name))
		}
		seen[spec.Name] = true
	}
}

// Registry returns a copy of the parameter registry. Callers
// MUST NOT modify the returned slice's specs (the slice itself
// is a copy but ParamSpec contains no pointers, so a deep
// copy is unnecessary). Used by the CLI's `gov params` listing
// and by tests that walk all params.
func Registry() []ParamSpec {
	out := make([]ParamSpec, len(registry))
	copy(out, registry)
	return out
}

// Lookup returns the ParamSpec for the named parameter, or
// (zero, false) if the name is not registered. Used by
// admission + applier to fetch bounds for a specific tx.
func Lookup(name string) (ParamSpec, bool) {
	for i := range registry {
		if string(registry[i].Name) == name {
			return registry[i], true
		}
	}
	return ParamSpec{}, false
}

// Names returns just the names of every registered parameter,
// sorted in registry-declaration order. Useful for CLI output
// and error messages that want to list "valid choices are X,
// Y, Z".
func Names() []string {
	out := make([]string, len(registry))
	for i := range registry {
		out[i] = string(registry[i].Name)
	}
	return out
}

// validParamName enforces the ASCII snake_case ≤32-byte
// constraint on registry names. Defended against here AND
// in the wire-payload validator so a hand-crafted JSON cannot
// inject a name that the registry tolerates but downstream
// log/metric handling would mangle.
func validParamName(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '_':
		default:
			return false
		}
	}
	// Cannot start with a digit or underscore — that's
	// reserved for future name-spacing schemes.
	first := s[0]
	if first == '_' || (first >= '0' && first <= '9') {
		return false
	}
	return true
}

// formatNames returns a comma-joined list of registered names
// suitable for embedding in error messages.
func formatNames() string {
	return strings.Join(Names(), ", ")
}
