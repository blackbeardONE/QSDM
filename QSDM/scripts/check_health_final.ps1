# Final health check for QSDM
Write-Host "Checking QSDM health (port 8082)..." -ForegroundColor Cyan
Write-Host ""

Start-Sleep -Seconds 5

try {
    $response = Invoke-WebRequest -Uri "http://localhost:8082/api/health" -TimeoutSec 10 -UseBasicParsing
    $health = $response.Content | ConvertFrom-Json
    
    Write-Host "=== QSDM Health Status ===" -ForegroundColor Cyan
    Write-Host ""
    
    $overall = $health.overall_status
    $overallColor = if ($overall -eq "healthy") { "Green" } elseif ($overall -eq "degraded") { "Yellow" } else { "Red" }
    Write-Host "Overall Status: $overall" -ForegroundColor $overallColor
    Write-Host ""
    
    Write-Host "Components:" -ForegroundColor Cyan
    $health.components.PSObject.Properties | ForEach-Object {
        $name = $_.Name
        $status = $_.Value.status
        $message = $_.Value.message
        
        $color = if ($status -eq "healthy") { "Green" } elseif ($status -eq "degraded") { "Yellow" } else { "Red" }
        Write-Host "  $name : $status" -ForegroundColor $color
        if ($message) {
            Write-Host "    $message" -ForegroundColor Gray
        }
    }
    
    Write-Host ""
    Write-Host "=== Key Status ===" -ForegroundColor Cyan
    
    if ($health.components.consensus) {
        $consensus = $health.components.consensus
        if ($consensus.status -eq "healthy") {
            Write-Host "✅ Consensus: HEALTHY (quantum-safe crypto working!)" -ForegroundColor Green
        } else {
            Write-Host "⚠️  Consensus: $($consensus.status)" -ForegroundColor Yellow
            Write-Host "   $($consensus.message)" -ForegroundColor Gray
        }
    }
    
    if ($health.components.wallet) {
        $wallet = $health.components.wallet
        if ($wallet.status -eq "healthy") {
            Write-Host "✅ Wallet: HEALTHY" -ForegroundColor Green
        } else {
            Write-Host "⚠️  Wallet: $($wallet.status)" -ForegroundColor Yellow
            Write-Host "   $($wallet.message)" -ForegroundColor Gray
        }
    }
    
    Write-Host ""
    Write-Host "Dashboard: http://localhost:8082" -ForegroundColor Cyan
    Write-Host "Log Viewer: http://localhost:8083" -ForegroundColor Cyan
    
} catch {
    Write-Host "Dashboard not responding yet..." -ForegroundColor Yellow
    Write-Host "Error: $($_.Exception.Message)" -ForegroundColor Gray
    Write-Host ""
    Write-Host "The application may still be starting. Try:" -ForegroundColor Yellow
    Write-Host "  1. Wait a bit longer and run this script again" -ForegroundColor Gray
    Write-Host "  2. Check http://localhost:8082 in your browser" -ForegroundColor Gray
    Write-Host "  3. Check if qsdmplus.exe process is still running" -ForegroundColor Gray
}

