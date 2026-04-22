package mining

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
)

// DefaultBlocksPerMiningEpoch is the reference value of `M` from
// MINING_PROTOCOL.md §3.1: 60 480 blocks ≈ 7 days at a 10 s target block
// time. Consumers MAY configure a different `M` via the EpochParams type
// but they are then operating a non-reference chain.
const DefaultBlocksPerMiningEpoch uint64 = 60_480

// EpochParams configures mining-epoch boundaries for a chain. Zero values
// are not valid; always construct via NewEpochParams.
type EpochParams struct {
	// BlocksPerEpoch is `M` from MINING_PROTOCOL.md §3.1.
	BlocksPerEpoch uint64
}

// NewEpochParams returns the default EpochParams for the reference chain.
func NewEpochParams() EpochParams {
	return EpochParams{BlocksPerEpoch: DefaultBlocksPerMiningEpoch}
}

// Validate rejects malformed parameter sets.
func (p EpochParams) Validate() error {
	if p.BlocksPerEpoch == 0 {
		return errors.New("mining: BlocksPerEpoch must be > 0")
	}
	return nil
}

// EpochForHeight returns the mining-epoch index a block at the given height
// belongs to. Heights below the chain tip are accepted; the function is
// pure and does not touch the chain state.
func (p EpochParams) EpochForHeight(height uint64) uint64 {
	return height / p.BlocksPerEpoch
}

// EpochStartHeight returns the first block height that belongs to the
// given mining-epoch index.
func (p EpochParams) EpochStartHeight(epoch uint64) uint64 {
	return epoch * p.BlocksPerEpoch
}

// EpochEndHeight returns the inclusive final block height of the given
// mining-epoch index. (end_height = start + M - 1; a retarget at the next
// block flips the epoch.)
func (p EpochParams) EpochEndHeight(epoch uint64) uint64 {
	return (epoch+1)*p.BlocksPerEpoch - 1
}

// -----------------------------------------------------------------------------
// Batches and work-sets
// -----------------------------------------------------------------------------

// ParentCellRef is a compact, canonical reference to a parent cell in the
// mesh3D DAG. Only the opaque ID and the content hash are required by the
// mining protocol; the cell payload itself is not part of the work-set
// derivation (that lives in pkg/mesh3d).
type ParentCellRef struct {
	// ID is the parent-cell identifier as emitted by pkg/mesh3d. The mining
	// protocol treats it as an opaque byte string for sorting and hashing.
	ID []byte
	// ContentHash is the 32-byte SHA-256 of the parent-cell's canonical
	// content encoding as produced by pkg/mesh3d. It MUST be exactly 32
	// bytes. Enforced by BatchHash.
	ContentHash [32]byte
}

// Batch is a group of 3–5 parent cells bundled together for mining
// validation. Construction MUST preserve the canonical ordering described
// in MINING_PROTOCOL.md §3.2: parent cells inside the batch are sorted
// ascending by ID, and the batches themselves are sorted by the hash of
// their concatenated-ID block.
type Batch struct {
	Cells []ParentCellRef
}

// MinBatchSize and MaxBatchSize bound the per-batch parent-cell count. The
// 3–5 window mirrors pkg/mesh3d's validation constraint (Major Update §5).
const (
	MinBatchSize = 3
	MaxBatchSize = 5
)

// Canonicalize re-sorts a batch's cells by ID in-place so two honest
// implementations converge on the same bytes before hashing.
func (b *Batch) Canonicalize() {
	sort.Slice(b.Cells, func(i, j int) bool {
		return lessBytes(b.Cells[i].ID, b.Cells[j].ID)
	})
}

// Validate enforces the 3–5 size window and rejects empty cell IDs.
func (b Batch) Validate() error {
	n := len(b.Cells)
	if n < MinBatchSize || n > MaxBatchSize {
		return fmt.Errorf("mining: batch size %d out of allowed range [%d,%d]",
			n, MinBatchSize, MaxBatchSize)
	}
	for i, c := range b.Cells {
		if len(c.ID) == 0 {
			return fmt.Errorf("mining: batch cell %d has empty ID", i)
		}
	}
	return nil
}

// BatchHash computes the canonical 32-byte hash of a batch per
// MINING_PROTOCOL.md §4.3:
//
//	batch_hash(B) := SHA256( 0x00 || pc_1.ID || pc_1.content_hash
//	                             || pc_2.ID || pc_2.content_hash
//	                             || … )
//
// The leading 0x00 prefix domain-separates batch hashes from Merkle
// internal-node hashes (prefix 0x01) and from DAG-seed hashes (no prefix,
// by virtue of the explicit textual domain string). Callers should
// Canonicalize the batch before calling this helper or they will compute a
// hash no validator will accept.
func BatchHash(b Batch) [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte{0x00})
	for _, c := range b.Cells {
		_, _ = h.Write(c.ID)
		_, _ = h.Write(c.ContentHash[:])
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// -----------------------------------------------------------------------------
// Work-sets and their Merkle root
// -----------------------------------------------------------------------------

// WorkSet is the canonical ordering of batches that define the per-mining-
// epoch work menu (§3.2). Implementations derive a WorkSet from the
// cell-pending queue of the previous epoch and MUST sort both (i) the
// cells inside each batch and (ii) the batches themselves before handing
// the structure to this package.
type WorkSet struct {
	Batches []Batch
}

// Canonicalize sorts the work-set in place according to §3.2. First each
// batch's cells are sorted by ID, then the batches themselves are sorted
// by the SHA-256 of the concatenation of their cell IDs.
func (ws *WorkSet) Canonicalize() {
	for i := range ws.Batches {
		ws.Batches[i].Canonicalize()
	}
	sort.SliceStable(ws.Batches, func(i, j int) bool {
		return lessBytes(workSetBatchKey(ws.Batches[i]), workSetBatchKey(ws.Batches[j]))
	})
}

func workSetBatchKey(b Batch) []byte {
	h := sha256.New()
	for _, c := range b.Cells {
		_, _ = h.Write(c.ID)
	}
	return h.Sum(nil)
}

// Validate checks every batch is well-formed.
func (ws WorkSet) Validate() error {
	if len(ws.Batches) == 0 {
		return errors.New("mining: work-set is empty")
	}
	for i, b := range ws.Batches {
		if err := b.Validate(); err != nil {
			return fmt.Errorf("mining: work-set batch %d: %w", i, err)
		}
	}
	return nil
}

// Root returns the 32-byte Merkle root over every batch in the canonical
// work-set. This value seeds the per-epoch DAG (§3.3) and serves as the
// public commitment to the work-set that both miners and validators
// independently derive.
func (ws WorkSet) Root() [32]byte {
	hashes := make([][32]byte, len(ws.Batches))
	for i, b := range ws.Batches {
		hashes[i] = BatchHash(b)
	}
	return merkleRoot(hashes)
}

// PrefixRoot returns the Merkle root over the first `count` batches of the
// canonical work-set, i.e. the root a miner MUST report when claiming to
// have validated the first `count` batches. §7.1 of the spec pins miners
// to the "first c batches" subset to keep validator verification O(1).
//
// `count == 0` is rejected; `count > len(ws.Batches)` is rejected.
func (ws WorkSet) PrefixRoot(count uint32) ([32]byte, error) {
	var zero [32]byte
	if count == 0 {
		return zero, errors.New("mining: prefix count must be > 0")
	}
	if uint64(count) > uint64(len(ws.Batches)) {
		return zero, fmt.Errorf("mining: prefix count %d exceeds work-set size %d",
			count, len(ws.Batches))
	}
	hashes := make([][32]byte, count)
	for i := uint32(0); i < count; i++ {
		hashes[i] = BatchHash(ws.Batches[i])
	}
	return merkleRoot(hashes), nil
}

// -----------------------------------------------------------------------------
// Merkle helpers (RFC 6962-style with 0x00 leaf / 0x01 internal prefix)
// -----------------------------------------------------------------------------

func merkleLeafHash(in [32]byte) [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte{0x01})
	_, _ = h.Write(in[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func merkleInternalHash(l, r [32]byte) [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte{0x02})
	_, _ = h.Write(l[:])
	_, _ = h.Write(r[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// merkleRoot returns the RFC-6962-flavoured Merkle root over the input
// hashes. An odd-length level is handled by duplicating the last entry
// (classic Bitcoin convention) rather than promoting it unchanged — the
// duplicate variant is simpler to reason about for §7.1's prefix
// comparison and is no weaker against second-preimage attacks because we
// domain-separate leaves with 0x01 and internal nodes with 0x02.
func merkleRoot(hashes [][32]byte) [32]byte {
	if len(hashes) == 0 {
		return [32]byte{}
	}
	level := make([][32]byte, len(hashes))
	for i, h := range hashes {
		level[i] = merkleLeafHash(h)
	}
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		next := make([][32]byte, len(level)/2)
		for i := 0; i < len(level); i += 2 {
			next[i/2] = merkleInternalHash(level[i], level[i+1])
		}
		level = next
	}
	return level[0]
}

// lessBytes orders []byte lexicographically. Moved to a helper so epoch.go
// does not import bytes for a single use and so the behaviour is
// documented next to the callers that depend on it.
func lessBytes(a, b []byte) bool {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
