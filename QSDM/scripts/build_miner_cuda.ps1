param(
    [string]$Output = "",
    [string]$CudaPath = $env:CUDA_PATH,
    [string]$Version = "",
    [switch]$SkipRuntimeSelfTest
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$RepoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$Source = Join-Path $RepoRoot "QSDM\source\cmd\qsdm-miner-cuda-solver\main.cu"
$HiveNative = Join-Path $RepoRoot "apps\qsdm-hive\qsdm-hive-main\native\windows\x64"
$HiveRoot = Join-Path $RepoRoot "apps\qsdm-hive\qsdm-hive-main"
$VsWhere = "C:\Program Files (x86)\Microsoft Visual Studio\Installer\vswhere.exe"

if (-not $Version) {
    $Version = (Get-Content -Raw (Join-Path $HiveRoot 'release\app\package.json') | ConvertFrom-Json).version
}
if ($Version -notmatch '^\d+\.\d+\.\d+$') {
    throw 'Version must use MAJOR.MINOR.PATCH format.'
}

if (-not $CudaPath) {
    $CudaPath = Get-ChildItem "C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA" -Directory -ErrorAction SilentlyContinue |
        Sort-Object Name -Descending |
        Select-Object -First 1 -ExpandProperty FullName
}
$Nvcc = if ($CudaPath) { Join-Path $CudaPath "bin\nvcc.exe" } else { "" }
if (-not $Nvcc -or -not (Test-Path -LiteralPath $Nvcc)) {
    throw "NVIDIA CUDA Toolkit with nvcc was not found."
}
if (-not (Test-Path -LiteralPath $VsWhere)) {
    throw "Visual Studio Build Tools discovery was not found at $VsWhere"
}
$VisualStudio = (& $VsWhere -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath).Trim()
$Compiler = Get-ChildItem (Join-Path $VisualStudio "VC\Tools\MSVC") -Recurse -Filter cl.exe |
    Where-Object { $_.FullName.EndsWith("\Hostx64\x64\cl.exe", [System.StringComparison]::OrdinalIgnoreCase) } |
    Select-Object -First 1
if ($null -eq $Compiler) {
    throw "Visual C++ x64 compiler is required by nvcc."
}

if (-not $Output) {
    New-Item -ItemType Directory -Force -Path $HiveNative | Out-Null
    $Destination = Join-Path $HiveNative "qsdm-miner-cuda-solver.exe"
    $BuildDirectory = Join-Path $RepoRoot ".cache\miner-cuda-release"
    New-Item -ItemType Directory -Force -Path $BuildDirectory | Out-Null
    $Output = Join-Path $BuildDirectory "qsdm-miner-cuda-solver.exe"
} else {
    $Output = [System.IO.Path]::GetFullPath($Output)
    $Destination = $Output
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Output) | Out-Null
    $BuildDirectory = Split-Path -Parent $Output
}

$ResourceObject = Join-Path $BuildDirectory 'qsdm-miner-cuda-solver-version.obj'
& (Join-Path $PSScriptRoot 'build_windows_version_resource.ps1') `
    -ProductVersion $Version `
    -FileVersion $Version `
    -ProductName 'QSDM Hive' `
    -FileDescription 'QSDM CUDA Miner Solver' `
    -InternalName 'qsdm-miner-cuda-solver' `
    -OriginalFilename 'qsdm-miner-cuda-solver.exe' `
    -IconPath (Join-Path $HiveRoot 'assets\icon.ico') `
    -OutputPath $ResourceObject

& $Nvcc `
    -O3 `
    -std=c++17 `
    -gencode arch=compute_75,code=sm_75 `
    -gencode arch=compute_86,code=sm_86 `
    -gencode arch=compute_89,code=sm_89 `
    -gencode arch=compute_90,code=sm_90 `
    -allow-unsupported-compiler `
    -ccbin $Compiler.DirectoryName `
    $Source `
    $ResourceObject `
    -o $Output
if ($LASTEXITCODE -ne 0) {
    throw "CUDA miner solver build failed with exit code $LASTEXITCODE"
}

if (-not $SkipRuntimeSelfTest) {
    & $Output --self-test
    if ($LASTEXITCODE -ne 0) {
        throw "CUDA miner solver self-test failed with exit code $LASTEXITCODE"
    }
}

if ($Destination -ne $Output) {
    Copy-Item -LiteralPath $Output -Destination $Destination -Force
}

Write-Host "CUDA miner solver built and verified: $Destination"
