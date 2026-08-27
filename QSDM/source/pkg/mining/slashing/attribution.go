package slashing

import (
	"errors"
	"sync/atomic"
)

// ErrAttestationUnattributable is returned by any verifier whose offence can
// only be established by attributing an nvidia-hmac-v1 attestation bundle to
// its claimed producer, while that attribution is impossible.
var ErrAttestationUnattributable = errors.New(
	"slashing: nvidia-hmac-v1 bundles are not attributable, so this offence cannot be proven")

// hmacAttestationAttributable gates every slash whose proof rests on deciding
// who produced an attestation bundle. It defaults to FALSE, and today there is
// no configuration that should set it true.
//
// # Why attribution is impossible
//
// nvidia-hmac-v1 authenticates a bundle with a SYMMETRIC key, and that key is
// public chain state. EnrollPayload.HMACKey travels in a plaintext enrollment
// transaction, pkg/mining/enrollment/validate.go only length-checks it, and
// pkg/chain/enrollment_apply.go copies it into the replayed EnrollmentRecord.
// The field documents itself: "HMACKey is the operator's shared signing key.
// Public chain state."
//
// So every node that replays the chain holds every miner's key. That collapses
// both directions of the inference a slash needs:
//
//   - A bundle whose HMAC VERIFIES proves nothing, because anyone who can read
//     chain state can compute the same MAC for any enrolled node.
//   - A bundle whose HMAC FAILS proves nothing either, because anyone can emit
//     arbitrary bytes naming any victim.
//
// A symmetric MAC cannot attribute in a system where the key is public. This
// is a property of the protocol, not a defect in the verifiers below, and it
// cannot be fixed inside them.
//
// # What that meant in practice
//
// forgedattest slashed whenever hmac.Verifier REJECTED a bundle -- so the
// evidence of the offence was itself forgeable, and manufacturing it required
// only naming a victim and supplying bytes that fail to verify.
// cmd/qsdmcli/slash_helper.go ships that flow, noting it "deliberately does
// NOT re-run the HMAC verifier locally" because "the slasher does not have the
// offender's HMAC key" -- but the key is public, so possession was never the
// obstacle; attribution was.
//
// doublemining has the same shape from the other side: it slashes on two
// conflicting proofs that both carry valid MACs, which any chain-state reader
// can mint for any enrolled node.
//
// Both are wired unconditionally in production (internal/v2wiring), and
// pkg/chain/slash_apply.go pays a share of the drained bond to an address the
// slasher chooses. That made them bond-theft primitives rather than deterrents.
//
// # What would make this true
//
// Attestation must become asymmetric: the miner signs with a private key and
// enrollment registers only the PUBLIC half, so a valid signature attributes
// and an invalid one cannot be manufactured against a victim. That is a
// protocol change requiring re-enrollment, not a flag flip. Whoever implements
// it should flip this gate in the same change, and only then.
//
// Failing closed loses no honest capability. An honest reporter cannot produce
// attributable evidence either, so the rails could only ever be used to steal.
var hmacAttestationAttributable atomic.Bool

// SetHMACAttestationAttributable declares that attestation bundles can be
// attributed to their producer. Enable ONLY once attestation is asymmetric and
// enrollment stores a public key rather than a shared secret; while the key is
// public chain state this re-opens a bond-theft primitive.
func SetHMACAttestationAttributable(attributable bool) {
	hmacAttestationAttributable.Store(attributable)
}

// HMACAttestationAttributable reports the current setting.
func HMACAttestationAttributable() bool { return hmacAttestationAttributable.Load() }

// GuardAttestationAttribution returns ErrAttestationUnattributable unless
// attribution has been declared possible. Verifiers whose offence depends on
// deciding who produced a bundle must call this before any other work, so an
// unprovable accusation cannot reach a slash.
func GuardAttestationAttribution() error {
	if hmacAttestationAttributable.Load() {
		return nil
	}
	return ErrAttestationUnattributable
}
