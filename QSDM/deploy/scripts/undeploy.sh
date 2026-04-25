#!/bin/bash

# QSDM Undeployment Script
# Removes QSDM cluster deployment

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

DEPLOY_METHOD="${1:-docker}"
NAMESPACE="${2:-qsdm}"

echo -e "${YELLOW}=== QSDM Undeployment ===${NC}"
echo "Method: $DEPLOY_METHOD"
echo ""

if [ "$DEPLOY_METHOD" = "docker" ]; then
    cd "$DEPLOY_DIR"
    echo "Stopping and removing containers..."
    docker-compose -f docker-compose.cluster.yml down -v
    echo -e "${GREEN}Undeployment complete!${NC}"
elif [ "$DEPLOY_METHOD" = "kubernetes" ]; then
    echo "Removing Kubernetes resources..."
    kubectl delete namespace "$NAMESPACE" --ignore-not-found=true
    echo -e "${GREEN}Undeployment complete!${NC}"
else
    echo "Error: Invalid method: $DEPLOY_METHOD"
    exit 1
fi

