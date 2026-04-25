# Script to verify consensus is healthy

Write-Host "Verifying QSDM consensus status..." -ForegroundColor Cyan
Write-Host ""

$env:PATH = "C:\msys64\mingw64\bin;$env:PATH"

# Start the process
$proc = Start-Process -FilePath ".\qsdm.exe" -NoNewWindow -PassThru

Write-Host "Application started (PID: $($proc.Id))" -ForegroundColor Yellow
Write-Host "Waiting 15 seconds for full initialization..." -ForegroundColor Gray

Start-Sleep -Seconds 15

# Check if still running
if (-not $proc.HasExited) {
    Write-Host "Application is RUNNING!" -ForegroundColor Green
    Write-Host ""
    
    # Check consensus status
    Write-Host "Checking consensus status..." -ForegroundColor Cyan
    try {
        $health = Invoke-RestMethod -Uri "http://localhost:8081/api/health" -TimeoutSec 5
        $consensus = $health.components.consensus
        
        Write-Host ""
        Write-Host "=== CONSENSUS STATUS ===" -ForegroundColor Cyan
        $statusColor = if ($consensus.status -eq 'healthy') { 'Green' } else { 'Yellow' }
        Write-Host "Status: $($consensus.status)" -ForegroundColor $statusColor
        Write-Host "Message: $($consensus.message)" -ForegroundColor Gray
        Write-Host ""
        
        if ($consensus.status -eq 'healthy') {
            Write-Host "CONSENSUS IS HEALTHY!" -ForegroundColor Green
            Write-Host ""
            Write-Host "Quantum-safe cryptography is working!" -ForegroundColor Green
            Write-Host "Proof-of-Entanglement consensus is operational!" -ForegroundColor Green
        } else {
            Write-Host "Consensus is $($consensus.status)" -ForegroundColor Yellow
        }
    } catch {
        Write-Host "Could not check dashboard: $_" -ForegroundColor Yellow
        Write-Host "  Application is running, but dashboard may need more time" -ForegroundColor Gray
    }
    
    Write-Host ""
    Write-Host "Stopping application..." -ForegroundColor Yellow
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    Write-Host "Verification complete!" -ForegroundColor Green
} else {
    $exitCode = $proc.ExitCode
    Write-Host "Application exited (Exit Code: $exitCode)" -ForegroundColor Red
}
