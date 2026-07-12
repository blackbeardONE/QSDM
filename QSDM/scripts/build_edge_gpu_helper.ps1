param(
    [string]$Output = "",
    [string]$CudaPath = $env:CUDA_PATH,
    [string]$Version = "",
    [switch]$SkipRuntimeSelfTest
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$source = Join-Path $repoRoot 'QSDM\source\cmd\qsdm-edge-gpu-helper\main.cu'
$hiveRoot = Join-Path $repoRoot 'apps\qsdm-hive\qsdm-hive-main'
$hiveNative = Join-Path $hiveRoot 'native\windows\x64'
$vsWhere = 'C:\Program Files (x86)\Microsoft Visual Studio\Installer\vswhere.exe'

if (-not $Version) {
    $Version = (Get-Content -Raw (Join-Path $hiveRoot 'release\app\package.json') | ConvertFrom-Json).version
}
if ($Version -notmatch '^\d+\.\d+\.\d+$') {
    throw 'Version must use MAJOR.MINOR.PATCH format.'
}
if (-not $CudaPath) {
    $CudaPath = Get-ChildItem 'C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA' -Directory -ErrorAction SilentlyContinue |
        Sort-Object Name -Descending |
        Select-Object -First 1 -ExpandProperty FullName
}
$nvcc = if ($CudaPath) { Join-Path $CudaPath 'bin\nvcc.exe' } else { '' }
if (-not $nvcc -or -not (Test-Path -LiteralPath $nvcc -PathType Leaf)) {
    throw 'NVIDIA CUDA Toolkit with nvcc was not found.'
}
if (-not (Test-Path -LiteralPath $vsWhere -PathType Leaf)) {
    throw "Visual Studio Build Tools discovery was not found at $vsWhere"
}
$visualStudio = (& $vsWhere -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath).Trim()
$compiler = Get-ChildItem (Join-Path $visualStudio 'VC\Tools\MSVC') -Recurse -Filter cl.exe |
    Where-Object { $_.FullName.EndsWith('\Hostx64\x64\cl.exe', [StringComparison]::OrdinalIgnoreCase) } |
    Select-Object -First 1
if ($null -eq $compiler) {
    throw 'Visual C++ x64 compiler is required by nvcc.'
}

if (-not $Output) {
    New-Item -ItemType Directory -Force -Path $hiveNative | Out-Null
    $destination = Join-Path $hiveNative 'qsdm-edge-gpu-helper.exe'
    $buildDirectory = Join-Path $repoRoot '.cache\edge-gpu-helper-release'
    New-Item -ItemType Directory -Force -Path $buildDirectory | Out-Null
    $Output = Join-Path $buildDirectory 'qsdm-edge-gpu-helper.exe'
} else {
    $Output = [IO.Path]::GetFullPath($Output)
    $destination = $Output
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Output) | Out-Null
    $buildDirectory = Split-Path -Parent $Output
}

$resourceObject = Join-Path $buildDirectory 'qsdm-edge-gpu-helper-version.obj'
& (Join-Path $PSScriptRoot 'build_windows_version_resource.ps1') `
    -ProductVersion $Version `
    -FileVersion $Version `
    -ProductName 'QSDM Hive' `
    -FileDescription 'QSDM Edge GPU Helper' `
    -InternalName 'qsdm-edge-gpu-helper' `
    -OriginalFilename 'qsdm-edge-gpu-helper.exe' `
    -IconPath (Join-Path $hiveRoot 'assets\icon.ico') `
    -OutputPath $resourceObject

& $nvcc `
    -O3 `
    -std=c++17 `
    -arch=sm_75 `
    -allow-unsupported-compiler `
    -ccbin $compiler.DirectoryName `
    $source `
    $resourceObject `
    -o $Output
if ($LASTEXITCODE -ne 0) {
    throw "CUDA edge helper build failed with exit code $LASTEXITCODE"
}

if (-not $SkipRuntimeSelfTest) {
    $result = & $Output --seed ('00' * 32) --units 1024 --json | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or -not $result.gpu_name -or $result.units -ne 1024) {
        throw 'QSDM Edge GPU Helper runtime self-test failed.'
    }
}

if ($destination -ne $Output) {
    Copy-Item -LiteralPath $Output -Destination $destination -Force
}

Write-Host "QSDM Edge GPU Helper built: $destination"
