# Simple test to run qsdmplus.exe
$env:PATH = "C:\msys64\mingw64\bin;$env:PATH"

Write-Host "Testing qsdmplus.exe..." -ForegroundColor Cyan
Write-Host "PATH includes OpenSSL: $($env:PATH -like '*mingw64*bin*')" -ForegroundColor Gray
Write-Host ""

# Try to run and capture output
try {
    $proc = Start-Process -FilePath ".\qsdmplus.exe" -NoNewWindow -PassThru -RedirectStandardOutput "test_stdout.txt" -RedirectStandardError "test_stderr.txt"
    
    Start-Sleep -Seconds 2
    
    Write-Host "Process ID: $($proc.Id)" -ForegroundColor Yellow
    
    # Check if still running
    $running = Get-Process -Id $proc.Id -ErrorAction SilentlyContinue
    if ($running) {
        Write-Host "✓ Process is RUNNING" -ForegroundColor Green
        Write-Host "Stopping process..." -ForegroundColor Yellow
        Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    } else {
        Write-Host "✗ Process exited immediately" -ForegroundColor Red
    }
    
    Write-Host ""
    Write-Host "=== STDOUT ===" -ForegroundColor Cyan
    if (Test-Path "test_stdout.txt") {
        Get-Content "test_stdout.txt" -ErrorAction SilentlyContinue
    } else {
        Write-Host "(empty)" -ForegroundColor Gray
    }
    
    Write-Host ""
    Write-Host "=== STDERR ===" -ForegroundColor Red
    if (Test-Path "test_stderr.txt") {
        Get-Content "test_stderr.txt" -ErrorAction SilentlyContinue
    } else {
        Write-Host "(empty)" -ForegroundColor Gray
    }
    
} catch {
    Write-Host "Error: $_" -ForegroundColor Red
}

Write-Host ""
Write-Host "Latest log entries:" -ForegroundColor Cyan
Get-Content "qsdmplus.log" -Tail 3 -ErrorAction SilentlyContinue

