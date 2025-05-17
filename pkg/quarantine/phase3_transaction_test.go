package quarantine_test

import (
    "testing"

    "github.com/blackbeardONE/QSDM/pkg/consensus"
    "github.com/blackbeardONE/QSDM/pkg/mesh3d"
    "github.com/blackbeardONE/QSDM/pkg/storage"
    "github.com/blackbeardONE/QSDM/pkg/quarantine"
)

import "github.com/blackbeardONE/QSDM/internal/logging"

func TestHandlePhase3Transaction(t *testing.T) {
    logging.SetupLogger("test_quarantine.log")

    mesh3dValidator := mesh3d.NewMesh3DValidator()
    quarantineManager := quarantine.NewQuarantineManager(0.5)
    reputationManager := quarantine.NewReputationManager(10, 5)
    consensus := consensus.NewProofOfEntanglement()
    if consensus == nil {
        t.Fatalf("Failed to initialize ProofOfEntanglement consensus")
    }
    storage, err := storage.NewStorage("test_transactions.db")
    if err != nil {
        t.Fatalf("Failed to initialize storage: %v", err)
    }
    defer storage.Close()

    txData := []byte("test transaction data")
    tx := &mesh3d.Transaction{
        ID: "tx1",
        ParentCells: []mesh3d.ParentCell{
            {ID: "p1", Data: []byte("parent1")},
            {ID: "p2", Data: []byte("parent2")},
            {ID: "p3", Data: []byte("parent3")},
        },
        Data: txData,
    }

    valid, err := mesh3dValidator.ValidateTransaction(tx)
    if err != nil {
        t.Fatalf("Validation error: %v", err)
    }

    if !valid {
        t.Fatalf("Expected transaction to be valid")
    }

    // Simulate handling transaction
    quarantineManager.RecordTransaction("default-submesh", valid)
    reputationManager.Reward("default-node")

    signature, err := consensus.Sign(tx.Data)
    if err != nil {
        t.Fatalf("Failed to sign transaction: %v", err)
    }
    signatures := [][]byte{signature}

    // Debug print consensus pointer only (cannot access unexported fields)
    t.Logf("consensus: %+v", consensus)

    validConsensus, err := consensus.ValidateTransaction(tx.Data, [][]byte{[]byte("parent1"), []byte("parent2")}, signatures)
    if err != nil {
        t.Fatalf("Consensus validation error: %v", err)
    }
    if !validConsensus {
        t.Fatalf("Expected consensus validation to pass")
    }

    err = storage.StoreTransaction(tx.Data)
    if err != nil {
        t.Fatalf("Failed to store transaction: %v", err)
    }
}
