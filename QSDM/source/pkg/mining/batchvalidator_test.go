package mining

import (
	"crypto/sha256"
	"strings"
	"testing"
)

func cellRef(id string) ParentCellRef {
	return ParentCellRef{ID: []byte(id), ContentHash: sha256.Sum256([]byte("content:" + id))}
}

// okBatch builds a canonical, well-formed 3-cell batch.
func okBatch() Batch {
	b := Batch{Cells: []ParentCellRef{cellRef("a"), cellRef("b"), cellRef("c")}}
	b.Canonicalize()
	return b
}

func TestStructuralBatchValidator_AcceptsCanonicalBatch(t *testing.T) {
	v := NewStructuralBatchValidator()
	if err := v.ValidateBatch(okBatch()); err != nil {
		t.Fatalf("canonical batch should validate: %v", err)
	}
}

func TestStructuralBatchValidator_RejectsSizeWindow(t *testing.T) {
	v := NewStructuralBatchValidator()
	// 2 cells — below MinBatchSize.
	b := Batch{Cells: []ParentCellRef{cellRef("a"), cellRef("b")}}
	if err := v.ValidateBatch(b); err == nil {
		t.Fatal("batch below the 3-cell minimum must be rejected")
	}
}

func TestStructuralBatchValidator_RejectsNonCanonicalOrder(t *testing.T) {
	v := NewStructuralBatchValidator()
	// Deliberately out of ascending ID order.
	b := Batch{Cells: []ParentCellRef{cellRef("c"), cellRef("b"), cellRef("a")}}
	err := v.ValidateBatch(b)
	if err == nil {
		t.Fatal("non-canonically-ordered batch must be rejected")
	}
	if !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("expected a canonical-ordering error, got %v", err)
	}
}

func TestStructuralBatchValidator_RejectsDuplicateCells(t *testing.T) {
	v := NewStructuralBatchValidator()
	// Padding the size window with a repeated cell.
	b := Batch{Cells: []ParentCellRef{cellRef("a"), cellRef("a"), cellRef("b")}}
	b.Canonicalize()
	err := v.ValidateBatch(b)
	if err == nil {
		t.Fatal("batch padded with a duplicate cell must be rejected")
	}
	if !strings.Contains(err.Error(), "same ID") {
		t.Fatalf("expected a duplicate-ID error, got %v", err)
	}
}

func TestStructuralBatchValidator_RejectsZeroContentHash(t *testing.T) {
	v := NewStructuralBatchValidator()
	b := okBatch()
	b.Cells[1].ContentHash = [32]byte{}
	if err := v.ValidateBatch(b); err == nil {
		t.Fatal("a cell with no content commitment must be rejected")
	}
}

type fakeLookup map[string][32]byte

func (f fakeLookup) ParentCellContentHash(id []byte) ([32]byte, bool) {
	h, ok := f[string(id)]
	return h, ok
}

func TestStructuralBatchValidator_LookupDetectsForgedContentHash(t *testing.T) {
	b := okBatch()
	lookup := fakeLookup{}
	for _, c := range b.Cells {
		lookup[string(c.ID)] = c.ContentHash
	}
	v := NewStructuralBatchValidator().WithCellLookup(lookup)

	if err := v.ValidateBatch(b); err != nil {
		t.Fatalf("batch matching the mesh should validate: %v", err)
	}

	// Miner forges a content hash for one cell.
	forged := okBatch()
	forged.Cells[2].ContentHash = sha256.Sum256([]byte("forged"))
	err := v.ValidateBatch(forged)
	if err == nil {
		t.Fatal("a forged content hash must be rejected when a mesh lookup is configured")
	}
	if !strings.Contains(err.Error(), "does not match the mesh") {
		t.Fatalf("expected a mesh-mismatch error, got %v", err)
	}
}

func TestStructuralBatchValidator_LookupRejectsUnknownCell(t *testing.T) {
	v := NewStructuralBatchValidator().WithCellLookup(fakeLookup{})
	if err := v.ValidateBatch(okBatch()); err == nil {
		t.Fatal("cells absent from the mesh must be rejected when a lookup is configured")
	}
}

// Without a lookup the validator must not invent semantic judgements — an
// otherwise well-formed batch passes even though no mesh view exists.
func TestStructuralBatchValidator_NoLookupSkipsMeshChecks(t *testing.T) {
	v := NewStructuralBatchValidator()
	if err := v.ValidateBatch(okBatch()); err != nil {
		t.Fatalf("encoding-only validation should accept a well-formed batch: %v", err)
	}
}
