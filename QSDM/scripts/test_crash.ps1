# Test script to capture crash details
Write-Host "Testing qsdmplus.exe startup..." -ForegroundColor Cyan
Write-Host ""

# Set up environment
$env:PATH = "$PWD;C:\msys64\mingw64\bin;$env:PATH"
if (Test-Path "C:\CUDA\bin") {
    $env:PATH = "C:\CUDA\bin;$env:PATH"
}

# Set custom ports
$env:DASHBOARD_PORT = "8082"
$env:LOG_VIEWER_PORT = "8083"

Write-Host "Environment:" -ForegroundColor Cyan
Write-Host "  PATH includes: $PWD" -ForegroundColor Gray
Write-Host "  Dashboard port: $env:DASHBOARD_PORT" -ForegroundColor Gray
Write-Host "  Log viewer port: $env:LOG_VIEWER_PORT" -ForegroundColor Gray
Write-Host ""

# Check DLLs
Write-Host "Checking DLLs..." -ForegroundColor Cyan
if (Test-Path ".\libcrypto-3-x64.dll") {
    Write-Host "  ✅ libcrypto-3-x64.dll" -ForegroundColor Green
} else {
    Write-Host "  ❌ libcrypto-3-x64.dll missing" -ForegroundColor Red
}

if (Test-Path ".\libssl-3-x64.dll") {
    Write-Host "  ✅ libssl-3-x64.dll" -ForegroundColor Green
} else {
    Write-Host "  ❌ libssl-3-x64.dll missing" -ForegroundColor Red
}

Write-Host ""
Write-Host "Starting qsdmplus.exe and capturing output..." -ForegroundColor Cyan
Write-Host ""

# Start process with output redirection
$process = Start-Process -FilePath ".\qsdmplus.exe" -NoNewWindow -PassThru -RedirectStandardOutput "crash_stdout.txt" -RedirectStandardError "crash_stderr.txt"

# Wait a moment
Start-Sleep -Seconds 2

if ($process.HasExited) {
    Write-Host "❌ Process crashed immediately!" -ForegroundColor Red
    Write-Host "   Exit code: $($process.ExitCode)" -ForegroundColor Yellow
    Write-Host ""
    Write-Host "=== STDOUT ===" -ForegroundColor Cyan
    if (Test-Path "crash_stdout.txt") {
        Get-Content "crash_stdout.txt"
    } else {
        Write-Host "(empty)" -ForegroundColor Gray
    }
    Write-Host ""
    Write-Host "=== STDERR ===" -ForegroundColor Cyan
    if (Test-Path "crash_stderr.txt") {
        Get-Content "crash_stderr.txt"
    } else {
        Write-Host "(empty)" -ForegroundColor Gray
    }
} else {
    Write-Host "✅ Process is running (PID: $($process.Id))" -ForegroundColor Green
    Write-Host ""
    Write-Host "Initial output:" -ForegroundColor Cyan
    if (Test-Path "crash_stdout.txt") {
        Get-Content "crash_stdout.txt" | Select-Object -First 20
    }
}

