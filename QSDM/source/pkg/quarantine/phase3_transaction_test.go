package quarantine_test

import (
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/consensus"
	"github.com/blackbeardONE/QSDM/pkg/mesh3d"
	"github.com/blackbeardONE/QSDM/pkg/quarantine"
	"github.com/blackbeardONE/QSDM/pkg/storage"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

func TestHandlePhase3Transaction(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "test_quarantine.log")
	logger := logging.NewLogger(logPath, false)
	// Close the logger before t.TempDir() runs its cleanup —
	// on Windows, unlinkat on a still-open log file blocks
	// removal and fails the test even when the test logic
	// itself passed. Pre-Stage-B this test was Skip()'d on
	// !cgo because ProofOfEntanglement was nil; with Stage B's
	// real PoE backend the test runs in full and exposed the
	// missing teardown.
	t.Cleanup(func() { _ = logger.Close() })

	mesh3dValidator := mesh3d.NewMesh3DValidator()
	quarantineManager := quarantine.NewQuarantineManager(0.5)
	reputationManager := quarantine.NewReputationManager(10, 5)
	poe := consensus.NewProofOfEntanglement()
	if poe == nil {
		t.Skip("ProofOfEntanglement not available")
	}

	st, err := storage.NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer st.Close()

	// A wallet envelope signed by its own key, wrapped as a mesh companion.
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet service: %v", err)
	}
	env := wallet.TransactionData{
		ID:          "phase3_" + strings.Repeat("0", 24) + "1",
		Sender:      ws.GetAddress(),
		Recipient:   strings.Repeat("ab", 32),
		Amount:      1,
		Fee:         0.01,
		ParentCells: []string{"solo-heartbeat-100-1791611407090734188", "solo-heartbeat-101-1791611417090734188"},
		Nonce:       1,
		PublicKey:   hex.EncodeToString(ws.GetPublicKey()),
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}
	canonical, err := env.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	signature, err := ws.SignData(canonical)
	if err != nil {
		t.Fatal(err)
	}
	env.Signature = hex.EncodeToString(signature)
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := mesh3d.CompanionFromWalletJSON(raw)
	if err != nil {
		t.Fatalf("companion: %v", err)
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

	// Consensus validation verifies under the envelope's own key (this
	// node's PoE key plays no part).
	validConsensus, err := poe.ValidateTransaction(consensus.SignedTransaction{
		ID: env.ID, Sender: env.Sender, SigningBytes: canonical, ParentCells: env.ParentCells,
		Signatures: [][]byte{signature}, PublicKey: ws.GetPublicKey(),
	}, logger)
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
