//go:build !cgo || dilithium_circl
// +build !cgo dilithium_circl

package crypto

import (
	"fmt"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// MLDSAKey is an ML-DSA-87 public key prepared once (mldsa_key.go). In this
// backend it holds the unpacked key, which mldsa87.Verify only reads, so one
// MLDSAKey can verify on several goroutines at once.
type MLDSAKey struct {
	raw []byte             // the packed key: a copy of what NewMLDSAKey got
	pk  *mldsa87.PublicKey // raw unpacked; nil if raw is not a packed ML-DSA-87 key
}

// NewMLDSAKey prepares publicKey, of which it keeps a copy. It does not
// fail: Verify refuses a key that VerifyWithPublicKey refuses, with the same
// error.
func NewMLDSAKey(publicKey []byte) *MLDSAKey {
	k := &MLDSAKey{raw: append([]byte(nil), publicKey...)}
	if len(k.raw) == mldsa87.PublicKeySize {
		pk := new(mldsa87.PublicKey)
		if pk.UnmarshalBinary(k.raw) == nil {
			k.pk = pk
		}
	}
	return k
}

// Verify returns what NewDilithiumVerifyOnly().VerifyWithPublicKey(message,
// signature, key) returns: the same checks in the same order, with the key
// unpacked once instead of on every call.
func (k *MLDSAKey) Verify(message, signature []byte) (bool, error) {
	if k.pk == nil {
		return NewDilithiumVerifyOnly().VerifyWithPublicKey(message, signature, k.raw)
	}
	if len(signature) != mldsa87.SignatureSize {
		return false, fmt.Errorf("dilithium (circl backend): signature must be %d bytes, got %d",
			mldsa87.SignatureSize, len(signature))
	}
	return mldsa87.Verify(k.pk, message, nil, signature), nil
}
