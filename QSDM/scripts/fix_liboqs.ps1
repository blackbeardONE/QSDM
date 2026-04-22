# Quick fix script to rebuild liboqs with OpenSSL shared linking
# This fixes the OQS_SIG_new returning nil issue

Write-Host "=== Fixing liboqs OpenSSL Configuration ===" -ForegroundColor Cyan
Write-Host ""

$ErrorActionPreference = "Stop"

try {
    # Step 1: Navigate to liboqs
    Write-Host "1. Navigating to liboqs source..." -ForegroundColor Green
    Set-Location "D:\Projects\QSDM\liboqs"
    
    # Step 2: Remove old build
    Write-Host "2. Removing old build..." -ForegroundColor Green
    if (Test-Path "build") {
        Remove-Item -Recurse -Force "build"
    }
    New-Item -ItemType Directory -Path "build" | Out-Null
    Set-Location "build"
    
    # Step 3: Configure with OpenSSL shared
    Write-Host "3. Configuring CMake with OpenSSL shared linking..." -ForegroundColor Green
    Write-Host "   This may take a minute..." -ForegroundColor Gray
    
    & cmake `
        -DCMAKE_INSTALL_PREFIX="D:/Projects/QSDM/liboqs_install" `
        -DOQS_USE_OPENSSL=ON `
        -DOQS_USE_OPENSSL_SHARED=ON `
        -DOPENSSL_ROOT_DIR="C:/msys64/mingw64" `
        -G "MinGW Makefiles" `
        ..
    
    if ($LASTEXITCODE -ne 0) {
        throw "CMake configuration failed"
    }
    Write-Host "   ✅ Configuration successful" -ForegroundColor Green
    Write-Host ""
    
    # Step 4: Build
    Write-Host "4. Building liboqs (this will take 5-10 minutes)..." -ForegroundColor Green
    Write-Host "   Please be patient..." -ForegroundColor Gray
    Write-Host ""
    
    & cmake --build . --config Release
    
    if ($LASTEXITCODE -ne 0) {
        throw "Build failed"
    }
    Write-Host "   ✅ Build successful" -ForegroundColor Green
    Write-Host ""
    
    # Step 5: Install
    Write-Host "5. Installing liboqs..." -ForegroundColor Green
    & cmake --install .
    
    if ($LASTEXITCODE -ne 0) {
        throw "Installation failed"
    }
    Write-Host "   ✅ Installation successful" -ForegroundColor Green
    Write-Host ""
    
    # Step 6: Copy DLLs
    Write-Host "6. Copying OpenSSL DLLs..." -ForegroundColor Green
    Copy-Item "C:\msys64\mingw64\bin\libcrypto-3-x64.dll" -Destination "D:\Projects\QSDM\" -Force
    Copy-Item "C:\msys64\mingw64\bin\libssl-3-x64.dll" -Destination "D:\Projects\QSDM\" -Force
    Write-Host "   ✅ DLLs copied" -ForegroundColor Green
    Write-Host ""
    
    # Return to project root
    Set-Location "D:\Projects\QSDM"
    
    Write-Host "=== SUCCESS ===" -ForegroundColor Green
    Write-Host ""
    Write-Host "liboqs has been rebuilt with OpenSSL shared linking!" -ForegroundColor Green
    Write-Host ""
    Write-Host "Next steps:" -ForegroundColor Yellow
    Write-Host "  1. Rebuild QSDM: .\build.ps1" -ForegroundColor Cyan
    Write-Host "  2. Run QSDM: .\run.ps1" -ForegroundColor Cyan
    Write-Host "  3. Check health: consensus and wallet should be 'healthy'" -ForegroundColor Cyan
    Write-Host ""
    
} catch {
    Write-Host ""
    Write-Host "=== ERROR ===" -ForegroundColor Red
    Write-Host "Failed: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host ""
    Write-Host "Please check:" -ForegroundColor Yellow
    Write-Host "  - CMake is installed and in PATH" -ForegroundColor Gray
    Write-Host "  - MinGW Makefiles generator is available" -ForegroundColor Gray
    Write-Host "  - OpenSSL is installed at C:\msys64\mingw64" -ForegroundColor Gray
    Write-Host ""
    Set-Location "D:\Projects\QSDM"
    exit 1
}

