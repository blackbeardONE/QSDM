# Quick diagnostic and fix script for liboqs initialization issues
# This script checks the liboqs build configuration and provides fix instructions

Write-Host "=== liboqs Initialization Diagnostic ===" -ForegroundColor Cyan
Write-Host ""

$liboqsBuildDir = "D:\Projects\QSDM\liboqs\build"
$cmakeCache = "$liboqsBuildDir\CMakeCache.txt"
$needsRebuild = $false

# Check if build directory exists
if (-not (Test-Path $liboqsBuildDir)) {
    Write-Host "❌ liboqs build directory not found" -ForegroundColor Red
    Write-Host "   Location: $liboqsBuildDir" -ForegroundColor Gray
    Write-Host ""
    Write-Host "Solution: liboqs needs to be built first" -ForegroundColor Yellow
    Write-Host "  Run: .\rebuild_liboqs.ps1" -ForegroundColor Cyan
    exit 1
}

# Check CMakeCache.txt
if (-not (Test-Path $cmakeCache)) {
    Write-Host "⚠️  CMakeCache.txt not found - build may not be configured" -ForegroundColor Yellow
    Write-Host "   Location: $cmakeCache" -ForegroundColor Gray
    Write-Host ""
    Write-Host "Solution: Rebuild liboqs" -ForegroundColor Yellow
    Write-Host "  Run: .\rebuild_liboqs.ps1" -ForegroundColor Cyan
    exit 1
}

# Check OpenSSL configuration
Write-Host "Checking liboqs build configuration..." -ForegroundColor Green
$cacheContent = Get-Content $cmakeCache -ErrorAction SilentlyContinue
$opensslShared = $cacheContent | Select-String "OQS_USE_OPENSSL_SHARED" | Select-Object -First 1
$opensslRoot = $cacheContent | Select-String "OPENSSL_ROOT_DIR" | Select-Object -First 1

Write-Host ""
if ($opensslShared) {
    Write-Host "  OpenSSL Shared Linking: $opensslShared" -ForegroundColor $(if ($opensslShared -match ":ON") { "Green" } else { "Red" })
    if ($opensslShared -notmatch ":ON") {
        $needsRebuild = $true
        Write-Host "    ❌ NOT ENABLED - This is likely causing OQS_SIG_new to fail!" -ForegroundColor Red
    } else {
        Write-Host "    ✅ ENABLED" -ForegroundColor Green
    }
} else {
    Write-Host "  ⚠️  OQS_USE_OPENSSL_SHARED not found in cache" -ForegroundColor Yellow
    $needsRebuild = $true
}

if ($opensslRoot) {
    Write-Host "  OpenSSL Root: $opensslRoot" -ForegroundColor Gray
}

Write-Host ""

# Check OpenSSL DLLs
Write-Host "Checking OpenSSL DLLs..." -ForegroundColor Green
$opensslBin = "C:\msys64\mingw64\bin"
$cryptoDll = "$opensslBin\libcrypto-3-x64.dll"
$sslDll = "$opensslBin\libssl-3-x64.dll"

if (Test-Path $cryptoDll) {
    Write-Host "  ✅ libcrypto-3-x64.dll found" -ForegroundColor Green
} else {
    Write-Host "  ❌ libcrypto-3-x64.dll NOT found at: $cryptoDll" -ForegroundColor Red
}

if (Test-Path $sslDll) {
    Write-Host "  ✅ libssl-3-x64.dll found" -ForegroundColor Green
} else {
    Write-Host "  ❌ libssl-3-x64.dll NOT found at: $sslDll" -ForegroundColor Red
}

# Check if DLLs are in project root
Write-Host ""
Write-Host "Checking project root for OpenSSL DLLs..." -ForegroundColor Green
if (Test-Path ".\libcrypto-3-x64.dll") {
    Write-Host "  ✅ libcrypto-3-x64.dll in project root" -ForegroundColor Green
} else {
    Write-Host "  ⚠️  libcrypto-3-x64.dll not in project root" -ForegroundColor Yellow
    Write-Host "     Copying from MSYS2..." -ForegroundColor Gray
    if (Test-Path $cryptoDll) {
        Copy-Item $cryptoDll -Destination ".\libcrypto-3-x64.dll" -Force -ErrorAction SilentlyContinue
        Write-Host "     ✅ Copied" -ForegroundColor Green
    }
}

if (Test-Path ".\libssl-3-x64.dll") {
    Write-Host "  ✅ libssl-3-x64.dll in project root" -ForegroundColor Green
} else {
    Write-Host "  ⚠️  libssl-3-x64.dll not in project root" -ForegroundColor Yellow
    Write-Host "     Copying from MSYS2..." -ForegroundColor Gray
    if (Test-Path $sslDll) {
        Copy-Item $sslDll -Destination ".\libssl-3-x64.dll" -Force -ErrorAction SilentlyContinue
        Write-Host "     ✅ Copied" -ForegroundColor Green
    }
}

Write-Host ""

# Summary and recommendations
Write-Host "=== Summary ===" -ForegroundColor Cyan
Write-Host ""

if ($needsRebuild) {
    Write-Host "🔧 ACTION REQUIRED: liboqs needs to be rebuilt" -ForegroundColor Yellow
    Write-Host ""
    Write-Host "The issue is that liboqs was built WITHOUT OpenSSL shared linking." -ForegroundColor Yellow
    Write-Host "This causes OQS_SIG_new to return nil even when OpenSSL DLLs are loaded." -ForegroundColor Yellow
    Write-Host ""
    Write-Host "To fix:" -ForegroundColor Cyan
    Write-Host "  1. Run: .\rebuild_liboqs.ps1" -ForegroundColor Green
    Write-Host "  2. Wait for rebuild to complete (may take several minutes)" -ForegroundColor Gray
    Write-Host "  3. Run: .\build.ps1" -ForegroundColor Green
    Write-Host "  4. Run: .\run.ps1" -ForegroundColor Green
    Write-Host ""
    Write-Host "Would you like to rebuild liboqs now? (Y/N)" -ForegroundColor Cyan
    $response = Read-Host
    if ($response -eq "Y" -or $response -eq "y") {
        Write-Host ""
        Write-Host "Starting rebuild..." -ForegroundColor Cyan
        & ".\rebuild_liboqs.ps1"
    }
} else {
    Write-Host "✅ liboqs build configuration looks correct" -ForegroundColor Green
    Write-Host ""
    Write-Host "If OQS_SIG_new still fails, check:" -ForegroundColor Yellow
    Write-Host "  - OpenSSL DLL version matches the version liboqs was built against" -ForegroundColor Gray
    Write-Host "  - All OpenSSL dependencies are available" -ForegroundColor Gray
    Write-Host "  - Run: .\check_liboqs_build.ps1 for more details" -ForegroundColor Gray
}

Write-Host ""

