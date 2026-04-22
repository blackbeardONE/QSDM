#!/bin/bash
# Script to organize QSDM project files
# Moves files to appropriate directories

set -e

echo "=== Organizing QSDM Project Files ==="
echo ""

# Create directories
echo "Creating directory structure..."
mkdir -p scripts
mkdir -p docs/archive
mkdir -p tests/archive
mkdir -p config

# Move build scripts
echo "Moving build scripts..."
mv build.sh scripts/ 2>/dev/null || true
mv build.ps1 scripts/ 2>/dev/null || true
mv build_no_cgo.ps1 scripts/ 2>/dev/null || true
mv rebuild_liboqs.sh scripts/ 2>/dev/null || true
mv rebuild_liboqs.ps1 scripts/ 2>/dev/null || true
mv run.sh scripts/ 2>/dev/null || true
mv run.ps1 scripts/ 2>/dev/null || true

# Move test scripts
echo "Moving test scripts..."
mv test_*.ps1 scripts/ 2>/dev/null || true
mv test_*.sh scripts/ 2>/dev/null || true
mv test_*.bat scripts/ 2>/dev/null || true
mv run_*_tests.* scripts/ 2>/dev/null || true

# Move check/verify scripts
echo "Moving utility scripts..."
mv check_*.ps1 scripts/ 2>/dev/null || true
mv verify_*.ps1 scripts/ 2>/dev/null || true
mv fix_*.ps1 scripts/ 2>/dev/null || true
mv diagnose_*.ps1 scripts/ 2>/dev/null || true
mv start_*.ps1 scripts/ 2>/dev/null || true

# Move documentation to docs/archive
echo "Moving old documentation to docs/archive..."
mv *.md docs/archive/ 2>/dev/null || true
# But keep important ones in root
mv docs/archive/README.md . 2>/dev/null || true
mv docs/archive/LICENSE . 2>/dev/null || true
mv docs/archive/PROJECT_STRUCTURE.md . 2>/dev/null || true

# Move config examples
echo "Moving configuration files..."
mv *.toml.example config/ 2>/dev/null || true
mv *.yaml.example config/ 2>/dev/null || true
mv *.service config/ 2>/dev/null || true

# Move test files
echo "Moving test files..."
mv test_*.c tests/archive/ 2>/dev/null || true
mv test_*.go tests/archive/ 2>/dev/null || true
mv test_*.exe tests/archive/ 2>/dev/null || true
mv test_*.txt tests/archive/ 2>/dev/null || true

echo ""
echo "=== Organization Complete ==="
echo ""
echo "Note: You may need to update:"
echo "  - Import paths in Go files"
echo "  - Script paths in documentation"
echo "  - Build script references"

