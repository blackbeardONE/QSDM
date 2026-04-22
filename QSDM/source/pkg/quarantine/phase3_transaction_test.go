package quarantine_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/consensus"
	"github.com/blackbeardONE/QSDM/pkg/mesh3d"
	"github.com/blackbeardONE/QSDM/pkg/quarantine"
	"github.com/blackbeardONE/QSDM/pkg/storage"
)

func parent32(b byte) []byte {
	return bytes.Repeat([]byte{b}, 32)
}

func TestHandlePhase3Transaction(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "test_quarantine.log")
	logger := logging.NewLogger(logPath, false)

	mesh3dValidator := mesh3d.NewMesh3DValidator()
	quarantineManager := quarantine.NewQuarantineManager(0.5)
	reputationManager := quarantine.NewReputationManager(10, 5)
	poe := consensus.NewProofOfEntanglement()
	if poe == nil {
		t.Skip("ProofOfEntanglement not available (CGO disabled)")
	}

	st, err := storage.NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer st.Close()

	txData := bytes.Repeat([]byte("t"), 64)
	tx := &mesh3d.Transaction{
		ID: string(bytes.Repeat([]byte("i"), 32)),
		ParentCells: []mesh3d.ParentCell{
			{ID: "p1", Data: parent32(1)},
			{ID: "p2", Data: parent32(2)},
			{ID: "p3", Data: parent32(3)},
		},
		Data: txData,
	}

	valid, err := mesh3dValidator.ValidateTransaction(tx)
	if err != nil {
		t.Fatalf("mesh3d validation error: %v", err)
	}
	if !valid {
		t.Fatal("expected mesh3d transaction valid")
	}

	quarantineManager.RecordTransaction("default-submesh", valid)
	reputationManager.Reward("default-node")

	signature, err := poe.Sign(tx.Data)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	signatures := [][]byte{signature}

	validConsensus, err := poe.ValidateTransaction(tx.Data, [][]byte{[]byte("parent1"), []byte("parent2")}, signatures, logger)
	if err != nil {
		t.Fatalf("consensus validation error: %v", err)
	}
	if !validConsensus {
		t.Fatal("expected consensus validation to pass")
	}

	if err := st.StoreTransaction(tx.Data); err != nil {
		t.Fatalf("store: %v", err)
	}
}
