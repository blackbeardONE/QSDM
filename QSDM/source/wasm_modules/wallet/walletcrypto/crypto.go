// Package walletcrypto — ML-DSA-87 keypair primitives used by the
// in-tree wallet WASM module (wasm_modules/wallet) and shared with
// the validator-side wallet at pkg/wallet.
//
// History: until 2026-05-11 this package shipped two stub files
// (`crypto.go` under //go:build cgo, `crypto_stub.go` under !cgo)
// that both returned `wallet crypto not available: use pkg/crypto`.
// The intent at the time was to push every caller to pkg/crypto's
// liboqs-backed implementation. That choice made the WASM build
// path unusable: a browser cannot load CGO + liboqs, so the wasm
// module's `init()` would `panic("Failed to generate key pair")`
// and the page would never finish loading.
//
// As of Stage B (commit 522f567 → 2026-05-06), pkg/crypto exposes
// a pure-Go ML-DSA-87 backend via cloudflare/circl. That backend
// produces FIPS 204-byte-compatible signatures with the liboqs path
// and compiles cleanly under GOOS=js GOARCH=wasm. This file now
// uses that backend directly — no liboqs, no CGO, no stub — so the
// same code works in:
//
//   - the QSDM validator (CGO or non-CGO build, irrelevant);
//   - the qsdmcli wallet subcommand;
//   - the browser wallet at qsdm.tech/wallet/.
//
// API:
//
//   - GenerateKeyPair — fresh keypair, KeyPair{PrivateKey, PublicKey}
//     where both fields are the FIPS 204 packed bytes (2592-byte
//     public key, 4896-byte private key).
//   - (*KeyPair).Sign — produce a 4627-byte ML-DSA-87 signature.
//   - (*KeyPair).Verify — verify a signature against the same
//     keypair's public key.
//
// The KeyPair type is byte-only so callers can serialise it without
// reaching into private fields — useful for the WASM-to-JS bridge
// which marshals everything through Uint8Array.
package walletcrypto

import (
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// KeyPair wraps a packed ML-DSA-87 keypair. The fields are the FIPS
// 204 byte-level representations: PrivateKey is the 4896-byte
// secret-key encoding, PublicKey is the 2592-byte public-key
// encoding. Either can be used independently of this struct
// (UnmarshalBinary on mldsa87.{PrivateKey,PublicKey} round-trips
// them).
type KeyPair struct {
	PrivateKey []byte
	PublicKey  []byte
}

// GenerateKeyPair returns a fresh ML-DSA-87 keypair. The randomness
// source is crypto/rand; in WASM this is wired through
// js.Global().Get("crypto").Get("getRandomValues") by the Go
// runtime, so the same call site is browser-safe.
func GenerateKeyPair() (*KeyPair, error) {
	pk, sk, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("walletcrypto: ML-DSA-87 keygen: %w", err)
	}
	pkBytes, err := pk.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("walletcrypto: pk marshal: %w", err)
	}
	skBytes, err := sk.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("walletcrypto: sk marshal: %w", err)
	}
	return &KeyPair{PrivateKey: skBytes, PublicKey: pkBytes}, nil
}

// FromBytes reconstructs a KeyPair from previously-marshalled FIPS
// 204 byte representations (e.g. after decrypting a keystore). Used
// by the browser-side import flow and by future qsdmcli paths that
// load a keystore for signing.
func FromBytes(privateKey, publicKey []byte) (*KeyPair, error) {
	if len(privateKey) == 0 && len(publicKey) == 0 {
		return nil, errors.New("walletcrypto: both private and public key bytes are empty")
	}
	// Validate parseability of the supplied bytes early so callers
	// don't discover a malformed keystore at sign time.
	if len(privateKey) > 0 {
		var sk mldsa87.PrivateKey
		if err := sk.UnmarshalBinary(privateKey); err != nil {
			return nil, fmt.Errorf("walletcrypto: private key parse: %w", err)
		}
	}
	if len(publicKey) > 0 {
		var pk mldsa87.PublicKey
		if err := pk.UnmarshalBinary(publicKey); err != nil {
			return nil, fmt.Errorf("walletcrypto: public key parse: %w", err)
		}
	}
	return &KeyPair{PrivateKey: privateKey, PublicKey: publicKey}, nil
}

// Sign produces a FIPS 204 ML-DSA-87 signature over message under
// the keypair's private key. The "pure" mode (empty context) is
// used and the randomized variant is selected (rand=true) — same
// as pkg/crypto/dilithium_circl.go so signatures produced here
// verify on any QSDM validator without setting a backend flag.
func (kp *KeyPair) Sign(message []byte) ([]byte, error) {
	if kp == nil || len(kp.PrivateKey) == 0 {
		return nil, errors.New("walletcrypto: no private key available for signing")
	}
	var sk mldsa87.PrivateKey
	if err := sk.UnmarshalBinary(kp.PrivateKey); err != nil {
		return nil, fmt.Errorf("walletcrypto: sign: parse private key: %w", err)
	}
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(&sk, message, nil, true /*randomized*/, sig); err != nil {
		return nil, fmt.Errorf("walletcrypto: sign: %w", err)
	}
	return sig, nil
}

// Verify checks signature against message using the keypair's
// public key. Returns (true, nil) for a valid signature; (false,
// nil) for a well-formed but incorrect signature; (false, err)
// only if the public key itself fails to parse — which means the
// KeyPair was constructed badly, not that the signature is
// invalid.
func (kp *KeyPair) Verify(message []byte, signature []byte) (bool, error) {
	if kp == nil || len(kp.PublicKey) == 0 {
		return false, errors.New("walletcrypto: no public key available for verification")
	}
	var pk mldsa87.PublicKey
	if err := pk.UnmarshalBinary(kp.PublicKey); err != nil {
		return false, fmt.Errorf("walletcrypto: verify: parse public key: %w", err)
	}
	return mldsa87.Verify(&pk, message, nil, signature), nil
}
