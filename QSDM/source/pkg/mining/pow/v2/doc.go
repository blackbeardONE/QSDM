// Package powv2 is the validator-side byte-exact reference implementation
// of the post-fork Tensor-Core PoW mixin specified in
// QSDM/docs/docs/MINING_PROTOCOL_V2.md §4.
//
// Status
//
// Pre-fork: validators run pkg/mining.ComputeMixDigest (the v1 64-step
// SHA3 walk). Post-FORK_V2_TC_HEIGHT: validators run
// powv2.ComputeMixDigestV2 in this package. The two functions take the
// same inputs and produce the same 32-byte output shape, but the v2
// step-body folds in a deterministic 16x16 FP16 matrix-vector multiply
// keyed off both the running mix and the DAG entry.
//
// Why a reference impl
//
// §4 of the protocol specifies the mixin abstractly ("16x16 FP16
// matmul with round-to-nearest-even"), but several details that affect
// bit-exact output are implementation-defined in IEEE-754 FP16:
//
//   - How the matrix is derived from the 32-byte mix (the running mix
//     is only 256 bits, far short of the 4096 bits a 16x16 FP16 matrix
//     needs).
//   - Endianness of the 2-byte FP16 encoding.
//   - NaN bit-pattern canonicalization (FP16 has 1022 distinct NaN
//     payloads; CUDA, x86, and ARM disagree on which one they emit).
//   - Order of accumulation inside the dot-product (CUDA WMMA fragments
//     do tree-reduction; CPU loops do left-to-right; results differ in
//     the last bit of FP16).
//
// This package is THE answer to those four questions. The validator is
// authoritative; any miner (CUDA, OpenCL, or otherwise) that produces
// a different 32-byte mix-digest for the same inputs is wrong, no
// matter what the hardware fast-path natively computes. Miners that
// want to use the WMMA fast-path must emulate this package's order
// of operations exactly.
//
// Locked byte-exact decisions
//
//   - Matrix expansion (mix -> 16x16 FP16):
//       M_bytes := SHAKE256("qsdm/pow/v2/matrix\x00" || mix), read 512 B.
//       For i,j in 0..16:
//         M[i][j] := DecodeFP16BE(M_bytes[2*(16*i + j) : 2*(16*i + j) + 2]).
//   - Vector unpack (entry -> 16 FP16):
//       For k in 0..16:
//         v[k] := DecodeFP16BE(D_e[idx][2k : 2k+2]).
//   - Matmul (per output element r[i]):
//       acc := float32(0)
//       for j in 0..16: acc = acc + float32(M[i][j]) * float32(v[j])  // RNE, left-to-right
//       r[i] := Float32ToFP16RNE(acc)
//   - NaN canonicalization:
//       FP16 NaN  -> 0x7E00       (sign=0, exp=11111, mantissa=1100000000)
//       FP32 NaN  -> 0x7FC00000
//     applied at every encode boundary so platform-specific NaN payloads
//     never leak into the SHA3 input.
//   - Subnormals and signed zero:
//       Preserved (no flush-to-zero, no -0 collapse). This matches CUDA
//       default and matters for proofs whose mix happens to land near
//       2^-14.
//   - Step body, post-fork:
//       seed := SHA3-256(header_hash || nonce)
//       mix  := seed
//       for s in 0..64:
//         idx   := uint32(BE(mix[0..4])) mod N
//         entry := D_e[idx]
//         M     := MatrixFromMix(mix)
//         v     := VectorFromEntry(entry)
//         r     := TensorMul(M, v)
//         tc    := PackFP16VectorBE(r)               // 32 bytes
//         mix   := SHA3-256(mix || entry || tc)
//       return mix
//
// Performance is explicitly NOT a goal of this package. The one-step
// budget on a modern x86 core is ~10us (SHAKE256 expansion dominates),
// well inside the §4.3 700us-per-proof validator SLO with the 64-step
// outer loop.
package powv2
