package consensus

import (
	"testing"

	"github.com/blackbeardONE/QSDM/internal/logging"
)

func BenchmarkValidateTransaction(b *testing.B) {
	poe := NewProofOfEntanglement()
	if poe == nil {
		b.Skip("ProofOfEntanglement not available (CGO disabled)")
	}

	logger := logging.NewLogger("test.log", false)
	txData := []byte("test transaction data for benchmarking")
	parentCells := [][]byte{[]byte("parent1"), []byte("parent2")}
	
	// Sign the transaction
	signature, err := poe.Sign(txData)
	if err != nil {
		b.Fatalf("Failed to sign transaction: %v", err)
	}
	signatures := [][]byte{signature}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := poe.ValidateTransaction(txData, parentCells, signatures, logger)
		if err != nil {
			b.Fatalf("Validation failed: %v", err)
		}
	}
}

func BenchmarkSignTransaction(b *testing.B) {
	poe := NewProofOfEntanglement()
	if poe == nil {
		b.Skip("ProofOfEntanglement not available (CGO disabled)")
	}

	txData := []byte("test transaction data for benchmarking")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := poe.Sign(txData)
		if err != nil {
			b.Fatalf("Signing failed: %v", err)
		}
	}
}

