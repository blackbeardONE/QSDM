package consensus

import (
	"github.com/blackbeardONE/QSDM/internal/logging"
	"testing"
)

func TestValidateTransaction(t *testing.T) {
	logger := logging.NewLogger("test_poe.log", false)
	poe := NewProofOfEntanglement()
	if poe == nil {
		t.Skip("ProofOfEntanglement requires CGO and liboqs")
	}

	// Prepare test data
	txData := []byte("test transaction")
	parentCells := [][]byte{[]byte("parent1"), []byte("parent2")}

	// Sign the transaction
	signature, err := poe.Sign(txData)
	if err != nil {
		t.Fatalf("Failed to sign transaction: %v", err)
	}

	signatures := [][]byte{signature}

	// Call ValidateTransaction with logger
	valid, err := poe.ValidateTransaction(txData, parentCells, signatures, logger)
	if err != nil {
		t.Errorf("ValidateTransaction failed: %v", err)
	}
	if !valid {
		t.Error("ValidateTransaction returned false for valid transaction")
	}
}
