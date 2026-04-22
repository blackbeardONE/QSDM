# Simple script to start qsdmplus.exe and keep it running
Write-Host "Starting QSDM node..." -ForegroundColor Cyan
Write-Host ""

# Set PATH to include OpenSSL DLLs FIRST (before anything else)
# This is critical - liboqs (even if statically linked) depends on OpenSSL
$env:PATH = "$PWD;C:\msys64\mingw64\bin;$env:PATH"

# Add CUDA bin to PATH if available
if (Test-Path "C:\CUDA\bin") {
    $env:PATH = "C:\CUDA\bin;$env:PATH"
    Write-Host "  ✅ Added CUDA path to PATH" -ForegroundColor Green
}

# Check for OpenSSL DLLs
if (Test-Path ".\libcrypto-3-x64.dll") {
    Write-Host "  ✅ Found OpenSSL DLLs in current directory" -ForegroundColor Green
} elseif (Test-Path "C:\msys64\mingw64\bin\libcrypto-3-x64.dll") {
    Write-Host "  ✅ OpenSSL DLLs available in PATH" -ForegroundColor Green
} else {
    Write-Host "  ⚠️  OpenSSL DLLs not found - may cause issues" -ForegroundColor Yellow
}

Write-Host ""
Write-Host "Running qsdmplus.exe..." -ForegroundColor Cyan
Write-Host "Press Ctrl+C to stop" -ForegroundColor Yellow
Write-Host ""

# Run the executable in foreground so we can see output
& ".\qsdmplus.exe"

# Check exit code
if ($LASTEXITCODE -ne 0 -and $LASTEXITCODE -ne $null) {
    Write-Host ""
    Write-Host "Process exited with code: $LASTEXITCODE" -ForegroundColor Red
    Write-Host ""
    Write-Host "If the application crashed:" -ForegroundColor Yellow
    Write-Host "  1. Check Event Viewer: Windows Logs > Application" -ForegroundColor Cyan
    Write-Host "  2. Check for error messages above" -ForegroundColor Cyan
    Write-Host "  3. Verify DLLs are available" -ForegroundColor Cyan
}

