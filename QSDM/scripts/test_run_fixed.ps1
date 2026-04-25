# Test script to verify application runs and consensus is healthy

Write-Host "Testing QSDM application..." -ForegroundColor Cyan
Write-Host ""

$env:PATH = "C:\msys64\mingw64\bin;$env:PATH"

# Start the process
$proc = Start-Process -FilePath ".\qsdm.exe" -NoNewWindow -PassThru -RedirectStandardOutput "test_out.txt" -RedirectStandardError "test_err.txt"

Write-Host "Process started (PID: $($proc.Id))" -ForegroundColor Yellow
Write-Host "Waiting 8 seconds for initialization..." -ForegroundColor Gray

Start-Sleep -Seconds 8

# Check if still running
if (-not $proc.HasExited) {
    Write-Host "✓ Application is RUNNING!" -ForegroundColor Green
    Write-Host ""
    
    # Check consensus status via dashboard
    Write-Host "Checking consensus status..." -ForegroundColor Cyan
    try {
        Start-Sleep -Seconds 2
        $health = Invoke-RestMethod -Uri "http://localhost:8081/api/health" -TimeoutSec 3
        $consensusStatus = $health.components.consensus.status
        $consensusMessage = $health.components.consensus.message
        
        Write-Host ""
        Write-Host "=== CONSENSUS STATUS ===" -ForegroundColor Cyan
        if ($consensusStatus -eq "healthy") {
            Write-Host "Status: HEALTHY" -ForegroundColor Green
            Write-Host "✓ Consensus is working correctly!" -ForegroundColor Green
        } elseif ($consensusStatus -eq "degraded") {
            Write-Host "Status: DEGRADED" -ForegroundColor Yellow
            Write-Host "⚠ Consensus is degraded" -ForegroundColor Yellow
        } else {
            Write-Host "Status: $consensusStatus" -ForegroundColor Red
        }
        Write-Host "Message: $consensusMessage" -ForegroundColor Gray
        Write-Host ""
    } catch {
        Write-Host "⚠ Could not check dashboard: $_" -ForegroundColor Yellow
    }
    
    Write-Host "Stopping application..." -ForegroundColor Yellow
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    Write-Host "✓ Test complete!" -ForegroundColor Green
} else {
    Write-Host "✗ Application exited immediately!" -ForegroundColor Red
    Write-Host "  Exit Code: $($proc.ExitCode)" -ForegroundColor Red
    Write-Host ""
    Write-Host "STDERR:" -ForegroundColor Red
    if (Test-Path "test_err.txt") {
        Get-Content "test_err.txt" -ErrorAction SilentlyContinue | Select-Object -First 20
    }
    Write-Host ""
    Write-Host "STDOUT:" -ForegroundColor Yellow
    if (Test-Path "test_out.txt") {
        Get-Content "test_out.txt" -ErrorAction SilentlyContinue | Select-Object -First 20
    }
}
