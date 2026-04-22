# Script to fix DLL dependencies for QSDM

Write-Host "Fixing DLL dependencies..." -ForegroundColor Cyan
Write-Host ""

# Copy OpenSSL DLLs to project directory
$opensslPath = "C:\msys64\mingw64\bin"
$dlls = @("libcrypto-3-x64.dll", "libssl-3-x64.dll")

Write-Host "Copying OpenSSL DLLs..." -ForegroundColor Yellow
foreach ($dll in $dlls) {
    $src = Join-Path $opensslPath $dll
    $dst = Join-Path (Get-Location) $dll
    
    if (Test-Path $src) {
        Copy-Item $src $dst -Force -ErrorAction SilentlyContinue
        if (Test-Path $dst) {
            Write-Host "  ✓ Copied $dll" -ForegroundColor Green
        } else {
            Write-Host "  ✗ Failed to copy $dll" -ForegroundColor Red
        }
    } else {
        Write-Host "  ⚠ $dll not found at $src" -ForegroundColor Yellow
    }
}

Write-Host ""
Write-Host "Checking for liboqs DLL..." -ForegroundColor Yellow
$liboqsPaths = @(
    "D:\Projects\QSDM\liboqs_install\bin",
    "D:\Projects\QSDM\liboqs_install\lib",
    "C:\liboqs\bin",
    "C:\liboqs\lib"
)

$liboqsDllFound = $false
foreach ($path in $liboqsPaths) {
    if (Test-Path $path) {
        $dlls = Get-ChildItem -Path $path -Filter "*.dll" -ErrorAction SilentlyContinue
        if ($dlls) {
            Write-Host "  Found liboqs DLLs in $path :" -ForegroundColor Green
            foreach ($dll in $dlls) {
                Write-Host "    - $($dll.Name)" -ForegroundColor Gray
                # Copy to project directory
                Copy-Item $dll.FullName (Join-Path (Get-Location) $dll.Name) -Force -ErrorAction SilentlyContinue
                $liboqsDllFound = $true
            }
        }
    }
}

if (-not $liboqsDllFound) {
    Write-Host "  ℹ liboqs appears to be static (no DLL needed)" -ForegroundColor Cyan
}

Write-Host ""
Write-Host "DLL setup complete!" -ForegroundColor Green

