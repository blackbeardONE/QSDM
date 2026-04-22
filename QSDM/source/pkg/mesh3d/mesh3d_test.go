package mesh3d

import (
	"bytes"
	"testing"
)

func parentData(n byte) []byte {
	return bytes.Repeat([]byte{n}, 32)
}

func TestValidateTransaction(t *testing.T) {
	validator := NewMesh3DValidator()

	txID := bytes.Repeat([]byte("x"), 32)
	txData := bytes.Repeat([]byte("d"), 64)

	tx := &Transaction{
		ID: string(txID),
		ParentCells: []ParentCell{
			{ID: "p1", Data: parentData(1)},
			{ID: "p2", Data: parentData(2)},
			{ID: "p3", Data: parentData(3)},
		},
		Data: txData,
	}

	valid, err := validator.ValidateTransaction(tx)
	if err != nil {
		t.Fatalf("Validation failed with error: %v", err)
	}
	if !valid {
		t.Fatal("expected transaction to be valid")
	}

	// Test invalid number of parent cells
	tx.ParentCells = tx.ParentCells[:2]
	valid, err = validator.ValidateTransaction(tx)
	if err == nil {
		t.Fatal("expected error for invalid number of parent cells")
	}
	if valid {
		t.Fatal("expected transaction to be invalid")
	}
}
