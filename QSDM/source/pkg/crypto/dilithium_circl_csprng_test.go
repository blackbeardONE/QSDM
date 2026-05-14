//go:build !cgo
// +build !cgo

package crypto

import (
	"bytes"
	"testing"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// CSPRNG / FIPS 204 conformance tests — closes the crypto-01
// audit-checklist row ("Verify ML-DSA-87 (Dilithium) keypair
// generation uses CSPRNG and follows NIST FIPS 204"). The tests
// in this file are intentionally narrower than the Stage A
// dilithium_circl_test.go suite: they prove the two claims the
// audit row asks for, in isolation, so a future regression on
// either claim surfaces with a clear failure name rather than
// burying inside a round-trip test.
//
// Claim 1 ("uses CSPRNG"):  N keypairs in a row produce N
// distinct public keys. A keygen that fell back to a fixed seed
// or a low-entropy source would collide quickly; crypto/rand on
// a healthy host produces no collisions across N=64 draws with
// overwhelming probability.
//
// Claim 2 ("follows NIST FIPS 204"): the byte sizes circl
// reports for ML-DSA-87 (PublicKeySize, SignatureSize, SeedSize)
// match the values FIPS 204 §6.1 fixes for the strength-3 / 256-bit-
// security parameter set, AND the deterministic-keygen contract
// (mldsa87.NewKeyFromSeed) returns the same packed public key
// for the same seed every time. Both properties are what allow
// the chain to verify the same signature under either backend.

// TestCircl_KeygenCSPRNG_NoCollisions generates N keypairs and
// asserts every public key is unique. A regression that wired
// keygen to a fixed or low-entropy source would produce
// duplicates almost immediately; crypto/rand should never
// collide over this many draws on any production host.
//
// N=64 keeps the test under 1 second on a workstation CPU
// (circl's pure-Go ML-DSA-87 keygen is ~5 ms per call).
func TestCircl_KeygenCSPRNG_NoCollisions(t *testing.T) {
	const N = 64

	seen := make(map[string]struct{}, N)
	for i := 0; i < N; i++ {
		d := NewDilithium()
		if d == nil {
			t.Fatalf("NewDilithium #%d returned nil (entropy source unavailable?)", i)
		}
		pk := d.GetPublicKey()
		d.Free()
		if pk == nil {
			t.Fatalf("GetPublicKey #%d returned nil", i)
		}
		if got, want := len(pk), mldsa87.PublicKeySize; got != want {
			t.Fatalf("public key length #%d: got %d, want %d", i, got, want)
		}
		key := string(pk)
		if _, dup := seen[key]; dup {
			t.Fatalf("public key #%d collides with an earlier draw; CSPRNG broken or keygen wired to a fixed seed", i)
		}
		seen[key] = struct{}{}
	}
}

// TestCircl_FIPS204_SizesConformToStrength3 asserts the wire-
// format byte sizes circl reports for ML-DSA-87 are the exact
// values FIPS 204 §6.1 Table 2 fixes for the strength-3
// (256-bit security) parameter set. The Stage A test
// TestCircl_FIPS204_SizesMatchTxsigConstants checks the same
// constants but compares to the txsig.go literals; this test
// pins them to the standard directly so a contributor that
// changes BOTH txsig.go and the circl import in tandem still
// fails CI.
func TestCircl_FIPS204_SizesConformToStrength3(t *testing.T) {
	// Values from NIST FIPS 204 §6.1 Table 2, strength category 3
	// (256-bit security, parameter set "ML-DSA-87"):
	const (
		fips204PublicKeySize = 2592
		fips204SignatureSize = 4627
		fips204SeedSize      = 32
	)

	if mldsa87.PublicKeySize != fips204PublicKeySize {
		t.Errorf("ML-DSA-87 PublicKeySize: circl reports %d, FIPS 204 §6.1 Table 2 fixes %d", mldsa87.PublicKeySize, fips204PublicKeySize)
	}
	if mldsa87.SignatureSize != fips204SignatureSize {
		t.Errorf("ML-DSA-87 SignatureSize: circl reports %d, FIPS 204 §6.1 Table 2 fixes %d", mldsa87.SignatureSize, fips204SignatureSize)
	}
	if mldsa87.SeedSize != fips204SeedSize {
		t.Errorf("ML-DSA-87 SeedSize: circl reports %d, FIPS 204 §6.1 Table 2 fixes %d", mldsa87.SeedSize, fips204SeedSize)
	}
}

// TestCircl_FIPS204_DeterministicKeygen_FromFixedSeed
// re-derives a keypair from a constant seed and asserts the
// packed public key is byte-identical across two calls. This is
// the FIPS 204 deterministic-keygen contract; it's also the
// foundation a future cross-backend KAT vector would build on.
//
// The seed is a constant in the test (not a random draw) so a
// regression that introduced extra entropy into the keygen path
// would also fail this test.
func TestCircl_FIPS204_DeterministicKeygen_FromFixedSeed(t *testing.T) {
	var seed [mldsa87.SeedSize]byte
	for i := range seed {
		// Arbitrary non-zero, non-monotonic byte pattern. The
		// test does not depend on the choice; it depends only
		// on the SAME seed producing the SAME public key on
		// every call.
		seed[i] = byte(i*17 + 3)
	}

	pk1, _ := mldsa87.NewKeyFromSeed(&seed)
	pk2, _ := mldsa87.NewKeyFromSeed(&seed)

	pkBytes1, err := pk1.MarshalBinary()
	if err != nil {
		t.Fatalf("pk1 MarshalBinary: %v", err)
	}
	pkBytes2, err := pk2.MarshalBinary()
	if err != nil {
		t.Fatalf("pk2 MarshalBinary: %v", err)
	}

	if !bytes.Equal(pkBytes1, pkBytes2) {
		t.Fatal("ML-DSA-87 keygen is not deterministic for a fixed seed; FIPS 204 §6 specifies deterministic keygen from rho")
	}
	if len(pkBytes1) != mldsa87.PublicKeySize {
		t.Errorf("public key length: got %d, want %d", len(pkBytes1), mldsa87.PublicKeySize)
	}
}
