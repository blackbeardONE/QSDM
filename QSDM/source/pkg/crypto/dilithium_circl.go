//go:build !cgo && dilithium_circl
// +build !cgo,dilithium_circl

// Package crypto — ML-DSA-87 signature backend, pure-Go variant.
//
// This file is selected by the `dilithium_circl` build tag in
// non-CGO builds, displacing the always-error stub at
// dilithium_stub.go. It satisfies the same *Dilithium API
// (NewDilithium, NewDilithiumVerifyOnly, Sign, Verify,
// VerifyWithPublicKey, Free) using the cloudflare/circl pure-Go
// implementation of FIPS 204 ML-DSA-87, so a non-CGO validator
// can verify (and optionally sign) the same wire-format
// signatures the CGO+liboqs build produces.
//
// Wire-format compatibility:
//
//   - FIPS 204 §6.1 fixes ML-DSA-87 sizes byte-for-byte:
//     2592-byte public key, 4627-byte signature.
//   - liboqs's "ML-DSA-87" mode and circl's mldsa87 both
//     implement FIPS 204 with empty context bytes, so a
//     signature produced by either backend verifies in the
//     other. The parity test in dilithium_circl_test.go is the
//     regression guard that catches any future drift.
//
// Stage A scope:
//
// This file lands the backend behind an opt-in build tag so the
// default non-CGO build is byte-identical to the prior stub
// behaviour. Stage B (a follow-up commit) flips the build tag so
// non-CGO binaries use this backend by default and the
// dilithium_stub.go path is retired entirely. Splitting in two
// stages lets Stage A's parity tests run in CI before Stage B
// changes any operational behaviour.
//
// Why "VerifyOnly" still allocates a real backend:
//
// In the CGO build, NewDilithiumVerifyOnly skips loading liboqs
// signing material to save process-wide memory; in this pure-Go
// backend the cost of a *Dilithium without a private key is one
// pointer field (the public key is supplied per-call to
// VerifyWithPublicKey), so the distinction is purely API
// compatibility — both constructors return a non-nil *Dilithium
// here and Sign on a verify-only handle returns
// ErrSignVerifyOnly so callers can detect the mode.

package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// ErrSignVerifyOnly is returned by Sign when the *Dilithium was
// constructed via NewDilithiumVerifyOnly. Callers that legitimately
// want to sign should use NewDilithium.
var ErrSignVerifyOnly = errors.New("dilithium (circl backend): handle is verify-only; use NewDilithium for signing")

// Dilithium is the pure-Go ML-DSA-87 signer/verifier. Field
// layout intentionally matches the CGO build's *Dilithium so
// callers cannot tell the two backends apart by reflection.
//
//   - signKey holds the secret key for full-power handles
//     (NewDilithium). nil for verify-only handles.
//   - verifyKey holds the public key paired with signKey.
//     Self-Sign/Self-Verify (the Verify method without an
//     external public key argument) reads from this field.
//     Set on construction; never mutated.
type Dilithium struct {
	signKey   *mldsa87.PrivateKey
	verifyKey *mldsa87.PublicKey
}

// NewDilithium generates a fresh ML-DSA-87 keypair and returns
// a signer-and-verifier handle. Returns nil only on a
// crypto/rand entropy failure (which is fatal for any signing
// path; callers that observe nil should not proceed).
func NewDilithium() *Dilithium {
	pk, sk, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		// Documented contract of the CGO build's NewDilithium
		// is "returns nil on init failure"; preserve that so
		// callers don't have to gate on a different signal in
		// the pure-Go backend.
		return nil
	}
	return &Dilithium{signKey: sk, verifyKey: pk}
}

// NewDilithiumVerifyOnly returns a verify-capable handle with
// no signing material. The handle is non-nil; Sign on it returns
// ErrSignVerifyOnly. The intent matches the CGO build:
// downstream code that only verifies signatures (e.g. mempool
// admission, block ingest) can avoid keeping signing-grade
// secrets in memory.
//
// In the pure-Go backend this is purely an API-compatibility
// affordance — there is no liboqs initialisation to elide — but
// preserving the constructor pair lets the *Dilithium type
// remain a drop-in across both backends.
func NewDilithiumVerifyOnly() *Dilithium {
	return &Dilithium{}
}

// Sign returns a FIPS 204 ML-DSA-87 signature over message,
// produced with the handle's private key and an empty context.
// Wire-format identical to the CGO build's Sign output for the
// same key (per FIPS 204 §6).
//
// The randomized variant of FIPS 204 §6.1 is selected (rand=nil
// → circl reads from crypto/rand). Verify is signature-stable
// across deterministic vs randomized signing, so the consensus
// path is unaffected by this choice; randomized signing reduces
// side-channel surface vs the deterministic mode.
func (d *Dilithium) Sign(message []byte) ([]byte, error) {
	if d == nil {
		return nil, errors.New("dilithium (circl backend): nil receiver")
	}
	if d.signKey == nil {
		return nil, ErrSignVerifyOnly
	}
	sig := make([]byte, mldsa87.SignatureSize)
	// ctx empty per FIPS 204 "pure" mode; randomized=true.
	if err := mldsa87.SignTo(d.signKey, message, nil, true, sig); err != nil {
		return nil, fmt.Errorf("dilithium (circl backend): sign: %w", err)
	}
	return sig, nil
}

// Verify checks the signature against the handle's own public
// key. Convenience for the same-process self-verify case
// (round-trip tests, key health checks). Production verifiers
// should use VerifyWithPublicKey because the public key is
// always supplied by the signed-tx envelope on the wire.
func (d *Dilithium) Verify(message []byte, signature []byte) (bool, error) {
	if d == nil {
		return false, errors.New("dilithium (circl backend): nil receiver")
	}
	if d.verifyKey == nil {
		return false, errors.New("dilithium (circl backend): handle has no verify key (constructed via NewDilithiumVerifyOnly without an externally supplied key)")
	}
	if len(signature) != mldsa87.SignatureSize {
		return false, fmt.Errorf("dilithium (circl backend): signature must be %d bytes, got %d",
			mldsa87.SignatureSize, len(signature))
	}
	return mldsa87.Verify(d.verifyKey, message, nil, signature), nil
}

// VerifyWithPublicKey is the consensus-critical entry point. It
// unpacks publicKey as a FIPS 204 ML-DSA-87 packed public key
// (PublicKeySize = 2592 bytes) and verifies signature over
// message under the empty FIPS 204 context.
//
// Returns false (without error) on a wire-valid but
// cryptographically-invalid signature; returns an error only on
// length / encoding violations the wire-format guard in
// pkg/chain/txsig.go is supposed to have caught upstream. Both
// failure modes route to the same outcome at the consensus
// applier (the tx is rejected), but distinguishing them in the
// error path lets the rejection-flood metrics carry a precise
// reason.
func (d *Dilithium) VerifyWithPublicKey(message []byte, signature []byte, publicKey []byte) (bool, error) {
	if len(publicKey) != mldsa87.PublicKeySize {
		return false, fmt.Errorf("dilithium (circl backend): public key must be %d bytes, got %d",
			mldsa87.PublicKeySize, len(publicKey))
	}
	if len(signature) != mldsa87.SignatureSize {
		return false, fmt.Errorf("dilithium (circl backend): signature must be %d bytes, got %d",
			mldsa87.SignatureSize, len(signature))
	}
	pk := new(mldsa87.PublicKey)
	if err := pk.UnmarshalBinary(publicKey); err != nil {
		return false, fmt.Errorf("dilithium (circl backend): unpack public key: %w", err)
	}
	return mldsa87.Verify(pk, message, nil, signature), nil
}

// Free is a no-op for the pure-Go backend. The CGO build needs
// it to release liboqs-allocated signing material; here Go's GC
// reclaims the keypair when the *Dilithium becomes unreachable.
// Kept for API parity so downstream callers that defer Free()
// compile under both backends.
func (d *Dilithium) Free() {}
