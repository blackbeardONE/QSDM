# Start QSDM with custom ports to avoid conflicts
Write-Host "Starting QSDM with custom ports..." -ForegroundColor Cyan
Write-Host ""

# Set custom ports via environment variables
# Dashboard on 8082, Log viewer on 8083
$env:DASHBOARD_PORT = "8082"
$env:LOG_VIEWER_PORT = "8083"

Write-Host "Using ports:" -ForegroundColor Cyan
Write-Host "  Dashboard: 8082" -ForegroundColor Gray
Write-Host "  Log Viewer: 8083" -ForegroundColor Gray
Write-Host ""

# Set PATH to include OpenSSL DLLs
$env:PATH = "$PWD;C:\msys64\mingw64\bin;$env:PATH"

# Add CUDA bin to PATH if available
if (Test-Path "C:\CUDA\bin") {
    $env:PATH = "C:\CUDA\bin;$env:PATH"
}

# Check for OpenSSL DLLs
if (Test-Path ".\libcrypto-3-x64.dll") {
    Write-Host "✅ Found OpenSSL DLLs" -ForegroundColor Green
} else {
    Write-Host "⚠️  OpenSSL DLLs not found in current directory" -ForegroundColor Yellow
}

Write-Host ""
Write-Host "Starting qsdm.exe..." -ForegroundColor Cyan
Write-Host "Press Ctrl+C to stop" -ForegroundColor Yellow
Write-Host ""

# Run the executable
& ".\qsdm.exe"

