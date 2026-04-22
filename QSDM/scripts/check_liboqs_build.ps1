# PowerShell script to check liboqs build configuration

Write-Host "=== Checking liboqs Build Configuration ===" -ForegroundColor Cyan
Write-Host ""

$liboqsBuild = "D:\Projects\QSDM\liboqs\build"
$liboqsInstall = "D:\Projects\QSDM\liboqs_install"

# Check if build directory exists
if (Test-Path $liboqsBuild) {
    Write-Host "1. Build directory found: $liboqsBuild" -ForegroundColor Green
    
    # Check CMakeCache.txt for build configuration
    $cacheFile = Join-Path $liboqsBuild "CMakeCache.txt"
    if (Test-Path $cacheFile) {
        Write-Host "   Reading CMakeCache.txt..." -ForegroundColor Yellow
        
        $cacheContent = Get-Content $cacheFile
        
        # Check for OpenSSL configuration
        $opensslShared = $cacheContent | Select-String "OQS_USE_OPENSSL_SHARED"
        $opensslRoot = $cacheContent | Select-String "OPENSSL_ROOT_DIR"
        
        Write-Host ""
        Write-Host "   Build Configuration:" -ForegroundColor Cyan
        if ($opensslShared) {
            Write-Host "   $opensslShared" -ForegroundColor $(if ($opensslShared -match "ON") { "Green" } else { "Yellow" })
        }
        if ($opensslRoot) {
            Write-Host "   $opensslRoot" -ForegroundColor Gray
        }
        
        if ($opensslShared -and $opensslShared -notmatch "ON") {
            Write-Host ""
            Write-Host "   ⚠️  liboqs was NOT built with OpenSSL shared linking!" -ForegroundColor Yellow
            Write-Host "   This is likely the cause of OQS_SIG_new returning nil." -ForegroundColor Yellow
            Write-Host ""
            Write-Host "   Solution: Run .\rebuild_liboqs.ps1" -ForegroundColor Cyan
        } elseif ($opensslShared -and $opensslShared -match "ON") {
            Write-Host ""
            Write-Host "   ✅ liboqs was built with OpenSSL shared linking" -ForegroundColor Green
        }
    } else {
        Write-Host "   ⚠️  CMakeCache.txt not found - build may not be configured" -ForegroundColor Yellow
    }
} else {
    Write-Host "1. Build directory not found: $liboqsBuild" -ForegroundColor Yellow
    Write-Host "   liboqs needs to be built first" -ForegroundColor Yellow
}

Write-Host ""

# Check installed liboqs
Write-Host "2. Checking installed liboqs..." -ForegroundColor Green
if (Test-Path "$liboqsInstall\lib\liboqs.a") {
    $liboqsInfo = Get-Item "$liboqsInstall\lib\liboqs.a"
    Write-Host "   ✅ Found liboqs.a" -ForegroundColor Green
    Write-Host "      Size: $([math]::Round($liboqsInfo.Length / 1MB, 2)) MB" -ForegroundColor Gray
    Write-Host "      Modified: $($liboqsInfo.LastWriteTime)" -ForegroundColor Gray
} else {
    Write-Host "   ❌ liboqs.a not found" -ForegroundColor Red
}

Write-Host ""

# Check if rebuild is needed
Write-Host "3. Recommendation:" -ForegroundColor Green
if (Test-Path $liboqsBuild) {
    $cacheFile = Join-Path $liboqsBuild "CMakeCache.txt"
    if (Test-Path $cacheFile) {
        $cacheContent = Get-Content $cacheFile
        $opensslShared = $cacheContent | Select-String "OQS_USE_OPENSSL_SHARED"
        
        if ($opensslShared -and $opensslShared -notmatch "ON") {
            Write-Host "   🔧 Rebuild required with: .\rebuild_liboqs.ps1" -ForegroundColor Yellow
        } else {
            Write-Host "   ✅ Build configuration looks correct" -ForegroundColor Green
            Write-Host "   If OQS_SIG_new still fails, check:" -ForegroundColor Yellow
            Write-Host "     - OpenSSL DLL version matches build" -ForegroundColor Gray
            Write-Host "     - All OpenSSL dependencies are available" -ForegroundColor Gray
        }
    } else {
        Write-Host "   🔧 Build not configured. Run: .\rebuild_liboqs.ps1" -ForegroundColor Yellow
    }
} else {
    Write-Host "   🔧 Build directory missing. Run: .\rebuild_liboqs.ps1" -ForegroundColor Yellow
}

Write-Host ""

