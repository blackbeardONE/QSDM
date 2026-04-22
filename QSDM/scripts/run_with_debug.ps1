# PowerShell script to run qsdmplus.exe with detailed debugging
# This helps diagnose silent crashes

Write-Host "Running qsdmplus.exe with debug output..." -ForegroundColor Cyan
Write-Host ""

# Set PATH to include OpenSSL DLLs FIRST (critical for liboqs)
$env:PATH = "C:\msys64\mingw64\bin;$env:PATH"

# Add current directory to PATH (for copied DLLs)
$env:PATH = "$PWD;$env:PATH"

# Check for required DLLs
Write-Host "Checking for required DLLs..." -ForegroundColor Cyan
$requiredDlls = @(
    "C:\msys64\mingw64\bin\libcrypto-3-x64.dll",
    "C:\msys64\mingw64\bin\libssl-3-x64.dll"
)

foreach ($dll in $requiredDlls) {
    if (Test-Path $dll) {
        Write-Host "  ✅ Found: $dll" -ForegroundColor Green
    } else {
        Write-Host "  ❌ Missing: $dll" -ForegroundColor Red
    }
}

# Check for liboqs DLL
$liboqsDlls = @(
    "D:\Projects\QSDM\liboqs_install\bin\liboqs.dll",
    "D:\Projects\QSDM\liboqs_install\lib\liboqs.dll",
    "C:\liboqs\bin\liboqs.dll"
)

$liboqsFound = $false
foreach ($dll in $liboqsDlls) {
    if (Test-Path $dll) {
        Write-Host "  ✅ Found liboqs: $dll" -ForegroundColor Green
        $env:PATH = "$(Split-Path $dll -Parent);$env:PATH"
        $liboqsFound = $true
        break
    }
}

if (-not $liboqsFound) {
    Write-Host "  ⚠️  liboqs.dll not found (may be statically linked)" -ForegroundColor Yellow
}

Write-Host ""
Write-Host "Starting qsdmplus.exe..." -ForegroundColor Cyan
Write-Host ""

# Run with output capture
$process = Start-Process -FilePath ".\qsdmplus.exe" -NoNewWindow -PassThru -RedirectStandardOutput "stdout_debug.txt" -RedirectStandardError "stderr_debug.txt"

# Wait a moment to see if it crashes immediately
Start-Sleep -Seconds 3

if ($process.HasExited) {
    Write-Host "❌ Process exited with code: $($process.ExitCode)" -ForegroundColor Red
    Write-Host ""
    Write-Host "Standard Output:" -ForegroundColor Yellow
    if (Test-Path "stdout_debug.txt") {
        Get-Content "stdout_debug.txt" | Write-Host
    } else {
        Write-Host "  (no output)" -ForegroundColor Gray
    }
    Write-Host ""
    Write-Host "Standard Error:" -ForegroundColor Yellow
    if (Test-Path "stderr_debug.txt") {
        Get-Content "stderr_debug.txt" | Write-Host
    } else {
        Write-Host "  (no error output)" -ForegroundColor Gray
    }
    Write-Host ""
    Write-Host "Check Event Viewer for crash details:" -ForegroundColor Cyan
    Write-Host "  Windows Logs > Application" -ForegroundColor Gray
} else {
    Write-Host "✅ Process is running (PID: $($process.Id))" -ForegroundColor Green
    Write-Host "Press Ctrl+C to stop..." -ForegroundColor Yellow
    try {
        $process.WaitForExit()
    } catch {
        Write-Host "Process stopped." -ForegroundColor Yellow
    }
}
