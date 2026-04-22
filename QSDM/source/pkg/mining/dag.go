package mining

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/crypto/sha3"
)

// DAG is the per-mining-epoch memory-hard dataset (MINING_PROTOCOL.md §3.3).
// Validators hold a full DAG in memory so that step 10 of §7 runs in
// microseconds. Miners also hold it in GPU VRAM. Tests use a much smaller
// N to stay fast.
//
// Entries are fixed-size 32-byte blocks; index 0 is seeded by the
// (mining_epoch, workset_root) pair. Adjacent entries form a hash chain,
// which means a validator computing only one entry on demand pays O(N)
// cost. In practice validators keep the full in-memory DAG; the
// lazy-compute path exists only for tests and narrow audit tooling.
type DAG interface {
	// N returns the number of 32-byte entries in the DAG.
	N() uint32
	// Get returns a copy of DAG entry idx. Callers MUST treat the returned
	// slice as read-only.
	Get(idx uint32) ([32]byte, error)
}

// ProductionDAGSize is N from MINING_PROTOCOL.md §3.3: 2^26 = 67 108 864
// entries × 32 B = 2 GiB. Using this size in a unit test will time out.
const ProductionDAGSize uint32 = 1 << 26

// dagDomainSeparator is the fixed ASCII string that bootstraps D_e[0]. It
// is domain-separated from all other SHA3 uses in the chain so a value
// collision between unrelated hashes is impossible.
const dagDomainSeparator = "QSDM/mesh3d-pow/v1"

// DAGSeed returns the 32-byte seed for entry 0 of the DAG for the given
// mining epoch and work-set root. MINING_PROTOCOL.md §3.3:
//
//	D_e[0] := SHA3-256( "QSDM/mesh3d-pow/v1" || LE64(e) || WS_e.root )
func DAGSeed(epoch uint64, workSetRoot [32]byte) [32]byte {
	h := sha3.New256()
	_, _ = h.Write([]byte(dagDomainSeparator))
	var le64 [8]byte
	binary.LittleEndian.PutUint64(le64[:], epoch)
	_, _ = h.Write(le64[:])
	_, _ = h.Write(workSetRoot[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// InMemoryDAG is a fully-materialised DAG. Use NewInMemoryDAG for
// production (with ProductionDAGSize) or NewInMemoryDAGForTest for tests
// (with a small N that fits the test timeout).
type InMemoryDAG struct {
	n    uint32
	data []byte // n * 32 bytes, contiguous to keep cache lines happy
}

// NewInMemoryDAG builds the DAG for mining-epoch e with the given
// work-set root and size N. N MUST be >= 2 (so that the recurrence has
// at least one non-seed entry) and MUST be <= math.MaxUint32. The build
// takes O(N) SHA3 invocations and allocates N*32 bytes in one block.
func NewInMemoryDAG(epoch uint64, workSetRoot [32]byte, n uint32) (*InMemoryDAG, error) {
	if n < 2 {
		return nil, errors.New("mining: DAG size N must be >= 2")
	}
	d := &InMemoryDAG{
		n:    n,
		data: make([]byte, uint64(n)*32),
	}
	seed := DAGSeed(epoch, workSetRoot)
	copy(d.data[:32], seed[:])
	h := sha3.New256()
	var le32 [4]byte
	var digest [32]byte
	for i := uint32(1); i < n; i++ {
		h.Reset()
		_, _ = h.Write(d.data[uint64(i-1)*32 : uint64(i)*32])
		binary.LittleEndian.PutUint32(le32[:], i)
		_, _ = h.Write(le32[:])
		sum := h.Sum(digest[:0])
		copy(d.data[uint64(i)*32:uint64(i+1)*32], sum)
	}
	return d, nil
}

// N returns the number of entries in the DAG.
func (d *InMemoryDAG) N() uint32 { return d.n }

// Get returns a copy of entry idx. Panics are never possible: out-of-
// range idx returns a typed error instead.
func (d *InMemoryDAG) Get(idx uint32) ([32]byte, error) {
	if idx >= d.n {
		return [32]byte{}, fmt.Errorf("mining: DAG index %d >= N %d", idx, d.n)
	}
	var out [32]byte
	copy(out[:], d.data[uint64(idx)*32:uint64(idx+1)*32])
	return out, nil
}

// -----------------------------------------------------------------------------
// LazyDAG — O(N) per lookup, for test-scaffolding only
// -----------------------------------------------------------------------------

// LazyDAG re-computes each requested entry from scratch. It is useful in
// audit tooling that needs to spot-check a single D_e[idx] without
// materialising 2 GiB. Production validators MUST NOT use this type.
type LazyDAG struct {
	epoch uint64
	root  [32]byte
	n     uint32

	mu    sync.Mutex
	seed  [32]byte
	built bool
}

// NewLazyDAG returns a lazy DAG view. No memory is allocated up front.
func NewLazyDAG(epoch uint64, workSetRoot [32]byte, n uint32) (*LazyDAG, error) {
	if n < 2 {
		return nil, errors.New("mining: DAG size N must be >= 2")
	}
	return &LazyDAG{epoch: epoch, root: workSetRoot, n: n}, nil
}

// N returns the number of entries.
func (d *LazyDAG) N() uint32 { return d.n }

// Get recomputes D_e[idx] from scratch in O(idx) SHA3 calls.
func (d *LazyDAG) Get(idx uint32) ([32]byte, error) {
	if idx >= d.n {
		return [32]byte{}, fmt.Errorf("mining: DAG index %d >= N %d", idx, d.n)
	}
	d.mu.Lock()
	if !d.built {
		d.seed = DAGSeed(d.epoch, d.root)
		d.built = true
	}
	cur := d.seed
	d.mu.Unlock()
	if idx == 0 {
		return cur, nil
	}
	h := sha3.New256()
	var le32 [4]byte
	buf := make([]byte, 0, 32+4)
	for i := uint32(1); i <= idx; i++ {
		h.Reset()
		_, _ = h.Write(cur[:])
		binary.LittleEndian.PutUint32(le32[:], i)
		_, _ = h.Write(le32[:])
		buf = h.Sum(buf[:0])
		copy(cur[:], buf)
	}
	return cur, nil
}

// -----------------------------------------------------------------------------
// Sanity compile-time checks
// -----------------------------------------------------------------------------

var (
	_ DAG = (*InMemoryDAG)(nil)
	_ DAG = (*LazyDAG)(nil)
)
