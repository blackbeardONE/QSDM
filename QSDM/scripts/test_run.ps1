# Simple test script to run qsdmplus.exe and capture output
$env:PATH = "C:\msys64\mingw64\bin;$env:PATH"

Write-Host "Running qsdmplus.exe with OpenSSL in PATH..."
Write-Host ""

# Run with output redirection
$proc = Start-Process -FilePath ".\qsdmplus.exe" -NoNewWindow -PassThru -RedirectStandardOutput "stdout.txt" -RedirectStandardError "stderr.txt"

Start-Sleep -Seconds 3

Write-Host "Checking output files..."
if (Test-Path "stdout.txt") {
    Write-Host "=== STDOUT ===" -ForegroundColor Cyan
    Get-Content "stdout.txt" -ErrorAction SilentlyContinue
}

if (Test-Path "stderr.txt") {
    Write-Host "=== STDERR ===" -ForegroundColor Red
    Get-Content "stderr.txt" -ErrorAction SilentlyContinue
}

Write-Host ""
Write-Host "Checking if process is running..."
$running = Get-Process -Id $proc.Id -ErrorAction SilentlyContinue
if ($running) {
    Write-Host "Process is RUNNING (PID: $($proc.Id))" -ForegroundColor Green
    Write-Host "Stopping process..."
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
} else {
    Write-Host "Process is NOT running (exited immediately)" -ForegroundColor Red
}

Write-Host ""
Write-Host "Latest log entries:"
Get-Content "qsdmplus.log" -Tail 5 -ErrorAction SilentlyContinue

