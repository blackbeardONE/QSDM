package mesh3d

import (
	"encoding/json"
	"fmt"
)

// BuildMeshCompanionFromWalletJSON wraps a signed wallet JSON transaction in a mesh pubsub envelope
// so mesh-aware nodes can process the same bytes via the phase-3 path.
//
// The wrapper's parent cells are the envelope's first two signed
// parent_cells plus the payload digest (structure.go); parentLabels must be
// exactly those two parents, so a companion can never claim parents its
// payload's signature does not cover.
func BuildMeshCompanionFromWalletJSON(walletJSON []byte, parentLabels []string, submeshKey string) ([]byte, error) {
	if len(parentLabels) < 2 {
		return nil, fmt.Errorf("need at least 2 parent labels")
	}
	tx, err := CompanionFromWalletJSON(walletJSON)
	if err != nil {
		return nil, err
	}
	if tx.ParentCells[0].ID != parentLabels[0] || tx.ParentCells[1].ID != parentLabels[1] {
		return nil, fmt.Errorf("parent labels must be the envelope's first two signed parent_cells")
	}
	return EncodeMeshPubsubWire(tx, submeshKey)
}

// CompanionFromWalletJSON builds the mesh Transaction that wraps a signed
// wallet JSON envelope: its ID is the envelope ID's first 32 characters and
// its parent cells are the envelope's first two signed parent_cells plus the
// payload digest, each carrying sha256 of its label.
func CompanionFromWalletJSON(walletJSON []byte) (*Transaction, error) {
	if len(walletJSON) == 0 {
		return nil, fmt.Errorf("empty wallet payload")
	}
	var meta struct {
		ID          string   `json:"id"`
		ParentCells []string `json:"parent_cells"`
	}
	if err := json.Unmarshal(walletJSON, &meta); err != nil {
		return nil, fmt.Errorf("wallet json: %w", err)
	}
	if len(meta.ID) < 32 {
		return nil, fmt.Errorf("wallet tx id too short for mesh companion (need >= 32 chars)")
	}
	if len(meta.ParentCells) < 2 {
		return nil, fmt.Errorf("wallet envelope names fewer than 2 parent_cells")
	}
	return &Transaction{
		ID: meta.ID[:32],
		ParentCells: []ParentCell{
			parentCellDataFromLabel(meta.ParentCells[0]),
			parentCellDataFromLabel(meta.ParentCells[1]),
			parentCellDataFromLabel(PayloadDigest(walletJSON)),
		},
		Data: append([]byte(nil), walletJSON...),
	}, nil
}

func parentCellDataFromLabel(label string) ParentCell {
	return ParentCell{ID: label, Data: ParentDataFor(label)}
}
