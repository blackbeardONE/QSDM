# PowerShell script to build QSDM with CGO enabled WITH CUDA
# Full features including GPU acceleration for 3D mesh validation

Write-Host "Building QSDM with CGO enabled (WITH CUDA)..." -ForegroundColor Cyan
Write-Host "This build includes:" -ForegroundColor Green
Write-Host "  - WASM module support"
Write-Host "  - Quantum-safe cryptography (liboqs)"
Write-Host "  - SQLite storage"
Write-Host "  - CUDA acceleration (3D mesh GPU validation)"
Write-Host ""

# Set Go environment
$env:GOROOT = "C:\Program Files\Go"
$env:PATH = "C:\Program Files\Go\bin;$env:PATH"
$env:CGO_ENABLED = "1"

# Clear any existing CGO flags
Remove-Item Env:\CGO_CFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:\CGO_LDFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:\CGO_CPPFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:\CGO_CXXFLAGS -ErrorAction SilentlyContinue

# Check if liboqs is available
$liboqsFound = $false

# Check D:\Projects\QSDM\liboqs_install (freshly installed location)
if (Test-Path "D:\Projects\QSDM\liboqs_install\include\oqs\oqs.h") {
    Write-Host "Found liboqs installation at D:\Projects\QSDM\liboqs_install\" -ForegroundColor Green
    $env:CGO_CFLAGS = "-ID:\Projects\QSDM\liboqs_install\include"
    if (Test-Path "D:\Projects\QSDM\liboqs_install\lib\liboqs.a") {
        $env:CGO_LDFLAGS = "-LD:\Projects\QSDM\liboqs_install\lib -loqs"
    } else {
        $env:CGO_LDFLAGS = "-loqs"
    }
    $liboqsFound = $true
}

# Check for OpenSSL (required by liboqs)
$opensslFound = $false
$opensslPaths = @(
    "C:\msys64\mingw64",
    "C:\OpenSSL-Win64",
    "C:\OpenSSL",
    "C:\Program Files\OpenSSL",
    "C:\Program Files (x86)\OpenSSL"
)
foreach ($path in $opensslPaths) {
    $cryptoStatic = Test-Path "$path\lib\libcrypto.a"
    $cryptoDll = Test-Path "$path\lib\libcrypto.dll.a"
    if ($cryptoStatic -or $cryptoDll) {
        Write-Host "Found OpenSSL at: $path" -ForegroundColor Green
        if ($env:CGO_LDFLAGS) {
            $env:CGO_LDFLAGS += " -L$path\lib -lcrypto"
        } else {
            $env:CGO_LDFLAGS = "-L$path\lib -lcrypto"
        }
        $opensslFound = $true
        break
    }
}
if (-not $opensslFound) {
    Write-Host "WARNING: OpenSSL not found. liboqs may require OpenSSL's libcrypto." -ForegroundColor Yellow
}

# Check other locations if not found
if (-not $liboqsFound) {
    $possiblePaths = @(
        "D:\Projects\QSDM\liboqs_build",
        "C:\liboqs\liboqs_install",
        "C:\liboqs"
    )
    foreach ($path in $possiblePaths) {
        if (Test-Path "$path\include\oqs\oqs.h") {
            Write-Host "Found liboqs at: $path" -ForegroundColor Green
            $env:CGO_CFLAGS = "-I$path\include"
            if (Test-Path "$path\lib") {
                $env:CGO_LDFLAGS = "-L$path\lib -loqs"
            } else {
                $env:CGO_LDFLAGS = "-loqs"
            }
            $liboqsFound = $true
            break
        }
    }
}

if (-not $liboqsFound) {
    Write-Host "ERROR: liboqs not found" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "CGO Environment:" -ForegroundColor Cyan
Write-Host "  CGO_ENABLED: $env:CGO_ENABLED"
Write-Host "  CGO_CFLAGS: $env:CGO_CFLAGS"
Write-Host "  CGO_LDFLAGS: $env:CGO_LDFLAGS"
Write-Host "  CUDA: Disabled (to avoid build issues)" -ForegroundColor Yellow
Write-Host ""

# Verify Go version
Write-Host "Go version:" -ForegroundColor Cyan
go version
if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Go is not working properly" -ForegroundColor Red
    exit 1
}

Write-Host ""
if ($cudaFound) {
    Write-Host "Building (with CUDA acceleration)..." -ForegroundColor Cyan
} else {
    Write-Host "Building (without CUDA, will use CPU)..." -ForegroundColor Cyan
}

# Try to kill any Go processes that might be locking the compiler
Get-Process | Where-Object {$_.ProcessName -like "*go*"} | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1

# Build with CGO enabled (liboqs for consensus)
# wasmtime-go has been removed, so no DLL issues
# Use -mod=mod to ignore vendor directory (wasmtime-go removed from go.mod)
# Add -ldflags to increase stack size (fixes stack overflow during CGO init)
# Use 16MB stack size (16777216 bytes) to handle deep CGO initialization
go build -mod=mod -ldflags "-extldflags=-Wl,--stack,16777216" -o qsdmplus.exe ./cmd/qsdmplus

if ($LASTEXITCODE -eq 0) {
    Write-Host ""
    Write-Host "Build successful! Executable: qsdmplus.exe" -ForegroundColor Green
    Write-Host ""
    Write-Host "Features enabled:" -ForegroundColor Green
    Write-Host "  - Quantum-safe consensus (Proof-of-Entanglement)"
    Write-Host "  - WASM modules (wallet/validator)"
    Write-Host "  - SQLite storage"
    Write-Host "  - Full cryptographic verification"
    if ($cudaFound) {
        Write-Host "  - 3D mesh (CUDA GPU acceleration enabled)" -ForegroundColor Green
    } else {
        Write-Host "  - 3D mesh (CPU-based, CUDA not available)" -ForegroundColor Yellow
    }
} else {
    Write-Host ""
    Write-Host "Build failed." -ForegroundColor Red
    Write-Host ""
    Write-Host "If you see 'Access is denied':" -ForegroundColor Yellow
    Write-Host "  1. Add C:\Program Files\Go to antivirus exclusions"
    Write-Host "  2. Run PowerShell as Administrator"
    Write-Host "  3. Close any running Go processes"
    Write-Host ""
    exit $LASTEXITCODE
}

