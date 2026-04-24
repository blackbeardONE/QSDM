// Package cc is the consensus-side verifier for the
// "nvidia-cc-v1" attestation path — datacenter GPUs signing
// quotes with their hardware AIK against genesis-pinned NVIDIA
// roots (MINING_PROTOCOL_V2_NVIDIA_LOCKED.md §3.2.1).
//
// This file ships the StubVerifier: a placeholder that
// cryptographically rejects every nvidia-cc-v1 proof with a
// clear "not yet available" error. It exists so validator wiring
// code can register a handler under mining.AttestationTypeCC
// today — the dispatcher's AssertAllRegistered check requires
// both attestation types to have SOMETHING registered or it
// fails at boot. Shipping the stub means operators get a loud,
// actionable error the moment a v2 proof with Type="nvidia-cc-v1"
// arrives, instead of a silent dispatch-routing miss.
//
// The real verifier (AIK chain validation, quote parsing, PCR
// comparison against a pinned reference manifest) is Phase 2c-iv
// work and depends on real Hopper/Blackwell hardware +
// NVIDIA CC SDK integration. Replacing this stub with the real
// implementation is expected to be a single-file swap; the
// registration surface via cc.NewStubVerifier is the contract.
package cc

import (
	"fmt"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// ErrNotYetAvailable is returned by StubVerifier.VerifyAttestation
// for every attempt. Wraps mining.ErrAttestationTypeUnknown so
// metrics/log aggregators that key on standard sentinels route
// this into the "attestation infrastructure problem" bucket
// rather than "bad attestation crypto" bucket — both would be
// technically true but the former is actionable ("upgrade your
// validator"), the latter is not.
var ErrNotYetAvailable = fmt.Errorf(
	"cc: nvidia-cc-v1 verifier not yet available in this build "+
		"(Phase 2c-iv); upgrade required: %w",
	mining.ErrAttestationTypeUnknown,
)

// StubVerifier satisfies mining.AttestationVerifier for
// Attestation.Type == "nvidia-cc-v1" and rejects every proof
// with ErrNotYetAvailable. Used exclusively as the registration
// placeholder until the Phase 2c-iv AIK verifier lands.
//
// The stub is NOT a backdoor: it cannot be configured to ever
// accept. The only way to accept nvidia-cc-v1 proofs is to
// replace the Verifier binding in the dispatcher with a real
// implementation.
type StubVerifier struct{}

// NewStubVerifier returns a StubVerifier ready to register under
// mining.AttestationTypeCC.
func NewStubVerifier() StubVerifier { return StubVerifier{} }

// VerifyAttestation rejects any proof whose Attestation.Type is
// "nvidia-cc-v1" (which, via dispatcher routing, is every proof
// that reaches this verifier). Returns ErrNotYetAvailable
// unconditionally.
//
// We still defend-in-depth: if the dispatcher routes wrong (bug)
// and passes us a non-CC type, we return a different error so
// the bug is visible in logs rather than masked by the generic
// "not yet available" message.
func (StubVerifier) VerifyAttestation(p mining.Proof, _ time.Time) error {
	if p.Attestation.Type != mining.AttestationTypeCC {
		return fmt.Errorf(
			"cc: StubVerifier received %q but is only registered for %q — "+
				"dispatcher routing bug: %w",
			p.Attestation.Type, mining.AttestationTypeCC,
			mining.ErrAttestationTypeUnknown,
		)
	}
	return ErrNotYetAvailable
}

// Compile-time guard.
var _ mining.AttestationVerifier = StubVerifier{}
