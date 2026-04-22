# Test script to verify OpenSSL DLL can be loaded
# This helps diagnose why liboqs can't find OpenSSL

Write-Host "Testing OpenSSL DLL loading..." -ForegroundColor Cyan
Write-Host ""

# Test 1: Check if DLLs exist
$dlls = @(
    ".\libcrypto-3-x64.dll",
    ".\libssl-3-x64.dll",
    "C:\msys64\mingw64\bin\libcrypto-3-x64.dll",
    "C:\msys64\mingw64\bin\libssl-3-x64.dll"
)

Write-Host "1. Checking DLL files..." -ForegroundColor Yellow
$found = $false
foreach ($dll in $dlls) {
    if (Test-Path $dll) {
        Write-Host "  ✅ Found: $dll" -ForegroundColor Green
        $found = $true
    }
}

if (-not $found) {
    Write-Host "  ❌ No OpenSSL DLLs found!" -ForegroundColor Red
    exit 1
}

# Test 2: Try to load DLL using LoadLibrary
Write-Host ""
Write-Host "2. Testing DLL loading..." -ForegroundColor Yellow

Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public class DllLoader {
    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern IntPtr LoadLibrary(string lpFileName);
    
    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool FreeLibrary(IntPtr hModule);
    
    [DllImport("kernel32.dll")]
    public static extern uint GetLastError();
}
"@

$testDll = ".\libcrypto-3-x64.dll"
if (-not (Test-Path $testDll)) {
    $testDll = "C:\msys64\mingw64\bin\libcrypto-3-x64.dll"
}

if (Test-Path $testDll) {
    Write-Host "  Attempting to load: $testDll" -ForegroundColor Cyan
    $handle = [DllLoader]::LoadLibrary($testDll)
    if ($handle -ne [IntPtr]::Zero) {
        Write-Host "  ✅ DLL loaded successfully!" -ForegroundColor Green
        [DllLoader]::FreeLibrary($handle)
    } else {
        $error = [DllLoader]::GetLastError()
        Write-Host "  ❌ Failed to load DLL. Error code: $error" -ForegroundColor Red
        Write-Host "     This may indicate:" -ForegroundColor Yellow
        Write-Host "     - Missing dependencies" -ForegroundColor Yellow
        Write-Host "     - Architecture mismatch (32-bit vs 64-bit)" -ForegroundColor Yellow
        Write-Host "     - Corrupted DLL file" -ForegroundColor Yellow
    }
} else {
    Write-Host "  ❌ Test DLL not found: $testDll" -ForegroundColor Red
}

# Test 3: Check PATH
Write-Host ""
Write-Host "3. Checking PATH..." -ForegroundColor Yellow
$path = $env:PATH -split ';'
$opensslInPath = $false
foreach ($entry in $path) {
    if ($entry -like "*msys64*" -or $entry -like "*openssl*") {
        Write-Host "  ✅ Found in PATH: $entry" -ForegroundColor Green
        $opensslInPath = $true
    }
}

if (-not $opensslInPath) {
    Write-Host "  ⚠️  OpenSSL path not in PATH" -ForegroundColor Yellow
}

Write-Host ""
Write-Host "Test complete!" -ForegroundColor Green


