# QSDM Deployment Script (PowerShell)
# Deploys QSDM cluster using Docker Compose

param(
    [string]$Method = "docker",
    [int]$NodeCount = 3,
    [string]$Namespace = "qsdm"
)

$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$DeployDir = Split-Path -Parent $ScriptDir
$ProjectRoot = Split-Path -Parent $DeployDir

Write-Host "=== QSDM Deployment Script ===" -ForegroundColor Green
Write-Host "Deployment method: $Method"
Write-Host "Node count: $NodeCount"
Write-Host ""

# Check prerequisites
function Check-Prerequisites {
    Write-Host "Checking prerequisites..." -ForegroundColor Yellow
    
    if ($Method -eq "docker") {
        if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
            Write-Host "Error: docker is not installed" -ForegroundColor Red
            exit 1
        }
        if (-not (Get-Command docker-compose -ErrorAction SilentlyContinue)) {
            Write-Host "Error: docker-compose is not installed" -ForegroundColor Red
            exit 1
        }
    }
    
    Write-Host "Prerequisites OK" -ForegroundColor Green
}

# Deploy with Docker Compose
function Deploy-Docker {
    Write-Host "Deploying with Docker Compose..." -ForegroundColor Yellow
    
    Push-Location $DeployDir
    
    try {
        # Build images
        Write-Host "Building Docker images..."
        docker-compose -f docker-compose.cluster.yml build
        
        # Start services
        Write-Host "Starting QSDM cluster..."
        docker-compose -f docker-compose.cluster.yml up -d
        
        # Wait for services
        Write-Host "Waiting for services to be healthy..."
        Start-Sleep -Seconds 10
        
        # Check health
        Check-HealthDocker
        
        Write-Host "Deployment complete!" -ForegroundColor Green
        Write-Host ""
        Write-Host "Access points:"
        Write-Host "  Node 1 Dashboard: http://localhost:8081"
        Write-Host "  Node 1 API:       http://localhost:8080"
        Write-Host "  Node 2 Dashboard: http://localhost:8082"
        Write-Host "  Node 2 API:       http://localhost:8083"
        Write-Host "  Node 3 Dashboard: http://localhost:8084"
        Write-Host "  Node 3 API:       http://localhost:8085"
    }
    finally {
        Pop-Location
    }
}

# Check health (Docker)
function Check-HealthDocker {
    Write-Host "Checking cluster health..." -ForegroundColor Yellow
    
    # API ports (docker-compose.cluster.yml): node1 8080, node2 8083, node3 8085 — public /api/v1/health/live
    $ports = @(8080, 8083, 8085)
    for ($i = 0; $i -lt $ports.Count; $i++) {
        $port = $ports[$i]
        $nodeNum = $i + 1
        try {
            $response = Invoke-WebRequest -Uri "http://localhost:$port/api/v1/health/live" -TimeoutSec 5 -UseBasicParsing
            if ($response.StatusCode -eq 200) {
                Write-Host "Node $nodeNum : Healthy" -ForegroundColor Green
            } else {
                Write-Host "Node $nodeNum : Unhealthy" -ForegroundColor Red
            }
        } catch {
            Write-Host "Node $nodeNum : Unhealthy" -ForegroundColor Red
        }
    }
}

# Main execution
function Main {
    Check-Prerequisites
    
    if ($Method -eq "docker") {
        Deploy-Docker
    } else {
        Write-Host "Error: Invalid deployment method: $Method" -ForegroundColor Red
        exit 1
    }
}

Main

