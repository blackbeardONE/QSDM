# Test script to check consensus status

Write-Host "Testing QSDM with CGO (liboqs enabled)..." -ForegroundColor Cyan
Write-Host ""

$env:PATH = "C:\msys64\mingw64\bin;$env:PATH"

# Start the process
$proc = Start-Process -FilePath ".\qsdmplus.exe" -NoNewWindow -PassThru -RedirectStandardOutput "test_out.txt" -RedirectStandardError "test_err.txt"

Write-Host "Process started (PID: $($proc.Id))" -ForegroundColor Yellow
Write-Host "Waiting 6 seconds for initialization..." -ForegroundColor Gray

Start-Sleep -Seconds 6

# Check if still running
if (-not $proc.HasExited) {
    Write-Host "✓ Application is RUNNING!" -ForegroundColor Green
    Write-Host ""
    
    # Check consensus status via dashboard
    Write-Host "Checking consensus status..." -ForegroundColor Cyan
    try {
        $health = Invoke-RestMethod -Uri "http://localhost:8081/api/health" -TimeoutSec 3
        $consensusStatus = $health.components.consensus.status
        $consensusMessage = $health.components.consensus.message
        
        if ($consensusStatus -eq "healthy") {
            Write-Host "✓ Consensus: HEALTHY" -ForegroundColor Green
        } elseif ($consensusStatus -eq "degraded") {
            Write-Host "⚠ Consensus: DEGRADED" -ForegroundColor Yellow
            Write-Host "  Message: $consensusMessage" -ForegroundColor Gray
        } else {
            Write-Host "✗ Consensus: $consensusStatus" -ForegroundColor Red
            Write-Host "  Message: $consensusMessage" -ForegroundColor Gray
        }
    } catch {
        Write-Host "⚠ Could not check dashboard: $_" -ForegroundColor Yellow
    }
    
    Write-Host ""
    Write-Host "Stopping application..." -ForegroundColor Yellow
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    Write-Host "✓ Test complete!" -ForegroundColor Green
} else {
    Write-Host "✗ Application exited immediately!" -ForegroundColor Red
    Write-Host "  Exit Code: $($proc.ExitCode)" -ForegroundColor Red
    Write-Host ""
    Write-Host "STDERR:" -ForegroundColor Red
    if (Test-Path "test_err.txt") {
        Get-Content "test_err.txt" -ErrorAction SilentlyContinue
    }
}

