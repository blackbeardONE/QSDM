//go:build !cgo || dilithium_circl
// +build !cgo dilithium_circl

package monitoring

// dilithiumBackend is the static, build-tag-determined identifier of
// the ML-DSA-87 implementation linked into this binary.
//
// !cgo builds and cgo builds with the dilithium_circl override compile
// pkg/crypto/dilithium_circl.go (cloudflare/circl pure-Go ML-DSA-87,
// FIPS 204 wire-compatible with liboqs). The symmetric file
// build_capabilities_dilithium_cgo.go sets this to "liboqs" for cgo
// builds that use the liboqs backend. Exactly one of the two files
// compiles per binary, so the constant is process-wide stable.
const dilithiumBackend = "circl"
