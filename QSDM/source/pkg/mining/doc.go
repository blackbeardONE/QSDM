// Package mining implements the QSDM mining sub-protocol: epoch management,
// proof-of-work verification, difficulty retargeting, and the canonical
// proof wire format.
//
// The normative specification lives in QSDM/docs/docs/MINING_PROTOCOL.md.
// Where this package and the spec diverge, the spec is authoritative and
// this code is a bug that must be fixed.
//
// # Subpackages
//
//   - pkg/mining/roleguard — startup role/build-profile consistency guard
//     (already in place as of Major Update Phase 2.3).
//
// # What this package does NOT do
//
// This package deliberately does not:
//
//   - Serve HTTP endpoints. That is pkg/api's job; see handlers_mining.go
//     (Phase 4 wiring) and handlers_status.go (read-only /api/v1/status).
//   - Talk to the chain state. The Verifier is given a ChainView via
//     dependency injection so it can be unit-tested without a running node.
//   - Run CUDA kernels. That is pkg/mining/cuda's job (build-tagged, Phase 6
//     post-audit).
//   - Participate in consensus. Miners are additive: see MINING_PROTOCOL.md §9.
//
// # Design notes
//
// All code in this package is pure Go and stdlib-only (plus
// golang.org/x/crypto/sha3 for FIPS-202 SHA3-256) so it compiles unchanged
// on every platform the QSDM validator binary targets. CGO-backed mesh3D
// batch verification is reached via the BatchValidator interface (§7 step
// 11 of the spec); the reference validator wires in pkg/mesh3d, while
// tests plug in an in-memory fake.
//
// Determinism is enforced at the type level where possible: all IDs are
// fixed-size byte arrays, all serialization uses canonical-JSON helpers in
// proof.go, and all integer math on 256-bit quantities goes through
// math/big.Int with explicit byte-width assertions.
package mining

// ProtocolVersion is the wire-format version of a Proof. Bumping this is a
// hard fork; see MINING_PROTOCOL.md §11.
const ProtocolVersion uint32 = 1
