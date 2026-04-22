package mesh3d

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// WireKindMeshPubsubV1 is the JSON envelope kind for mesh payloads on the shared pubsub topic.
const WireKindMeshPubsubV1 = "qsdm_mesh3d_v1"

// MeshPubsubParentWire is one parent cell in a mesh pubsub JSON envelope.
type MeshPubsubParentWire struct {
	ID      string `json:"id"`
	DataB64 string `json:"data_b64"`
}

// MeshPubsubWireMsg is a portable JSON representation of a Transaction for P2P gossip.
type MeshPubsubWireMsg struct {
	Kind        string                 `json:"kind"`
	ID          string                 `json:"id"`
	ParentCells []MeshPubsubParentWire `json:"parent_cells"`
	PayloadB64  string                 `json:"payload_b64"`
	Submesh     string                 `json:"submesh,omitempty"`
}

// ErrNotMeshPubsubWire is returned when JSON is valid but not a mesh pubsub envelope.
var ErrNotMeshPubsubWire = errors.New("not a mesh pubsub wire message")

// ParseMeshPubsubWire decodes a mesh pubsub JSON message.
func ParseMeshPubsubWire(msg []byte) (*Transaction, string, error) {
	var w MeshPubsubWireMsg
	if err := json.Unmarshal(msg, &w); err != nil {
		return nil, "", err
	}
	if w.Kind != WireKindMeshPubsubV1 {
		return nil, "", ErrNotMeshPubsubWire
	}
	if w.ID == "" {
		return nil, "", errors.New("mesh pubsub wire: id required")
	}
	if len(w.ParentCells) < 3 || len(w.ParentCells) > 5 {
		return nil, "", fmt.Errorf("mesh pubsub wire: want 3-5 parent_cells, got %d", len(w.ParentCells))
	}
	payload, err := base64.StdEncoding.DecodeString(w.PayloadB64)
	if err != nil {
		return nil, "", fmt.Errorf("mesh pubsub wire: payload_b64: %w", err)
	}
	parents := make([]ParentCell, 0, len(w.ParentCells))
	for i, p := range w.ParentCells {
		if p.ID == "" {
			return nil, "", fmt.Errorf("mesh pubsub wire: parent %d missing id", i)
		}
		raw, err := base64.StdEncoding.DecodeString(p.DataB64)
		if err != nil {
			return nil, "", fmt.Errorf("mesh pubsub wire: parent %d data_b64: %w", i, err)
		}
		parents = append(parents, ParentCell{ID: p.ID, Data: raw})
	}
	tx := &Transaction{
		ID:          w.ID,
		ParentCells: parents,
		Data:        payload,
	}
	sub := w.Submesh
	if sub == "" {
		sub = "default-submesh"
	}
	return tx, sub, nil
}

// EncodeMeshPubsubWire builds JSON for ParseMeshPubsubWire.
func EncodeMeshPubsubWire(tx *Transaction, submesh string) ([]byte, error) {
	if tx == nil {
		return nil, errors.New("nil transaction")
	}
	if len(tx.ParentCells) < 3 || len(tx.ParentCells) > 5 {
		return nil, fmt.Errorf("want 3-5 parent cells, got %d", len(tx.ParentCells))
	}
	w := MeshPubsubWireMsg{Kind: WireKindMeshPubsubV1, ID: tx.ID, Submesh: submesh}
	for _, p := range tx.ParentCells {
		w.ParentCells = append(w.ParentCells, MeshPubsubParentWire{
			ID: p.ID, DataB64: base64.StdEncoding.EncodeToString(p.Data),
		})
	}
	w.PayloadB64 = base64.StdEncoding.EncodeToString(tx.Data)
	return json.Marshal(w)
}
