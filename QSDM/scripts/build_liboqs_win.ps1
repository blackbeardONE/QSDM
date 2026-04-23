<#
.SYNOPSIS
  Build liboqs (ML-DSA / Dilithium via OQS) on Windows for local CGO
  development. Matches the Dockerfile.miner build but targets MinGW-w64
  so the resulting DLL is link-compatible with the MSYS2 Go toolchain
  the repo's CGO builds use.

.DESCRIPTION
  Sequence:
    1. Clone (or pull) liboqs into $installRoot\src if missing.
    2. Configure with CMake + Ninja, MinGW gcc, MSYS2 OpenSSL 3.
    3. Build --target oqs (skip tests, they need extra deps).
    4. Install to $installRoot\liboqs_install.
    5. Print the CGO_CFLAGS / CGO_LDFLAGS / PATH lines to paste. With
       -SetEnv they are also exported in the current PS session.

  Total build time: ~3-5 min on a modern x86_64 box.

  Why we need this:
    pkg/crypto/dilithium.go is an `import "C"` file that includes
    <oqs/oqs.h> unconditionally. pkg/mesh3d/mesh3d.go imports
    pkg/crypto for the Mesh3DValidator signature-verify path, so ANY
    cgo build that pulls pkg/mesh3d transitively fails to compile
    without oqs headers on the host. The CPU-only CI avoids this by
    setting CGO_ENABLED=0 (dilithium_stub.go kicks in). Dev boxes
    that want to run GPU paths or the full miner binary need the
    real oqs headers + libs, which is what this script provisions.

.PARAMETER InstallRoot
  Directory that will hold both the liboqs git checkout and the built
  install tree. Defaults to $env:LOCALAPPDATA\QSDM, so nothing lands
  inside the repo and nothing needs admin rights.

.PARAMETER SetEnv
  After a successful install, set CGO_CFLAGS, CGO_LDFLAGS, and PATH
  in the current PowerShell session so the next `go build -tags cuda`
  Just Works without the operator needing to copy-paste.

.PARAMETER Rebuild
  Wipe the build dir and re-run cmake from scratch. Useful after an
  OpenSSL upgrade in MSYS2 or after editing this script.

.EXAMPLE
  pwsh -File QSDM\scripts\build_liboqs_win.ps1 -SetEnv

.NOTES
  Prerequisites that MUST already be on PATH:
    - cmake    (>=3.18, tested on 3.31)
    - ninja    (MSYS2 mingw64 ships one at C:\msys64\mingw64\bin)
    - gcc      (MSYS2 mingw64 or MinGW-w64; the SAME gcc Go's cgo uses)
    - git

  The script fails with a descriptive error if any are missing.
#>
[CmdletBinding()]
param(
    [string]$InstallRoot = (Join-Path $env:LOCALAPPDATA 'QSDM'),
    [switch]$SetEnv,
    [switch]$Rebuild
)

$ErrorActionPreference = 'Stop'

# -----------------------------------------------------------------
# 0. Prereqs
# -----------------------------------------------------------------
function Require-Command {
    param([string]$Name, [string]$Hint)
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "required tool '$Name' not found on PATH. $Hint"
    }
}

Require-Command cmake "Install from https://cmake.org/download/"
Require-Command git   "Install from https://git-scm.com/download/win"
Require-Command ninja "Install MSYS2 and add C:\msys64\mingw64\bin to PATH, or: pacman -S mingw-w64-x86_64-ninja"
Require-Command gcc   "Install MSYS2 with mingw-w64-x86_64-gcc, or the standalone MinGW-w64 distribution"

$opensslRoot = 'C:\msys64\mingw64'
if (-not (Test-Path (Join-Path $opensslRoot 'include\openssl\crypto.h'))) {
    throw @"
OpenSSL 3 headers not found under $opensslRoot\include\openssl.
Install them via MSYS2:
  pacman -S mingw-w64-x86_64-openssl
(This script assumes the standard C:\msys64\mingw64 install layout.)
"@
}

New-Item -ItemType Directory -Force -Path $InstallRoot | Out-Null
$src     = Join-Path $InstallRoot 'liboqs'
$install = Join-Path $InstallRoot 'liboqs_install'
$build   = Join-Path $src 'build-win'

Write-Host ""
Write-Host "[build_liboqs] src     = $src"
Write-Host "[build_liboqs] install = $install"
Write-Host "[build_liboqs] openssl = $opensslRoot"

# -----------------------------------------------------------------
# 1. Clone / update liboqs source
# -----------------------------------------------------------------
if (-not (Test-Path $src)) {
    Write-Host "[build_liboqs] cloning liboqs..." -ForegroundColor Cyan
    git clone --depth 1 https://github.com/open-quantum-safe/liboqs.git $src
    if ($LASTEXITCODE -ne 0) { throw "git clone failed" }
} else {
    Write-Host "[build_liboqs] liboqs checkout exists; skipping clone" -ForegroundColor DarkGray
}

# -----------------------------------------------------------------
# 2. Configure + build + install
# -----------------------------------------------------------------
if ($Rebuild -and (Test-Path $build)) {
    Write-Host "[build_liboqs] -Rebuild: wiping $build" -ForegroundColor Yellow
    Remove-Item -Recurse -Force $build
}
New-Item -ItemType Directory -Force -Path $build | Out-Null

Push-Location $build
try {
    $cmakeArgs = @(
        '-G', 'Ninja'
        "-DCMAKE_INSTALL_PREFIX=$install"
        '-DCMAKE_BUILD_TYPE=Release'
        '-DBUILD_SHARED_LIBS=ON'
        '-DOQS_BUILD_ONLY_LIB=ON'
        '-DOQS_USE_OPENSSL=ON'
        "-DOPENSSL_ROOT_DIR=$opensslRoot"
        "-DOPENSSL_INCLUDE_DIR=$opensslRoot\include"
        "-DOPENSSL_CRYPTO_LIBRARY=$opensslRoot\lib\libcrypto.dll.a"
        "-DOPENSSL_SSL_LIBRARY=$opensslRoot\lib\libssl.dll.a"
        '-DCMAKE_C_COMPILER=gcc'
        '-DCMAKE_CXX_COMPILER=g++'
        # The AVX2 asm path fails under MinGW (known upstream issue).
        # Disable it; the portable C implementations are still fast
        # enough for ML-DSA dev+bench use cases.
        '-DOQS_DIST_BUILD=OFF'
        '-DOQS_OPT_TARGET=generic'
        '..'
    )

    Write-Host "[build_liboqs] configuring with cmake..." -ForegroundColor Cyan
    & cmake @cmakeArgs
    if ($LASTEXITCODE -ne 0) { throw "cmake configure failed" }

    Write-Host "[build_liboqs] building (ninja)..." -ForegroundColor Cyan
    $cores = [Environment]::ProcessorCount
    & cmake --build . --target oqs --parallel $cores
    if ($LASTEXITCODE -ne 0) { throw "ninja build failed" }

    Write-Host "[build_liboqs] installing to $install ..." -ForegroundColor Cyan
    & cmake --install .
    if ($LASTEXITCODE -ne 0) { throw "cmake --install failed" }
} finally {
    Pop-Location
}

# -----------------------------------------------------------------
# 3. Verify
# -----------------------------------------------------------------
$oqsDll = Join-Path $install 'bin\liboqs.dll'
$oqsHdr = Join-Path $install 'include\oqs\oqs.h'
$oqsLib = Join-Path $install 'lib\liboqs.dll.a'
foreach ($need in @($oqsDll, $oqsHdr, $oqsLib)) {
    if (-not (Test-Path $need)) {
        Write-Host "[build_liboqs] expected artifact missing: $need" -ForegroundColor Yellow
    }
}
if (-not (Test-Path $oqsHdr)) { throw "install succeeded but $oqsHdr is missing" }

Write-Host ""
Write-Host "[build_liboqs] install OK:" -ForegroundColor Green
Write-Host "    header: $oqsHdr"
Write-Host "    dll   : $oqsDll"
Write-Host "    implib: $oqsLib"

# -----------------------------------------------------------------
# 4. Emit CGO env
# -----------------------------------------------------------------
# Normalise to DOS 8.3 short paths so cgo's whitespace-tokenising
# directive parser never sees a literal space. Same trick as
# QSDM\scripts\build_kernels.ps1.
$fso = New-Object -ComObject Scripting.FileSystemObject
$installShort = $fso.GetFolder($install).ShortPath

$cflags  = "-I${installShort}/include -I${opensslRoot}/include"
$ldflags = "-L${installShort}/lib -L${opensslRoot}/lib -loqs -lssl -lcrypto"
$pathAdd = "${install}\bin;${opensslRoot}\bin"

Write-Host ""
Write-Host "[build_liboqs] paste these before 'go build -tags cuda':" -ForegroundColor Cyan
Write-Host "    `$env:CGO_ENABLED = '1'"
Write-Host "    `$env:CGO_CFLAGS  = '$cflags' + ' ' + `$env:CGO_CFLAGS"
Write-Host "    `$env:CGO_LDFLAGS = '$ldflags' + ' ' + `$env:CGO_LDFLAGS"
Write-Host "    `$env:PATH       = '$pathAdd;' + `$env:PATH"
Write-Host ""
Write-Host "    # then, if you also want CUDA:"
Write-Host "    .\QSDM\scripts\build_kernels.ps1 -SetEnv"
Write-Host ""

if ($SetEnv) {
    $env:CGO_ENABLED = '1'
    $existingC = if ($env:CGO_CFLAGS)  { ' ' + $env:CGO_CFLAGS  } else { '' }
    $existingL = if ($env:CGO_LDFLAGS) { ' ' + $env:CGO_LDFLAGS } else { '' }
    $env:CGO_CFLAGS  = $cflags  + $existingC
    $env:CGO_LDFLAGS = $ldflags + $existingL
    $env:PATH        = "$pathAdd;$env:PATH"
    Write-Host "[build_liboqs] CGO env set in current session." -ForegroundColor Green
}
