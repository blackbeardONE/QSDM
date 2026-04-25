# Run qsdm.exe in foreground to see all output
Write-Host "Starting QSDM in foreground mode..." -ForegroundColor Cyan
Write-Host "This will show all output so we can see what's happening"
Write-Host ""

# Set up environment
$env:PATH = "$PWD;C:\msys64\mingw64\bin;$env:PATH"
if (Test-Path "C:\CUDA\bin") {
    $env:PATH = "C:\CUDA\bin;$env:PATH"
}

# Set custom ports
$env:DASHBOARD_PORT = "8082"
$env:LOG_VIEWER_PORT = "8083"

Write-Host "Configuration:" -ForegroundColor Green
Write-Host "  Dashboard port: $env:DASHBOARD_PORT" -ForegroundColor Gray
Write-Host "  Log viewer port: $env:LOG_VIEWER_PORT" -ForegroundColor Gray
Write-Host ""

# Check DLLs
if (Test-Path ".\libcrypto-3-x64.dll") {
    Write-Host "✅ OpenSSL DLLs found" -ForegroundColor Green
} else {
    Write-Host "❌ OpenSSL DLLs missing!" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "Starting qsdm.exe..." -ForegroundColor Cyan
Write-Host "=" * 70 -ForegroundColor Gray
Write-Host ""

# Run in foreground - this will show all output
& ".\qsdm.exe"

# If we get here, the process exited
Write-Host ""
Write-Host "=" * 70 -ForegroundColor Gray
if ($LASTEXITCODE -ne 0 -and $LASTEXITCODE -ne $null) {
    Write-Host "Process exited with code: $LASTEXITCODE" -ForegroundColor Red
    if ($LASTEXITCODE -eq -1073741571) {
        Write-Host ""
        Write-Host "This is STATUS_ACCESS_VIOLATION - usually means:" -ForegroundColor Yellow
        Write-Host "  - Missing or incompatible DLLs" -ForegroundColor Gray
        Write-Host "  - DLL loading failure" -ForegroundColor Gray
        Write-Host "  - Memory access issue" -ForegroundColor Gray
    }
}

