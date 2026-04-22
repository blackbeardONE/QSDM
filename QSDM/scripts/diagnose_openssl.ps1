# PowerShell script to diagnose OpenSSL DLL compatibility with liboqs

Write-Host "=== OpenSSL DLL Diagnostic ===" -ForegroundColor Cyan
Write-Host ""

# Check for OpenSSL DLLs
Write-Host "1. Checking for OpenSSL DLLs..." -ForegroundColor Green
$cryptoDll = ".\libcrypto-3-x64.dll"
$sslDll = ".\libssl-3-x64.dll"

if (Test-Path $cryptoDll) {
    Write-Host "   ✅ Found $cryptoDll" -ForegroundColor Green
    $cryptoInfo = Get-Item $cryptoDll
    Write-Host "      Size: $($cryptoInfo.Length) bytes"
    Write-Host "      Modified: $($cryptoInfo.LastWriteTime)"
} else {
    Write-Host "   ❌ Missing $cryptoDll" -ForegroundColor Red
}

if (Test-Path $sslDll) {
    Write-Host "   ✅ Found $sslDll" -ForegroundColor Green
    $sslInfo = Get-Item $sslDll
    Write-Host "      Size: $($sslInfo.Length) bytes"
    Write-Host "      Modified: $($sslInfo.LastWriteTime)"
} else {
    Write-Host "   ❌ Missing $sslDll" -ForegroundColor Red
}

Write-Host ""

# Check MSYS2 OpenSSL
Write-Host "2. Checking MSYS2 OpenSSL installation..." -ForegroundColor Green
$msys2Crypto = "C:\msys64\mingw64\bin\libcrypto-3-x64.dll"
$msys2Ssl = "C:\msys64\mingw64\bin\libssl-3-x64.dll"

if (Test-Path $msys2Crypto) {
    Write-Host "   ✅ Found MSYS2 libcrypto" -ForegroundColor Green
} else {
    Write-Host "   ❌ MSYS2 libcrypto not found" -ForegroundColor Yellow
}

if (Test-Path $msys2Ssl) {
    Write-Host "   ✅ Found MSYS2 libssl" -ForegroundColor Green
} else {
    Write-Host "   ❌ MSYS2 libssl not found" -ForegroundColor Yellow
}

Write-Host ""

# Check liboqs installation
Write-Host "3. Checking liboqs installation..." -ForegroundColor Green
$liboqsPath = "D:\Projects\QSDM\liboqs_install"
if (Test-Path "$liboqsPath\lib\liboqs.a") {
    Write-Host "   ✅ Found liboqs.a (statically linked)" -ForegroundColor Green
    Write-Host "      Path: $liboqsPath\lib\liboqs.a"
} else {
    Write-Host "   ❌ liboqs.a not found" -ForegroundColor Red
}

Write-Host ""

# Recommendations
Write-Host "=== Recommendations ===" -ForegroundColor Cyan
Write-Host ""
Write-Host "If OQS_SIG_new returns nil even though DLLs are preloaded:" -ForegroundColor Yellow
Write-Host ""
Write-Host "1. Rebuild liboqs with matching OpenSSL:" -ForegroundColor Green
Write-Host "   cd D:\Projects\QSDM\liboqs" -ForegroundColor Gray
Write-Host "   mkdir build && cd build" -ForegroundColor Gray
Write-Host "   cmake -DCMAKE_INSTALL_PREFIX=D:/Projects/QSDM/liboqs_install \" -ForegroundColor Gray
Write-Host "         -DOQS_USE_OPENSSL_SHARED=ON \" -ForegroundColor Gray
Write-Host "         -DOPENSSL_ROOT_DIR=C:/msys64/mingw64 .." -ForegroundColor Gray
Write-Host "   cmake --build . --config Release" -ForegroundColor Gray
Write-Host "   cmake --install ." -ForegroundColor Gray
Write-Host ""
Write-Host "2. Or rebuild liboqs to statically link OpenSSL completely:" -ForegroundColor Green
Write-Host "   cmake -DCMAKE_INSTALL_PREFIX=D:/Projects/QSDM/liboqs_install \" -ForegroundColor Gray
Write-Host "         -DOQS_USE_OPENSSL_SHARED=OFF \" -ForegroundColor Gray
Write-Host "         -DOPENSSL_ROOT_DIR=C:/msys64/mingw64 .." -ForegroundColor Gray
Write-Host ""
Write-Host "3. Verify OpenSSL DLL dependencies:" -ForegroundColor Green
Write-Host "   Use Dependency Walker or:" -ForegroundColor Gray
Write-Host "   dumpbin /dependents libcrypto-3-x64.dll" -ForegroundColor Gray
Write-Host ""
Write-Host "4. Check if DLLs match liboqs build:" -ForegroundColor Green
Write-Host "   - liboqs was built against OpenSSL from MSYS2" -ForegroundColor Gray
Write-Host "   - Ensure DLLs are from the same OpenSSL version" -ForegroundColor Gray
Write-Host "   - Copy DLLs from C:\msys64\mingw64\bin to current directory" -ForegroundColor Gray
Write-Host ""

