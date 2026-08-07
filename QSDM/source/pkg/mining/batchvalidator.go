package mining

import (
	"bytes"
	"fmt"
)

// StructuralBatchValidator is the reference BatchValidator used for the
// step-11 spot check (MINING_PROTOCOL.md §8.3).
//
// Why this and not pkg/mesh3d: the spot check's job is to decide whether a
// batch is *well-formed under the mining protocol's own canonical encoding*.
// Every rule below is specified in MINING_PROTOCOL.md §3.2 / §4.3 and is
// independently checkable from the batch bytes alone, so a failure is
// unambiguous evidence of a malformed or forged batch rather than a
// heuristic. Deeper semantic validation (does this parent cell exist in the
// mesh, does its content hash match the stored cell) belongs behind a
// ChainView-style lookup and is layered on via WithCellLookup.
//
// The zero value is ready to use and performs the encoding checks only.
type StructuralBatchValidator struct {
	// lookup, when non-nil, resolves a parent-cell ID to its authoritative
	// 32-byte content hash. A batch claiming a content hash that disagrees
	// with the mesh is fraud. Absent a lookup the validator restricts
	// itself to encoding invariants and does not guess.
	lookup CellContentHashLookup
}

// CellContentHashLookup resolves a parent-cell ID to the content hash the
// mesh considers authoritative. Implementations return ok=false for unknown
// cells; the validator treats unknown cells as a validation failure only
// when a lookup is configured, because absent a mesh view an unknown cell is
// indistinguishable from a cell this node has not synced yet.
type CellContentHashLookup interface {
	ParentCellContentHash(id []byte) (hash [32]byte, ok bool)
}

// NewStructuralBatchValidator returns a validator that enforces the
// canonical-encoding rules only.
func NewStructuralBatchValidator() *StructuralBatchValidator {
	return &StructuralBatchValidator{}
}

// WithCellLookup returns a copy of v that additionally verifies each cell's
// ContentHash against the mesh's authoritative value.
func (v *StructuralBatchValidator) WithCellLookup(l CellContentHashLookup) *StructuralBatchValidator {
	return &StructuralBatchValidator{lookup: l}
}

var zeroContentHash [32]byte

// ValidateBatch implements BatchValidator.
//
// Rules, in order of cost:
//
//  1. §3.2 size window and non-empty IDs (delegated to Batch.Validate).
//  2. §3.2 canonical ordering: cells MUST be sorted ascending by ID. An
//     unordered batch hashes to a value no honest validator computes, so
//     submitting one is either a forged batch or a broken miner.
//  3. No duplicate cell IDs — padding a batch with a repeated cell is the
//     cheapest way to fake the 3–5 size window.
//  4. Non-zero ContentHash — an all-zero hash means the cell content was
//     never hashed, so the batch carries no commitment to its contents.
//  5. When a cell lookup is configured, the claimed ContentHash MUST equal
//     the mesh's value for that cell ID.
func (v *StructuralBatchValidator) ValidateBatch(batch Batch) error {
	if err := batch.Validate(); err != nil {
		return err
	}

	for i := 1; i < len(batch.Cells); i++ {
		prev, cur := batch.Cells[i-1].ID, batch.Cells[i].ID
		if cmp := bytes.Compare(prev, cur); cmp > 0 {
			return fmt.Errorf(
				"mining: batch cells %d and %d are not in canonical ascending ID order", i-1, i)
		} else if cmp == 0 {
			return fmt.Errorf("mining: batch cells %d and %d share the same ID", i-1, i)
		}
	}

	for i, c := range batch.Cells {
		if c.ContentHash == zeroContentHash {
			return fmt.Errorf("mining: batch cell %d has a zero content hash", i)
		}
	}

	if v.lookup != nil {
		for i, c := range batch.Cells {
			want, ok := v.lookup.ParentCellContentHash(c.ID)
			if !ok {
				return fmt.Errorf("mining: batch cell %d references unknown parent cell", i)
			}
			if want != c.ContentHash {
				return fmt.Errorf(
					"mining: batch cell %d content hash does not match the mesh", i)
			}
		}
	}

	return nil
}

// Compile-time guard that the reference validator satisfies the interface
// the verifier's step-11 spot check consumes.
var _ BatchValidator = (*StructuralBatchValidator)(nil)
