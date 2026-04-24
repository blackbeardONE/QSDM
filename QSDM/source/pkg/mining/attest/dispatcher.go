// Package attest contains the Dispatcher that routes v2 mining
// proofs to the correct per-type AttestationVerifier implementation
// (nvidia-hmac-v1 → pkg/mining/attest/hmac, nvidia-cc-v1 →
// pkg/mining/attest/cc, future types → their own subpackages).
//
// This package deliberately does NOT import the concrete verifier
// subpackages — it depends only on the pkg/mining.AttestationVerifier
// interface. The validator binary is where the Dispatcher gets
// constructed and populated; that composition point is the only
// place in the tree that needs to import every subpackage at once,
// keeping each subpackage independently testable.
//
// Why a separate dispatch layer instead of a giant switch in
// pkg/mining/verifier.go: the set of attestation types is open-
// ended (governance can ratify new `type` strings without forking
// the verifier), so the verifier should not hard-code the set.
// Registering at startup gives us:
//
//   - clean separation between pkg/mining (consensus) and the
//     crypto subpackages (implementation)
//   - a single place to assert "every attestation type this node
//     accepts has a verifier wired in" (see AssertAllRegistered)
//   - a default "fail-closed for unknown types" behaviour that
//     does not depend on the subpackages being present
//
// Concurrency model: Register is NOT safe to call concurrently
// with VerifyAttestation. The intended pattern is: construct at
// startup, Register all supported types, then publish the
// Dispatcher to the Verifier. After that, only VerifyAttestation
// is called, which is read-only on the internal map and therefore
// safe for concurrent use.
package attest

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// Dispatcher implements mining.AttestationVerifier by routing on
// Proof.Attestation.Type to a registered per-type verifier.
//
// Zero value is NOT usable — callers MUST use NewDispatcher to
// obtain the internal map. Doing this rather than lazy-init keeps
// VerifyAttestation on the hot path free of a nil check.
type Dispatcher struct {
	verifiers map[string]mining.AttestationVerifier
}

// NewDispatcher returns an empty Dispatcher. Until at least one
// verifier is Registered, every VerifyAttestation call rejects
// with mining.ErrAttestationTypeUnknown. This is the intended
// fail-closed default.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{verifiers: make(map[string]mining.AttestationVerifier)}
}

// Register associates attestationType (e.g. "nvidia-hmac-v1") with
// verifier. Returns an error if type or verifier is empty/nil, or
// if a verifier for this type is already registered. Duplicate
// registration is an error rather than a silent overwrite because
// double-registering usually means a composition mistake in the
// validator boot code — surfacing it at startup is safer than
// silently clobbering a mature verifier with a freshly imported
// package's init side-effect.
func (d *Dispatcher) Register(attestationType string, verifier mining.AttestationVerifier) error {
	if attestationType == "" {
		return errors.New("attest: attestationType must be non-empty")
	}
	if verifier == nil {
		return errors.New("attest: verifier must be non-nil")
	}
	if _, exists := d.verifiers[attestationType]; exists {
		return fmt.Errorf("attest: type %q already registered", attestationType)
	}
	d.verifiers[attestationType] = verifier
	return nil
}

// MustRegister is a convenience wrapper around Register that
// panics on error. Intended for validator startup code where a
// registration failure is always a programmer error — there is
// no meaningful recovery path if the set of supported attestation
// types can't be wired up.
func (d *Dispatcher) MustRegister(attestationType string, verifier mining.AttestationVerifier) {
	if err := d.Register(attestationType, verifier); err != nil {
		panic(fmt.Sprintf("attest.Dispatcher.MustRegister: %v", err))
	}
}

// RegisteredTypes returns the sorted list of attestation types
// currently registered. Used by AssertAllRegistered and by
// validator startup banners that log the accepted types.
func (d *Dispatcher) RegisteredTypes() []string {
	out := make([]string, 0, len(d.verifiers))
	for t := range d.verifiers {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// AssertAllRegistered returns an error if any of the supplied
// required types is not currently registered. Validators use this
// at startup to fail fast if the operator forgot to wire in a
// supported verifier. Passing zero types is a no-op.
//
// Example:
//
//	d := attest.NewDispatcher()
//	d.MustRegister("nvidia-hmac-v1", hmac.NewVerifier(reg))
//	if err := d.AssertAllRegistered("nvidia-hmac-v1", "nvidia-cc-v1"); err != nil {
//		log.Fatalf("attestation dispatcher misconfigured: %v", err)
//	}
func (d *Dispatcher) AssertAllRegistered(required ...string) error {
	missing := make([]string, 0)
	for _, t := range required {
		if _, ok := d.verifiers[t]; !ok {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("attest: missing verifiers for types: %v", missing)
	}
	return nil
}

// VerifyAttestation implements mining.AttestationVerifier. It
// routes on p.Attestation.Type:
//
//   - empty type                → ErrAttestationRequired
//   - unregistered type         → ErrAttestationTypeUnknown
//   - registered type           → delegated to the per-type verifier
//
// The outer pkg/mining Verifier already guards against empty type
// before calling this hook, so the empty-type branch here is
// defensive — it catches misuse when the Dispatcher is invoked
// standalone (e.g. from tests or from a non-consensus replay
// tool) rather than through the main Verify() pipeline.
func (d *Dispatcher) VerifyAttestation(p mining.Proof, now time.Time) error {
	t := p.Attestation.Type
	if t == "" {
		return fmt.Errorf("attest: empty attestation type: %w", mining.ErrAttestationRequired)
	}
	v, ok := d.verifiers[t]
	if !ok {
		return fmt.Errorf("attest: no verifier registered for type %q: %w",
			t, mining.ErrAttestationTypeUnknown)
	}
	return v.VerifyAttestation(p, now)
}

// compile-time check that Dispatcher satisfies the interface.
var _ mining.AttestationVerifier = (*Dispatcher)(nil)
