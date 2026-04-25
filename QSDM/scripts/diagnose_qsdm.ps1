# Diagnostic script for qsdm.exe
Write-Host "=== QSDM Diagnostic ===" -ForegroundColor Cyan
Write-Host ""

# Check if executable exists
if (-not (Test-Path ".\qsdm.exe")) {
    Write-Host "ERROR: qsdm.exe not found!" -ForegroundColor Red
    Write-Host "Run .\build.ps1 first to build the executable" -ForegroundColor Yellow
    exit 1
}

Write-Host "1. Executable found" -ForegroundColor Green

# Check for required DLLs
Write-Host ""
Write-Host "2. Checking for required DLLs..." -ForegroundColor Cyan
$dlls = @(
    ".\libcrypto-3-x64.dll",
    ".\libssl-3-x64.dll",
    ".\cudart64_12.dll"
)

foreach ($dll in $dlls) {
    if (Test-Path $dll) {
        Write-Host "   ✅ $dll" -ForegroundColor Green
    } else {
        Write-Host "   ⚠️  $dll (not found)" -ForegroundColor Yellow
    }
}

# Set up PATH
Write-Host ""
Write-Host "3. Setting up environment..." -ForegroundColor Cyan
$env:PATH = "$PWD;C:\msys64\mingw64\bin;$env:PATH"
if (Test-Path "C:\CUDA\bin") {
    $env:PATH = "C:\CUDA\bin;$env:PATH"
    Write-Host "   ✅ Added CUDA to PATH" -ForegroundColor Green
}

# Try to start the process
Write-Host ""
Write-Host "4. Starting qsdm.exe..." -ForegroundColor Cyan
Write-Host "   (Capturing output for 5 seconds...)" -ForegroundColor Gray

$process = Start-Process -FilePath ".\qsdm.exe" -NoNewWindow -PassThru -RedirectStandardOutput "qsdm_output.txt" -RedirectStandardError "qsdm_error.txt"

Start-Sleep -Seconds 5

if ($process.HasExited) {
    Write-Host "   ❌ Process exited with code: $($process.ExitCode)" -ForegroundColor Red
    Write-Host ""
    Write-Host "=== Output ===" -ForegroundColor Yellow
    if (Test-Path "qsdm_output.txt") {
        Get-Content "qsdm_output.txt"
    } else {
        Write-Host "(no output)" -ForegroundColor Gray
    }
    Write-Host ""
    Write-Host "=== Errors ===" -ForegroundColor Yellow
    if (Test-Path "qsdm_error.txt") {
        Get-Content "qsdm_error.txt"
    } else {
        Write-Host "(no errors)" -ForegroundColor Gray
    }
} else {
    Write-Host "   ✅ Process is running (PID: $($process.Id))" -ForegroundColor Green
    Write-Host ""
    Write-Host "=== Initial Output ===" -ForegroundColor Yellow
    if (Test-Path "qsdm_output.txt") {
        Get-Content "qsdm_output.txt" | Select-Object -First 30
    }
    Write-Host ""
    Write-Host "Application is running. Check:" -ForegroundColor Cyan
    Write-Host "  - Dashboard: http://localhost:8081" -ForegroundColor Gray
    Write-Host "  - Log viewer: http://localhost:8080" -ForegroundColor Gray
    Write-Host ""
    Write-Host "To stop the process, run:" -ForegroundColor Yellow
    Write-Host "  Stop-Process -Id $($process.Id)" -ForegroundColor Gray
}

