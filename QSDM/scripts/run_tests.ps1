# PowerShell script to run comprehensive tests with proper Go environment

Write-Host "Running QSDM Comprehensive Tests..." -ForegroundColor Cyan
Write-Host ""

# Set Go environment
$env:GOROOT = "C:\Program Files\Go"
$env:PATH = "C:\Program Files\Go\bin;$env:PATH"

# Clear any stale CGO environment variables that might cause issues
Remove-Item Env:\CGO_CFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:\CGO_LDFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:\CGO_CPPFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:\CGO_CXXFLAGS -ErrorAction SilentlyContinue

# For tests, we can run without CGO to avoid build issues
# Set CGO_ENABLED=0 for tests (tests will skip CGO-dependent features)
$env:CGO_ENABLED = "0"

# Clean up vendor directory if it has inconsistencies
if (Test-Path "vendor") {
    Write-Host "Checking vendor directory..." -ForegroundColor Yellow
    $vendorCheck = go mod vendor 2>&1
    if ($LASTEXITCODE -ne 0 -or $vendorCheck -match "inconsistent") {
        Write-Host "Removing inconsistent vendor directory..." -ForegroundColor Yellow
        Remove-Item -Recurse -Force vendor -ErrorAction SilentlyContinue
        Write-Host "Vendor directory removed. Using -mod=mod for tests." -ForegroundColor Green
    }
}

# Run integration tests (use -mod=mod to ignore vendor if needed)
Write-Host ""
Write-Host "=== Integration Tests ===" -ForegroundColor Green
go test -mod=mod ./tests/... -v -run TestFullTransactionLifecycleComprehensive 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Host "Integration tests failed" -ForegroundColor Red
    exit 1
}

# Run performance benchmarks
Write-Host ""
Write-Host "=== Performance Benchmarks ===" -ForegroundColor Green
go test -mod=mod -bench=. -benchmem ./tests/... -run=^$ 2>&1 | Select-Object -First 50

Write-Host ""
Write-Host "Tests completed!" -ForegroundColor Green

