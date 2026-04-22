# Simple health check for QSDM
Write-Host "Checking QSDM health status..." -ForegroundColor Cyan
Write-Host ""

Start-Sleep -Seconds 3

try {
    $response = Invoke-WebRequest -Uri "http://localhost:8081/api/health" -TimeoutSec 3 -UseBasicParsing
    $health = $response.Content | ConvertFrom-Json
    
    Write-Host "=== QSDM Health Status ===" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "Overall Status: " -NoNewline
    if ($health.overall_status -eq "healthy") {
        Write-Host $health.overall_status -ForegroundColor Green
    } elseif ($health.overall_status -eq "degraded") {
        Write-Host $health.overall_status -ForegroundColor Yellow
    } else {
        Write-Host $health.overall_status -ForegroundColor Red
    }
    Write-Host ""
    Write-Host "Component Status:" -ForegroundColor Cyan
    Write-Host ""
    
    $health.components.PSObject.Properties | ForEach-Object {
        $name = $_.Name
        $status = $_.Value.status
        $message = $_.Value.message
        
        Write-Host "  $name" -NoNewline
        if ($status -eq "healthy") {
            Write-Host " : $status" -ForegroundColor Green
        } elseif ($status -eq "degraded") {
            Write-Host " : $status" -ForegroundColor Yellow
        } else {
            Write-Host " : $status" -ForegroundColor Red
        }
        
        if ($message) {
            Write-Host "    $message" -ForegroundColor Gray
        }
    }
    
    Write-Host ""
    Write-Host "Dashboard: http://localhost:8081" -ForegroundColor Cyan
    Write-Host "Log Viewer: http://localhost:8080" -ForegroundColor Cyan
    
} catch {
    Write-Host "Dashboard not responding yet..." -ForegroundColor Yellow
    Write-Host "Error: $($_.Exception.Message)" -ForegroundColor Gray
    Write-Host ""
    Write-Host "The application may still be starting up." -ForegroundColor Gray
    Write-Host "Wait a few seconds and try again, or check:" -ForegroundColor Gray
    Write-Host "  http://localhost:8081" -ForegroundColor Cyan
}

