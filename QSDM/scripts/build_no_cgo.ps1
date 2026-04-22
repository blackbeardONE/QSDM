# PowerShell script to build QSDM without CGO dependencies
# This builds a version without WASM, quantum crypto, or CUDA support

Write-Host "Building QSDM without CGO dependencies..." -ForegroundColor Cyan
Write-Host "This version will have limited functionality but will run without external C libraries." -ForegroundColor Yellow

# Set Go environment
$env:GOROOT = "C:\Program Files\Go"
$env:PATH = "C:\Program Files\Go\bin;$env:PATH"
$env:CGO_ENABLED = "0"

# Build
go build -o qsdmplus.exe ./cmd/qsdmplus

if ($LASTEXITCODE -eq 0) {
    Write-Host ""
    Write-Host "Build successful! Executable: qsdmplus.exe" -ForegroundColor Green
    Write-Host ""
    Write-Host "Note: This build does not include:" -ForegroundColor Yellow
    Write-Host "  - WASM module support"
    Write-Host "  - Quantum-safe cryptography (liboqs)"
    Write-Host "  - CUDA acceleration"
    Write-Host "  - SQLite storage (uses file storage instead)"
    Write-Host ""
    Write-Host "The dashboard and core functionality will still work." -ForegroundColor Green
} else {
    Write-Host ""
    Write-Host "Build failed. Check errors above." -ForegroundColor Red
    exit $LASTEXITCODE
}

