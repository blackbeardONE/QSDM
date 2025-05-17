#!/bin/bash

# Set environment variables for CGO to find OQS and CUDA headers and libs
export CGO_CFLAGS="-IC:/liboqs/include -IC:/CUDA/include"
export CGO_LDFLAGS="-LC:/liboqs/lib -LC:/CUDA/lib/x64"
export CGO_ENABLED=1

# Clean previous builds
go clean -cache -modcache -testcache

# Build Rust wasm_module
cargo build --release --manifest-path wasm_module/Cargo.toml
if [ $? -ne 0 ]; then
  echo "Rust wasm_module build failed"
  exit 1
fi

# Build Go project
go build -o qsdm.exe ./cmd/qsdm
if [ $? -ne 0 ]; then
  echo "Go project build failed"
  exit 1
fi

echo "Build successful. You can now run ./qsdm.exe"
