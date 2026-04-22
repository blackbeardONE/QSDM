package mining

import (
	"encoding/binary"
	"math/big"

	"golang.org/x/crypto/sha3"
)

// NumWalkSteps is the constant number of DAG lookups a miner performs per
// attempt (MINING_PROTOCOL.md §5.3). Changing this is a hard fork.
const NumWalkSteps = 64

// ComputeMixDigest performs the 64-step DAG walk described in
// MINING_PROTOCOL.md §5.3:
//
//	seed := SHA3-256( header_hash || nonce )
//	mix  := seed
//	for s in 0..64:
//	    idx := uint32(BE(mix[0..4])) mod N
//	    mix := SHA3-256( mix || D_e[idx] )
//	mix_digest := mix
//
// The function is pure: same inputs always give the same output. It is
// also the only hot-path call on the validator side (§1.1(4)), so we
// reuse a single sha3 state across the 64 iterations.
func ComputeMixDigest(headerHash [32]byte, nonce [16]byte, dag DAG) ([32]byte, error) {
	h := sha3.New256()
	_, _ = h.Write(headerHash[:])
	_, _ = h.Write(nonce[:])
	var mix [32]byte
	copy(mix[:], h.Sum(nil))

	n := dag.N()
	var digest [32]byte
	for s := 0; s < NumWalkSteps; s++ {
		idx := binary.BigEndian.Uint32(mix[0:4]) % n
		entry, err := dag.Get(idx)
		if err != nil {
			return [32]byte{}, err
		}
		h.Reset()
		_, _ = h.Write(mix[:])
		_, _ = h.Write(entry[:])
		sum := h.Sum(digest[:0])
		copy(mix[:], sum)
	}
	return mix, nil
}

// ProofPoWHash is the final hash that MUST fall below the target for a
// proof to be accepted (MINING_PROTOCOL.md §5.2):
//
//	H( header_hash || nonce || batch_root || mix_digest )
//
// The four inputs concatenate as fixed-width byte strings (32 || 16 ||
// 32 || 32 = 112 bytes).
func ProofPoWHash(headerHash [32]byte, nonce [16]byte, batchRoot, mixDigest [32]byte) [32]byte {
	h := sha3.New256()
	_, _ = h.Write(headerHash[:])
	_, _ = h.Write(nonce[:])
	_, _ = h.Write(batchRoot[:])
	_, _ = h.Write(mixDigest[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// MeetsTarget returns true iff the 256-bit hash (big-endian) is strictly
// less than the target. Both arguments are treated as unsigned 256-bit
// big-endian integers per MINING_PROTOCOL.md §5.2.
func MeetsTarget(hash [32]byte, target *big.Int) bool {
	if target == nil || target.Sign() <= 0 {
		return false
	}
	h := new(big.Int).SetBytes(hash[:])
	return h.Cmp(target) < 0
}
