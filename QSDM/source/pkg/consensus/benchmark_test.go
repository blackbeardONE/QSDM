package consensus

import (
	"strings"
	"testing"
)

func BenchmarkValidateTransaction(b *testing.B) {
	p := NewProofOfEntanglement()
	if p == nil {
		b.Skip("ProofOfEntanglement not available (no ML-DSA-87 backend)")
	}

	txData := []byte("test transaction data for benchmarking")
	signature, err := p.Sign(txData)
	if err != nil {
		b.Fatalf("Failed to sign transaction: %v", err)
	}
	pub := p.MLDSAPublicKey()
	tx := SignedTransaction{
		ID:           "bench-transaction-0001",
		Sender:       walletAddress(pub),
		SigningBytes: txData,
		ParentCells:  []string{strings.Repeat("a", 32), strings.Repeat("b", 32)},
		Signatures:   [][]byte{signature},
		PublicKey:    pub,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := ValidateSignedTransaction(tx); err != nil {
			b.Fatalf("Validation failed: %v", err)
		}
	}
}

func BenchmarkSignTransaction(b *testing.B) {
	p := NewProofOfEntanglement()
	if p == nil {
		b.Skip("ProofOfEntanglement not available (no ML-DSA-87 backend)")
	}

	txData := []byte("test transaction data for benchmarking")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Sign(txData)
		if err != nil {
			b.Fatalf("Signing failed: %v", err)
		}
	}
}
