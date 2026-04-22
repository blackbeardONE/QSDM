# Building CUDA Kernels for mesh3d

## Prerequisites
- NVIDIA GPU with Compute Capability >= 5.0
- CUDA Toolkit >= 11.0
- `nvcc` on PATH

## Windows
```powershell
cd pkg/mesh3d/kernels
nvcc -shared -o mesh3d_kernels.dll sha256_validate.cu
# Copy DLL to a directory on PATH or next to the Go binary
copy mesh3d_kernels.dll ..\..\..\
```

## Linux
```bash
cd pkg/mesh3d/kernels
nvcc -shared -fPIC -o libmesh3d_kernels.so sha256_validate.cu
sudo cp libmesh3d_kernels.so /usr/local/lib/
sudo ldconfig
```

## Build the Go binary with CUDA support
```bash
CGO_ENABLED=1 go build -tags cuda ./cmd/qsdmplus/
```

## Without CUDA
When the CUDA toolkit is not installed, the build uses `cuda_stub.go` which returns nil for all GPU operations. The `CPUParallelAccelerator` in `gpu.go` provides a goroutine-based fallback.
