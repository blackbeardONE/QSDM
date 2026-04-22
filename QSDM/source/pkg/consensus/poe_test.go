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

// TestValidateTransaction_AcceptsMesh3DParentCount guards against the historic
// regression where ValidateTransaction required exactly 2 parent cells and
// silently rejected every Phase-3 / mesh3D payload (those carry 3 parent
// cells by protocol, see pkg/mesh3d). Both 2-parent (wallet) and 3-parent
// (mesh3D) shapes must pass for the same signed `tx`.
func TestValidateTransaction_AcceptsMesh3DParentCount(t *testing.T) {
	logger := logging.NewLogger("test_poe_mesh3d.log", false)
	poe := NewProofOfEntanglement()
	if poe == nil {
		t.Skip("ProofOfEntanglement requires CGO and liboqs")
	}

	txData := []byte("phase3 mesh3d transaction")
	signature, err := poe.Sign(txData)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	signatures := [][]byte{signature}

	cases := []struct {
		name        string
		parentCells [][]byte
		wantValid   bool
	}{
		{
			name:        "wallet_path_two_parents",
			parentCells: [][]byte{[]byte("p1"), []byte("p2")},
			wantValid:   true,
		},
		{
			name:        "mesh3d_phase3_three_parents",
			parentCells: [][]byte{[]byte("p1"), []byte("p2"), []byte("p3")},
			wantValid:   true,
		},
		{
			name:        "zero_parents_is_rejected",
			parentCells: nil,
			wantValid:   false,
		},
		{
			name:        "one_parent_is_rejected",
			parentCells: [][]byte{[]byte("p1")},
			wantValid:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			valid, err := poe.ValidateTransaction(txData, tc.parentCells, signatures, logger)
			if tc.wantValid {
				if err != nil || !valid {
					t.Fatalf("expected valid=true, got valid=%v err=%v", valid, err)
				}
				return
			}
			if err == nil || valid {
				t.Fatalf("expected rejection, got valid=%v err=%v", valid, err)
			}
		})
	}
}
