# Run qsdmplus.exe and show output in real-time
Write-Host "Starting QSDM with real-time output..." -ForegroundColor Cyan
Write-Host ""

# Set up environment
$env:PATH = "$PWD;C:\msys64\mingw64\bin;$env:PATH"
if (Test-Path "C:\CUDA\bin") {
    $env:PATH = "C:\CUDA\bin;$env:PATH"
}

# Set custom ports to avoid conflict
$env:DASHBOARD_PORT = "8082"
$env:LOG_VIEWER_PORT = "8083"

Write-Host "Environment configured:" -ForegroundColor Green
Write-Host "  Dashboard: http://localhost:$env:DASHBOARD_PORT" -ForegroundColor Gray
Write-Host "  Log Viewer: http://localhost:$env:LOG_VIEWER_PORT" -ForegroundColor Gray
Write-Host ""

# Check DLLs
if (Test-Path ".\libcrypto-3-x64.dll") {
    Write-Host "✅ OpenSSL DLLs found" -ForegroundColor Green
} else {
    Write-Host "⚠️  OpenSSL DLLs not found" -ForegroundColor Yellow
}

Write-Host ""
Write-Host "Starting qsdmplus.exe (output will appear below)..." -ForegroundColor Cyan
Write-Host "Press Ctrl+C to stop" -ForegroundColor Yellow
Write-Host ""
Write-Host "=" * 60 -ForegroundColor Gray
Write-Host ""

# Run directly to see output
& ".\qsdmplus.exe"

# Check exit code
if ($LASTEXITCODE -ne 0 -and $LASTEXITCODE -ne $null) {
    Write-Host ""
    Write-Host ("=" * 60) -ForegroundColor Gray
    Write-Host "Process exited with code: $LASTEXITCODE" -ForegroundColor Red
}

