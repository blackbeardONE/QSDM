# Quick health check script for QSDM
Start-Sleep -Seconds 3

Write-Host "Checking QSDM health status..." -ForegroundColor Cyan
Write-Host ""

try {
    $response = Invoke-WebRequest -Uri 'http://localhost:8081/api/health' -TimeoutSec 2 -UseBasicParsing
    $health = $response.Content | ConvertFrom-Json
    
    Write-Host "=== QSDM Health Status ===" -ForegroundColor Cyan
    Write-Host "Overall Status: $($health.overall_status)" -ForegroundColor $(if ($health.overall_status -eq 'healthy') { 'Green' } else { 'Yellow' })
    Write-Host ""
    Write-Host "Component Status:" -ForegroundColor Cyan
    
    $health.components.PSObject.Properties | ForEach-Object {
        $status = $_.Value.status
        $color = if ($status -eq 'healthy') { 'Green' } elseif ($status -eq 'degraded') { 'Yellow' } else { 'Red' }
        Write-Host "  $($_.Name): $status" -ForegroundColor $color
        if ($_.Value.message) {
            Write-Host "    Message: $($_.Value.message)" -ForegroundColor Gray
        }
    }
} catch {
    Write-Host "Dashboard not responding yet. Application may still be starting..." -ForegroundColor Yellow
    Write-Host "Error: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host ""
    Write-Host "You can check:" -ForegroundColor Cyan
    Write-Host "  1. Open browser: http://localhost:8081" -ForegroundColor Gray
    Write-Host "  2. Check if qsdm.exe process is running" -ForegroundColor Gray
    Write-Host "  3. Check application logs" -ForegroundColor Gray
}

