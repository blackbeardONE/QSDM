package transaction

import "github.com/blackbeardONE/QSDM/pkg/mesh3d"

// WireKindMesh3DV1 is kept for backward compatibility with docs and tests.
const WireKindMesh3DV1 = mesh3d.WireKindMeshPubsubV1

// ParsePhase3Wire decodes a mesh pubsub wire message (delegates to mesh3d.ParseMeshPubsubWire).
func ParsePhase3Wire(msg []byte) (*mesh3d.Transaction, string, error) {
	return mesh3d.ParseMeshPubsubWire(msg)
}

// EncodeMesh3DWire builds JSON for mesh pubsub (delegates to mesh3d.EncodeMeshPubsubWire).
func EncodeMesh3DWire(tx *mesh3d.Transaction, submesh string) ([]byte, error) {
	return mesh3d.EncodeMeshPubsubWire(tx, submesh)
}
