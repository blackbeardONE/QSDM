# MINER_RTX_3050_COOKBOOK — v2 mining on a consumer Ampere card

> [!IMPORTANT]
> **Historical build-from-source notes.** This page predates QSDM Hive 1.4.21
> and is not how most people mine today. Hive ships the miner and CUDA solver
> for you: follow [Mining today (Hive 1.4.21)](MINING_TODAY.md). New mining
> enrollments are paused during the current recovery; only GPUs enrolled
> before the recovery can mine.

> **Audience.** A home operator with an **NVIDIA GeForce RTX 3050** (or
> any other Ampere card: RTX 30-series, A2/A4/A10) who wants to mine
> Cell on the live `https://api.qsdm.tech` pilot network (pre-mainnet). Higher SKUs
> (RTX 40-series Ada, RTX 50-series Blackwell consumer) follow the
> same recipe with a different `-Arch` flag.
>
> **Status.** The live validator advertises
> `attestation_types_required = ["nvidia-cc-v1", "nvidia-hmac-v1"]` —
> the dispatcher accepts a proof carrying **either** type (see
> `pkg/mining/attest/dispatcher.go::VerifyAttestation`), so the
> consumer HMAC path is fully wired. Ampere is explicitly registered
> in `pkg/mining/attest/archcheck`. The kernel was
> proved end-to-end on this exact silicon on 2026-04-23 — numbers in
> [`MESH3D_GPU_BENCHMARK.md`](./MESH3D_GPU_BENCHMARK.md).
>
> The general v2 quickstart is [`MINER_QUICKSTART.md`](./MINER_QUICKSTART.md);
> this page is just the RTX-3050-specific overlay (which arch flag to
> pass to `nvcc`, what to expect in the
> live panel). Read both.

---

## 0. Reference rig (what was proved)

| Component       | Value                                            |
|-----------------|--------------------------------------------------|
| GPU             | NVIDIA GeForce RTX 3050 (Ampere, 8 GB, CC 8.6)   |
| Driver          | 576.28                                           |
| CUDA Toolkit    | 12.9 at `C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA\v12.9` |
| Host CPU        | Intel Xeon E5-2670 @ 2.60 GHz (32 threads)       |
| Host OS         | Windows 10 22H2                                  |
| Go              | 1.25.9 (MSYS2 mingw64 toolchain)                 |
| MSVC (for nvcc) | VS 2017 Build Tools                              |
| Validate kernel | 4.06× over CPU at n=4096 (243 MB/s)              |
| Hash kernel     | 2.23× over CPU at n=4096 (120 MB/s)              |
| Break-even      | ≈ n=1000 cells                                   |

The benchmark binary used here is the same `pkg/mesh3d` kernel that
the production miner links against, built with the `cuda` build tag —
the "what runs on your GPU during mining" cannot diverge from "what
the benchmark proves" without breaking CI.

---

## 1. One-time tooling install

```powershell
# CUDA Toolkit 12.x (any 12.x works; pinned to 12.9 in the reference run)
#   https://developer.nvidia.com/cuda-downloads
#   Choose: Windows / x86_64 / 10 / exe (local). 3.4 GB.
#   Reboot.
nvidia-smi  # confirm driver + GPU + CUDA runtime version

# MSVC build tools for nvcc's host compiler (VS 2017 build tools 14.16 is enough)
#   https://aka.ms/vs/15/release/vs_buildtools.exe
#   Workload: "C++ build tools" → MSVC v141 + Windows 10 SDK

# Go 1.25+
#   https://go.dev/dl/  → go1.25.9.windows-amd64.msi

# MSYS2 mingw64 for cgo on Windows (only needed for the cuda fatbin build)
#   https://www.msys2.org/
#   In MSYS2 shell: pacman -S mingw-w64-x86_64-gcc mingw-w64-x86_64-pkgconf
#   Add C:\msys64\mingw64\bin to PATH.

# Verify
nvcc --version          # → release 12.x
cl                      # → MSVC 19.16+ (VS 2017 build tools) on PATH
go version              # → go1.25+
gcc --version           # → MSYS2 mingw64 12.x+
```

---

## 2. Build the CUDA fatbin + the cuda-tagged miner

```powershell
git clone https://github.com/blackbeardONE/QSDM
cd QSDM

# Narrow the build to your arch — sm_86 = RTX 30-series (Ampere consumer).
# Drop the -Arch flag to build a Turing→Hopper fatbin instead.
.\QSDM\scripts\build_kernels.ps1 -Arch 'sm_86' -SetEnv     # ~60 s

# liboqs (post-quantum signatures, needed by the SDK linked into the miner)
.\QSDM\scripts\build_liboqs_win.ps1 -SetEnv                # ~2 min, first time only

# Build the friendly console miner with the `cuda` tag so it picks up
# pkg/mesh3d/cuda.go instead of the CPU-only stub.
cd QSDM\source
$env:CGO_ENABLED = '1'
go build -tags cuda -trimpath -ldflags "-s -w" `
    -o ..\..\bin\qsdmminer-console.exe `
    .\cmd\qsdmminer-console
```

The binary is now self-contained — the CUDA fatbin is loaded at run
time from `mesh3d_kernels.dll` next to the executable, and the
post-quantum signing path comes from `liboqs.dll` (also next to the
binary or somewhere on PATH).

If you skip `-tags cuda` the miner still works; it just runs every
mesh3d validate on the CPU fallback, which on this rig is ~60× slower
at the fan-outs miners see (see §0 numbers). The on-chain proof
itself is identical either way — the `nvidia-hmac-v1` attestation
only proves *who* signed the proof, not *which kernel computed it* —
but the operator with `-tags cuda` solves more challenges per second
and earns more emission. **Always pass `-tags cuda` on a GPU host.**

---

## 3. Generate an HMAC key

```powershell
mkdir $env:USERPROFILE\.qsdm 2>$null
.\bin\qsdmminer-console.exe --gen-hmac-key $env:USERPROFILE\.qsdm\miner-hmac.hex
```

This prints the 64-char hex line that the `qsdmcli enroll` step
needs, and writes the file `0o600` (Windows ACL equivalent). The
private key never leaves disk — the validator only ever sees the
SHA-256 of `(challenge || gpu_uuid || node_id || hmac_key)`.

> **Same key, every mining session.** If you regenerate the file your
> previous enrollment becomes useless and you have to enroll again
> with a fresh `(node_id, gpu_uuid, hmac_key)` tuple. The previous
> stake is recoverable after the unbonding period of 201,600 blocks (about 23 days at 10-second blocks)
> (see [Mining protocol v2](MINING_PROTOCOL_V2.md)).

---

## 4. Reward address + funding

Follow [`MINER_QUICKSTART.md §2`](./MINER_QUICKSTART.md#2-reward-address)
for the keystore. Two routes; both produce a `pkg/keystore` v1 JSON
file with a PBKDF2-HMAC-SHA-256(600 000 iter) + AES-256-GCM-wrapped
ML-DSA-87 keypair.

You do not need CELL up front. The 10 CELL enrollment bond can be built from
your first mining rewards (`--bond-from-rewards`, or **Use mining earnings** in
Hive), or locked immediately if the wallet already holds at least 10.001 CELL.
See [`MINER_QUICKSTART.md §3.3`](./MINER_QUICKSTART.md#33-choose-how-to-build-the-10-cell-bond).

---

## 5. Enroll on chain

```powershell
$env:QSDM_API_URL = 'https://api.qsdm.tech/api/v1'
.\bin\qsdmcli.exe enroll `
    --in              $env:USERPROFILE\.qsdm\wallet.json `
    --passphrase-file $env:USERPROFILE\.qsdm\pass.txt `
    --node-id         '<your-node-id>' `
    --gpu-uuid        'GPU-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx' `
    --hmac-key-file   $env:USERPROFILE\.qsdm\miner-hmac.hex `
    --nonce           <your-current-account-nonce>
# Add --bond-from-rewards to build the bond from rewards instead of
# locking 10 CELL now (--stake defaults to 10 CELL).

# Wait for inclusion (~1 block, 10 s on the live pilot network).
.\bin\qsdmcli.exe enrollment-status '<your-node-id>'
# Expect: phase: active
```

> **`gpu-uuid`** — read straight from `nvidia-smi`:
>
> ```powershell
> nvidia-smi --query-gpu=uuid --format=csv,noheader
> ```
>
> Submit it verbatim, prefix and all. The validator does a
> case-sensitive equality check against the registry record.

> **`node-id`** — an operator-chosen tag for this rig (Hive derives one
> automatically from the GPU UUID and your wallet). Use the same value
> for enrollment and mining.

---

## 6. Mine

```powershell
.\bin\qsdmminer-console.exe --protocol=v2 `
    --node-id       '<your-node-id>' `
    --gpu-uuid      'GPU-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx' `
    --gpu-arch      'ampere' `
    --gpu-name      'NVIDIA GeForce RTX 3050' `
    --compute-cap   '8.6' `
    --driver-ver    '576.28' `
    --cuda-version  '12.9' `
    --hmac-key-path $env:USERPROFILE\.qsdm\miner-hmac.hex `
    --operator-keystore        $env:USERPROFILE\.qsdm\wallet.json `
    --operator-passphrase-file $env:USERPROFILE\.qsdm\pass.txt `
    --address       '<your-reward-address>' `
    --validator     https://api.qsdm.tech
```

What you should see in the live panel within ~30 seconds:

  * `protocol = v2 (NVIDIA-locked)`
  * `attestation = nvidia-hmac-v1`
  * `gpu = NVIDIA GeForce RTX 3050 / Ampere / sm_86`
  * `accepted` rising steadily, `rejected (bad-version)` = 0,
    `rejected (attestation)` = 0
  * `cell_balance` updating after each accepted block reward

If `accepted` stays at 0 for more than a minute, jump to
[`MINER_QUICKSTART.md §6 (Troubleshooting)`](./MINER_QUICKSTART.md#6-troubleshooting).
Common cause #1 on a fresh consumer rig is host-clock drift — v2
attestations carry a 60-second freshness window; an out-of-sync clock
makes the validator reject every proof with `ReasonAttestation`.

---

## 7. (Optional) Run the kernel-level benchmark locally

Useful as a self-test before you enroll: confirms that *your* RTX
3050 build chain produces the same kernel-execution numbers the docs
quote.

```powershell
cd QSDM\source
$env:CGO_ENABLED = '1'
go test -tags cuda -c -o _mesh3d.test.exe ./pkg/mesh3d/
Start-Process .\_mesh3d.test.exe -NoNewWindow -Wait -ArgumentList `
    '-test.bench=BenchmarkMesh3DGPUVsCPU', `
    '-test.benchmem', `
    '-test.benchtime=1s', `
    '-test.run=^$'
```

Compare against [`MESH3D_GPU_BENCHMARK.md`](./MESH3D_GPU_BENCHMARK.md).
On the reference rig the validate-path crossover is at n≈1000 and the
n=4096 row hits 4.06× over CPU. A wildly different number on the
same silicon is a *build* problem (wrong arch fatbin, missing DLL),
not a mining-policy problem.

---

## 8. What changed in v0.3.2 vs. earlier drafts

The previous wording in `RELEASE_NOTES_v0.3.0.md` listed **NVIDIA
hardware + nvcc toolchain** as an external blocker. That was
misleading — both have been on a reference dev box since 2026-04-23,
and `pkg/mining/attest/archcheck` has been accepting Ampere proofs
since the v2 protocol shipped. The actual residual work is the live
end-to-end mining session covered by this cookbook (enroll → submit →
accepted). Tracking moved to an actionable item; [`RELEASE_NOTES_v0.3.0.md`
"Remaining external blockers"](../../../RELEASE_NOTES_v0.3.0.md#remaining-external-blockers)
no longer carries the entry.

The hash- and validate-kernel speedups in §0 come from
`pkg/mesh3d/mesh3d_gpu_bench_test.go`; the build instructions in §1–§2
come from `QSDM/scripts/build_kernels.ps1` (the `-Arch` flag and the
`-SetEnv` switch were both added in session 73 specifically for
operators with a single known arch). Re-run the benchmark on any rig
that changes the mesh3d kernel or the `GPUAccelerator` interface.
