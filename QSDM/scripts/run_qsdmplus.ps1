# PowerShell script to run QSDM with proper environment setup

Write-Host "Starting QSDM..." -ForegroundColor Cyan
Write-Host ""

# Add OpenSSL to PATH
$env:PATH = "C:\msys64\mingw64\bin;$env:PATH"

# Verify OpenSSL DLL is available
if (Test-Path "C:\msys64\mingw64\bin\libcrypto-3-x64.dll") {
    Write-Host "✓ OpenSSL DLL found" -ForegroundColor Green
} else {
    Write-Host "⚠ OpenSSL DLL not found - application may fail" -ForegroundColor Yellow
}

Write-Host ""
Write-Host "Running qsdmplus.exe..." -ForegroundColor Cyan
Write-Host "Press Ctrl+C to stop" -ForegroundColor Gray
Write-Host ""

# Run the application with error capture
$ErrorActionPreference = 'Continue'
try {
    # Run in foreground and capture output
    & .\qsdmplus.exe 2>&1 | ForEach-Object {
        Write-Host $_
    }
} catch {
    Write-Host "Error running application: $_" -ForegroundColor Red
    Write-Host "Exit code: $LASTEXITCODE" -ForegroundColor Red
    Write-Host ""
    Write-Host "Check qsdmplus.log for details" -ForegroundColor Yellow
}

