# Test dashboard access with longer timeout
Write-Host "Testing dashboard access..." -ForegroundColor Cyan
Write-Host ""

$url = "http://127.0.0.1:8081/api/health"

try {
    Write-Host "Connecting to: $url" -ForegroundColor Gray
    $response = Invoke-WebRequest -Uri $url -TimeoutSec 15 -UseBasicParsing
    
    Write-Host "✅ SUCCESS! Dashboard is responding!" -ForegroundColor Green
    Write-Host ""
    
    $health = $response.Content | ConvertFrom-Json
    
    Write-Host "=== Health Status ===" -ForegroundColor Cyan
    Write-Host ""
    
    # Overall status
    $overall = $health.overall_status
    $overallColor = if ($overall -eq "healthy") { "Green" } elseif ($overall -eq "degraded") { "Yellow" } else { "Red" }
    Write-Host "Overall: $overall" -ForegroundColor $overallColor
    Write-Host ""
    
    # Components
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
    Write-Host "=== Key Components ===" -ForegroundColor Cyan
    
    # Check consensus
    if ($health.components.consensus) {
        $consensus = $health.components.consensus
        if ($consensus.status -eq "healthy") {
            Write-Host "✅ Consensus: HEALTHY (quantum-safe crypto working!)" -ForegroundColor Green
        } else {
            Write-Host "⚠️  Consensus: $($consensus.status)" -ForegroundColor Yellow
            Write-Host "   $($consensus.message)" -ForegroundColor Gray
        }
    }
    
    # Check wallet
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
    Write-Host "Dashboard URL: http://localhost:8081" -ForegroundColor Cyan
    
} catch {
    Write-Host "❌ Failed to connect to dashboard" -ForegroundColor Red
    Write-Host "Error: $($_.Exception.Message)" -ForegroundColor Yellow
    Write-Host ""
    Write-Host "The dashboard may still be starting. Try:" -ForegroundColor Yellow
    Write-Host "  1. Wait a bit longer" -ForegroundColor Gray
    Write-Host "  2. Check http://localhost:8081 in your browser" -ForegroundColor Gray
    Write-Host "  3. Check application logs" -ForegroundColor Gray
}

