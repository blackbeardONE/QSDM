//go:build !cgo || dilithium_circl
// +build !cgo dilithium_circl

// Package crypto — ML-DSA-87 signature backend, pure-Go variant.
//
// This file is the default non-CGO backend as of Stage B
// (2026-05-06). It replaces the prior dilithium_stub.go (deleted)
// for any build with CGO disabled. It satisfies the same
// *Dilithium API (NewDilithium, NewDilithiumVerifyOnly, Sign,
// Verify, VerifyWithPublicKey, Free) using the cloudflare/circl
// pure-Go implementation of FIPS 204 ML-DSA-87, so a non-CGO
// validator verifies (and optionally signs) the same
// wire-format signatures the CGO+liboqs build produces.
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
// Stage progression:
//
//   - Stage A (commit 522f567) added this file behind the opt-in
//     `dilithium_circl` build tag so parity tests could soak in
//     CI without changing operational behaviour.
//   - Stage B (this commit) flips the default: !cgo builds now
//     use this backend automatically, and dilithium_stub.go is
//     deleted. The qsdm_stub_active{kind="dilithium"} gauge
//     remains in the registry for forward compatibility but no
//     code path flips it on under !cgo any more — it stays at
//     0 in every build configuration QSDM ships.
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
	"sync"

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

// GetPublicKey returns a defensive copy of the handle's packed
// public key. Matches the CGO backend's GetPublicKey: returns
// nil for a nil receiver or a verify-only handle constructed
// without a key. pkg/wallet/wallet.go relies on this to embed
// the validator's public key in outbound signed transactions.
func (d *Dilithium) GetPublicKey() []byte {
	if d == nil || d.verifyKey == nil {
		return nil
	}
	pk, err := d.verifyKey.MarshalBinary()
	if err != nil {
		// MarshalBinary on a freshly-generated circl mldsa87
		// PublicKey is infallible (the only failure mode is
		// nil receiver, which we already gated above). If
		// circl ever introduces a real error path here,
		// returning nil matches the CGO backend's "key not
		// available" semantics — callers already handle nil.
		return nil
	}
	return pk
}

// GetPrivateKey returns a defensive copy of the handle's packed
// private key. Matches the CGO backend; returns nil for
// verify-only handles. Used by pkg/wallet for on-disk wallet
// persistence in the optional encrypted-keystore flow.
func (d *Dilithium) GetPrivateKey() []byte {
	if d == nil || d.signKey == nil {
		return nil
	}
	sk, err := d.signKey.MarshalBinary()
	if err != nil {
		return nil
	}
	return sk
}

// SignOptimized is an alias for Sign in the pure-Go backend. The
// CGO backend exposes this as a memory-pooled fast path that
// reuses signature buffers across calls (5-10% throughput win at
// scale). circl already allocates internally; layering a pool
// here would not move the bottleneck. Kept as a separate method
// for API parity so pkg/wallet (which calls SignOptimized
// explicitly to opt in to the perf path on CGO builds) compiles
// without conditionals.
func (d *Dilithium) SignOptimized(message []byte) ([]byte, error) {
	return d.Sign(message)
}

// SignCompressed signs a message and returns a zstd-compressed
// signature (typically ~50% the size of the raw 4627-byte
// FIPS 204 signature). Compression is implemented in
// pkg/crypto/signature_compression.go (pure Go) so this method
// is wire-compatible with the CGO backend's SignCompressed —
// both produce the same compressed bytes for the same input
// signature, modulo the underlying signature being randomized
// per FIPS 204 §6.1.
func (d *Dilithium) SignCompressed(message []byte) ([]byte, error) {
	sig, err := d.Sign(message)
	if err != nil {
		return nil, err
	}
	return CompressSignature(sig)
}

// VerifyCompressed decompresses a signature produced by
// SignCompressed (or by the CGO backend's SignCompressed) and
// verifies it under the handle's own public key.
func (d *Dilithium) VerifyCompressed(message []byte, compressedSig []byte) (bool, error) {
	sig, err := DecompressSignature(compressedSig)
	if err != nil {
		return false, fmt.Errorf("dilithium (circl backend): decompress: %w", err)
	}
	return d.Verify(message, sig)
}

// VerifyWithPublicKeyCompressed decompresses a signature and
// verifies it under an externally-supplied public key. This is
// the consensus-critical companion to SignCompressed; pkg/wallet
// uses it on the verify side of compressed-signature transports.
func (d *Dilithium) VerifyWithPublicKeyCompressed(message []byte, compressedSig []byte, publicKey []byte) (bool, error) {
	sig, err := DecompressSignature(compressedSig)
	if err != nil {
		return false, fmt.Errorf("dilithium (circl backend): decompress: %w", err)
	}
	return d.VerifyWithPublicKey(message, sig, publicKey)
}

// SignBatchOptimized signs N messages and returns N signatures
// in parallel. The CGO backend gets a measured 10-100× speedup
// from running multiple liboqs sign loops concurrently because
// each ML-DSA-87 signature is dominated by SHAKE / NTT
// arithmetic that does not contend on shared state. circl's
// pure-Go signer is also stateless per call, so the same
// fan-out works.
//
// On the first error, all in-flight goroutines are allowed to
// finish (we don't cancel — circl signs in <2ms per message,
// fan-out is short-lived) and the first non-nil error is
// returned.
func (d *Dilithium) SignBatchOptimized(messages [][]byte) ([][]byte, error) {
	if d == nil || d.signKey == nil {
		return nil, ErrSignVerifyOnly
	}
	if len(messages) == 0 {
		return nil, nil
	}
	results := make([][]byte, len(messages))
	errs := make([]error, len(messages))
	var wg sync.WaitGroup
	for i := range messages {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sig := make([]byte, mldsa87.SignatureSize)
			if err := mldsa87.SignTo(d.signKey, messages[idx], nil, true, sig); err != nil {
				errs[idx] = err
				return
			}
			results[idx] = sig
		}(i)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return nil, fmt.Errorf("dilithium (circl backend): batch sign: %w", e)
		}
	}
	return results, nil
}
