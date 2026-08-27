#!/bin/bash
# Production deployment script for QSDM

set -e

echo "=== QSDM Production Deployment ==="
echo ""

# Check prerequisites
echo "Checking prerequisites..."
command -v docker >/dev/null 2>&1 || { echo "Docker is required but not installed. Aborting." >&2; exit 1; }
command -v docker-compose >/dev/null 2>&1 || { echo "Docker Compose is required but not installed. Aborting." >&2; exit 1; }

# Create directories
echo "Creating directories..."
mkdir -p data logs monitoring/prometheus monitoring/grafana/dashboards

# Set permissions
echo "Setting permissions..."
chmod 755 data logs
chmod 644 monitoring/prometheus/*.yml 2>/dev/null || true

# Build Docker image
echo "Building Docker image..."
docker build -t qsdm:latest .

# Start services
echo "Starting services..."
docker-compose -f docker-compose.production.yml up -d

# Wait for services to be healthy
echo "Waiting for services to be healthy..."
sleep 10

# Check health
echo "Checking service health..."
for i in {1..30}; do
    # Probe the API liveness route, not the dashboard. /api/health is
    # requireAuth-wrapped (dashboard.go:294) and now fails closed, and
    # requireAuth REDIRECTS unauthenticated non-JSON requests to the login
    # page -- which `curl -f` does not treat as an error. The gate would
    # have passed on a 302 and verified nothing at all.
    if curl -fsS http://localhost:8080/api/v1/health/live >/dev/null 2>&1; then
        echo "✓ Node 1 is healthy"
        break
    fi
    if [ $i -eq 30 ]; then
        echo "✗ Node 1 failed to become healthy"
        exit 1
    fi
    sleep 2
done

echo ""
echo "=== Deployment Complete ==="
echo ""
echo "Services:"
echo "  - Node 1: http://localhost:8081"
echo "  - Node 2: http://localhost:8083"
echo "  - Node 3: http://localhost:8085"
echo "  - Prometheus: http://localhost:9090"
echo "  - Grafana: http://localhost:3000"
echo ""
echo "To view logs:"
echo "  docker-compose -f docker-compose.production.yml logs -f"
echo ""
echo "To stop services:"
echo "  docker-compose -f docker-compose.production.yml down"
echo ""

