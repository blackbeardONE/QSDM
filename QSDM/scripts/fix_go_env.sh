#!/bin/bash
# Fix Go environment for QSDM build
# This script sets up GOROOT and ensures the correct Go binary is used

# Try to use official Go installation first
if [ -d "/c/Program Files/Go" ]; then
    export GOROOT="/c/Program Files/Go"
    export PATH="/c/Program Files/Go/bin:$PATH"
    echo "Using official Go installation: $GOROOT"
elif [ -d "/c/msys64/mingw64/lib/go" ]; then
    export GOROOT="/c/msys64/mingw64/lib/go"
    export PATH="/c/msys64/mingw64/bin:$PATH"
    echo "Using MSYS2 Go installation: $GOROOT"
else
    echo "ERROR: Go installation not found!"
    exit 1
fi

# Verify Go works
go version
if [ $? -ne 0 ]; then
    echo "ERROR: Go is not working properly"
    exit 1
fi

echo "Go environment configured successfully"
echo "GOROOT: $GOROOT"
echo "GOPATH: $GOPATH"

