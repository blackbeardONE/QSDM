# Script to copy all required DLLs to project directory

Write-Host "=== Copying Required DLLs ===" -ForegroundColor Cyan
Write-Host ""

$copied = @()

# 1. OpenSSL DLLs
Write-Host "1. OpenSSL DLLs:" -ForegroundColor Yellow
$opensslDlls = @(
    "C:\msys64\mingw64\bin\libcrypto-3-x64.dll",
    "C:\msys64\mingw64\bin\libssl-3-x64.dll"
)

foreach ($dll in $opensslDlls) {
    if (Test-Path $dll) {
        $name = Split-Path $dll -Leaf
        Copy-Item $dll -Destination "." -Force -ErrorAction SilentlyContinue
        Write-Host "  ✓ Copied $name" -ForegroundColor Green
        $copied += $name
    } else {
        Write-Host "  ✗ Not found: $dll" -ForegroundColor Red
    }
}

Write-Host ""

# 2. liboqs DLL
Write-Host "2. liboqs DLL:" -ForegroundColor Yellow
$liboqsDll = "D:\Projects\QSDM\liboqs_install\lib\liboqs.dll"
if (Test-Path $liboqsDll) {
    Copy-Item $liboqsDll -Destination "." -Force -ErrorAction SilentlyContinue
    Write-Host "  ✓ Copied liboqs.dll" -ForegroundColor Green
    $copied += "liboqs.dll"
} else {
    Write-Host "  ℹ liboqs.dll not found (may be static library)" -ForegroundColor Gray
}

Write-Host ""

# 3. wasmtime DLLs
Write-Host "3. wasmtime DLLs:" -ForegroundColor Yellow
$wasmtimeDlls = Get-ChildItem -Path "vendor" -Recurse -Filter "*wasmtime*.dll" -ErrorAction SilentlyContinue
if ($wasmtimeDlls) {
    foreach ($dll in $wasmtimeDlls) {
        $name = $dll.Name
        Copy-Item $dll.FullName -Destination "." -Force -ErrorAction SilentlyContinue
        Write-Host "  ✓ Copied $name" -ForegroundColor Green
        $copied += $name
    }
} else {
    Write-Host "  ℹ No wasmtime DLLs found" -ForegroundColor Gray
}

Write-Host ""

# Summary
Write-Host "=== Summary ===" -ForegroundColor Cyan
Write-Host "Copied $($copied.Count) DLL(s):" -ForegroundColor Yellow
foreach ($dll in $copied) {
    Write-Host "  - $dll" -ForegroundColor Gray
}

Write-Host ""
Write-Host "Now try running: .\run_qsdm.ps1" -ForegroundColor Cyan

