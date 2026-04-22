# Test with minimal environment to isolate the issue
Write-Host "Testing minimal startup..." -ForegroundColor Cyan
Write-Host ""

# Check if we can even load the executable
if (-not (Test-Path ".\qsdmplus.exe")) {
    Write-Host "ERROR: qsdmplus.exe not found!" -ForegroundColor Red
    exit 1
}

Write-Host "Executable found" -ForegroundColor Green
Write-Host ""

# Check DLL dependencies using dumpbin if available
$dumpbin = Get-Command dumpbin -ErrorAction SilentlyContinue
if ($dumpbin) {
    Write-Host "Checking DLL dependencies..." -ForegroundColor Cyan
    dumpbin /dependents qsdmplus.exe | Select-String -Pattern "\.dll" | ForEach-Object {
        Write-Host "  $_" -ForegroundColor Gray
    }
    Write-Host ""
}

# Try running with minimal environment
Write-Host "Attempting to run with minimal PATH..." -ForegroundColor Cyan
$env:PATH = "$PWD;C:\msys64\mingw64\bin"

# Set custom ports
$env:DASHBOARD_PORT = "8082"
$env:LOG_VIEWER_PORT = "8083"

Write-Host "PATH: $env:PATH" -ForegroundColor Gray
Write-Host ""

# Try to run
Write-Host "Starting qsdmplus.exe..." -ForegroundColor Cyan
& ".\qsdmplus.exe"

if ($LASTEXITCODE -ne 0) {
    Write-Host ""
    Write-Host "Exit code: $LASTEXITCODE" -ForegroundColor Red
    if ($LASTEXITCODE -eq -1073741571) {
        Write-Host ""
        Write-Host "STATUS_ACCESS_VIOLATION detected" -ForegroundColor Yellow
        Write-Host "This usually means a DLL dependency issue" -ForegroundColor Gray
        Write-Host ""
        Write-Host "Check Event Viewer for detailed crash information:" -ForegroundColor Cyan
        Write-Host "  Windows Logs > Application" -ForegroundColor Gray
    }
}

