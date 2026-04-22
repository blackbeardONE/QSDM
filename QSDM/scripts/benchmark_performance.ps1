# Performance Benchmarking Script for QSDM
# Tests signing, verification, compression, and batch operations

Write-Host "=== QSDM Performance Benchmarking ===" -ForegroundColor Cyan
Write-Host ""

# Check if Go is available
$goVersion = go version 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Host "❌ Go is not available. Please install Go first." -ForegroundColor Red
    exit 1
}

Write-Host "Go version: $goVersion" -ForegroundColor Gray
Write-Host ""

# Set CGO environment
$env:CGO_ENABLED = "1"

# Check if liboqs is available
$liboqsPath = "liboqs_install"
if (-not (Test-Path "$liboqsPath\lib\liboqs.dll.a")) {
    Write-Host "⚠️  Warning: liboqs not found. Some benchmarks may be skipped." -ForegroundColor Yellow
    Write-Host ""
}

Write-Host "Running performance benchmarks..." -ForegroundColor Green
Write-Host ""

# Run benchmarks
Write-Host "1. Signing Performance Benchmarks" -ForegroundColor Cyan
go test -bench=BenchmarkSign -benchmem -benchtime=100x ./pkg/crypto 2>&1 | Select-String -Pattern "Benchmark|ns/op|B/op|allocs/op"

Write-Host ""
Write-Host "2. Optimized Signing Performance" -ForegroundColor Cyan
go test -bench=BenchmarkSignOptimized -benchmem -benchtime=100x ./pkg/crypto 2>&1 | Select-String -Pattern "Benchmark|ns/op|B/op|allocs/op"

Write-Host ""
Write-Host "3. Compressed Signing Performance" -ForegroundColor Cyan
go test -bench=BenchmarkSignCompressed -benchmem -benchtime=100x ./pkg/crypto 2>&1 | Select-String -Pattern "Benchmark|ns/op|B/op|allocs/op"

Write-Host ""
Write-Host "4. Verification Performance" -ForegroundColor Cyan
go test -bench=BenchmarkVerify -benchmem -benchtime=1000x ./pkg/crypto 2>&1 | Select-String -Pattern "Benchmark|ns/op|B/op|allocs/op"

Write-Host ""
Write-Host "5. Batch Signing Performance" -ForegroundColor Cyan
go test -bench=BenchmarkSignBatchOptimized -benchmem -benchtime=10x ./pkg/crypto 2>&1 | Select-String -Pattern "Benchmark|ns/op|B/op|allocs/op"

Write-Host ""
Write-Host "6. Compression Ratio Test" -ForegroundColor Cyan
go test -v -run=TestSignatureCompressionRatio ./pkg/crypto 2>&1 | Select-String -Pattern "PASS|FAIL|signature size|Compression|reduction"

Write-Host ""
Write-Host "7. Performance Comparison Test" -ForegroundColor Cyan
go test -v -run=TestSigningPerformanceComparison ./pkg/crypto 2>&1 | Select-String -Pattern "PASS|FAIL|signing|Performance|improvement"

Write-Host ""
Write-Host "8. Batch Signing Performance Test" -ForegroundColor Cyan
go test -v -run=TestBatchSigningPerformance ./pkg/crypto 2>&1 | Select-String -Pattern "PASS|FAIL|Batch size|avg"

Write-Host ""
Write-Host "=== Benchmark Complete ===" -ForegroundColor Green
Write-Host ""
Write-Host "For detailed results, run:" -ForegroundColor Gray
Write-Host "  go test -bench=. -benchmem ./pkg/crypto" -ForegroundColor Gray
Write-Host "  go test -v ./pkg/crypto" -ForegroundColor Gray

