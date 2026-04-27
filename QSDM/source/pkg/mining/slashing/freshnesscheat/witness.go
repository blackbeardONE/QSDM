// Package freshnesscheat implements the EvidenceVerifier for
// slashing.EvidenceKindFreshnessCheat — the third and final
// member of the v2 slashing trilogy.
//
// # The offence
//
// A freshness-cheat is the canonical "validator collusion or
// clock skew" case: an enrolled operator's mining Proof was
// **accepted** on-chain even though its attestation
// `bundle.IssuedAt` falls outside the protocol's freshness
// window relative to the chain's wall-clock anchor at acceptance
// time. In a well-behaved network this never happens — the
// attestation verifier (`pkg/mining/attest/hmac`) rejects stale
// proofs synchronously. The offence therefore implies one of:
//
//   - A validator that ran a tampered binary (suppressed the
//     freshness check).
//   - A validator whose system clock is meaningfully off from
//     the rest of the network's consensus time.
//   - An operator who exploited a real bug in the attestation
//     verifier and managed to land a stale proof anyway.
//
// All three cases are slashable: the operator collected a
// reward they should not have. The submitter of the slash —
// any peer — re-runs the verification deterministically against
// a witness statement of "this proof was on-chain at height H,
// and at H the chain's wall-clock anchor was T".
//
// # The BFT-finality dependency
//
// The verifier needs a *trusted* `(Height, BlockTime)` pair,
// because that's the chain-side observation the offence is
// proven against. Without BFT finality (or an equivalent
// quorum-attested block-header feed) there is no chain-internal
// way to authenticate such a pair.
//
// Rather than ship `freshness-cheat` as a permanent stub
// (`StubVerifier`, always-reject), this package defines a
// `BlockInclusionWitness` interface that callers wire to
// whatever observability layer they have:
//
//   - In production today: `RejectAllWitness` — every slash of
//     this kind is rejected with a kind-specific
//     `ErrEvidenceVerification` ("witness layer not configured")
//     so operators see exactly what's missing instead of the
//     generic stub error. Same end-user behavior as the previous
//     `StubVerifier{K: EvidenceKindFreshnessCheat}` but with
//     better diagnostics.
//   - On testnets / dev: `TrustingTestWitness` — accepts every
//     anchor at face value. Lets the slashing path run end-to-
//     end so bugs surface before mainnet.
//   - When BFT finality lands: a real `quorum.HeaderWitness` (or
//     similar) plugs in here and freshness-cheat starts slashing
//     for real, with no changes elsewhere in this package.
//
// See MINING_PROTOCOL_V2.md §8.2 (slashing-table row) and §12.3
// (deferred-work register) for the spec posture.
package freshnesscheat

import (
	"errors"
	"fmt"

	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

// BlockInclusionWitness is the trust anchor for the chain-side
// observation that lets a freshness-cheat slash be evaluated
// post-hoc.
//
// Contract:
//
//   - Pure: implementations MUST NOT do I/O, sleep, or mutate
//     external state during VerifyAnchor. The whole verifier
//     runs on the consensus-apply path and must be byte-
//     deterministic across all validators.
//   - Idempotent: repeated calls with the same arguments MUST
//     return the same result.
//   - Fail-closed: ambiguous or unsupported anchors MUST return
//     an error wrapping `ErrAnchorUnverified`. A nil error means
//     "every validator running this binary will agree this
//     anchor is real".
//
// Implementations MAY be stateful internally (e.g. an in-memory
// index of finalised block headers populated by an upstream BFT
// pipeline), as long as the externally-observable behaviour
// satisfies the contract above.
type BlockInclusionWitness interface {
	// VerifyAnchor returns nil iff the witness can certify
	// that block at `height` was sealed with `blockTime` and
	// included a transaction whose proof_id matches `proofID`
	// (32-byte canonical proof identity from `mining.Proof.ID`).
	//
	// The proofID parameter binds the witness to the specific
	// proof being slashed, so a malicious slasher cannot reuse
	// a real anchor against an unrelated proof.
	//
	// Returns an error wrapping ErrAnchorUnverified when the
	// witness cannot certify the anchor. The Verify path
	// surfaces this to the dispatcher as
	// slashing.ErrEvidenceVerification.
	VerifyAnchor(height uint64, blockTime int64, proofID [32]byte) error
}

// ErrAnchorUnverified is the sentinel returned by witnesses
// when they cannot certify an `(height, blockTime, proofID)`
// triple. The Verifier wraps this with
// `slashing.ErrEvidenceVerification` before returning to the
// dispatcher.
var ErrAnchorUnverified = errors.New("freshnesscheat: anchor not verifiable by witness")

// RejectAllWitness is the default production posture: it rejects
// every anchor. Use this in any binary that does not have a
// real BFT-finality witness wired yet (which is every binary
// today).
//
// The kind-specific error message names the missing component
// so operators receiving an `ErrEvidenceVerification` see
// "freshnesscheat: witness layer not configured" instead of the
// generic stub error.
type RejectAllWitness struct{}

// VerifyAnchor implements BlockInclusionWitness.
func (RejectAllWitness) VerifyAnchor(_ uint64, _ int64, _ [32]byte) error {
	return fmt.Errorf(
		"%w: production binary has no quorum-attested block-header source "+
			"(BFT finality dependency, see MINING_PROTOCOL_V2.md §12.3)",
		ErrAnchorUnverified)
}

// TrustingTestWitness accepts every anchor at face value. NEVER
// USE IN PRODUCTION — it lets a malicious slasher slash anyone
// by inventing an arbitrary `(height, blockTime)` pair.
//
// Two legitimate uses:
//
//   - Unit / integration tests for the freshness-cheat verifier
//     itself, where the test author owns both sides of the
//     evidence and wants to exercise the verification logic.
//   - Local devnets where every node trusts every other node,
//     used for protocol-development end-to-end runs of the
//     slashing pipeline before the real witness ships.
//
// To make accidental production use loud, the type embeds a
// runtime tag in its name and does not satisfy any conditional
// "default" interface.
type TrustingTestWitness struct{}

// VerifyAnchor implements BlockInclusionWitness — always
// returns nil.
func (TrustingTestWitness) VerifyAnchor(_ uint64, _ int64, _ [32]byte) error {
	return nil
}

// FixedAnchorWitness is a witness that certifies exactly one
// pre-registered (height, blockTime, proofID) tuple. Useful in
// ops scenarios where an operator wants to manually attest a
// single freshness-cheat case: register the anchor at startup,
// run the slash, decommission the witness.
//
// Not appropriate for general production (it doesn't scale to a
// full block-header index), but stronger than TrustingTestWitness
// because it pins to one specific anchor — a malicious slasher
// can't repurpose it.
type FixedAnchorWitness struct {
	Height    uint64
	BlockTime int64
	ProofID   [32]byte
}

// VerifyAnchor implements BlockInclusionWitness.
func (w FixedAnchorWitness) VerifyAnchor(height uint64, blockTime int64, proofID [32]byte) error {
	if height != w.Height || blockTime != w.BlockTime || proofID != w.ProofID {
		return fmt.Errorf(
			"%w: witness was registered for (height=%d, time=%d, proof_id=%x) "+
				"but slash anchored at (height=%d, time=%d, proof_id=%x)",
			ErrAnchorUnverified,
			w.Height, w.BlockTime, w.ProofID,
			height, blockTime, proofID)
	}
	return nil
}

// Compile-time guards.
var (
	_ BlockInclusionWitness = RejectAllWitness{}
	_ BlockInclusionWitness = TrustingTestWitness{}
	_ BlockInclusionWitness = FixedAnchorWitness{}
)

// wrapAnchorErr converts a witness error into a slashing-domain
// error suitable for returning from Verifier.Verify. Centralised
// so every callsite produces the same wrapping order.
func wrapAnchorErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", err, slashing.ErrEvidenceVerification)
}
