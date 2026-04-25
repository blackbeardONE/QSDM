# Create Migration Package for QSDM
# This script creates a complete package of all files needed for migration

Write-Host "=== Creating QSDM Migration Package ===" -ForegroundColor Cyan
Write-Host ""

$packageDir = "migration_package"
$timestamp = Get-Date -Format "yyyyMMdd_HHmmss"

# Remove old package if exists
if (Test-Path $packageDir) {
    Remove-Item -Path $packageDir -Recurse -Force
    Write-Host "Removed old package directory" -ForegroundColor Yellow
}

# Create package structure
New-Item -ItemType Directory -Path $packageDir | Out-Null
New-Item -ItemType Directory -Path "$packageDir\source" | Out-Null
New-Item -ItemType Directory -Path "$packageDir\databases" | Out-Null
New-Item -ItemType Directory -Path "$packageDir\config" | Out-Null
New-Item -ItemType Directory -Path "$packageDir\scripts" | Out-Null
New-Item -ItemType Directory -Path "$packageDir\docs" | Out-Null

Write-Host "Created package structure" -ForegroundColor Green
Write-Host ""

# 1. Copy source code
Write-Host "1. Copying source code..." -ForegroundColor Cyan
$sourceDirs = @("cmd", "pkg", "internal", "sdk", "libs", "wasm_modules")
foreach ($dir in $sourceDirs) {
    if (Test-Path $dir) {
        Copy-Item -Path $dir -Destination "$packageDir\source\$dir" -Recurse -Force
        Write-Host "  Copied: $dir" -ForegroundColor Gray
    }
}

# Copy root Go files
if (Test-Path "go.mod") { Copy-Item -Path "go.mod" -Destination "$packageDir\source\" -Force }
if (Test-Path "go.sum") { Copy-Item -Path "go.sum" -Destination "$packageDir\source\" -Force }
Write-Host "  Copied: go.mod, go.sum" -ForegroundColor Gray

# 2. Copy WASM module source
Write-Host ""
Write-Host "2. Copying WASM module source..." -ForegroundColor Cyan
if (Test-Path "wasm_module") {
    # Copy source files but exclude target directory
    $wasmSource = "$packageDir\source\wasm_module"
    New-Item -ItemType Directory -Path $wasmSource | Out-Null
    Get-ChildItem -Path "wasm_module" -Exclude "target" | Copy-Item -Destination $wasmSource -Recurse -Force
    Write-Host "  Copied: wasm_module (excluding target/)" -ForegroundColor Gray
}

# 3. Copy wasmer-go-patched source (excluding target)
Write-Host ""
Write-Host "3. Copying wasmer-go-patched source..." -ForegroundColor Cyan
if (Test-Path "wasmer-go-patched") {
    $wasmerSource = "$packageDir\source\wasmer-go-patched"
    New-Item -ItemType Directory -Path $wasmerSource | Out-Null
    Get-ChildItem -Path "wasmer-go-patched" -Exclude "target" | Copy-Item -Destination $wasmerSource -Recurse -Force
    Write-Host "  Copied: wasmer-go-patched (excluding target/)" -ForegroundColor Gray
}

# 4. Copy database exports
Write-Host ""
Write-Host "4. Copying database exports..." -ForegroundColor Cyan
if (Test-Path "database_exports") {
    Copy-Item -Path "database_exports\*" -Destination "$packageDir\databases\" -Recurse -Force
    Write-Host "  Copied: database exports" -ForegroundColor Gray
}

# Also copy production databases
if (Test-Path "qsdm.db") {
    Copy-Item -Path "qsdm.db" -Destination "$packageDir\databases\" -Force
    Write-Host "  Copied: qsdm.db" -ForegroundColor Gray
}
if (Test-Path "qsdm.db-wal") {
    Copy-Item -Path "qsdm.db-wal" -Destination "$packageDir\databases\" -Force
    Write-Host "  Copied: qsdm.db-wal" -ForegroundColor Gray
}
if (Test-Path "qsdm.db-shm") {
    Copy-Item -Path "qsdm.db-shm" -Destination "$packageDir\databases\" -Force
    Write-Host "  Copied: qsdm.db-shm" -ForegroundColor Gray
}
if (Test-Path "transactions.db") {
    Copy-Item -Path "transactions.db" -Destination "$packageDir\databases\" -Force
    Write-Host "  Copied: transactions.db" -ForegroundColor Gray
}

# 5. Copy configuration files
Write-Host ""
Write-Host "5. Copying configuration files..." -ForegroundColor Cyan
if (Test-Path "config") {
    Copy-Item -Path "config\*" -Destination "$packageDir\config\" -Recurse -Force
    Write-Host "  Copied: config/" -ForegroundColor Gray
}

# Copy Docker files
if (Test-Path "Dockerfile") { Copy-Item -Path "Dockerfile" -Destination "$packageDir\" -Force }
if (Test-Path "docker-compose.yml") { Copy-Item -Path "docker-compose.yml" -Destination "$packageDir\" -Force }
if (Test-Path "docker-compose.production.yml") { Copy-Item -Path "docker-compose.production.yml" -Destination "$packageDir\" -Force }
if (Test-Path ".dockerignore") { Copy-Item -Path ".dockerignore" -Destination "$packageDir\" -Force }
Write-Host "  Copied: Docker files" -ForegroundColor Gray

# 6. Copy Node.js files
Write-Host ""
Write-Host "6. Copying Node.js files..." -ForegroundColor Cyan
if (Test-Path "package.json") { Copy-Item -Path "package.json" -Destination "$packageDir\" -Force }
if (Test-Path "package-lock.json") { Copy-Item -Path "package-lock.json" -Destination "$packageDir\" -Force }
Write-Host "  Copied: package.json, package-lock.json" -ForegroundColor Gray

# 7. Copy Python requirements
Write-Host ""
Write-Host "7. Copying Python requirements..." -ForegroundColor Cyan
if (Test-Path "requirements.txt") { Copy-Item -Path "requirements.txt" -Destination "$packageDir\" -Force }
Write-Host "  Copied: requirements.txt" -ForegroundColor Gray

# 8. Copy scripts
Write-Host ""
Write-Host "8. Copying scripts..." -ForegroundColor Cyan
if (Test-Path "scripts") {
    Copy-Item -Path "scripts\*" -Destination "$packageDir\scripts\" -Recurse -Force
    Write-Host "  Copied: scripts/" -ForegroundColor Gray
}

# 9. Copy deployment files
Write-Host ""
Write-Host "9. Copying deployment files..." -ForegroundColor Cyan
if (Test-Path "deploy") {
    Copy-Item -Path "deploy" -Destination "$packageDir\deploy" -Recurse -Force
    Write-Host "  Copied: deploy/" -ForegroundColor Gray
}

# 10. Copy documentation
Write-Host ""
Write-Host "10. Copying documentation..." -ForegroundColor Cyan
if (Test-Path "docs") {
    Copy-Item -Path "docs" -Destination "$packageDir\docs" -Recurse -Force
    Write-Host "  Copied: docs/" -ForegroundColor Gray
}

# Copy root documentation
$rootDocs = @("README.md", "LICENSE", "MIGRATION_GUIDE.md", "PROJECT_STATUS.md", "PROJECT_STRUCTURE.md")
foreach ($doc in $rootDocs) {
    if (Test-Path $doc) {
        Copy-Item -Path $doc -Destination "$packageDir\" -Force
        Write-Host "  Copied: $doc" -ForegroundColor Gray
    }
}

# 11. Copy tests (optional, but useful)
Write-Host ""
Write-Host "11. Copying tests..." -ForegroundColor Cyan
if (Test-Path "tests") {
    Copy-Item -Path "tests" -Destination "$packageDir\tests" -Recurse -Force
    Write-Host "  Copied: tests/" -ForegroundColor Gray
}

# 12. Create setup script
Write-Host ""
Write-Host "12. Creating setup script..." -ForegroundColor Cyan
$setupScript = @"
#!/bin/bash
# QSDM Migration Setup Script
# Run this script on the target server to set up the project

set -e

echo "=== QSDM Migration Setup ==="
echo ""

# Check prerequisites
echo "Checking prerequisites..."
command -v go >/dev/null 2>&1 || { echo "Error: Go is not installed"; exit 1; }
command -v npm >/dev/null 2>&1 || { echo "Error: npm is not installed"; exit 1; }
command -v sqlite3 >/dev/null 2>&1 || { echo "Warning: sqlite3 not found - database import may fail"; }
echo "Prerequisites OK"
echo ""

# Install Go dependencies
echo "Installing Go dependencies..."
cd source
go mod download
go mod verify
cd ..
echo "Go dependencies installed"
echo ""

# Install Node.js dependencies
echo "Installing Node.js dependencies..."
npm install
echo "Node.js dependencies installed"
echo ""

# Import databases
echo "Importing databases..."
if [ -f "databases/qsdm_*.sql" ]; then
    DB_FILE=`ls databases/qsdm_*.sql | head -1`
    echo "Importing main database from `$DB_FILE..."
    sqlite3 databases/qsdm.db < `$DB_FILE
    chmod 644 databases/qsdm.db
fi

if [ -f "databases/transactions_*.sql" ]; then
    TX_FILE=`ls databases/transactions_*.sql | head -1`
    echo "Importing transactions database from `$TX_FILE..."
    sqlite3 databases/transactions.db < `$TX_FILE
    chmod 644 databases/transactions.db
fi

# Or copy binary databases if SQL dumps don't exist
if [ ! -f "databases/qsdm.db" ] && [ -f "databases/qsdm.db" ]; then
    echo "Copying binary database files..."
    cp databases/qsdm.db* . 2>/dev/null || true
    cp databases/transactions.db . 2>/dev/null || true
    chmod 644 *.db* 2>/dev/null || true
fi

echo "Databases imported"
echo ""

# Build the project
echo "Building QSDM..."
cd source
go build -o ../qsdm ./cmd/qsdm
cd ..
echo "Build complete"
echo ""

# Verify installation
echo "Verifying installation..."
if [ -f "qsdm" ]; then
    echo "✓ QSDM binary created successfully"
else
    echo "✗ QSDM binary not found"
fi

if [ -f "databases/qsdm.db" ] || [ -f "qsdm.db" ]; then
    echo "✓ Database files present"
    if command -v sqlite3 >/dev/null 2>&1; then
        TX_COUNT=`sqlite3 databases/qsdm.db "SELECT COUNT(*) FROM transactions;" 2>/dev/null || sqlite3 qsdm.db "SELECT COUNT(*) FROM transactions;" 2>/dev/null || echo "0"`
        echo "  Transactions in database: `$TX_COUNT"
    fi
else
    echo "✗ Database files not found"
fi

echo ""
echo "=== Setup Complete ==="
echo ""
echo "Next steps:"
echo "1. Review and update configuration files in config/"
echo "2. Test the installation: ./qsdm --help"
echo "3. Start the service as needed"
echo ""
"@

Set-Content -Path "$packageDir\setup.sh" -Value $setupScript -Encoding UTF8
Write-Host "  Created: setup.sh" -ForegroundColor Gray

# Create Windows setup script
$setupScriptPS = @"
# QSDM Migration Setup Script (PowerShell)
# Run this script on the target Windows server

Write-Host "=== QSDM Migration Setup ===" -ForegroundColor Cyan
Write-Host ""

# Check prerequisites
Write-Host "Checking prerequisites..." -ForegroundColor Yellow
try {
    `$null = go version
    Write-Host "  Go: OK" -ForegroundColor Green
} catch {
    Write-Host "  Error: Go is not installed" -ForegroundColor Red
    exit 1
}

try {
    `$null = npm --version
    Write-Host "  npm: OK" -ForegroundColor Green
} catch {
    Write-Host "  Error: npm is not installed" -ForegroundColor Red
    exit 1
}

Write-Host ""

# Install Go dependencies
Write-Host "Installing Go dependencies..." -ForegroundColor Yellow
Set-Location source
go mod download
go mod verify
Set-Location ..
Write-Host "Go dependencies installed" -ForegroundColor Green
Write-Host ""

# Install Node.js dependencies
Write-Host "Installing Node.js dependencies..." -ForegroundColor Yellow
npm install
Write-Host "Node.js dependencies installed" -ForegroundColor Green
Write-Host ""

# Import databases
Write-Host "Importing databases..." -ForegroundColor Yellow
`$dbFiles = Get-ChildItem -Path "databases" -Filter "qsdm_*.sql"
if (`$dbFiles) {
    `$dbFile = `$dbFiles[0].FullName
    Write-Host "  Importing main database from `$dbFile..." -ForegroundColor Gray
    sqlite3 databases\qsdm.db < `$dbFile
}

`$txFiles = Get-ChildItem -Path "databases" -Filter "transactions_*.sql"
if (`$txFiles) {
    `$txFile = `$txFiles[0].FullName
    Write-Host "  Importing transactions database from `$txFile..." -ForegroundColor Gray
    sqlite3 databases\transactions.db < `$txFile
}

# Or copy binary databases
if (-not (Test-Path "databases\qsdm.db") -and (Test-Path "databases\qsdm.db")) {
    Write-Host "  Copying binary database files..." -ForegroundColor Gray
    Copy-Item -Path "databases\qsdm.db*" -Destination "." -Force
    Copy-Item -Path "databases\transactions.db" -Destination "." -Force -ErrorAction SilentlyContinue
}

Write-Host "Databases imported" -ForegroundColor Green
Write-Host ""

# Build the project
Write-Host "Building QSDM..." -ForegroundColor Yellow
Set-Location source
go build -o ..\qsdm.exe .\cmd\qsdm
Set-Location ..
Write-Host "Build complete" -ForegroundColor Green
Write-Host ""

# Verify installation
Write-Host "Verifying installation..." -ForegroundColor Yellow
if (Test-Path "qsdm.exe") {
    Write-Host "  QSDM binary created successfully" -ForegroundColor Green
} else {
    Write-Host "  QSDM binary not found" -ForegroundColor Red
}

if ((Test-Path "databases\qsdm.db") -or (Test-Path "qsdm.db")) {
    Write-Host "  Database files present" -ForegroundColor Green
} else {
    Write-Host "  Database files not found" -ForegroundColor Red
}

Write-Host ""
Write-Host "=== Setup Complete ===" -ForegroundColor Cyan
Write-Host ""
Write-Host "Next steps:" -ForegroundColor Yellow
Write-Host "1. Review and update configuration files in config\" -ForegroundColor White
Write-Host "2. Test the installation: .\qsdm.exe --help" -ForegroundColor White
Write-Host "3. Start the service as needed" -ForegroundColor White
Write-Host ""
"@

Set-Content -Path "$packageDir\setup.ps1" -Value $setupScriptPS -Encoding UTF8
Write-Host "  Created: setup.ps1" -ForegroundColor Gray

# 13. Create package README
Write-Host ""
Write-Host "13. Creating package README..." -ForegroundColor Cyan
$packageReadme = @"
QSDM Migration Package
======================

Created: $(Get-Date -Format "yyyy-MM-dd HH:mm:ss")

This package contains all files needed to migrate QSDM to a new development server.

Package Contents:
-----------------

1. source/          - All source code (Go, Rust, JavaScript)
2. databases/       - Database exports and production databases
3. config/          - Configuration files and examples
4. scripts/         - Utility scripts
5. docs/            - Documentation
6. tests/           - Test files
7. deploy/          - Deployment configurations
8. setup.sh         - Linux/Unix setup script
9. setup.ps1        - Windows setup script
10. MIGRATION_GUIDE.md - Detailed migration instructions

Quick Start:
-----------

Linux/Unix:
  chmod +x setup.sh
  ./setup.sh

Windows:
  powershell -ExecutionPolicy Bypass -File setup.ps1

Manual Setup:
------------

1. Install dependencies:
   - Go: go mod download (in source/)
   - Node.js: npm install
   - Python: pip install -r requirements.txt (if needed)

2. Import databases:
   - Use SQL dumps in databases/ directory
   - Or copy binary .db files directly

3. Build the project:
   - cd source
   - go build -o ../qsdm ./cmd/qsdm

4. Configure:
   - Review config/ directory
   - Copy example configs as needed

For detailed instructions, see MIGRATION_GUIDE.md

"@

Set-Content -Path "$packageDir\README.txt" -Value $packageReadme -Encoding UTF8
Write-Host "  Created: README.txt" -ForegroundColor Gray

# 14. Create archive
Write-Host ""
Write-Host "14. Creating archive..." -ForegroundColor Cyan
$archiveName = "qsdm_migration_$timestamp.zip"

# Use PowerShell compression
Compress-Archive -Path "$packageDir\*" -DestinationPath $archiveName -Force
Write-Host "  Created: $archiveName" -ForegroundColor Green

# Calculate sizes
$packageSize = (Get-ChildItem -Path $packageDir -Recurse | Measure-Object -Property Length -Sum).Sum / 1MB
$archiveSize = (Get-Item $archiveName).Length / 1MB

Write-Host ""
Write-Host "=== Package Creation Complete ===" -ForegroundColor Cyan
Write-Host "Package directory: $packageDir ($([math]::Round($packageSize, 2)) MB)" -ForegroundColor Green
Write-Host "Archive file: $archiveName ($([math]::Round($archiveSize, 2)) MB)" -ForegroundColor Green
Write-Host ""
Write-Host "Ready for migration!" -ForegroundColor Green
Write-Host ""

