//go:build cgo && cuda
// +build cgo,cuda

package monitoring

// mesh3dBackend is the static, build-tag-determined identifier of
// the mesh-3D validator backend compiled into this binary.
//
// The constraint is the exact mirror of pkg/mesh3d/cuda.go's build
// tag (the real CUDA-accelerated path). Every other tag combination
// falls into build_capabilities_mesh3d_cpu.go and gets the
// "cpu_fallback" label.
const mesh3dBackend = "cuda"
