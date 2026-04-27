package mining

import (
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"time"
)

// This file defines the compile-time constants and runtime-settable
// fork height that govern the v2 ("NVIDIA-locked") upgrade of the
// mining sub-protocol. The full spec is
// QSDM/docs/docs/MINING_PROTOCOL_V2.md; the three parameter values
// below are ratified in §13 of that doc (2026-04-24 owner sign-off).
//
// Phase 2 of the pivot (this file is part of it) introduces v2 as
// a compile-time concept only — ForkV2Height defaults to
// math.MaxUint64, so no proof is ever routed through the v2 gate
// until a later activation step (Phase 4 genesis-reset) calls
// SetForkV2Height with the real activation block number.
//
// Why a runtime-settable gate rather than another const: fork
// heights are per-network configuration (testnet vs mainnet; local
// integration-test chains that activate v2 at height 0). Making it
// a package-level atomic lets unit tests exercise v2 code paths
// without having to recompile the package and without leaking test
// state across concurrent t.Parallel() runs.

// ProtocolVersionV2 is the Proof.Version value for the NVIDIA-locked
// upgrade. v1 (= ProtocolVersion, declared in doc.go) remains the
// value Proof.Version is checked against for heights below
// ForkV2Height. At or above ForkV2Height validators switch to this
// value and additionally enforce the mandatory-attestation rules of
// MINING_PROTOCOL_V2 §3 and §7.
//
// Bumping this is a hard fork; see MINING_PROTOCOL_V2.md §1 for
// the full list of consensus-visible changes it implies.
const ProtocolVersionV2 uint32 = 2

// FreshnessWindow is the maximum age of an attestation nonce
// (MINING_PROTOCOL_V2 §6.2). A proof whose Attestation.IssuedAt is
// more than FreshnessWindow before the validator's wall clock is
// rejected with ReasonAttestationStale. The validator's nonce
// ring-buffer retains issued nonces for 2*FreshnessWindow so
// double-spend of the same challenge is also detectable.
//
// 60 seconds balances replay resistance (tight enough that a
// replayed bundle becomes invalid within one block-production
// cycle) against false-positive rejection (loose enough that a
// miner on a slow residential link can fetch, compute, and submit
// without flickering in and out of staleness).
const FreshnessWindow = 60 * time.Second

// MinEnrollStakeDust is the minimum stake a miner must lock to
// register a (node_id, gpu_uuid, hmac_key) tuple in the
// nvidia-hmac-v1 operator registry (MINING_PROTOCOL_V2 §5.4).
// Encoded in dust (1 CELL = 1e8 dust) so the whole constant fits
// in a uint64 without any floating-point conversion.
//
// 10 CELL is the ratified initial value (2026-04-24). Governance
// can adjust post-launch via the chain-config delta mechanism of
// §5.2; this constant is the genesis default.
const MinEnrollStakeDust uint64 = 10 * 100_000_000

// AttestationTypeCC and AttestationTypeHMAC are the two whitelisted
// values for Attestation.Type under v2. Any other value at or above
// ForkV2Height is rejected with ErrAttestationTypeUnknown. These
// constants are exported because pkg/mining/attest/* and the
// monitoring pipeline in pkg/monitoring both need to format them
// consistently.
const (
	// AttestationTypeCC is the Confidential-Computing path. Bundle
	// carries an NVIDIA-signed device certificate chain, an
	// AIK-signed quote over the proof challenge, and the current
	// firmware/driver PCR-equivalents. Verified against
	// genesis-pinned NVIDIA roots — see pkg/mining/attest/cc.
	AttestationTypeCC = "nvidia-cc-v1"

	// AttestationTypeHMAC is the consumer-GPU path. Bundle is a
	// canonical-JSON object carrying nvidia-smi self-report fields
	// plus an HMAC-SHA256 over them keyed by a genesis-registered
	// operator secret. Verified against the operator registry —
	// see pkg/mining/attest/hmac.
	AttestationTypeHMAC = "nvidia-hmac-v1"
)

// forkV2Height is the chain height at which the v2 protocol
// activates. It is a package-level atomic so tests can pin it to
// 0 (or any desired value) without leaking state into other
// tests that depend on the default. The getter / setter pair is
// used by Verifier.Verify to decide which code path to run.
//
// A forkV2Height of math.MaxUint64 means "v2 is never active",
// which is the safe default until a Phase 4 genesis ceremony
// commits the activation height into the chain-config.
var forkV2Height atomic.Uint64

func init() {
	forkV2Height.Store(math.MaxUint64)
}

// ForkV2Height returns the current activation height of the v2
// NVIDIA-locked upgrade. Callers should treat the default
// (math.MaxUint64) as "v2 not yet scheduled" and must not rely on
// any specific non-default value unless it was explicitly set
// by SetForkV2Height in the same process.
func ForkV2Height() uint64 {
	return forkV2Height.Load()
}

// SetForkV2Height pins the block height at which v2 activates.
// Pass 0 to activate v2 from genesis (the intended Phase 4
// configuration for a reset chain); pass math.MaxUint64 to
// disable v2 entirely (the default). This function is safe for
// concurrent use.
//
// SetForkV2Height is intentionally unguarded by environment — it
// must be called exactly once at process startup by the chain
// initialisation path (pkg/chain or pkg/api setup) and by tests
// that need to exercise v2 code paths. Calling it mid-execution
// is a bug; validators MUST NOT be able to move the fork height
// at runtime in response to adversarial input.
func SetForkV2Height(h uint64) {
	forkV2Height.Store(h)
}

// IsV2 reports whether a block at the given chain height is
// governed by the v2 NVIDIA-locked protocol. Callers should
// use this in preference to open-coded comparisons against
// ForkV2Height() so the "boundary-inclusive" semantics are
// consistent everywhere.
func IsV2(height uint64) bool {
	return height >= ForkV2Height()
}

// ErrAttestationRequired is returned by the verifier when a proof
// at or above ForkV2Height carries an empty or zero-value
// Attestation. v1 proofs with an absent attestation are
// explicitly permitted by MINING_PROTOCOL.md §6; v2 is the
// opposite.
var ErrAttestationRequired = errors.New("mining: v2 proof missing mandatory attestation")

// ErrAttestationTypeUnknown is returned when Attestation.Type is
// non-empty but not in the whitelist (AttestationTypeCC or
// AttestationTypeHMAC). Unknown types are rejected rather than
// ignored so that a future v3 type-string cannot be smuggled into
// v2 blocks and silently accepted by an un-updated validator.
var ErrAttestationTypeUnknown = errors.New("mining: unknown attestation type")

// ErrAttestationStale is returned when Attestation.IssuedAt is
// more than FreshnessWindow before the verifier's wall clock, or
// more than the tolerated skew in the future.
var ErrAttestationStale = errors.New("mining: attestation outside freshness window")

// ErrAttestationNonceMismatch is returned when Attestation.Nonce
// cannot be matched against a nonce this validator (or a trusted
// peer validator) issued within FreshnessWindow.
var ErrAttestationNonceMismatch = errors.New("mining: attestation nonce not recognised")

// ErrAttestationSignatureInvalid is returned by either the CC or
// HMAC path when the cryptographic check over the bundle fails.
// The verifier wraps this with more specific context via fmt.Errorf
// but preserves this sentinel for errors.Is callers.
var ErrAttestationSignatureInvalid = errors.New("mining: attestation cryptographic verification failed")

// ErrAttestationBundleMalformed is returned when the base64 or
// inner JSON of Attestation.Bundle cannot be parsed. Distinct
// sentinel from ErrAttestationSignatureInvalid so downstream
// metrics can tell "attacker sent garbage" apart from "operator
// lost their HMAC key."
var ErrAttestationBundleMalformed = errors.New("mining: attestation bundle not parseable")

// -----------------------------------------------------------------------------
// AttestationVerifier interface (pluggable per-type verification)
// -----------------------------------------------------------------------------

// AttestationVerifier is the contract a v2 verifier depends on
// for cryptographic attestation checks. The interface lives in
// pkg/mining (not pkg/mining/attest) so VerifierConfig can embed
// an implementation without pkg/mining importing its own
// subpackages — that direction would create an import cycle for
// any implementation that needs access to Proof.
//
// Phase 2c will ship the two production implementations under
// pkg/mining/attest/cc and pkg/mining/attest/hmac. Phase 2b (the
// commit that adds this interface) ships FailClosedVerifier as
// the default — it rejects every v2 proof so that a misconfigured
// validator that forgets to wire in a real verifier fails safely
// rather than silently accepting anything.
//
// Implementations MUST be safe for concurrent use: a single
// AttestationVerifier instance is shared across all validator
// goroutines processing proofs.
type AttestationVerifier interface {
	// VerifyAttestation cryptographically validates a v2 proof's
	// attestation. It is called AFTER the Verifier has confirmed
	// the proof is well-formed and the height gate has selected
	// the v2 path, so implementations may assume:
	//
	//   - p.Version == ProtocolVersionV2
	//   - p.Attestation.Type is non-empty
	//   - now is the validator's current wall-clock time (it is
	//     passed in rather than read internally so tests are
	//     deterministic)
	//
	// Implementations dispatch on p.Attestation.Type to run the
	// nvidia-cc-v1 or nvidia-hmac-v1 verification flow described
	// in MINING_PROTOCOL_V2 §3.2. A nil return means "attested";
	// any non-nil return means reject. Errors should wrap one of
	// the ErrAttestation* sentinels in this file so downstream
	// metrics can group by reason.
	VerifyAttestation(p Proof, now time.Time) error
}

// FailClosedVerifier is the default AttestationVerifier. It
// rejects every proof with ErrAttestationSignatureInvalid and is
// used when VerifierConfig.Attestation is left nil. The name is
// deliberately scary so reviewers catch its presence in
// production configurations.
//
// The rationale for fail-closed over fail-open is MINING_PROTOCOL_V2
// §9.1's attacker model: a misconfigured validator that accepts
// unattested proofs is worse than one that accepts none, because
// the former silently erodes the attestation guarantee while the
// latter surfaces immediately as "nothing validates." Phase 2c
// replaces FailClosedVerifier with the real dispatch tree.
type FailClosedVerifier struct{}

// VerifyAttestation on FailClosedVerifier always rejects with a
// wrapped ErrAttestationSignatureInvalid so validator operators
// see a distinctive log line if they forget to wire in a real
// verifier.
func (FailClosedVerifier) VerifyAttestation(_ Proof, _ time.Time) error {
	return fmt.Errorf("mining: FailClosedVerifier is the default AttestationVerifier; "+
		"wire in a real pkg/mining/attest implementation before activating FORK_V2: %w",
		ErrAttestationSignatureInvalid)
}
