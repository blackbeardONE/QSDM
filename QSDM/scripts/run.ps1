# PowerShell script to run qsdm.exe with proper environment setup
# This ensures all required DLLs are in PATH

Write-Host "Starting QSDM node..." -ForegroundColor Cyan
Write-Host ""

# Set PATH to include OpenSSL DLLs FIRST (before anything else)
# This is critical - liboqs (even if statically linked) depends on OpenSSL
# Add current directory FIRST (for copied DLLs), then OpenSSL bin
$env:PATH = "$PWD;C:\msys64\mingw64\bin;$env:PATH"

# Add CUDA bin to PATH if available
if (Test-Path "C:\CUDA\bin") {
    $env:PATH = "C:\CUDA\bin;$env:PATH"
}

# Add liboqs DLL directory to PATH if DLL exists locally
$liboqsDllFound = $false
if (Test-Path ".\liboqs.dll") {
    Write-Host "✅ Found liboqs.dll in current directory" -ForegroundColor Green
    $liboqsDllFound = $true
} else {
    # Search for liboqs.dll in installation directory
    $searchPaths = @(
        "D:\Projects\QSDM\liboqs_install\bin",
        "D:\Projects\QSDM\liboqs_install\lib",
        "D:\Projects\QSDM\liboqs_install"
    )
    
    foreach ($searchPath in $searchPaths) {
        if (Test-Path $searchPath) {
            $foundDll = Get-ChildItem -Path $searchPath -Filter "liboqs.dll" -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1
            if ($foundDll) {
                $dllDir = Split-Path $foundDll.FullName -Parent
                $env:PATH = "$dllDir;$env:PATH"
                Write-Host "✅ Found liboqs.dll, added to PATH: $dllDir" -ForegroundColor Green
                $liboqsDllFound = $true
                break
            }
        }
    }
    
    if (-not $liboqsDllFound) {
        Write-Host "⚠️  liboqs.dll not found (may be statically linked)" -ForegroundColor Yellow
        Write-Host "   If runtime fails, check if liboqs was built as dynamic library" -ForegroundColor Gray
    }
}

# Check for OpenSSL DLLs
if (Test-Path ".\libcrypto-3-x64.dll") {
    Write-Host "✅ Found OpenSSL DLLs in current directory" -ForegroundColor Green
} elseif (Test-Path "C:\msys64\mingw64\bin\libcrypto-3-x64.dll") {
    Write-Host "✅ OpenSSL DLLs available in PATH" -ForegroundColor Green
} else {
    Write-Host "⚠️  OpenSSL DLLs not found - may cause issues" -ForegroundColor Yellow
}

Write-Host ""
Write-Host "Running qsdm.exe..." -ForegroundColor Cyan
Write-Host ""

# Run the executable
& ".\qsdm.exe"

# Check exit code
if ($LASTEXITCODE -ne 0 -and $LASTEXITCODE -ne $null) {
    Write-Host ""
    Write-Host "Process exited with code: $LASTEXITCODE" -ForegroundColor Red
    Write-Host ""
    Write-Host "If the application crashed silently:" -ForegroundColor Yellow
    Write-Host "  1. Check Event Viewer: Windows Logs > Application" -ForegroundColor Cyan
    Write-Host "  2. Run: .\run_with_debug.ps1" -ForegroundColor Cyan
    Write-Host "  3. Verify DLLs are available" -ForegroundColor Cyan
}

