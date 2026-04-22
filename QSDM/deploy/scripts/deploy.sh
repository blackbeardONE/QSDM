#!/bin/bash

# QSDM+ Deployment Script
# Deploys QSDM+ cluster using Docker Compose or Kubernetes

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
PROJECT_ROOT="$(cd "$DEPLOY_DIR/.." && pwd)"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Default values
DEPLOY_METHOD="docker"
NODE_COUNT=3
NAMESPACE="qsdm"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --method)
            DEPLOY_METHOD="$2"
            shift 2
            ;;
        --nodes)
            NODE_COUNT="$2"
            shift 2
            ;;
        --namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        --help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --method METHOD     Deployment method: docker or kubernetes (default: docker)"
            echo "  --nodes COUNT       Number of nodes (default: 3)"
            echo "  --namespace NAME    Kubernetes namespace (default: qsdm)"
            echo "  --help              Show this help message"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

echo -e "${GREEN}=== QSDM+ Deployment Script ===${NC}"
echo "Deployment method: $DEPLOY_METHOD"
echo "Node count: $NODE_COUNT"
echo ""

# Check prerequisites
check_prerequisites() {
    echo -e "${YELLOW}Checking prerequisites...${NC}"
    
    if [ "$DEPLOY_METHOD" = "docker" ]; then
        if ! command -v docker &> /dev/null; then
            echo -e "${RED}Error: docker is not installed${NC}"
            exit 1
        fi
        if ! command -v docker-compose &> /dev/null; then
            echo -e "${RED}Error: docker-compose is not installed${NC}"
            exit 1
        fi
    elif [ "$DEPLOY_METHOD" = "kubernetes" ]; then
        if ! command -v kubectl &> /dev/null; then
            echo -e "${RED}Error: kubectl is not installed${NC}"
            exit 1
        fi
    fi
    
    echo -e "${GREEN}Prerequisites OK${NC}"
}

# Deploy with Docker Compose
deploy_docker() {
    echo -e "${YELLOW}Deploying with Docker Compose...${NC}"
    
    cd "$DEPLOY_DIR"
    
    # Build images
    echo "Building Docker images..."
    docker-compose -f docker-compose.cluster.yml build
    
    # Start services
    echo "Starting QSDM+ cluster..."
    docker-compose -f docker-compose.cluster.yml up -d
    
    # Wait for services to be healthy
    echo "Waiting for services to be healthy..."
    sleep 10
    
    # Check health
    check_health_docker
    
    echo -e "${GREEN}Deployment complete!${NC}"
    echo ""
    echo "Access points:"
    echo "  Node 1 Dashboard: http://localhost:8081"
    echo "  Node 1 API:       http://localhost:8080"
    echo "  Node 2 Dashboard: http://localhost:8082"
    echo "  Node 2 API:       http://localhost:8083"
    echo "  Node 3 Dashboard: http://localhost:8084"
    echo "  Node 3 API:       http://localhost:8085"
}

# Deploy with Kubernetes
deploy_kubernetes() {
    echo -e "${YELLOW}Deploying with Kubernetes...${NC}"
    
    cd "$DEPLOY_DIR/kubernetes"
    
    # Create namespace
    echo "Creating namespace..."
    kubectl apply -f namespace.yaml
    
    # Apply ConfigMap
    echo "Applying ConfigMap..."
    kubectl apply -f configmap.yaml
    
    # Apply Secret
    echo "Applying Secret..."
    kubectl apply -f secret.yaml
    
    # Apply PVC
    echo "Applying PersistentVolumeClaim..."
    kubectl apply -f pvc.yaml
    
    # Apply StatefulSet
    echo "Applying StatefulSet..."
    kubectl apply -f statefulset.yaml
    
    # Apply Services
    echo "Applying Services..."
    kubectl apply -f service.yaml
    
    # Wait for pods to be ready
    echo "Waiting for pods to be ready..."
    kubectl wait --for=condition=ready pod -l app=qsdm -n "$NAMESPACE" --timeout=300s
    
    # Check health
    check_health_kubernetes
    
    echo -e "${GREEN}Deployment complete!${NC}"
    echo ""
    echo "Access points:"
    kubectl get svc -n "$NAMESPACE" | grep qsdm
}

# Check health (Docker)
check_health_docker() {
    echo -e "${YELLOW}Checking cluster health...${NC}"
    
    for port in 8080 8083 8085; do
        if curl -f -s "http://localhost:${port}/api/v1/health/live" > /dev/null; then
            echo -e "${GREEN}API :${port} healthy${NC}"
        else
            echo -e "${RED}API :${port} unhealthy${NC}"
        fi
    done
}

# Check health (Kubernetes)
check_health_kubernetes() {
    echo -e "${YELLOW}Checking cluster health...${NC}"
    
    pods=$(kubectl get pods -n "$NAMESPACE" -l app=qsdm -o jsonpath='{.items[*].metadata.name}')
    
    for pod in $pods; do
        if kubectl exec -n "$NAMESPACE" "$pod" -- wget -q -O- http://localhost:8080/api/v1/health/live > /dev/null 2>&1; then
            echo -e "${GREEN}$pod: Healthy${NC}"
        else
            echo -e "${RED}$pod: Unhealthy${NC}"
        fi
    done
}

# Main execution
main() {
    check_prerequisites
    
    if [ "$DEPLOY_METHOD" = "docker" ]; then
        deploy_docker
    elif [ "$DEPLOY_METHOD" = "kubernetes" ]; then
        deploy_kubernetes
    else
        echo -e "${RED}Error: Invalid deployment method: $DEPLOY_METHOD${NC}"
        exit 1
    fi
}

main

