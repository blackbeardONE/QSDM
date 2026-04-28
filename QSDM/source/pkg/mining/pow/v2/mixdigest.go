package powv2

import (
	"encoding/binary"

	mining "github.com/blackbeardONE/QSDM/pkg/mining"
	"golang.org/x/crypto/sha3"
)

// NumWalkSteps is the post-fork DAG-walk length. It is identical to
// the v1 constant -- §4 of the protocol changes only the per-step
// body, not the loop count -- and is re-exported here so callers
// never need to import the v1 package just to learn the iteration
// count.
const NumWalkSteps = 64

// ComputeMixDigestV2 runs the post-FORK_V2_TC_HEIGHT mix-digest
// algorithm (MINING_PROTOCOL_V2.md §4.2):
//
//	seed := SHA3-256(header_hash || nonce)
//	mix  := seed
//	for s in 0..64:
//	    idx   := uint32(BE(mix[0..4])) mod N
//	    entry := D_e[idx]
//	    M     := MatrixFromMix(mix)
//	    v     := VectorFromEntry(entry)
//	    r     := TensorMul(M, v)
//	    tc    := PackFP16VectorBE(r)
//	    mix   := SHA3-256(mix || entry || tc)
//	return mix
//
// This function is the validator-side reference. It is pure (no
// global state, no time, no I/O) and returns an error only when the
// supplied DAG implementation fails -- in which case the proof must
// be rejected outright.
func ComputeMixDigestV2(headerHash [32]byte, nonce [16]byte, dag mining.DAG) ([32]byte, error) {
	if dag == nil {
		return [32]byte{}, errNilDAG
	}

	h := sha3.New256()
	_, _ = h.Write(headerHash[:])
	_, _ = h.Write(nonce[:])
	var mix [32]byte
	copy(mix[:], h.Sum(nil))

	n := dag.N()
	if n == 0 {
		return [32]byte{}, errEmptyDAG
	}

	var digest [32]byte
	for s := 0; s < NumWalkSteps; s++ {
		idx := binary.BigEndian.Uint32(mix[0:4]) % n
		entry, err := dag.Get(idx)
		if err != nil {
			return [32]byte{}, err
		}

		M := MatrixFromMix(mix)
		v := VectorFromEntry(entry)
		r := TensorMul(M, v)
		tc := PackFP16VectorBE(r)

		h.Reset()
		_, _ = h.Write(mix[:])
		_, _ = h.Write(entry[:])
		_, _ = h.Write(tc[:])
		sum := h.Sum(digest[:0])
		copy(mix[:], sum)
	}
	return mix, nil
}
