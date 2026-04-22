package wasmer

import (
	"testing"
)

func TestKEMLifecycle(t *testing.T) {
	alg := AlgKyber512
	kem, err := NewKEM(alg)
	if err != nil {
		t.Fatalf("Failed to create KEM: %v", err)
	}
	defer kem.Free()

	pubKey, secKey, err := kem.KeyPair()
	if err != nil {
		t.Fatalf("KeyPair generation failed: %v", err)
	}

	ct, ssEnc, err := kem.Encapsulate(pubKey)
	if err != nil {
		t.Fatalf("Encapsulation failed: %v", err)
	}

	ssDec, err := kem.Decapsulate(ct, secKey)
	if err != nil {
		t.Fatalf("Decapsulation failed: %v", err)
	}

	if len(ssEnc) != len(ssDec) {
		t.Fatalf("Shared secret length mismatch: %d vs %d", len(ssEnc), len(ssDec))
	}

	for i := range ssEnc {
		if ssEnc[i] != ssDec[i] {
			t.Fatalf("Shared secret mismatch at byte %d", i)
		}
	}
}
