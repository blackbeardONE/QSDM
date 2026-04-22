# PowerShell script to diagnose and fix liboqs/OpenSSL initialization issues

Write-Host "Diagnosing liboqs/OpenSSL initialization issue..." -ForegroundColor Cyan
Write-Host ""

# Check OpenSSL DLLs
Write-Host "1. Checking OpenSSL DLLs..." -ForegroundColor Yellow
$opensslDlls = @(
    ".\libcrypto-3-x64.dll",
    ".\libssl-3-x64.dll",
    "C:\msys64\mingw64\bin\libcrypto-3-x64.dll",
    "C:\msys64\mingw64\bin\libssl-3-x64.dll"
)

$foundDlls = @()
foreach ($dll in $opensslDlls) {
    if (Test-Path $dll) {
        $item = Get-Item $dll
        Write-Host "  ✅ Found: $dll" -ForegroundColor Green
        Write-Host "     Size: $($item.Length) bytes" -ForegroundColor Gray
        Write-Host "     Modified: $($item.LastWriteTime)" -ForegroundColor Gray
        $foundDlls += $dll
    }
}

if ($foundDlls.Count -eq 0) {
    Write-Host "  ❌ No OpenSSL DLLs found!" -ForegroundColor Red
    Write-Host "     Please ensure OpenSSL is installed (e.g., via MSYS2)" -ForegroundColor Yellow
    exit 1
}

# Check liboqs installation
Write-Host ""
Write-Host "2. Checking liboqs installation..." -ForegroundColor Yellow
$liboqsPaths = @(
    "D:\Projects\QSDM\liboqs_install",
    "C:\liboqs"
)

$liboqsFound = $false
foreach ($path in $liboqsPaths) {
    if (Test-Path "$path\include\oqs\oqs.h") {
        Write-Host "  ✅ Found liboqs at: $path" -ForegroundColor Green
        
        # Check if static or dynamic
        if (Test-Path "$path\lib\liboqs.a") {
            Write-Host "     Type: Static library (liboqs.a)" -ForegroundColor Cyan
        }
        if (Test-Path "$path\lib\liboqs.dll") {
            Write-Host "     Type: Dynamic library (liboqs.dll)" -ForegroundColor Cyan
        }
        if (Test-Path "$path\bin\liboqs.dll") {
            Write-Host "     Type: Dynamic library (bin\liboqs.dll)" -ForegroundColor Cyan
        }
        
        $liboqsFound = $true
        break
    }
}

if (-not $liboqsFound) {
    Write-Host "  ❌ liboqs not found!" -ForegroundColor Red
    exit 1
}

# Check PATH
Write-Host ""
Write-Host "3. Checking PATH environment..." -ForegroundColor Yellow
$currentPath = $env:PATH
$pathEntries = $currentPath -split ';'

$opensslInPath = $false
$liboqsInPath = $false

foreach ($entry in $pathEntries) {
    if ($entry -like "*msys64*mingw64*bin*" -or $entry -like "*OpenSSL*") {
        Write-Host "  ✅ OpenSSL path in PATH: $entry" -ForegroundColor Green
        $opensslInPath = $true
    }
    if ($entry -like "*liboqs*") {
        Write-Host "  ✅ liboqs path in PATH: $entry" -ForegroundColor Green
        $liboqsInPath = $true
    }
}

if (-not $opensslInPath) {
    Write-Host "  ⚠️  OpenSSL not in PATH" -ForegroundColor Yellow
    Write-Host "     Adding current directory to PATH..." -ForegroundColor Cyan
    $env:PATH = "$PWD;C:\msys64\mingw64\bin;$env:PATH"
    Write-Host "     ✅ PATH updated" -ForegroundColor Green
}

# Check DLL dependencies
Write-Host ""
Write-Host "4. Checking DLL dependencies..." -ForegroundColor Yellow
if (Test-Path ".\qsdmplus.exe") {
    Write-Host "  ✅ qsdmplus.exe found" -ForegroundColor Green
    Write-Host "     Note: Use Dependency Walker or dumpbin to check DLL dependencies" -ForegroundColor Gray
    Write-Host "     Command: dumpbin /dependents qsdmplus.exe" -ForegroundColor Gray
} else {
    Write-Host "  ⚠️  qsdmplus.exe not found - build first" -ForegroundColor Yellow
}

# Test OpenSSL DLL loading
Write-Host ""
Write-Host "5. Testing OpenSSL DLL accessibility..." -ForegroundColor Yellow
$testDll = $null
if (Test-Path ".\libcrypto-3-x64.dll") {
    $testDll = ".\libcrypto-3-x64.dll"
} elseif (Test-Path "C:\msys64\mingw64\bin\libcrypto-3-x64.dll") {
    $testDll = "C:\msys64\mingw64\bin\libcrypto-3-x64.dll"
}

if ($testDll) {
    Write-Host "  Testing DLL: $testDll" -ForegroundColor Cyan
    try {
        # Try to load the DLL using .NET reflection (basic test)
        Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public class DllTest {
    [DllImport("$testDll", CallingConvention = CallingConvention.Cdecl)]
    public static extern int ERR_get_error();
}
"@ -ErrorAction SilentlyContinue
        if ($?) {
            Write-Host "     ✅ DLL can be loaded" -ForegroundColor Green
        } else {
            Write-Host "     ⚠️  DLL load test inconclusive" -ForegroundColor Yellow
        }
    } catch {
        Write-Host "     ⚠️  DLL load test failed: $_" -ForegroundColor Yellow
    }
}

# Recommendations
Write-Host ""
Write-Host "6. Recommendations..." -ForegroundColor Yellow
Write-Host "  - Ensure OpenSSL DLLs are in the same directory as qsdmplus.exe" -ForegroundColor Cyan
Write-Host "  - Or add OpenSSL bin directory to system PATH" -ForegroundColor Cyan
Write-Host "  - Verify liboqs was built with compatible OpenSSL version" -ForegroundColor Cyan
Write-Host "  - Check Event Viewer for DLL loading errors" -ForegroundColor Cyan
Write-Host ""

Write-Host "Diagnosis complete!" -ForegroundColor Green


