# Simple script to check consensus status

$env:PATH = "C:\msys64\mingw64\bin;$env:PATH"

Write-Host "Checking if QSDM is running..." -ForegroundColor Cyan

try {
    $health = Invoke-RestMethod -Uri "http://localhost:8081/api/health" -TimeoutSec 3
    $consensus = $health.components.consensus
    
    Write-Host ""
    Write-Host "=== CONSENSUS STATUS ===" -ForegroundColor Cyan
    if ($consensus.status -eq 'healthy') {
        Write-Host "Status: HEALTHY" -ForegroundColor Green
        Write-Host ""
        Write-Host "CONSENSUS IS HEALTHY!" -ForegroundColor Green
        Write-Host "Quantum-safe cryptography is working!" -ForegroundColor Green
    } else {
        Write-Host "Status: $($consensus.status)" -ForegroundColor Yellow
    }
    Write-Host "Message: $($consensus.message)" -ForegroundColor Gray
    Write-Host ""
} catch {
    Write-Host "Application is not running or dashboard not accessible" -ForegroundColor Yellow
    Write-Host "Error: $_" -ForegroundColor Red
    Write-Host ""
    Write-Host "To start the application:" -ForegroundColor Cyan
    Write-Host "  $env:PATH = 'C:\msys64\mingw64\bin;$env:PATH'" -ForegroundColor Gray
    Write-Host "  .\qsdmplus.exe" -ForegroundColor Gray
}

