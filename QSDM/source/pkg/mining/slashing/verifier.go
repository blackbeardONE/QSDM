package slashing

import (
	"fmt"
	"sync"
)

// EvidenceVerifier verifies that an evidence blob proves the
// alleged offence. Implementations live in per-kind sub-packages
// (forged_attestation/, double_mining/, etc.); the wire-level
// dispatch happens through Dispatcher below.
//
// Contract:
//
//   - Verify is pure: no I/O, no clock reads beyond the `now`
//     parameter, no goroutine fan-out. This makes slash txs
//     deterministic across all validators that run the same
//     binary.
//   - On success returns the maximum dust amount the verifier
//     judges the offence is worth (the actual forfeiture is
//     min(payload.SlashAmountDust, verifier-cap), enforced by
//     the chain-side applier).
//   - On failure returns ErrEvidenceVerification or any error
//     wrapping it.
type EvidenceVerifier interface {
	// Kind returns the EvidenceKind this verifier handles. The
	// dispatcher uses this for self-registration; a verifier
	// returning the wrong kind is a programmer error and the
	// dispatcher panics at registration time.
	Kind() EvidenceKind

	// Verify decodes the evidence blob, runs its
	// kind-specific cryptographic checks, and returns the
	// maximum dust the verifier considers slashable. The
	// `currentHeight` parameter lets verifiers reject stale
	// evidence (e.g. forged-attestation evidence older than
	// some retention window).
	Verify(p SlashPayload, currentHeight uint64) (maxSlashDust uint64, err error)
}

// Dispatcher is a type-keyed registry of EvidenceVerifier
// implementations. Multiple verifiers per kind are NOT
// supported — the dispatcher panics on duplicate registration
// because two implementations for the same kind would create
// non-determinism.
type Dispatcher struct {
	mu        sync.RWMutex
	verifiers map[EvidenceKind]EvidenceVerifier
}

// NewDispatcher returns an empty dispatcher. Production wiring
// uses NewProductionDispatcher (defined in production.go,
// follow-on commit) which registers all known evidence kinds.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{verifiers: make(map[EvidenceKind]EvidenceVerifier)}
}

// Register installs the verifier for v.Kind(). Panics on a
// duplicate registration — at process boot, that's the right
// failure mode (a bug like double-init must surface
// immediately).
func (d *Dispatcher) Register(v EvidenceVerifier) {
	if d == nil {
		panic("slashing: Register on nil Dispatcher")
	}
	if v == nil {
		panic("slashing: Register received nil verifier")
	}
	k := v.Kind()
	if k == "" {
		panic("slashing: verifier returned empty Kind()")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.verifiers[k]; exists {
		panic(fmt.Sprintf("slashing: duplicate verifier registration for kind %q", k))
	}
	d.verifiers[k] = v
}

// Verify dispatches to the verifier for p.EvidenceKind. Returns
// ErrUnknownEvidenceKind if no verifier is registered for the
// given kind — a node running an older binary that does not yet
// know about a new kind will reject the slash tx, which is the
// safe fail-closed behaviour for an under-versioned validator.
func (d *Dispatcher) Verify(p SlashPayload, currentHeight uint64) (uint64, error) {
	if d == nil {
		return 0, fmt.Errorf("slashing: nil dispatcher")
	}
	d.mu.RLock()
	v, ok := d.verifiers[p.EvidenceKind]
	d.mu.RUnlock()
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownEvidenceKind, p.EvidenceKind)
	}
	return v.Verify(p, currentHeight)
}

// Kinds returns the set of EvidenceKinds the dispatcher knows
// about, sorted in the AllEvidenceKinds order. Useful for
// startup logging and operator-side diagnostics.
func (d *Dispatcher) Kinds() []EvidenceKind {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]EvidenceKind, 0, len(AllEvidenceKinds))
	for _, k := range AllEvidenceKinds {
		if _, ok := d.verifiers[k]; ok {
			out = append(out, k)
		}
	}
	return out
}

// StubVerifier is a placeholder EvidenceVerifier that ALWAYS
// rejects. Useful for two scenarios:
//
//  1. Wire a kind into the dispatcher BEFORE its real verifier
//     ships, so slash txs of that kind get a clean
//     "not-yet-implemented" rejection rather than the more-
//     ambiguous ErrUnknownEvidenceKind.
//  2. Test-fixture seeding: registries that need at least one
//     verifier per kind for coverage checks.
//
// Production dispatchers MUST replace each StubVerifier with a
// real implementation before slashing of that kind can take
// effect on-chain.
type StubVerifier struct {
	K EvidenceKind
}

// Kind implements EvidenceVerifier.
func (s StubVerifier) Kind() EvidenceKind { return s.K }

// Verify always returns a wrapped ErrEvidenceVerification.
func (s StubVerifier) Verify(_ SlashPayload, _ uint64) (uint64, error) {
	return 0, fmt.Errorf("%w: %q verifier is a stub (not yet implemented)",
		ErrEvidenceVerification, s.K)
}

// Compile-time guard that StubVerifier satisfies EvidenceVerifier.
var _ EvidenceVerifier = StubVerifier{}
