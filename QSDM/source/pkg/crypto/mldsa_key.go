package crypto

import "errors"

// MLDSAKey (mldsa_key_circl.go, mldsa_key_oqs.go) is an ML-DSA-87 public key
// prepared once to verify many signatures: Verify returns exactly what
// VerifyWithPublicKey returns for the same key, message and signature, errors
// included.
//
// The circl backend unpacks a public key before every verification, and
// unpacking derives the key's 8x7 polynomial matrix from its seed (56
// SHAKE-128 samplings) into a new 57 KB allocation: about two thirds of the
// work of one verification. A node that verifies many signatures of the same
// key, such as every block of a producer's epoch at startup, unpacks it once
// with NewMLDSAKey instead.

// ErrMLDSAVerifierUnavailable is returned by MLDSAKey.Verify when the backend
// cannot create a verifier: the case where the CGO backend's
// NewDilithiumVerifyOnly returns nil. The circl backend never returns it.
var ErrMLDSAVerifierUnavailable = errors.New("crypto: ML-DSA verifier unavailable")
