# Building the QSDM mesh3d CUDA kernels

The kernels live in [`sha256_validate.cu`](sha256_validate.cu) and ship as
a shared object that `pkg/mesh3d/cuda.go` links against via cgo.

## Prerequisites

- NVIDIA GPU with compute capability ≥ 5.0 (Maxwell GTX 9xx and newer)
- CUDA Toolkit ≥ 11.0; ≥ 12.0 recommended (sm_89, sm_90 codepaths)
- `nvcc` on `PATH`
- GNU `make` (Linux/macOS) or MSYS2 / cygwin `make` (Windows)

## Quick build (recommended)

The reproducible default. Produces a fat binary covering every still-supported
NVIDIA GPU architecture (sm_50 through sm_90) so the resulting library runs on
every GPU in the support matrix without nvcc on the operator host.

```bash
cd QSDM/source/pkg/mesh3d/kernels
make
```

Outputs:

| Host | Artefact |
|------|----------|
| Linux | `build/libmesh3d_kernels.so` |
| macOS | `build/libmesh3d_kernels.dylib` |
| Windows (MSYS2) | `build/mesh3d_kernels.dll` |

Install (Linux):

```bash
sudo cp build/libmesh3d_kernels.so /usr/local/lib/
sudo ldconfig
```

Install (Windows):

```powershell
copy build\mesh3d_kernels.dll <next-to-qsdm.exe>
```

## Per-architecture build

When you only target one GPU and want a smaller binary:

```bash
make sm_80    # NVIDIA A100
make sm_86    # GeForce RTX 30xx / RTX A series
make sm_89    # GeForce RTX 40xx / Ada
make sm_90    # H100 / Hopper
```

## Override the toolchain

```bash
make NVCC=/opt/cuda-12.6/bin/nvcc
make GENCODES='-gencode arch=compute_80,code=sm_80'
```

## Build the QSDM Go binary against the kernels

Once the shared object is on the loader's path:

```bash
CGO_ENABLED=1 go build -tags cuda ./cmd/qsdm
```

## Without CUDA

When the CUDA toolkit is not installed, the build uses
[`../cuda_stub.go`](../cuda_stub.go) which returns `nil, nil` for every GPU
operation, and the [`CPUParallelAccelerator`](../gpu.go) provides a
goroutine-based fallback that mirrors the kernel API exactly. The
`qsdm_stub_active{kind="mesh3d_cuda"}` gauge flips to `1` so operators see
they're on the CPU path.

## Hardening notes

The host-side wrappers in [`sha256_validate.cu`](sha256_validate.cu) check
every `cudaMalloc`, `cudaMemcpy`, kernel launch and `cudaDeviceSynchronize`
call individually via the `CUDA_CHECK` / `CUDA_CHECK_LAUNCH` macros and free
every device buffer that was already allocated on the failure path. The Go
side ([`../cuda.go`](../cuda.go)) translates the numeric return code to a
symbolic name (e.g. `cudaErrorMemoryAllocation`) so operators see what's
actually wrong without grepping the CUDA headers. Errors are returned to
the caller; callers are expected to fall back to the CPU accelerator when
the CUDA path returns a non-nil error.
