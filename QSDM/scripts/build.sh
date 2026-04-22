#!/bin/bash
# Build script for QSDM on Linux (Ubuntu/Debian)
# This script builds QSDM with CGO enabled for quantum-safe cryptography

set -e  # Exit on error

echo "=== Building QSDM for Linux ==="
echo ""
echo "This build includes:"
echo "  - Quantum-safe cryptography (liboqs)"
echo "  - SQLite storage"
echo "  - CUDA acceleration (if available)"
echo ""

# Check if Go is installed
if ! command -v go &> /dev/null; then
    echo "ERROR: Go is not installed!"
    echo "Please install Go 1.20 or higher:"
    echo "  sudo apt update"
    echo "  sudo apt install golang-go"
    exit 1
fi

GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
echo "Go version: $GO_VERSION"
echo ""

# Set CGO environment
export CGO_ENABLED=1

# Clear any existing CGO flags
unset CGO_CFLAGS
unset CGO_LDFLAGS
unset CGO_CPPFLAGS
unset CGO_CXXFLAGS

# Check if liboqs is available
LIBOQS_FOUND=false
LIBOQS_PATH=""

# Check common locations
POSSIBLE_PATHS=(
    "$HOME/liboqs_install"
    "/usr/local"
    "/opt/liboqs"
    "$(pwd)/liboqs_install"
    "$(pwd)/liboqs_build"
)

for path in "${POSSIBLE_PATHS[@]}"; do
    if [ -f "$path/include/oqs/oqs.h" ]; then
        echo "Found liboqs installation at $path" | grep -v "^$"
        export CGO_CFLAGS="-I$path/include"
        
        # Check for shared library first (preferred)
        if [ -f "$path/lib/liboqs.so" ] || [ -f "$path/lib64/liboqs.so" ]; then
            if [ -d "$path/lib64" ]; then
                export CGO_LDFLAGS="-L$path/lib64 -loqs"
            else
                export CGO_LDFLAGS="-L$path/lib -loqs"
            fi
            echo "  Using liboqs shared library"
        elif [ -f "$path/lib/liboqs.a" ] || [ -f "$path/lib64/liboqs.a" ]; then
            if [ -d "$path/lib64" ]; then
                export CGO_LDFLAGS="-L$path/lib64 -loqs"
            else
                export CGO_LDFLAGS="-L$path/lib -loqs"
            fi
            echo "  Using liboqs static library"
        else
            export CGO_LDFLAGS="-loqs"
        fi
        
        LIBOQS_FOUND=true
        LIBOQS_PATH="$path"
        break
    fi
done

# Check system-wide installation (pkg-config)
if [ "$LIBOQS_FOUND" = false ]; then
    if command -v pkg-config &> /dev/null; then
        if pkg-config --exists liboqs; then
            echo "Found liboqs via pkg-config"
            export CGO_CFLAGS="$(pkg-config --cflags liboqs)"
            export CGO_LDFLAGS="$(pkg-config --libs liboqs)"
            LIBOQS_FOUND=true
        fi
    fi
fi

if [ "$LIBOQS_FOUND" = false ]; then
    echo ""
    echo "WARNING: liboqs not found!" | grep -v "^$"
    echo "Attempting to install liboqs automatically..."
    echo ""
    
    # Check if rebuild script exists
    if [ -f "./rebuild_liboqs.sh" ]; then
        echo "Running rebuild_liboqs.sh..."
        bash ./rebuild_liboqs.sh
        
        # Check again after installation
        if [ -f "$(pwd)/liboqs_install/include/oqs/oqs.h" ]; then
            echo "liboqs installed successfully!"
            export CGO_CFLAGS="-I$(pwd)/liboqs_install/include"
            if [ -d "$(pwd)/liboqs_install/lib64" ]; then
                export CGO_LDFLAGS="-L$(pwd)/liboqs_install/lib64 -loqs"
            else
                export CGO_LDFLAGS="-L$(pwd)/liboqs_install/lib -loqs"
            fi
            LIBOQS_FOUND=true
            LIBOQS_PATH="$(pwd)/liboqs_install"
        fi
    fi
    
    if [ "$LIBOQS_FOUND" = false ]; then
        echo ""
        echo "ERROR: liboqs is required but not found." | grep -v "^$"
        echo "Please install liboqs manually or run:" | grep -v "^$"
        echo "  ./rebuild_liboqs.sh" | grep -v "^$"
        echo ""
        echo "The build will continue but quantum-safe features will be unavailable."
        echo ""
    fi
fi

# Check for OpenSSL (required by liboqs)
if ! command -v openssl &> /dev/null; then
    echo "WARNING: OpenSSL not found in PATH" | grep -v "^$"
    echo "liboqs requires OpenSSL. Please install:" | grep -v "^$"
    echo "  sudo apt install libssl-dev" | grep -v "^$"
    echo ""
fi

# Check for SQLite development headers
if ! pkg-config --exists sqlite3 2>/dev/null; then
    echo "WARNING: SQLite development headers not found" | grep -v "^$"
    echo "Please install:" | grep -v "^$"
    echo "  sudo apt install libsqlite3-dev" | grep -v "^$"
    echo ""
fi

# Set library path for runtime
if [ -n "$LIBOQS_PATH" ]; then
    if [ -d "$LIBOQS_PATH/lib64" ]; then
        export LD_LIBRARY_PATH="$LIBOQS_PATH/lib64:${LD_LIBRARY_PATH:-}"
    elif [ -d "$LIBOQS_PATH/lib" ]; then
        export LD_LIBRARY_PATH="$LIBOQS_PATH/lib:${LD_LIBRARY_PATH:-}"
    fi
fi

echo "Building QSDM..."
echo "CGO_CFLAGS: ${CGO_CFLAGS:-<none>}"
echo "CGO_LDFLAGS: ${CGO_LDFLAGS:-<none>}"
echo ""

# Check if we're in the root directory or source directory
if [ -f "source/go.mod" ]; then
    # We're in root, build from source directory
    echo "Building from source/ directory..."
    cd source
    go build -o ../qsdmplus -v ./cmd/qsdmplus
    cd ..
elif [ -f "go.mod" ]; then
    # We're already in source directory
    go build -o ../qsdmplus -v ./cmd/qsdmplus
else
    echo "ERROR: go.mod not found. Please run from QSDM root or source directory."
    exit 1
fi

if [ $? -eq 0 ]; then
    echo ""
    echo "=== Build Successful! ===" | grep -v "^$"
    echo ""
    echo "Binary: ./qsdmplus"
    echo ""
    echo "To run QSDM:"
    echo "  ./run.sh"
    echo "  # Or directly:"
    echo "  ./qsdmplus"
    echo ""
    
    # Check if binary needs liboqs at runtime
    if [ -n "$LIBOQS_PATH" ]; then
        echo "Note: Make sure liboqs is available at runtime:"
        if [ -d "$LIBOQS_PATH/lib64" ]; then
            echo "  export LD_LIBRARY_PATH=\"$LIBOQS_PATH/lib64:\$LD_LIBRARY_PATH\""
        else
            echo "  export LD_LIBRARY_PATH=\"$LIBOQS_PATH/lib:\$LD_LIBRARY_PATH\""
        fi
        echo ""
    fi
else
    echo ""
    echo "=== Build Failed! ===" | grep -v "^$"
    exit 1
fi

