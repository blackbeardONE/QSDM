<#
.SYNOPSIS
  Build the mesh3d CUDA kernel DLL on Windows, then expose its location
  to the QSDM Go build via CGO_CFLAGS / CGO_LDFLAGS.

.DESCRIPTION
  nvcc on Windows requires the MSVC host compiler (cl.exe). This script:

    1. Locates the latest installed MSVC toolchain via vswhere.exe
       (shipped with every VS 2017+/Build Tools install). If it's not
       found we print the exact `vs_buildtools.exe` command to install
       only the bits we need (about 2.2 GB) and exit with code 2.
    2. Locates CUDA via `$env:CUDA_PATH` (set by the NVIDIA installer)
       and verifies nvcc.exe exists under it.
    3. Imports MSVC's `vcvars64.bat` into the current PowerShell session
       so `cl.exe` lands on PATH for `nvcc`.
    4. Invokes nvcc to produce mesh3d_kernels.dll alongside the .cu
       source, then copies it to
           QSDM/source/pkg/mesh3d/kernels/mesh3d_kernels.dll
           QSDM/source/mesh3d_kernels.dll    (next to the would-be Go binary)
       so CGO builds and `go test` both find it.
    5. Prints the CGO_CFLAGS / CGO_LDFLAGS lines to paste (or dot-source)
       before `go build -tags cuda`. With -SetEnv it also exports them
       into the current PS session so a subsequent `go test ...` in the
       same terminal Just Works.

.PARAMETER Arch
  GPU compute capability to target (default `sm_86` = RTX 3050/3080/3090
  Ampere). Change to `sm_75` for Turing (T4, RTX 20xx), `sm_89` for Ada
  Lovelace (RTX 40xx), `sm_90` for Hopper. Multi-arch fatbins are
  supported by passing a comma list, e.g. `-Arch 'sm_75,sm_86,sm_89'`.

.PARAMETER SetEnv
  Also set CGO_CFLAGS / CGO_LDFLAGS / PATH in the current PowerShell
  session so a follow-up `go build -tags cuda` in the same terminal
  inherits them.

.PARAMETER SkipIfExists
  Skip the nvcc step when the DLL already exists and is newer than the
  .cu source. Useful when iterating on the Go side.

.EXAMPLE
  pwsh -File QSDM\scripts\build_kernels.ps1 -SetEnv

.EXAMPLE
  # Multi-arch fatbin for the common NVIDIA lineup we support
  .\QSDM\scripts\build_kernels.ps1 -Arch 'sm_75,sm_86,sm_89' -SetEnv

.NOTES
  vcvars64.bat pollutes the environment with 100+ vars that only matter
  while nvcc runs. We scope that import to a Start-Process sub-shell
  UNLESS -SetEnv is passed, in which case the caller explicitly asked
  for them in the current session.
#>
[CmdletBinding()]
param(
    [string]$Arch       = 'sm_86',
    [switch]$SetEnv,
    [switch]$SkipIfExists
)

$ErrorActionPreference = 'Stop'
$scriptRoot  = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot    = Split-Path -Parent (Split-Path -Parent $scriptRoot)
$kernelsDir  = Join-Path $repoRoot 'QSDM\source\pkg\mesh3d\kernels'
$cuFile      = Join-Path $kernelsDir 'sha256_validate.cu'
$dllOut      = Join-Path $kernelsDir 'mesh3d_kernels.dll'
$libOut      = Join-Path $kernelsDir 'mesh3d_kernels.lib'  # import lib for CGO linking
$sourceDir   = Join-Path $repoRoot 'QSDM\source'
$dllMirror   = Join-Path $sourceDir 'mesh3d_kernels.dll'

Write-Host ""
Write-Host "[build_kernels] QSDM mesh3d CUDA kernel builder" -ForegroundColor Cyan
Write-Host "[build_kernels] repo=$repoRoot"
Write-Host "[build_kernels] .cu =$cuFile"

if (-not (Test-Path $cuFile)) {
    throw "kernel source not found at $cuFile"
}

# -----------------------------------------------------------------
# 1. Locate CUDA
# -----------------------------------------------------------------
$cudaPath = $env:CUDA_PATH
if (-not $cudaPath -or -not (Test-Path $cudaPath)) {
    # Fall back: pick the highest version under the canonical install dir.
    $root = 'C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA'
    if (Test-Path $root) {
        $pick = Get-ChildItem -Directory $root |
            Where-Object Name -Match '^v\d+\.\d+$' |
            Sort-Object { [version]($_.Name.Substring(1)) } -Descending |
            Select-Object -First 1
        if ($pick) { $cudaPath = $pick.FullName }
    }
}
if (-not $cudaPath -or -not (Test-Path (Join-Path $cudaPath 'bin\nvcc.exe'))) {
    throw @"
could not find the CUDA Toolkit.
    Expected ${env:CUDA_PATH}\bin\nvcc.exe or a versioned dir under
    C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA\.
    Install it from https://developer.nvidia.com/cuda-downloads then
    re-run this script. Minimum supported version: CUDA 11.x.
"@
}
Write-Host "[build_kernels] CUDA=$cudaPath" -ForegroundColor Green
$nvcc = Join-Path $cudaPath 'bin\nvcc.exe'

# -----------------------------------------------------------------
# 2. Locate MSVC via vswhere
# -----------------------------------------------------------------
$vswhere = 'C:\Program Files (x86)\Microsoft Visual Studio\Installer\vswhere.exe'
if (-not (Test-Path $vswhere)) {
    Write-Host ""
    Write-Host "[build_kernels] MSVC Build Tools are NOT installed." -ForegroundColor Yellow
    Write-Host ""
    Write-Host "   The NVIDIA CUDA compiler (nvcc) on Windows requires"
    Write-Host "   the Microsoft C/C++ compiler (cl.exe). Install it with:"
    Write-Host ""
    Write-Host "     # (run as Administrator)" -ForegroundColor Gray
    Write-Host "     cd `$env:TEMP"
    Write-Host "     Invoke-WebRequest https://aka.ms/vs/17/release/vs_buildtools.exe -OutFile vs_buildtools.exe"
    Write-Host "     .\vs_buildtools.exe --quiet --wait --norestart --nocache \"
    Write-Host "         --add Microsoft.VisualStudio.Workload.VCTools \"
    Write-Host "         --add Microsoft.VisualStudio.Component.VC.Tools.x86.x64 \"
    Write-Host "         --add Microsoft.VisualStudio.Component.Windows11SDK.22621"
    Write-Host ""
    Write-Host "   The install is ~2.2 GB on disk and takes 5–15 min."
    Write-Host "   Re-run this script once it finishes."
    Write-Host ""
    exit 2
}
$vsInstall = & $vswhere -latest -products * `
    -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 `
    -property installationPath 2>$null
if (-not $vsInstall) {
    # vswhere is present but no VC toolset found → Build Tools installed
    # without the VCTools workload; same install command as above.
    Write-Host "[build_kernels] MSVC C++ toolset missing; re-run the vs_buildtools install with --add Microsoft.VisualStudio.Workload.VCTools" -ForegroundColor Yellow
    exit 2
}
$vcvars = Join-Path $vsInstall 'VC\Auxiliary\Build\vcvars64.bat'
if (-not (Test-Path $vcvars)) {
    throw "vcvars64.bat not found under $vsInstall"
}
Write-Host "[build_kernels] MSVC=$vsInstall" -ForegroundColor Green

# -----------------------------------------------------------------
# 3. Skip if already up to date
# -----------------------------------------------------------------
$skipBuild = $false
if ($SkipIfExists -and (Test-Path $dllOut)) {
    $srcTime = (Get-Item $cuFile).LastWriteTime
    $dllTime = (Get-Item $dllOut).LastWriteTime
    if ($dllTime -gt $srcTime) {
        Write-Host "[build_kernels] DLL up to date ($dllOut); -SkipIfExists honoured" -ForegroundColor DarkGray
        $skipBuild = $true
    }
}

# -----------------------------------------------------------------
# 4. Build: source vcvars64 then nvcc, inside one cmd.exe
# -----------------------------------------------------------------
if (-not $skipBuild) {
    Write-Host "[build_kernels] compiling (arch=$Arch) ..." -ForegroundColor Cyan
    Push-Location $kernelsDir
    try {
        # Split a comma-list like "sm_75,sm_86" into multiple -gencode
        # pairs so nvcc emits a fatbin that runs on every listed arch.
        $gencode = @()
        foreach ($a in $Arch.Split(',')) {
            $trim = $a.Trim()
            if ($trim -eq '') { continue }
            $num = $trim -replace '^sm_',''
            $gencode += "-gencode=arch=compute_${num},code=sm_${num}"
        }
        $gencodeStr = $gencode -join ' '

        # nvcc -shared produces mesh3d_kernels.dll plus the matching
        # .exp/.lib import library next to it, which CGO needs to
        # link. `-Xcompiler /MD` picks the DLL C runtime so we don't
        # link a debug CRT by accident.
        $cmdline = @(
            'call "' + $vcvars + '" >nul 2>&1'
            ('"' + $nvcc + '" -shared ' +
                '-o mesh3d_kernels.dll sha256_validate.cu ' +
                $gencodeStr + ' ' +
                '-Xcompiler "/MD" -Wno-deprecated-gpu-targets')
        ) -join ' && '

        $proc = Start-Process -FilePath 'cmd.exe' `
            -ArgumentList @('/c', $cmdline) `
            -NoNewWindow -PassThru -Wait
        if ($proc.ExitCode -ne 0) {
            throw "nvcc failed (exit=$($proc.ExitCode))"
        }
    } finally {
        Pop-Location
    }

    if (-not (Test-Path $dllOut)) {
        throw "expected $dllOut after build but it was not produced"
    }
    Write-Host "[build_kernels] produced $dllOut" -ForegroundColor Green

    # Copy to the go-source root so `go test` and the built binary can
    # load the DLL from its own working directory without the user
    # having to twiddle PATH.
    Copy-Item -Force $dllOut $dllMirror
    Write-Host "[build_kernels] mirrored -> $dllMirror" -ForegroundColor Green

    # Regenerate a MinGW-compatible import library. nvcc emits an
    # MSVC-format mesh3d_kernels.lib next to the DLL, but MinGW ld
    # links against libmesh3d_kernels.{a,dll.a}. Without this step
    # `go build -tags cuda` fails with
    #   undefined reference to `mesh3d_hash_cells'
    # because -lmesh3d_kernels finds the wrong archive format.
    $gendef  = Get-Command gendef.exe  -ErrorAction SilentlyContinue
    $dlltool = Get-Command dlltool.exe -ErrorAction SilentlyContinue
    if ($gendef -and $dlltool) {
        Push-Location $kernelsDir
        try {
            # gendef writes mesh3d_kernels.def; dlltool turns that into
            # libmesh3d_kernels.dll.a. Tolerate pre-existing files by
            # overwriting unconditionally.
            & $gendef.Source  mesh3d_kernels.dll | Out-Null
            & $dlltool.Source --input-def mesh3d_kernels.def `
                --output-lib libmesh3d_kernels.dll.a
            if (Test-Path 'libmesh3d_kernels.dll.a') {
                Write-Host "[build_kernels] generated libmesh3d_kernels.dll.a for MinGW/CGO" -ForegroundColor Green
            } else {
                Write-Host "[build_kernels] WARN: dlltool did not produce libmesh3d_kernels.dll.a" -ForegroundColor Yellow
            }
        } finally {
            Pop-Location
        }
    } else {
        Write-Host "[build_kernels] WARN: gendef / dlltool missing; MinGW CGO links will fail. Install MSYS2 mingw-w64-x86_64-tools-git." -ForegroundColor Yellow
    }
}

# -----------------------------------------------------------------
# 5. Emit CGO env (and set it in-session when -SetEnv)
# -----------------------------------------------------------------
# cgo on Windows hates spaces in paths. The reliable fix is the DOS
# 8.3 short-name form (PROGRA~1, etc.). Resolve it via the FileSystem
# COM object rather than hand-munging, so it keeps working if the
# 8.3 suffix ever shifts to ~2.
$fso = New-Object -ComObject Scripting.FileSystemObject
$cudaShort = $fso.GetFolder($cudaPath).ShortPath
$cflags  = "-I${cudaShort}/include"
$ldflags = "-L${cudaShort}/lib/x64 -L`"${kernelsDir}`""

Write-Host ""
Write-Host "[build_kernels] ready. For a CGO build, run:" -ForegroundColor Cyan
Write-Host "    `$env:CGO_CFLAGS  = '$cflags'"
Write-Host "    `$env:CGO_LDFLAGS = '$ldflags'"
Write-Host "    `$env:PATH        = '$cudaPath\bin;' + `$env:PATH"
Write-Host "    cd QSDM\source"
Write-Host "    go test -tags cuda -bench 'BenchmarkMesh3DGPUVsCPU' -benchmem -run '^`$' ./pkg/mesh3d/..."
Write-Host ""

if ($SetEnv) {
    $env:CGO_CFLAGS  = $cflags
    $env:CGO_LDFLAGS = $ldflags
    $env:PATH        = "$cudaPath\bin;$env:PATH"
    Write-Host "[build_kernels] CGO_CFLAGS / CGO_LDFLAGS / PATH set in current session." -ForegroundColor Green
}
