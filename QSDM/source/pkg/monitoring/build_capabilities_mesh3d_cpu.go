//go:build !(cgo && cuda)

package monitoring

// mesh3dBackend is the static, build-tag-determined identifier of
// the mesh-3D validator backend compiled into this binary.
//
// The constraint is the exact inverse of pkg/mesh3d/cuda.go's build
// tag, so it covers the union of:
//
//   - pkg/mesh3d/cuda_stub.go        (cgo, but no explicit cuda tag)
//   - pkg/mesh3d/mesh3d_stub.go      (!cgo, every OS)
//
// In both cases the validator runs the CPU fallback path; this
// label distinguishes "CPU fallback because the binary was built
// without CUDA" from a real CUDA build whose driver later panicked
// at runtime (which would surface elsewhere — qsdm_mesh3d_*
// counters and qsdm_stub_active{kind="mesh3d_cuda"}).
//
// Note: we deliberately drop the legacy `// +build` line here.
// Negation across multiple operands is awkward to express in the
// pre-1.17 syntax and the modern //go:build directive is the only
// one Go 1.17+ honours; including a wrong/incomplete legacy line
// would silently confuse older toolchains. Pure //go:build is the
// safer choice for this single complex constraint.
const mesh3dBackend = "cpu_fallback"
