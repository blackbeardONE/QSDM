//go:build cgo && !dilithium_circl
// +build cgo,!dilithium_circl

package crypto

// MLDSAKey is an ML-DSA-87 public key prepared once (mldsa_key.go). liboqs
// takes the packed key on every verification, so in this backend preparing
// it only keeps a copy, and Verify is VerifyWithPublicKey.
type MLDSAKey struct {
	raw []byte // the packed key: a copy of what NewMLDSAKey got
}

// NewMLDSAKey prepares publicKey, of which it keeps a copy. It does not
// fail: Verify refuses a key that VerifyWithPublicKey refuses, with the same
// error.
func NewMLDSAKey(publicKey []byte) *MLDSAKey {
	return &MLDSAKey{raw: append([]byte(nil), publicKey...)}
}

// Verify returns what NewDilithiumVerifyOnly().VerifyWithPublicKey(message,
// signature, key) returns, or ErrMLDSAVerifierUnavailable when
// NewDilithiumVerifyOnly returns nil.
func (k *MLDSAKey) Verify(message, signature []byte) (bool, error) {
	d := NewDilithiumVerifyOnly()
	if d == nil {
		return false, ErrMLDSAVerifierUnavailable
	}
	defer d.Free()
	return d.VerifyWithPublicKey(message, signature, k.raw)
}
