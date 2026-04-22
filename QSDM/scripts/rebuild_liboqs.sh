#!/bin/bash
# Rebuild liboqs for Linux (Ubuntu/Debian)
# This script builds and installs liboqs with ML-DSA-87 support

set -e  # Exit on error

echo "=== Rebuilding liboqs for Linux ==="
echo ""

# Check for required tools
REQUIRED_TOOLS=("cmake" "make" "gcc" "git")
MISSING_TOOLS=()

for tool in "${REQUIRED_TOOLS[@]}"; do
    if ! command -v $tool &> /dev/null; then
        MISSING_TOOLS+=($tool)
    fi
done

if [ ${#MISSING_TOOLS[@]} -ne 0 ]; then
    echo "ERROR: Missing required tools: ${MISSING_TOOLS[*]}"
    echo "Please install them:"
    echo "  sudo apt update"
    echo "  sudo apt install build-essential cmake git libssl-dev"
    exit 1
fi

# Check for OpenSSL
if ! pkg-config --exists openssl 2>/dev/null; then
    echo "ERROR: OpenSSL development headers not found"
    echo "Please install:"
    echo "  sudo apt install libssl-dev"
    exit 1
fi

# Set installation directory
INSTALL_DIR="$(pwd)/liboqs_install"
BUILD_DIR="$(pwd)/liboqs_build"

echo "Installation directory: $INSTALL_DIR"
echo "Build directory: $BUILD_DIR"
echo ""

# Clean previous builds
if [ -d "$BUILD_DIR" ]; then
    echo "Cleaning previous build..."
    rm -rf "$BUILD_DIR"
fi

if [ -d "$INSTALL_DIR" ]; then
    echo "Removing previous installation..."
    rm -rf "$INSTALL_DIR"
fi

# Clone or update liboqs
LIBOQS_SRC="liboqs_src"
if [ ! -d "$LIBOQS_SRC" ]; then
    echo "Cloning liboqs repository..."
    git clone --depth 1 --branch main https://github.com/open-quantum-safe/liboqs.git "$LIBOQS_SRC"
else
    echo "Updating liboqs repository..."
    cd "$LIBOQS_SRC"
    git pull
    cd ..
fi

# Create build directory
mkdir -p "$BUILD_DIR"
cd "$BUILD_DIR"

echo ""
echo "Configuring liboqs with CMake..."
echo "  - Building shared library"
echo "  - Enabling ML-DSA-87 (256-bit quantum-safe)"
echo "  - Using OpenSSL for primitives"
echo ""

# Configure CMake
cmake -DCMAKE_INSTALL_PREFIX="$INSTALL_DIR" \
      -DCMAKE_BUILD_TYPE=Release \
      -DBUILD_SHARED_LIBS=ON \
      -DOQS_USE_OPENSSL_SHARED=ON \
      -DOQS_USE_AES_OPENSSL=ON \
      -DOQS_USE_SHA2_OPENSSL=ON \
      -DOQS_USE_SHA3_OPENSSL=ON \
      -DOQS_BUILD_ONLY_LIB=ON \
      -DOQS_ENABLE_SIG_ml_dsa_87=ON \
      ../"$LIBOQS_SRC"

echo ""
echo "Building liboqs (this may take several minutes)..."
make -j$(nproc)

echo ""
echo "Installing liboqs..."
make install

cd ..

echo ""
echo "=== liboqs Build Complete! ==="
echo ""
echo "Installation location: $INSTALL_DIR"
echo ""
echo "Library files:"
if [ -d "$INSTALL_DIR/lib64" ]; then
    ls -lh "$INSTALL_DIR/lib64"/liboqs.so* 2>/dev/null || echo "  (shared library)"
else
    ls -lh "$INSTALL_DIR/lib"/liboqs.so* 2>/dev/null || echo "  (shared library)"
fi
echo ""
echo "Header files:"
ls -lh "$INSTALL_DIR/include/oqs/"*.h 2>/dev/null | head -5
echo ""
echo "To use this installation, set:"
if [ -d "$INSTALL_DIR/lib64" ]; then
    echo "  export CGO_CFLAGS=\"-I$INSTALL_DIR/include\""
    echo "  export CGO_LDFLAGS=\"-L$INSTALL_DIR/lib64 -loqs\""
    echo "  export LD_LIBRARY_PATH=\"$INSTALL_DIR/lib64:\$LD_LIBRARY_PATH\""
else
    echo "  export CGO_CFLAGS=\"-I$INSTALL_DIR/include\""
    echo "  export CGO_LDFLAGS=\"-L$INSTALL_DIR/lib -loqs\""
    echo "  export LD_LIBRARY_PATH=\"$INSTALL_DIR/lib:\$LD_LIBRARY_PATH\""
fi
echo ""
echo "Or run: ./build.sh (automatically detects this installation)"
echo ""

