# mesh3d GPU benchmark — RTX 3050 (reference run)

**Purpose.** Operators sizing a miner rig want one concrete answer:
*does a GPU actually beat a CPU at mesh3d parent-cell validation, and
at what fan-out does the crossover happen?*  This page pins a reference
run from a dev box so the number has a source every time the docs, the
website copy, or a support ticket cite it.

All results below come from `BenchmarkMesh3DGPUVsCPU_*` in
`QSDM/source/pkg/mesh3d/mesh3d_gpu_bench_test.go`. Reproduce with:

```powershell
# 1. CUDA toolkit (12.x) must already be installed (we probe CUDA_PATH).
.\QSDM\scripts\build_liboqs_win.ps1 -SetEnv      # first time only (~2 min)
.\QSDM\scripts\build_kernels.ps1 -SetEnv         # ~30 s, per cu-source edit

# 2. Compile the test binary — go test itself can't find the DLLs from
#    its %TEMP% work dir, so we build and run explicitly.
cd QSDM\source
$env:CGO_ENABLED = '1'
go test -tags cuda -c -o _mesh3d.test.exe ./pkg/mesh3d/
Start-Process .\_mesh3d.test.exe -NoNewWindow -Wait -ArgumentList `
    '-test.bench=BenchmarkMesh3DGPUVsCPU', `
    '-test.benchmem', `
    '-test.benchtime=1s', `
    '-test.run=^$'
```

## Reference hardware (2026-04-23)

| Component | Value |
|-----------|-------|
| GPU | NVIDIA GeForce RTX 3050 (Ampere, 8 GB, CC 8.6) |
| Driver | 576.28 |
| CUDA Toolkit | 12.9 |
| Host CPU | Intel Xeon E5-2670 @ 2.60 GHz (32 threads) |
| Go | 1.25.9 (MSYS2 mingw64 toolchain) |
| MSVC (for nvcc) | VS 2017 Build Tools |

## Validate (SHA-256 + early-reject + all-zero scan)

Workload: `n` parent cells × 64 bytes, random patterns, throughput
reported over the aggregate byte count per op.

| n (cells) | CPU 32-thread | GPU RTX 3050 | GPU speedup |
|-----------|---------------|--------------|-------------|
| 16 | 26 µs, 39 MB/s | 732 µs, 1.4 MB/s | 0.04× |
| 256 | 259 µs, 63 MB/s | 753 µs, 22 MB/s | 0.34× |
| 4096 | 4.39 ms, 60 MB/s | **1.08 ms, 243 MB/s** | **4.06×** |

## Hash only (no result scan)

| n (cells) | CPU 32-thread | GPU RTX 3050 | GPU speedup |
|-----------|---------------|--------------|-------------|
| 16 | 28 µs, 37 MB/s | 1031 µs, 1.0 MB/s | 0.03× |
| 256 | 281 µs, 58 MB/s | 1134 µs, 14 MB/s | 0.25× |
| 4096 | 4.88 ms, 54 MB/s | **2.19 ms, 120 MB/s** | **2.23×** |

## What the numbers mean

1. **Launch overhead dominates up to ~1k cells.** On Windows the
   CUDA kernel-launch + host→device memcpy for a handful of 64-byte
   cells costs around 700–1000 µs; the useful work is a fraction of
   that. At n=16 the CPU finishes the whole batch in 26 µs, faster
   than CUDA can even *start* the GPU kernel.
2. **GPU pulls ahead somewhere between n=256 and n=4k.** With this
   Xeon + RTX 3050 pairing the break-even point sits around n≈1000.
   The exact number shifts with:
   - cell size (larger cells amortise the launch faster),
   - CPU thread count (a 4-core i5 would cross over at n~200),
   - PCIe generation (a Gen4 link halves the memcpy time).
3. **Throughput ceiling at n=4k: 243 MB/s for validate, 120 MB/s for
   raw hash.** The validate path benefits from the early-exit on
   cells below 32 bytes (our sample data has none, but the branch
   still prunes the all-zero scan). Larger cell payloads would push
   the ceiling further; this benchmark intentionally uses the
   single-block SHA-256 path (≤55 bytes).

## Operator guidance

- **Validators (CPU-only by design).** No GPU needed — the
  `Mesh3DValidator` falls back to `CPUParallelAccelerator`, which on
  a modern 8+ core server already hits tens of MB/s per second and
  scales linearly with core count. GPU wouldn't help at the fan-outs
  validators see per block.
- **Miners batching thousands of candidate cells.** A GPU is a clear
  win at n ≥ ~2k; the RTX 3050 (entry-level Ampere, street price
  ~$180) is enough to 2–4× the mesh3d validation throughput of a
  32-thread Xeon. Anything above an RTX 3060 12 GB will widen this
  further because those SKUs have more SMs and a 192-bit memory bus.

## Known caveats

- The `CGO_ENABLED=0` validator CI build ignores `cuda.go` via the
  `//go:build cgo && (windows || (linux && cuda))` tag, so none of
  these numbers show up in the default CI run. Rerun this page by
  hand on any machine that changes the mesh3d kernel or the
  `GPUAccelerator` interface.
- `go test` invokes the benchmark binary from a `%TEMP%\go-build*`
  directory where Windows' DLL search policy ignores PATH. The
  `-c -o ... && Start-Process` split above side-steps that; the
  mesh3d package directory is on cwd when the binary runs, so
  `liboqs.dll`, `cudart64_12.dll`, and `mesh3d_kernels.dll` resolve
  cleanly from the existing PATH entries.
