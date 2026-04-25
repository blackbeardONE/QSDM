# PowerShell cleanup script for QSDM project
# Removes temporary files, build artifacts, and unnecessary files

Write-Host "=== QSDM Project Cleanup ===" -ForegroundColor Cyan
Write-Host ""

$removedCount = 0
$removedSize = 0

function Remove-File {
    param($Path, $Description)
    if (Test-Path $Path) {
        $size = (Get-Item $Path).Length
        Remove-Item $Path -Force -ErrorAction SilentlyContinue
        if (-not (Test-Path $Path)) {
            Write-Host "  Removed: $Description" -ForegroundColor Gray
            $script:removedCount++
            $script:removedSize += $size
        }
    }
}

function Remove-Files {
    param($Pattern, $Description)
    Get-ChildItem -Filter $Pattern -ErrorAction SilentlyContinue | ForEach-Object {
        $size = $_.Length
        Remove-Item $_.FullName -Force -ErrorAction SilentlyContinue
        Write-Host "  Removed: $Description ($($_.Name))" -ForegroundColor Gray
        $script:removedCount++
        $script:removedSize += $size
    }
}

# 1. Temporary text files
Write-Host "1. Cleaning temporary text files..." -ForegroundColor Green
Get-ChildItem -Filter "*.txt" -ErrorAction SilentlyContinue | Where-Object {
    $_.Name -match "^(crash_|output|qsdm_error|qsdm_output|stderr|stdout)" -and
    $_.Name -notmatch "requirements|setup_dependencies|build_wasmer"
} | ForEach-Object {
    $size = $_.Length
    Remove-Item $_.FullName -Force -ErrorAction SilentlyContinue
    Write-Host "  Removed: Temporary text file ($($_.Name))" -ForegroundColor Gray
    $script:removedCount++
    $script:removedSize += $size
}

# Specific temporary files
Remove-File "crash_stderr.txt" "Crash stderr output"
Remove-File "crash_stdout.txt" "Crash stdout output"
Remove-File "output.txt" "Output file"
Remove-File "qsdm_error.txt" "QSDM error output"
Remove-File "qsdm_output.txt" "QSDM output"
Remove-File "stderr.txt" "Stderr output"
Remove-File "stdout.txt" "Stdout output"
Remove-File "stderr_debug.txt" "Debug stderr"
Remove-File "stdout_debug.txt" "Debug stdout"

# 2. Build artifacts and executables
Write-Host "2. Cleaning build artifacts..." -ForegroundColor Green
Remove-Files "*.exe" "Executable"
Remove-Files "*.test.exe" "Test executable"
Remove-File "qsdm-node" "QSDM node binary"
Remove-File "dashboard-test.exe" "Dashboard test"
Remove-File "qsdm_test.exe" "QSDM test"
Remove-File "tests.test.exe" "Tests executable"
Remove-File "verify_dashboard.exe" "Verify dashboard"

# 3. Database temporary files
Write-Host "3. Cleaning database temporary files..." -ForegroundColor Green
Remove-File "qsdm.db-shm" "SQLite shared memory"
Remove-File "qsdm.db-wal" "SQLite write-ahead log"
Remove-File "*.db-journal" "SQLite journal"

# 4. Backup files
Write-Host "4. Cleaning backup files..." -ForegroundColor Green
Remove-Files "*.bak" "Backup file"
Remove-File "go.mod.bak" "Go mod backup"

# 5. Old build scripts (should be in scripts/)
Write-Host "5. Moving remaining build scripts to scripts/..." -ForegroundColor Green
$buildScripts = @(
    "build_*.ps1", "build_*.bat", "build_*.sh",
    "run_*.ps1", "run_*.sh",
    "deploy.*", "copy_*.ps1", "copy_*.bat",
    "download_*.ps1", "find_*.ps1",
    "fix_*.sh", "serve_*.bat", "serve_*.sh",
    "benchmark_*.ps1", "docker_run_*.sh"
)

foreach ($pattern in $buildScripts) {
    Get-ChildItem -Filter $pattern -ErrorAction SilentlyContinue | Where-Object {
        $_.DirectoryName -eq (Get-Location).Path
    } | ForEach-Object {
        Move-Item $_.FullName "scripts\" -Force -ErrorAction SilentlyContinue
        Write-Host "  Moved to scripts/: $($_.Name)" -ForegroundColor Gray
    }
}

# 6. DLL files in root (should be in libs/ or ignored)
Write-Host "6. Cleaning DLL files from root..." -ForegroundColor Green
Remove-File "libcrypto-3-x64.dll" "OpenSSL DLL (should be in PATH)"
Remove-File "libssl-3-x64.dll" "OpenSSL DLL (should be in PATH)"
Remove-File "cudart64_12.dll" "CUDA DLL (should be in PATH)"
Remove-File "liboqs.dll" "liboqs DLL (should be in liboqs_install/)"

# 7. Test/debug files
Write-Host "7. Cleaning test/debug files..." -ForegroundColor Green
Remove-File "fix_wasmer_wasi.exe" "Wasmer fix executable"
Remove-File "fix_wasmer_wasi.go" "Wasmer fix source (temporary)"
Remove-File "verify_dashboard.go" "Verify dashboard (temporary)"
Remove-File "wasm_js_integration_browser_test_wasm3.html" "Test HTML (move to tests/)"

# 8. Old instruction files (move to docs/archive)
Write-Host "8. Moving instruction files to docs/archive/..." -ForegroundColor Green
$instructionFiles = @(
    "build_wasmer_go_windows_instructions.txt",
    "setup_dependencies_instructions.txt"
)

foreach ($file in $instructionFiles) {
    if (Test-Path $file) {
        Move-Item $file "docs\archive\" -Force -ErrorAction SilentlyContinue
        Write-Host "  Moved to docs/archive/: $file" -ForegroundColor Gray
    }
}

# 9. Other temporary files
Write-Host "9. Cleaning other temporary files..." -ForegroundColor Green
Remove-File "qsdm.def" "Definition file (generated)"
Remove-File "patched_wasi_binding.cc" "Patched file (should be in wasmer-go-patched/)"
Remove-File "comparative analysis.png" "Image (move to docs/images/)"

# 10. Empty directories
Write-Host "10. Cleaning empty directories..." -ForegroundColor Green
$emptyDirs = @("storage")
foreach ($dir in $emptyDirs) {
    if (Test-Path $dir) {
        $items = Get-ChildItem $dir -ErrorAction SilentlyContinue
        if ($items.Count -eq 0) {
            Remove-Item $dir -Force -ErrorAction SilentlyContinue
            Write-Host "  Removed empty directory: $dir" -ForegroundColor Gray
        }
    }
}

# Summary
Write-Host ""
Write-Host "=== Cleanup Complete ===" -ForegroundColor Green
Write-Host "  Files removed: $removedCount" -ForegroundColor Cyan
$sizeMB = [math]::Round($removedSize / 1MB, 2)
Write-Host "  Space freed: $sizeMB MB" -ForegroundColor Cyan
Write-Host ""
Write-Host "Note: Build artifacts and temporary files removed." -ForegroundColor Yellow
Write-Host "      Source code and essential files preserved." -ForegroundColor Yellow
Write-Host ""

