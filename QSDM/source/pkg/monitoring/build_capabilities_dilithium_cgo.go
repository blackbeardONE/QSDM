//go:build cgo
// +build cgo

package monitoring

// dilithiumBackend is the static, build-tag-determined identifier of
// the ML-DSA-87 implementation linked into this binary.
//
// CGO builds compile pkg/crypto/dilithium.go (liboqs FFI). The
// symmetric file build_capabilities_dilithium_pure.go sets this to
// "circl" for !cgo builds. Exactly one of the two files compiles
// per binary, so the constant is process-wide stable.
const dilithiumBackend = "liboqs"
