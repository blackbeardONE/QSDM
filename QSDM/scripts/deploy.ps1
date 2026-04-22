# PowerShell production deployment script for QSDM

Write-Host "=== QSDM Production Deployment ===" -ForegroundColor Cyan
Write-Host ""

# Check prerequisites
Write-Host "Checking prerequisites..." -ForegroundColor Yellow
if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Write-Host "ERROR: Docker is required but not installed." -ForegroundColor Red
    exit 1
}
if (-not (Get-Command docker-compose -ErrorAction SilentlyContinue)) {
    Write-Host "ERROR: Docker Compose is required but not installed." -ForegroundColor Red
    exit 1
}

# Create directories
Write-Host "Creating directories..." -ForegroundColor Yellow
New-Item -ItemType Directory -Force -Path "data", "logs", "monitoring\prometheus", "monitoring\grafana\dashboards" | Out-Null

# Build Docker image
Write-Host "Building Docker image..." -ForegroundColor Yellow
docker build -t qsdm:latest .

if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Docker build failed" -ForegroundColor Red
    exit 1
}

# Start services
Write-Host "Starting services..." -ForegroundColor Yellow
docker-compose -f docker-compose.production.yml up -d

if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Failed to start services" -ForegroundColor Red
    exit 1
}

# Wait for services
Write-Host "Waiting for services to be healthy..." -ForegroundColor Yellow
Start-Sleep -Seconds 10

# Check health
Write-Host "Checking service health..." -ForegroundColor Yellow
$healthy = $false
for ($i = 1; $i -le 30; $i++) {
    try {
        $response = Invoke-WebRequest -Uri "http://localhost:8081/api/health" -TimeoutSec 2 -ErrorAction Stop
        if ($response.StatusCode -eq 200) {
            Write-Host "✓ Node 1 is healthy" -ForegroundColor Green
            $healthy = $true
            break
        }
    } catch {
        Start-Sleep -Seconds 2
    }
}

if (-not $healthy) {
    Write-Host "✗ Node 1 failed to become healthy" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "=== Deployment Complete ===" -ForegroundColor Green
Write-Host ""
Write-Host "Services:" -ForegroundColor Cyan
Write-Host "  - Node 1: http://localhost:8081"
Write-Host "  - Node 2: http://localhost:8083"
Write-Host "  - Node 3: http://localhost:8085"
Write-Host "  - Prometheus: http://localhost:9090"
Write-Host "  - Grafana: http://localhost:3000"
Write-Host ""
Write-Host "To view logs:" -ForegroundColor Gray
Write-Host "  docker-compose -f docker-compose.production.yml logs -f"
Write-Host ""
Write-Host "To stop services:" -ForegroundColor Gray
Write-Host "  docker-compose -f docker-compose.production.yml down"
Write-Host ""

