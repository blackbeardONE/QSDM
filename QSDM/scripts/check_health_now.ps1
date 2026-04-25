# Quick health check - wait and retry
Write-Host "Waiting for QSDM to fully start..." -ForegroundColor Cyan
Start-Sleep -Seconds 8

Write-Host ""
Write-Host "Checking health status..." -ForegroundColor Cyan
Write-Host ""

$maxRetries = 3
$retryCount = 0

while ($retryCount -lt $maxRetries) {
    try {
        $response = Invoke-WebRequest -Uri "http://localhost:8081/api/health" -TimeoutSec 5 -UseBasicParsing
        $health = $response.Content | ConvertFrom-Json
        
        Write-Host "=== QSDM Health Status ===" -ForegroundColor Cyan
        Write-Host ""
        
        # Overall status
        $overallColor = if ($health.overall_status -eq "healthy") { "Green" } elseif ($health.overall_status -eq "degraded") { "Yellow" } else { "Red" }
        Write-Host "Overall Status: $($health.overall_status)" -ForegroundColor $overallColor
        Write-Host ""
        
        # Component status
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
        Write-Host "Dashboard: http://localhost:8081" -ForegroundColor Cyan
        
        # Check specifically for consensus and wallet
        $consensus = $health.components.consensus
        $wallet = $health.components.wallet
        
        Write-Host ""
        if ($consensus -and $consensus.status -eq "healthy") {
            Write-Host "✅ Consensus: HEALTHY (quantum-safe crypto working!)" -ForegroundColor Green
        } elseif ($consensus -and $consensus.status -eq "degraded") {
            Write-Host "⚠️  Consensus: DEGRADED" -ForegroundColor Yellow
            Write-Host "   $($consensus.message)" -ForegroundColor Gray
        }
        
        if ($wallet -and $wallet.status -eq "healthy") {
            Write-Host "✅ Wallet: HEALTHY" -ForegroundColor Green
        } elseif ($wallet -and $wallet.status -eq "degraded") {
            Write-Host "⚠️  Wallet: DEGRADED" -ForegroundColor Yellow
            Write-Host "   $($wallet.message)" -ForegroundColor Gray
        }
        
        break
    } catch {
        $retryCount++
        if ($retryCount -lt $maxRetries) {
            Write-Host "Dashboard not ready yet, retrying in 3 seconds... ($retryCount/$maxRetries)" -ForegroundColor Yellow
            Start-Sleep -Seconds 3
        } else {
            Write-Host "Dashboard not responding after $maxRetries attempts" -ForegroundColor Red
            Write-Host "Error: $($_.Exception.Message)" -ForegroundColor Gray
            Write-Host ""
            Write-Host "The application may still be starting. Try:" -ForegroundColor Yellow
            Write-Host "  1. Wait a bit longer and run this script again" -ForegroundColor Gray
            Write-Host "  2. Check http://localhost:8081 in your browser" -ForegroundColor Gray
            Write-Host "  3. Check if qsdm.exe process is still running" -ForegroundColor Gray
        }
    }
}

