# PowerShell script to download and set up liboqs for QSDM
# This script clones, builds, and installs liboqs automatically

Write-Host "Downloading and setting up liboqs..." -ForegroundColor Cyan
Write-Host ""

$liboqsDir = "D:\Projects\QSDM\liboqs"
$buildDir = "$liboqsDir\build"
$installDir = "D:\Projects\QSDM\liboqs_install"

# Step 1: Clone liboqs repository
if (Test-Path $liboqsDir) {
    Write-Host "liboqs repository already exists, updating..." -ForegroundColor Yellow
    Push-Location $liboqsDir
    git pull
    Pop-Location
} else {
    Write-Host "Cloning liboqs repository..." -ForegroundColor Cyan
    git clone --depth 1 https://github.com/open-quantum-safe/liboqs.git $liboqsDir
    if ($LASTEXITCODE -ne 0) {
        Write-Host "ERROR: Failed to clone liboqs repository" -ForegroundColor Red
        exit 1
    }
    Write-Host "Repository cloned successfully" -ForegroundColor Green
}

# Step 2: Create build directory
if (-not (Test-Path $buildDir)) {
    New-Item -ItemType Directory -Path $buildDir | Out-Null
    Write-Host "Created build directory" -ForegroundColor Green
}

# Step 3: Configure with CMake
Write-Host ""
Write-Host "Configuring liboqs build..." -ForegroundColor Cyan
Push-Location $buildDir

# Check for MinGW or MSVC
$cmakeGenerator = "MinGW Makefiles"
if (Get-Command cl.exe -ErrorAction SilentlyContinue) {
    $cmakeGenerator = "Visual Studio 17 2022"
    Write-Host "Using Visual Studio generator" -ForegroundColor Yellow
} else {
    Write-Host "Using MinGW generator" -ForegroundColor Yellow
}

cmake -G $cmakeGenerator `
    -DCMAKE_INSTALL_PREFIX=$installDir `
    -DCMAKE_BUILD_TYPE=Release `
    ..

if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: CMake configuration failed" -ForegroundColor Red
    Pop-Location
    exit 1
}
Write-Host "CMake configuration successful" -ForegroundColor Green

# Step 4: Build liboqs
Write-Host ""
Write-Host "Building liboqs (this may take several minutes)..." -ForegroundColor Cyan
if ($cmakeGenerator -like "*Visual Studio*") {
    cmake --build . --config Release --parallel
} else {
    cmake --build . --config Release -j4
}

if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Build failed" -ForegroundColor Red
    Pop-Location
    exit 1
}
Write-Host "Build successful" -ForegroundColor Green

# Step 5: Install liboqs
Write-Host ""
Write-Host "Installing liboqs to $installDir..." -ForegroundColor Cyan
cmake --install . --config Release

if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Installation failed" -ForegroundColor Red
    Pop-Location
    exit 1
}
Write-Host "Installation successful" -ForegroundColor Green

Pop-Location

# Step 6: Verify installation
Write-Host ""
Write-Host "Verifying installation..." -ForegroundColor Cyan
if (Test-Path "$installDir\include\oqs\oqs.h") {
    Write-Host "✓ Header files found" -ForegroundColor Green
} else {
    Write-Host "✗ Header files not found" -ForegroundColor Red
}

if (Test-Path "$installDir\lib\liboqs.a") {
    Write-Host "✓ Library files found (static)" -ForegroundColor Green
} elseif (Test-Path "$installDir\lib\oqs.lib") {
    Write-Host "✓ Library files found (Windows)" -ForegroundColor Green
} else {
    Write-Host "✗ Library files not found" -ForegroundColor Red
}

Write-Host ""
Write-Host "liboqs setup complete!" -ForegroundColor Green
Write-Host "Installation location: $installDir" -ForegroundColor Cyan
Write-Host ""
Write-Host "You can now build QSDM with CGO:" -ForegroundColor Yellow
Write-Host "  .\build_with_cgo.ps1" -ForegroundColor Cyan

