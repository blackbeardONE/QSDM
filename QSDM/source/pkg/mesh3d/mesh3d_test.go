package mesh3d

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/poe"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

var (
	meshParentA = "solo-heartbeat-100-1791611407090734188"
	meshParentB = "solo-heartbeat-101-1791611417090734188"
)

// signedEnvelope returns a wallet envelope signed by a fresh ML-DSA-87 key.
func signedEnvelope(t *testing.T, mutate func(*wallet.TransactionData)) []byte {
	t.Helper()
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Fatalf("NewWalletService: %v", err)
	}
	env := wallet.TransactionData{
		ID:          "hive_wallet_1791611407090_00112233aabbccdd",
		Sender:      ws.GetAddress(),
		Recipient:   strings.Repeat("ab", 32),
		Amount:      2,
		Fee:         0.01,
		ParentCells: []string{meshParentA, meshParentB},
		Nonce:       1,
		PublicKey:   hex.EncodeToString(ws.GetPublicKey()),
		Timestamp:   time.Unix(1_790_000_000, 0).UTC().Format(time.RFC3339),
	}
	if mutate != nil {
		mutate(&env)
	}
	canonical, err := env.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := ws.SignData(canonical)
	if err != nil {
		t.Fatal(err)
	}
	env.Signature = hex.EncodeToString(sig)
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// companion builds and parses a mesh companion for walletJSON.
func companion(t *testing.T, walletJSON []byte) *Transaction {
	t.Helper()
	var meta struct {
		ParentCells []string `json:"parent_cells"`
	}
	if err := json.Unmarshal(walletJSON, &meta); err != nil {
		t.Fatal(err)
	}
	wire, err := BuildMeshCompanionFromWalletJSON(walletJSON, meta.ParentCells, "sm1")
	if err != nil {
		t.Fatalf("build companion: %v", err)
	}
	tx, _, err := ParseMeshPubsubWire(wire)
	if err != nil {
		t.Fatalf("parse companion: %v", err)
	}
	return tx
}

func mustReject(t *testing.T, tx *Transaction) error {
	t.Helper()
	valid, err := NewMesh3DValidator().ValidateTransaction(tx)
	if err == nil || valid {
		t.Fatalf("invalid mesh transaction accepted (valid=%v)", valid)
	}
	if !errors.Is(err, ErrStructure) {
		t.Fatalf("error does not wrap ErrStructure: %v", err)
	}
	return err
}

func TestValidateTransaction_AcceptsSignedCompanion(t *testing.T) {
	tx := companion(t, signedEnvelope(t, nil))
	valid, err := NewMesh3DValidator().ValidateTransaction(tx)
	if err != nil || !valid {
		t.Fatalf("valid companion rejected: valid=%v err=%v", valid, err)
	}
}

// Structure failures used to be warnings; every one is now fatal.
func TestValidateTransaction_StructureFailuresAreFatal(t *testing.T) {
	raw := signedEnvelope(t, nil)
	cases := map[string]func(tx *Transaction){
		"two-parent-cells": func(tx *Transaction) { tx.ParentCells = tx.ParentCells[:2] },
		"parent-data-not-label-hash": func(tx *Transaction) {
			tx.ParentCells[0].Data = make([]byte, 32)
		},
		"duplicate-parent-cell": func(tx *Transaction) {
			tx.ParentCells[1] = tx.ParentCells[0]
		},
		"last-parent-not-payload-digest": func(tx *Transaction) {
			tx.ParentCells[2] = parentCellDataFromLabel(strings.Repeat("f", 64))
		},
		"mesh-parent-not-signed-parent": func(tx *Transaction) {
			tx.ParentCells[0] = parentCellDataFromLabel("solo-heartbeat-999-1791611407090734188")
		},
		"mesh-id-not-envelope-prefix": func(tx *Transaction) { tx.ID = strings.Repeat("z", 32) },
		"short-mesh-id":               func(tx *Transaction) { tx.ID = "hive" },
		"empty-payload":               func(tx *Transaction) { tx.Data = nil },
		"payload-not-envelope": func(tx *Transaction) {
			tx.Data = []byte(`{"kind":"other"}`)
			tx.ParentCells[2] = parentCellDataFromLabel(PayloadDigest(tx.Data))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			tx := companion(t, raw)
			mutate(tx)
			mustReject(t, tx)
		})
	}
}

// The payload signature is verified under the payload's own public key with
// the sender bound to it (it used to be a placeholder).
func TestValidateTransaction_PayloadSignatureIsBound(t *testing.T) {
	t.Run("tampered-amount", func(t *testing.T) {
		raw := signedEnvelope(t, nil)
		var env wallet.TransactionData
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		env.Amount = 2000
		tampered, _ := json.Marshal(env)
		mustReject(t, companion(t, tampered))
	})
	t.Run("sender-not-key-address", func(t *testing.T) {
		raw := signedEnvelope(t, func(env *wallet.TransactionData) {
			sum := sha256.Sum256([]byte("someone else"))
			env.Sender = hex.EncodeToString(sum[:])
		})
		mustReject(t, companion(t, raw))
	})
	t.Run("signed-by-other-key", func(t *testing.T) {
		raw := signedEnvelope(t, nil)
		other := signedEnvelope(t, nil)
		var env, otherEnv wallet.TransactionData
		_ = json.Unmarshal(raw, &env)
		_ = json.Unmarshal(other, &otherEnv)
		env.Signature = otherEnv.Signature
		swapped, _ := json.Marshal(env)
		mustReject(t, companion(t, swapped))
	})
}

func TestValidateTransaction_EnvelopeParentRules(t *testing.T) {
	// A companion needs two labels, so a one-parent envelope cannot even be
	// wrapped; build its wire by hand and expect the PoE shape error.
	raw := signedEnvelope(t, func(env *wallet.TransactionData) {
		env.ParentCells = []string{meshParentA, meshParentA}
	})
	tx := &Transaction{
		ID: "hive_wallet_1791611407090_001122",
		ParentCells: []ParentCell{
			parentCellDataFromLabel(meshParentA),
			parentCellDataFromLabel(meshParentB),
			parentCellDataFromLabel(PayloadDigest(raw)),
		},
		Data: raw,
	}
	err := mustReject(t, tx)
	if !errors.Is(err, poe.ErrDuplicateParent) {
		t.Fatalf("err=%v, want poe.ErrDuplicateParent", err)
	}
}
