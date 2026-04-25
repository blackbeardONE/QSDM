#!/bin/bash

# Build the QSDM project
echo "Building QSDM project..."
go build -o qsdm cmd/qsdm/main.go
if [ $? -ne 0 ]; then
  echo "Build failed."
  exit 1
fi
echo "Build succeeded."

# Run the QSDM node
echo "Starting QSDM node..."
./qsdm

# Note: Ensure environment variables are set as needed, e.g. USE_SCYLLA=true for ScyllaDB usage
