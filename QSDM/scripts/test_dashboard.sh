#!/bin/bash
# Quick test script for dashboard

echo "Testing QSDM Dashboard..."

# Test if node is running
if ! pgrep -f qsdm > /dev/null; then
    echo "ERROR: QSDM node is not running!"
    echo "Please start the node first: ./qsdm"
    exit 1
fi

echo "✓ Node is running"

# Test dashboard endpoint
echo ""
echo "Testing dashboard endpoint..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8081/)
if [ "$HTTP_CODE" == "200" ]; then
    echo "✓ Dashboard endpoint responding (HTTP $HTTP_CODE)"
else
    echo "✗ Dashboard endpoint failed (HTTP $HTTP_CODE)"
    exit 1
fi

# Test metrics API
echo ""
echo "Testing metrics API..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8081/api/metrics)
if [ "$HTTP_CODE" == "200" ]; then
    echo "✓ Metrics API responding (HTTP $HTTP_CODE)"
    echo ""
    echo "Sample metrics:"
    curl -s http://localhost:8081/api/metrics | head -20
else
    echo "✗ Metrics API failed (HTTP $HTTP_CODE)"
    exit 1
fi

# Test health API
echo ""
echo "Testing health API..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8081/api/health)
if [ "$HTTP_CODE" == "200" ]; then
    echo "✓ Health API responding (HTTP $HTTP_CODE)"
else
    echo "✗ Health API failed (HTTP $HTTP_CODE)"
    exit 1
fi

# Test static files
echo ""
echo "Testing static files..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8081/static/dashboard.js)
if [ "$HTTP_CODE" == "200" ]; then
    echo "✓ Static files serving (HTTP $HTTP_CODE)"
else
    echo "✗ Static files failed (HTTP $HTTP_CODE)"
    exit 1
fi

echo ""
echo "All tests passed! Dashboard should be working."
echo "Open http://localhost:8081 in your browser"

