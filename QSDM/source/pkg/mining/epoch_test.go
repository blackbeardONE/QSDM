package mining

import (
	"bytes"
	"testing"
)

func TestEpochParamsRoundTrip(t *testing.T) {
	p := NewEpochParams()
	if err := p.Validate(); err != nil {
		t.Fatalf("default params invalid: %v", err)
	}
	if p.BlocksPerEpoch != DefaultBlocksPerMiningEpoch {
		t.Fatalf("default BlocksPerEpoch = %d want %d", p.BlocksPerEpoch, DefaultBlocksPerMiningEpoch)
	}
	if p.EpochForHeight(0) != 0 {
		t.Fatalf("epoch for height 0: got %d want 0", p.EpochForHeight(0))
	}
	if got := p.EpochForHeight(p.BlocksPerEpoch); got != 1 {
		t.Fatalf("epoch for first block of epoch 1: got %d want 1", got)
	}
	if got := p.EpochStartHeight(3); got != 3*p.BlocksPerEpoch {
		t.Fatalf("epoch 3 start: got %d want %d", got, 3*p.BlocksPerEpoch)
	}
	if got := p.EpochEndHeight(3); got != 4*p.BlocksPerEpoch-1 {
		t.Fatalf("epoch 3 end: got %d want %d", got, 4*p.BlocksPerEpoch-1)
	}
}

func TestEpochParamsInvalid(t *testing.T) {
	var p EpochParams
	if err := p.Validate(); err == nil {
		t.Fatalf("zero-value params should be invalid")
	}
}

func TestBatchCanonicalization(t *testing.T) {
	b := Batch{Cells: []ParentCellRef{
		{ID: []byte{0x03}, ContentHash: [32]byte{3}},
		{ID: []byte{0x01}, ContentHash: [32]byte{1}},
		{ID: []byte{0x02}, ContentHash: [32]byte{2}},
	}}
	b.Canonicalize()
	if !bytes.Equal(b.Cells[0].ID, []byte{0x01}) ||
		!bytes.Equal(b.Cells[1].ID, []byte{0x02}) ||
		!bytes.Equal(b.Cells[2].ID, []byte{0x03}) {
		t.Fatalf("canonicalize did not sort: %+v", b.Cells)
	}
	if err := b.Validate(); err != nil {
		t.Fatalf("valid 3-cell batch rejected: %v", err)
	}
}

func TestBatchTooSmall(t *testing.T) {
	b := Batch{Cells: []ParentCellRef{
		{ID: []byte{0x01}}, {ID: []byte{0x02}},
	}}
	if err := b.Validate(); err == nil {
		t.Fatalf("2-cell batch must fail validation (min is %d)", MinBatchSize)
	}
}

func TestBatchHashDeterministic(t *testing.T) {
	cells := []ParentCellRef{
		{ID: []byte("a"), ContentHash: [32]byte{0xaa}},
		{ID: []byte("b"), ContentHash: [32]byte{0xbb}},
		{ID: []byte("c"), ContentHash: [32]byte{0xcc}},
	}
	b1 := Batch{Cells: append([]ParentCellRef(nil), cells...)}
	b2 := Batch{Cells: append([]ParentCellRef(nil), cells...)}
	if BatchHash(b1) != BatchHash(b2) {
		t.Fatalf("identical batches produced different hashes")
	}
	b3 := Batch{Cells: []ParentCellRef{cells[2], cells[0], cells[1]}}
	if BatchHash(b3) == BatchHash(b1) {
		t.Fatalf("reordered un-canonicalised batch hashed the same; canonicalization must precede hashing")
	}
}

func TestWorkSetRootIsStable(t *testing.T) {
	ws := makeWorkSet(t, 8)
	ws.Canonicalize()
	r1 := ws.Root()
	// Rebuild with a shuffled slice.
	shuffled := WorkSet{Batches: append([]Batch(nil), ws.Batches...)}
	shuffled.Batches[0], shuffled.Batches[7] = shuffled.Batches[7], shuffled.Batches[0]
	shuffled.Canonicalize()
	if shuffled.Root() != r1 {
		t.Fatalf("workset root changed after re-canonicalising a shuffled copy")
	}
}

func TestPrefixRootMatchesRootForFullPrefix(t *testing.T) {
	ws := makeWorkSet(t, 5)
	ws.Canonicalize()
	full := ws.Root()
	prefix, err := ws.PrefixRoot(uint32(len(ws.Batches)))
	if err != nil {
		t.Fatalf("prefix root: %v", err)
	}
	if prefix != full {
		t.Fatalf("full-length prefix root differs from root")
	}
}

func TestPrefixRootRejectsBadCount(t *testing.T) {
	ws := makeWorkSet(t, 3)
	if _, err := ws.PrefixRoot(0); err == nil {
		t.Fatalf("count=0 must be rejected")
	}
	if _, err := ws.PrefixRoot(4); err == nil {
		t.Fatalf("count > len must be rejected")
	}
}

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

// makeWorkSet constructs a synthetic WorkSet with `size` batches. Each
// batch contains three cells whose IDs are derived deterministically from
// the batch and cell indices so the result is stable across runs.
func makeWorkSet(t *testing.T, size int) WorkSet {
	t.Helper()
	ws := WorkSet{Batches: make([]Batch, size)}
	for i := 0; i < size; i++ {
		cells := make([]ParentCellRef, 3)
		for j := 0; j < 3; j++ {
			id := []byte{byte(i), byte(j), 0xAB}
			var ch [32]byte
			ch[0] = byte(i)
			ch[1] = byte(j)
			cells[j] = ParentCellRef{ID: id, ContentHash: ch}
		}
		ws.Batches[i] = Batch{Cells: cells}
	}
	ws.Canonicalize()
	return ws
}
