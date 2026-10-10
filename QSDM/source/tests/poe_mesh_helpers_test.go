package tests

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/consensus"
	"github.com/blackbeardONE/QSDM/pkg/mesh3d"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

// signedWalletEnvelope returns a wallet envelope signed by a fresh ML-DSA-87
// key, naming two well-formed parents, and the wallet that signed it.
func signedWalletEnvelope(tb testing.TB) ([]byte, *wallet.WalletService) {
	tb.Helper()
	ws, err := wallet.NewWalletService()
	if err != nil {
		tb.Skipf("wallet service unavailable: %v", err)
	}
	env := wallet.TransactionData{
		ID:          "mesh_test_" + strings.Repeat("0", 22) + "1",
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
		tb.Fatal(err)
	}
	sig, err := ws.SignData(canonical)
	if err != nil {
		tb.Fatal(err)
	}
	env.Signature = hex.EncodeToString(sig)
	raw, err := json.Marshal(env)
	if err != nil {
		tb.Fatal(err)
	}
	return raw, ws
}

// signedMeshCompanion returns the mesh transaction wrapping a signed wallet
// envelope: the only shape the mesh validator accepts now that its
// entanglement-structure and signature checks are fatal.
func signedMeshCompanion(tb testing.TB) *mesh3d.Transaction {
	tb.Helper()
	raw, _ := signedWalletEnvelope(tb)
	tx, err := mesh3d.CompanionFromWalletJSON(raw)
	if err != nil {
		tb.Fatalf("mesh companion: %v", err)
	}
	return tx
}

// signedPoETransaction returns a consensus.SignedTransaction for a wallet
// envelope signed by its own key, as the PoE helper validates it.
func signedPoETransaction(tb testing.TB) consensus.SignedTransaction {
	tb.Helper()
	raw, ws := signedWalletEnvelope(tb)
	var env wallet.TransactionData
	if err := json.Unmarshal(raw, &env); err != nil {
		tb.Fatal(err)
	}
	canonical, err := env.CanonicalBytes()
	if err != nil {
		tb.Fatal(err)
	}
	sig, err := hex.DecodeString(env.Signature)
	if err != nil {
		tb.Fatal(err)
	}
	return consensus.SignedTransaction{
		ID:           env.ID,
		Sender:       env.Sender,
		SigningBytes: canonical,
		ParentCells:  env.ParentCells,
		Signatures:   [][]byte{sig},
		PublicKey:    ws.GetPublicKey(),
	}
}
