package mesh3d

import (
    "testing"
)

func TestValidateTransaction(t *testing.T) {
    validator := NewMesh3DValidator()

    tx := &Transaction{
        ID: "tx1",
        ParentCells: []ParentCell{
            {ID: "p1", Data: []byte("parent1")},
            {ID: "p2", Data: []byte("parent2")},
            {ID: "p3", Data: []byte("parent3")},
        },
        Data: []byte("transaction data"),
    }

    valid, err := validator.ValidateTransaction(tx)
    if err != nil {
        t.Fatalf("Validation failed with error: %v", err)
    }
    if !valid {
        t.Errorf("Expected transaction to be valid")
    }

    // Test invalid number of parent cells
    tx.ParentCells = tx.ParentCells[:2]
    valid, err = validator.ValidateTransaction(tx)
    if err == nil {
        t.Errorf("Expected error for invalid number of parent cells")
    }
    if valid {
        t.Errorf("Expected transaction to be invalid")
    }
}
