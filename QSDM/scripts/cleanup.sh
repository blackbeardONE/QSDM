#!/bin/bash
# Cleanup script for QSDM project
# Removes temporary files, build artifacts, and unnecessary files

set -e

echo "=== QSDM Project Cleanup ==="
echo ""

removed_count=0
removed_size=0

remove_file() {
    local file="$1"
    local desc="$2"
    if [ -f "$file" ]; then
        size=$(stat -f%z "$file" 2>/dev/null || stat -c%s "$file" 2>/dev/null || echo 0)
        rm -f "$file"
        if [ ! -f "$file" ]; then
            echo "  Removed: $desc"
            removed_count=$((removed_count + 1))
            removed_size=$((removed_size + size))
        fi
    fi
}

# 1. Temporary text files
echo "1. Cleaning temporary text files..."
for file in crash_*.txt output.txt qsdm_*.txt stderr*.txt stdout*.txt; do
    if [ -f "$file" ]; then
        remove_file "$file" "Temporary text file"
    fi
done

# 2. Build artifacts and executables
echo "2. Cleaning build artifacts..."
find . -maxdepth 1 -name "*.exe" -type f -delete
find . -maxdepth 1 -name "*.test" -type f -delete
find . -maxdepth 1 -name "qsdm-node" -type f -delete
remove_file "dashboard-test.exe" "Dashboard test"
remove_file "verify_dashboard.exe" "Verify dashboard"

# 3. Database temporary files
echo "3. Cleaning database temporary files..."
remove_file "qsdm.db-shm" "SQLite shared memory"
remove_file "qsdm.db-wal" "SQLite write-ahead log"
find . -maxdepth 1 -name "*.db-journal" -type f -delete

# 4. Backup files
echo "4. Cleaning backup files..."
find . -maxdepth 1 -name "*.bak" -type f -delete
remove_file "go.mod.bak" "Go mod backup"

# 5. Old build scripts (move to scripts/)
echo "5. Moving remaining build scripts to scripts/..."
mkdir -p scripts
for pattern in build_*.{ps1,bat,sh} run_*.{ps1,sh} deploy.* copy_*.{ps1,bat} download_*.ps1 find_*.ps1 fix_*.sh serve_*.{bat,sh} benchmark_*.ps1 docker_run_*.sh; do
    for file in $pattern; do
        if [ -f "$file" ] && [ "$(dirname "$file")" = "." ]; then
            mv "$file" scripts/ 2>/dev/null && echo "  Moved to scripts/: $file"
        fi
    done
done

# 6. DLL files in root
echo "6. Cleaning DLL files from root..."
remove_file "libcrypto-3-x64.dll" "OpenSSL DLL"
remove_file "libssl-3-x64.dll" "OpenSSL DLL"
remove_file "cudart64_12.dll" "CUDA DLL"
remove_file "liboqs.dll" "liboqs DLL"

# 7. Test/debug files
echo "7. Cleaning test/debug files..."
remove_file "fix_wasmer_wasi.exe" "Wasmer fix executable"
remove_file "fix_wasmer_wasi.go" "Wasmer fix source"
remove_file "verify_dashboard.go" "Verify dashboard"

# 8. Instruction files (move to docs/archive)
echo "8. Moving instruction files to docs/archive/..."
mkdir -p docs/archive
for file in build_wasmer_go_windows_instructions.txt setup_dependencies_instructions.txt; do
    if [ -f "$file" ]; then
        mv "$file" docs/archive/ && echo "  Moved to docs/archive/: $file"
    fi
done

# 9. Other temporary files
echo "9. Cleaning other temporary files..."
remove_file "qsdm.def" "Definition file"
remove_file "patched_wasi_binding.cc" "Patched file"

# 10. Empty directories
echo "10. Cleaning empty directories..."
if [ -d "storage" ] && [ -z "$(ls -A storage 2>/dev/null)" ]; then
    rmdir storage && echo "  Removed empty directory: storage"
fi

# Summary
echo ""
echo "=== Cleanup Complete ==="
echo "  Files removed: $removed_count"
size_mb=$(echo "scale=2; $removed_size / 1048576" | bc 2>/dev/null || echo "0")
echo "  Space freed: ${size_mb} MB"
echo ""
echo "Note: Build artifacts and temporary files removed."
echo "      Source code and essential files preserved."
echo ""

