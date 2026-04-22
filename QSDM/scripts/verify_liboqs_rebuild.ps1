# Verify liboqs rebuild and check for issues

Write-Host "=== Verifying liboqs Rebuild ===" -ForegroundColor Cyan
Write-Host ""

# Check liboqs.a modification time
$liboqsA = "D:\Projects\QSDM\liboqs_install\lib\liboqs.a"
if (Test-Path $liboqsA) {
    $info = Get-Item $liboqsA
    Write-Host "1. liboqs.a Status:" -ForegroundColor Green
    Write-Host "   Modified: $($info.LastWriteTime)" -ForegroundColor Gray
    Write-Host "   Size: $([math]::Round($info.Length / 1MB, 2)) MB" -ForegroundColor Gray
    
    $rebuildTime = Get-Date "2025-12-15 18:00:00"
    if ($info.LastWriteTime -gt $rebuildTime) {
        Write-Host "   ✅ Recently rebuilt (after 6 PM today)" -ForegroundColor Green
    } else {
        Write-Host "   ⚠️  Not recently rebuilt" -ForegroundColor Yellow
    }
} else {
    Write-Host "   ❌ liboqs.a not found!" -ForegroundColor Red
}

Write-Host ""

# Check CMakeCache
$cacheFile = "D:\Projects\QSDM\liboqs\build\CMakeCache.txt"
if (Test-Path $cacheFile) {
    Write-Host "2. CMake Configuration:" -ForegroundColor Green
    $cache = Get-Content $cacheFile
    
    $opensslShared = $cache | Select-String "OQS_USE_OPENSSL_SHARED"
    $opensslRoot = $cache | Select-String "OPENSSL_ROOT_DIR:"
    
    if ($opensslShared) {
        Write-Host "   $opensslShared" -ForegroundColor $(if ($opensslShared -match "ON") { "Green" } else { "Yellow" })
    }
    if ($opensslRoot) {
        Write-Host "   $opensslRoot" -ForegroundColor Gray
    }
} else {
    Write-Host "   ⚠️  CMakeCache.txt not found" -ForegroundColor Yellow
}

Write-Host ""

# Check if build completed
$buildDir = "D:\Projects\QSDM\liboqs\build"
if (Test-Path $buildDir) {
    Write-Host "3. Build Directory:" -ForegroundColor Green
    $buildFiles = Get-ChildItem $buildDir -Filter "*.a" -Recurse -ErrorAction SilentlyContinue
    if ($buildFiles) {
        Write-Host "   Found $($buildFiles.Count) .a files in build directory" -ForegroundColor Gray
        $latest = $buildFiles | Sort-Object LastWriteTime -Descending | Select-Object -First 1
        Write-Host "   Latest: $($latest.Name) - $($latest.LastWriteTime)" -ForegroundColor Gray
    }
}

Write-Host ""

# Recommendations
Write-Host "=== Analysis ===" -ForegroundColor Cyan
Write-Host ""

if (Test-Path $liboqsA) {
    $info = Get-Item $liboqsA
    if ($info.LastWriteTime -gt (Get-Date "2025-12-15 18:00:00")) {
        Write-Host "liboqs was rebuilt, but OQS_SIG_new still fails." -ForegroundColor Yellow
        Write-Host ""
        Write-Host "Possible issues:" -ForegroundColor Yellow
        Write-Host "  1. Build may have failed silently" -ForegroundColor Gray
        Write-Host "  2. Wrong OpenSSL version in DLLs vs build" -ForegroundColor Gray
        Write-Host "  3. Missing OpenSSL dependencies" -ForegroundColor Gray
        Write-Host ""
        Write-Host "Try:" -ForegroundColor Cyan
        Write-Host "  1. Check build logs in liboqs\build\" -ForegroundColor Gray
        Write-Host "  2. Verify OpenSSL DLL version:" -ForegroundColor Gray
        Write-Host "     Get-Item libcrypto-3-x64.dll | Select VersionInfo" -ForegroundColor Gray
        Write-Host "  3. Rebuild with verbose output:" -ForegroundColor Gray
        Write-Host "     cd liboqs\build" -ForegroundColor Gray
        Write-Host "     cmake --build . --config Release --verbose" -ForegroundColor Gray
    } else {
        Write-Host "liboqs needs to be rebuilt!" -ForegroundColor Red
        Write-Host "Run: .\fix_liboqs.ps1" -ForegroundColor Cyan
    }
}

Write-Host ""

