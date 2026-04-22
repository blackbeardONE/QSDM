package mesh3d

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// BuildMeshCompanionFromWalletJSON wraps a signed wallet JSON transaction in a mesh pubsub envelope
// so mesh-aware nodes can process the same bytes via the phase-3 path.
func BuildMeshCompanionFromWalletJSON(walletJSON []byte, parentLabels []string, submeshKey string) ([]byte, error) {
	if len(walletJSON) == 0 {
		return nil, fmt.Errorf("empty wallet payload")
	}
	if len(parentLabels) < 2 {
		return nil, fmt.Errorf("need at least 2 parent labels")
	}
	var meta struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(walletJSON, &meta); err != nil {
		return nil, fmt.Errorf("wallet json: %w", err)
	}
	id := meta.ID
	if len(id) < 32 {
		return nil, fmt.Errorf("wallet tx id too short for mesh companion (need >= 32 chars)")
	}
	digest := sha256.Sum256(walletJSON)
	thirdID := hex.EncodeToString(digest[:]) // 64 hex chars
	parents := []ParentCell{
		parentCellDataFromLabel(parentLabels[0]),
		parentCellDataFromLabel(parentLabels[1]),
		parentCellDataFromLabel(thirdID),
	}
	tx := &Transaction{
		ID:          id[:32],
		ParentCells: parents,
		Data:        append([]byte(nil), walletJSON...),
	}
	return EncodeMeshPubsubWire(tx, submeshKey)
}

func parentCellDataFromLabel(label string) ParentCell {
	sum := sha256.Sum256([]byte(label))
	return ParentCell{ID: label, Data: sum[:]}
}
