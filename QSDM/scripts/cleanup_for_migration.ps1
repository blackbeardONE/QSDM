# QSDM Project Cleanup for Migration
# This script removes build artifacts, dependencies, and temporary files
# to prepare the project for moving to another development server

Write-Host "=== QSDM Project Cleanup Script ===" -ForegroundColor Cyan
Write-Host "This will remove build artifacts and temporary files." -ForegroundColor Yellow
Write-Host ""

$confirm = Read-Host "Continue with cleanup? (y/N)"
if ($confirm -ne "y" -and $confirm -ne "Y") {
    Write-Host "Cleanup cancelled." -ForegroundColor Yellow
    exit 0
}

Write-Host ""
Write-Host "Starting cleanup..." -ForegroundColor Green
Write-Host ""

$removedItems = @()
$errors = @()

# Function to safely remove items
function Remove-ItemSafely {
    param(
        [string]$path,
        [string]$description
    )
    
    if (Test-Path $path) {
        try {
            if ((Get-Item $path) -is [System.IO.DirectoryInfo]) {
                Remove-Item -Path $path -Recurse -Force -ErrorAction Stop
            } else {
                Remove-Item -Path $path -Force -ErrorAction Stop
            }
            Write-Host "  Removed: $description" -ForegroundColor Green
            $script:removedItems += $description
            return $true
        } catch {
            Write-Host "  Error removing $description : $_" -ForegroundColor Red
            $script:errors += "$description : $_"
            return $false
        }
    } else {
        Write-Host "  Not found: $description (skipping)" -ForegroundColor Gray
        return $false
    }
}

# 1. Remove executables
Write-Host "1. Removing executables..." -ForegroundColor Cyan
Remove-ItemSafely -path "qsdmplus.exe" -description "qsdmplus.exe"
Get-ChildItem -Path . -Filter "*.exe" -Recurse -ErrorAction SilentlyContinue | ForEach-Object {
    if ($_.FullName -notlike "*\node_modules\*" -and $_.FullName -notlike "*\wasmer-go-patched\*") {
        Remove-ItemSafely -path $_.FullName -description $_.Name
    }
}

# 2. Remove DLL files (build artifacts)
Write-Host ""
Write-Host "2. Removing DLL files..." -ForegroundColor Cyan
Remove-ItemSafely -path "cudart64_12.dll" -description "cudart64_12.dll"
Remove-ItemSafely -path "libcrypto-3-x64.dll" -description "libcrypto-3-x64.dll"
Remove-ItemSafely -path "liboqs.dll" -description "liboqs.dll"
Remove-ItemSafely -path "libssl-3-x64.dll" -description "libssl-3-x64.dll"
Remove-ItemSafely -path "wasmer_go.dll" -description "wasmer_go.dll"

# 3. Remove build directories
Write-Host ""
Write-Host "3. Removing build directories..." -ForegroundColor Cyan
Remove-ItemSafely -path "liboqs_build" -description "liboqs_build/"
Remove-ItemSafely -path "liboqs_install" -description "liboqs_install/"

# 4. Remove Rust/Cargo build artifacts
Write-Host ""
Write-Host "4. Removing Rust/Cargo build artifacts..." -ForegroundColor Cyan
Remove-ItemSafely -path "wasm_module\target" -description "wasm_module/target/"
Remove-ItemSafely -path "wasmer-go-patched\target" -description "wasmer-go-patched/target/"

# 5. Remove node_modules
Write-Host ""
Write-Host "5. Removing node_modules..." -ForegroundColor Cyan
Remove-ItemSafely -path "node_modules" -description "node_modules/"

# 6. Remove log files
Write-Host ""
Write-Host "6. Removing log files..." -ForegroundColor Cyan
Get-ChildItem -Path . -Filter "*.log" -Recurse -ErrorAction SilentlyContinue | ForEach-Object {
    if ($_.FullName -notlike "*\node_modules\*" -and $_.FullName -notlike "*\wasmer-go-patched\*") {
        Remove-ItemSafely -path $_.FullName -description $_.Name
    }
}
Remove-ItemSafely -path "qsdmplus.log" -description "qsdmplus.log"

# 7. Remove test databases (keep production databases)
Write-Host ""
Write-Host "7. Removing test databases..." -ForegroundColor Cyan
Remove-ItemSafely -path "pkg\quarantine\test_transactions.db" -description "test_transactions.db"

# 8. Remove temporary files
Write-Host ""
Write-Host "8. Removing temporary files..." -ForegroundColor Cyan
Get-ChildItem -Path . -Filter "*.tmp" -Recurse -ErrorAction SilentlyContinue | ForEach-Object {
    if ($_.FullName -notlike "*\node_modules\*" -and $_.FullName -notlike "*\wasmer-go-patched\*") {
        Remove-ItemSafely -path $_.FullName -description $_.Name
    }
}
Get-ChildItem -Path . -Filter "*.bak" -Recurse -ErrorAction SilentlyContinue | ForEach-Object {
    if ($_.FullName -notlike "*\node_modules\*" -and $_.FullName -notlike "*\wasmer-go-patched\*") {
        Remove-ItemSafely -path $_.FullName -description $_.Name
    }
}

# 9. Remove coverage files
Write-Host ""
Write-Host "9. Removing coverage files..." -ForegroundColor Cyan
Get-ChildItem -Path . -Filter "*.coverprofile" -Recurse -ErrorAction SilentlyContinue | ForEach-Object {
    if ($_.FullName -notlike "*\node_modules\*") {
        Remove-ItemSafely -path $_.FullName -description $_.Name
    }
}
Remove-ItemSafely -path "coverage.out" -description "coverage.out"

# 10. Remove Go test binaries
Write-Host ""
Write-Host "10. Removing Go test binaries..." -ForegroundColor Cyan
Get-ChildItem -Path . -Filter "*.test" -Recurse -ErrorAction SilentlyContinue | ForEach-Object {
    if ($_.FullName -notlike "*\node_modules\*" -and $_.FullName -notlike "*\wasmer-go-patched\*") {
        Remove-ItemSafely -path $_.FullName -description $_.Name
    }
}
Get-ChildItem -Path . -Filter "*.out" -Recurse -ErrorAction SilentlyContinue | ForEach-Object {
    if ($_.FullName -notlike "*\node_modules\*" -and $_.FullName -notlike "*\wasmer-go-patched\*") {
        Remove-ItemSafely -path $_.FullName -description $_.Name
    }
}

# Summary
Write-Host ""
Write-Host "=== Cleanup Summary ===" -ForegroundColor Cyan
Write-Host "Items removed: $($removedItems.Count)" -ForegroundColor Green
if ($errors.Count -gt 0) {
    Write-Host "Errors encountered: $($errors.Count)" -ForegroundColor Red
    foreach ($error in $errors) {
        Write-Host "  - $error" -ForegroundColor Red
    }
}

Write-Host ""
Write-Host "=== Important Notes ===" -ForegroundColor Yellow
Write-Host "1. Production databases (qsdmplus.db, transactions.db) were NOT removed" -ForegroundColor Yellow
Write-Host "2. Source code and configuration files were preserved" -ForegroundColor Yellow
Write-Host "3. Run 'scripts\export_databases.ps1' to export databases before migration" -ForegroundColor Yellow
Write-Host "4. On the new server, you'll need to:" -ForegroundColor Yellow
Write-Host "   - Run 'go mod download' to restore Go dependencies" -ForegroundColor Yellow
Write-Host "   - Run 'npm install' to restore Node.js dependencies" -ForegroundColor Yellow
Write-Host "   - Rebuild the project" -ForegroundColor Yellow
Write-Host ""
Write-Host "Cleanup complete!" -ForegroundColor Green

